package qemu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image/color"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/diag"

	"github.com/DimmKirr/devcell/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Unit tests for BluePixelRatio (always run)
// ---------------------------------------------------------------------------

func TestBluePixelRatio_AllBlue(t *testing.T) {
	dir := t.TempDir()
	ppm := filepath.Join(dir, "blue.ppm")
	// Windows Setup blue: approximately (0, 102, 204)
	writePPMP6(t, ppm, 100, 100, color.RGBA{R: 0, G: 80, B: 200, A: 255})

	ratio, err := BluePixelRatio(ppm)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, ratio, 0.01, "all-blue image should be ~100%%")
}

func TestBluePixelRatio_NoBlue(t *testing.T) {
	dir := t.TempDir()
	ppm := filepath.Join(dir, "red.ppm")
	writePPMP6(t, ppm, 100, 100, color.RGBA{R: 200, G: 50, B: 50, A: 255})

	ratio, err := BluePixelRatio(ppm)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, ratio, 0.01, "all-red image should be ~0%% blue")
}

func TestBluePixelRatio_MixedHalf(t *testing.T) {
	dir := t.TempDir()
	ppm := filepath.Join(dir, "mixed.ppm")

	// 2x1 image: one blue pixel, one white pixel
	f, err := os.Create(ppm)
	require.NoError(t, err)
	fmt.Fprintf(f, "P6\n2 1\n255\n")
	f.Write([]byte{0, 80, 200})    // blue
	f.Write([]byte{255, 255, 255}) // white
	f.Close()

	ratio, err := BluePixelRatio(ppm)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, ratio, 0.01, "half-blue image should be ~50%%")
}

func TestBluePixelRatio_Black(t *testing.T) {
	dir := t.TempDir()
	ppm := filepath.Join(dir, "black.ppm")
	writePPMP6(t, ppm, 100, 100, color.RGBA{R: 0, G: 0, B: 0, A: 255})

	ratio, err := BluePixelRatio(ppm)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, ratio, 0.01, "all-black should be 0%% blue")
}

// ---------------------------------------------------------------------------
// Unit tests for screen classification / screenshot naming (always run)
// ---------------------------------------------------------------------------

// Regression: screenshots were named `screen-%03d-blue%.0f.png`, encoding only
// the blue ratio. Windows 11 Setup measures ~1.2% blue, so the two runs that
// SUCCEEDED via the white-on-purple criterion were written as
// `screen-006-blue1.png` / `screen-007-blue1.png` — indistinguishable from a
// failure by name. That cost a full misdiagnosis cycle. The file name must say
// which criterion decided.
func TestClassifyScreen_Win11SetupIsNotNamedAfterBlue(t *testing.T) {
	// Ratios as measured on test/results/20260730T044831 screen-007, the
	// Windows 11 "Select language settings" wizard.
	v := classifyScreen(0.012, 0.73, 0.16)
	assert.Equal(t, verdictWin11UI, v, "a white wizard on a purple backdrop is a Win11 Setup pass")

	name := screenshotName(screenshotNameTestTime, 7, v, 0.012, 0.73, 0.16)
	assert.Contains(t, name, string(verdictWin11UI), "the name must state the deciding criterion")
	assert.NotContains(t, name, "-blue", "a Win11 pass must not be named as if blue decided it")
}

func TestClassifyScreen_ClassicBlueStillRecognised(t *testing.T) {
	v := classifyScreen(0.85, 0.05, 0.0)
	assert.Equal(t, verdictClassicBlue, v, "legacy Setup media must still pass on blue")
	assert.Contains(t, screenshotName(screenshotNameTestTime, 1, v, 0.85, 0.05, 0.0), string(verdictClassicBlue))
}

// install-080.png of run 20260729T190505: the TianoCore firmware splash, almost
// entirely black. It must not read as a running installer.
func TestClassifyScreen_FirmwareSplashIsNotSuccess(t *testing.T) {
	v := classifyScreen(0.0, 0.02, 0.0)
	assert.Equal(t, verdictNone, v)
	assert.Contains(t, screenshotName(screenshotNameTestTime, 80, v, 0.0, 0.02, 0.0), string(verdictNone))
}

// A fixed instant so name-format tests are deterministic.
var screenshotNameTestTime = time.Date(2026, 7, 30, 22, 15, 30, 0, time.UTC)

// All three ratios belong in the name: a frame that fails is far easier to
// triage when the numbers that were measured are visible without opening it.
func TestScreenshotName_EncodesAllThreeRatios(t *testing.T) {
	name := screenshotName(screenshotNameTestTime, 12, verdictNone, 0.01, 0.73, 0.16)
	for _, want := range []string{"b01", "w73", "p16"} {
		assert.Contains(t, name, want, "name must encode every measured ratio")
	}
	assert.True(t, strings.HasSuffix(name, ".png"), "name must stay a .png: %s", name)
}

