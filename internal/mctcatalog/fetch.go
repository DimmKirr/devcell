package mctcatalog

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FetchConfig struct {
	CacheDir    string
	Language    string
	Edition     string
	LogFunc     func(format string, args ...any)
	OnProgress  func(filename string, bytesDownloaded, totalBytes int64)
}

func (c *FetchConfig) logf(format string, args ...any) {
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

// FetchWindowsISO downloads a self-contained Windows 11 ARM64 ESD from
// Microsoft's CDN via the MCT catalog and assembles it into a bootable ISO.
//
// MCT ESDs have a different image layout than UUP dump ESDs:
//
//	image 1: boot files          → extract to staging dir
//	image 2: WinPE               → export to boot.wim (image 1)
//	image 3: Windows Setup       → export to boot.wim (image 2, marked bootable)
//	image 4+: editions (Pro etc) → export to install.wim
func FetchWindowsISO(ctx context.Context, cfg FetchConfig) (string, error) {
	if cfg.Language == "" {
		cfg.Language = "en-us"
	}
	if cfg.Edition == "" {
		cfg.Edition = "Professional"
	}
	if cfg.CacheDir == "" {
		return "", fmt.Errorf("CacheDir is required")
	}

	isoPath := filepath.Join(cfg.CacheDir, fmt.Sprintf("windows-arm64-%s.iso",
		strings.ReplaceAll(strings.ToLower(cfg.Language), " ", "-")))

	if info, err := os.Stat(isoPath); err == nil && info.Size() > 0 {
		cfg.logf("ISO already exists: %s (%d bytes)", isoPath, info.Size())
		return isoPath, nil
	}

	client := NewClient(WithLogFunc(cfg.LogFunc))

	cfg.logf("searching MCT catalog for ARM64 ESD (lang=%s, edition=%s)", cfg.Language, cfg.Edition)
	esdEntry, err := client.FindARM64ESD(ctx, cfg.Language, cfg.Edition)
	if err != nil {
		return "", fmt.Errorf("finding ARM64 ESD in MCT catalog: %w", err)
	}

	downloadDir := filepath.Join(cfg.CacheDir, "mct-download")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return "", fmt.Errorf("creating download dir: %w", err)
	}

	esdPath := filepath.Join(downloadDir, esdEntry.FileName)

	if alreadyComplete(esdPath, esdEntry) {
		cfg.logf("ESD already downloaded: %s", esdPath)
	} else {
		cfg.logf("downloading MCT ESD: %s (%.1f MB) from Microsoft CDN",
			esdEntry.FileName, float64(esdEntry.Size)/(1024*1024))
		if err := downloadESD(ctx, esdEntry, esdPath, cfg.OnProgress); err != nil {
			return "", fmt.Errorf("downloading ESD: %w", err)
		}
		cfg.logf("download complete: %s", esdPath)
	}

	workDir := filepath.Join(cfg.CacheDir, "mct-work")

	cfg.logf("assembling ISO from MCT ESD")
	if err := AssembleMCTISO(ctx, esdPath, AssembleConfig{
		WorkDir: workDir,
		ISOPath: isoPath,
		Label:   fmt.Sprintf("W11_%s", strings.ToUpper(cfg.Language)),
		LogFunc: cfg.LogFunc,
	}); err != nil {
		return "", fmt.Errorf("assembling ISO: %w", err)
	}

	return isoPath, nil
}

func alreadyComplete(path string, entry *ESDFile) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if entry.Size > 0 && info.Size() != entry.Size {
		return false
	}
	if entry.SHA1 != "" {
		return verifySHA1(path, entry.SHA1) == nil
	}
	return entry.Size > 0 && info.Size() == entry.Size
}

func verifySHA1(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing %s: %w", path, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("SHA-1 mismatch: expected %s, got %s", expected, got)
	}
	return nil
}

func downloadESD(ctx context.Context, entry *ESDFile, dest string, progress func(string, int64, int64)) error {
	client := &http.Client{Timeout: 0}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.FilePath, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, entry.FilePath)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	total := entry.Size
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	var written int64
	buf := make([]byte, 64*1024)
	var lastReport time.Time
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return wErr
			}
			written += int64(n)
			if progress != nil && time.Since(lastReport) > 200*time.Millisecond {
				lastReport = time.Now()
				progress(entry.FileName, written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if progress != nil {
		progress(entry.FileName, written, total)
	}

	if entry.SHA1 != "" {
		if err := verifySHA1(dest, entry.SHA1); err != nil {
			os.Remove(dest)
			return err
		}
	}

	return nil
}
