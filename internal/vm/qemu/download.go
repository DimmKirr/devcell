package qemu

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-winkit/mctcatalog"
	"github.com/devcell-sh/go-winkit/uupdump"
)

const (
	// VirtioDriversURL is the stable direct-download link for the latest VirtIO drivers ISO.
	VirtioDriversURL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso"

	// WindowsISODownloadURL is the Microsoft page for downloading Windows 11 ARM64 ISO.
	// Kept for the manual-download fallback message in ResolveWindowsISO.
	WindowsISODownloadURL = "https://www.microsoft.com/en-us/software-download/windows11arm64"

	// PwshVersion is the PowerShell 7 release shipped on the answer volume.
	PwshVersion = "7.6.5"
	// PwshReleaseURL is the direct GitHub download for the self-contained ARM64 zip.
	PwshReleaseURL = "https://github.com/PowerShell/PowerShell/releases/download/v" + PwshVersion + "/PowerShell-" + PwshVersion + "-win-arm64.zip"
	// PwshZipName is the cached zip filename.
	PwshZipName = "pwsh-arm64.zip"

	// AlpineVersion is the Alpine minirootfs release used as the WSL2
	// smoke-test distro — a ~4 MB tarball, the cheapest real Linux there is.
	AlpineVersion = "3.22.1"
	// AlpineRootfsURL is the direct CDN download for the aarch64 minirootfs.
	AlpineRootfsURL = "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/aarch64/alpine-minirootfs-" + AlpineVersion + "-aarch64.tar.gz"
	// AlpineRootfsName is the cached tarball filename.
	AlpineRootfsName = "alpine-minirootfs-" + AlpineVersion + "-aarch64.tar.gz"
)

// WindowsISOPath returns the path to the cached Windows ISO for a given language.
func WindowsISOPath(home, language string) string {
	safe := strings.ReplaceAll(strings.ToLower(language), " ", "-")
	return filepath.Join(CacheDir(home), fmt.Sprintf("windows-arm64-%s.iso", safe))
}

// DownloadWindowsISO fetches and caches the Windows 11 ARM64 ISO via UUP dump.
// Downloads an ESD from Microsoft's CDN and assembles it into a bootable ISO
// using wimlib-imagex and mkisofs (must be on PATH).
func DownloadWindowsISO(ctx context.Context, home, language string, noCache bool, obs Observer) (string, error) {
	if language == "" {
		language = "en-us"
	}

	cacheDir := CacheDir(home)
	dest := WindowsISOPath(home, language)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		obs.Logf("--no-cache: removing Windows ISO download marker")
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			// A cached image that firmware cannot boot (e.g. pure UDF with no
			// El Torito, what hdiutil used to master) would burn a 20–40 min
			// install cycle before failing at the EFI shell. Re-master instead.
			if err := WindowsISOBootable(dest); err != nil {
				obs.Logf("cached Windows ISO is unusable (%v) — re-mastering", err)
				os.Remove(dest)
				os.Remove(dest + ".done")
			} else {
				obs.Logf("Windows ISO cache hit: %s", dest)
				return dest, nil
			}
		} else {
			obs.Logf("Windows ISO .done marker found but file missing — re-downloading")
			os.Remove(dest + ".done")
		}
	}

	var dlStart time.Time
	var lastLogPct float64
	progressCb := func(filename string, downloaded, total int64) {
		if total <= 0 {
			return
		}
		if dlStart.IsZero() {
			dlStart = time.Now()
		}
		pct := float64(downloaded) / float64(total) * 100
		dlMB := float64(downloaded) / (1024 * 1024)
		totalMB := float64(total) / (1024 * 1024)

		spinnerMsg := fmt.Sprintf("%.0f MB / %.0f MB (%.1f%%)", dlMB, totalMB, pct)
		obs.Progress(float64(downloaded)/float64(total), spinnerMsg)

		if pct-lastLogPct >= 5 || pct >= 100 {
			lastLogPct = pct
			elapsed := time.Since(dlStart)
			logMsg := spinnerMsg
			if downloaded > 0 && elapsed > time.Second {
				speed := float64(downloaded) / elapsed.Seconds() / (1024 * 1024)
				remaining := time.Duration(float64(total-downloaded) / float64(downloaded) * float64(elapsed))
				logMsg = fmt.Sprintf("%.0f MB / %.0f MB (%.1f%%) — %s left @ %.0f MB/s",
					dlMB, totalMB, pct, remaining.Round(time.Second), speed)
			}
			obs.Logf("download: %s", logMsg)
		}
	}

	// Try MCT catalog first (self-contained ESD from Microsoft CDN — always works for ARM64).
	obs.Logf("trying MCT catalog path (self-contained ESD)")
	isoPath, err := mctcatalog.FetchWindowsISO(ctx, mctcatalog.FetchConfig{
		CacheDir:   cacheDir,
		Language:   language,
		Edition:    "Professional",
		LogFunc:    obs.Logf,
		OnProgress: progressCb,
	})
	if err != nil {
		obs.Logf("MCT catalog failed: %v — falling back to UUP dump", err)
		isoPath, err = uupdump.FetchWindowsISO(ctx, uupdump.FetchConfig{
			CacheDir:    cacheDir,
			Language:    language,
			Edition:     "PROFESSIONAL",
			Concurrency: 5,
			LogFunc:     obs.Logf,
			OnProgress:  progressCb,
		})
		if err != nil {
			return "", fmt.Errorf("downloading Windows ISO (MCT + UUP dump both failed): %w", err)
		}
	}

	os.WriteFile(isoPath+".done", []byte("ok"), 0644)
	obs.Logf("download complete, wrote %s.done", isoPath)
	return isoPath, nil
}

