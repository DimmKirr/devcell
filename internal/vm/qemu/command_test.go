package qemu

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSpec() Spec {
	return Spec{
		VMName:       "test-vm",
		CPUs:         4,
		MemoryGB:     8,
		DiskPath:     "/tmp/disk.qcow2",
		FirmwarePath: "/tmp/efi.fd",
		VarsPath:     "/tmp/vars.fd",
		VirtioISO:    "/tmp/virtio.iso",
		SSHPort:      2222,
		SSHHost:      "127.0.0.1",
		MACAddr:      "02:ab:cd:ef:01:23",
		DisplayType:  "none",
	}
}

func TestBuildRunCommand_ContainsQEMU(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	require.NotEmpty(t, argv)
	assert.Equal(t, "qemu-system-aarch64", argv[0])
}

func TestBuildRunCommand_CPU(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-smp 4")
	assert.Contains(t, joined, "-m 8G")
}

func TestBuildRunCommand_SSHForward(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:2222-:22")
}

func TestBuildRunCommand_MACAddress(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "mac=02:ab:cd:ef:01:23")
}

func TestBuildRunCommand_UEFI(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "pflash")
	assert.Contains(t, joined, "/tmp/efi.fd")
	assert.Contains(t, joined, "/tmp/vars.fd")
}

func TestBuildRunCommand_Disk(t *testing.T) {
	// NVMe, not virtio: Windows ARM64 has inbox stornvme.sys but no virtio
	// drivers — WinPE/Windows can't see a virtio disk (CELL-359).
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "if=none,format=qcow2,file=/tmp/disk.qcow2,id=disk0")
	assert.Contains(t, joined, "nvme,drive=disk0")
	assert.NotContains(t, joined, "if=virtio")
}

func TestBuildRunCommand_RamfbDisplay(t *testing.T) {
	// ramfb, not virtio-gpu-pci: VirtioGpuDxe exposes no linear framebuffer
	// (FrameBufferBase=0); Windows bootmgr blits to it and dead-loops on a
	// NULL-dest data abort (CELL-352 root cause, 2026-07-29).
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-device ramfb")
	assert.NotContains(t, joined, "virtio-gpu-pci")
}

func TestBuildInstallCommand_CDIsRealCDROM(t *testing.T) {
	// The drive carries media=cdrom so QEMU presents optical media (2048-byte
	// sectors) — that is what makes it a CD, not the device model. The device
	// is usb-storage with removable=true, the wiring UTM ships for aarch64
	// virt. An earlier note here claimed usb-storage "always instantiates a
	// scsi-DISK" and blamed it for a cdboot data abort; that conflated it
	// with usb-bot, and UTM ships this exact config at scale.
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/win11.iso,media=cdrom")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
	assert.NotContains(t, joined, "usb-bot")
}

func TestBuildInstallCommand_NVMeDisk(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "nvme,drive=disk0")
	assert.NotContains(t, joined, "if=virtio")
}

func TestBuildRunCommand_Display(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-display none")
}

func TestBuildRunCommand_BootDisk(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-boot c")
}

func TestBuildRunCommand_QMP(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-qmp")
	assert.Contains(t, joined, "test-vm-qmp.sock")
}

func TestBuildInstallCommand_WindowsISO(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/win11.iso,media=cdrom,if=none,id=cdrom0")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
}

func TestBuildInstallCommand_VirtioISO(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/virtio.iso,media=cdrom,if=none,id=cdrom1")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus=usb-bus.0,bootindex=2")
}

