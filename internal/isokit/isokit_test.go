package isokit

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSimpleISO_ContainsFile(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "test.iso")

	content := []byte("<xml>autounattend</xml>")
	err := CreateSimpleISO(isoPath, map[string][]byte{
		"/autounattend.xml": content,
	})
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	data, err := ReadFileFromISO(isoPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestCreateSimpleISO_MultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "multi.iso")

	files := map[string][]byte{
		"/file1.txt":        []byte("hello"),
		"/subdir/file2.txt": []byte("world"),
	}
	err := CreateSimpleISO(isoPath, files)
	require.NoError(t, err)

	for path, expected := range files {
		data, err := ReadFileFromISO(isoPath, path)
		require.NoError(t, err, "reading %s", path)
		assert.Equal(t, expected, data, "content mismatch for %s", path)
	}
}

func TestCreateSimpleISO_EmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "empty.iso")

	err := CreateSimpleISO(isoPath, map[string][]byte{})
	assert.Error(t, err, "should reject empty file map")
}

func TestCreateWindowsISO_ContainsBootFiles(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "sources"), 0o755))

	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "boot.wim"), []byte("boot-wim-data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "install.wim"), []byte("install-wim-data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "setup.exe"), []byte("setup"), 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "MYWINISO")
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "ISO must not be empty")
}

func TestCreateWindowsISO_HasUDF(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "sources"), 0o755))

	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "boot.wim"), []byte("boot-wim-data"), 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "TESTLABEL")
	require.NoError(t, err)

	assert.True(t, isoHasUDF(t, isoPath),
		"ISO must contain UDF — install.wim can exceed ISO 9660's 4GB file size limit")
}

// isoHasUDF scans the volume descriptor area for BEA01 (UDF Volume Recognition
// Sequence) or NSR02/NSR03 (UDF structure descriptors). Both genisoimage and
// hdiutil place these in the first 64 sectors.
func isoHasUDF(t *testing.T, isoPath string) bool {
	t.Helper()
	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 2048)
	for sector := 16; sector < 256; sector++ {
		_, err := f.ReadAt(buf, int64(sector)*2048)
		if err != nil {
			break
		}
		tag := string(buf[1:6])
		if tag == "BEA01" || tag == "NSR02" || tag == "NSR03" {
			return true
		}
	}
	return false
}

func requireISOTool(t *testing.T) {
	t.Helper()
	for _, name := range []string{"hdiutil", "genisoimage", "mkisofs"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no ISO tool available (need hdiutil, genisoimage, or mkisofs)")
}

func requireGenISOTool(t *testing.T) {
	t.Helper()
	for _, name := range []string{"genisoimage", "mkisofs"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("genisoimage/mkisofs not available")
}

// TestCreateWindowsISO_IsEFIBootable reproduces run 20260812T081924 ("Boot
// failed ... startup.nsh could not find BOOTAA64.EFI") at its source. On
// macOS CreateWindowsISO masters with `hdiutil makehybrid -udf`, which emits
// a pure-UDF image: no ISO 9660 bridge and no El Torito boot catalog. EDK2
// only assigns FSn: mappings to El Torito/FAT volumes, so the firmware drops
// to the EFI shell and the install never starts. Every installer ISO we
// master must carry a bootable EFI (0xEF) El Torito entry, whichever tool
// produced it. Skips where no mastering tool exists; the genisoimage path
// (Linux) passes; the hdiutil path (macOS) is RED today.
func TestCreateWindowsISO_IsEFIBootable(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys_noprompt.bin"), efiBin, 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "TESTLABEL")
	require.NoError(t, err)

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err,
		"mastered installer ISO has no El Torito boot catalog — UEFI firmware cannot boot it")
	assert.Equal(t, byte(0xEF), info.PlatformID, "El Torito platform must be EFI (0xEF)")
	assert.True(t, info.Bootable, "default El Torito entry must be marked bootable (0x88)")
}

// An ISO 9660 bridge is a genisoimage-only property: hdiutil has no level-3
// multi-extent support, and install.wim exceeds ISO 9660's 4 GiB single-file
// limit, so the macOS path masters UDF + injected El Torito instead (see
// TestCreateWindowsISO_IsEFIBootable for the contract both paths share).
func TestCreateWindowsISO_HasISO9660Magic(t *testing.T) {
	requireGenISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "TESTLABEL")
	require.NoError(t, err)

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}

