package qemu

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/DimmKirr/devcell/internal/isokit"
)

type ISOInfo struct {
	Size       int64
	Format     string // "udf", "iso9660", or "unknown"
	HasBootEFI bool   // "BOOTAA64.EFI" found in raw metadata
}

// ISOPreflight validates a Windows ISO without parsing its filesystem.
// It checks: file exists, plausible size, disc format signature, and
// whether BOOTAA64.EFI appears in the raw directory metadata.
// minSize is the minimum expected file size (use 0 to skip the size check).
func ISOPreflight(isoPath string) (*ISOInfo, error) {
	return isoPreflight(isoPath, 1<<30)
}

func isoPreflight(isoPath string, minSize int64) (*ISOInfo, error) {
	f, err := os.Open(isoPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open ISO: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot stat ISO: %w", err)
	}

	info := &ISOInfo{Size: stat.Size()}

	if minSize > 0 && stat.Size() < minSize {
		return info, fmt.Errorf("ISO is too small (%d bytes) — expected a Windows installer (>%d bytes)", stat.Size(), minSize)
	}

	info.Format = detectISOFormat(f)

	info.HasBootEFI = scanForBootEFI(f, stat.Size())
	if !info.HasBootEFI {
		return info, fmt.Errorf("BOOTAA64.EFI not found in ISO metadata — this may not be an ARM64 Windows installer")
	}

	return info, nil
}

// detectISOFormat walks the volume recognition area that starts at sector 16.
// ISO 9660 discs carry CD001 descriptors there; UDF discs carry the ECMA-167
// recognition sequence (BEA01, NSR02/NSR03, TEA01), which on bridge discs
// follows the CD001 set. A disc with an ISO 9660 descriptor is reported as
// iso9660 — our readers parse that — and a pure-UDF disc as udf.
func detectISOFormat(f *os.File) string {
	buf := make([]byte, 6)
	hasISO, hasUDF := false, false
scan:
	for sector := int64(16); sector < 48; sector++ {
		if _, err := f.ReadAt(buf, sector*2048); err != nil {
			break
		}
		switch string(buf[1:6]) {
		case "CD001":
			hasISO = true
		case "NSR02", "NSR03":
			hasUDF = true
		case "BEA01", "TEA01", "BOOT2":
			// recognition-sequence members with no format verdict of their own
		default:
			break scan
		}
	}

	switch {
	case hasISO:
		return "iso9660"
	case hasUDF:
		return "udf"
	default:
		return "unknown"
	}
}

// scanForBootEFI scans the first 1 MB and last 1 MB of the ISO for the
// string "BOOTAA64.EFI" in the directory metadata. UDF and ISO 9660 both
// store directory entries near the beginning of the disc.
func scanForBootEFI(f *os.File, size int64) bool {
	needle := []byte("BOOTAA64.EFI")

	scan := func(offset int64, length int64) bool {
		if offset < 0 {
			offset = 0
		}
		if offset+length > size {
			length = size - offset
		}
		buf := make([]byte, length)
		n, _ := f.ReadAt(buf, offset)
		return bytes.Contains(buf[:n], needle)
	}

	// Directory metadata is typically in the first few MB
	if scan(0, 4*1024*1024) {
		return true
	}

	return false
}

// WindowsISOBootable reports whether firmware can boot an installer ISO:
// it must carry an El Torito boot catalog with a bootable EFI (0xEF) entry.
// A cached image failing this (e.g. the pure-UDF images hdiutil used to
// master) must be re-mastered, not reused — run 20260812T081924 burned a
// build cycle to find that out at the EFI shell.
func WindowsISOBootable(isoPath string) error {
	return isokit.RequireEFIBootable(isoPath)
}