// The name is `<tech>-<datetimeISO>-<screenName>-<id>.png`: the acquisition
// technology first (qmp screendump vs an rdp/vnc session capture — they see
// different framebuffers and must never be conflated when triaging), then
// capture time so a listing sorts chronologically within a tech. The
// timestamp is ISO 8601 basic format — no colons, safe on every filesystem.
func TestScreenshotName_TimestampFirstThenScreenNameThenID(t *testing.T) {
	name := screenshotName(screenshotNameTestTime, 12, verdictNone, 0.01, 0.73, 0.16)
	assert.Equal(t, "qmp-20260730T221530Z-none-b01-w73-p16-012.png", name)

	// The instant is stamped in UTC regardless of the caller's zone.
	inCET := screenshotNameTestTime.In(time.FixedZone("CET", 3600))
	assert.Equal(t, name, screenshotName(inCET, 12, verdictNone, 0.01, 0.73, 0.16),
		"the timestamp must be normalised to UTC")
}

// ---------------------------------------------------------------------------
// Integration: boot Windows ISO in QEMU with TCG, screenshot blue detection
// ---------------------------------------------------------------------------

// windowsBootConfigs is the executable evidence table behind the 2026-07-30
// PMU root cause. Each row is one accelerator/CPU configuration with the
// outcome the evidence demands; the rows form single-variable pairs:
//
//	config      accel              cpu                          expected
//	TCG         tcg,thread=multi   max,pauth-impdef=on (dflt)   Setup UI (~76s)
//	TCG_NoPMU   tcg,thread=multi   … same +pmu=off              park in sync-exception
//	                                                            vector (+0x200, DAIF masked)
//	KVM         kvm                max (dflt)                   Setup UI — but only on a
//	                                                            host whose KVM has a vPMU;
//	                                                            skips pre-launch otherwise
//
// TCG vs TCG_NoPMU differ in exactly one CPU feature, so together they prove
// "this media requires a PMU" with no accelerator involved. The KVM row ties
// that requirement to the host: its pre-launch ioctl (WindowsBootBlocker)
// names the missing vPMU on nested hosts, and on PMU-capable hosts it is a
// live boot assertion. Anyone doubting the PMU claim runs TCG_NoPMU.
type windowsBootConfig struct {
	accel  string
	cpu    string // "" = cpuType default for the accelerator
	expect windowsBootOutcome
}

type windowsBootOutcome int

const (
	// expectSetup: the Windows Setup UI must be detected.
	expectSetup windowsBootOutcome = iota
	// expectPMUStall: the guest must park in its synchronous-exception vector
	// — the no-PMU signature. Booting to Setup FAILS this expectation.
	expectPMUStall
)

var windowsBootConfigs = map[string]windowsBootConfig{
	"TCG":       {accel: "tcg,thread=multi", expect: expectSetup},
	"TCG_NoPMU": {accel: "tcg,thread=multi", cpu: "max,pauth-impdef=on,pmu=off", expect: expectPMUStall},
	"KVM":       {accel: "kvm", expect: expectSetup},
}

// TestWindowsISOBoot boots a Windows installer ISO in QEMU and asserts the
// installer starts by detecting the Setup UI in a screenshot.
//
// Long test: requires QEMU, UEFI firmware, and a Windows ISO (or ESD to
// assemble one). Run with:
//
//	go test -tags wimlib -run TestWindowsISOBoot/tcg -timeout 30m ./internal/vm/qemu/
//	go test -tags wimlib -run TestWindowsISOBoot/hvf -timeout 30m ./internal/vm/qemu/
//
// The ISO is resolved from (in priority order):
//  1. DEVCELL_TEST_WINDOWS_ISO env var (pre-built ISO)
//  2. Cached ISO at ~/.devcell/cache/qemu/windows-arm64-en-us.iso
//  3. DEVCELL_TEST_ESD_PATH env var → assembled on the fly (needs -tags wimlib)
func TestWindowsISOBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots Windows ISO in QEMU (~5 min)")
	}
	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}
			cfg := windowsBootConfigs["TCG"]
			if accel == "hvf" {
				cfg.accel = "hvf"
			}
			bootWindowsISO(t, cfg)
		})
	}
}

// TestWindowsISOBoot_TCG_NoPMU is the promoted form of the 2026-07-30 ad-hoc
// disproof of "Windows runs fine without a PMU": the passing TCG config with
// exactly one token added (pmu=off) must park in bootmgr's panic vector
// instead of reaching Setup. If this test ever FAILS by booting, the PMU
// requirement no longer holds (new media or QEMU behavior) and the KVM
// blocker in KVMHostCaps.WindowsBootBlocker deserves re-examination.
func TestWindowsISOBoot_TCG_NoPMU(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots Windows ISO in QEMU with TCG, PMU disabled (~2 min)")
	}
	bootWindowsISO(t, windowsBootConfigs["TCG_NoPMU"])
}

