package isokit

import (
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
		rw, err := fs.OpenFile(filePath, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("creating %s: %w", filePath, err)
		}
		if _, err := rw.Write(data); err != nil {
			return fmt.Errorf("writing %s: %w", filePath, err)
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
		rw, err := fs.OpenFile(filePath, os.O_CREATE|os.O_WRONLY)
		if err != nil {
			return fmt.Errorf("creating %s: %w", filePath, err)
		}
		if _, err := rw.Write(data); err != nil {
			return fmt.Errorf("writing %s: %w", filePath, err)
		}
	}
	return nil
}

// CreateWindowsISO creates a bootable Windows installer ISO from a staged
// directory tree. The stageDir must contain efi/microsoft/boot/efisys.bin for
// EFI boot.
//
// On Linux: uses genisoimage/mkisofs with UDF + El Torito.
// On macOS: uses hdiutil makehybrid (built-in) as primary, genisoimage/mkisofs
// as fallback. hdiutil produces a UDF+ISO9660 hybrid that UEFI firmware boots
// natively without an El Torito catalog.
func CreateWindowsISO(isoPath, stageDir, volumeLabel string) error {
	if volumeLabel == "" {
		volumeLabel = "YOURISO"
	}

	efiBootFile := filepath.Join("efi", "microsoft", "boot", "efisys.bin")
	if _, err := os.Stat(filepath.Join(stageDir, efiBootFile)); err != nil {
		return fmt.Errorf("EFI boot file not found in stage dir: %s", efiBootFile)
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
	diskSize := totalSize + 10*1024*1024
	if diskSize < 20*1024*1024 {
		diskSize = 20 * 1024 * 1024
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

	data, err := io.ReadAll(rdr)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	return data, nil
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
