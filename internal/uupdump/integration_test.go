package uupdump

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/DimmKirr/devcell/internal/wimlib"
)

func TestFetchWindowsISO_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("long: downloads ~4 GB ESD from Microsoft CDN and assembles ISO; run without -short")
	}
	if !wimlib.Available() {
		t.Skip("wimlib not available: build with -tags wimlib and wimlib installed (brew install wimlib)")
	}

	cacheDir := os.Getenv("DEVCELL_TEST_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = t.TempDir()
	}
	t.Logf("cache dir: %s", cacheDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	isoPath, err := FetchWindowsISO(ctx, FetchConfig{
		CacheDir: cacheDir,
		Language: "en-us",
		Edition:  "PROFESSIONAL",
		LogFunc:  t.Logf,
		OnProgress: func(filename string, downloaded, total int64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				t.Logf("%s: %.0f MB / %.0f MB (%.1f%%)",
					filename,
					float64(downloaded)/(1024*1024),
					float64(total)/(1024*1024),
					pct)
			}
		},
	})
	if err != nil {
		t.Fatalf("FetchWindowsISO: %v", err)
	}

	t.Logf("ISO path: %s", isoPath)
	info, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("stat ISO: %v", err)
	}
	t.Logf("ISO size: %.1f MB", float64(info.Size())/(1024*1024))

	if info.Size() < 100*1024*1024 {
		t.Errorf("ISO too small (%d bytes), expected at least 100 MB", info.Size())
	}

	bootWim, err := isokit.ReadFileFromISO(isoPath, "/sources/boot.wim")
	if err != nil {
		t.Fatalf("reading boot.wim from ISO: %v", err)
	}
	t.Logf("boot.wim size in ISO: %.1f MB", float64(len(bootWim))/(1024*1024))
	if len(bootWim) < 1024*1024 {
		t.Errorf("boot.wim too small (%d bytes)", len(bootWim))
	}

	installWim, err := isokit.ReadFileFromISO(isoPath, "/sources/install.wim")
	if err != nil {
		t.Fatalf("reading install.wim from ISO: %v", err)
	}
	t.Logf("install.wim size in ISO: %.1f MB", float64(len(installWim))/(1024*1024))
	if len(installWim) < 100*1024*1024 {
		t.Errorf("install.wim too small (%d bytes)", len(installWim))
	}
}

func TestAssembleISO_WithRealESD(t *testing.T) {
	if testing.Short() {
		t.Skip("long: requires pre-downloaded ESD; run without -short")
	}
	if !wimlib.Available() {
		t.Skip("wimlib not available: build with -tags wimlib and wimlib installed (brew install wimlib)")
	}

	esdPath := os.Getenv("DEVCELL_TEST_ESD_PATH")
	if esdPath == "" {
		t.Skip("set DEVCELL_TEST_ESD_PATH to a downloaded professional_en-us.esd to run this test")
	}

	info, err := os.Stat(esdPath)
	if err != nil {
		t.Fatalf("stat ESD: %v", err)
	}
	t.Logf("ESD: %s (%.1f MB)", esdPath, float64(info.Size())/(1024*1024))

	workDir := t.TempDir()
	isoPath := workDir + "/test-output.iso"

	err = AssembleISO(context.Background(), esdPath, AssembleConfig{
		WorkDir: workDir,
		ISOPath: isoPath,
		Label:   "W11_TEST",
		LogFunc: t.Logf,
	})
	if err != nil {
		t.Fatalf("AssembleISO: %v", err)
	}

	isoInfo, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("stat ISO: %v", err)
	}
	t.Logf("ISO created: %s (%.1f MB)", isoPath, float64(isoInfo.Size())/(1024*1024))

	bootWim, err := isokit.ReadFileFromISO(isoPath, "/sources/boot.wim")
	if err != nil {
		t.Fatalf("reading boot.wim from ISO: %v", err)
	}
	t.Logf("boot.wim: %.1f MB", float64(len(bootWim))/(1024*1024))

	installWim, err := isokit.ReadFileFromISO(isoPath, "/sources/install.wim")
	if err != nil {
		t.Fatalf("reading install.wim from ISO: %v", err)
	}
	t.Logf("install.wim: %.1f MB", float64(len(installWim))/(1024*1024))
}