// TestWindowsISOBoot_KVM boots the same media through the same code path under
// hardware virtualization, so the accelerator is the only variable between the
// two tests.
//
// It skips rather than fails when /dev/kvm is unusable: that is a host
// property, not a defect. Getting the device requires `[cell] kvm = true` in
// .devcell.toml (which passes --device=/dev/kvm) AND the session user in the
// device's group, which the entrypoint arranges at container start.
//
//	go test -run TestWindowsISOBoot_KVM -timeout 30m ./internal/vm/qemu/
func TestWindowsISOBoot_KVM(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots Windows ISO in QEMU with KVM")
	}
	if err := ProbeKVM(); err != nil {
		t.Skipf("%s unusable (%v) — needs `[cell] kvm = true` and group membership on the device",
			KVMDevice, err)
	}
	// A usable device is not sufficient: Windows has host-capability
	// requirements KVM cannot paper over. Skip with the specific wall rather
	// than spend a stall-timeout rediscovering it — this keeps the test a live
	// assertion on capable hosts (bare-metal ARM with a PMU) and an accurate
	// explanation on incapable ones.
	if caps, err := QueryKVMHostCaps(KVMDevice); err == nil {
		t.Logf("kvm host caps: %s", caps.Summary())
		if reason := caps.WindowsBootBlocker(); reason != "" {
			t.Skipf("KVM is usable but cannot boot Windows ARM64: %s "+
				"(local proof of the PMU requirement: TestWindowsISOBoot_TCG_NoPMU)", reason)
		}
	}
	bootWindowsISO(t, windowsBootConfigs["KVM"])
}