// ISOMetadata holds parsed information from an ISO filename.
type ISOMetadata struct {
	Version string // e.g. "24H2"
	Arch    string // e.g. "Arm64", "x64"
}

var isoFilenameRe = regexp.MustCompile(`Win\d+_(\w+)_\w+_(\w+)\.iso`)

// ParseISOFilename extracts version and architecture from a Windows ISO filename.
func ParseISOFilename(name string) ISOMetadata {
	m := isoFilenameRe.FindStringSubmatch(name)
	if len(m) < 3 {
		return ISOMetadata{}
	}
	return ISOMetadata{Version: m[1], Arch: m[2]}
}

// CacheDir returns the QEMU media cache directory.
//
// DEVCELL_QEMU_CACHE_DIR points it somewhere shared. Inside a cell $HOME is
// itself a per-cell directory, so the default renders as
// ~/.devcell/<cell>/.devcell/cache/qemu and every cell re-downloads the same
// ~6 GB of immutable media. There is no way to reach the real host home from
// inside the container, so the location has to be pointable rather than
// inferred (CELL-386).
func CacheDir(home string) string {
	if dir := os.Getenv("DEVCELL_QEMU_CACHE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".devcell", "cache", "qemu")
}

// VirtioISOPath returns the path to the cached VirtIO drivers ISO.
func VirtioISOPath(home string) string {
	return filepath.Join(CacheDir(home), "virtio-win.iso")
}

// DownloadVirtioDrivers downloads the VirtIO drivers ISO if not already cached.
// Uses .done marker pattern (mirrors tart.DownloadIPSW).
// When noCache is true, removes the .done marker to force re-download.
func DownloadVirtioDrivers(ctx context.Context, home string, noCache bool, obs Observer) (string, error) {
	dest := VirtioISOPath(home)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		obs.Logf("--no-cache: removing VirtIO download marker")
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			obs.Logf("VirtIO drivers cache hit: %s", dest)
			return dest, nil
		}
		obs.Logf("VirtIO .done marker found but file missing — re-downloading")
		os.Remove(dest + ".done")
	}

	obs.Logf("downloading VirtIO drivers from %s", VirtioDriversURL)
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		obs.Logf("download attempt %d/3", attempt)
		lastErr = downloadFile(ctx, VirtioDriversURL, dest, obs)
		if lastErr == nil {
			os.WriteFile(dest+".done", []byte("ok"), 0644)
			obs.Logf("download complete, wrote %s.done", dest)
			return dest, nil
		}
		obs.Logf("attempt %d failed: %v", attempt, lastErr)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	return "", fmt.Errorf("downloading VirtIO drivers after 3 attempts: %w", lastErr)
}