// InstallerBootloader returns the installer's BOOTAA64.EFI: extracted from
// the ISO when a reader can parse it, else from the sidecar file that
// mastering writes next to the ISO. Pure-UDF media (macOS hdiutil output)
// defeats every Go-native reader, and without this bootloader the answer
// volume gives startup.nsh nothing to chainload — the QEMU v11+ HVF boot
// path dies at the EFI shell (run 20260812T081924).
func InstallerBootloader(isoPath string) ([]byte, error) {
	data, isoErr := isokit.ReadFileFromISO(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
	if isoErr == nil {
		return data, nil
	}
	data, sidecarErr := os.ReadFile(isokit.BootloaderSidecarPath(isoPath))
	if sidecarErr == nil {
		return data, nil
	}
	return nil, fmt.Errorf("no bootloader source: ISO readers: %v; sidecar: %v", isoErr, sidecarErr)
}

// vioscsiISODirs are the virtio-win.iso directories probed for the ARM64
// vioscsi driver, in preference order. virtio-win names its per-OS
// directories inconsistently across releases: 0.1.2xx ships vioscsi ARM64
// under 2k25 while NetKVM uses w11.
var vioscsiISODirs = []string{
	"vioscsi/w11/ARM64",
	"vioscsi/2k25/ARM64",
	"vioscsi/2k22/ARM64",
}

// winPEDriverDir is where WinPE storage drivers land on the answer volume.
// The answer file's PnpCustomizationsWinPE/DriverPaths points Setup at this
// directory via %configsetroot% — the volume the answer file was read from,
// so the path always resolves. Rejected alternatives, each disproven by a
// run: RunSynchronous drvload aborts Setup (0x8007000D, 20260812T132820);
// $WinPEDriver$ never loads on the media boot path (20260812T144140);
// DriverPaths at the virtio CD aborts because the CD is exactly what is
// invisible (0x80070001, 20260729T172019).
const winPEDriverDir = "/drivers/vioscsi/"

// LoadWinPEStorageDrivers extracts the ARM64 vioscsi driver from the
// virtio-win ISO, keyed by answer-volume path for
// AutounattendConfig.AnswerDrivers. ARM64 WinPE has no inbox vioscsi, so
// without this on the answer volume the virtio-scsi installer CD is
// invisible to Setup and the install stalls at "a media driver your
// computer needs is missing" (CELL-429).
func LoadWinPEStorageDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range vioscsiISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/vioscsi.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{winPEDriverDir + "vioscsi.inf": inf}
		for _, name := range []string{"vioscsi.sys", "vioscsi.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers[winPEDriverDir+name] = data
		}
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 vioscsi driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

// vioserialISODirs are the virtio-win.iso directories probed for the ARM64
// vioserial driver, in preference order.
var vioserialISODirs = []string{
	"vioserial/w11/ARM64",
	"vioserial/2k25/ARM64",
	"vioserial/2k22/ARM64",
}

const winPEVioserialDir = "/drivers/vioserial/"

// LoadWinPEVioserialDrivers extracts the ARM64 vioserial (virtio-serial)
// driver from the virtio-win ISO. The guest needs this driver loaded via
// drvload so it can write progress to \\.\Global\<ProgressPortName>.
func LoadWinPEVioserialDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range vioserialISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/vioser.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{winPEVioserialDir + "vioser.inf": inf}
		for _, name := range []string{"vioser.sys", "vioser.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers[winPEVioserialDir+name] = data
		}
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 vioserial driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

// viofsISODirs are the virtio-win.iso directories probed for the ARM64 viofs
// driver, in preference order.
var viofsISODirs = []string{
	"viofs/w11/ARM64",
	"viofs/2k25/ARM64",
	"viofs/2k22/ARM64",
}

// LoadWinPEViofsDrivers extracts the ARM64 viofs (virtio-fs) driver and the
// virtiofs.exe mount helper from the virtio-win ISO, keyed by answer-volume
// path. The returned map also includes virtiofs.exe which the guest uses to
// mount the shared directory after drvload loads the PnP driver.
func LoadWinPEViofsDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range viofsISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/viofs.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{"/drivers/viofs/viofs.inf": inf}
		for _, name := range []string{"viofs.sys", "viofs.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers["/drivers/viofs/"+name] = data
		}
		exe, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/virtiofs.exe")
		if err != nil {
			return nil, fmt.Errorf("reading %s/virtiofs.exe: %w", dir, err)
		}
		drivers["/drivers/viofs/virtiofs.exe"] = exe
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 viofs driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

// BootloaderInfo describes the EFI bootloader extracted from a Windows ISO.
type BootloaderInfo struct {
	Arch string // "aarch64", "x86_64", or "unknown"
	Size int
}

// ValidateBootloaderPE checks that raw EFI bootloader bytes are a valid
// aarch64 PE binary.
func ValidateBootloaderPE(data []byte) (*BootloaderInfo, error) {
	if len(data) < 0x86 {
		return nil, fmt.Errorf("BOOTAA64.EFI is too small to be a valid PE binary (%d bytes)", len(data))
	}

	if data[0] != 'M' || data[1] != 'Z' {
		return nil, fmt.Errorf("BOOTAA64.EFI is not a PE binary (missing MZ magic, got %02x%02x)", data[0], data[1])
	}

	peOffset := binary.LittleEndian.Uint32(data[0x3C:])
	if int(peOffset)+6 > len(data) {
		return nil, fmt.Errorf("BOOTAA64.EFI has invalid PE header offset (%d, file is %d bytes)", peOffset, len(data))
	}

	if data[peOffset] != 'P' || data[peOffset+1] != 'E' || data[peOffset+2] != 0 || data[peOffset+3] != 0 {
		return nil, fmt.Errorf("BOOTAA64.EFI has invalid PE signature at offset %d", peOffset)
	}

	machine := binary.LittleEndian.Uint16(data[peOffset+4:])
	arch := peMachineArch(machine)

	if arch != "aarch64" {
		return nil, fmt.Errorf("BOOTAA64.EFI has wrong architecture: expected aarch64, got %s (PE machine 0x%04X)", arch, machine)
	}

	return &BootloaderInfo{Arch: arch, Size: len(data)}, nil
}

func peMachineArch(machine uint16) string {
	switch machine {
	case 0xAA64:
		return "aarch64"
	case 0x8664:
		return "x86_64"
	case 0x014C:
		return "i386"
	default:
		return fmt.Sprintf("unknown(0x%04X)", machine)
	}
}
