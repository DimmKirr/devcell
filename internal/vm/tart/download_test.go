package tart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadIPSW_CachesWhenDoneMarkerExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")
	if err := os.WriteFile(path, []byte("cached-ipsw-data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".done", []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	called := false
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("download function should not be called when .done marker exists")
	}
}

func TestDownloadIPSW_DownloadsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")

	calls := 0
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		calls++
		return os.WriteFile(p, []byte("fresh-ipsw"), 0644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if _, err := os.Stat(path + ".done"); err != nil {
		t.Error("expected .done marker to be created after successful download")
	}
}

func TestDownloadIPSW_ResumesPartialWithoutDoneMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")
	if err := os.WriteFile(path, []byte("partial-data"), 0644); err != nil {
		t.Fatal(err)
	}

	called := false
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		called = true
		return os.WriteFile(p, []byte("complete-data"), 0644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("download should be called when file exists but .done marker is missing (partial download)")
	}
}

func TestDownloadIPSW_RetriesOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")

	calls := 0
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("catalog failed (attempt %d)", calls)
		}
		return os.WriteFile(p, []byte("third-time-charm"), 0644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDownloadIPSW_FailsAfterMaxRetries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")

	calls := 0
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		calls++
		return fmt.Errorf("always fails")
	})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
	if _, err := os.Stat(path + ".done"); err == nil {
		t.Error(".done marker should not exist after failed download")
	}
}

func TestDownloadIPSW_RedownloadsWhenDoneMarkerExistsButFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")
	// Only .done marker exists, IPSW file was deleted/corrupted away
	if err := os.WriteFile(path+".done", []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	called := false
	err := DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		called = true
		return os.WriteFile(p, []byte("re-downloaded-ipsw"), 0644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("download should be called when .done exists but IPSW file is missing")
	}
	if _, err := os.Stat(path + ".done"); err != nil {
		t.Error("new .done marker should exist after re-download")
	}
}

func TestDownloadIPSW_NoDoneMarkerOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.ipsw")

	_ = DownloadIPSW(context.Background(), path, func(ctx context.Context, p string) error {
		os.WriteFile(p, []byte("partial"), 0644)
		return fmt.Errorf("network error")
	})
	if _, err := os.Stat(path + ".done"); err == nil {
		t.Error(".done marker should not be created when download fails")
	}
}