// bootWindowsISO is the shared body of the accelerator-specific tests above.
// Keeping one body means a device-wiring or classifier change cannot silently
// apply to one configuration and not the others.
func bootWindowsISO(t *testing.T, cfg windowsBootConfig) {
	t.Helper()
	accel := cfg.accel

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	isoPath := requireWindowsISO(t)

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// Create a small qcow2 disk (UEFI needs a disk target even for ISO boot)
	diskPath := filepath.Join(tmpDir, "disk.qcow2")
	// 100G thin-provisioned: Win11 setup enforces a ~64GB minimum disk at the
	// partitioning step; qcow2 only consumes host space as the guest writes.
	out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "100G").CombinedOutput()
	if err != nil {
		out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "100G").CombinedOutput()
	}
	require.NoError(t, err, "qemu-img create: %s", out)

	varsPath := filepath.Join(tmpDir, "vars.fd")
	require.NoError(t, PrepareVarsFile(fwPath, varsPath))

	serialLog := filepath.Join(resultsDir, "serial.log")

	// Build the argv through the same code path production uses — no
	// hand-rolled device list. A divergence here is what let the broken
	// usb-storage CD wiring survive in BuildInstallCommand unnoticed.
	spec := Spec{
		VMName:        "boot-test",
		CPUs:          4,
		MemoryGB:      4,
		DiskPath:      diskPath,
		FirmwarePath:  fwPath,
		VarsPath:      varsPath,
		QMPSocketDir:  tmpDir,
		DisplayType:   "none",
		Accel:         accel,
		CPU:           cfg.cpu,
		SerialLogPath: serialLog,
		NoReboot:      true,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)
	argv := BuildInstallCommand(spec, isoPath, "")
	argv[0] = qemuBin
	// QEMU-side diagnostics. guest_errors reports invalid guest accesses (bad
	// MMIO width, writes to read-only regions) and unimp reports unimplemented
	// device functionality — both are silent otherwise, and both are prime
	// suspects for a firmware fault that happens only under KVM.
	argv = append(argv, "-d", "guest_errors,unimp", "-D", filepath.Join(resultsDir, "qemu-guest-errors.log"))
	t.Logf("serial log: %s", serialLog)
	t.Logf("accel: %s", spec.Accel)

	// Persist the decisive facts next to the screenshots. Reading the accel out
	// of stdout is not good enough: a run directory that records 40 frames but
	// not which accelerator produced them cannot be attributed afterwards
	// without re-deriving it from the code state at launch.
	updateRunJSON(t, resultsDir, map[string]any{
		"test": t.Name(), "accel": spec.Accel, "machine": machineType(spec),
		"cpu": cpuType(spec), "qemu": qemuBin, "iso": isoPath,
		"qemu-args": strings.Join(argv, " "),
	})

	t.Logf("QEMU command: %v", argv)
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

	// KVM-specific: what can this host's KVM actually give the guest?
	if kvmEnabled, _, err := QMPQueryKVM(qmpSock); err == nil && kvmEnabled {
		if caps, err := QueryKVMHostCaps(KVMDevice); err == nil {
			t.Logf("kvm host caps: %s", caps.Summary())
			updateRunJSON(t, resultsDir, map[string]any{"kvm-caps": caps.Summary()})
		} else {
			t.Logf("WARNING: kvm host caps query failed: %v", err)
		}
	}

	blockStats, err := QMPBlockStats(qmpSock)
	require.NoError(t, err, "query-blockstats after VM start")
	require.Contains(t, blockStats, "cdrom0", "installer CD-ROM not attached to VM")
	require.Contains(t, blockStats, "disk0", "target NVMe disk not attached to VM")
	if blk, err := QMPHumanMonitor(qmpSock, "info block"); err == nil {
		t.Logf("attached block devices:\n%s", blk)
	}

	// Recognition thresholds live with the classifier in screenshot.go
	// (blueThreshold / win11WhiteMin / win11PurpleMin) so the loop, the file
	// names and the unit tests cannot drift apart.
	const (
		pollInterval = 15 * time.Second
		timeout      = 10 * time.Minute
		// A guest that shows the same frame, reads nothing and never moves its
		// PC for this long is hung, not slow. Without this the deterministic
		// KVM firmware fault burned the full 10-minute deadline (602s) to
		// report what was already certain after one minute.
		stallBudget = 60 * time.Second
	)
	stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
	var stall StallTracker

	// Fallback: if the Enter spam misses cdboot's prompt window, the VM drops
	// to the EFI Shell. Relaunch cdboot from the El Torito FAT (FS0 is the
	// only filesystem EDK2 mounts — the ISO's genisoimage UDF is not
	// EDK2-readable, so there is no FS1). Retry once.
	shellCmds := []string{
		`FS0:\EFI\BOOT\BOOTAA64.EFI` + "\n",
		`FS0:\EFI\BOOT\BOOTAA64.EFI` + "\n",
	}
	shellAttempt := 0
	shellSent := false
	deadline := time.Now().Add(timeout)
	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	attempt := 0

	// cdboot from efisys.bin shows "Press any key to boot from CD or DVD..."
	// and returns EFI_TIMEOUT if nothing is pressed (~10s window), dropping
	// the VM to the EFI Shell. Spam Enter through the early boot window so
	// the El Torito path proceeds unattended. Root-caused 2026-07-29.
	go func() {
		spamDeadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(spamDeadline) {
			time.Sleep(2 * time.Second)
			_ = QMPSendKeys(qmpSock, [][]string{{"ret"}})
		}
	}()

	// Liveness telemetry: distinguish "display frozen but guest booting"
	// (virtio-gpu stops updating at ExitBootServices) from "guest hung".
	var prevStats map[string]BlockDeviceStats
	var prevScreenHash uint64
	frozenPolls := 0

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		attempt++

		// Check serial log for Shell prompt and send bootloader command.
		// Re-send on each subsequent "Shell>" (bootloader may time out,
		// and we try the next FS path).
		if logData, err := os.ReadFile(serialLog); err == nil {
			logStr := string(logData)
			shellCount := strings.Count(logStr, "Shell>")
			if shellCount > shellAttempt && shellAttempt < len(shellCmds) {
				t.Logf("EFI Shell prompt #%d detected — sending bootloader command (attempt %d)", shellCount, shellAttempt+1)
				time.Sleep(2 * time.Second)
				// Dump the firmware's device map to serial first: which
				// FS*/BLK* devices UEFI actually sees (host attachment is
				// asserted via QMP; this shows the guest-side view).
				if shellAttempt == 0 {
					if err := QMPSendKeys(qmpSock, StringToQKeyStrokes("map -r\n")); err == nil {
						time.Sleep(3 * time.Second)
					}
				}
				keystrokes := StringToQKeyStrokes(shellCmds[shellAttempt])
				if err := QMPSendKeys(qmpSock, keystrokes); err != nil {
					t.Logf("WARNING: send-key failed: %v", err)
				} else {
					t.Logf("sent %d keystrokes: %s", len(keystrokes), strings.TrimSpace(shellCmds[shellAttempt]))
					shellAttempt++
					shellSent = true
					// Answer cdboot's "Press any key" prompt after relaunch.
					for i := 0; i < 8; i++ {
						time.Sleep(2 * time.Second)
						_ = QMPSendKeys(qmpSock, [][]string{{"ret"}})
					}
				}
			}
		}

		os.Remove(ppmPath)
		if err := QMPScreendump(qmpSock, ppmPath); err != nil {
			t.Logf("[attempt %d] screendump failed: %v", attempt, err)
			continue
		}

		if info, _ := os.Stat(ppmPath); info == nil || info.Size() == 0 {
			t.Logf("[attempt %d] empty screenshot", attempt)
			continue
		}

		ratio, err := BluePixelRatio(ppmPath)
		if err != nil {
			t.Logf("[attempt %d] pixel analysis failed: %v", attempt, err)
			continue
		}
		white, _ := WhitePixelRatio(ppmPath)
		purple, _ := WindowsPurpleRatio(ppmPath)

		t.Logf("[attempt %d] blue=%.1f%% white=%.1f%% purple=%.1f%% (shell_sent=%v)",
			attempt, ratio*100, white*100, purple*100, shellSent)

		// Signals for this poll, fed to the stall detector below.
		var pollHash uint64
		var pollRead int64
		var pollPC string

		// Display-freeze detection: hash the raw screendump.
		if ppmData, err := os.ReadFile(ppmPath); err == nil {
			h := fnv.New64a()
			h.Write(ppmData)
			screenHash := h.Sum64()
			if screenHash == prevScreenHash {
				frozenPolls++
			} else {
				frozenPolls = 0
			}
			prevScreenHash = screenHash
			pollHash = screenHash
			t.Logf("[attempt %d] display: hash=%016x unchanged_polls=%d", attempt, screenHash, frozenPolls)
		}

		// Guest liveness: disk I/O counters + vCPU program counter.
		if stats, err := QMPBlockStats(qmpSock); err != nil {
			t.Logf("[attempt %d] blockstats failed: %v", attempt, err)
		} else {
			for _, dev := range []string{"cdrom0", "disk0"} {
				cur, ok := stats[dev]
				if !ok {
					continue
				}
				var delta BlockDeviceStats
				if prev, ok := prevStats[dev]; ok {
					delta = BlockDeviceStats{
						ReadBytes:  cur.ReadBytes - prev.ReadBytes,
						ReadOps:    cur.ReadOps - prev.ReadOps,
						WriteBytes: cur.WriteBytes - prev.WriteBytes,
					}
				}
				t.Logf("[attempt %d] io %s: rd=%d (+%d) rd_ops=%d (+%d) wr=%d (+%d)",
					attempt, dev,
					cur.ReadBytes, delta.ReadBytes,
					cur.ReadOps, delta.ReadOps,
					cur.WriteBytes, delta.WriteBytes)
			}
			for _, st := range stats {
				pollRead += st.ReadBytes
			}
			prevStats = stats
		}
		if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err != nil {
			t.Logf("[attempt %d] info registers failed: %v", attempt, err)
		} else if i := strings.Index(regs, "PC="); i >= 0 {
			end := i + 3
			for end < len(regs) && regs[end] != ' ' && regs[end] != '\n' {
				end++
			}
			pollPC = regs[i+3 : end]
			t.Logf("[attempt %d] vcpu PC=%s", attempt, pollPC)
		}

		// Fail fast on a hung guest. All three signals must be static: a
		// blanked-but-live display, or a quiet disk on a running guest, is not
		// a stall — only a PC that never moves alongside them is.
		if n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC}); n > 0 {
			t.Logf("[attempt %d] stall: %d/%d consecutive static polls", attempt, n, stallLimit)
		}
		if stall.Stalled(stallLimit) {
			if _, err := os.Stat(ppmPath); err == nil {
				ConvertPPMtoPNG(ppmPath, filepath.Join(resultsDir, "stalled-last.png"))
			}
			interp := captureStallDiagnostics(t, qmpSock, resultsDir, spec)
			if cfg.expect == expectPMUStall {
				// The stall is the expected outcome — but only THIS stall: a
				// synchronous-exception park with interrupts fully masked. A
				// generic hang (wrong slot, DAIF clear) would be a different
				// bug wearing the same timeout.
				requireNoPMUStallSignature(t, pollPC, interp)
				t.Logf("EXPECTED no-PMU stall confirmed after %v: %s",
					time.Duration(stallLimit)*pollInterval, interp)
				return // SUCCESS for expectPMUStall
			}
			t.Fatalf("guest hung after %v: %d consecutive polls with an unchanged frame, "+
				"zero bytes read (rd=%d) and a static PC=%s.\n  %s\nFull dump: %s",
				time.Duration(stallLimit)*pollInterval, stall.Consecutive(), pollRead, pollPC,
				interp, filepath.Join(resultsDir, "stall-diagnostics.txt"))
		}

		// Classify BEFORE naming the file, so the name records which criterion
		// decided rather than a ratio that did not.
		verdict := classifyScreen(ratio, white, purple)
		pngPath := filepath.Join(resultsDir, screenshotName(time.Now(), attempt, verdict, ratio, white, purple))
		if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
			t.Logf("  saved: %s", pngPath)
		}

		switch verdict {
		case verdictClassicBlue, verdictWin11UI:
			if cfg.expect == expectPMUStall {
				t.Fatalf("Windows booted to Setup (%s) despite the configuration expected to stall "+
					"(cpu=%q). The PMU requirement no longer holds for this media/QEMU — "+
					"re-examine KVMHostCaps.WindowsBootBlocker before trusting its skip.",
					verdict, cpuType(spec))
			}
			if verdict == verdictClassicBlue {
				t.Logf("Windows installer detected: %.1f%% blue pixels (threshold %.0f%%)", ratio*100, blueThreshold*100)
			} else {
				// Windows 11 Setup is a large white wizard window on the purple
				// backdrop (real UI measures ~73% white / ~16% purple) — the
				// classic blue criterion never fires on it (peaks ~1.2%).
				t.Logf("Windows 11 Setup UI detected: %.1f%% white window on %.1f%% purple backdrop", white*100, purple*100)
			}
			return // SUCCESS for expectSetup
		}
	}

	// Save final screenshot on timeout
	if _, err := os.Stat(ppmPath); err == nil {
		finalPNG := filepath.Join(resultsDir, "timeout-last.png")
		ConvertPPMtoPNG(ppmPath, finalPNG)
		t.Logf("timeout screenshot: %s", finalPNG)
	}

	t.Fatalf("timed out after %v waiting for Windows installer; no frame satisfied either criterion "+
		"(legacy: blue >= %.0f%%; Windows 11: white >= %.0f%% AND purple >= %.0f%%). Screenshots in %s — "+
		"each is named with its verdict and measured b/w/p ratios",
		timeout, blueThreshold*100, win11WhiteMin*100, win11PurpleMin*100, resultsDir)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func requireQEMUBin(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("qemu-system-aarch64"); err == nil {
		return p
	}
	t.Skip("qemu-system-aarch64 not found — install QEMU")
	return ""
}

