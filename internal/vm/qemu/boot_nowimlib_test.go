//go:build !wimlib

package qemu

import (
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assembleISOFromESD(t *testing.T, _, _ string) {
	t.Skip("assembleISOFromESD requires -tags wimlib")
}

// TestEmptyDiskBoot_StallDetected boots QEMU with an empty disk (no ISO, no
// bootable OS). The firmware finds nothing bootable and drops to the UEFI
// Interactive Shell. The StallTracker must detect the stall within 1 minute:
// the screen, disk reads, and vCPU PC all freeze once the shell prompt appears.
//
// This is the "success if stall detected" test: it validates that the stall
// detection machinery catches a VM that never progressed past the bootloader.
// The real-world failure mode: `cell build --engine=qemu` sat at the UEFI
// shell for 20 minutes before the WriteProgressTracker's window expired,
// because no richer detector was wired into the build path.
//
// Run with:
//
//	go test -run TestEmptyDiskBoot_StallDetected -timeout 5m ./internal/vm/qemu/
func TestEmptyDiskBoot_StallDetected(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots QEMU with empty disk to validate stall detection (~2 min)")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)

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

	serialLog := filepath.Join(resultsDir, "serial.log")

	spec := Spec{
		VMName:        "stall-detect-test",
		CPUs:          2,
		MemoryGB:      2,
		DiskPath:      diskPath,
		FirmwarePath:  fwPath,
		VarsPath:      varsPath,
		QMPSocketDir:  tmpDir,
		DisplayType:   "none",
		Accel:         "tcg,thread=multi",
		SerialLogPath: serialLog,
		NoReboot:      true,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)

	// Boot with BuildRunCommand (no ISO) — firmware will find nothing bootable.
	argv := BuildRunCommand(spec)
	argv[0] = qemuBin
	argv = append(argv, "-d", "guest_errors,unimp", "-D", filepath.Join(resultsDir, "qemu-guest-errors.log"))

	t.Logf("QEMU command: %v", argv)
	appendRunInfo(t, resultsDir, "test:    "+t.Name()+"\naccel:   "+spec.Accel+"\nargv:    "+strings.Join(argv, " ")+"\n")

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

	const (
		pollInterval = 10 * time.Second
		stallBudget  = 60 * time.Second
		timeout      = 3 * time.Minute
	)
	stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
	var stall StallTracker

	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		attempt++

		var pollHash uint64
		var pollRead int64
		var pollPC string

		// Screenshot hash
		os.Remove(ppmPath)
		if err := QMPScreendump(qmpSock, ppmPath); err != nil {
			t.Logf("[attempt %d] screendump failed: %v", attempt, err)
			continue
		}
		if ppmData, err := os.ReadFile(ppmPath); err == nil {
			h := fnv.New64a()
			h.Write(ppmData)
			pollHash = h.Sum64()
		}

		// Disk I/O
		if stats, err := QMPBlockStats(qmpSock); err == nil {
			for _, s := range stats {
				pollRead += s.ReadBytes
			}
		}

		// vCPU PC
		if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
			pollPC = ExtractRegister(regs, "PC=")
		}

		n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
		t.Logf("[attempt %d] hash=%016x rd=%d PC=%s stall=%d/%d",
			attempt, pollHash, pollRead, pollPC, n, stallLimit)

		if stall.Stalled(stallLimit) {
			// Save the stalled screenshot for debugging
			if _, err := os.Stat(ppmPath); err == nil {
				ConvertPPMtoPNG(ppmPath, filepath.Join(resultsDir, "stalled-last.png"))
			}
			t.Logf("stall detected after %d polls (%v) — empty-disk boot stuck at UEFI shell as expected",
				stall.Consecutive(), time.Duration(stall.Consecutive())*pollInterval)
			return // SUCCESS: stall was detected
		}
	}

	// If we get here, the stall detector did not fire — that's a test failure.
	assert.Fail(t, "stall detector did not fire within %v — an empty-disk boot should stall at the UEFI shell", timeout)
}

