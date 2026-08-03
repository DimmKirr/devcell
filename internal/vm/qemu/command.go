package qemu

import (
	"fmt"
	"runtime"
	"strings"
)

// InstallerCDDeviceID is the qdev id of the installer CD. QMP addresses
// devices by qdev id, not drive id, so ejecting requires this name.
const InstallerCDDeviceID = "installer-cd"

// BuildInstallCommand constructs the QEMU argv for initial Windows installation.
// windowsISO and any virtioISO are attached as USB CD-ROMs. autounattendImage
// is attached as a further CD-ROM when it is an .iso, or as a removable
// usb-storage disk for a raw FAT image; Windows Setup searches both kinds of
// removable media for autounattend.xml.
func BuildInstallCommand(spec Spec, windowsISO, autounattendImage string) []string {
	argv := baseCommand(spec)

	// CDs go on usb-bot + scsi-cd, not the legacy usb-storage device: that one
	// always instantiates a scsi-DISK (EDK2 names it "USB HARDDRIVE", 512-byte
	// blocks) and Windows cdboot faults reading it as a 2048-byte CD. usb-bot
	// is a plain USB Bulk-Only Transport controller we can hang a scsi-cd off.
	// bootindex 0 belongs to the disk (see baseCommand); CDs follow it.
	bootIdx := 1
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", windowsISO),
		"-device", "usb-bot,id=bot0",
		"-device", fmt.Sprintf("scsi-cd,bus=bot0.0,drive=cdrom0,id=%s,bootindex=%d", InstallerCDDeviceID, bootIdx))
	bootIdx++

	nextIdx := 1
	if spec.VirtioISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", spec.VirtioISO, nextIdx),
			"-device", fmt.Sprintf("usb-bot,id=bot%d", nextIdx),
			"-device", fmt.Sprintf("scsi-cd,bus=bot%d.0,drive=cdrom%d,bootindex=%d", nextIdx, nextIdx, bootIdx))
		bootIdx++
		nextIdx++
	}

	// Answer file. Omitted for boot-only validation runs.
	switch {
	case autounattendImage == "":
		// nothing to attach
	case strings.HasSuffix(autounattendImage, ".iso"):
		// Preferred: another CD-ROM. A FAT superfloppy on usb-storage next to
		// the installer made cdboot take a data abort mid-boot, and Setup
		// searches CD/DVD drives for autounattend.xml just the same.
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", autounattendImage, nextIdx),
			"-device", fmt.Sprintf("usb-bot,id=bot%d", nextIdx),
			"-device", fmt.Sprintf("scsi-cd,bus=bot%d.0,drive=cdrom%d", nextIdx, nextIdx))
	default:
		// Raw FAT image: usb-storage, and it must report removable media —
		// the image has no partition table and Windows only mounts such a
		// volume from a removable device.
		//
		// bootindex last, and explicitly: this volume has no bootloader, so a
		// firmware that tries it first parks at "Start boot option" forever.
		// Without an index its position is the firmware's choice, which makes
		// the whole install intermittent.
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", autounattendImage),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bootindex=%d", bootIdx))
	}

	return argv
}

