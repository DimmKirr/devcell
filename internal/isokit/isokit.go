package isokit

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// CreateSimpleISO creates an ISO 9660 image with Rock Ridge extensions at
// isoPath containing the given files. Keys are absolute paths (e.g.
// "/autounattend.xml"), values are file content.
func CreateSimpleISO(isoPath string, files map[string][]byte) error {
	if len(files) == 0 {
		return fmt.Errorf("no files to add to ISO")
	}

	var totalSize int64
	for _, data := range files {
		totalSize += int64(len(data))
	}
	diskSize := totalSize + 10*1024*1024
	if diskSize < 20*1024*1024 {
		diskSize = 20 * 1024 * 1024
	}

	os.Remove(isoPath)
	d, err := diskfs.Create(isoPath, diskSize, diskfs.SectorSize4k)
	if err != nil {
		return fmt.Errorf("creating disk image: %w", err)
	}
	d.LogicalBlocksize = 2048

	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "DATA",
	}
	fs, err := d.CreateFilesystem(fspec)
	if err != nil {
		return fmt.Errorf("creating filesystem: %w", err)
	}

	if err := addFilesToFS(fs, files); err != nil {
		return err
	}

	isoFS := fs.(*iso9660.FileSystem)
	if err := isoFS.Finalize(iso9660.FinalizeOptions{RockRidge: true}); err != nil {
		return fmt.Errorf("finalizing ISO: %w", err)
	}

	return nil
}

func addFilesToFAT(fs filesystem.FileSystem, files map[string][]byte) error {
	dirs := map[string]bool{}
	for filePath := range files {
		dir := path.Dir(filePath)
		for dir != "/" && dir != "." && !dirs[dir] {
			dirs[dir] = true
			dir = path.Dir(dir)
		}
	}

	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)

	for _, dir := range sortedDirs {
		if err := fs.Mkdir(dir); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	for filePath, data := range files {
		if err := writeFileToFS(fs, filePath, data, os.O_CREATE|os.O_RDWR); err != nil {
			return err
		}
	}
	return nil
}

// dirEntryFlusher is implemented by go-diskfs FAT files. Any of its setters
// rewrites the parent directory entries — see writeFileToFS.
type dirEntryFlusher interface {
	SetReadOnly(bool) error
}

// writeFileToFS writes one file and flushes its directory entry.
//
// go-diskfs (v1.9.4) FAT Write() updates the in-memory directory entry size
// but never persists it, and Close() only drops the filesystem pointer. The
// on-disk size therefore stays at the cluster-rounded value from space
// allocation, so reads return trailing padding — e.g. a 6129-byte
// autounattend.xml reads back as 6144 bytes with junk after </unattend>,
// which Windows Setup can reject. Calling a setter forces the library to
// rewrite the directory entries, persisting the correct size.
func writeFileToFS(fs filesystem.FileSystem, filePath string, data []byte, flag int) error {
	rw, err := fs.OpenFile(filePath, flag)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filePath, err)
	}
	if _, err := rw.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	if f, ok := rw.(dirEntryFlusher); ok {
		if err := f.SetReadOnly(false); err != nil {
			return fmt.Errorf("flushing directory entry for %s: %w", filePath, err)
		}
	}
	if c, ok := rw.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return fmt.Errorf("closing %s: %w", filePath, err)
		}
	}
	return nil
}

func addFilesToFS(fs filesystem.FileSystem, files map[string][]byte) error {
	dirs := map[string]bool{}
	for filePath := range files {
		dir := path.Dir(filePath)
		for dir != "/" && dir != "." && !dirs[dir] {
			dirs[dir] = true
			dir = path.Dir(dir)
		}
	}

	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)

	for _, dir := range sortedDirs {
		if err := fs.Mkdir(dir); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	for filePath, data := range files {
		if err := writeFileToFS(fs, filePath, data, os.O_CREATE|os.O_WRONLY); err != nil {
			return err
		}
	}
	return nil
}

// ElToritoInfo describes the El Torito boot catalog of an ISO image.
type ElToritoInfo struct {
	// PlatformID from the validation entry: 0x00 = 80x86, 0xEF = EFI.
	// UEFI firmware requires 0xEF; 0x00 marks the image as BIOS-bootable only.
	PlatformID byte
	// Bootable is true when the default entry's boot indicator is 0x88.
	Bootable bool
	// LoadRBA is the absolute sector (2048-byte) of the boot image.
	LoadRBA uint32
	// SectorCount is the number of 512-byte virtual sectors to load.
	SectorCount uint16
}