func requireFirmware(t *testing.T) string {
	t.Helper()
	if override := os.Getenv("QEMU_FIRMWARE_OVERRIDE"); override != "" {
		if _, err := os.Stat(override); err != nil {
			t.Fatalf("QEMU_FIRMWARE_OVERRIDE=%s: %v", override, err)
		}
		t.Logf("using firmware override: %s", override)
		return override
	}
	fw := FirmwarePath()
	if _, err := os.Stat(fw); err != nil {
		t.Skipf("UEFI firmware not found at %s — install QEMU", fw)
	}
	return fw
}

func requireWindowsISO(t *testing.T) string {
	t.Helper()

	// 1. Explicit env var (pre-built ISO)
	if p := os.Getenv("DEVCELL_TEST_WINDOWS_ISO"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("DEVCELL_TEST_WINDOWS_ISO=%s: %v", p, err)
		}
		return p
	}

	// 2. Assemble from ESD on the fly (needs -tags wimlib + genisoimage/hdiutil)
	if esdPath := os.Getenv("DEVCELL_TEST_ESD_PATH"); esdPath != "" {
		if _, err := os.Stat(esdPath); err != nil {
			t.Fatalf("DEVCELL_TEST_ESD_PATH=%s: %v", esdPath, err)
		}
		isoPath := filepath.Join(t.TempDir(), "windows-arm64.iso")
		assembleISOFromESD(t, esdPath, isoPath)
		return isoPath
	}

	// 3. Download via MCT catalog / UUP dump (uses cache)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	path, err := DownloadWindowsISO(t.Context(), home, "en-us", false, NopObserver{})
	if err != nil {
		t.Skipf("could not obtain Windows ISO: %v", err)
	}
	return path
}

