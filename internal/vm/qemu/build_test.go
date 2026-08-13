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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildWindows drives the Windows install through the library API, not the
// CLI binary. Mirrors the proven TestWinPECDVisibility config (SCSI CDs,
// vioscsi drvload, BOOTAA64.EFI on the answer volume) and adds the SSH wait
// and bootstrap assertions from TestCellBuildWindows_QEMU.
//
// Flaky: the Windows CD bootloader (cdboot_noprompt.pdb) intermittently
// crashes with "Synchronous Exception" during TianoCore BDS on aarch64 TCG.
// Same argv boots fine on retry. Suspected QEMU TCG thread-scheduling race
// in EFI memory-map handling. No devcell-side suspects identified yet.
//
//	go test -run TestBuildWindows/tcg  -timeout 8h -v ./internal/vm/qemu/
//	go test -run TestBuildWindows/hvf  -timeout 8h -v ./internal/vm/qemu/
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

			// --- Answer volume ---
			cfg := DefaultAutounattendConfig()
			cfg.SSHPubKey = pubKey
			cfg.VirtIODrivers = NetKVMDriverPaths()
			cfg.EnableRDP = true

			drivers, err := LoadWinPEStorageDrivers(virtioISO)
			require.NoError(t, err, "extracting vioscsi drivers from virtio ISO")
			cfg.AnswerDrivers = drivers

			bootloader, err := InstallerBootloader(winISO)
			require.NoError(t, err, "extracting BOOTAA64.EFI from Windows ISO")
			blInfo, err := ValidateBootloaderPE(bootloader)
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

			// --- Spec (mirrors cmd/build_qemu.go buildSpec) ---
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
			spec.ApplyDefaults()
			require.NoError(t, spec.Validate())

			qmpSock := QMPSocketPath(spec)
			argv := BuildInstallCommand(spec, winISO, answerImg)
			argv[0] = qemuBin

			appendRunInfo(t, resultsDir, "test:  "+t.Name()+"\nargv:  "+strings.Join(argv, " ")+"\n")

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

			waitForSocket(t, qmpSock, 30*time.Second, resultsDir)
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

				// Screenshot
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

				// Block stats
				if stats, err := QMPBlockStats(qmpSock); err == nil {
					for _, s := range stats {
						pollRead += s.ReadBytes
					}
				}

				// Registers
				if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
					pollPC = ExtractRegister(regs, "PC=")
				}

				// Check if QEMU exited (reboot, crash, shutdown).
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

				// Serial watchers
				select {
				case reason := <-nshFailCh:
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("startup.nsh could not chainload BOOTAA64.EFI: %s", reason)
				default:
				}
				select {
				case reason := <-efiShellCh:
					t.Logf("EFI shell appeared — startup.nsh should recover: %s", reason)
					efiShellCh = nil // only log once
				default:
				}

				// SSH probe
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

			// --- Collect guest logs from answer volume ---
			dumpSerialLog(t, serialLog, resultsDir)

			for _, l := range CollectGuestLogs(answerImg) {
				if l.Err != nil {
					t.Logf("%s: %v", l.Name, l.Err)
					continue
				}
				writeArtifact(t, resultsDir, l.Name, string(l.Content))
				t.Logf("%s: %d bytes saved", l.Name, len(l.Content))
			}

			// --- Assertions (from TestCellBuildWindows_QEMU) ---
			if transcript, err := readGuestLog(answerImg, BootstrapLogName); err == nil {
				steps := ParseBootstrapSteps(transcript)
				t.Logf("bootstrap: %d ok, %d failed, %d unfinished", len(steps.OK), len(steps.Failed), len(steps.Unfinished))
				assert.Empty(t, steps.Failed, "bootstrap steps failed in the guest")
				assert.Empty(t, steps.Unfinished, "bootstrap steps started but never reported — the guest died mid-step")
				assert.True(t, steps.SSHReady(),
					"bootstrap never installed and started sshd; ok steps: %v", steps.OK)
			} else {
				t.Errorf("no bootstrap transcript on the answer volume: %v", err)
			}

			require.True(t, sshReady, "SSH never became available — install did not complete; artifacts in %s", resultsDir)
		})
	}
}

// probeSSH checks whether a TCP connection to host:port succeeds.
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
