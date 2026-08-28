package qemu

import (
	"fmt"
	"strings"
)

// WimBuilderResult holds the output of a successful WIM builder run.
type WimBuilderResult struct {
	// SharedImg is the path to the shared FAT volume after the builder
	// finished. Contains devcell.wim and the builder log.
	SharedImg string
	// AgentOutput is the agent's combined stdout/stderr (if available).
	AgentOutput string
}

// WimBuilderSpec configures a WIM builder WinPE boot. Callers populate this
// and pass it to BuildWimBuilderArgv to get a QEMU command line.
type WimBuilderSpec struct {
	// Spec is the base QEMU spec (machine, firmware, etc.).
	Spec Spec
	// WinPEISO is the bootable WinPE ISO (may be stock or custom-injected).
	WinPEISO string
	// SharedImg is the FAT volume with boot.wim + builder script.
	SharedImg string
	// WindowsISO is the full Windows ISO (provides install.wim).
	WindowsISO string
	// VirtIOISO is the virtio-win ISO (provides driver directories for
	// DISM /Add-Driver). Required when any WimPrepOp uses the Driver field.
	VirtIOISO string
	// EFIBootLoader is the raw bytes of BOOTAA64.EFI. When set and CDBus
	// is "scsi", SharedVolumeFiles ships it at /EFI/BOOT/BOOTAA64.EFI
	// alongside startup.nsh so the firmware can chainload WinPE from the
	// FAT volume after failing to read ISO9660 on the SCSI CD.
	EFIBootLoader []byte
}

// BuildWimBuilderArgv constructs the QEMU argv for the WIM builder VM.
// When Spec.CDBus is "scsi", ISOs are attached on a virtio-scsi-pci
// controller (scsi-cd) so EDK2 can boot them on QEMU 11.x/HVF where
// USB-attached ISOs are invisible to the firmware. The shared FAT
// volume always goes on usb-storage (Windows needs it as removable media).
func BuildWimBuilderArgv(wbs WimBuilderSpec) []string {
	if wbs.Spec.CDBus == "scsi" {
		return buildWimBuilderSCSI(wbs)
	}
	return buildWimBuilderUSB(wbs)
}

func buildWimBuilderUSB(wbs WimBuilderSpec) []string {
	argv := BuildWinPECommand(wbs.Spec, wbs.WinPEISO, wbs.SharedImg)

	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.WindowsISO+",media=cdrom,if=none,id=cdrom1",
			"-device", "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
	}
	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.VirtIOISO+",media=cdrom,if=none,id=cdrom2",
			"-device", "usb-storage,drive=cdrom2,removable=true,bus="+USBBusID+".0")
	}
	return argv
}

func buildWimBuilderSCSI(wbs WimBuilderSpec) []string {
	argv := baseCommand(wbs.Spec)

	argv = append(argv, "-device", fmt.Sprintf("virtio-scsi-pci,id=%s", CDBusID))

	// WinPE ISO on scsi-cd (boot device)
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", wbs.WinPEISO),
		"-device", fmt.Sprintf("scsi-cd,drive=cdrom0,bus=%s.0,id=%s,bootindex=1",
			CDBusID, InstallerCDDeviceID))

	// Shared FAT volume on usb-storage (WinPE reads/writes it).
	if wbs.SharedImg != "" {
		driveFormat := "raw"
		if strings.HasSuffix(wbs.SharedImg, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=usbfat0", wbs.SharedImg, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=2", USBBusID))
	}

	// Windows ISO on scsi-cd (provides install.wim)
	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom1", wbs.WindowsISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom1,bus=%s.0", CDBusID))
	}

	// VirtIO ISO on scsi-cd (provides driver directories)
	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom2", wbs.VirtIOISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom2,bus=%s.0", CDBusID))
	}

	return argv
}