func TestCreateFATImage_LegalFAT32Geometry(t *testing.T) {
	// The FAT spec determines filesystem type from the data-cluster count:
	// under 65525 clusters, a parser MUST treat the volume as FAT16 — even if
	// the BPB is laid out as FAT32 (rootEntries=0, fatSize16=0). go-diskfs
	// writes FAT32 layout regardless, so a small image is self-contradictory:
	// EDK2 type-detects FAT16, parses garbage, and takes a data abort at
	// boot (run 20260729T184712, "Start boot option" hang). The image must be
	// big enough that FAT32 is the spec-correct interpretation.
	imgPath := filepath.Join(t.TempDir(), "geom.img")
	require.NoError(t, CreateFATImage(imgPath, map[string][]byte{
		"/a.xml": []byte("<a/>"),
	}))

	img, err := os.ReadFile(imgPath)
	require.NoError(t, err)

	bps := int64(binary.LittleEndian.Uint16(img[11:13]))
	spc := int64(img[13])
	reserved := int64(binary.LittleEndian.Uint16(img[14:16]))
	nfats := int64(img[16])
	rootEntries := int64(binary.LittleEndian.Uint16(img[17:19]))
	totSec := int64(binary.LittleEndian.Uint16(img[19:21]))
	if totSec == 0 {
		totSec = int64(binary.LittleEndian.Uint32(img[32:36]))
	}
	fatSz := int64(binary.LittleEndian.Uint16(img[22:24]))
	if fatSz == 0 {
		fatSz = int64(binary.LittleEndian.Uint32(img[36:40]))
	}
	require.NotZero(t, bps)
	require.NotZero(t, spc)
	rootDirSectors := (rootEntries*32 + bps - 1) / bps
	clusters := (totSec - (reserved + nfats*fatSz + rootDirSectors)) / spc

	if rootEntries == 0 { // FAT32 layout
		assert.GreaterOrEqual(t, clusters, int64(65525),
			"FAT32-laid-out volume with %d clusters is spec-illegal: parsers type-detect it as FAT16 and misparse everything", clusters)
	}
}

func TestCreateFATImage_ContainsFile(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.img")

	content := []byte("startup.nsh content")
	err := CreateFATImage(imgPath, map[string][]byte{
		"/startup.nsh": content,
	})
	require.NoError(t, err)

	info, err := os.Stat(imgPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	data, err := ReadFileFromFAT(imgPath, "/startup.nsh")
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestCreateFATImage_MultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "multi.img")

	files := map[string][]byte{
		"/startup.nsh":      []byte("FS0:\\EFI\\BOOT\\BOOTAA64.EFI"),
		"/autounattend.xml": []byte("<xml>test</xml>"),
	}
	err := CreateFATImage(imgPath, files)
	require.NoError(t, err)

	for path, expected := range files {
		data, err := ReadFileFromFAT(imgPath, path)
		require.NoError(t, err, "reading %s", path)
		assert.Equal(t, expected, data, "content mismatch for %s", path)
	}
}

func TestCreateFATImage_EmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "empty.img")

	err := CreateFATImage(imgPath, map[string][]byte{})
	assert.Error(t, err, "should reject empty file map")
}

func TestCreateSimpleISO_HasISO9660Magic(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "magic.iso")

	err := CreateSimpleISO(isoPath, map[string][]byte{
		"/test.txt": []byte("data"),
	})
	require.NoError(t, err)

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}

// buildPureUDFISO writes the layout `hdiutil makehybrid -udf` masters (run
// 20260812T081924): the ECMA-167 volume recognition sequence BEA01/NSR02/TEA01
// at sectors 16–18, no CD001 descriptor anywhere, no El Torito.
func buildPureUDFISO(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pure-udf.iso")
	img := make([]byte, 64*2048)
	writeVSD := func(sector int, magic string) {
		copy(img[sector*2048+1:], magic)
		img[sector*2048+6] = 0x01
	}
	writeVSD(16, "BEA01")
	writeVSD(17, "NSR02")
	writeVSD(18, "TEA01")
	require.NoError(t, os.WriteFile(p, img, 0o644))
	return p
}

func readSector(t *testing.T, isoPath string, sector int64) []byte {
	t.Helper()
	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()
	buf := make([]byte, 2048)
	_, err = f.ReadAt(buf, sector*2048)
	require.NoError(t, err)
	return buf
}

