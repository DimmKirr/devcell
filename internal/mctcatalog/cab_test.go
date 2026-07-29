package mctcatalog

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"testing"
)

func TestExtractCABWithFallback_LZX(t *testing.T) {
	if _, err := exec.LookPath("cabextract"); err != nil {
		t.Skip("cabextract not on PATH")
	}
	if testing.Short() {
		t.Skip("short: skipping network test")
	}

	// Download the actual MCT catalog CAB (LZX compressed, ~29KB).
	resp, err := http.Get(Win11CatalogURL)
	if err != nil {
		t.Fatalf("downloading catalog CAB: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d from %s", resp.StatusCode, Win11CatalogURL)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading CAB: %v", err)
	}
	if len(data) < 100 {
		t.Fatalf("CAB too small: %d bytes", len(data))
	}

	logf := func(format string, args ...any) { t.Logf(format, args...) }
	files, err := extractCABWithFallback(data, logf)
	if err != nil {
		t.Fatalf("extractCABWithFallback: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("no files extracted from CAB")
	}

	var foundProducts bool
	for name := range files {
		t.Logf("extracted: %s (%d bytes)", name, len(files[name]))
		if len(name) >= 8 && name[len(name)-4:] == ".xml" {
			foundProducts = true
		}
	}
	if !foundProducts {
		t.Error("no .xml file found in extracted CAB")
	}
}

func TestFindARM64ESD(t *testing.T) {
	if _, err := exec.LookPath("cabextract"); err != nil {
		t.Skip("cabextract not on PATH")
	}
	if testing.Short() {
		t.Skip("short: skipping network test")
	}

	client := NewClient(WithLogFunc(func(format string, args ...any) { t.Logf(format, args...) }))
	esd, err := client.FindARM64ESD(context.Background(), "en-us", "Professional")
	if err != nil {
		t.Fatalf("FindARM64ESD: %v", err)
	}

	t.Logf("found: %s (arch=%s, lang=%s, edition=%s, size=%d)", esd.FileName, esd.Architecture, esd.LanguageCode, esd.Edition, esd.Size)
	if esd.FilePath == "" {
		t.Error("ESD FilePath (CDN URL) is empty")
	}
	if esd.Size <= 0 {
		t.Error("ESD Size is zero or negative")
	}
}
