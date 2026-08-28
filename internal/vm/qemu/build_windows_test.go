//go:build wimlib

package qemu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildWindows drives the full Windows build pipeline through library APIs:
//
//  1. Prep WIM — boot WinPE, run DISM offline servicing, verify devcell.wim
//     contents (Hyper-V binaries + registry, WSL2 feature, OpenSSH binaries,
//     VirtIO drivers in DriverStore)
//  2. Install — boot Windows installer with devcell.wim, wait for SSH,
//     assert bootstrap steps
//
// The WIM verification runs during the WinPE phase — before we commit to the
// multi-hour install — so a broken DISM pipeline fails in minutes, not hours.
//
//	go test -tags wimlib -run TestBuildWindows/tcg  -timeout 8h -v ./internal/vm/qemu/
//	go test -tags wimlib -run TestBuildWindows/hvf  -timeout 8h -v ./internal/vm/qemu/
func TestBuildWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("long: full unattended Windows install")
	}
	if os.Getenv("DEVCELL_TEST_INSTALL") == "" {
		t.Skip("set DEVCELL_TEST_INSTALL=1 to run the multi-hour unattended install")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	winISO := requireWindowsISO(t)
	virtioISO := requireVirtioISO(t)
	pwshFiles := requirePwshFiles(t)

	if fwData, err := os.ReadFile(fwPath); err == nil {
		h := sha256.Sum256(fwData)
		t.Logf("firmware: %s (%d bytes, sha256=%s)", fwPath, len(fwData), hex.EncodeToString(h[:8]))
	}

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}

			resultsDir := testResultsDir(t)
			tmpDir := t.TempDir()

			qemuAccel := "tcg,thread=multi"
			if accel == "hvf" {
				qemuAccel = "hvf"
			}

			// ══════════════════════════════════════════════════════════
			// Phase 1: Prep WIM — DISM offline servicing in WinPE
			// ══════════════════════════════════════════════════════════
			devcellWimPath := buildAndVerifyDevcellWim(t, qemuBin, fwPath, winISO, virtioISO, qemuAccel, tmpDir, resultsDir)

			// ══════════════════════════════════════════════════════════
			// Phase 2: Install Windows
			// ══════════════════════════════════════════════════════════

			// --- Disk ---
			diskPath := filepath.Join(tmpDir, "disk.qcow2")
			out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			if err != nil {
				out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			}
			require.NoError(t, err, "qemu-img create: %s", out)

			// --- Firmware vars ---
			varsPath := filepath.Join(tmpDir, "vars.fd")
			require.NoError(t, PrepareVarsFile(fwPath, varsPath))

			// --- SSH key ---
			sshKeyDir := filepath.Join(tmpDir, "ssh")
			require.NoError(t, os.MkdirAll(sshKeyDir, 0o700))
			privKey := filepath.Join(sshKeyDir, "id_ed25519")
			keygen := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", privKey)
			keyOut, err := keygen.CombinedOutput()
			require.NoError(t, err, "ssh-keygen: %s", keyOut)
			pubKeyBytes, err := os.ReadFile(privKey + ".pub")
			require.NoError(t, err)
			pubKey := strings.TrimSpace(string(pubKeyBytes))

			// --- devcell.wim volume ---
			// Package the patched devcell.wim as a FAT image Windows Setup
			// can read via <InstallFrom><Path>X:\devcell-install.wim</Path>.
			devcellWimData, err := os.ReadFile(devcellWimPath)
			require.NoError(t, err, "reading verified devcell.wim")
			devcellWimFiles := map[string][]byte{
				"/devcell-install.wim": devcellWimData,
			}
			devcellWimImg := filepath.Join(tmpDir, "devcell-wim.qcow2")
			require.NoError(t, CreateFATQcow2(devcellWimImg, devcellWimFiles, 20*1024*1024*1024))
			t.Logf("devcell WIM volume: %s (%.1f MB WIM inside)", devcellWimImg, float64(len(devcellWimData))/(1024*1024))

			// --- Answer volume ---
			cfg := DefaultAutounattendConfig()
			cfg.SSHPubKey = pubKey
			cfg.VirtIODrivers = NetKVMDriverPaths()
			cfg.EnableRDP = true
			cfg.InstallWimPath = `X:\devcell-install.wim`

			drivers, err := winpe.LoadWinPEStorageDrivers(virtioISO)
			require.NoError(t, err, "extracting vioscsi drivers from virtio ISO")
			cfg.AnswerDrivers = drivers

			bootloader, err := winpe.InstallerBootloader(winISO)
			require.NoError(t, err, "extracting BOOTAA64.EFI from Windows ISO")
			blInfo, err := winpe.ValidateBootloaderPE(bootloader)
			require.NoError(t, err, "validating BOOTAA64.EFI")
			cfg.EFIBootLoader = bootloader
			t.Logf("embedded BOOTAA64.EFI (%d bytes, arch=%s) on answer volume", blInfo.Size, blInfo.Arch)

			homeDir, err := os.UserHomeDir()
			require.NoError(t, err, "resolving home dir for OpenSSH cache")
			opensshPath, err := DownloadOpenSSH(t.Context(), homeDir, false, NopObserver{})
			require.NoError(t, err, "downloading OpenSSH payload")
			opensshData, err := os.ReadFile(opensshPath)
			require.NoError(t, err, "reading OpenSSH payload")
			cfg.OpenSSHPayload = OpenSSHPayloadName
			cfg.OpenSSHPayloadData = opensshData
			cfg.OpenSSHPayloadSize = len(opensshData)
			t.Logf("embedded OpenSSH payload (%d bytes) on answer volume", len(opensshData))

			answerImg := filepath.Join(tmpDir, "autounattend.img")
			require.NoError(t, BuildAnswerVolume(cfg, answerImg))

			// --- Budget ---
			var memoryGB uint64 = 4
			var sshDeadline = 45 * time.Minute
			var diskCacheMode string
			if accel == "tcg" {
				memoryGB = 8
				sshDeadline = 5 * time.Hour
				diskCacheMode = "unsafe"
				qemuAccel += ",tb-size=512"
			}

			// --- Spec ---
			sshPort := findFreePort(t)

			serialLog := filepath.Join(resultsDir, "serial.log")
			guestProgressLog := filepath.Join(resultsDir, "guest-progress.log")
			spec := Spec{
				VMName:               "build-windows-test",
				CPUs:                 4,
				MemoryGB:             memoryGB,
				DiskCacheMode:        diskCacheMode,
				DiskPath:             diskPath,
				FirmwarePath:         fwPath,
				VarsPath:             varsPath,
				SerialLogPath:        serialLog,
				GuestProgressLogPath: guestProgressLog,
				NestedVirt:           true,
				VirtioISO:            virtioISO,
				CDBus:                "scsi",
				SSHPort:              sshPort,
				SSHHost:              "127.0.0.1",
				SSHUser:              SessionUsername(),
				SSHKeyPath:           privKey,
				MACAddr:              DeterministicMAC("build-test-" + accel),
				DisplayType:          "none",
				QMPSocketDir:         tmpDir,
				Accel:                qemuAccel,
				NoReboot:             false,
			}
			spec.DevcellWimImg = devcellWimImg
			spec.ApplyDefaults()
			require.NoError(t, spec.Validate())

			qmpSock := QMPSocketPath(spec)
			argv := BuildInstallCommand(spec, winISO, answerImg)
			argv[0] = qemuBin

			t.Logf("install command: %s", strings.Join(argv, " "))
			updateRunJSON(t, resultsDir, map[string]any{
				"test": t.Name(), "qemu-args": strings.Join(argv, " "),
			})

			// --- Launch QEMU ---
			require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

			exclusiveQEMU(t)
			cmd := exec.Command(argv[0], argv[1:]...)
			qemuLog := qemuOutput(t, resultsDir, argv)
			cmd.Stdout = qemuLog
			cmd.Stderr = qemuLog
			require.NoError(t, cmd.Start(), "starting QEMU")
			qemuDone := make(chan error, 1)
			go func() { qemuDone <- cmd.Wait() }()
			defer func() {
				cmd.Process.Kill()
				<-qemuDone
			}()

			waitForSocket(t, qmpSock, 30*time.Second, qemuLog)
			assertAccel(t, qmpSock, accel, resultsDir)

			if qtree, err := QMPHumanMonitor(qmpSock, "info qtree"); err == nil {
				os.WriteFile(filepath.Join(resultsDir, "qtree.txt"), []byte(qtree), 0o644)
			}

			// --- Serial log watchers ---
			stop := make(chan struct{})
			defer close(stop)
			efiShellCh := WatchSerialForEFIShell(serialLog, stop)
			nshFailCh := WatchSerialForStartupNSHFail(serialLog, stop)

			// --- Poll loop: screenshots + stall detection + SSH probe ---
			const (
				pollInterval = 15 * time.Second
				stallBudget  = 10 * time.Minute
			)
			stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
			var stall StallTracker

			ppmPath := filepath.Join(tmpDir, "screen.ppm")
			start := time.Now()
			frame := 0
			sshReady := false

			for time.Since(start) < sshDeadline {
				time.Sleep(pollInterval)
				frame++

				var pollHash uint64
				var pollRead int64
				var pollPC string

				os.Remove(ppmPath)
				if err := QMPScreendump(qmpSock, ppmPath); err == nil {
					if ppmData, err := os.ReadFile(ppmPath); err == nil {
						h := fnv.New64a()
						h.Write(ppmData)
						pollHash = h.Sum64()
					}
					pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
						"none", frame, frame, "png")
					if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
						t.Logf("[frame %d] saved %s", frame, filepath.Base(pngPath))
					}
				}

				if stats, err := QMPBlockStats(qmpSock); err == nil {
					for _, s := range stats {
						pollRead += s.ReadBytes
					}
				}

				if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
					pollPC = ExtractRegister(regs, "PC=")
				}

				select {
				case err := <-qemuDone:
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("QEMU exited unexpectedly after %s (frame %d): %v",
						time.Since(start).Round(time.Second), frame, err)
				default:
				}

				n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
				t.Logf("[frame %d] hash=%016x rd=%d PC=%s stall=%d/%d elapsed=%s",
					frame, pollHash, pollRead, pollPC, n, stallLimit, time.Since(start).Round(time.Second))

				if stall.Stalled(stallLimit) {
					if _, err := os.Stat(ppmPath); err == nil {
						ConvertPPMtoPNG(ppmPath, filepath.Join(resultsDir, "stalled-last.png"))
					}
					dumpStallDiagnostics(t, qmpSock, resultsDir, "")
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("guest stalled: screen and disk IO unchanged for %d consecutive polls (%v)",
						stall.Consecutive(), time.Duration(stall.Consecutive())*pollInterval)
				}

				select {
				case reason := <-nshFailCh:
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("startup.nsh could not chainload BOOTAA64.EFI: %s", reason)
				default:
				}
				select {
				case reason := <-efiShellCh:
					t.Logf("EFI shell appeared — startup.nsh should recover: %s", reason)
					efiShellCh = nil
				default:
				}

				if probeSSH(spec.SSHHost, spec.SSHPort) {
					t.Logf("SSH ready after %s (%d frames)", time.Since(start).Round(time.Second), frame)
					sshReady = true
					break
				}
			}

			// --- Final screenshot ---
			os.Remove(ppmPath)
			if err := QMPScreendump(qmpSock, ppmPath); err == nil {
				frame++
				pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
					"final", frame, frame, "png")
				ConvertPPMtoPNG(ppmPath, pngPath)
			}

			// --- Collect guest logs ---
			dumpSerialLog(t, serialLog, resultsDir)

			for _, l := range winpe.CollectGuestLogs(answerImg) {
				if l.Err != nil {
					t.Logf("%s: %v", l.Name, l.Err)
					continue
				}
				writeArtifact(t, resultsDir, l.Name, string(l.Content))
				t.Logf("%s: %d bytes saved", l.Name, len(l.Content))
			}

			// --- Bootstrap assertions ---
			if transcript, err := readGuestLog(answerImg, BootstrapLogName); err == nil {
				steps := winpe.ParseBootstrapSteps(transcript)
				t.Logf("bootstrap: %d ok, %d failed, %d unfinished", len(steps.OK), len(steps.Failed), len(steps.Unfinished))
				assert.Empty(t, steps.Failed, "bootstrap steps failed in the guest")
				assert.Empty(t, steps.Unfinished, "bootstrap steps started but never reported — the guest died mid-step")
				assert.True(t, steps.SSHReady(),
					"bootstrap never installed and started sshd; ok steps: %v", steps.OK)
			} else {
				t.Errorf("no bootstrap transcript on the answer volume: %v", err)
			}

			require.True(t, sshReady, "SSH never became available — install did not complete; artifacts in %s", resultsDir)

			// --- Post-SSH: Hyper-V installed assertion ---
			hypervScript := `
$feature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V
if ($feature.State -ne 'Enabled') {
    Write-Output "FAIL: Hyper-V state is $($feature.State)"
    exit 1
}
Write-Output "OK: Hyper-V state is Enabled"

$svc = Get-Service vmcompute -ErrorAction SilentlyContinue
if (-not $svc) {
    Write-Output "FAIL: vmcompute service not found"
    exit 1
}
Write-Output "OK: vmcompute service exists (status: $($svc.Status))"
`
			hypervArgv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, spec.SSHUser, privKey, PowerShellEncodedCommand(hypervScript))
			t.Logf("Hyper-V check: %s", strings.Join(hypervArgv, " "))
			hypervOut, hypervErr := exec.Command(hypervArgv[0], hypervArgv[1:]...).CombinedOutput()
			t.Logf("Hyper-V check output:\n%s", hypervOut)
			assert.NoError(t, hypervErr, "Hyper-V post-install check failed")
			assert.Contains(t, string(hypervOut), "OK: Hyper-V state is Enabled")
			assert.Contains(t, string(hypervOut), "OK: vmcompute service exists")
		})
	}
}