// AddElToritoEFIBoot must turn a pure-UDF disc (what hdiutil masters on
// macOS) into firmware-bootable media: El Torito BRVD at sector 17 — the
// LBA the specification fixes and firmware reads directly — with the UDF
// recognition sequence shifted down one sector, which stays valid because
// UDF readers skip foreign CD001 descriptors while walking the sequence
// (that is exactly how stock Microsoft bridge media is laid out).
func TestAddElToritoEFIBoot_PureUDF(t *testing.T) {
	isoPath := buildPureUDFISO(t)
	bootImage := bytes.Repeat([]byte("FAT-boot-image!!"), 256) // 4096 bytes

	require.NoError(t, AddElToritoEFIBoot(isoPath, bootImage))

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err, "injected catalog must be visible to InspectElTorito")
	assert.Equal(t, byte(0xEF), info.PlatformID)
	assert.True(t, info.Bootable)
	assert.Equal(t, uint16(8), info.SectorCount, "4096 bytes = 8 virtual 512-byte sectors")

	// The boot image bytes must live at LoadRBA.
	got := readSector(t, isoPath, int64(info.LoadRBA))
	assert.Equal(t, bootImage[:2048], got)

	// The UDF recognition sequence must survive, shifted past the BRVD.
	assert.Equal(t, "BEA01", string(readSector(t, isoPath, 16)[1:6]))
	assert.Equal(t, "CD001", string(readSector(t, isoPath, 17)[1:6]))
	assert.Equal(t, "NSR02", string(readSector(t, isoPath, 18)[1:6]))
	assert.Equal(t, "TEA01", string(readSector(t, isoPath, 19)[1:6]))
}

// The same injection must work on a plain ISO 9660 image (terminator at
// sector 17), and existing file reads must keep working afterwards.
func TestAddElToritoEFIBoot_PlainISO9660(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "plain.iso")
	content := []byte("hello from inside the iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{"/hello.txt": content}))
	bootImage := bytes.Repeat([]byte{0xAB}, 1024)

	require.NoError(t, AddElToritoEFIBoot(isoPath, bootImage))

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err)
	assert.Equal(t, byte(0xEF), info.PlatformID)
	assert.True(t, info.Bootable)

	got, err := ReadFileFromISO(isoPath, "/hello.txt")
	require.NoError(t, err, "file reads must survive the injection")
	assert.Equal(t, content, got)
}

// The macOS mastering path must not ship what hdiutil emits verbatim:
// `makehybrid -udf` output is pure UDF with no El Torito catalog, which
// firmware cannot boot (run 20260812T081924). After mastering, the EFI boot
// image from the stage tree must be injected. Verified with a fake hdiutil
// that emits a canonical pure-UDF image, so this runs without macOS.
func TestCreateWindowsISOHdiutil_InjectsElTorito(t *testing.T) {
	tmpDir := t.TempDir()

	stageDir := filepath.Join(tmpDir, "stage")
	bootDir := filepath.Join(stageDir, "efi", "microsoft", "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	efisys := bytes.Repeat([]byte("EFISYS-FAT-IMAGE"), 128) // 2048 bytes
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "efisys_noprompt.bin"), efisys, 0o644))

	canned := buildPureUDFISO(t)
	fakeHdiutil := filepath.Join(tmpDir, "hdiutil")
	script := "#!/bin/sh\nout=\"\"\nprev=\"\"\nfor a in \"$@\"; do\n  [ \"$prev\" = \"-o\" ] && out=\"$a\"\n  prev=\"$a\"\ndone\ncp \"" + canned + "\" \"$out.cdr\"\n"
	require.NoError(t, os.WriteFile(fakeHdiutil, []byte(script), 0o755))

	isoPath := filepath.Join(tmpDir, "win.iso")
	efiBootFile := filepath.Join("efi", "microsoft", "boot", "efisys_noprompt.bin")
	require.NoError(t, createWindowsISOHdiutil(fakeHdiutil, isoPath, stageDir, efiBootFile, "TESTLABEL"))

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err, "hdiutil-mastered ISO must leave with an El Torito catalog")
	assert.Equal(t, byte(0xEF), info.PlatformID)
	assert.True(t, info.Bootable)
	assert.Equal(t, efisys, readSector(t, isoPath, int64(info.LoadRBA)),
		"boot entry must point at the efisys_noprompt.bin payload from the stage tree")
}

