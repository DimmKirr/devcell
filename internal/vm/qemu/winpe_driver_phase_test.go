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

	"github.com/devcell-sh/go-winkit/diag"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readAnswerVolumeFile reads a file from a point-in-time copy of the answer
// volume. QEMU holds the image open read-write, so the copy isolates the
// reader from in-flight FAT updates; readAllGuarded inside ReadFileFromFAT
// turns a half-written volume into an error instead of a panic.
func readAnswerVolumeFile(t *testing.T, answerImg, name string) string {
	t.Helper()
	if strings.HasSuffix(answerImg, ".qcow2") {
		content, err := ReadFileFromFATQcow2(answerImg, name)
		if err != nil {
			return ""
		}
		return string(content)
	}
	snap := filepath.Join(t.TempDir(), "answer-snap.img")
	data, err := os.ReadFile(answerImg)
	if err != nil {
		return ""
	}
	if err := os.WriteFile(snap, data, 0644); err != nil {
		return ""
	}
	content, err := isokit.ReadFileFromFAT(snap, name)
	if err != nil {
		return ""
	}
	return string(content)
}

// TestWindowsSetupDriverPhase reproduces CELL-429's driver-phase
// failures: it boots the full install stack (real installer ISO, real
// virtio-win drivers under $WinPEDriver$, the WinPE agent, autounattend)
// and reads the agent's log snapshots off the answer volume. The agent is
// the oracle — Setup's fatal dialogs (0x8007000D run 20260812T132820,
// 0x80070103 run 20260812T143146) leave no other machine-readable trace,
// since X:\Windows\Panther dies with the RAM disk.
//
//	go test -run TestWindowsSetupDriverPhase/tcg -timeout 25m ./internal/vm/qemu/
//	go test -run TestWindowsSetupDriverPhase/hvf -timeout 25m ./internal/vm/qemu/
func TestWindowsSetupDriverPhase(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots Windows Setup through the windowsPE driver phase (~10 min)")
	}

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}
			qemuAccel := "tcg,thread=multi"
			if accel == "hvf" {
				qemuAccel = "hvf"
			}

			qemuBin := requireQEMUBin(t)
			fwPath := requireFirmware(t)
			winISO := requireWindowsISO(t)
			virtioISO := requireVirtioISO(t)

			tmpDir := t.TempDir()
			resultsDir := testResultsDir(t)

			diskPath := filepath.Join(tmpDir, "disk.qcow2")
			out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			if err != nil {
				out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			}
			require.NoError(t, err, "qemu-img create: %s", out)

			varsPath := filepath.Join(tmpDir, "vars.fd")
			require.NoError(t, PrepareVarsFile(fwPath, varsPath))

			drivers, err := LoadWinPEStorageDrivers(virtioISO)
			require.NoError(t, err)

			cfg := unattend.DefaultConfig()
			cfg.SSHPubKey = "ssh-ed25519 AAAATESTKEY driver-phase-test"
			cfg.WinPEAgent = true
			cfg.AgentCommand = winpe.DiagScriptCommand()
			cfg.AnswerDrivers = drivers
			answerImg := filepath.Join(tmpDir, "autounattend.img")
			require.NoError(t, unattend.BuildAnswerVolume(cfg, answerImg))

			serialLog := filepath.Join(resultsDir, "serial.log")
			spec := Spec{
				VMName:        "winpe-driver-phase-test",
				CPUs:          4,
				MemoryGB:      4,
				DiskPath:      diskPath,
				FirmwarePath:  fwPath,
				VarsPath:      varsPath,
				QMPSocketDir:  tmpDir,
				DisplayType:   "none",
				Accel:         qemuAccel,
				SerialLogPath: serialLog,
				NoReboot:      true,
				VirtioISO:     virtioISO,
			}
			spec.ApplyDefaults()
			require.NoError(t, spec.Validate())

			qmpSock := QMPSocketPath(spec)

			argv := BuildInstallCommand(spec, winISO, answerImg)
			argv[0] = qemuBin
			updateRunJSON(t, resultsDir, map[string]any{
				"test": t.Name(), "qemu-args": strings.Join(argv, " "),
			})

			require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

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

			const (
				overallDeadline = 15 * time.Minute
				settleAfterSeen = 3 * time.Minute
				pollInterval    = 20 * time.Second
				stallBudget     = 60 * time.Second
			)
			stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
			var stall StallTracker

			ppmPath := filepath.Join(tmpDir, "screen.ppm")
			start := time.Now()
			frame := 0
			var firstSeen time.Time
			var setupact, setuperr, diagOut string
			for time.Since(start) < overallDeadline {
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
					pollPC = diag.ExtractRegister(regs, "PC=")
				}

				n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
				t.Logf("[frame %d] hash=%016x rd=%d PC=%s stall=%d/%d",
					frame, pollHash, pollRead, pollPC, n, stallLimit)

				if stall.Stalled(stallLimit) {
					if _, err := os.Stat(ppmPath); err == nil {
						ConvertPPMtoPNG(ppmPath, filepath.Join(resultsDir, "stalled-last.png"))
					}
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("guest stalled: screen, disk IO, and PC unchanged for %d consecutive polls (%v)",
						stall.Consecutive(), time.Duration(stall.Consecutive())*pollInterval)
				}

				select {
				case reason := <-syncExCh:
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("firmware crashed during boot — Synchronous Exception: %s", reason)
				case reason := <-efiShellCh:
					dumpSerialLog(t, serialLog, resultsDir)
					t.Fatalf("firmware dropped to EFI shell — ISO not bootable via usb-storage: %s", reason)
				default:
				}

				setupact = readAnswerVolumeFile(t, answerImg, "/"+winpe.SetupActSnapshotName)
				setuperr = readAnswerVolumeFile(t, answerImg, "/"+winpe.SetupErrSnapshotName)
				diagOut = readAnswerVolumeFile(t, answerImg, "/"+winpe.AgentResultFile)

				if setupact != "" && firstSeen.IsZero() {
					firstSeen = time.Now()
					t.Logf("agent snapshots appeared after %s", time.Since(start).Round(time.Second))
				}
				combined := setupact + setuperr
				if strings.Contains(combined, "0x80070103") || strings.Contains(combined, "0x8007000D") {
					break
				}
				if !firstSeen.IsZero() && time.Since(firstSeen) > settleAfterSeen && diagOut != "" {
					break
				}
			}

			os.Remove(ppmPath)
			if err := QMPScreendump(qmpSock, ppmPath); err == nil {
				frame++
				pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
					"final", frame, frame, "png")
				ConvertPPMtoPNG(ppmPath, pngPath)
			}

			cmd.Process.Kill()
			cmd.Wait()

			setupact = readAnswerVolumeFile(t, answerImg, "/"+winpe.SetupActSnapshotName)
			setuperr = readAnswerVolumeFile(t, answerImg, "/"+winpe.SetupErrSnapshotName)
			diagOut = readAnswerVolumeFile(t, answerImg, "/"+winpe.AgentResultFile)

			t.Logf("=== devcell-out.txt (diagnostic) ===\n%s", diagOut)
			t.Logf("=== devcell-setuperr.log ===\n%s", setuperr)
			if n := len(setupact); n > 4000 {
				t.Logf("=== devcell-setupact.log (tail) ===\n%s", setupact[n-4000:])
			} else {
				t.Logf("=== devcell-setupact.log ===\n%s", setupact)
			}
			dumpSerialLog(t, serialLog, resultsDir)

			require.NotEmpty(t, setupact, "agent never snapshotted setupact.log — agent/launcher did not run")

			combined := setupact + setuperr
			assert.NotContains(t, combined, "0x80070103",
				"driver double-load abort (ERROR_NO_MORE_ITEMS) — run 20260812T143146 regression")
			assert.NotContains(t, combined, "0x8007000D",
				"unattend windowsPE abort — run 20260812T132820 regression")
			assert.NotContains(t, setuperr, "0x80070001",
				"unresolved DriverPaths abort — run 20260729T172019 regression")

			assert.NotContains(t, setupact, "Unable to find media",
				"SetupHost could not see the installer CD — vioscsi was not loaded before the media search")
		})
	}
}
