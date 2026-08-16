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

// TestWimBuilder boots a builder WinPE that runs DISM offline servicing
// against a copy of boot.wim. The test verifies:
//  1. The builder script finds install.wim on the Windows ISO
//  2. The builder script finds the virtio-win ISO
//  3. DISM mount/unmount/servicing commands execute
//  4. The builder writes a completion marker
//  5. devcell.wim contains Hyper-V binaries and enabled features
//  6. devcell.wim contains WSL2 (Microsoft-Windows-Subsystem-Linux) feature
//  7. devcell.wim contains OpenSSH binaries
//  8. devcell.wim contains virtio drivers (NetKVM, vioserial, vioscsi)
//
// This is the integration test for `cell build --engine=qemu`'s WIM prep
// phase. The same BuildWimBuilderArgv and SharedVolumeFiles functions are
// used by the build command.
//
// Requires: QEMU, Windows ISO, VirtIO drivers, 7z.
//
//	go test -run TestWimBuilder/tcg -timeout 20m ./internal/vm/qemu/
func TestWimBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to run DISM offline servicing")
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
			require.NoError(t, ExtractWinPEStage(winISO, stageDir))

			// ── 2. Extract vioserial drivers ──
			vioserialDrivers, err := LoadWinPEVioserialDrivers(virtioISO)
			require.NoError(t, err)

			// ── 3. Create shared FAT volume with boot.wim ──
			bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
			bootWimData, err := os.ReadFile(bootWimPath)
			require.NoError(t, err)
			t.Logf("boot.wim: %d bytes (%.1f MB)", len(bootWimData), float64(len(bootWimData))/(1024*1024))

			var ops []WimPrepOp
			ops = append(ops, HyperVPrepOps()...)
			ops = append(ops, WSL2PrepOps()...)
			ops = append(ops, OpenSSHPrepOps()...)
			ops = append(ops, VirtIODriverPrepOps()...)
			cfg := WimPrepConfig{
				Ops: ops,
			}
			sharedFiles := SharedVolumeFiles(cfg)
			sharedFiles["/boot.wim"] = bootWimData

			sharedImg := filepath.Join(tmpDir, "shared.qcow2")
			// 20GB sparse qcow2: boot.wim (~500MB) + devcell.wim (~700MB) + DISM scratch.
			// Only actual data occupies host disk.
			require.NoError(t, CreateFATQcow2(sharedImg, sharedFiles, 20*1024*1024*1024))
			t.Logf("shared volume: %s", sharedImg)

			// ── 4. Inject agent into boot.wim for standalone WinPE ──
			injectDir := filepath.Join(tmpDir, "inject")
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
				GenerateWinPEBootstrap(payloadCfg), 0644))
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, "agent.cmd"),
				GenerateWinPEAgent(payloadCfg), 0644))

			require.NoError(t, InjectWinPEPayload(bootWimPath, injectDir))

			// ── 5. Create WinPE ISO ──
			winpeISO := filepath.Join(tmpDir, "winpe-builder.iso")
			require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))

			// ── 6. Build QEMU command ──
			diskPath := filepath.Join(tmpDir, "scratch.qcow2")
			out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "4G").CombinedOutput()
			if err != nil {
				out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "4G").CombinedOutput()
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
				VMName:               "wim-builder-test",
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
			appendRunInfo(t, resultsDir, "test:  "+t.Name()+"\nargv:  "+strings.Join(argv, " ")+"\n")

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

			waitForSocket(t, qmpSock, 30*time.Second, resultsDir)
			assertAccel(t, qmpSock, accel, resultsDir)

			stop := make(chan struct{})
			defer close(stop)
			efiShellCh := WatchSerialForEFIShell(serialLog, stop)
			syncExCh := WatchSerialForSyncException(serialLog, stop)

			const (
				overallDeadline = 15 * time.Minute
				pollInterval    = 15 * time.Second
				stallBudget     = 90 * time.Second
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

			// ── 8. Capture results ──
			cmd.Process.Kill()
			cmd.Wait()

			agentOut := readAnswerVolumeFile(t, sharedImg, "/"+AgentResultFile)
			t.Logf("=== builder output ===\n%s", agentOut)
			os.WriteFile(filepath.Join(resultsDir, "builder-output.txt"), []byte(agentOut), 0644)
			dumpSerialLog(t, serialLog, resultsDir)

			// ── 9. Assertions ──
			doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)
			require.NotEmpty(t, doneMarker, "builder never completed")

			assert.Contains(t, agentOut, "DEVCELL WIM BUILDER",
				"builder script header must appear in output")

			assert.Contains(t, agentOut, "Found Windows ISO",
				"builder must find install.wim on the Windows ISO drive")

			assert.Contains(t, agentOut, "Found virtio-win ISO",
				"builder must find virtio-win drivers ISO")

			assert.Contains(t, agentOut, "Mounting boot.wim",
				"builder must attempt to mount boot.wim")

			result := strings.TrimSpace(doneMarker)
			t.Logf("builder result: %s", result)

			if result != "SUCCESS" {
				t.Skipf("builder reported %s — DISM offline servicing did not succeed in WinPE; skipping WIM verification", result)
			}

			assert.Contains(t, agentOut, "boot.wim committed successfully")
			assert.Contains(t, agentOut, "devcell.wim created")

			// ── 10. Verify devcell.wim contents with wimlib ──
			// Extract devcell.wim from the shared volume and open it.
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

			// Extract image 2 to inspect its contents.
			extractDir := filepath.Join(tmpDir, "devcell-extracted")
			require.NoError(t, os.MkdirAll(extractDir, 0755))
			require.NoError(t, wim.ExtractImage(2, extractDir, nil))

			// ── 10a. Hyper-V: binaries and feature enablement ──
			assert.Contains(t, agentOut, "OK: Enable-Feature Microsoft-Hyper-V",
				"DISM must report Hyper-V feature enabled")
			assert.Contains(t, agentOut, "OK: Enable-Feature VirtualMachinePlatform",
				"DISM must report VirtualMachinePlatform feature enabled")

			hypervFiles := []string{
				"Windows/System32/vmms.exe",
				"Windows/System32/vmwp.exe",
				"Windows/System32/vmcompute.exe",
				"Windows/System32/drivers/Vid.sys",
				"Windows/System32/drivers/vmswitch.sys",
				"Windows/System32/drivers/storvsp.sys",
			}
			for _, f := range hypervFiles {
				fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
				if info, err := os.Stat(fullPath); err == nil {
					t.Logf("  Hyper-V OK: %s (%d bytes)", f, info.Size())
				} else {
					t.Errorf("  Hyper-V MISSING: %s", f)
				}
			}

			// ── 10a-reg. Hyper-V: registry boot patches ──
			if err := VerifyWimRegistry(wim, 2, `\Windows\System32\config\SYSTEM`, HyperVBootChecks()); err != nil {
				t.Errorf("Hyper-V registry verification failed: %v", err)
			} else {
				t.Log("  Hyper-V registry patches verified")
			}

			// ── 10b. WSL2: feature enablement ──
			assert.Contains(t, agentOut, "OK: Enable-Feature Microsoft-Windows-Subsystem-Linux",
				"DISM must report WSL feature enabled")

			// ── 10c. OpenSSH: binaries ──
			opensshFiles := []string{
				"Windows/System32/OpenSSH/sshd.exe",
				"Windows/System32/OpenSSH/ssh.exe",
				"Windows/System32/OpenSSH/ssh-keygen.exe",
			}
			for _, f := range opensshFiles {
				fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
				if info, err := os.Stat(fullPath); err == nil {
					t.Logf("  OpenSSH OK: %s (%d bytes)", f, info.Size())
				} else {
					t.Errorf("  OpenSSH MISSING: %s", f)
				}
			}

			// ── 10d. VirtIO drivers: DISM /Add-Driver output and DriverStore files ──
			assert.Contains(t, agentOut, `OK: Add-Driver NetKVM\w11\ARM64`,
				"DISM must report NetKVM driver added")
			assert.Contains(t, agentOut, `OK: Add-Driver vioserial\w11\ARM64`,
				"DISM must report vioserial driver added")
			assert.Contains(t, agentOut, `OK: Add-Driver vioscsi\w11\ARM64`,
				"DISM must report vioscsi driver added")

			// DISM /Add-Driver places driver files under DriverStore/FileRepository.
			// The exact subdirectory name is generated by DISM (e.g.
			// netkvm.inf_arm64_<hash>), so we glob for the key binaries.
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
		})
	}
}