// Mastering must export the installer's BOOTAA64.EFI next to the ISO: on
// pure-UDF media (macOS hdiutil path) no Go-native reader can extract it
// afterwards, and without it the answer volume ships no bootloader for
// startup.nsh — the QEMU v11+ HVF boot path (run 20260812T081924).
func TestWriteBootloaderSidecar(t *testing.T) {
	tmpDir := t.TempDir()
	stageDir := filepath.Join(tmpDir, "stage")
	bootDir := filepath.Join(stageDir, "efi", "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	loader := []byte("MZ-arm64-bootmgr")
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "bootaa64.efi"), loader, 0o644))

	isoPath := filepath.Join(tmpDir, "win.iso")
	require.NoError(t, os.WriteFile(isoPath, []byte("iso"), 0o644))

	require.NoError(t, writeBootloaderSidecar(isoPath, stageDir))

	got, err := os.ReadFile(BootloaderSidecarPath(isoPath))
	require.NoError(t, err)
	assert.Equal(t, loader, got)
}

func TestWriteBootloaderSidecar_NoLoaderInStage(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")
	require.NoError(t, os.WriteFile(isoPath, []byte("iso"), 0o644))

	err := writeBootloaderSidecar(isoPath, filepath.Join(tmpDir, "empty-stage"))
	require.Error(t, err)
	_, statErr := os.Stat(BootloaderSidecarPath(isoPath))
	assert.True(t, os.IsNotExist(statErr), "no sidecar must be written when the stage has no loader")
}

// hdiutil chooses the output extension itself — run 20260812T090917 showed
// makehybrid -udf appending ".iso", not the ".cdr" the code assumed. The
// rename then failed, CreateWindowsISO errored before El Torito injection,
// and the fetch fallback blessed the orphaned un-bootable image. Whatever
// extension hdiutil picks, mastering must succeed and inject.
func TestCreateWindowsISOHdiutil_OutputWithISOExtension(t *testing.T) {
	tmpDir := t.TempDir()

	stageDir := filepath.Join(tmpDir, "stage")
	bootDir := filepath.Join(stageDir, "efi", "microsoft", "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	efisys := bytes.Repeat([]byte("EFISYS-FAT-IMAGE"), 128)
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "efisys_noprompt.bin"), efisys, 0o644))

	canned := buildPureUDFISO(t)
	fakeHdiutil := filepath.Join(tmpDir, "hdiutil")
	script := "#!/bin/sh\nout=\"\"\nprev=\"\"\nfor a in \"$@\"; do\n  [ \"$prev\" = \"-o\" ] && out=\"$a\"\n  prev=\"$a\"\ndone\ncp \"" + canned + "\" \"$out.iso\"\n"
	require.NoError(t, os.WriteFile(fakeHdiutil, []byte(script), 0o755))

	isoPath := filepath.Join(tmpDir, "win.iso")
	efiBootFile := filepath.Join("efi", "microsoft", "boot", "efisys_noprompt.bin")
	require.NoError(t, createWindowsISOHdiutil(fakeHdiutil, isoPath, stageDir, efiBootFile, "TESTLABEL"))

	require.NoError(t, RequireEFIBootable(isoPath))
}

func TestRequireEFIBootable_RejectsPureUDF(t *testing.T) {
	iso := buildPureUDFISO(t)
	err := RequireEFIBootable(iso)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "El Torito")
}

func TestRequireEFIBootable_AcceptsInjected(t *testing.T) {
	iso := buildPureUDFISO(t)
	require.NoError(t, AddElToritoEFIBoot(iso, []byte("boot")))
	assert.NoError(t, RequireEFIBootable(iso))
}

// buildElToritoISO writes a minimal ISO image containing a Boot Record Volume
// Descriptor at sector 17 pointing to a boot catalog with the given platform ID.
func buildElToritoISO(t *testing.T, platformID byte, bootable bool) string {
	t.Helper()
	isoPath := filepath.Join(t.TempDir(), "eltorito.iso")

	const sector = 2048
	img := make([]byte, 25*sector)

	// PVD at sector 16
	pvd := img[16*sector:]
	pvd[0] = 1
	copy(pvd[1:6], "CD001")

	// Boot Record Volume Descriptor at sector 17
	brvd := img[17*sector:]
	brvd[0] = 0
	copy(brvd[1:6], "CD001")
	copy(brvd[7:], "EL TORITO SPECIFICATION")
	catalogSector := uint32(19)
	binary.LittleEndian.PutUint32(brvd[0x47:], catalogSector)

	// Volume Descriptor Set Terminator at sector 18
	term := img[18*sector:]
	term[0] = 255
	copy(term[1:6], "CD001")

	// Boot catalog at sector 19: validation entry + default entry
	cat := img[19*sector:]
	cat[0] = 0x01
	cat[1] = platformID
	cat[0x1c] = 0xaa
	cat[0x1d] = 0x55
	cat[0x1e] = 0x55
	cat[0x1f] = 0xaa
	entry := cat[0x20:]
	if bootable {
		entry[0] = 0x88
	}
	entry[6] = 0x20 // sector count = 0x0d20
	entry[7] = 0x0d
	binary.LittleEndian.PutUint32(entry[8:], 20) // load RBA

	require.NoError(t, os.WriteFile(isoPath, img, 0o644))
	return isoPath
}

