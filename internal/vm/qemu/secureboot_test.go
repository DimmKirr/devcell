package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// qemuEFIFirmware returns the self-contained EDK2 image for -kernel loading.
func qemuEFIFirmware(t *testing.T) string {
	t.Helper()
	path := os.Getenv("DEVCELL_QEMU_EFI")
	if path == "" {
		t.Skip("set DEVCELL_QEMU_EFI to QEMU_EFI.fd (nix build nixpkgs#OVMF.fd → FV/QEMU_EFI.fd)")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("DEVCELL_QEMU_EFI=%s: %v", path, err)
	}
	return path
}

// captureViaVNC pulls the *guest's* framebuffer straight off QEMU's VNC
// server. The earlier version screenshotted the host X root, which captured
// the container desktop with a viewer window in it — the VM's screen was a
// rectangle inside a picture of something else. A VNC client that writes the
// framebuffer to a file has no host desktop in it at all, and is a second
// transport independent of QMP screendump (which once reported QEMU's own
// placeholder as guest output).
func captureViaVNC(t *testing.T, spec Spec, resultsDir string, seq int) {
	t.Helper()
	vncdo := os.Getenv("DEVCELL_VNCDO")
	if vncdo == "" || spec.VNCPort == 0 {
		t.Logf("VNC capture skipped (vncdo=%q vncPort=%d)", vncdo, spec.VNCPort)
		return
	}
	shot := filepath.Join(resultsDir, fmt.Sprintf("vnc-guest-%02d.png", seq))
	// vncdotool addresses displays as host::port.
	cmd := exec.Command(vncdo, "-s", fmt.Sprintf("127.0.0.1::%d", spec.VNCPort), "capture", shot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("VNC capture failed: %v\n%s", err, out)
		return
	}
	if info, err := os.Stat(shot); err == nil {
		t.Logf("VNC guest capture: %s (%d bytes) — guest framebuffer only", shot, info.Size())
	}
}

// litPixelRatio is the fraction of pixels that are not near-black — the
// cheapest possible "did anything draw?" signal.
func litPixelRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool { return r+g+b > 90 })
}

