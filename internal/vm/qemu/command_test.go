package qemu

import (
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
	argv := BuildRunCommand(testSpec())
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "virtio")
	assert.Contains(t, joined, "/tmp/disk.qcow2")
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
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bootindex=0")
}

func TestBuildInstallCommand_VirtioISO(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.iso")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/virtio.iso,media=cdrom,if=none,id=cdrom1")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bootindex=1")
}

func TestBuildInstallCommand_BootIndex(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "bootindex=0", "Windows ISO must have bootindex=0 so UEFI Boot Manager tries it first")
	assert.Contains(t, joined, "bootindex=1", "VirtIO ISO must have bootindex=1")
}

func TestBuildInstallCommand_BootIndex_NoVirtio(t *testing.T) {
	s := testSpec()
	s.VirtioISO = ""
	argv := BuildInstallCommand(s, "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "bootindex=0", "Windows ISO must have bootindex=0 even without VirtIO")
	count := strings.Count(joined, "bootindex=")
	assert.Equal(t, 1, count, "only Windows ISO should have bootindex when no VirtIO")
}

func TestBuildInstallCommand_RemovableMedia(t *testing.T) {
	argv := BuildInstallCommand(testSpec(), "/tmp/win11.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "removable=true", "CDROM devices must be marked removable for UEFI fallback boot")
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
	assert.Contains(t, joined, "qemu-xhci,p2=8",
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