// TestISOBootReachesBootloader boots QEMU with the Windows ISO and asserts that
// the UEFI firmware finds and starts the boot entry — i.e. does NOT fall
// through to the EFI Interactive Shell. The test kills the VM as soon as the
// serial log shows the outcome, so it finishes in ~10 seconds.
//
// All CDs ride usb-storage on a shared xhci controller (the UTM rule for
// aarch64 `virt`). The old CDBus strategies (usb-bot, virtio-scsi) were
// removed — usb-storage is the only wiring that both EDK2 and WinPE can see.
//
//	go test -run TestISOBootReachesBootloader -timeout 5m ./internal/vm/qemu/
func TestISOBootReachesBootloader(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots QEMU with Windows ISO to check firmware device visibility (~10s)")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	isoPath := requireWindowsISO(t)

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

	serialLog := filepath.Join(resultsDir, "serial.log")

	spec := Spec{
		VMName:        "iso-boot-test",
		CPUs:          2,
		MemoryGB:      2,
		DiskPath:      diskPath,
		FirmwarePath:  fwPath,
		VarsPath:      varsPath,
		QMPSocketDir:  tmpDir,
		DisplayType:   "none",
		Accel:         "tcg,thread=multi",
		SerialLogPath: serialLog,
		NoReboot:      true,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	argv := BuildInstallCommand(spec, isoPath, "")
	argv[0] = qemuBin
	argv = append(argv, "-d", "guest_errors,unimp", "-D", filepath.Join(resultsDir, "qemu-guest-errors.log"))

	t.Logf("QEMU command: %v", argv)
	appendRunInfo(t, resultsDir, "test:    "+t.Name()+"\naccel:   "+spec.Accel+"\nargv:    "+strings.Join(argv, " ")+"\n")

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

	stop := make(chan struct{})
	defer close(stop)
	efiShellCh := WatchSerialForEFIShell(serialLog, stop)

	bootSuccess := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				data, err := os.ReadFile(serialLog)
				if err != nil {
					continue
				}
				s := string(data)
				if strings.Contains(s, "BdsDxe: starting") && !strings.Contains(s, EFIShellMarker) {
					for _, line := range strings.Split(s, "\n") {
						if strings.Contains(line, "BdsDxe: starting") {
							bootSuccess <- stripANSI(line)
							return
						}
					}
				}
			}
		}
	}()

	timeout := 90 * time.Second
	select {
	case reason := <-efiShellCh:
		if data, err := os.ReadFile(serialLog); err == nil {
			t.Logf("serial log:\n%s", string(data))
		}
		t.Fatalf("firmware dropped to EFI shell — ISO not visible via usb-storage: %s", reason)

	case device := <-bootSuccess:
		t.Logf("firmware found boot device via usb-storage: %s", device)
		if data, err := os.ReadFile(serialLog); err == nil {
			t.Logf("serial log:\n%s", string(data))
			os.WriteFile(filepath.Join(resultsDir, "serial-final.log"), data, 0644)
		}

	case <-time.After(timeout):
		if data, err := os.ReadFile(serialLog); err == nil {
			t.Logf("serial log at timeout:\n%s", string(data))
		}
		t.Fatalf("timed out after %s waiting for firmware boot decision via usb-storage", timeout)
	}
}

