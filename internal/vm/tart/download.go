package tart

import (
	"context"
	"fmt"
	"os"
	"time"
)

// DownloadFunc fetches a macOS restore image to the given path.
// The implementation may resume a partial download if the file already exists
// (e.g., vz uses HTTP Range headers based on existing file size).
type DownloadFunc func(ctx context.Context, path string) error

// DownloadIPSW downloads a restore image with completion tracking and retry.
// A ".done" marker file signals that a previous download completed successfully;
// if present, the download is skipped. Partial files are left on disk for the
// DownloadFunc to resume via Range headers.
// On failure, retries up to 3 times with backoff.
func DownloadIPSW(ctx context.Context, path string, download DownloadFunc) error {
	return DownloadIPSWObserved(ctx, path, download, NopObserver{})
}

// DownloadIPSWObserved is like DownloadIPSW but reports progress to obs.
func DownloadIPSWObserved(ctx context.Context, path string, download DownloadFunc, obs Observer) error {
	donePath := path + ".done"
	obs.Logf("checking for cached IPSW at %s", path)
	if _, err := os.Stat(donePath); err == nil {
		if fi, err := os.Stat(path); err == nil {
			obs.Logf("IPSW cache hit: %s (%d MB)", path, fi.Size()/(1024*1024))
		} else {
			obs.Logf("IPSW .done marker found but file missing — re-downloading")
			os.Remove(donePath)
			goto download
		}
		return nil
	}

	if fi, err := os.Stat(path); err == nil {
		obs.Logf("partial IPSW found (%d MB, no .done marker) — will resume", fi.Size()/(1024*1024))
	} else {
		obs.Logf("no cached IPSW found — downloading")
	}
download:

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		obs.Logf("download attempt %d/3", attempt)
		lastErr = download(ctx, path)
		if lastErr == nil {
			os.WriteFile(donePath, []byte("ok"), 0644)
			obs.Logf("download complete, wrote %s", donePath)
			return nil
		}
		obs.Logf("attempt %d failed: %v", attempt, lastErr)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	return fmt.Errorf("after 3 attempts: %w", lastErr)
}