// buildAndVerifyDevcellWim boots a WinPE builder VM, runs DISM offline
// servicing against boot.wim, and verifies the output devcell.wim contains
// Hyper-V, WSL2, OpenSSH, and VirtIO drivers — all before the install phase
// starts. Returns the path to the verified devcell.wim.
func buildAndVerifyDevcellWim(t *testing.T, qemuBin, fwPath, winISO, virtioISO, accel, tmpDir, resultsDir string) string {
	t.Helper()
	wimDir := filepath.Join(tmpDir, "wim-builder")
	require.NoError(t, os.MkdirAll(wimDir, 0755))

	// ── 1. Extract boot.wim and EFI boot files ──
	stageDir := filepath.Join(wimDir, "stage")
	require.NoError(t, ExtractWinPEStage(winISO, stageDir))

	// ── 2. Extract vioserial drivers ──
	vioserialDrivers, err := winpe.LoadWinPEVioserialDrivers(virtioISO)
	require.NoError(t, err)

	// ── 3. Create shared FAT volume with boot.wim + builder script ──
	bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
	bootWimData, err := os.ReadFile(bootWimPath)
	require.NoError(t, err)
	t.Logf("boot.wim: %d bytes (%.1f MB)", len(bootWimData), float64(len(bootWimData))/(1024*1024))

	var ops []WimPrepOp
	ops = append(ops, HyperVPrepOps()...)
	ops = append(ops, WSL2PrepOps()...)
	ops = append(ops, OpenSSHPrepOps()...)
	ops = append(ops, VirtIODriverPrepOps()...)
	prepCfg := WimPrepConfig{Ops: ops}
	var efiBootLoader []byte
	if bl, err := winpe.InstallerBootloader(winISO); err == nil {
		if _, err := winpe.ValidateBootloaderPE(bl); err == nil {
			efiBootLoader = bl
			t.Logf("BOOTAA64.EFI: %d bytes", len(bl))
		}
	}
	sharedFiles := winpe.SharedVolumeFiles(prepCfg, efiBootLoader, pwshFiles)
	sharedFiles["/boot.wim"] = bootWimData

	sharedImg := filepath.Join(wimDir, "shared.qcow2")
	require.NoError(t, CreateFATQcow2(sharedImg, sharedFiles, 20*1024*1024*1024))
	t.Logf("shared volume: %s", sharedImg)

	// ── 4. Inject agent into boot.wim ──
	injectDir := filepath.Join(wimDir, "inject")
	require.NoError(t, os.MkdirAll(injectDir, 0755))

	for answerPath, data := range vioserialDrivers {
		hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0755))
		require.NoError(t, os.WriteFile(hostPath, data, 0644))
	}

	payloadCfg := WinPEPayloadConfig{
		WPEInit:      true,
		ProgressPort: `\\.\Global\` + ProgressPortName,
		PollSeconds:  5,
		SyncAgent:    true,
	}
	if len(vioserialDrivers) > 0 {
		payloadCfg.DriverINFs = []string{`X:\devcell\drivers\vioserial\vioser.inf`}
	}

	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "winpeshl.ini"),
		GenerateWinPEShellINI_NoSetup(), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "bootstrap.cmd"),
		GenerateWinPEBootstrapCmd(), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "bootstrap.ps1"),
		GenerateWinPEBootstrap(payloadCfg), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "agent.ps1"),
		GenerateWinPEAgent(payloadCfg), 0644))

	require.NoError(t, InjectWinPEPayload(bootWimPath, injectDir))

	// ── 5. Create WinPE ISO ──
	winpeISO := filepath.Join(wimDir, "winpe-builder.iso")
	require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))

	// ── 6. Build QEMU command ──
	scratchDisk := filepath.Join(wimDir, "scratch.qcow2")
	out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", scratchDisk, "4G").CombinedOutput()
	if err != nil {
		out, err = exec.Command("qemu-img", "create", "-f", "qcow2", scratchDisk, "4G").CombinedOutput()
	}
	require.NoError(t, err, "qemu-img create: %s", out)

	fwInfo, err := os.Stat(fwPath)
	require.NoError(t, err)
	kernelMode := fwInfo.Size() < 64*1024*1024

	var varsPath string
	if !kernelMode {
		varsPath = filepath.Join(wimDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))
	}

	wimSerialLog := filepath.Join(resultsDir, "wim-serial.log")
	wimProgressLog := filepath.Join(resultsDir, "wim-guest-progress.log")
	spec := Spec{
		VMName:               "wim-builder-test",
		CPUs:                 4,
		MemoryGB:             4,
		DiskPath:             scratchDisk,
		FirmwarePath:         fwPath,
		VarsPath:             varsPath,
		FirmwareKernel:       kernelMode,
		QMPSocketDir:         wimDir,
		DisplayType:          "none",
		Accel:                accel,
		MachineType:          "virt",
		SerialLogPath:        wimSerialLog,
		GuestProgressLogPath: wimProgressLog,
		NoReboot:             true,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)

	wbs := WimBuilderSpec{
		Spec:       spec,
		WinPEISO:   winpeISO,
		SharedImg:  sharedImg,
		WindowsISO: winISO,
		VirtIOISO:  virtioISO,
	}
	argv := BuildWimBuilderArgv(wbs)
	argv[0] = qemuBin
	updateRunJSON(t, resultsDir, map[string]any{
		"test": t.Name(), "qemu-args": strings.Join(argv, " "),
	})

	require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

	// ── 7. Boot and poll ──
	exclusiveQEMU(t)
	cmd := exec.Command(argv[0], argv[1:]...)
	wimLog := qemuOutput(t, resultsDir, argv)
	cmd.Stdout = wimLog
	cmd.Stderr = wimLog
	require.NoError(t, cmd.Start(), "starting WIM builder QEMU")
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	waitForSocket(t, qmpSock, 30*time.Second, wimLog)
	assertAccel(t, qmpSock, strings.SplitN(accel, ",", 2)[0], resultsDir)

	stop := make(chan struct{})
	defer close(stop)
	efiShellCh := WatchSerialForEFIShell(wimSerialLog, stop)
	syncExCh := WatchSerialForSyncException(wimSerialLog, stop)

	const (
		wimOverallDeadline = 15 * time.Minute
		wimPollInterval    = 15 * time.Second
		wimStallBudget     = 90 * time.Second
	)
	wimStallLimit := StallPollsFor(int(wimStallBudget.Seconds()), int(wimPollInterval.Seconds()))
	var wimStall StallTracker

	ppmPath := filepath.Join(wimDir, "screen.ppm")
	start := time.Now()
	frame := 0
	for time.Since(start) < wimOverallDeadline {
		time.Sleep(wimPollInterval)
		frame++

		var pollHash uint64
		var pollRead int64
		var pollPC string

		os.Remove(ppmPath)
		if err := QMPScreendump(qmpSock, ppmPath); err == nil {
			if ppmData, err := os.ReadFile(ppmPath); err == nil {
				h := fnv.New64a()
				h.Write(ppmData)
				pollHash = h.Sum64()
			}
			pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
				"none", frame, frame, "png")
			if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
				t.Logf("[wim frame %d] saved %s", frame, filepath.Base(pngPath))
			}
		}

		if stats, err := QMPBlockStats(qmpSock); err == nil {
			for _, s := range stats {
				pollRead += s.ReadBytes
			}
		}
		if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
			pollPC = ExtractRegister(regs, "PC=")
		}

		n := wimStall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
		t.Logf("[wim frame %d] hash=%016x rd=%d PC=%s stall=%d/%d",
			frame, pollHash, pollRead, pollPC, n, wimStallLimit)

		if wimStall.Stalled(wimStallLimit) {
			dumpSerialLog(t, wimSerialLog, resultsDir)
			t.Fatalf("WIM builder stalled: screen, disk IO, and PC unchanged for %d consecutive polls",
				wimStall.Consecutive())
		}

		select {
		case reason := <-syncExCh:
			dumpSerialLog(t, wimSerialLog, resultsDir)
			t.Fatalf("Synchronous Exception during WIM builder: %s", reason)
		case reason := <-efiShellCh:
			dumpSerialLog(t, wimSerialLog, resultsDir)
			t.Fatalf("firmware dropped to EFI shell during WIM builder: %s", reason)
		default:
		}

		doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)
		if doneMarker != "" {
			t.Logf("WIM builder done: %q (after %s, %d frames)",
				strings.TrimSpace(doneMarker), time.Since(start).Round(time.Second), frame)
			break
		}
	}

	// ── 8. Capture results ──
	cmd.Process.Kill()
	cmd.Wait()

	agentOut := readAnswerVolumeFile(t, sharedImg, "/"+AgentResultFile)
	t.Logf("=== WIM builder output ===\n%s", agentOut)
	os.WriteFile(filepath.Join(resultsDir, "wim-builder-output.txt"), []byte(agentOut), 0644)
	dumpSerialLog(t, wimSerialLog, resultsDir)

	// ══════════════════════════════════════════════════════════
	// WIM builder assertions
	// ══════════════════════════════════════════════════════════

	doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)
	require.NotEmpty(t, doneMarker, "WIM builder never completed")

	assert.Contains(t, agentOut, "DEVCELL WIM BUILDER",
		"builder script header must appear in output")
	assert.Contains(t, agentOut, "Found Windows ISO",
		"builder must find install.wim on the Windows ISO drive")
	assert.Contains(t, agentOut, "Found virtio-win ISO",
		"builder must find virtio-win drivers ISO")
	assert.Contains(t, agentOut, "Mounting boot.wim",
		"builder must attempt to mount boot.wim")

	result := strings.TrimSpace(doneMarker)
	t.Logf("WIM builder result: %s", result)
	require.Equal(t, "SUCCESS", result,
		"DISM offline servicing must succeed in WinPE — cannot install from a broken devcell.wim")

	assert.Contains(t, agentOut, "boot.wim committed successfully")
	assert.Contains(t, agentOut, "devcell.wim created")

	// ── 9. Verify devcell.wim contents with wimlib ──
	devcellWimData, err := ReadFileFromFATQcow2(sharedImg, "/devcell.wim")
	require.NoError(t, err, "reading devcell.wim from shared volume")
	require.NotEmpty(t, devcellWimData, "devcell.wim is empty")
	t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))

	devcellWimPath := filepath.Join(resultsDir, "devcell.wim")
	require.NoError(t, os.WriteFile(devcellWimPath, devcellWimData, 0644))

	wim, err := wimlib.OpenWIM(devcellWimPath)
	require.NoError(t, err, "opening devcell.wim")
	defer wim.Close()

	count, err := wim.ImageCount()
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2, "devcell.wim must still have at least 2 images")

	extractDir := filepath.Join(wimDir, "devcell-extracted")
	require.NoError(t, os.MkdirAll(extractDir, 0755))
	require.NoError(t, wim.ExtractImage(2, extractDir, nil))

	// ── 9a. Hyper-V: DISM output, binaries, and registry ──
	assert.Contains(t, agentOut, "OK: Enable-Feature Microsoft-Hyper-V",
		"DISM must report Hyper-V feature enabled")
	assert.Contains(t, agentOut, "OK: Enable-Feature VirtualMachinePlatform",
		"DISM must report VirtualMachinePlatform feature enabled")

	for _, f := range []string{
		"Windows/System32/vmms.exe",
		"Windows/System32/vmwp.exe",
		"Windows/System32/vmcompute.exe",
		"Windows/System32/drivers/Vid.sys",
		"Windows/System32/drivers/vmswitch.sys",
		"Windows/System32/drivers/storvsp.sys",
	} {
		fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
		if info, err := os.Stat(fullPath); err == nil {
			t.Logf("  Hyper-V OK: %s (%d bytes)", f, info.Size())
		} else {
			t.Errorf("  Hyper-V MISSING: %s", f)
		}
	}

	// ── 9b. WSL2 ──
	assert.Contains(t, agentOut, "OK: Enable-Feature Microsoft-Windows-Subsystem-Linux",
		"DISM must report WSL feature enabled")

	// ── 9c. OpenSSH ──
	for _, f := range []string{
		"Windows/System32/OpenSSH/sshd.exe",
		"Windows/System32/OpenSSH/ssh.exe",
		"Windows/System32/OpenSSH/ssh-keygen.exe",
	} {
		fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
		if info, err := os.Stat(fullPath); err == nil {
			t.Logf("  OpenSSH OK: %s (%d bytes)", f, info.Size())
		} else {
			t.Errorf("  OpenSSH MISSING: %s", f)
		}
	}

	// ── 9d. VirtIO drivers ──
	assert.Contains(t, agentOut, `OK: Add-Driver NetKVM\w11\ARM64`,
		"DISM must report NetKVM driver added")
	assert.Contains(t, agentOut, `OK: Add-Driver vioserial\w11\ARM64`,
		"DISM must report vioserial driver added")
	assert.Contains(t, agentOut, `OK: Add-Driver vioscsi\w11\ARM64`,
		"DISM must report vioscsi driver added")

	driverStoreDir := filepath.Join(extractDir, "Windows", "System32", "DriverStore", "FileRepository")
	for _, drv := range []struct {
		name string
		sys  string
	}{
		{"NetKVM", "netkvm.sys"},
		{"vioserial", "vioser.sys"},
		{"vioscsi", "vioscsi.sys"},
	} {
		matches, _ := filepath.Glob(filepath.Join(driverStoreDir, "*", drv.sys))
		if len(matches) > 0 {
			info, _ := os.Stat(matches[0])
			t.Logf("  VirtIO OK: %s → %s (%d bytes)", drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
		} else {
			t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)", drv.name, drv.sys)
		}
	}

	// ── 10. Apply registry patches, then verify ──
	// PatchDevcellWim sets correct Start values for Hyper-V services —
	// DISM creates them with Start=3/4, the patch sets Start=0 (boot).
	// Verify AFTER patching: that's the devcell.wim Windows boots from.
	wim.Close()
	if err := PatchDevcellWim(devcellWimPath, 2, HyperVBootPatches()); err != nil {
		t.Logf("post-DISM registry patching failed: %v", err)
	} else {
		t.Log("  Hyper-V boot patches applied to devcell.wim")
	}

	wim2, err := wimlib.OpenWIM(devcellWimPath)
	require.NoError(t, err, "reopening devcell.wim after patching")
	defer wim2.Close()

	if err := VerifyWimRegistry(wim2, 2, `\Windows\System32\config\SYSTEM`, HyperVBootChecks()); err != nil {
		t.Errorf("Hyper-V registry verification failed (after patching): %v", err)
	} else {
		t.Log("  Hyper-V registry boot patches verified")
	}

	t.Logf("WIM builder phase complete — devcell.wim verified at %s", devcellWimPath)
	return devcellWimPath
}

func findFreePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return uint16(port)
}

// probeSSH checks whether an SSH server is listening: connects and reads the
// banner line (e.g. "SSH-2.0-OpenSSH_9.8"). A bare TCP accept (QEMU's
// hostfwd) is not enough — the guest's sshd must be answering.
func probeSSH(host string, port uint16) bool {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).Dial("tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || n < 4 {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "SSH-")
}