func TestBuildInstallCommand_BootIndex(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	// Disk is bootindex=0 (empty on first boot ⇒ no UEFI entry ⇒ falls through
	// to the CD); installer CD 1, VirtIO CD 2.
	assert.Contains(t, joined, "nvme,drive=disk0,serial=devcell0,bootindex=0")
	assert.Contains(t, joined, "drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
	assert.Contains(t, joined, "drive=cdrom1,removable=true,bus=usb-bus.0,bootindex=2")
}

// Every bootable device must have an explicit bootindex, the answer volume
// included. It is a partition-table-less FAT superfloppy with no bootloader, so
// when the firmware happens to try it first the VM parks at
//
//	BdsDxe: starting Boot0001 "UEFI QEMU QEMU USB HARDDRIVE ..."
//	Start boot option
//
// forever. Leaving one device unordered makes the boot order firmware-dependent
// and therefore intermittent: run 20260730T222409 installed fine and run
// 20260731T011818, same argv, sat in firmware for the full 5-hour deadline.
func TestBuildInstallCommand_AnswerVolumeBootsLastNotByChance(t *testing.T) {
	joined := strings.Join(BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img"), " ")

	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus=usb-bus.0,bootindex=3",
		"the answer volume must be ordered explicitly, after the disk and both CDs")
}

func TestBuildInstallCommand_BootIndex_NoVirtio(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
	// The answer volume takes the slot the VirtIO CD would have had — it is
	// ordered explicitly either way, so nothing is left to the firmware.
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus=usb-bus.0,bootindex=2")
	count := strings.Count(joined, "bootindex=")
	assert.Equal(t, 3, count, "disk, Windows ISO and answer volume — every bootable device ordered")
}

func TestBuildInstallCommand_RemovableMedia(t *testing.T) {
	// removable=true is required: Windows only mounts a partition-table-less
	// volume, and only presents optical media correctly, from a removable
	// device. media=cdrom on the drive is what makes it optical.
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "media=cdrom", "installer media must be presented as a CD-ROM for UEFI El Torito boot")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true")
}

func TestBuildInstallCommand_AutounattendFAT(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/autounattend.img,format=raw,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0")
	assert.NotContains(t, joined, "autounattend.img,media=cdrom", "autounattend must NOT be cdrom")
}

func TestBuildInstallCommand_NoVirtio_AutounattendIndex(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "virtio.iso")
	assert.Contains(t, joined, "file=/tmp/autounattend.img,format=raw,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0")
}

func TestBuildInstallCommand_NoBIOSBootFlag(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "-boot d", "UEFI uses startup.nsh, not BIOS -boot d")
}

func TestBuildRunCommand_NoMAC(t *testing.T) {
	s := testSpec()
	s.MACAddr = ""
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "virtio-net-pci,netdev=net0")
	assert.NotContains(t, joined, "mac=")
}

func TestBuildRunCommand_InputDevices(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "usb-kbd")
	assert.Contains(t, joined, "usb-tablet")
	assert.Contains(t, joined, "qemu-xhci")
}

func TestBaseCommand_XHCIPortCount(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "qemu-xhci,id=usb-bus,p2=8",
		"XHCI must have p2=8 USB 2.0 ports — default p2=4 causes hub spillover "+
			"when install attaches kbd+tablet+3 storage devices, and UEFI can't mount "+
			"FS on hub-connected devices (no FS alias → startup.nsh not found)")
}

func TestBuildRunCommand_VMName(t *testing.T) {
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-name test-vm")
}

// --- CELL-352: VNC and RDP port forwarding ---

func TestBuildRunCommand_VNCDisplay(t *testing.T) {
	s := testSpec()
	s.VNCPort = 15050
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	// VNC display number = port - 5900
	assert.Contains(t, joined, "-vnc localhost:9150")
}

func TestBuildRunCommand_VNCNotPresentWhenZero(t *testing.T) {
	s := testSpec()
	s.VNCPort = 0
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "-vnc")
}

func TestBuildRunCommand_RDPHostfwd(t *testing.T) {
	s := testSpec()
	s.RDPPort = 15089
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:15089-:3389")
}

func TestBuildRunCommand_RDPNotPresentWhenZero(t *testing.T) {
	s := testSpec()
	s.RDPPort = 0
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "3389")
}

func TestBuildRunCommand_BothVNCAndRDP(t *testing.T) {
	s := testSpec()
	s.VNCPort = 25650
	s.RDPPort = 25689
	argv := BuildRunCommand(s)
	joined := strings.Join(argv, " ")
	// VNC display = 25650 - 5900 = 19750
	assert.Contains(t, joined, "-vnc localhost:19750")
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:25689-:3389")
	// SSH hostfwd still present
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:2222-:22")
}

func TestBuildInstallCommand_VNCDisplay(t *testing.T) {
	s := testSpec()
	s.VNCPort = 15050
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-vnc localhost:9150")
}

func TestBuildInstallCommand_RDPHostfwd(t *testing.T) {
	s := testSpec()
	s.RDPPort = 15089
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:15089-:3389")
}

// --- Spec-driven argv knobs (single source of truth for QEMU args) ---

func TestBaseCommand_ExplicitAccel(t *testing.T) {
	s := testSpec()
	s.Accel = "tcg,thread=multi"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-accel tcg,thread=multi")
}