// InspectElTorito parses the El Torito boot catalog of an ISO image. It
// returns an error if the image has no Boot Record Volume Descriptor.
func InspectElTorito(isoPath string) (*ElToritoInfo, error) {
	f, err := os.Open(isoPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const sectorSize = 2048

	// Scan volume descriptors from sector 16 for the Boot Record (type 0).
	var brvd []byte
	buf := make([]byte, sectorSize)
	for sector := int64(16); ; sector++ {
		if _, err := f.ReadAt(buf, sector*sectorSize); err != nil {
			return nil, fmt.Errorf("reading volume descriptor at sector %d: %w", sector, err)
		}
		if string(buf[1:6]) != "CD001" {
			break
		}
		if buf[0] == 0 && strings.HasPrefix(string(buf[7:39]), "EL TORITO SPECIFICATION") {
			brvd = append([]byte(nil), buf...)
			break
		}
		if buf[0] == 255 { // set terminator
			break
		}
	}
	if brvd == nil {
		return nil, fmt.Errorf("%s: no El Torito boot record volume descriptor", isoPath)
	}

	catalogSector := int64(binary.LittleEndian.Uint32(brvd[0x47:]))
	cat := make([]byte, sectorSize)
	if _, err := f.ReadAt(cat, catalogSector*sectorSize); err != nil {
		return nil, fmt.Errorf("reading boot catalog at sector %d: %w", catalogSector, err)
	}

	if cat[0] != 0x01 || cat[0x1e] != 0x55 || cat[0x1f] != 0xaa {
		return nil, fmt.Errorf("%s: invalid El Torito validation entry", isoPath)
	}

	entry := cat[0x20:0x40]
	return &ElToritoInfo{
		PlatformID:  cat[1],
		Bootable:    entry[0] == 0x88,
		SectorCount: binary.LittleEndian.Uint16(entry[6:]),
		LoadRBA:     binary.LittleEndian.Uint32(entry[8:]),
	}, nil
}

// efiBootImage picks the El Torito boot image from the staged tree. Prefers
// efisys_noprompt.bin: the plain efisys.bin cdboot shows "Press any key to
// boot from CD or DVD..." and returns EFI_TIMEOUT when unattended, dropping
// the VM to the EFI Shell. The noprompt variant boots straight through.
func efiBootImage(stageDir string) (string, error) {
	bootDir := filepath.Join("efi", "microsoft", "boot")
	for _, name := range []string{"efisys_noprompt.bin", "efisys.bin"} {
		rel := filepath.Join(bootDir, name)
		if _, err := os.Stat(filepath.Join(stageDir, rel)); err == nil {
			return rel, nil
		}
	}
	return "", fmt.Errorf("EFI boot file not found in stage dir: %s", filepath.Join(bootDir, "efisys.bin"))
}

// CreateWindowsISO creates a bootable Windows installer ISO from a staged
// directory tree. The stageDir must contain efi/microsoft/boot/efisys.bin
// (or efisys_noprompt.bin, preferred) for EFI boot.
//
// On Linux: uses genisoimage/mkisofs with UDF + El Torito.
// On macOS: uses hdiutil makehybrid (built-in) as primary, genisoimage/mkisofs
// as fallback. hdiutil produces a UDF+ISO9660 hybrid that UEFI firmware boots
// natively without an El Torito catalog.
func CreateWindowsISO(isoPath, stageDir, volumeLabel string) error {
	if volumeLabel == "" {
		volumeLabel = "YOURISO"
	}

	efiBootFile, err := efiBootImage(stageDir)
	if err != nil {
		return err
	}

	os.Remove(isoPath)

	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("hdiutil"); err == nil {
			return createWindowsISOHdiutil(p, isoPath, stageDir, volumeLabel)
		}
	}

	geniso, err := findGenISO()
	if err != nil {
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("neither hdiutil nor genisoimage/mkisofs found; " +
				"install cdrtools (brew install cdrtools)")
		}
		return err
	}
	return createWindowsISOGeniso(geniso, isoPath, stageDir, efiBootFile, volumeLabel)
}