func repoRoot(_ *testing.T) string {
	return testutil.RepoRoot()
}

func testResultsDir(t *testing.T) string {
	t.Helper()
	return testutil.TestResultsDir(t, nil)
}

func waitForSocket(t *testing.T, sockPath string, timeout time.Duration, ql *qemuLog) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("QMP socket %s did not appear within %v%s", sockPath, timeout, qemuLaunchHint(ql))
}

type qemuLog struct {
	bytes.Buffer
}

// qemuOutput tees QEMU's stdout+stderr into a buffer and the test log.
// On cleanup it writes the captured output into run.json["qemu-output"].
func qemuOutput(t *testing.T, resultsDir string, argv []string) *qemuLog {
	t.Helper()
	ql := &qemuLog{}
	t.Cleanup(func() {
		if ql.Len() > 0 {
			updateRunJSON(t, resultsDir, map[string]any{
				"qemu-output": ql.String(),
			})
		}
	})
	return ql
}

func (ql *qemuLog) Write(p []byte) (int, error) {
	os.Stderr.Write(p)
	return ql.Buffer.Write(p)
}

// qemuLaunchHint turns a bare QMP timeout into the actual reason when QEMU
// failed at launch rather than hanging.
func qemuLaunchHint(ql *qemuLog) string {
	if ql == nil || ql.Len() == 0 {
		return ""
	}
	output := ql.String()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Could not set up host forwarding rule") {
			return "\n  QEMU never started: a forwarded host port was already in use:\n  " + strings.TrimSpace(line)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "qemu-system") {
			return "\n  QEMU reported at launch:\n  " + strings.TrimSpace(line)
		}
	}
	return ""
}

