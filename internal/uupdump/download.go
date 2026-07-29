package uupdump

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
	"sync"
	"sync/atomic"
	"time"
)

type ProgressFunc func(filename string, bytesDownloaded, totalBytes int64)

type DownloadConfig struct {
	Dir         string
	Concurrency int
	OnProgress  ProgressFunc
	HTTPClient  *http.Client
}

type DownloadResult struct {
	Filename string
	Path     string
	Size     int64
	Err      error
}

func DownloadFiles(ctx context.Context, files map[string]File, cfg DownloadConfig) ([]DownloadResult, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 0}
	}

	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating download dir: %w", err)
	}

	type work struct {
		name string
		file File
	}

	var items []work
	for name, f := range files {
		items = append(items, work{name: name, file: f})
	}

	var totalExpected int64
	for _, f := range files {
		totalExpected += f.Size
	}

	var aggregateDownloaded atomic.Int64
	progress := cfg.OnProgress
	if progress != nil && len(items) > 1 && totalExpected > 0 {
		progress = func(_ string, _, _ int64) {}
	}

	results := make([]DownloadResult, len(items))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		go func(idx int, w work) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			var perFileProgress ProgressFunc
			if cfg.OnProgress != nil && len(items) > 1 && totalExpected > 0 {
				var lastPerFile int64
				var lastReport time.Time
				perFileProgress = func(filename string, downloaded, total int64) {
					delta := downloaded - lastPerFile
					lastPerFile = downloaded
					cur := aggregateDownloaded.Add(delta)
					if time.Since(lastReport) > 200*time.Millisecond || downloaded == total {
						lastReport = time.Now()
						cfg.OnProgress(filename, cur, totalExpected)
					}
				}
			} else {
				perFileProgress = cfg.OnProgress
			}

			dest := filepath.Join(cfg.Dir, w.name)
			size, err := downloadOne(ctx, cfg.HTTPClient, w.file, dest, w.name, perFileProgress)
			results[idx] = DownloadResult{
				Filename: w.name,
				Path:     dest,
				Size:     size,
				Err:      err,
			}
		}(i, item)
	}

	wg.Wait()

	for _, r := range results {
		if r.Err != nil {
			return results, fmt.Errorf("download %s: %w", r.Filename, r.Err)
		}
	}
	return results, nil
}

func downloadOne(ctx context.Context, client *http.Client, f File, dest, name string, progress ProgressFunc) (int64, error) {
	if done, err := alreadyComplete(dest, f); err != nil {
		return 0, err
	} else if done {
		info, _ := os.Stat(dest)
		if progress != nil {
			progress(name, info.Size(), info.Size())
		}
		return info.Size(), nil
	}

	var resumeFrom int64
	if info, err := os.Stat(dest); err == nil {
		resumeFrom = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return 0, err
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resumeFrom = 0
		resp.Body.Close()
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		resp, err = client.Do(req2)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("HTTP %d for %s", resp.StatusCode, name)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusOK {
		flags |= os.O_TRUNC
		resumeFrom = 0
	} else {
		flags |= os.O_APPEND
	}

	out, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	var written atomic.Int64
	written.Store(resumeFrom)

	totalSize := f.Size
	if totalSize <= 0 && resp.ContentLength > 0 {
		totalSize = resp.ContentLength + resumeFrom
	}

	var lastReport time.Time
	pr := &progressReader{
		r: resp.Body,
		onRead: func(n int) {
			cur := written.Add(int64(n))
			if progress != nil && time.Since(lastReport) > 200*time.Millisecond {
				lastReport = time.Now()
				progress(name, cur, totalSize)
			}
		},
	}

	n, err := io.Copy(out, pr)
	if err != nil {
		return resumeFrom + n, err
	}

	if progress != nil {
		progress(name, resumeFrom+n, totalSize)
	}

	if f.SHA1 != "" {
		if err := verifySHA1(dest, f.SHA1); err != nil {
			os.Remove(dest)
			return 0, err
		}
	}

	return resumeFrom + n, nil
}

func alreadyComplete(path string, f File) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, nil
	}
	if f.Size > 0 && info.Size() != f.Size {
		return false, nil
	}
	if f.SHA1 != "" {
		return verifySHA1(path, f.SHA1) == nil, nil
	}
	return f.Size > 0 && info.Size() == f.Size, nil
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
		return fmt.Errorf("SHA-1 mismatch for %s: expected %s, got %s", filepath.Base(path), expected, got)
	}
	return nil
}

type progressReader struct {
	r      io.Reader
	onRead func(n int)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.onRead(n)
	}
	return n, err
}
