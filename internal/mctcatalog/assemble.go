package mctcatalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/DimmKirr/devcell/internal/wimlib"
)

type AssembleConfig struct {
	WorkDir string
	ISOPath string
	Label   string
	LogFunc func(format string, args ...any)
}

func (c *AssembleConfig) logf(format string, args ...any) {
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

// AssembleMCTISO converts a self-contained MCT ESD into a bootable Windows ISO.
//
// MCT ESD image layout (differs from UUP dump):
//
//	image 1: boot files          → extract to staging dir
//	image 2: WinPE               → export to boot.wim (image 1)
//	image 3: Windows Setup       → export to boot.wim (image 2, marked bootable)
//	image 4+: editions (Pro etc) → export to install.wim
//
// No --ref / resource references needed — MCT ESDs are self-contained.
func AssembleMCTISO(_ context.Context, esdPath string, cfg AssembleConfig) error {
	if cfg.Label == "" {
		cfg.Label = "W11_EN-US"
	}

	stageDir := filepath.Join(cfg.WorkDir, "iso-stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("creating stage dir: %w", err)
	}

	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available: build with -tags wimlib and install wimlib (brew install wimlib)")
	}

	esdInfo, err := os.Stat(esdPath)
	if err != nil {
		return fmt.Errorf("stat ESD: %w", err)
	}
	cfg.logf("opening MCT ESD: %s (%.1f MB)", esdPath, float64(esdInfo.Size())/(1024*1024))

	esd, err := wimlib.OpenWIM(esdPath)
	if err != nil {
		return fmt.Errorf("opening ESD: %w", err)
	}
	defer esd.Close()

	imageCount, err := esd.ImageCount()
	if err != nil {
		return fmt.Errorf("counting ESD images: %w", err)
	}
	cfg.logf("ESD contains %d image(s)", imageCount)
	for i := 1; i <= imageCount; i++ {
		desc, _ := esd.ImageDescription(i)
		cfg.logf("  image %d: %s", i, desc)
	}

	if imageCount < 4 {
		return fmt.Errorf("MCT ESD has %d image(s), expected at least 4 (boot files, WinPE, Setup, 1+ edition)", imageCount)
	}

	cfg.logf("extracting boot files (image 1) → %s", stageDir)
	if err := esd.ExtractImage(1, stageDir, nil); err != nil {
		return fmt.Errorf("extracting boot files (image 1): %w", err)
	}

	sourcesDir := filepath.Join(stageDir, "sources")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return fmt.Errorf("creating sources dir: %w", err)
	}

	bootWimPath := filepath.Join(sourcesDir, "boot.wim")
	installWimPath := filepath.Join(sourcesDir, "install.wim")

	cfg.logf("creating boot.wim with WinPE (image 2) + Setup (image 3)")
	bootWim, err := wimlib.CreateWIM(wimlib.LZX)
	if err != nil {
		return fmt.Errorf("creating boot.wim: %w", err)
	}
	defer bootWim.Close()

	cfg.logf("exporting WinPE (image 2) → boot.wim")
	if err := esd.ExportImage(2, bootWim, wimlib.LZX); err != nil {
		return fmt.Errorf("exporting WinPE (image 2): %w", err)
	}

	cfg.logf("exporting Windows Setup (image 3) → boot.wim")
	if err := esd.ExportImage(3, bootWim, wimlib.LZX); err != nil {
		return fmt.Errorf("exporting Setup (image 3): %w", err)
	}

	// Mark image 2 in boot.wim (Setup) as the boot image — matches CrystalFetch esd2iso.sh.
	if err := bootWim.SetBootImage(2); err != nil {
		return fmt.Errorf("setting boot image: %w", err)
	}

	cfg.logf("writing boot.wim → %s", bootWimPath)
	if err := bootWim.Write(bootWimPath); err != nil {
		return fmt.Errorf("writing boot.wim: %w", err)
	}
	if info, _ := os.Stat(bootWimPath); info != nil {
		cfg.logf("boot.wim: %.1f MB", float64(info.Size())/(1024*1024))
	}

	editionCount := imageCount - 3
	cfg.logf("creating install.wim with %d edition(s)", editionCount)
	installWim, err := wimlib.CreateWIM(wimlib.LZMS)
	if err != nil {
		return fmt.Errorf("creating install.wim: %w", err)
	}
	defer installWim.Close()

	for i := 4; i <= imageCount; i++ {
		desc, _ := esd.ImageDescription(i)
		if desc == "" {
			desc = fmt.Sprintf("image %d", i)
		}
		cfg.logf("exporting %s (image %d) → install.wim", desc, i)
		if err := esd.ExportImage(i, installWim, wimlib.LZMS); err != nil {
			return fmt.Errorf("exporting image %d (%s): %w", i, desc, err)
		}
	}

	cfg.logf("writing install.wim → %s", installWimPath)
	if err := installWim.Write(installWimPath); err != nil {
		return fmt.Errorf("writing install.wim: %w", err)
	}
	if info, _ := os.Stat(installWimPath); info != nil {
		cfg.logf("install.wim: %.1f MB", float64(info.Size())/(1024*1024))
	}

	cfg.logf("creating ISO: %s", cfg.ISOPath)
	if err := isokit.CreateWindowsISO(cfg.ISOPath, stageDir, cfg.Label); err != nil {
		return fmt.Errorf("creating ISO: %w", err)
	}

	if info, _ := os.Stat(cfg.ISOPath); info != nil {
		cfg.logf("ISO created: %s (%.1f MB)", cfg.ISOPath, float64(info.Size())/(1024*1024))
	}
	return nil
}