// BuildRunCommand constructs the QEMU argv for normal VM operation (post-install).
func BuildRunCommand(spec Spec) []string {
	argv := baseCommand(spec)

	// Boot from disk (default order)
	argv = append(argv, "-boot", "c")

	// Driver ISO: post-install driver work (pnputil the ARM64 INFs, run the
	// guest-agent MSI) reads it from a normal running VM, not only during
	// install. Same usb-bot + scsi-cd shape as the installer CDs — see
	// BuildInstallCommand for why usb-storage cannot carry a CD.
	if spec.VirtioISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom1", spec.VirtioISO),
			"-device", "usb-bot,id=bot1",
			"-device", "scsi-cd,bus=bot1.0,drive=cdrom1")
	}

	// Guest-written log volume — see Spec.LogVolumePath. Raw FAT on
	// usb-storage, removable (Windows only mounts a partition-table-less
	// volume from a removable device — same constraint as the answer file).
	if spec.LogVolumePath != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", spec.LogVolumePath),
			"-device", "usb-storage,drive=usbfat0,removable=true")
	}

	// qemu-ga channel — see Spec.GuestAgentSocketPath.
	if spec.GuestAgentSocketPath != "" {
		argv = append(argv,
			"-chardev", fmt.Sprintf("socket,id=qga0,path=%s,server=on,wait=off", spec.GuestAgentSocketPath),
			"-device", "virtio-serial-pci",
			"-device", "virtserialport,chardev=qga0,name=org.qemu.guest_agent.0")
	}

	// virtio-fs — see Spec.VirtioFSSocketPath. vhost-user devices refuse to
	// start without shareable guest memory, hence the memfd backend + numa
	// node binding all of RAM to it.
	if spec.VirtioFSSocketPath != "" && spec.VirtioFSTag != "" {
		argv = append(argv,
			"-chardev", fmt.Sprintf("socket,id=virtiofs0,path=%s", spec.VirtioFSSocketPath),
			"-device", fmt.Sprintf("vhost-user-fs-pci,queue-size=1024,chardev=virtiofs0,tag=%s", spec.VirtioFSTag),
			"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dG,share=on", spec.MemoryGB),
			"-numa", "node,memdev=mem")
	}

	return argv
}

func baseCommand(spec Spec) []string {
	qemuBin := "qemu-system-aarch64"

	argv := []string{
		qemuBin,
		"-machine", machineType(spec),
		"-cpu", cpuType(spec),
		"-accel", spec.effectiveAccel(),
		"-smp", fmt.Sprintf("%d", spec.CPUs),
		"-m", fmt.Sprintf("%dG", spec.MemoryGB),
	}

	// UEFI firmware. Two loading modes — see Spec.FirmwareKernel: pflash is
	// the normal one (keeps an NVRAM vars store); -kernel is what the proven
	// secure-world config uses, because QEMU's boot stub then handles the EL3
	// entry that a normal-world EDK2 cannot.
	if spec.FirmwareKernel {
		argv = append(argv, "-kernel", spec.FirmwarePath)
	} else {
		argv = append(argv,
			"-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", spec.FirmwarePath))
		if spec.VarsPath != "" {
			argv = append(argv,
				"-drive", fmt.Sprintf("if=pflash,format=raw,file=%s", spec.VarsPath))
		}
	}

	// Main disk on NVMe: Windows ARM64 has inbox stornvme.sys but no virtio
	// drivers, so a virtio disk is invisible to WinPE/Windows (CELL-359).
	argv = append(argv,
		"-drive", diskDriveArg(spec),
	)
	argv = append(argv, diskDeviceArgs(spec)...)

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

	// Display: ramfb is the only aarch64 device with a linear framebuffer.
	// virtio-gpu-pci reports FrameBufferBase=0 and Windows bootmgr dead-loops
	// blitting to NULL (CELL-352 root cause, 2026-07-29).
	argv = append(argv, "-device", "ramfb")

	// USB input (keyboard + tablet for absolute pointing).
	// p2=8: install attaches kbd+tablet+3 USB storage devices (Windows ISO,
	// VirtIO ISO, autounattend FAT); default p2=4 puts overflow behind a hub
	// and UEFI can't mount FS on hub-connected devices.
	argv = append(argv,
		"-device", "qemu-xhci,p2=8",
		"-device", "usb-kbd",
		"-device", "usb-tablet")

	// Serial console to a file (boot/EMS diagnostics)
	if spec.SerialLogPath != "" {
		argv = append(argv, "-serial", "file:"+spec.SerialLogPath)
	}

	// Guest-writable progress port — see Spec.GuestProgressLogPath.
	if spec.GuestProgressLogPath != "" {
		argv = append(argv,
			"-chardev", "file,id=guestprog,path="+spec.GuestProgressLogPath,
			"-device", "pci-serial,chardev=guestprog")
	}

	// QMP monitor (machine protocol for programmatic control)
	argv = append(argv,
		"-qmp", "unix:"+QMPSocketPath(spec)+",server,nowait")

	if spec.NoReboot {
		argv = append(argv, "-no-reboot")
	}

	// VM name
	if spec.VMName != "" {
		argv = append(argv, "-name", spec.VMName)
	}

	return argv
}