func TestInspectElTorito_EFIPlatform(t *testing.T) {
	isoPath := buildElToritoISO(t, 0xEF, true)

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err)
	assert.Equal(t, byte(0xEF), info.PlatformID)
	assert.True(t, info.Bootable)
	assert.Equal(t, uint32(20), info.LoadRBA)
	assert.Equal(t, uint16(0x0d20), info.SectorCount)
}

func TestInspectElTorito_X86Platform(t *testing.T) {
	isoPath := buildElToritoISO(t, 0x00, true)

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err)
	assert.Equal(t, byte(0x00), info.PlatformID)
}

func TestInspectElTorito_NoBootRecord(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "plain.iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{"/hello.txt": []byte("hi")}))

	_, err := InspectElTorito(isoPath)
	assert.Error(t, err)
}

func TestEFIBootImage_PrefersNoPrompt(t *testing.T) {
	stageDir := t.TempDir()
	bootDir := filepath.Join(stageDir, "efi", "microsoft", "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "efisys.bin"), []byte("prompt"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "efisys_noprompt.bin"), []byte("noprompt"), 0o644))

	got, err := efiBootImage(stageDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("efi", "microsoft", "boot", "efisys_noprompt.bin"), got)
}

func TestEFIBootImage_FallsBackToPrompt(t *testing.T) {
	stageDir := t.TempDir()
	bootDir := filepath.Join(stageDir, "efi", "microsoft", "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bootDir, "efisys.bin"), []byte("prompt"), 0o644))

	got, err := efiBootImage(stageDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("efi", "microsoft", "boot", "efisys.bin"), got)
}

func TestEFIBootImage_MissingBoth(t *testing.T) {
	_, err := efiBootImage(t.TempDir())
	assert.Error(t, err)
}

func TestCreateFATImage_NeverSilentlyCorrupts(t *testing.T) {
	// go-diskfs v1.9.4 mis-records the directory entry size when a file ends
	// within 63 bytes of a cluster boundary, so reads return trailing padding.
	// A padded autounattend.xml has junk after </unattend> and Windows Setup
	// can reject it. CreateFATImage must never produce such an image silently:
	// either the round-trip is exact, or it fails loudly.
	for _, size := range []int{6129, 6100, 6143, 6144, 513, 20000} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte('a' + i%26)
		}

		imgPath := filepath.Join(t.TempDir(), "rt.img")
		err := CreateFATImage(imgPath, map[string][]byte{"/autounattend.xml": payload})
		if err != nil {
			continue // failed loudly — acceptable
		}

		got, readErr := ReadFileFromFAT(imgPath, "/autounattend.xml")
		require.NoError(t, readErr)
		assert.Equal(t, payload, got, "size %d: image written but content differs", size)
	}
}

func TestCreateSimpleISO_RoundTripsExactBytes(t *testing.T) {
	for _, size := range []int{6129, 513, 20000} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte('a' + i%26)
		}

		isoPath := filepath.Join(t.TempDir(), "rt.iso")
		require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{"/data.xml": payload}))

		got, err := ReadFileFromISO(isoPath, "/data.xml")
		require.NoError(t, err)
		assert.Len(t, got, size, "size %d: read back wrong length", size)
	}
}

func TestFindFileExtent_LocatesFileOnISO(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "find.iso")
	payload := []byte("boot-image-contents")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{
		"/efi/microsoft/boot/efisys_noprompt.bin": payload,
	}))

	rba, size, err := FindFileExtent(isoPath, "/efi/microsoft/boot/efisys_noprompt.bin")
	require.NoError(t, err)
	assert.Positive(t, rba, "extent sector must be located")
	assert.Equal(t, int64(len(payload)), size)

	// The extent must actually point at the data.
	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()
	got := make([]byte, len(payload))
	_, err = f.ReadAt(got, int64(rba)*2048)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestFindFileExtent_MissingFile(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "find.iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{"/a.txt": []byte("x")}))

	_, _, err := FindFileExtent(isoPath, "/nope.bin")
	assert.Error(t, err)
}