// TestWindowsInstall_SecureBoot answers one question: with secure=on, does the
// machine get anywhere at all?
//
// The dev-env run that first tried secure=on measured only "SSH never came
// back" (116 polls, 26 minutes, no stage ever started) and left the cause
// inferred rather than seen. The reasoning was that our pflash firmware —
// edk2-aarch64-code.fd, a normal-world UEFI — becomes the *secure world's*
// firmware under secure=on and is entered at EL3, which it has no code to
// handle. Plausible, but never observed.
//
// This test observes it. It boots the Windows *installer* (not an installed
// disk, so no NVRAM boot entry is involved — a disk that never boots and
// firmware that never runs would otherwise look identical) on a secure
// machine, and watches two independent signals:
//
//   - block reads from the installer CD: firmware got far enough to enumerate
//     and read a device
//   - screenshots: anything other than a black screen means the firmware, and
//     then the bootloader, produced output
//
// Both silent for the whole window means the failure is at firmware entry,
// exactly as theorised — and then TF-A (or the -kernel loading path) is the
// only way forward, not a Windows-side fix.
//
//	DEVCELL_TEST_SECUREBOOT=1 go test -run TestWindowsInstall_SecureBoot -timeout 1h -v ./internal/vm/qemu/
func TestWindowsInstall_SecureBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots a Windows installer under TCG")
	}
	if os.Getenv("DEVCELL_TEST_SECUREBOOT") == "" {
		t.Skip("set DEVCELL_TEST_SECUREBOOT=1 to run the secure-world boot probe")
	}
	requireQEMUBin(t)
	isoPath := requireWindowsISO(t)

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	// A blank disk: this probe is about firmware, not about installing.
	disk := filepath.Join(workDir, "secureboot-probe.qcow2")
	require.NoError(t, CreateDisk(disk, 8))
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, PrepareVarsFile(FirmwarePath(), varsPath))

	spec := Spec{
		VMName:   "devcell-secureboot-probe",
		CPUs:     4,
		MemoryGB: 4,
		DiskPath: disk,
		// QEMU_EFI.fd, not edk2-aarch64-code.fd: -kernel needs a
		// self-contained image (3 MB), while the pflash CODE half is a padded
		// 64 MB flash map with nothing loadable at its entry — feeding it to
		// -kernel produced total silence on both UARTs.
		FirmwarePath:  qemuEFIFirmware(t),
		VarsPath:      varsPath,
		SSHHost:       "127.0.0.1",
		SSHPort:       freeTCPPort(10422),
		QMPSocketDir:  workDir,
		DiskCacheMode: "unsafe",
		// The configuration reported working for Hyper-V/WSL2 on ARM64
		// ("tested with Build 25931"): secure world, firmware via -kernel so
		// QEMU's stub owns the EL3 entry, a concrete CPU rather than max, and
		// virtio-scsi for the system disk.
		SecureWorld:    true,
		FirmwareKernel: true,
		CPU:            "neoverse-n1",
		DiskBus:        "scsi",
		SerialLogPath:  filepath.Join(resultsDir, "secureboot-serial-ns.log"),
		// A VNC server on the same console QMP screendump reads. It adds a
		// live view for a human; it cannot reveal anything screendump misses,
		// since both render the identical framebuffer.
		VNCPort: freeTCPPort(5905),
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	argv := BuildInstallCommand(spec, isoPath, "")
	// secure=on adds a Secure-World-only PL011; firmware entered at EL3 writes
	// there, so capture both UARTs or the decisive output is invisible.
	argv = append(argv, "-serial", "file:"+filepath.Join(resultsDir, "secureboot-serial-secure.log"))
	t.Logf("booting secure-world machine: %s", strings.Join(argv, " "))
	require.Contains(t, strings.Join(argv, " "), "secure=on",
		"the probe is meaningless without the flag under test")

	exclusiveQEMU(t)
	cmd := exec.Command(argv[0], argv[1:]...)
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}()

	// A machine that boots shows something well inside this window: the
	// non-secure control run reached the installer in ~2 minutes.
	const probeWindow = 12 * time.Minute
	qmpSock := QMPSocketPath(spec)
	ppm := filepath.Join(workDir, "screen.ppm")

	var sawCDReads, sawPixels bool
	deadline := time.Now().Add(probeWindow)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		time.Sleep(30 * time.Second)
		if _, err := os.Stat(qmpSock); err != nil {
			t.Logf("[%02d] QMP socket not up yet", attempt)
			continue
		}
		if stats, err := QMPBlockStats(qmpSock); err == nil {
			for dev, s := range stats {
				if strings.Contains(dev, "cdrom") && s.ReadBytes > 0 {
					if !sawCDReads {
						t.Logf("[%02d] firmware read the installer CD: %s %d bytes", attempt, dev, s.ReadBytes)
					}
					sawCDReads = true
				}
			}
		}
		if err := QMPScreendump(qmpSock, ppm); err == nil {
			png := filepath.Join(resultsDir, fmt.Sprintf("secureboot-%02d.png", attempt))
			_ = ConvertPPMtoPNG(ppm, png)
			// Any non-black pixel is the signal: firmware that never runs
			// leaves the framebuffer blank.
			if lit, ratioErr := litPixelRatio(ppm); ratioErr == nil && lit > 0.001 {
				if !sawPixels {
					t.Logf("[%02d] screen is no longer blank (%.3f%% lit) — firmware produced output",
						attempt, lit*100)
				}
				sawPixels = true
			}
		}
		captureViaVNC(t, spec, resultsDir, attempt)
		if sawCDReads && sawPixels {
			break
		}
	}

	// Serial is the primary signal. Pixels are not: QEMU draws its own
	// "Guest has not initialized the display (yet)" placeholder, and counting
	// those glyphs as guest output passed this test while firmware had in fact
	// never run (run 20260801T173031, both UARTs 0 bytes).
	var serialBytes int
	for _, log := range []string{"secureboot-serial-ns.log", "secureboot-serial-secure.log"} {
		data, _ := os.ReadFile(filepath.Join(resultsDir, log))
		serialBytes += len(data)
		if len(data) > 0 {
			t.Logf("%s (%d bytes):\n%s", log, len(data), tailLines(string(data), 25))
		} else {
			t.Logf("%s: empty — nothing was written to this UART", log)
		}
	}

	captureViaVNC(t, spec, resultsDir, 99)

	t.Logf("secure-world probe: CD reads=%v, serial bytes=%d, lit pixels=%v",
		sawCDReads, serialBytes, sawPixels)
	require.True(t, sawCDReads || serialBytes > 0,
		"nothing happened in %s under secure=on: no CD read, not one UART byte — "+
			"the normal-world EDK2 in pflash cannot serve as secure-world firmware, "+
			"so this needs TF-A (BL1+FIP via -bios) or the -kernel loading path, "+
			"not a Windows-side change", probeWindow)
}