// diskDriveArg builds the -drive argument for the main disk, appending the
// cache policy when one is configured.
// diskDeviceArg selects the system disk controller. NVMe is our default
// (Windows ARM64 has an inbox driver); virtio-scsi is what the proven
// Hyper-V config uses.
// diskDeviceArgs attaches the system disk. NVMe is our default (Windows ARM64
// has an inbox driver and needs one device); virtio-scsi is what the proven
// Hyper-V config uses and needs TWO — the controller and the drive bound to
// it. Emitting only the controller leaves drive=disk0 orphaned and the guest
// with no disk at all.
func diskDeviceArgs(spec Spec) []string {
	if spec.DiskBus == "scsi" {
		return []string{
			"-device", "virtio-scsi-pci",
			"-device", "scsi-hd,drive=disk0,serial=devcell0,bootindex=0",
		}
	}
	return []string{"-device", "nvme,drive=disk0,serial=devcell0,bootindex=0"}
}

func diskDriveArg(spec Spec) string {
	arg := fmt.Sprintf("if=none,format=qcow2,file=%s,id=disk0", spec.DiskPath)
	if spec.DiskCacheMode != "" {
		arg += ",cache=" + spec.DiskCacheMode
	}
	return arg
}

func machineType(spec Spec) string {
	if spec.SecureWorld {
		return secureMachineType(spec)
	}
	if spec.usesTCG() {
		// virtualization=true gives the guest EL2, which Windows ARM64 expects.
		if spec.NestedVirt {
			// EL2 alone is not enough for Windows to start its own hypervisor:
			// it also wants GICv3 virtual interrupts (with ITS) and a secure
			// world. This is the configuration the community reports booting
			// Hyper-V/WSL2 on ARM64 — under TCG specifically; the same setup is
			// reported to fail under KVM.
			// secure=on is deliberately absent: it needs firmware built with
			// secure-world support, and the aarch64 EDK2 we ship
			// (edk2-aarch64-code.fd) is the non-secure build — only the i386
			// tree has a *-secure-code.fd. Asking for it left an installed
			// Windows unable to boot at all (run 20260801T131920: 116 SSH
			// polls, no stage ever started).
			return "virt,virtualization=true,gic-version=3,its=on"
		}
		return "virt,virtualization=true"
	}
	if runtime.GOOS == "darwin" {
		return "virt,highmem=on"
	}
	return "virt"
}

// secureMachineType is the machine Windows' hypervisor is reported to need
// (see CELL-392): EL2 plus GICv3/ITS plus a secure world. Whether an
// installed Windows — or even the installer — can boot on it is what
// TestWindowsInstall_SecureBoot measures.
func secureMachineType(spec Spec) string {
	// Exactly the machine of the config reported working for Hyper-V/WSL2 on
	// ARM64 (Vogtinator gist, "tested with Build 25931"): no its=on, which was
	// our own addition from a different source.
	base := "virt,virtualization=on,gic-version=3,secure=on"
	if !spec.usesTCG() && runtime.GOOS == "darwin" {
		return base + ",highmem=on"
	}
	return base
}

func cpuType(spec Spec) string {
	if spec.CPU != "" {
		return spec.CPU
	}
	if spec.SecureWorld {
		// A real CPU model, not max: with -cpu max the firmware boots but
		// Windows itself never writes a byte on the secure=on machine
		// (run 20260802T065846) — max enables every TCG feature and Windows
		// trips on one of them when EL3 is present. neoverse-n1 is the model
		// the whole WSL2 chain was proven on.
		return "neoverse-n1"
	}
	if spec.usesTCG() {
		// pauth-impdef=on swaps architectural pointer authentication for a
		// cheap implementation-defined one — emulating the real algorithm
		// under TCG is punishingly slow.
		return "max,pauth-impdef=on"
	}
	if runtime.GOOS == "darwin" {
		return "host"
	}
	return "max"
}