func TestBaseCommand_TCGUsesVirtualizationAndPauth(t *testing.T) {
	// Under TCG the aarch64 guest needs virtualization=true (EL2) and
	// pauth-impdef=on — real pointer auth emulation is punishingly slow.
	s := testSpec()
	s.Accel = "tcg,thread=multi"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-machine virt,virtualization=true")
	assert.Contains(t, joined, "-cpu max,pauth-impdef=on")
}

func TestBaseCommand_SerialLogPath(t *testing.T) {
	s := testSpec()
	s.SerialLogPath = "/tmp/serial.log"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-serial file:/tmp/serial.log")
}

func TestBaseCommand_NoSerialByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "-serial")
}

func TestBaseCommand_NoReboot(t *testing.T) {
	s := testSpec()
	s.NoReboot = true
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-no-reboot")
}

func TestBaseCommand_NoRebootOmittedByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "-no-reboot")
}

func TestBuildInstallCommand_WithoutAutounattend(t *testing.T) {
	// Boot-only validation: no autounattend image to attach.
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
	assert.NotContains(t, joined, "usbfat0")
}

func TestBaseCommand_GuestProgressVirtioSerial(t *testing.T) {
	// pci-serial (16550 on PCI) shows up as COM1 on x86 but NOT on ARM64 —
	// guest-progress.log was always empty (CELL-430). virtio-serial with the
	// vioserial driver exposes a named port the guest writes to via
	// \\.\Global\<name>, which works on both architectures.
	s := testSpec()
	s.GuestProgressLogPath = "/tmp/guest-progress.log"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-chardev file,id=guestprog,path=/tmp/guest-progress.log")
	assert.Contains(t, joined, "-device virtio-serial-pci")
	assert.Contains(t, joined, "-device virtserialport,bus=virtio-serial0.0,chardev=guestprog,name="+ProgressPortName)
	assert.NotContains(t, joined, "pci-serial")
}

func TestBaseCommand_GuestProgressSharesBusWithGuestAgent(t *testing.T) {
	s := testSpec()
	s.GuestProgressLogPath = "/tmp/guest-progress.log"
	s.GuestAgentSocketPath = "/tmp/qga.sock"
	joined := strings.Join(BuildRunCommand(s), " ")
	// Only one virtio-serial-pci bus, shared by both ports.
	assert.Equal(t, 1, strings.Count(joined, "virtio-serial-pci"))
}

func TestBaseCommand_NoGuestProgressByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "pci-serial")
	assert.NotContains(t, joined, "guestprog")
}

func TestBuildInstallCommand_AutounattendIsRemovable(t *testing.T) {
	// CreateFATImage writes a superfloppy (no partition table). Windows only
	// mounts such a volume when the device reports removable media; as a fixed
	// disk it stays RAW and Setup never finds autounattend.xml — observed
	// live: guest mounted nothing and sat on the language screen (CELL-362).
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true")
}

func TestBuildInstallCommand_AutounattendISOAsCDROM(t *testing.T) {
	// An .iso answer file is attached as a second usb-bot CD-ROM. A FAT
	// superfloppy on usb-storage alongside the installer made cdboot take a
	// data abort during boot; CD-ROMs are the device type this firmware path
	// handles reliably, and Windows Setup searches CD/DVD drives for
	// autounattend.xml too (CELL-362).
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/autounattend.iso,media=cdrom,if=none,id=cdrom2")
	assert.NotContains(t, joined, "usb-bot")
	assert.Contains(t, joined, "usb-storage,drive=cdrom2,removable=true,bus=usb-bus.0")
	assert.NotContains(t, joined, "usbfat0")
}

func TestBuildInstallCommand_AutounattendImgStaysUSBStorage(t *testing.T) {
	// Raw FAT images keep the removable usb-storage path for callers that
	// still want a writable answer-file volume.
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true")
}

func TestBuildInstallCommand_BootOrderDiskBeforeCD(t *testing.T) {
	// Disk first, CD second. An empty disk exposes no UEFI boot entry, so the
	// firmware falls through to the CD and installs; once Windows is on the
	// disk it has an ESP and wins every subsequent boot. Self-correcting —
	// no eject and no timing heuristic required.
	//
	// With the CD at bootindex=0 the post-install reboot booted the installer
	// again and Setup stopped on "you started an upgrade and booted from
	// installation media" (observed twice).
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "nvme,drive=disk0,serial=devcell0,bootindex=0")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")

	diskIdx := strings.Index(joined, "bootindex=0")
	cdIdx := strings.Index(joined, "bootindex=1")
	assert.Positive(t, diskIdx)
	assert.Positive(t, cdIdx)
}

