package qemu

import (
	"fmt"
	"image/color"
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
	f.Write([]byte{0, 80, 200}) // blue
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
// Integration: boot Windows ISO in QEMU with TCG, screenshot blue detection
// ---------------------------------------------------------------------------

// TestWindowsISOBoot_TCG boots a Windows installer ISO in QEMU with software
// emulation (TCG) and asserts the installer starts by detecting >20% blue
// pixels in a screenshot.
//
// Long test: requires QEMU, UEFI firmware, and a Windows ISO (or ESD to
// assemble one). Run with:
//
//	go test -tags wimlib -run TestWindowsISOBoot_TCG -timeout 30m ./internal/vm/qemu/
//
// The ISO is resolved from (in priority order):
//  1. DEVCELL_TEST_WINDOWS_ISO env var (pre-built ISO)
//  2. Cached ISO at ~/.devcell/cache/qemu/windows-arm64-en-us.iso
//  3. DEVCELL_TEST_ESD_PATH env var → assembled on the fly (needs -tags wimlib)
func TestWindowsISOBoot_TCG(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots Windows ISO in QEMU with TCG (~5 min)")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	isoPath := requireWindowsISO(t)

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// Create a small qcow2 disk (UEFI needs a disk target even for ISO boot)
	diskPath := filepath.Join(tmpDir, "disk.qcow2")
	out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	if err != nil {
		out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	}
	require.NoError(t, err, "qemu-img create: %s", out)

	varsPath := filepath.Join(tmpDir, "vars.fd")
	require.NoError(t, PrepareVarsFile(fwPath, varsPath))

	qmpSock := filepath.Join(tmpDir, "qmp.sock")

	serialLog := filepath.Join(resultsDir, "serial.log")
	argv := []string{
		qemuBin,
		"-machine", "virt,virtualization=true",
		"-cpu", "max,pauth-impdef=on",
		"-accel", "tcg,thread=multi",
		"-smp", "4",
		"-m", "4G",
		"-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", fwPath),
		"-drive", fmt.Sprintf("if=pflash,format=raw,file=%s", varsPath),
		"-drive", fmt.Sprintf("if=virtio,format=qcow2,file=%s", diskPath),
		"-device", "virtio-scsi-pci,id=scsi0",
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", isoPath),
		"-device", "scsi-cd,drive=cdrom0,bus=scsi0.0,bootindex=0",
		"-device", "qemu-xhci,p2=8",
		"-device", "usb-kbd",
		"-device", "virtio-gpu-pci",
		"-display", "none",
		"-serial", fmt.Sprintf("file:%s", serialLog),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", qmpSock),
		"-no-reboot",
	}
	t.Logf("serial log: %s", serialLog)

	t.Logf("QEMU command: %v", argv)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start(), "starting QEMU")

	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	waitForSocket(t, qmpSock, 30*time.Second)

	const (
		pollInterval = 15 * time.Second
		timeout      = 10 * time.Minute
		threshold    = 0.20 // 20% blue pixels
	)

	// Phase 1: Wait for UEFI Shell prompt (watchdog kills bootloader under
	// TCG, UEFI falls back to EFI Shell). Then type the bootloader command.
	// Try FS0 first (CD-ROM), fall back to FS1 if needed.
	shellCmds := []string{
		`FS0:\EFI\BOOT\BOOTAA64.EFI` + "\n",
		`FS1:\EFI\BOOT\BOOTAA64.EFI` + "\n",
	}
	shellAttempt := 0
	shellSent := false
	deadline := time.Now().Add(timeout)
	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	attempt := 0

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
				keystrokes := StringToQKeyStrokes(shellCmds[shellAttempt])
				if err := QMPSendKeys(qmpSock, keystrokes); err != nil {
					t.Logf("WARNING: send-key failed: %v", err)
				} else {
					t.Logf("sent %d keystrokes: %s", len(keystrokes), strings.TrimSpace(shellCmds[shellAttempt]))
					shellAttempt++
					shellSent = true
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

		t.Logf("[attempt %d] blue pixels: %.1f%% (shell_sent=%v)", attempt, ratio*100, shellSent)

		pngName := fmt.Sprintf("screen-%03d-blue%.0f.png", attempt, ratio*100)
		pngPath := filepath.Join(resultsDir, pngName)
		if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
			t.Logf("  saved: %s", pngPath)
		}

		if ratio >= threshold {
			t.Logf("Windows installer detected: %.1f%% blue pixels (threshold %.0f%%)", ratio*100, threshold*100)
			return // SUCCESS
		}
	}

	// Save final screenshot on timeout
	if _, err := os.Stat(ppmPath); err == nil {
		finalPNG := filepath.Join(resultsDir, "timeout-last.png")
		ConvertPPMtoPNG(ppmPath, finalPNG)
		t.Logf("timeout screenshot: %s", finalPNG)
	}

	t.Fatalf("timed out after %v waiting for Windows installer (expected >%.0f%% blue pixels); screenshots in %s",
		timeout, threshold*100, resultsDir)
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

	// 2. Cached ISO (from `cell init --engine=qemu`)
	home, _ := os.UserHomeDir()
	if home != "" {
		cached := WindowsISOPath(home, "en-us")
		if info, err := os.Stat(cached); err == nil && info.Size() > 1024*1024 {
			return cached
		}
	}

	// 3. Assemble from ESD on the fly (needs -tags wimlib + genisoimage/hdiutil)
	if esdPath := os.Getenv("DEVCELL_TEST_ESD_PATH"); esdPath != "" {
		if _, err := os.Stat(esdPath); err != nil {
			t.Fatalf("DEVCELL_TEST_ESD_PATH=%s: %v", esdPath, err)
		}
		isoPath := filepath.Join(t.TempDir(), "windows-arm64.iso")
		assembleISOFromESD(t, esdPath, isoPath)
		return isoPath
	}

	t.Skip("no Windows ISO available — set DEVCELL_TEST_WINDOWS_ISO, " +
		"DEVCELL_TEST_ESD_PATH, or run `cell init --engine=qemu`")
	return ""
}

func testResultsDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find project root (go.mod) from %s", file)
		}
		root = parent
	}
	stamp := time.Now().Format("20060102T150405")
	dir := filepath.Join(root, "test", "results", stamp+"-"+t.Name())
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Logf("test results: %s", dir)
	return dir
}

func waitForSocket(t *testing.T, sockPath string, timeout time.Duration) {
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
	t.Fatalf("QMP socket %s did not appear within %v", sockPath, timeout)
}
