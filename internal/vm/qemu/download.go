package qemu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/mctcatalog"
	"github.com/DimmKirr/devcell/internal/uupdump"
)

const (
	// VirtioDriversURL is the stable direct-download link for the latest VirtIO drivers ISO.
	VirtioDriversURL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso"

	// WindowsISODownloadURL is the Microsoft page for downloading Windows 11 ARM64 ISO.
	// Kept for the manual-download fallback message in ResolveWindowsISO.
	WindowsISODownloadURL = "https://www.microsoft.com/en-us/software-download/windows11arm64"
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
			obs.Logf("Windows ISO cache hit: %s", dest)
			return dest, nil
		}
		obs.Logf("Windows ISO .done marker found but file missing — re-downloading")
		os.Remove(dest + ".done")
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

// CacheDir returns the shared QEMU cache directory.
func CacheDir(home string) string {
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

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

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
	return nil
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

// ValidateISO checks that a file is a valid ISO 9660 image by reading the
// magic bytes "CD001" at offset 0x8001.
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
	if string(magic) != "CD001" {
		return fmt.Errorf("not an ISO 9660 image (expected CD001 at offset 0x8001, got %q)", magic)
	}
	return nil
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