func TestBuildRunCommand_DiskIsBootable(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.Contains(t, joined, "nvme,drive=disk0,serial=devcell0,bootindex=0")
}

func TestBuildInstallCommand_CDsHaveQdevIDs(t *testing.T) {
	// Secondary safety net: QMP `eject` addresses devices by qdev id, not by
	// drive id — ejecting "cdrom0" fails with DeviceNotFound.
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "id="+InstallerCDDeviceID)
}

func TestBaseCommand_DiskCacheMode(t *testing.T) {
	// Under TCG every guest flush becomes a real host fsync, and Windows
	// Setup flushes constantly. cache=unsafe drops them — the disk is
	// garbage if the host dies mid-run, which is fine for a disposable
	// install VM but must stay opt-in.
	s := testSpec()
	s.DiskCacheMode = "unsafe"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "file=/tmp/disk.qcow2,id=disk0,cache=unsafe")
}

func TestBaseCommand_NoDiskCacheModeByDefault(t *testing.T) {
	// Production VMs keep QEMU's safe default.
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "cache=")
}

// --- dev-env wiring: guest agent channel, virtio-fs, driver ISO at run time ---

// The qemu-ga channel (VIRTIO.md "idiomatic host side"): a virtio-serial port
// named org.qemu.guest_agent.0 — the exact name the agent looks for. On ARM64
// the agent itself is the x64 MSI under emulation, but the channel is the same.
func TestBuildRunCommand_GuestAgentChannel(t *testing.T) {
	spec := testSpec()
	spec.GuestAgentSocketPath = "/tmp/qga.sock"

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "-chardev socket,id=qga0,path=/tmp/qga.sock,server=on,wait=off")
	assert.Contains(t, joined, "-device virtio-serial-pci")
	assert.Contains(t, joined, "-device virtserialport,bus=virtio-serial0.0,chardev=qga0,name=org.qemu.guest_agent.0")
}

func TestBuildRunCommand_NoGuestAgentChannelByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "guest_agent")
}

// virtio-fs needs three things wired together: the vhost-user socket, the
// device with the tag the guest mounts by, and shareable guest RAM — without
// memory-backend + numa, QEMU rejects vhost-user-fs outright.
func TestBuildRunCommand_VirtioFS(t *testing.T) {
	spec := testSpec()
	spec.VirtioFSSocketPath = "/tmp/vfs.sock"
	spec.VirtioFSTag = "devcell"

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "-chardev socket,id=virtiofs0,path=/tmp/vfs.sock")
	assert.Contains(t, joined, "-device vhost-user-fs-pci,queue-size=1024,chardev=virtiofs0,tag=devcell")
	assert.Contains(t, joined, "-object memory-backend-memfd,id=mem,size=8G,share=on")
	assert.Contains(t, joined, "-numa node,memdev=mem")
}

func TestBuildRunCommand_NoVirtioFSByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "vhost-user-fs")
	assert.NotContains(t, joined, "memory-backend")
}

// Post-install driver work (pnputil from the virtio ISO) needs the ISO
// attached to a *running* VM, not only during install.
func TestBuildRunCommand_AttachesVirtioISO(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")

	assert.Contains(t, joined, "file=/tmp/virtio.iso,media=cdrom")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus=usb-bus.0")
}

func TestBuildRunCommand_NoCDWithoutVirtioISO(t *testing.T) {
	spec := testSpec()
	spec.VirtioISO = ""
	joined := strings.Join(BuildRunCommand(spec), " ")
	assert.NotContains(t, joined, "media=cdrom")
}