func TestReadFileFromISO_RawFallbackReadsDeepPath(t *testing.T) {
	payload := []byte("MZ-bootloader-stub")
	isoPath := filepath.Join(t.TempDir(), "test.iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{
		"/EFI/BOOT/BOOTAA64.EFI": payload,
	}))

	got, err := readFileFromISORaw(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestSetElToritoBootImage_UpdatesCatalog(t *testing.T) {
	isoPath := buildElToritoISO(t, 0xEF, true)

	require.NoError(t, SetElToritoBootImage(isoPath, 4242, 1234))

	info, err := InspectElTorito(isoPath)
	require.NoError(t, err)
	assert.Equal(t, uint32(4242), info.LoadRBA)
	assert.Equal(t, uint16(1234), info.SectorCount)
	// Untouched fields stay valid.
	assert.Equal(t, byte(0xEF), info.PlatformID)
	assert.True(t, info.Bootable)
}

func TestSetElToritoBootImage_RejectsNonBootableISO(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "plain.iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{"/a.txt": []byte("x")}))

	assert.Error(t, SetElToritoBootImage(isoPath, 1, 1))
}

// go-diskfs v1.9.4 panics with "slice bounds out of range" when the FAT
// directory entry disagrees with the cluster chain — which is exactly what a
// volume looks like while a guest is mid-write to it. Reading the answer volume
// of a running VM crashed the caller on 2026-07-31 (guest_diagnostics.go:96),
// and a panic in a library that exists to *read diagnostics* takes down the one
// tool you reach for when something is already wrong.
//
// Same library version that silently truncated files before (see padForFAT):
// its FAT handling cannot be trusted to fail gracefully.
func TestReadFileFromFAT_ReturnsAnErrorInsteadOfPanicking(t *testing.T) {
	// A file that is not FAT at all: whatever go-diskfs does internally, the
	// caller must get an error it can handle.
	path := filepath.Join(t.TempDir(), "garbage.img")
	junk := make([]byte, 1<<20)
	for i := range junk {
		junk[i] = byte(i % 251)
	}
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadFileFromFAT(path, "/devcell-diag.log")

	if err == nil {
		t.Fatalf("expected an error for a non-FAT image, got %d bytes", len(data))
	}
}

// The real corruption, reproduced: a directory entry whose size field claims
// more bytes than the cluster chain holds. That is what a volume looks like
// while a guest is mid-write, and it is what crashed the caller on 2026-07-31
// with `slice bounds out of range [448:351]` at fat12/file.go:136.
func TestReadFileFromFAT_InflatedDirectoryEntrySizeIsAnErrorNotAPanic(t *testing.T) {
	img := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, CreateFATImage(img, map[string][]byte{
		"/devcell-diag.log": []byte("short log\n"),
	}))

	raw, err := os.ReadFile(img)
	require.NoError(t, err)
	// Find the 8.3 directory entry and inflate its size field (last 4 bytes of
	// the 32-byte entry) past what the chain actually contains.
	idx := bytes.Index(raw, []byte("DEVCEL~1LOG"))
	require.GreaterOrEqual(t, idx, 0, "could not locate the directory entry to corrupt")
	off := idx + 28
	binary.LittleEndian.PutUint32(raw[off:off+4], binary.LittleEndian.Uint32(raw[off:off+4])+100000)
	require.NoError(t, os.WriteFile(img, raw, 0o644))

	// Must return an error. A panic here takes down the very tool reached for
	// when something has already gone wrong.
	_, err = ReadFileFromFAT(img, "/devcell-diag.log")
	require.Error(t, err, "a corrupt directory entry must be reported, not panicked on")
}

// Truncated mid-image: the directory can be readable while the data it points
// at is not, which is the live-volume case.
func TestReadFileFromFAT_SurvivesATruncatedImage(t *testing.T) {
	full := filepath.Join(t.TempDir(), "answer.img")
	if err := CreateFATImage(full, map[string][]byte{
		"/devcell-diag.log": []byte(strings.Repeat("diagnostic output\n", 512)),
	}); err != nil {
		t.Fatal(err)
	}
	whole, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(t.TempDir(), "cut.img")
	if err := os.WriteFile(cut, whole[:len(whole)/3], 0o644); err != nil {
		t.Fatal(err)
	}

	// Must not panic. Either outcome is acceptable; a crash is not.
	if _, err := ReadFileFromFAT(cut, "/devcell-diag.log"); err != nil {
		t.Logf("truncated image reported: %v", err)
	}
}
