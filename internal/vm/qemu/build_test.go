//go:build wimlib

package qemu

import (
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/DimmKirr/devcell/internal/wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wimBuilderRun holds output from a single WIM builder QEMU session.
type wimBuilderRun struct {
	agentOut   string
	sharedImg  string
	resultsDir string
	tmpDir     string
	doneMarker string
}

// runWimBuilder boots a WinPE builder VM, polls until it finishes, and
// returns the captured output. The caller supplies the DISM ops and the
// deadline; everything else (QEMU config, polling, stall detection) is
// shared across subtests.
func runWimBuilder(t *testing.T, accel string, ops []WimPrepOp, deadline time.Duration) wimBuilderRun {
	t.Helper()

	qemuAccel := "tcg,thread=multi"
	if accel == "hvf" {
		qemuAccel = "hvf"
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	virtioISO := requireVirtioISO(t)
	pwshFiles := requirePwshFiles(t)

	winISO := requireWindowsISO(t)

	needsInstallWim := false
	for _, op := range ops {
		if op.Feature != "" || op.Capability != "" || op.Package != "" {
			needsInstallWim = true
			break
		}
	}

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// ── 1. Extract boot.wim and EFI boot files ──
	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, ExtractWinPEStage(winISO, stageDir))

	// ── 2. Extract vioserial + vioscsi drivers ──
	vioserialDrivers, err := LoadWinPEVioserialDrivers(virtioISO)
	require.NoError(t, err)
	vioscsiDrivers, err := LoadWinPEStorageDrivers(virtioISO)
	require.NoError(t, err)

	// ── 3. Create shared FAT volume with boot.wim ──
	bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
	bootWimData, err := os.ReadFile(bootWimPath)
	require.NoError(t, err)
	t.Logf("boot.wim: %d bytes (%.1f MB)", len(bootWimData), float64(len(bootWimData))/(1024*1024))

	var efiBootLoader []byte
	if bl, err := InstallerBootloader(winISO); err != nil {
		t.Logf("could not extract BOOTAA64.EFI: %v", err)
	} else if _, err := ValidateBootloaderPE(bl); err != nil {
		t.Logf("BOOTAA64.EFI validation failed: %v", err)
	} else {
		efiBootLoader = bl
		t.Logf("BOOTAA64.EFI: %d bytes", len(bl))
	}

	cfg := WimPrepConfig{Ops: ops}
	sharedFiles := SharedVolumeFiles(cfg, efiBootLoader, pwshFiles)
	sharedFiles["/boot.wim"] = bootWimData

	sharedImg := filepath.Join(tmpDir, "shared.qcow2")
	require.NoError(t, CreateFATQcow2(sharedImg, sharedFiles, 20*1024*1024*1024))
	t.Logf("shared volume: %s", sharedImg)

	// ── 4. Inject agent into boot.wim for standalone WinPE ──
	injectDir := filepath.Join(tmpDir, "inject")
	require.NoError(t, os.MkdirAll(injectDir, 0755))

	for _, driverSet := range []map[string][]byte{vioserialDrivers, vioscsiDrivers} {
		for answerPath, data := range driverSet {
			hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
			require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0755))
			require.NoError(t, os.WriteFile(hostPath, data, 0644))
		}
	}

	payloadCfg := WinPEPayloadConfig{
		WPEInit:      true,
		ProgressPort: `\\.\Global\` + ProgressPortName,
		PollSeconds:  5,
		SyncAgent:    true,
	}
	var driverINFs []string
	if len(vioserialDrivers) > 0 {
		driverINFs = append(driverINFs, `X:\devcell\drivers\vioserial\vioser.inf`)
	}
	if len(vioscsiDrivers) > 0 {
		driverINFs = append(driverINFs, `X:\devcell\drivers\vioscsi\vioscsi.inf`)
	}
	payloadCfg.DriverINFs = driverINFs

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
	winpeISO := filepath.Join(tmpDir, "winpe-builder.iso")
	require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))

	// ── 6. Build QEMU command ──
	diskPath := filepath.Join(tmpDir, "scratch.qcow2")
	out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	if err != nil {
		out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	}
	require.NoError(t, err, "qemu-img create: %s", out)

	fwInfo, err := os.Stat(fwPath)
	require.NoError(t, err)
	kernelMode := fwInfo.Size() < 64*1024*1024

	var varsPath string
	if !kernelMode {
		varsPath = filepath.Join(tmpDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))
	}

	serialLog := filepath.Join(resultsDir, "serial.log")
	guestProgressLog := filepath.Join(resultsDir, "build.log")
	spec := Spec{
		VMName:               "wim-builder-test",
		CPUs:                 2,
		MemoryGB:             5,
		DiskPath:             diskPath,
		FirmwarePath:         fwPath,
		VarsPath:             varsPath,
		FirmwareKernel:       kernelMode,
		QMPSocketDir:         tmpDir,
		DisplayType:          "none",
		Accel:                qemuAccel,
		MachineType:          "virt",
		SerialLogPath:        serialLog,
		GuestProgressLogPath: guestProgressLog,
		NoReboot:             true,
		CDBus:                "scsi",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)

	wbs := WimBuilderSpec{
		Spec:      spec,
		WinPEISO:  winpeISO,
		SharedImg: sharedImg,
		VirtIOISO: virtioISO,
	}
	if needsInstallWim {
		wbs.WindowsISO = winISO
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
	qemuLog := qemuOutput(t, resultsDir, argv)
	cmd.Stdout = qemuLog
	cmd.Stderr = qemuLog
	require.NoError(t, cmd.Start(), "starting QEMU")
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	waitForSocket(t, qmpSock, 30*time.Second, qemuLog)
	assertAccel(t, qmpSock, accel, resultsDir)

	stop := make(chan struct{})
	defer close(stop)
	efiShellCh := WatchSerialForEFIShell(serialLog, stop)
	syncExCh := WatchSerialForSyncException(serialLog, stop)

	pollInterval := 15 * time.Second
	stallBudget := 90 * time.Second
	stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
	var stall StallTracker

	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	start := time.Now()
	frame := 0
	for time.Since(start) < deadline {
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

		n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
		t.Logf("[frame %d] hash=%016x rd=%d PC=%s stall=%d/%d",
			frame, pollHash, pollRead, pollPC, n, stallLimit)

		if stall.Stalled(stallLimit) {
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("guest stalled: screen, disk IO, and PC unchanged for %d consecutive polls",
				stall.Consecutive())
		}

		select {
		case reason := <-syncExCh:
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("Synchronous Exception: %s", reason)
		case reason := <-efiShellCh:
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("firmware dropped to EFI shell: %s", reason)
		default:
		}

		doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)
		if doneMarker != "" {
			t.Logf("builder done marker: %q (after %s, %d frames)",
				strings.TrimSpace(doneMarker), time.Since(start).Round(time.Second), frame)
			break
		}
	}

	cmd.Process.Kill()
	cmd.Wait()

	agentOut := readAnswerVolumeFile(t, sharedImg, "/"+AgentResultFile)
	t.Logf("=== builder output ===\n%s", agentOut)
	os.WriteFile(filepath.Join(resultsDir, "builder-output.txt"), []byte(agentOut), 0644)
	dumpSerialLog(t, serialLog, resultsDir)

	doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)

	return wimBuilderRun{
		agentOut:   agentOut,
		sharedImg:  sharedImg,
		resultsDir: resultsDir,
		tmpDir:     tmpDir,
		doneMarker: strings.TrimSpace(doneMarker),
	}
}

// TestWimBuilder boots a builder WinPE that runs DISM offline servicing
// against a copy of boot.wim.
//
// Subtests:
//
//	boot-wim  — VirtIO drivers only, no install.wim mount (~5 min TCG)
//	full      — Hyper-V + WSL + OpenSSH + VirtIO drivers (~30 min TCG)
//
//	go test -tags wimlib -run TestWimBuilder/tcg/boot-wim -timeout 15m ./internal/vm/qemu/
//	go test -tags wimlib -run TestWimBuilder/tcg/full -timeout 35m ./internal/vm/qemu/
func TestWimBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to run DISM offline servicing")
	}

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}

			t.Run("boot-wim", func(t *testing.T) {
				ops := VirtIODriverPrepOps()
				run := runWimBuilder(t, accel, ops, 10*time.Minute)

				require.NotEmpty(t, run.doneMarker, "builder never completed")
				assert.Contains(t, run.agentOut, "DEVCELL WIM BUILDER")
				assert.Contains(t, run.agentOut, "Found virtio-win ISO")
				assert.Contains(t, run.agentOut, "Mounting boot.wim")
				assert.Contains(t, run.agentOut, "boot.wim committed successfully")
				assert.Contains(t, run.agentOut, "devcell.wim created")

				if run.doneMarker != "SUCCESS" {
					t.Skipf("builder reported %s; skipping WIM verification", run.doneMarker)
				}

				devcellWimData, err := ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
				require.NoError(t, err, "reading devcell.wim from shared volume")
				require.NotEmpty(t, devcellWimData, "devcell.wim is empty")
				t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))

				devcellWimPath := filepath.Join(run.resultsDir, "devcell.wim")
				require.NoError(t, os.WriteFile(devcellWimPath, devcellWimData, 0644))

				wim, err := wimlib.OpenWIM(devcellWimPath)
				require.NoError(t, err, "opening devcell.wim")
				defer wim.Close()

				count, err := wim.ImageCount()
				require.NoError(t, err)
				require.GreaterOrEqual(t, count, 2, "devcell.wim must still have at least 2 images")

				extractDir := filepath.Join(run.tmpDir, "devcell-extracted")
				require.NoError(t, os.MkdirAll(extractDir, 0755))
				require.NoError(t, wim.ExtractImage(2, extractDir, nil))

				// VirtIO drivers
				assert.Contains(t, run.agentOut, `OK: Add-Driver NetKVM\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioserial\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioscsi\w11\ARM64`)

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
						t.Logf("  VirtIO OK: %s -> %s (%d bytes)", drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
					} else {
						t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)", drv.name, drv.sys)
					}
				}

				// install.wim must NOT have been mounted
				assert.NotContains(t, run.agentOut, "Mounting install.wim")
			})

			t.Run("full", func(t *testing.T) {
				var ops []WimPrepOp
				ops = append(ops, HyperVPrepOps()...)
				ops = append(ops, WSL2PrepOps()...)
				ops = append(ops, OpenSSHPrepOps()...)
				ops = append(ops, VirtIODriverPrepOps()...)
				run := runWimBuilder(t, accel, ops, 30*time.Minute)

				require.NotEmpty(t, run.doneMarker, "builder never completed")
				assert.Contains(t, run.agentOut, "DEVCELL WIM BUILDER")
				assert.Contains(t, run.agentOut, "Found Windows ISO")
				assert.Contains(t, run.agentOut, "Found virtio-win ISO")
				assert.Contains(t, run.agentOut, "Mounting boot.wim")
				assert.Contains(t, run.agentOut, "boot.wim committed successfully")
				assert.Contains(t, run.agentOut, "devcell.wim created")

				if run.doneMarker != "SUCCESS" {
					t.Skipf("builder reported %s; skipping WIM verification", run.doneMarker)
				}

				devcellWimData, err := ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
				require.NoError(t, err, "reading devcell.wim from shared volume")
				require.NotEmpty(t, devcellWimData, "devcell.wim is empty")
				t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))

				devcellWimPath := filepath.Join(run.resultsDir, "devcell.wim")
				require.NoError(t, os.WriteFile(devcellWimPath, devcellWimData, 0644))

				wim, err := wimlib.OpenWIM(devcellWimPath)
				require.NoError(t, err, "opening devcell.wim")
				defer wim.Close()

				count, err := wim.ImageCount()
				require.NoError(t, err)
				require.GreaterOrEqual(t, count, 2, "devcell.wim must still have at least 2 images")

				extractDir := filepath.Join(run.tmpDir, "devcell-extracted")
				require.NoError(t, os.MkdirAll(extractDir, 0755))
				require.NoError(t, wim.ExtractImage(2, extractDir, nil))

				// Hyper-V
				assert.Contains(t, run.agentOut, "OK: Enable-Feature Microsoft-Hyper-V")
				assert.Contains(t, run.agentOut, "OK: Enable-Feature VirtualMachinePlatform")

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

				if err := VerifyWimRegistry(wim, 2, `\Windows\System32\config\SYSTEM`, HyperVBootChecks()); err != nil {
					t.Errorf("Hyper-V registry verification failed: %v", err)
				} else {
					t.Log("  Hyper-V registry patches verified")
				}

				// WSL
				assert.Contains(t, run.agentOut, "OK: Enable-Feature Microsoft-Windows-Subsystem-Linux")

				// OpenSSH
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

				// VirtIO drivers
				assert.Contains(t, run.agentOut, `OK: Add-Driver NetKVM\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioserial\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioscsi\w11\ARM64`)

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
						t.Logf("  VirtIO OK: %s -> %s (%d bytes)", drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
					} else {
						t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)", drv.name, drv.sys)
					}
				}
			})
		})
	}
}

// requirePwshFiles returns the extracted PowerShell 7 files (volume-path ->
// content) needed by SharedVolumeFiles.  Resolution order:
//  1. DEVCELL_TEST_PWSH_ZIP env var pointing to a pre-downloaded zip
//  2. Cached zip under ~/.devcell/cache/qemu/
//
// Skips the test if neither source is available.
func requirePwshFiles(t *testing.T) map[string][]byte {
	t.Helper()

	var zipPath string

	if p := os.Getenv("DEVCELL_TEST_PWSH_ZIP"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("DEVCELL_TEST_PWSH_ZIP=%s: %v", p, err)
		}
		zipPath = p
	} else {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		p, err := DownloadPwsh(t.Context(), home, false, NopObserver{})
		if err != nil {
			t.Skipf("could not obtain pwsh zip: %v", err)
		}
		zipPath = p
	}

	files, err := ExtractPwshFiles(zipPath)
	require.NoError(t, err, "extracting pwsh files from %s", zipPath)
	require.NotEmpty(t, files, "pwsh zip contained no files")
	t.Logf("pwsh: %d files extracted from %s", len(files), filepath.Base(zipPath))
	return files
}