// Windows' own hypervisor needs more than EL2 to launch: the community
// configuration that boots Hyper-V/WSL2 on ARM64 under TCG also asks for a
// GICv3 with ITS and a secure world
// (https://gist.github.com/Vogtinator/293c4f90c5e92838f7e72610725905fd —
// "WSL2/Hyper-V support (TCG only, failing under KVM)"). With only
// virtualization=true, run 20260801T123644 had the feature installed and
// hypervisorlaunchtype=Auto while HCS still answered HYPERV_NOT_INSTALLED.
func TestMachineType_NestedVirtAddsGICv3WithoutSecureWorld(t *testing.T) {
	spec := testSpec()
	spec.Accel = "tcg,thread=multi"
	spec.NestedVirt = true

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "virtualization=true", "EL2 for the guest hypervisor")
	assert.Contains(t, joined, "gic-version=3", "a hypervisor needs GICv3 virtual interrupts")
	assert.Contains(t, joined, "its=on", "ITS pairs with GICv3")
	assert.NotContains(t, joined, "secure=on",
		"secure=on needs secure-world firmware we do not ship — it stopped an installed Windows from booting")
}

// The install path is proven working with the plain machine line; changing the
// boot environment underneath it is not something a dev-env experiment gets to
// do implicitly.
func TestMachineType_NestedVirtIsOptIn(t *testing.T) {
	spec := testSpec()
	spec.Accel = "tcg,thread=multi"

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "virtualization=true")
	assert.NotContains(t, joined, "secure=on")
	assert.NotContains(t, joined, "gic-version=3")
}

// secure=on is its own axis: it hands the pflash firmware the secure world and
// an EL3 entry. Kept separate from NestedVirt so a test can enable one without
// the other and attribute a boot failure to the right change.
func TestMachineType_SecureWorldIsSeparateFromNestedVirt(t *testing.T) {
	spec := testSpec()
	spec.Accel = "tcg,thread=multi"

	spec.SecureWorld = true
	secure := strings.Join(BuildRunCommand(spec), " ")
	assert.Contains(t, secure, "secure=on")
	assert.Contains(t, secure, "gic-version=3")
	// The proven config spells it virtualization=on (QEMU accepts on/true).
	assert.Contains(t, secure, "virtualization=on")

	spec.SecureWorld = false
	spec.NestedVirt = true
	nested := strings.Join(BuildRunCommand(spec), " ")
	assert.NotContains(t, nested, "secure=on", "nested virt must not drag in the secure world")
}

// A controller with no drive bound to it is a machine with no disk. The scsi
// path needs both devices; emitting only virtio-scsi-pci left drive=disk0
// orphaned and the guest diskless.
// --- CELL-427: CDBus selects the CD-ROM controller ---

// --- CELL-429: CDs ride usb-storage, the way UTM does it ---
//
// UTM's ARM64 rule (UTMQemuConfigurationDrive.swift:125) sends every CD on
// the `virt` machine to USB and only non-CDs to virtio, rendering
// `usb-storage,removable=true,bus=usb-bus.0` on a `id=usb-bus` xhci
// controller. That is the config a very large Windows-on-Apple-Silicon user
// base runs, and both halves are already proven in our own logs: EDK2 on
// QEMU 11/HVF boots usb-storage (the answer volume's BOOTAA64.EFI
// chainloaded that way, run 20260812T091924) and ARM64 WinPE reads
// usb-storage with inbox usbstor (the answer volume is the C: diskpart
// lists and Setup evaluates as a media location, run 20260812T150644).
//
// This removes the vioscsi dependency entirely — no driver, no drvload, no
// PnP-settle race against EarlyF6DriverInstall. Note usb-storage is NOT
// usb-bot: usb-bot is what killed USB enumeration on QEMU 11/HVF (run
// 20260812T122950).
func TestBuildInstallCommand_CDsOnUSBStorage(t *testing.T) {
	s := testSpec()
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "qemu-xhci,id=usb-bus",
		"the xhci controller needs an id so drives can name their bus")
	assert.Contains(t, joined, "file=/tmp/win11.iso,media=cdrom,if=none,id=cdrom0")
	assert.Contains(t, joined,
		fmt.Sprintf("usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=%s,bootindex=1", InstallerCDDeviceID))
	assert.NotContains(t, joined, "usb-bot", "usb-bot is invisible to EDK2 on QEMU 11/HVF (CELL-427)")
	assert.NotContains(t, joined, "virtio-scsi", "WinPE has no inbox vioscsi (CELL-429)")
}