// TestISOBootWithStartupNSH boots QEMU with the Windows ISO and a FAT image
// carrying startup.nsh. This tests the CELL-427 recovery path: when the
// firmware's boot manager can't load the CD (QEMU 11/HVF regression), the EFI
// shell executes startup.nsh which chainloads BOOTAA64.EFI from whichever FS
// has it.
//
// On TCG/QEMU 10 the firmware boots directly and startup.nsh is never needed.
// On HVF/QEMU 11 the firmware drops to the EFI shell and startup.nsh recovers.
// Both outcomes are success: the test fails only if startup.nsh itself reports
// "BOOTAA64.EFI not found".
//
//	go test -run TestISOBootWithStartupNSH -timeout 5m ./internal/vm/qemu/
func TestISOBootWithStartupNSH(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots QEMU with startup.nsh fallback (~15s)")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	isoPath := requireWindowsISO(t)

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

	// Build a FAT image with startup.nsh — the same recovery script the
	// production build puts on the answer volume.
	startupImg := filepath.Join(tmpDir, "startup.img")
	require.NoError(t, isokit.CreateFATImage(startupImg, map[string][]byte{
		"/startup.nsh": padForFAT([]byte(startupNSH)),
	}))

	serialLog := filepath.Join(resultsDir, "serial.log")

	spec := Spec{
		VMName:        "iso-boot-nsh-test",
		CPUs:          2,
		MemoryGB:      2,
		DiskPath:      diskPath,
		FirmwarePath:  fwPath,
		VarsPath:      varsPath,
		QMPSocketDir:  tmpDir,
		DisplayType:   "none",
		SerialLogPath: serialLog,
		NoReboot:      true,
	}
	// Let ApplyDefaults resolve the accelerator — HVF on macOS, TCG on Linux.
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	argv := BuildInstallCommand(spec, isoPath, startupImg)
	argv[0] = qemuBin
	argv = append(argv, "-d", "guest_errors,unimp", "-D", filepath.Join(resultsDir, "qemu-guest-errors.log"))

	t.Logf("accel=%s  QEMU command: %v", spec.Accel, argv)
	appendRunInfo(t, resultsDir, "test:    "+t.Name()+"\naccel:   "+spec.Accel+"\nargv:    "+strings.Join(argv, " ")+"\n")

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

	stop := make(chan struct{})
	defer close(stop)

	// Success: direct boot (BdsDxe: starting without EFI shell)
	directBoot := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				data, _ := os.ReadFile(serialLog)
				s := string(data)
				if strings.Contains(s, "BdsDxe: starting") && !strings.Contains(s, EFIShellMarker) {
					for _, line := range strings.Split(s, "\n") {
						if strings.Contains(line, "BdsDxe: starting") {
							directBoot <- stripANSI(line)
							return
						}
					}
				}
			}
		}
	}()

	// Success: startup.nsh recovery (EFI shell appeared, startup.nsh ran,
	// "Searching for Windows EFI boot loader" appeared but "not found" didn't)
	nshRecovery := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		sawSearch := false
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				data, _ := os.ReadFile(serialLog)
				s := string(data)
				if !sawSearch && strings.Contains(s, "Searching for Windows EFI boot loader") {
					sawSearch = true
				}
				// Once startup.nsh started searching and enough time has
				// passed for it to complete (it's sequential FS0-FS4 checks),
				// if we haven't seen the failure marker, it found BOOTAA64.EFI.
				if sawSearch && !strings.Contains(s, StartupNSHFailMarker) {
					// Give startup.nsh 3 seconds to either succeed or fail
					time.Sleep(3 * time.Second)
					data, _ = os.ReadFile(serialLog)
					if !strings.Contains(string(data), StartupNSHFailMarker) {
						nshRecovery <- "startup.nsh chainloaded BOOTAA64.EFI"
						return
					}
				}
			}
		}
	}()

	// Failure: startup.nsh reported BOOTAA64.EFI not found
	nshFail := WatchSerialForStartupNSHFail(serialLog, stop)

	timeout := 90 * time.Second
	select {
	case device := <-directBoot:
		t.Logf("direct boot success: %s", device)

	case msg := <-nshRecovery:
		t.Logf("startup.nsh recovery success: %s", msg)

	case reason := <-nshFail:
		if data, err := os.ReadFile(serialLog); err == nil {
			t.Logf("serial log:\n%s", string(data))
		}
		t.Fatalf("startup.nsh could not find BOOTAA64.EFI: %s", reason)

	case <-time.After(timeout):
		if data, err := os.ReadFile(serialLog); err == nil {
			t.Logf("serial log at timeout:\n%s", string(data))
		}
		t.Fatalf("timed out after %s waiting for boot decision", timeout)
	}

	if data, err := os.ReadFile(serialLog); err == nil {
		t.Logf("serial log:\n%s", string(data))
		os.WriteFile(filepath.Join(resultsDir, "serial-final.log"), data, 0644)
	}
}