// updateRunJSON merges fields into resultsDir/run.json. It reads the existing
// file (if any), applies the updates, and writes it back. Safe for incremental
// additions (argv at launch, query-kvm after boot).
func updateRunJSON(t *testing.T, resultsDir string, fields map[string]any) {
	t.Helper()
	path := filepath.Join(resultsDir, "run.json")
	data := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &data)
	}
	for k, v := range fields {
		data[k] = v
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Logf("WARNING: could not marshal run.json: %v", err)
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Logf("WARNING: could not write run.json: %v", err)
	}
}

// assertAccel proves via QMP that the running VM uses the expected accelerator.
// query-kvm only reports KVM state — HVF returns enabled=false because it is
// a separate accelerator. For HVF we verify KVM is NOT enabled (QEMU exits if
// the requested accelerator is unavailable, so a live VM is sufficient proof).
func assertAccel(t *testing.T, qmpSock, requestedAccel, resultsDir string) {
	t.Helper()
	kvmEnabled, kvmPresent, err := QMPQueryKVM(qmpSock)
	if err != nil {
		t.Logf("WARNING: query-kvm failed: %v", err)
		return
	}
	t.Logf("query-kvm: enabled=%v present=%v (requested %s)", kvmEnabled, kvmPresent, requestedAccel)
	updateRunJSON(t, resultsDir, map[string]any{"query-kvm": fmt.Sprintf("enabled=%v present=%v", kvmEnabled, kvmPresent)})

	switch {
	case strings.HasPrefix(requestedAccel, "kvm"):
		require.True(t, kvmEnabled,
			"asked for -accel kvm but query-kvm reports enabled=false (present=%v)", kvmPresent)
	case requestedAccel == "hvf":
		require.False(t, kvmEnabled,
			"asked for -accel hvf but query-kvm reports enabled=true — unexpected KVM when HVF was requested")
		t.Logf("HVF active — query-kvm correctly reports enabled=false (HVF is not KVM)")
	default:
		require.False(t, kvmEnabled,
			"asked for -accel %s but query-kvm reports enabled=true — expected TCG (software emulation)", requestedAccel)
	}
}