func TestBuildInstallCommand_VirtioISOOnUSBStorage(t *testing.T) {
	s := testSpec()
	joined := strings.Join(BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img"), " ")

	assert.Contains(t, joined, "file=/tmp/virtio.iso,media=cdrom,if=none,id=cdrom1")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus=usb-bus.0,bootindex=2")
	assert.Equal(t, 1, strings.Count(joined, "qemu-xhci"), "one USB controller for every device")
}

func TestBuildRunCommand_VirtioISOOnUSBStorage(t *testing.T) {
	s := testSpec()
	joined := strings.Join(BuildRunCommand(s), " ")

	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus=usb-bus.0")
	assert.NotContains(t, joined, "usb-bot")
	assert.NotContains(t, joined, "virtio-scsi")
}

func TestBuildInstallCommand_CDsOnSCSI(t *testing.T) {
	s := testSpec()
	s.CDBus = "scsi"
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, fmt.Sprintf("virtio-scsi-pci,id=%s", CDBusID),
		"scsi CDs need a dedicated virtio-scsi controller")
	assert.Contains(t, joined, "file=/tmp/win11.iso,media=cdrom,if=none,id=cdrom0")
	assert.Contains(t, joined,
		fmt.Sprintf("scsi-cd,drive=cdrom0,bus=%s.0,id=%s,bootindex=1", CDBusID, InstallerCDDeviceID))
	assert.Contains(t, joined,
		fmt.Sprintf("scsi-cd,drive=cdrom1,bus=%s.0,bootindex=2", CDBusID),
		"virtio ISO also on scsi-cd")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0",
		"answer FAT image still on usb-storage regardless of CD bus")
}

// --- CELL-430: BuildWinPECommand — WinPE-only boot from custom ISO ---

func TestBuildWinPECommand_BootsFromCustomISO(t *testing.T) {
	s := testSpec()
	s.VirtioISO = "" // WinPE-only, no virtio ISO
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/winpe.iso,media=cdrom,if=none,id=cdrom0")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus=usb-bus.0,id=installer-cd,bootindex=1")
}

func TestBuildWinPECommand_AnswerVolumeOnUSBStorage(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/answer.img,format=raw,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus=usb-bus.0")
	assert.NotContains(t, joined, "usbfat0,removable=true,bus=usb-bus.0,bootindex=",
		"answer volume must not be a boot candidate — ARM64 kernel firmware crashes on non-bootable FAT")
}

func TestBuildWinPECommand_NoVirtioISO(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "virtio.iso")
	count := strings.Count(joined, "media=cdrom")
	assert.Equal(t, 1, count, "only one CD: the custom WinPE ISO")
}

func TestBuildWinPECommand_HasBaseElements(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "qemu-system-aarch64")
	assert.Contains(t, joined, "nvme,drive=disk0")
	assert.Contains(t, joined, "qemu-xhci")
	assert.Contains(t, joined, "-qmp")
}

func TestBuildWinPECommand_Qcow2AnswerVolume(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/shared.qcow2")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/shared.qcow2,format=qcow2,if=none,id=usbfat0",
		"qcow2 extension must produce format=qcow2")
}

func TestBuildWinPECommand_RawAnswerVolumeStaysRaw(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildWinPECommand(s, "/tmp/winpe.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "format=raw",
		"non-qcow2 extension must remain format=raw")
}

func TestBuildRunCommand_ScsiDiskIsActuallyAttached(t *testing.T) {
	spec := testSpec()
	spec.DiskBus = "scsi"

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "id=disk0", "the drive must exist")
	assert.Contains(t, joined, "virtio-scsi-pci", "and its controller")
	assert.Contains(t, joined, "scsi-hd,drive=disk0", "and the drive must be bound to it")
}

func TestBuildInstallCommand_DevcellWimImg(t *testing.T) {
	spec := testSpec()
	spec.DevcellWimImg = "/tmp/devcell-wim.img"
	argv := BuildInstallCommand(spec, "/tmp/win.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "devcellwim0")
	assert.Contains(t, joined, "/tmp/devcell-wim.img")
	assert.Contains(t, joined, "usb-storage,drive=devcellwim0,removable=true")
}

func TestBuildInstallCommand_DevcellWimImg_Qcow2(t *testing.T) {
	spec := testSpec()
	spec.DevcellWimImg = "/tmp/devcell-wim.qcow2"
	argv := BuildInstallCommand(spec, "/tmp/win.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "format=qcow2")
	assert.Contains(t, joined, "devcellwim0")
}

func TestBuildInstallCommand_NoDevcellWimImg(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/win.iso", "/tmp/answer.img")
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "devcellwim0")
}