func createWindowsISOGeniso(geniso, isoPath, stageDir, efiBootFile, volumeLabel string) error {
	cmd := exec.Command(geniso,
		"-efi-boot", efiBootFile,
		"--no-emul-boot",
		"--udf",
		"-iso-level", "3",
		"--allow-limited-size",
		"-V", volumeLabel,
		"-o", isoPath,
		stageDir,
	)
	cmd.Stderr = io.Discard
	cmd.Stdout = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(geniso), err)
	}
	return nil
}

func createWindowsISOHdiutil(hdiutil, isoPath, stageDir, volumeLabel string) error {
	// hdiutil appends .cdr to the output path — strip any extension we provide
	// and rename afterwards.
	base := strings.TrimSuffix(isoPath, filepath.Ext(isoPath))
	cdrPath := base + ".cdr"
	os.Remove(cdrPath)

	cmd := exec.Command(hdiutil, "makehybrid",
		"-o", base,
		"-udf",
		"-default-volume-name", volumeLabel,
		stageDir,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hdiutil makehybrid failed: %w\n%s", err, stderr.String())
	}

	if cdrPath != isoPath {
		if err := os.Rename(cdrPath, isoPath); err != nil {
			return fmt.Errorf("renaming %s → %s: %w", cdrPath, isoPath, err)
		}
	}
	return nil
}

func findGenISO() (string, error) {
	for _, name := range []string{"genisoimage", "mkisofs"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("genisoimage or mkisofs not found; " +
		"install cdrkit (Linux: apt install genisoimage, macOS: brew install cdrtools)")
}

func dirTreeSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func collectFiles(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		isoPath := "/" + filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[isoPath] = data
		return nil
	})
	return files, err
}

// CreateFATImage creates a FAT32 disk image at imgPath containing the given
// files. Keys are absolute paths (e.g. "/startup.nsh"), values are file content.
// UEFI firmware mounts FAT natively, so this is the right format for images
// that need to be visible as an FS device in the UEFI shell.
func CreateFATImage(imgPath string, files map[string][]byte) error {
	if len(files) == 0 {
		return fmt.Errorf("no files to add to FAT image")
	}

	var totalSize int64
	for _, data := range files {
		totalSize += int64(len(data))
	}
	// Floor of 64MB, never less than content + headroom. go-diskfs always
	// lays the volume out as FAT32, and the FAT spec makes any volume under
	// 65525 data clusters a FAT16 volume BY DEFINITION — parsers type-detect
	// from the cluster count, not the BPB layout. A 20MB image was therefore
	// self-contradictory; EDK2 misparsed it as FAT16 and data-aborted at boot
	// (run 20260729T184712). 64MB yields ~130k clusters of 512B: legal FAT32.
	diskSize := totalSize + 10*1024*1024
	if diskSize < 64*1024*1024 {
		diskSize = 64 * 1024 * 1024
	}

	os.Remove(imgPath)
	d, err := diskfs.Create(imgPath, diskSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("creating FAT disk image: %w", err)
	}

	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeFat32,
		VolumeLabel: "UEFIBOOT",
	}
	fs, err := d.CreateFilesystem(fspec)
	if err != nil {
		return fmt.Errorf("creating FAT32 filesystem: %w", err)
	}

	if err := addFilesToFAT(fs, files); err != nil {
		return err
	}

	// Closing the disk flushes the directory entries written above; without
	// it the image on disk keeps stale sizes.
	if err := d.Close(); err != nil {
		return fmt.Errorf("finalizing FAT image: %w", err)
	}

	// Verify every file reads back byte-identical. go-diskfs v1.9.4 records a
	// cluster-rounded directory entry size when a file ends within 63 bytes of
	// a cluster boundary, which appends garbage to the file. Silently shipping
	// a corrupt autounattend.xml would surface as an unexplained Windows Setup
	// failure much later, so fail here instead.
	for filePath, want := range files {
		got, err := ReadFileFromFAT(imgPath, filePath)
		if err != nil {
			return fmt.Errorf("verifying %s in FAT image: %w", filePath, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("FAT image verification failed for %s: wrote %d bytes, read back %d "+
				"(go-diskfs size bug near cluster boundary — pad the payload)",
				filePath, len(want), len(got))
		}
	}
	return nil
}

