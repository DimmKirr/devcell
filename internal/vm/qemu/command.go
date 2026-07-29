package qemu

import (
	"fmt"
	"runtime"
)

// BuildInstallCommand constructs the QEMU argv for initial Windows installation.
// windowsISO and virtioISO are attached as USB CDROMs; autounattendImage is a
// FAT32 disk image attached as USB mass storage so UEFI mounts it as an FS
// device and finds startup.nsh to boot the Windows EFI loader.
func BuildInstallCommand(spec Spec, windowsISO, autounattendImage string) []string {
	argv := baseCommand(spec)

	bootIdx := 0
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", windowsISO),
		"-device", fmt.Sprintf("usb-storage,drive=cdrom0,removable=true,bootindex=%d", bootIdx))
	bootIdx++

	nextIdx := 1
	if spec.VirtioISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", spec.VirtioISO, nextIdx),
			"-device", fmt.Sprintf("usb-storage,drive=cdrom%d,removable=true,bootindex=%d", nextIdx, bootIdx))
		bootIdx++
		nextIdx++
	}

	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", autounattendImage),
		"-device", "usb-storage,drive=usbfat0")

	return argv
}

// BuildRunCommand constructs the QEMU argv for normal VM operation (post-install).
func BuildRunCommand(spec Spec) []string {
	argv := baseCommand(spec)

	// Boot from disk (default order)
	argv = append(argv, "-boot", "c")

	return argv
}

func baseCommand(spec Spec) []string {
	qemuBin := "qemu-system-aarch64"

	argv := []string{
		qemuBin,
		"-machine", machineType(),
		"-cpu", cpuType(),
		"-accel", accelerator(runtime.GOOS),
		"-smp", fmt.Sprintf("%d", spec.CPUs),
		"-m", fmt.Sprintf("%dG", spec.MemoryGB),
	}

	// UEFI firmware
	argv = append(argv,
		"-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", spec.FirmwarePath))
	if spec.VarsPath != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("if=pflash,format=raw,file=%s", spec.VarsPath))
	}

	// Main disk (VirtIO block)
	argv = append(argv,
		"-drive", fmt.Sprintf("if=virtio,format=qcow2,file=%s", spec.DiskPath))

	// Network with SSH port forwarding (+ optional RDP)
	netdev := fmt.Sprintf("user,id=net0,hostfwd=tcp:%s:%d-:22", spec.SSHHost, spec.SSHPort)
	if spec.RDPPort > 0 {
		netdev += fmt.Sprintf(",hostfwd=tcp:%s:%d-:3389", spec.SSHHost, spec.RDPPort)
	}
	if spec.MACAddr != "" {
		argv = append(argv,
			"-netdev", netdev,
			"-device", fmt.Sprintf("virtio-net-pci,netdev=net0,mac=%s", spec.MACAddr))
	} else {
		argv = append(argv,
			"-netdev", netdev,
			"-device", "virtio-net-pci,netdev=net0")
	}

	// Display
	argv = append(argv, "-display", spec.DisplayType)

	// VNC server (independent of -display)
	if spec.VNCPort > 0 {
		display := int(spec.VNCPort) - 5900
		argv = append(argv, "-vnc", fmt.Sprintf("localhost:%d", display))
	}

	// VGA
	argv = append(argv, "-device", "virtio-gpu-pci")

	// USB input (keyboard + tablet for absolute pointing).
	// p2=8: install attaches kbd+tablet+3 USB storage devices (Windows ISO,
	// VirtIO ISO, autounattend FAT); default p2=4 puts overflow behind a hub
	// and UEFI can't mount FS on hub-connected devices.
	argv = append(argv,
		"-device", "qemu-xhci,p2=8",
		"-device", "usb-kbd",
		"-device", "usb-tablet")

	// QMP monitor (machine protocol for programmatic control)
	argv = append(argv,
		"-qmp", "unix:"+QMPSocketPath(spec)+",server,nowait")

	// VM name
	if spec.VMName != "" {
		argv = append(argv, "-name", spec.VMName)
	}

	return argv
}

func machineType() string {
	if runtime.GOOS == "darwin" {
		return "virt,highmem=on"
	}
	return "virt"
}

func cpuType() string {
	if runtime.GOOS == "darwin" {
		return "host"
	}
	return "max"
}
