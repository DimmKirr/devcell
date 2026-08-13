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

// TestWinPEHyperVInjection boots WinPE from a custom ISO with Hyper-V
// diagnostics baked into boot.wim. No Windows installation happens — the test
// modifies boot.wim offline to inject scripts, creates a bootable ISO, and
// verifies the diagnostics ran inside WinPE (CELL-430).
//
// Requires wimlib (CGO bindings): build with -tags wimlib.
//
//	PKG_CONFIG_PATH=/home/dmitry/.local/lib/pkgconfig \
//	CGO_CFLAGS="-I/home/dmitry/.local/include" \
//	CGO_LDFLAGS="-L/home/dmitry/.local/lib" \
//	go test -tags wimlib -run TestWinPEHyperVInjection/tcg -timeout 10m ./internal/vm/qemu/
func TestWinPEHyperVInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to probe Hyper-V/WSL2 injection via DISM")
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

			// ── 1. Extract boot.wim and EFI boot files from Windows ISO ──
			stageDir := filepath.Join(tmpDir, "stage")
			extractWinPEStage(t, winISO, stageDir)

			// ── 1b. Extract vioserial driver for progress channel ──
			vioserialDrivers, err := LoadWinPEVioserialDrivers(virtioISO)
			require.NoError(t, err, "extracting vioserial drivers from virtio-win ISO")

			// ── 2. Generate WinPE payload scripts ──
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
				DriverINFs:   []string{`X:\devcell\drivers\vioserial\vioser.inf`},
				PollSeconds:  5,
				SyncAgent:    true,
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
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, WinPEHyperVDiagScriptName),
				GenerateWinPEHyperVDiagScript(payloadCfg.ProgressPort), 0644))

			// ── 3. Inject into boot.wim image 2 via wimlib ──
			bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
			injectIntoBootWim(t, bootWimPath, injectDir, HyperVBootPatches())

			// ── 4. Create custom bootable ISO ──
			winpeISO := filepath.Join(tmpDir, "winpe-hyperv.iso")
			require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))
			t.Logf("custom WinPE ISO: %s", winpeISO)

			// ── 5. Create answer volume with agent command ──
			answerImg := filepath.Join(tmpDir, "answer.img")
			answerFiles := map[string][]byte{
				"/" + AgentVolumeMarker:         []byte("1"),
				"/" + AgentCommandFile:          []byte(WinPEHyperVDiagScriptCommand()),
				"/" + WinPEHyperVDiagScriptName: GenerateWinPEHyperVDiagScript(payloadCfg.ProgressPort),
			}
			require.NoError(t, isokit.CreateFATImage(answerImg, answerFiles))

			// ── 6. Build QEMU command ──
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
				VMName:               "winpe-hyperv-injection-test",
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

			argv := BuildWinPECommand(spec, winpeISO, answerImg)
			argv[0] = qemuBin
			// Attach the original Windows ISO so the diag script can find
			// install.wim (the custom WinPE ISO only has boot files).
			argv = append(argv,
				"-drive", "file="+winISO+",media=cdrom,if=none,id=cdrom1",
				"-device", "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
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
					pollPC = ExtractRegister(regs, "PC=")
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

				doneMarker := readAnswerVolumeFile(t, answerImg, "/"+AgentDoneFile)
				if doneMarker != "" {
					t.Logf("agent done marker appeared after %s (%d frames)", time.Since(start).Round(time.Second), frame)
					break
				}
			}

			// ── 8. Capture final state ──
			os.Remove(ppmPath)
			if err := QMPScreendump(qmpSock, ppmPath); err == nil {
				frame++
				pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
					"final", frame, frame, "png")
				ConvertPPMtoPNG(ppmPath, pngPath)
			}

			cmd.Process.Kill()
			cmd.Wait()

			// ── 9. Assert diagnostics ──
			diagOut := readAnswerVolumeFile(t, answerImg, "/"+AgentResultFile)

			t.Logf("=== devcell-out.txt (Hyper-V/WSL2 diagnostics) ===\n%s", diagOut)
			os.WriteFile(filepath.Join(resultsDir, "devcell-out.txt"), []byte(diagOut), 0644)
			dumpSerialLog(t, serialLog, resultsDir)

			if data, err := os.ReadFile(guestProgressLog); err == nil && len(data) > 0 {
				t.Logf("=== guest progress log ===\n%s", string(data))
			}

			require.NotEmpty(t, diagOut, "agent never ran — WinPE did not boot or the answer volume was not found")
			assert.Contains(t, diagOut, "DEVCELL HYPERV DIAGNOSTICS COMPLETE",
				"diagnostics script did not run to completion")

			// Section presence checks
			for _, section := range []struct {
				marker string
				desc   string
			}{
				{"SYSTEM INFO", "system architecture and version info"},
				{"BCD HYPERVISOR CONFIG", "BCD hypervisor launch configuration"},
				{"HYPERVISOR HOST BINARIES", "hypervisor host binaries check"},
				{"DRIVER REGISTRY DETAILS", "full driver registry dumps"},
				{"HYPERVISOR DRIVER STATE", "driverquery output"},
				{"DISM ONLINE PACKAGES", "DISM package list"},
				{"OFFLINE SERVICE ENABLE", "offline registry service enablement"},
				{"HYPERV SERVICE STATE", "Hyper-V service state"},
				{"WSL SERVICE STATE", "WSL service state"},
				{"HYPERVISOR RUNTIME STATUS", "pre-start driver status"},
				{"HYPERVISOR DETECTION", "hypervisor presence detection"},
				{"START HYPERV SERVICES", "service start attempts"},
				{"EVENT LOGS", "Windows event logs"},
				{"SETUPAPI LOGS", "driver setup API logs"},
				{"FINAL DRIVER STATUS", "post-start driver status"},
				{"POST-MORTEM SUMMARY", "diagnostic summary"},
			} {
				assert.Contains(t, diagOut, section.marker,
					"must contain section: %s", section.desc)
			}

			assert.Contains(t, diagOut, "vmms",
				"must query the vmms (Hyper-V VM Management) service")
			assert.Contains(t, diagOut, "net start hvservice",
				"must attempt net start hvservice")
			assert.Contains(t, diagOut, "hvservice_STATUS=",
				"must report hvservice registration status")
		})
	}
}

