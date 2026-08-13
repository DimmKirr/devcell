package qemu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestISOPreflight_ValidISO(t *testing.T) {
	iso := buildTestISOWithBootloader(t, peARM64Stub())
	info, err := isoPreflight(iso, 0)
	require.NoError(t, err)
	assert.True(t, info.HasBootEFI)
	assert.Equal(t, "iso9660", info.Format)
}

// buildPureUDFISO writes the on-disk layout the macOS ISO cache held in run
// 20260812T081924: a UDF volume recognition sequence — BEA01/NSR02/TEA01 at
// sectors 16–18, then a zero sector — with no ISO 9660 PVD anywhere. This is
// what `hdiutil makehybrid -udf` masters on macOS.
func buildPureUDFISO(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pure-udf.iso")
	f, err := os.Create(p)
	require.NoError(t, err)
	defer f.Close()

	writeVSD := func(sector int64, magic string) {
		buf := make([]byte, 2048)
		copy(buf[1:], magic) // type=0x00, then magic, then version
		buf[6] = 0x01
		_, err := f.WriteAt(buf, sector*2048)
		require.NoError(t, err)
	}
	writeVSD(16, "BEA01")
	writeVSD(17, "NSR02")
	writeVSD(18, "TEA01")
	require.NoError(t, f.Truncate(64*2048))
	return p
}

func TestISOPreflight_PureUDF_DetectsUDFFormat(t *testing.T) {
	iso := buildPureUDFISO(t)
	info, _ := isoPreflight(iso, 0)
	require.NotNil(t, info)
	assert.Equal(t, "udf", info.Format,
		"pure-UDF media must be reported as udf: detectISOFormat reads offset 0x8001, "+
			"which is sector 16 (BEA01), but the NSR02 descriptor sits at sector 17")
}

func TestISOPreflight_TooSmall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tiny.iso")
	require.NoError(t, os.WriteFile(p, make([]byte, 1024), 0644))
	info, err := ISOPreflight(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
	assert.NotNil(t, info)
}

func TestISOPreflight_NoBootEFI(t *testing.T) {
	iso := buildTestISOWithoutBootloader(t)
	_, err := isoPreflight(iso, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BOOTAA64.EFI not found")
}

// InstallerBootloader must fall back to the sidecar written at mastering
// time when the ISO itself is unreadable (pure UDF, no 7z/bsdtar on the
// host) — the condition that left the answer volume without BOOTAA64.EFI in
// run 20260812T081924.
func TestInstallerBootloader_SidecarFallback(t *testing.T) {
	iso := buildPureUDFISO(t)
	loader := peARM64Stub()
	require.NoError(t, os.WriteFile(isokit.BootloaderSidecarPath(iso), loader, 0o644))

	got, err := InstallerBootloader(iso)
	require.NoError(t, err)
	assert.Equal(t, loader, got)
}

func TestInstallerBootloader_NoSourceAtAll(t *testing.T) {
	iso := buildPureUDFISO(t)
	_, err := InstallerBootloader(iso)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar")
}

func TestInstallerBootloader_ReadsFromISO(t *testing.T) {
	iso := buildTestISOWithBootloader(t, peARM64Stub())
	got, err := InstallerBootloader(iso)
	require.NoError(t, err)
	assert.Equal(t, peARM64Stub(), got)
}

// A cached installer ISO with no El Torito EFI catalog cannot be booted by
// the firmware — run 20260812T081924 spent a build cycle discovering that at
// the EFI shell. The cache check must reject such an image so it gets
// re-mastered instead of reused.
func TestWindowsISOBootable_RejectsPureUDFWithoutElTorito(t *testing.T) {
	iso := buildPureUDFISO(t)
	err := WindowsISOBootable(iso)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "El Torito")
}

func TestWindowsISOBootable_AcceptsInjectedElTorito(t *testing.T) {
	iso := buildPureUDFISO(t)
	require.NoError(t, isokit.AddElToritoEFIBoot(iso, []byte("boot-image")))
	assert.NoError(t, WindowsISOBootable(iso))
}

// LoadWinPEStorageDrivers pulls the ARM64 vioscsi driver out of the
// virtio-win ISO so the answer volume can carry it for the windowsPE
// drvload (CELL-429). virtio-win names its per-OS directories
// inconsistently across releases, so several are probed.
func TestLoadWinPEStorageDrivers_FromFixtureISO(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "virtio.iso")
	require.NoError(t, isokit.CreateSimpleISO(isoPath, map[string][]byte{
		"/vioscsi/2k25/ARM64/vioscsi.inf": []byte("[Version]\r\n"),
		"/vioscsi/2k25/ARM64/vioscsi.sys": {0x4D, 0x5A},
		"/vioscsi/2k25/ARM64/vioscsi.cat": {0x30},
	}))

	drivers, err := LoadWinPEStorageDrivers(isoPath)
	require.NoError(t, err)
	assert.Contains(t, drivers, "/drivers/vioscsi/vioscsi.inf")
	assert.Contains(t, drivers, "/drivers/vioscsi/vioscsi.sys")
	assert.Contains(t, drivers, "/drivers/vioscsi/vioscsi.cat")
	assert.Equal(t, []byte("[Version]\r\n"), drivers["/drivers/vioscsi/vioscsi.inf"])
}

func TestLoadWinPEStorageDrivers_NoDriverInISO(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "empty.iso")
	require.NoError(t, isokit.CreateSimpleISO(isoPath, map[string][]byte{
		"/README.txt": []byte("no drivers"),
	}))
	_, err := LoadWinPEStorageDrivers(isoPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vioscsi")
}

func TestValidateBootloaderPE_ValidARM64(t *testing.T) {
	info, err := ValidateBootloaderPE(peARM64Stub())
	require.NoError(t, err)
	assert.Equal(t, "aarch64", info.Arch)
	assert.True(t, info.Size > 0)
}

func TestValidateBootloaderPE_RejectsX64(t *testing.T) {
	_, err := ValidateBootloaderPE(peX64Stub())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "x86_64")
}

func TestValidateBootloaderPE_RejectsNonPE(t *testing.T) {
	_, err := ValidateBootloaderPE([]byte("not a PE binary at all, needs to be long enough"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PE")
}

func peARM64Stub() []byte {
	return buildPEStub(0xAA64)
}

func peX64Stub() []byte {
	return buildPEStub(0x8664)
}

func buildPEStub(machine uint16) []byte {
	pe := make([]byte, 256)
	pe[0], pe[1] = 'M', 'Z'
	pe[0x3C] = 0x80
	pe[0x80], pe[0x81], pe[0x82], pe[0x83] = 'P', 'E', 0, 0
	pe[0x84] = byte(machine)
	pe[0x85] = byte(machine >> 8)
	return pe
}

func buildTestISOWithBootloader(t *testing.T, bootloader []byte) string {
	t.Helper()
	isoPath := t.TempDir() + "/test.iso"
	err := isokit.CreateSimpleISO(isoPath, map[string][]byte{
		"/EFI/BOOT/BOOTAA64.EFI": bootloader,
	})
	require.NoError(t, err)
	return isoPath
}

func buildTestISOWithoutBootloader(t *testing.T) string {
	t.Helper()
	isoPath := t.TempDir() + "/test.iso"
	err := isokit.CreateSimpleISO(isoPath, map[string][]byte{
		"/README.txt": []byte("no bootloader here"),
	})
	require.NoError(t, err)
	return isoPath
}
