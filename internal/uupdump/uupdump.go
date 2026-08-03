package uupdump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FetchConfig struct {
	CacheDir    string
	Language    string
	Edition     string
	Concurrency int
	OnProgress  ProgressFunc
	LogFunc     func(format string, args ...any)
}

func (c *FetchConfig) logf(format string, args ...any) {
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

func FetchWindowsISO(ctx context.Context, cfg FetchConfig) (string, error) {
	if cfg.Language == "" {
		cfg.Language = "en-us"
	}
	if cfg.Edition == "" {
		cfg.Edition = "PROFESSIONAL"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
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

	client := NewClient()

	cfg.logf("searching for latest Windows 11 ARM64 build")
	build, err := client.FindLatestARM64(ctx)
	if err != nil {
		return "", fmt.Errorf("finding ARM64 build: %w", err)
	}
	cfg.logf("found build: %s (%s)", build.Title, build.Build)

	cfg.logf("fetching package for edition=%s lang=%s", cfg.Edition, cfg.Language)
	pkg, err := client.GetPackage(ctx, build.UUID, cfg.Language, []string{cfg.Edition})
	if err != nil {
		return "", fmt.Errorf("getting package: %w", err)
	}
	cfg.logf("package has %d files", len(pkg.Files))

	esdFiles := map[string]File{}
	var esdNames []string
	var totalESDSize int64
	for name, f := range pkg.Files {
		if strings.HasSuffix(strings.ToLower(name), ".esd") {
			esdFiles[name] = f
			esdNames = append(esdNames, name)
			totalESDSize += f.Size
		}
	}
	cfg.logf("ESD files in package: %d (total %.1f MB)", len(esdFiles), float64(totalESDSize)/(1024*1024))
	cfg.logf("ESD file list: %v", esdNames)
	// Say out loud what is being thrown away. AssembleISO only speaks ESD, so
	// dropping the rest is consistent — but for the current ARM64 build it is
	// 45 of 65 files and 4.2 GB, including the cumulative update and the FoD
	// payloads a later provisioning step will then fail to find (CELL-385).
	cfg.logf("%s", SummarizeSkipped(pkg.Files, ".esd"))

	installESD := findESD(pkg.Files, cfg.Edition, cfg.Language)
	if installESD == "" {
		return "", fmt.Errorf("no install ESD found in package (ESD files: %v)", esdNames)
	}
	cfg.logf("install ESD: %s", installESD)

	downloadDir := filepath.Join(cfg.CacheDir, "uupdump-download")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return "", fmt.Errorf("creating download dir: %w", err)
	}

	cfg.logf("downloading %d ESD files to %s", len(esdFiles), downloadDir)
	results, err := DownloadFiles(ctx, esdFiles, DownloadConfig{
		Dir:         downloadDir,
		Concurrency: cfg.Concurrency,
		OnProgress:  cfg.OnProgress,
	})
	if err != nil {
		return "", fmt.Errorf("downloading ESD files: %w", err)
	}

	var downloadedSize int64
	for _, r := range results {
		downloadedSize += r.Size
		cfg.logf("  %s: %.1f MB", r.Filename, float64(r.Size)/(1024*1024))
	}
	cfg.logf("all ESDs downloaded: %.1f MB total", float64(downloadedSize)/(1024*1024))

	esdPath := filepath.Join(downloadDir, installESD)
	if info, err := os.Stat(esdPath); err != nil {
		return "", fmt.Errorf("install ESD missing after download: %w", err)
	} else {
		cfg.logf("install ESD on disk: %s (%.1f MB)", esdPath, float64(info.Size())/(1024*1024))
	}

	workDir := filepath.Join(cfg.CacheDir, "uupdump-work")
	refGlob := filepath.Join(downloadDir, "*.esd")

	cfg.logf("assembling ISO from ESD (ref=%s)", refGlob)
	if err := AssembleISO(ctx, esdPath, AssembleConfig{
		WorkDir: workDir,
		ISOPath: isoPath,
		Label:   fmt.Sprintf("W11_%s", strings.ToUpper(cfg.Language)),
		RefESDs: []string{refGlob},
		LogFunc: cfg.LogFunc,
	}); err != nil {
		return "", fmt.Errorf("assembling ISO: %w", err)
	}

	return isoPath, nil
}

func findESD(files map[string]File, edition, language string) string {
	edLower := strings.ToLower(edition)
	langLower := strings.ToLower(language)

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && strings.Contains(lower, edLower) && strings.Contains(lower, langLower) {
			return name
		}
	}

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && strings.Contains(lower, edLower) {
			return name
		}
	}

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && !strings.Contains(lower, "metadata") {
			return name
		}
	}
	return ""
}