// TestSecureBoot_FirmwareMatrix finds the simplest combination that actually
// starts firmware with a secure world, instead of changing one variable per
// multi-hour run.
//
// Three loading modes exist and they are not interchangeable:
//
//	-bios    the firmware image is the boot ROM (how QEMU_EFI.fd is normally used)
//	-kernel  QEMU's boot stub loads it and owns the EL3 entry
//	pflash   the image is mapped as flash; under secure=on it becomes the
//	         SECURE world's firmware and is entered at EL3
//
// Each case gets a short window and is judged on evidence only: bytes on
// either UART, or reads from the installer CD. A control case with no secure
// world proves the harness itself can see a working boot.
//
//	DEVCELL_TEST_SECUREBOOT=1 DEVCELL_QEMU_EFI=…/QEMU_EFI.fd \
//	  go test -run TestSecureBoot_FirmwareMatrix -timeout 90m -v ./internal/vm/qemu/
func TestSecureBoot_FirmwareMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots several firmware/machine combinations")
	}
	if os.Getenv("DEVCELL_TEST_SECUREBOOT") == "" {
		t.Skip("set DEVCELL_TEST_SECUREBOOT=1")
	}
	requireQEMUBin(t)
	isoPath := requireWindowsISO(t)
	efi := qemuEFIFirmware(t)
	resultsDir := testResultsDir(t)

	cases := []struct {
		name    string
		machine string
		load    string // bios | kernel | pflash
		fw      string
	}{
		{"control-no-secure-bios", "virt,virtualization=on,gic-version=3", "bios", efi},
		{"secure-only-bios", "virt,secure=on", "bios", efi},
		{"secure-virt-gic3-bios", "virt,virtualization=on,gic-version=3,secure=on", "bios", efi},
		{"secure-virt-gic3-kernel", "virt,virtualization=on,gic-version=3,secure=on", "kernel", efi},
		{"secure-virt-gic3-pflash", "virt,virtualization=on,gic-version=3,secure=on", "pflash", FirmwarePath()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			disk := filepath.Join(work, "d.qcow2")
			require.NoError(t, CreateDisk(disk, 8))
			nsLog := filepath.Join(resultsDir, tc.name+"-serial-ns.log")
			secLog := filepath.Join(resultsDir, tc.name+"-serial-secure.log")

			argv := []string{
				requireQEMUBin(t), "-machine", tc.machine, "-cpu", "neoverse-n1",
				"-accel", "tcg,thread=multi", "-smp", "4", "-m", "4G",
				"-drive", "if=none,format=qcow2,file=" + disk + ",id=disk0,cache=unsafe",
				"-device", "virtio-scsi-pci", "-device", "scsi-hd,drive=disk0,bootindex=0",
				// The xHCI controller must come before anything hanging off
				// it, or QEMU refuses to start at all: "No 'usb-bus' bus found
				// for device 'usb-bot'". The control case caught this — every
				// other case would have reported "firmware silent" for a
				// reason unrelated to secure=on.
				"-device", "qemu-xhci,p2=8", "-device", "usb-kbd",
				"-drive", "file=" + isoPath + ",media=cdrom,if=none,id=cdrom0",
				"-device", "usb-bot,id=bot0",
				"-device", "scsi-cd,bus=bot0.0,drive=cdrom0,bootindex=1",
				"-display", "none", "-device", "ramfb",
				"-serial", "file:" + nsLog, "-serial", "file:" + secLog,
			}
			switch tc.load {
			case "bios":
				argv = append(argv, "-bios", tc.fw)
			case "kernel":
				argv = append(argv, "-kernel", tc.fw)
			case "pflash":
				vars := filepath.Join(work, "vars.fd")
				require.NoError(t, PrepareVarsFile(tc.fw, vars))
				argv = append(argv,
					"-drive", "if=pflash,format=raw,readonly=on,file="+tc.fw,
					"-drive", "if=pflash,format=raw,file="+vars)
			}

			exclusiveQEMU(t)
			cmd := exec.Command(argv[0], argv[1:]...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("%s: qemu did not start: %v", tc.name, err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			defer func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-exited
			}()

			const window = 4 * time.Minute
			deadline := time.Now().Add(window)
			var ns, sec int64
			for time.Now().Before(deadline) {
				time.Sleep(20 * time.Second)
				if fi, err := os.Stat(nsLog); err == nil {
					ns = fi.Size()
				}
				if fi, err := os.Stat(secLog); err == nil {
					sec = fi.Size()
				}
				if ns > 0 || sec > 0 {
					break
				}
				select {
				case <-exited:
					t.Logf("%s: QEMU exited early: %s", tc.name, tailLines(stderr.String(), 5))
					deadline = time.Now()
				default:
				}
			}
			t.Logf("RESULT %-26s serial ns=%d secure=%d", tc.name, ns, sec)
			if ns == 0 && sec == 0 {
				t.Logf("%s: no firmware output in %s", tc.name, window)
			}
		})
	}
}
