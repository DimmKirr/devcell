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

	"github.com/devcell-sh/go-winkit/diag"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWinPEEchoProbe boots WinPE standalone with vioserial + viofs drivers
// and runs the echo probe script. The vioserial driver enables the
// virtio-serial progress channel (\\.\Global\devcell.progress.0) so the
// bootstrap's progress lines arrive in guest-progress.log on the host.
// The test also probes COM1–COM4 and loads viofs (CELL-430).
//
// Requires wimlib (CGO bindings): build with -tags wimlib.
//
//	PKG_CONFIG_PATH=/home/dmitry/.local/lib/pkgconfig \
//	CGO_CFLAGS="-I/home/dmitry/.local/include" \
//	CGO_LDFLAGS="-L/home/dmitry/.local/lib" \
//	go test -tags wimlib -run TestWinPEEchoProbe/tcg -timeout 10m ./internal/vm/qemu/
func TestWinPEEchoProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to probe COM ports and virtiofs")
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

			// ── 1. Extract boot.wim and EFI boot files ──
			stageDir := filepath.Join(tmpDir, "stage")
			extractWinPEStage(t, winISO, stageDir)

			// ── 2. Extract vioserial + viofs drivers from virtio-win ISO ──
			vioserialDrivers, err := winpe.LoadWinPEVioserialDrivers(virtioISO)
			require.NoError(t, err, "extracting vioserial drivers from virtio-win ISO")
			t.Logf("extracted %d vioserial driver files", len(vioserialDrivers))

			viofsDrivers, err := winpe.LoadWinPEViofsDrivers(virtioISO)
			require.NoError(t, err, "extracting viofs drivers from virtio-win ISO")
			t.Logf("extracted %d viofs driver files", len(viofsDrivers))

			// ── 3. Generate WinPE payload scripts ──
			injectDir := filepath.Join(tmpDir, "inject")
			require.NoError(t, os.MkdirAll(injectDir, 0755))

			// Write vioserial driver files into inject dir so they land in
			// boot.wim at X:\devcell\drivers\vioserial\. The bootstrap drvloads
			// them before writing progress to the virtio-serial port.
			for answerPath, data := range vioserialDrivers {
				hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
				require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0755))
				require.NoError(t, os.WriteFile(hostPath, data, 0644))
			}

			payloadCfg := winpe.PayloadConfig{
				WPEInit:      true,
				ProgressPort: `\\.\Global\` + ProgressPortName,
				DriverINFs:   []string{`X:\devcell\drivers\vioserial\vioser.inf`},
				PollSeconds:  5,
				SyncAgent:    true,
			}
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, "winpeshl.ini"),
				winpe.GenerateShellINI_NoSetup(), 0644))
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, "bootstrap.cmd"),
				winpe.GenerateBootstrapCmd(), 0644))
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, "bootstrap.ps1"),
				winpe.GenerateBootstrap(payloadCfg), 0644))
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, "agent.ps1"),
				winpe.GenerateAgent(payloadCfg), 0644))

			const viofsTag = "devcell-logs"
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, winpe.EchoProbeScriptName),
				winpe.GenerateEchoProbeScript(viofsTag), 0644))

			// ── 4. Inject into boot.wim image 2 ──
			bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
			injectIntoBootWim(t, bootWimPath, injectDir, diagToolFiles{})

			// ── 5. Create custom bootable ISO ──
			winpeISO := filepath.Join(tmpDir, "winpe-echo.iso")
			require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))
			t.Logf("custom WinPE ISO: %s", winpeISO)

			// ── 6. Create answer volume with agent command + viofs drivers ──
			answerImg := filepath.Join(tmpDir, "answer.img")
			answerFiles := map[string][]byte{
				"/" + winpe.AgentVolumeMarker:   []byte("1"),
				"/" + winpe.AgentCommandFile:    []byte(winpe.EchoProbeScriptCommand()),
				"/" + winpe.EchoProbeScriptName: winpe.GenerateEchoProbeScript(viofsTag),
			}
			for path, data := range viofsDrivers {
				answerFiles[path] = data
			}
			for path, data := range vioserialDrivers {
				answerFiles[path] = data
			}
			require.NoError(t, isokit.CreateFATImage(answerImg, answerFiles))

			// ── 7. Start virtiofsd ──
			virtiofsdBin, err := VirtiofsdPath()
			if err != nil {
				t.Skipf("virtiofsd not available: %v", err)
			}

			viofsSharedDir := filepath.Join(resultsDir, "viofs-shared")
			require.NoError(t, os.MkdirAll(viofsSharedDir, 0755))

			viofsSock := filepath.Join(tmpDir, "virtiofs.sock")
			fsd := VirtiofsdCommand(virtiofsdBin, viofsSock, viofsSharedDir)
			fsd.Stdout = os.Stderr
			fsd.Stderr = os.Stderr
			require.NoError(t, fsd.Start(), "starting virtiofsd")
			defer func() {
				fsd.Process.Kill()
				fsd.Wait()
			}()

			// Give virtiofsd a moment to create the socket.
			for i := 0; i < 20; i++ {
				if _, err := os.Stat(viofsSock); err == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			require.FileExists(t, viofsSock, "virtiofsd socket not created")

			// ── 8. Build QEMU command ──
			diskPath := filepath.Join(tmpDir, "disk.qcow2")
			out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			if err != nil {
				out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
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
			guestProgressLog := filepath.Join(resultsDir, "guest-progress.log")
			spec := Spec{
				VMName:               "winpe-echo-probe-test",
				CPUs:                 4,
				MemoryGB:             4,
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
				VirtioFSSocketPath:   viofsSock,
				VirtioFSTag:          viofsTag,
				NoReboot:             true,
			}
			spec.ApplyDefaults()
			require.NoError(t, spec.Validate())

			qmpSock := QMPSocketPath(spec)

			argv := BuildWinPECommand(spec, winpeISO, answerImg)
			argv[0] = qemuBin
			updateRunJSON(t, resultsDir, map[string]any{
				"test": t.Name(), "qemu-args": strings.Join(argv, " "),
			})
			require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

			// ── 9. Boot and poll ──
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
				overallDeadline = 6 * time.Minute
				pollInterval    = 10 * time.Second
				stallBudget     = 60 * time.Second
			)
			stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
			var stall StallTracker

			ppmPath := filepath.Join(tmpDir, "screen.ppm")
			start := time.Now()
			frame := 0
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
					t.Fatalf("firmware dropped to EFI shell: %s", reason)
				default:
				}

				doneMarker := readAnswerVolumeFile(t, answerImg, "/"+winpe.AgentDoneFile)
				if doneMarker != "" {
					t.Logf("agent done marker appeared after %s (%d frames)", time.Since(start).Round(time.Second), frame)
					break
				}
			}

			// ── 10. Capture final state ──
			os.Remove(ppmPath)
			if err := QMPScreendump(qmpSock, ppmPath); err == nil {
				frame++
				pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
					"final", frame, frame, "png")
				ConvertPPMtoPNG(ppmPath, pngPath)
			}

			cmd.Process.Kill()
			cmd.Wait()

			// ── 11. Assert echo probe results ──
			diagOut := readAnswerVolumeFile(t, answerImg, "/"+winpe.AgentResultFile)
			t.Logf("=== devcell-out.txt (echo probe) ===\n%s", diagOut)
			os.WriteFile(filepath.Join(resultsDir, "devcell-out.txt"), []byte(diagOut), 0644)
			dumpSerialLog(t, serialLog, resultsDir)

			require.NotEmpty(t, diagOut, "agent never ran — WinPE did not boot or the answer volume was not found")
			assert.Contains(t, diagOut, "DEVCELL ECHO PROBE COMPLETE",
				"echo probe script did not run to completion")

			// COM port probe ran
			assert.Contains(t, diagOut, "COM PORT PROBE")
			assert.Contains(t, diagOut, "COM PROBE DONE")

			// Check which COM port(s) succeeded — at least one should
			comOK := false
			for i := 1; i <= 4; i++ {
				if strings.Contains(diagOut, "COM"+string(rune('0'+i))+": OK") {
					t.Logf("COM%d echo succeeded", i)
					comOK = true
				}
			}

			// Check guest-progress.log for virtio-serial progress markers.
			// The bootstrap loads vioserial via drvload, then writes progress
			// to \\.\Global\devcell.progress.0 which the host reads here.
			if progressData, err := os.ReadFile(guestProgressLog); err == nil && len(progressData) > 0 {
				t.Logf("=== guest-progress.log ===\n%s", string(progressData))
				os.WriteFile(filepath.Join(resultsDir, "guest-progress.log"), progressData, 0644)
				assert.Contains(t, string(progressData), "devcell:",
					"guest-progress.log must contain devcell progress markers via virtio-serial")
			} else {
				t.Log("guest-progress.log is empty — vioserial driver may not have loaded")
			}

			if !comOK {
				t.Log("WARNING: no COM port echo succeeded inside WinPE (expected on ARM64 without ISA serial)")
			}

			// viofs section ran
			assert.Contains(t, diagOut, "VIOFS MOUNT")
			assert.Contains(t, diagOut, "VIOFS DONE")

			// Check if virtiofs file landed on host
			probeFile := filepath.Join(viofsSharedDir, "viofs-probe.txt")
			if data, err := os.ReadFile(probeFile); err == nil {
				t.Logf("viofs probe file content: %q", strings.TrimSpace(string(data)))
				assert.Contains(t, string(data), "DEVCELL_VIOFS_HELLO",
					"virtiofs write did not produce the expected content")
			} else {
				t.Logf("viofs probe file not found at %s — virtiofs mount may have failed (check devcell-out.txt)", probeFile)
			}
		})
	}
}