// extractWinPEStage extracts the files needed for a custom WinPE ISO from
// the stock Windows ISO into stageDir:
//   - sources/boot.wim
//   - efi/boot/bootaa64.efi
//   - efi/microsoft/boot/efisys_noprompt.bin (or efisys.bin)
func extractWinPEStage(t *testing.T, winISO, stageDir string) {
	t.Helper()

	for _, dir := range []string{
		filepath.Join(stageDir, "sources"),
		filepath.Join(stageDir, "boot"),
		filepath.Join(stageDir, "efi", "boot"),
		filepath.Join(stageDir, "efi", "microsoft", "boot"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}

	extractions := []struct {
		isoPath string
		dest    string
		alts    []string // alternative ISO paths (case variations)
	}{
		{
			isoPath: "sources/boot.wim",
			dest:    filepath.Join(stageDir, "sources", "boot.wim"),
		},
		{
			isoPath: "efi/microsoft/boot/bcd",
			dest:    filepath.Join(stageDir, "boot", "bcd"),
			alts:    []string{"EFI/Microsoft/Boot/BCD"},
		},
		{
			isoPath: "boot/boot.sdi",
			dest:    filepath.Join(stageDir, "boot", "boot.sdi"),
			alts:    []string{"Boot/boot.sdi", "BOOT/BOOT.SDI"},
		},
		{
			isoPath: "bootmgr.efi",
			dest:    filepath.Join(stageDir, "bootmgr.efi"),
			alts:    []string{"BOOTMGR.EFI"},
		},
		{
			isoPath: "efi/boot/bootaa64.efi",
			dest:    filepath.Join(stageDir, "efi", "boot", "bootaa64.efi"),
			alts:    []string{"EFI/BOOT/BOOTAA64.EFI", "EFI/Boot/bootaa64.efi"},
		},
		{
			isoPath: "efi/microsoft/boot/bcd",
			dest:    filepath.Join(stageDir, "efi", "microsoft", "boot", "bcd"),
			alts:    []string{"EFI/Microsoft/Boot/BCD"},
		},
		{
			isoPath: "efi/microsoft/boot/efisys_noprompt.bin",
			dest:    filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys_noprompt.bin"),
			alts: []string{
				"EFI/Microsoft/Boot/efisys_noprompt.bin",
				"efi/microsoft/boot/efisys.bin",
				"EFI/Microsoft/Boot/efisys.bin",
			},
		},
	}

	for _, e := range extractions {
		paths := append([]string{e.isoPath}, e.alts...)
		var data []byte
		var extractErr error
		for _, p := range paths {
			data, extractErr = extract7z(winISO, p)
			if extractErr == nil && len(data) > 0 {
				break
			}
		}
		require.NoError(t, extractErr, "extracting %s from Windows ISO", e.isoPath)
		require.NotEmpty(t, data, "%s is empty", e.isoPath)
		require.NoError(t, os.WriteFile(e.dest, data, 0644))
		t.Logf("extracted %s (%d bytes)", e.isoPath, len(data))
	}
}

// extract7z extracts a single file from an ISO using 7z.
func extract7z(isoPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "7z-extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("7z", "e", "-o"+tmpDir, "-y", isoPath, filePath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	base := filepath.Base(filePath)
	return os.ReadFile(filepath.Join(tmpDir, base))
}

// injectIntoBootWim uses wimlib to add WinPE payload files into boot.wim
// image 2 ("Microsoft Windows Setup"). The WIM is modified in-place.
// Optional registryPatches are applied to hives inside the WIM before
// overwriting (e.g. setting hvservice Start=0 for Hyper-V boot).
func injectIntoBootWim(t *testing.T, bootWimPath, injectDir string, registryPatches ...WimRegistryPatch) {
	t.Helper()

	require.True(t, wimlib.Available(), "wimlib CGO bindings not available — build with -tags wimlib")

	wim, err := wimlib.OpenWIM(bootWimPath)
	require.NoError(t, err, "opening boot.wim")
	defer wim.Close()

	count, err := wim.ImageCount()
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2, "boot.wim must have at least 2 images")

	imageNum := 2

	desc, _ := wim.ImageDescription(imageNum)
	t.Logf("injecting into boot.wim image %d: %s", imageNum, desc)

	require.NoError(t, wim.UpdateImageAdd(imageNum,
		filepath.Join(injectDir, "winpeshl.ini"),
		`\Windows\System32\winpeshl.ini`))

	require.NoError(t, wim.UpdateImageAddTree(imageNum,
		injectDir, `\devcell`))

	for _, rp := range registryPatches {
		t.Logf("patching WIM registry: %s (%d patches)", rp.HivePath, len(rp.Patches))
		cleanup, err := PatchWimRegistry(wim, imageNum, rp)
		require.NoError(t, err)
		defer cleanup()
	}

	require.NoError(t, wim.Overwrite())
	t.Logf("boot.wim modified in-place with payload scripts")
}
