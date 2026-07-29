package uupdump

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha1sum(data []byte) string {
	h := sha1.Sum(data)
	return hex.EncodeToString(h[:])
}

func TestDownloadFiles_BasicDownload(t *testing.T) {
	content := []byte("hello windows esd content")
	hash := sha1sum(content)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	files := map[string]File{
		"test.esd": {
			SHA1: hash,
			Size: int64(len(content)),
			URL:  srv.URL + "/test.esd",
		},
	}

	results, err := DownloadFiles(context.Background(), files, DownloadConfig{
		Dir:         dir,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	got, err := os.ReadFile(filepath.Join(dir, "test.esd"))
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch")
	}
}

func TestDownloadFiles_SHA1Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("corrupted data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	files := map[string]File{
		"bad.esd": {
			SHA1: "0000000000000000000000000000000000000000",
			Size: 14,
			URL:  srv.URL + "/bad.esd",
		},
	}

	_, err := DownloadFiles(context.Background(), files, DownloadConfig{
		Dir:         dir,
		Concurrency: 1,
	})
	if err == nil {
		t.Fatal("expected SHA-1 mismatch error")
	}

	if _, statErr := os.Stat(filepath.Join(dir, "bad.esd")); !os.IsNotExist(statErr) {
		t.Error("expected corrupted file to be removed")
	}
}

func TestDownloadFiles_SkipComplete(t *testing.T) {
	content := []byte("already downloaded")
	hash := sha1sum(content)

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "done.esd")
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]File{
		"done.esd": {
			SHA1: hash,
			Size: int64(len(content)),
			URL:  srv.URL + "/done.esd",
		},
	}

	results, err := DownloadFiles(context.Background(), files, DownloadConfig{
		Dir:         dir,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}
	if requestCount != 0 {
		t.Errorf("expected 0 HTTP requests (file complete), got %d", requestCount)
	}
	if results[0].Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), results[0].Size)
	}
}

func TestDownloadFiles_ProgressCallback(t *testing.T) {
	content := []byte("progress test data")
	hash := sha1sum(content)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	files := map[string]File{
		"prog.esd": {
			SHA1: hash,
			Size: int64(len(content)),
			URL:  srv.URL + "/prog.esd",
		},
	}

	var gotProgress bool
	results, err := DownloadFiles(context.Background(), files, DownloadConfig{
		Dir:         dir,
		Concurrency: 1,
		OnProgress: func(filename string, downloaded, total int64) {
			gotProgress = true
		},
	})
	if err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}
	if !gotProgress {
		t.Error("progress callback was never called")
	}
	_ = results
}

func TestDownloadFiles_Resume(t *testing.T) {
	fullContent := []byte("AAABBBCCC")
	hash := sha1sum(fullContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "bytes=3-" {
			w.Header().Set("Content-Range", "bytes 3-8/9")
			w.WriteHeader(http.StatusPartialContent)
			w.Write(fullContent[3:])
		} else {
			w.Write(fullContent)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "resume.esd")
	if err := os.WriteFile(dest, fullContent[:3], 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]File{
		"resume.esd": {
			SHA1: hash,
			Size: int64(len(fullContent)),
			URL:  srv.URL + "/resume.esd",
		},
	}

	_, err := DownloadFiles(context.Background(), files, DownloadConfig{
		Dir:         dir,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fullContent) {
		t.Errorf("expected %q, got %q", fullContent, got)
	}
}
