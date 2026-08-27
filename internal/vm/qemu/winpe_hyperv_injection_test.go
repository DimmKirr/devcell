//go:build wimlib

package qemu

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/diag"
	"github.com/devcell-sh/go-winkit/wim"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/isokit"
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
			kernelFW, err := KernelFirmwarePath()
			if err != nil {
				t.Skipf("no kernel-bootable firmware: %v", err)
			}
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

			// ── 1c. Extract diagnostic tools from install.wim ──
			// Stock WinPE lacks sc.exe, tasklist.exe, wevtutil.exe — extract
			// them from the full Windows install.wim so they can be injected
			// into boot.wim alongside scripts and registry patches.
			diagTools := extractDiagToolsFromInstallWim(t, winISO)

			// ── 2. Generate WinPE payload scripts ──
			injectDir := filepath.Join(tmpDir, "inject")
			require.NoError(t, os.MkdirAll(injectDir, 0755))

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
			require.NoError(t, os.WriteFile(
				filepath.Join(injectDir, winpe.HyperVDiagScriptName),
				winpe.GenerateHyperVDiagScript(payloadCfg.ProgressPort), 0644))

			// ── 3. Inject into boot.wim image 2 via wimlib ──
			bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
			injectIntoBootWim(t, bootWimPath, injectDir, diagTools, wim.HyperVBootPatches())

			// ── 4. Create custom bootable ISO ──
			winpeISO := filepath.Join(tmpDir, "winpe-hyperv.iso")
			require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))
			t.Logf("custom WinPE ISO: %s", winpeISO)

			// ── 5. Create answer volume with agent command ──
			answerImg := filepath.Join(tmpDir, "answer.img")
			answerFiles := map[string][]byte{
				"/" + winpe.AgentVolumeMarker:    []byte("1"),
				"/" + winpe.AgentCommandFile:     []byte(winpe.HyperVDiagScriptCommand()),
				"/" + winpe.HyperVDiagScriptName: winpe.GenerateHyperVDiagScript(payloadCfg.ProgressPort),
			}
			require.NoError(t, isokit.CreateFATImage(answerImg, answerFiles))

			// ── 6. Build QEMU command ──
			diskPath := filepath.Join(tmpDir, "disk.qcow2")
			out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			if err != nil {
				out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
			}
			require.NoError(t, err, "qemu-img create: %s", out)

			serialLog := filepath.Join(resultsDir, "serial.log")
			guestProgressLog := filepath.Join(resultsDir, "guest-progress.log")
			spec := Spec{
				VMName:               "winpe-hyperv-injection-test",
				CPUs:                 4,
				MemoryGB:             4,
				DiskPath:             diskPath,
				FirmwarePath:         kernelFW,
				FirmwareKernel:       true,
				SecureWorld:          true,
				QMPSocketDir:         tmpDir,
				DisplayType:          "none",
				Accel:                qemuAccel,
				SerialLogPath:        serialLog,
				GuestProgressLogPath: guestProgressLog,
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

			// ── 7. Boot and poll ──
			// ARM64 EDK2 kernel firmware intermittently crashes with
			// Synchronous Exception when enumerating the USB answer
			// volume as a boot candidate. Retry up to 3 times.
			const maxFWRetries = 3
			var fwCrashCount int
			for attempt := 1; attempt <= maxFWRetries; attempt++ {
				if attempt > 1 {
					t.Logf("firmware crash retry %d/%d — restarting QEMU", attempt, maxFWRetries)
					os.Truncate(serialLog, 0)
				}

				crashed := bootWinPEAndPoll(t, argv, qmpSock, serialLog, answerImg, tmpDir, resultsDir)
				if !crashed {
					break
				}
				fwCrashCount++
				if fwCrashCount >= maxFWRetries {
					t.Fatalf("firmware crashed %d/%d times — giving up", fwCrashCount, maxFWRetries)
				}
			}

			// ── 9. Assert diagnostics ──
			diagOut := readAnswerVolumeFile(t, answerImg, "/"+winpe.AgentResultFile)

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

			// ── 1. Packages installed: core Hyper-V binaries present in boot.wim ──

			for _, bin := range []string{
				"hvaa64.exe", "hvloader.dll", "hvservice.sys",
				"winhv.sys", "winhvr.sys", "hvsocket.sys",
				"vmbus.sys", "vmbkmcl.sys",
			} {
				assert.NotContains(t, diagOut, "MISSING: X:\\Windows\\System32\\"+bin,
					"core binary %s must not be missing from boot.wim", bin)
				assert.NotContains(t, diagOut, "MISSING: X:\\Windows\\System32\\drivers\\"+bin,
					"core binary %s must not be missing from boot.wim", bin)
			}

			// Services report their registration status.
			for _, svc := range []string{
				"hvservice", "vmbus", "vmbusr", "HvHost", "vmcompute",
				"winhv", "winhvr", "hvsocket", "vmbkmcl",
			} {
				assert.Contains(t, diagOut, svc+"_STATUS=",
					"must report %s registration status", svc)
			}

			// Services that exist in stock boot.wim MUST be REGISTERED
			// after go-regedit patching.
			for _, svc := range []string{"hvservice", "vmbus", "HvHost"} {
				assert.Contains(t, diagOut, svc+"_STATUS=REGISTERED",
					"%s must be registered in boot.wim", svc)
			}

			// ── 2. Able to start: correct Start values + start attempted ──

			// Boot-load drivers: go-regedit patched Start from 3→0.
			for _, svc := range []struct{ name, value string }{
				{"hvservice", "0x0"},
				{"vmbus", "0x0"},
			} {
				assert.Contains(t, diagOut, svc.name+"_START_VALUE="+svc.value,
					"%s must have Start=%s (Boot) after offline registry patching", svc.name, svc.value)
			}

			// Auto-start service: go-regedit patched HvHost Start to 2.
			assert.Contains(t, diagOut, "HvHost_START_VALUE=0x2",
				"HvHost must have Start=0x2 (Auto) after offline registry patching")

			// net start was attempted for every core driver.
			for _, svc := range []string{"hvservice", "vmbus", "winhv", "winhvr", "hvsocket"} {
				assert.Contains(t, diagOut, svc+"_NET_START_EXIT=",
					"must attempt net start %s and report exit code", svc)
			}

			// sc query: sc.exe on ARM64 uses WriteConsoleW so text output
			// can't be captured, but the exit code is reliable. Exit 0 means
			// the service exists and is queryable; 1060 means absent.
			for _, svc := range []string{"hvservice", "vmbus", "HvHost"} {
				assert.Contains(t, diagOut, svc+"_SC_EXIT=0",
					"sc query %s must succeed (exit 0) for registered services", svc)
				assert.Contains(t, diagOut, svc+"_SC_STATE=QUERYABLE",
					"registered service %s must be QUERYABLE via sc.exe", svc)
			}

			// ── 2.1. Clean logs: no driver setup errors ──

			assert.Contains(t, diagOut, "SETUPAPI_ERRORS=NONE",
				"setupapi.dev.log must have no ERROR/FAIL entries after Hyper-V injection")
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

// diagToolFiles holds the WIM path → host filesystem path mapping for
// diagnostic tools extracted from install.wim.
type diagToolFiles struct {
	// wimPath → host filesystem path
	tools map[string]string
}

// extractDiagToolsFromInstallWim extracts diagnostic tools (sc.exe, etc.)
// from the full Windows install.wim inside the ISO. Returns the extracted
// tool mappings; the caller injects them into boot.wim in the same WIM
// session as scripts and registry patches.
func extractDiagToolsFromInstallWim(t *testing.T, winISO string) diagToolFiles {
	t.Helper()

	result := diagToolFiles{tools: make(map[string]string)}
	tmpDir := t.TempDir()

	installWimPath := filepath.Join(tmpDir, "install.wim")
	for _, isoPath := range []string{"sources/install.wim", "Sources/install.wim"} {
		cmd := exec.Command("7z", "e", "-o"+tmpDir, "-y", winISO, isoPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("7z extract %s: %v (%s)", isoPath, err, out)
			continue
		}
		break
	}
	if _, err := os.Stat(installWimPath); err != nil {
		t.Logf("warning: could not extract install.wim from ISO — skipping tool extraction")
		return result
	}
	t.Logf("extracted install.wim (%s)", installWimPath)

	installWim, err := wimlib.OpenWIM(installWimPath)
	require.NoError(t, err, "opening install.wim")
	defer installWim.Close()

	toolsDir := filepath.Join(tmpDir, "tools")
	require.NoError(t, os.MkdirAll(toolsDir, 0755))

	for _, wp := range winpe.DiagToolPaths() {
		if err := installWim.ExtractPaths(1, toolsDir, []string{wp}); err != nil {
			t.Logf("warning: could not extract %s from install.wim: %v", wp, err)
			continue
		}
		rel := strings.TrimPrefix(wp, `\`)
		rel = strings.ReplaceAll(rel, `\`, string(filepath.Separator))
		fsPath := filepath.Join(toolsDir, rel)
		if _, err := os.Stat(fsPath); err != nil {
			t.Logf("warning: extracted %s but file not found at %s", wp, fsPath)
			continue
		}
		result.tools[wp] = fsPath
		t.Logf("extracted %s (%s)", wp, fsPath)
	}
	installWim.Close()
	os.Remove(installWimPath)

	t.Logf("extracted %d diagnostic tools from install.wim", len(result.tools))
	return result
}

// injectIntoBootWim uses wimlib to add WinPE payload files into boot.wim
// image 2 ("Microsoft Windows Setup"). The WIM is modified in-place.
// Optional registryPatches are applied to hives inside the WIM before
// overwriting (e.g. setting hvservice Start=0 for Hyper-V boot).
func injectIntoBootWim(t *testing.T, bootWimPath, injectDir string, diagTools diagToolFiles, registryPatches ...wim.RegistryPatch) {
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

	for wimPath, fsPath := range diagTools.tools {
		t.Logf("injecting diag tool %s into boot.wim", wimPath)
		require.NoError(t, wim.UpdateImageAdd(imageNum, fsPath, wimPath))
	}

	for _, rp := range registryPatches {
		t.Logf("patching WIM registry: %s (%d patches)", rp.HivePath, len(rp.Patches))
		cleanup, err := wim.PatchRegistry(wim, imageNum, rp)
		require.NoError(t, err)
		defer cleanup()
	}

	infoJSON := filepath.Join(injectDir, "info.json")
	require.NoError(t, os.WriteFile(infoJSON,
		[]byte(fmt.Sprintf(`{"version":"%s"}`, time.Now().UTC().Format(time.RFC3339))),
		0644))
	require.NoError(t, wim.UpdateImageAdd(imageNum, infoJSON, `\info.json`))

	require.NoError(t, wim.Overwrite())
	t.Logf("boot.wim modified in-place with payload scripts and %d diag tools", len(diagTools.tools))
}

// bootWinPEAndPoll starts QEMU, polls for completion, and returns true if
// the firmware crashed with a Synchronous Exception (caller should retry).
func bootWinPEAndPoll(t *testing.T, argv []string, qmpSock, serialLog, answerImg, tmpDir, resultsDir string) (firmwareCrashed bool) {
	t.Helper()

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
			t.Logf("firmware Synchronous Exception (intermittent EDK2 bug): %s", reason)
			return true
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

	// Capture final state.
	os.Remove(ppmPath)
	if err := QMPScreendump(qmpSock, ppmPath); err == nil {
		frame++
		pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
			"final", frame, frame, "png")
		ConvertPPMtoPNG(ppmPath, pngPath)
	}

	cmd.Process.Kill()
	cmd.Wait()
	return false
}