// ReadFileFromFAT reads a file from a FAT32 disk image and returns its content.
func ReadFileFromFAT(imgPath, filePath string) ([]byte, error) {
	d, err := diskfs.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("opening FAT image: %w", err)
	}

	fs, err := d.GetFilesystem(0)
	if err != nil {
		return nil, fmt.Errorf("reading filesystem: %w", err)
	}

	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	candidates := []string{filePath, strings.ToUpper(filePath), strings.ToLower(filePath)}

	var rdr io.ReadCloser
	var openErr error
	for _, p := range candidates {
		rdr, openErr = fs.OpenFile(p, os.O_RDONLY)
		if openErr == nil {
			break
		}
	}
	if openErr != nil {
		return nil, fmt.Errorf("opening %s: %w", filePath, openErr)
	}

	data, err := readAllGuarded(rdr)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	return data, nil
}

// readAllGuarded turns a panic inside the FAT reader into an error.
//
// go-diskfs v1.9.4 indexes the cluster chain using the directory entry's size
// field without checking the two agree, so an entry claiming more bytes than
// the chain holds panics with "slice bounds out of range". That is exactly what
// a volume looks like while a guest is mid-write to it — and this function
// exists to read *diagnostics*, so crashing here takes down the tool reached
// for when something has already gone wrong. Same library version that silently
// truncated files on write (see padForFAT); its FAT handling is not trusted to
// fail gracefully.
func readAllGuarded(r io.Reader) (data []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			data, err = nil, fmt.Errorf("corrupt or partially written FAT volume: %v", rec)
		}
	}()
	return io.ReadAll(r)
}

// ReadFileFromISO reads a file from an ISO 9660 image and returns its content.
func ReadFileFromISO(isoPath, filePath string) ([]byte, error) {
	d, err := diskfs.Open(isoPath)
	if err != nil {
		return nil, fmt.Errorf("opening ISO: %w", err)
	}

	fs, err := d.GetFilesystem(0)
	if err != nil {
		return nil, fmt.Errorf("reading filesystem: %w", err)
	}

	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	candidates := []string{filePath, strings.ToUpper(filePath), strings.ToLower(filePath)}

	var rdr io.ReadCloser
	var openErr error
	for _, p := range candidates {
		rdr, openErr = fs.OpenFile(p, os.O_RDONLY)
		if openErr == nil {
			break
		}
	}
	if openErr != nil {
		return nil, fmt.Errorf("opening %s: %w", filePath, openErr)
	}

	data, err := io.ReadAll(rdr)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	return data, nil
}