// hasDownloadMarker checks if a .done marker exists for the given file path.
func hasDownloadMarker(path string) bool {
	_, err := os.Stat(path + ".done")
	return err == nil
}

// downloadFile fetches url to dest with progress reporting.
func downloadFile(ctx context.Context, url, dest string, obs Observer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Download to a sibling temp file and rename into place. Writing to dest
	// directly writes *through* any hard link sharing that inode — which
	// truncated the host's real 789MB virtio-win.iso to a 300MB stub when a
	// test seeded its cache by linking (CELL-386). Rename replaces the
	// directory entry instead, and has the second benefit that a killed
	// download leaves no half-file that looks complete.
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once renamed
	}()
	f := tmp

	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return wErr
			}
			written += int64(n)
			if resp.ContentLength > 0 {
				obs.Progress(float64(written)/float64(resp.ContentLength),
					fmt.Sprintf("%.0f MB / %.0f MB", float64(written)/(1024*1024), float64(resp.ContentLength)/(1024*1024)))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// ResolveWindowsISO resolves the Windows ARM64 ISO path.
// Priority: env DEVCELL_QEMU_WINDOWS_ISO > config path > cached download > error.
func ResolveWindowsISO(envISO, configISO, home string) (string, error) {
	path := envISO
	if path == "" {
		path = configISO
	}
	if path == "" && home != "" {
		cached := WindowsISOPath(home, "en-us")
		if hasDownloadMarker(cached) {
			if _, err := os.Stat(cached); err == nil {
				path = cached
			}
		}
	}
	if path == "" {
		return "", fmt.Errorf("Windows ARM64 ISO not configured.\n\n"+
			"Run: cell init --engine=qemu  (downloads automatically)\n"+
			"Or download from: %s\n"+
			"Then set: export DEVCELL_QEMU_WINDOWS_ISO=/path/to/Win11_ARM64.iso\n"+
			"Or add to .devcell.toml:\n"+
			"  [cell]\n"+
			"  qemu_windows_iso = \"/path/to/Win11_ARM64.iso\"", WindowsISODownloadURL)
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("Windows ISO not found at %s: %w", path, err)
	}
	if err := ValidateISO(path); err != nil {
		return "", fmt.Errorf("invalid ISO at %s: %w", path, err)
	}
	return path, nil
}

// ValidateISO checks that a file carries a recognised disc format by reading
// the volume descriptor at sector 16 (offset 0x8001). Both ISO 9660 (CD001)
// and UDF (BEA01/NSR02/NSR03) are accepted — Windows ARM64 ISOs built by UUP
// dump are pure UDF.
func ValidateISO(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	magic := make([]byte, 5)
	if _, err := f.ReadAt(magic, 0x8001); err != nil {
		return fmt.Errorf("cannot read ISO magic bytes: %w", err)
	}
	switch string(magic) {
	case "CD001", "BEA01", "NSR02", "NSR03":
		return nil
	default:
		return fmt.Errorf("not a recognised disc image (expected CD001 or UDF descriptor at offset 0x8001, got %q)", magic)
	}
}

// RemoveDownloadMarkers removes .done markers for all cached ISOs,
// forcing re-download on next use. Used by --no-cache.
func RemoveDownloadMarkers(home string) {
	cacheDir := CacheDir(home)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".done") {
			os.Remove(filepath.Join(cacheDir, e.Name()))
		}
	}
}

// OpenSSHPayloadPath returns the cached Win32-OpenSSH release path.
func OpenSSHPayloadPath(home string) string {
	return filepath.Join(CacheDir(home), unattend.OpenSSHPayloadName)
}

