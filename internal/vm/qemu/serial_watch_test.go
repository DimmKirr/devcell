package qemu

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchSerialForEFIShell_DetectsShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	// Start watcher before the file exists (just like real boot).
	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForEFIShell(path, stop)

	// Simulate firmware writing serial output with the shell marker.
	serial := `UEFI firmware (version edk2-stable202408)
BdsDxe: failed to load Boot0001 "UEFI QEMU NVMe Ctrl devcell0 1" from PciRoot(0x0): Not Found
BdsDxe: loading Boot0002 "EFI Internal Shell" from Fv(64074AFE)
BdsDxe: starting Boot0002 "EFI Internal Shell" from Fv(64074AFE)
UEFI Interactive Shell v2.2
`
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.Contains(t, msg, "BdsDxe: starting")
		assert.Contains(t, msg, "EFI Internal Shell")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for EFI shell detection")
	}
}

func TestWatchSerialForEFIShell_IgnoresNormalBoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	ch := WatchSerialForEFIShell(path, stop)

	// Simulate a successful boot — no EFI shell.
	serial := `UEFI firmware (version edk2-stable202408)
BdsDxe: loading Boot0001 "UEFI QEMU QEMU USB HARDDRIVE 1" from PciRoot(0x0)/USB(0x2,0x0)
BdsDxe: starting Boot0001 "UEFI QEMU QEMU USB HARDDRIVE 1" from PciRoot(0x0)/USB(0x2,0x0)
`
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	// Should NOT fire — give it a moment to confirm.
	select {
	case msg := <-ch:
		t.Fatalf("should not have fired, got: %s", msg)
	case <-time.After(2 * time.Second):
		// Expected — no EFI shell marker.
	}
	close(stop)
}

func TestWatchSerialForEFIShell_DetectsWithANSIEscapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForEFIShell(path, stop)

	// Real serial output has ANSI escapes embedded.
	serial := "\x1b[2J\x1b[01;01HBdsDxe: starting Boot0004 \"EFI Internal Shell\" from Fv(64074AFE)\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.Contains(t, msg, "EFI Internal Shell")
		assert.NotContains(t, msg, "\x1b", "ANSI escapes should be stripped")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestWatchSerialForStartupNSHFail_DetectsFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForStartupNSHFail(path, stop)

	serial := `BdsDxe: starting Boot0005 "EFI Internal Shell" from Fv(64074AFE)
UEFI Interactive Shell v2.2
FS0:\> startup.nsh
echo Searching for Windows EFI boot loader...
BOOTAA64.EFI not found on FS0-FS4. Listing all FS devices:
`
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.NotEmpty(t, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startup.nsh failure detection")
	}
}

func TestWatchSerialForStartupNSHFail_DoesNotFireOnEFIShellAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	ch := WatchSerialForStartupNSHFail(path, stop)

	// EFI shell appears but startup.nsh hasn't reported failure yet.
	serial := `BdsDxe: starting Boot0005 "EFI Internal Shell" from Fv(64074AFE)
UEFI Interactive Shell v2.2
Press ESC in 5 seconds to skip startup.nsh
`
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		t.Fatalf("should not fire on EFI shell alone, got: %s", msg)
	case <-time.After(2 * time.Second):
		// Expected — startup.nsh hasn't failed yet.
	}
	close(stop)
}

func TestStripANSI(t *testing.T) {
	assert.Equal(t, "hello world", stripANSI("\x1b[2Jhello \x1b[01;01Hworld"))
	assert.Equal(t, "plain", stripANSI("plain"))
	assert.Equal(t, "", stripANSI(""))
}
