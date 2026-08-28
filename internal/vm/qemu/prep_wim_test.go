package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildWimBuilderArgv_VirtIOISO(t *testing.T) {
	s := testSpec()
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=cdrom2")
	assert.Contains(t, joined, "usb-storage,drive=cdrom2")
}

func TestBuildWimBuilderArgv_NoVirtIOISO(t *testing.T) {
	s := testSpec()
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "cdrom2")
}

func TestBuildWimBuilderArgv_SCSI(t *testing.T) {
	s := testSpec()
	s.CDBus = "scsi"
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "virtio-scsi-pci,id="+CDBusID,
		"SCSI bus controller must be present")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom0",
		"WinPE ISO must be on scsi-cd")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom1",
		"Windows ISO must be on scsi-cd")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom2",
		"VirtIO ISO must be on scsi-cd")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0,bootindex=2",
		"shared FAT volume must be on usb-storage with bootindex=2 for startup.nsh chainload")
	assert.NotContains(t, joined, "usb-storage,drive=cdrom",
		"ISOs must not be on usb-storage in SCSI mode")
}