// DownloadOpenSSH fetches Microsoft's signed Win32-OpenSSH ARM64 release.
//
// The guest cannot install OpenSSH Server through Windows servicing: our media
// carries the capability manifest but not its payload, so the capability sits
// Staged and the install fails 0x80070002 — with Windows Update reachable and
// permitted. The Server FoD ships on a separate build-matched ISO, and the UUP
// package has no Server package at all. This release needs no servicing.
func DownloadOpenSSH(ctx context.Context, home string, noCache bool, obs Observer) (string, error) {
	dest := OpenSSHPayloadPath(home)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		obs.Logf("--no-cache: removing OpenSSH download marker")
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			obs.Logf("OpenSSH payload cache hit: %s", dest)
			return dest, nil
		}
		obs.Logf("OpenSSH .done marker found but file missing — re-downloading")
		os.Remove(dest + ".done")
	}

	obs.Logf("downloading OpenSSH from %s", unattend.OpenSSHReleaseURL)
	if err := downloadFile(ctx, unattend.OpenSSHReleaseURL, dest, obs); err != nil {
		return "", fmt.Errorf("downloading OpenSSH release: %w", err)
	}
	if err := os.WriteFile(dest+".done", nil, 0644); err != nil {
		return "", fmt.Errorf("writing download marker: %w", err)
	}
	return dest, nil
}

// PwshZipPath returns the cached PowerShell 7 zip path.
func PwshZipPath(home string) string {
	return filepath.Join(CacheDir(home), PwshZipName)
}

// AlpineRootfsPath returns the cached Alpine minirootfs tarball path.
func AlpineRootfsPath(home string) string {
	return filepath.Join(CacheDir(home), AlpineRootfsName)
}

// DownloadAlpineRootfs fetches the Alpine aarch64 minirootfs if not cached.
func DownloadAlpineRootfs(ctx context.Context, home string, noCache bool, obs Observer) (string, error) {
	dest := AlpineRootfsPath(home)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		obs.Logf("--no-cache: removing alpine download marker")
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			obs.Logf("alpine cache hit: %s", dest)
			return dest, nil
		}
		obs.Logf("alpine .done marker found but file missing — re-downloading")
		os.Remove(dest + ".done")
	}

	obs.Logf("downloading Alpine minirootfs %s from %s", AlpineVersion, AlpineRootfsURL)
	if err := downloadFile(ctx, AlpineRootfsURL, dest, obs); err != nil {
		return "", fmt.Errorf("downloading Alpine minirootfs: %w", err)
	}
	if err := os.WriteFile(dest+".done", nil, 0644); err != nil {
		return "", fmt.Errorf("writing download marker: %w", err)
	}
	return dest, nil
}

// DownloadPwsh fetches the PowerShell 7 ARM64 self-contained zip if not cached.
func DownloadPwsh(ctx context.Context, home string, noCache bool, obs Observer) (string, error) {
	dest := PwshZipPath(home)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		obs.Logf("--no-cache: removing pwsh download marker")
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			obs.Logf("pwsh cache hit: %s", dest)
			return dest, nil
		}
		obs.Logf("pwsh .done marker found but file missing — re-downloading")
		os.Remove(dest + ".done")
	}

	obs.Logf("downloading PowerShell %s from %s", PwshVersion, PwshReleaseURL)
	if err := downloadFile(ctx, PwshReleaseURL, dest, obs); err != nil {
		return "", fmt.Errorf("downloading PowerShell release: %w", err)
	}
	if err := os.WriteFile(dest+".done", nil, 0644); err != nil {
		return "", fmt.Errorf("writing download marker: %w", err)
	}
	return dest, nil
}

// ExtractPwshFiles reads a PowerShell zip and returns its contents as a map
// keyed by answer-volume paths (e.g. "/pwsh/pwsh.exe"). The winpe.PwshVolDir
// prefix is prepended automatically.
func ExtractPwshFiles(zipPath string) (map[string][]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening pwsh zip: %w", err)
	}
	defer r.Close()

	files := make(map[string][]byte)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s in zip: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s from zip: %w", f.Name, err)
		}
		volPath := "/" + winpe.PwshVolDir + "/" + f.Name
		files[volPath] = data
	}
	return files, nil
}