// captureStallDiagnostics dumps everything the live VM can still tell us at the
// moment of the hang, and returns a one-line interpretation for the failure
// message.
//
// Written to a file rather than only the test log: the 10-minute KVM runs that
// preceded this left directories full of screenshots and nothing explaining
// them, so the analysis had to be re-derived from the code state at launch.
//
// Each dump targets one question about the KVM firmware hang:
//   - PSTATE decode: fatal-exception dead loop, or a WFI idle?
//   - disassembly at PC: `b .` self-branch, or a wild branch into data?
//   - disassembly at LR: which caller got there.
//   - info registers -a: is every vCPU stuck, or only vCPU 0?
//   - info mtree -f: does the guest-physical map differ from the TCG run
//     (the "ConvertPages: failed to find range" lead).
//   - info pci / info block: device state at the fault.
func captureStallDiagnostics(t *testing.T, qmpSock, resultsDir string, spec Spec) string {
	t.Helper()

	var b strings.Builder
	fmt.Fprintf(&b, "accel:   %s\nmachine: %s\ncpu:     %s\n\n",
		spec.Accel, machineType(spec), cpuType(spec))

	regs, regErr := QMPHumanMonitor(qmpSock, "info registers")
	if regErr != nil {
		fmt.Fprintf(&b, "info registers FAILED: %v\n", regErr)
	}

	interp := "PSTATE unavailable — cannot tell a dead loop from a WFI idle"
	pc := diag.ExtractRegister(regs, "PC=")
	lr := diag.ExtractRegister(regs, "X30=")
	pcVal, pcValErr := strconv.ParseUint(pc, 16, 64)
	if ps := diag.ExtractRegister(regs, "PSTATE="); ps != "" {
		if decoded, err := DecodePSTATE(ps); err == nil {
			interp = decoded.Summary()
			// A masked-DAIF park at a vector-shaped offset names the
			// exception class: +0x200 = a synchronous exception taken at the
			// current EL on SP_ELx — the KVM-hang signature.
			if decoded.AllInterruptsMasked() && pcValErr == nil {
				base, slot := ExceptionVectorSlot(pcVal)
				interp += fmt.Sprintf("; PC sits at slot %s of an 0x800-aligned vector frame (VBAR would be 0x%x)", slot, base)
			}
		} else {
			interp = fmt.Sprintf("PSTATE=%s undecodable: %v", ps, err)
		}
	}
	fmt.Fprintf(&b, "INTERPRETATION: %s\n", interp)
	t.Logf("stall interpretation: %s", interp)

	dumps := []struct{ label, hmp string }{
		{"info registers (vCPU 0)", "info registers"},
		{"info registers -a (all vCPUs — is only one stuck?)", "info registers -a"},
	}
	if pc != "" {
		dumps = append(dumps, struct{ label, hmp string }{
			"disassembly at PC 0x" + pc, "x/16i 0x" + pc})
	}
	if lr != "" {
		dumps = append(dumps, struct{ label, hmp string }{
			"disassembly at LR 0x" + lr + " (caller)", "x/8i 0x" + lr})
	}
	if pcValErr == nil {
		// If the parked PC really is a vector slot, sibling slots of the same
		// 0x800 frame should hold the same `b .` stubs. Three probes tell a
		// minimal panic-vector table apart from a coincidental address.
		base := pcVal &^ 0x7FF
		for _, off := range []uint64{0x000, 0x200, 0x400} {
			addr := base + off
			dumps = append(dumps, struct{ label, hmp string }{
				fmt.Sprintf("assumed vector table, slot +0x%03x (0x%x)", off, addr),
				fmt.Sprintf("x/2i 0x%x", addr)})
		}
	}
	dumps = append(dumps,
		struct{ label, hmp string }{"info mtree -f (guest-physical map)", "info mtree -f"},
		struct{ label, hmp string }{"info pci", "info pci"},
		struct{ label, hmp string }{"info block", "info block"},
		struct{ label, hmp string }{"info status", "info status"},
	)

	for _, d := range dumps {
		fmt.Fprintf(&b, "\n===== %s =====\n", d.label)
		out, err := QMPHumanMonitor(qmpSock, d.hmp)
		if err != nil {
			fmt.Fprintf(&b, "FAILED: %v\n", err)
			continue
		}
		b.WriteString(strings.TrimRight(out, "\n") + "\n")
	}

	path := filepath.Join(resultsDir, "stall-diagnostics.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Logf("WARNING: could not write %s: %v", path, err)
		t.Logf("stall diagnostics:\n%s", b.String())
	}
	if pc != "" {
		if dis, err := QMPHumanMonitor(qmpSock, "x/4i 0x"+pc); err == nil {
			t.Logf("instructions at PC 0x%s:\n%s", pc, strings.TrimRight(dis, "\n"))
		}
	}
	return interp
}

// requireNoPMUStallSignature asserts the stall is the specific no-PMU death,
// not merely "some hang": the PC parked in the synchronous-exception slot
// (+0x200 of an 0x800-aligned vector frame) with all of DAIF masked. Measured
// on both occurrences: KVM PC=0x13c347200, TCG+pmu=off PC=0x4064e200 —
// different load addresses, same slot.
func requireNoPMUStallSignature(t *testing.T, pc, interp string) {
	t.Helper()
	pcVal, err := strconv.ParseUint(pc, 16, 64)
	require.NoError(t, err, "stalled without a parseable PC (%q)", pc)
	require.Equal(t, uint64(0x200), pcVal&0x7FF,
		"parked PC 0x%x is not in the sync-exception vector slot — a different failure than the no-PMU death", pcVal)
	require.Contains(t, interp, "DAIF=all masked",
		"a park with interrupts enabled is an idle, not the no-PMU dead loop")
}

// testResultsDir must answer "this run's directory", not "a new directory".
//
// It used to stamp time.Now() on every call, so calling it twice in one test
// scattered that run's artifacts across two directories seconds apart —
// observed on 2026-07-31 as 20260731T140208-… holding the screenshots while
// 20260731T140210-… held the live logs. Fixing the caller is not enough: the
// next second caller reintroduces it.
func TestTestResultsDir_IsStableWithinOneTest(t *testing.T) {
	// Clean up after itself: this asserts on a helper, and a helper test has no
	// business leaving empty directories in the shared results tree that real
	// runs write to. Seven accumulated before it was noticed.
	t.Cleanup(func() { os.RemoveAll(testResultsDir(t)) })

	first := testResultsDir(t)
	time.Sleep(1100 * time.Millisecond) // cross a timestamp boundary
	second := testResultsDir(t)

	assert.Equal(t, first, second, "one test, one results directory")
}

// Different tests still get their own.
func TestTestResultsDir_DiffersBetweenTests(t *testing.T) {
	t.Cleanup(func() { os.RemoveAll(testResultsDir(t)) })

	mine := testResultsDir(t)

	assert.Contains(t, mine, t.Name(), "the directory must name the test that produced it")
	assert.NotContains(t, mine, "IsStableWithinOneTest")
}