// FindFileExtent returns the starting sector (2048-byte LBA) and size of a
// file on an ISO 9660 image. Needed to point boot structures at a file, which
// ReadFileFromISO cannot express since it only returns contents.
func FindFileExtent(isoPath, filePath string) (uint32, int64, error) {
	f, err := os.Open(isoPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	pvd, err := readPrimaryVolumeDescriptor(f)
	if err != nil {
		return 0, 0, err
	}

	// Root directory record lives at offset 156 of the PVD.
	extent := binary.LittleEndian.Uint32(pvd[156+2:])
	size := binary.LittleEndian.Uint32(pvd[156+10:])

	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	for i, part := range parts {
		entries, err := readDirectoryRecords(f, extent, size)
		if err != nil {
			return 0, 0, err
		}
		e, ok := lookupISOEntry(entries, part)
		if !ok {
			return 0, 0, fmt.Errorf("%s: %s not found in ISO", isoPath, filePath)
		}
		if i == len(parts)-1 {
			return e.extent, int64(e.size), nil
		}
		extent, size = e.extent, e.size
	}
	return 0, 0, fmt.Errorf("%s: %s not found in ISO", isoPath, filePath)
}

// SetElToritoBootImage repoints an ISO's default El Torito boot entry at a
// different image. Windows media carries both a prompting boot image
// (efisys.bin, "Press any key to boot from CD or DVD") and a silent one
// (efisys_noprompt.bin); unattended installs want the latter, and stock
// Microsoft ISOs always ship with the prompting one selected.
func SetElToritoBootImage(isoPath string, loadRBA uint32, sectorCount uint16) error {
	info, err := InspectElTorito(isoPath)
	if err != nil {
		return err
	}
	_ = info // validated above; catalog is well-formed

	f, err := os.OpenFile(isoPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	catalogSector, err := elToritoCatalogSector(f)
	if err != nil {
		return err
	}

	// Default entry starts 0x20 bytes into the catalog: sector count at +6,
	// load RBA at +8.
	entryOff := int64(catalogSector)*isoSectorSize + 0x20
	var buf [4]byte
	binary.LittleEndian.PutUint16(buf[:2], sectorCount)
	if _, err := f.WriteAt(buf[:2], entryOff+6); err != nil {
		return fmt.Errorf("writing sector count: %w", err)
	}
	binary.LittleEndian.PutUint32(buf[:], loadRBA)
	if _, err := f.WriteAt(buf[:], entryOff+8); err != nil {
		return fmt.Errorf("writing load RBA: %w", err)
	}
	return nil
}

const isoSectorSize = 2048

type isoDirEntry struct {
	extent uint32
	size   uint32
	isDir  bool
}

func readPrimaryVolumeDescriptor(f *os.File) ([]byte, error) {
	buf := make([]byte, isoSectorSize)
	for sector := int64(16); sector < 32; sector++ {
		if _, err := f.ReadAt(buf, sector*isoSectorSize); err != nil {
			return nil, err
		}
		if string(buf[1:6]) != "CD001" {
			break
		}
		if buf[0] == 1 {
			return append([]byte(nil), buf...), nil
		}
		if buf[0] == 255 {
			break
		}
	}
	return nil, fmt.Errorf("no primary volume descriptor found")
}

func readDirectoryRecords(f *os.File, extent, size uint32) (map[string]isoDirEntry, error) {
	data := make([]byte, size)
	if _, err := f.ReadAt(data, int64(extent)*isoSectorSize); err != nil {
		return nil, fmt.Errorf("reading directory at sector %d: %w", extent, err)
	}

	entries := map[string]isoDirEntry{}
	for i := 0; i < len(data); {
		recLen := int(data[i])
		if recLen == 0 {
			// Records never span sector boundaries; skip to the next sector.
			i = (i/isoSectorSize + 1) * isoSectorSize
			continue
		}
		if i+recLen > len(data) {
			break
		}
		rec := data[i : i+recLen]
		nameLen := int(rec[32])
		name := string(rec[33 : 33+nameLen])
		if name != "\x00" && name != "\x01" {
			name = strings.ToUpper(strings.SplitN(name, ";", 2)[0])
			entries[name] = isoDirEntry{
				extent: binary.LittleEndian.Uint32(rec[2:]),
				size:   binary.LittleEndian.Uint32(rec[10:]),
				isDir:  rec[25]&2 != 0,
			}
		}
		i += recLen
	}
	return entries, nil
}

func elToritoCatalogSector(f *os.File) (uint32, error) {
	buf := make([]byte, isoSectorSize)
	for sector := int64(16); sector < 32; sector++ {
		if _, err := f.ReadAt(buf, sector*isoSectorSize); err != nil {
			return 0, err
		}
		if string(buf[1:6]) != "CD001" {
			break
		}
		if buf[0] == 0 && strings.HasPrefix(string(buf[7:39]), "EL TORITO SPECIFICATION") {
			return binary.LittleEndian.Uint32(buf[0x47:]), nil
		}
		if buf[0] == 255 {
			break
		}
	}
	return 0, fmt.Errorf("no El Torito boot record found")
}

// lookupISOEntry finds a directory entry by name, tolerating ISO 9660 Level 1
// name mangling. Windows media uses long names, but images written at Level 1
// truncate to 8.3 ("microsoft" becomes "MICROSOF"), so an exact match alone
// fails depending on which tool produced the image.
func lookupISOEntry(entries map[string]isoDirEntry, want string) (isoDirEntry, bool) {
	upper := strings.ToUpper(want)
	if e, ok := entries[upper]; ok {
		return e, true
	}
	for name, e := range entries {
		if strings.HasPrefix(upper, name) || isoNameMatches8Dot3(name, upper) {
			return e, true
		}
	}
	return isoDirEntry{}, false
}

// isoNameMatches8Dot3 compares a possibly-truncated 8.3 name against a full
// name: stem truncated to 8 characters, extension to 3.
func isoNameMatches8Dot3(candidate, want string) bool {
	cStem, cExt, _ := strings.Cut(candidate, ".")
	wStem, wExt, _ := strings.Cut(want, ".")
	if len(wStem) > 8 {
		wStem = wStem[:8]
	}
	if len(wExt) > 3 {
		wExt = wExt[:3]
	}
	return cStem == wStem && cExt == wExt
}
