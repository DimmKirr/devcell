package uupdump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/DimmKirr/devcell/internal/wimlib"
)

type AssembleConfig struct {
	WorkDir string
	ISOPath string
	Label   string
	RefESDs []string // glob patterns for reference ESD files (e.g. "/path/to/*.esd")
	LogFunc func(format string, args ...any)
}

func (c *AssembleConfig) logf(format string, args ...any) {
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

// bootWimSetupFiles lists the sources/ files that must be injected into
// boot.wim image 2 (Windows Setup) so that setup.exe can run inside WinPE.
// Derived from CrystalFetch converter/convert.sh bootSourcesList.
var bootWimSetupFiles = []string{
	"sources/setup.exe",
	"sources/SetupHost.exe",
	"sources/SetupCore.dll",
	"sources/SetupMgr.dll",
	"sources/SetupPlatform.dll",
	"sources/SetupPlatform.exe",
	"sources/SetupPlatform.cfg",
	"sources/SetupPrep.exe",
	"sources/setupcompat.dll",
	"sources/wimgapi.dll",
	"sources/wimprovider.dll",
	"sources/win32ui.dll",
	"sources/w32uiimg.dll",
	"sources/w32uires.dll",
	"sources/uxlib.dll",
	"sources/uxlibres.dll",
	"sources/spwizeng.dll",
	"sources/spwizimg.dll",
	"sources/spwizres.dll",
	"sources/spflvrnt.dll",
	"sources/spprgrss.dll",
	"sources/imagelib.dll",
	"sources/imagingprovider.dll",
	"sources/dism.exe",
	"sources/dismapi.dll",
	"sources/dismcore.dll",
	"sources/dismcoreps.dll",
	"sources/dismprov.dll",
	"sources/bcd.dll",
	"sources/bootsvc.dll",
	"sources/cmisetup.dll",
	"sources/unattend.dll",
	"sources/unbcl.dll",
	"sources/xmllite.dll",
	"sources/lang.ini",
	"sources/locale.nls",
	"sources/compliance.ini",
	"sources/autorun.dll",
	"sources/MediaSetupUIMgr.dll",
	"sources/vhdprovider.dll",
	"sources/ServicingCommon.dll",
	"sources/SmiEngine.dll",
	"sources/wdscore.dll",
	"sources/wdscsl.dll",
	"sources/diagnostic.dll",
	"sources/diager.dll",
	"sources/diagtrack.dll",
	"sources/diagtrackrunner.exe",
	"sources/cryptosetup.dll",
	"sources/input.dll",
	"sources/reagent.dll",
	"sources/reagent.xml",
	"sources/schema.dat",
	"sources/segoeui.ttf",
	"sources/hwcompat.dll",
	"sources/hwcompat.txt",
	"sources/hwreqchk.dll",
	"sources/ndiscompl.dll",
	"sources/pnpibs.dll",
	"sources/offline.xml",
	"sources/appraiser.dll",
	"sources/compatctrl.dll",
	"sources/compatprovider.dll",
	"sources/folderprovider.dll",
	"sources/logprovider.dll",
	"sources/nlsbres.dll",
	"sources/ntdsupg.dll",
	"sources/upgloader.dll",
	"sources/upgrade_frmwrk.xml",
	"sources/sqmapi.dll",
	"sources/utcapi.dll",
	"sources/wpx.dll",
	"sources/WinDlp.dll",
	"sources/ARUNIMG.dll",
	"sources/arunres.dll",
	"sources/alert.gif",
	"sources/warning.gif",
	"sources/winsetup.dll",
	"sources/rollback.exe",
	"sources/appcompat.xsl",
	"sources/appcompat_bidi.xsl",
	"sources/appcompat_detailed_bidi_txt.xsl",
	"sources/appcompat_detailed_txt.xsl",
	"sources/idwbinfo.txt",
	"sources/hwexclude.txt",
	"sources/hwexcludePE.txt",
	"sources/hwcompatPE.txt",
	"sources/testplugin.dll",
	"sources/wdsclient.dll",
	"sources/wdsclientapi.dll",
	"sources/wdscommonlib.dll",
	"sources/wdsimage.dll",
	"sources/wdstptc.dll",
	"sources/wdsutil.dll",
	"sources/reagent.admx",
	"sources/inf/setup.cfg",
}

// AssembleISO converts an ESD file into a bootable Windows installer ISO.
//
// UUP dump ESDs typically contain 3 images:
//
//	image 1: "Windows Setup Media"   → extract to staging dir (boot files)
//	image 2: "WinRE / WinPE"         → export to boot.wim (boot environment)
//	image 3: "Windows 11 Pro" (etc.) → export to install.wim (OS image)
//
// boot.wim gets two images (matching CrystalFetch converter):
//
//	image 1: "Microsoft Windows PE"     — WinPE recovery environment
//	image 2: "Microsoft Windows Setup"  — installer with setup.exe (bootable)
//
// Traditional install ESDs may have 4+ images (images 2-3 boot, 4+ editions).
func AssembleISO(_ context.Context, esdPath string, cfg AssembleConfig) error {
	if cfg.Label == "" {
		cfg.Label = "YOURISO"
	}

	cfg.logf("assembling ISO from ESD: %s", esdPath)

	stageDir := filepath.Join(cfg.WorkDir, "iso-stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("creating stage dir: %w", err)
	}

	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available: build with -tags wimlib and wimlib installed (brew install wimlib)")
	}

	esdInfo, err := os.Stat(esdPath)
	if err != nil {
		return fmt.Errorf("stat ESD: %w", err)
	}
	cfg.logf("opening ESD: %s (%.1f MB)", esdPath, float64(esdInfo.Size())/(1024*1024))

	esd, err := wimlib.OpenWIM(esdPath)
	if err != nil {
		return fmt.Errorf("opening ESD: %w", err)
	}
	defer esd.Close()

	if len(cfg.RefESDs) > 0 {
		cfg.logf("referencing component ESDs via %d glob pattern(s)", len(cfg.RefESDs))
		for _, g := range cfg.RefESDs {
			cfg.logf("  glob pattern: %s", g)
			matches, globErr := filepath.Glob(g)
			if globErr != nil {
				cfg.logf("  WARNING: Go filepath.Glob error: %v", globErr)
			} else {
				cfg.logf("  Go filepath.Glob matched %d file(s):", len(matches))
				for _, m := range matches {
					info, _ := os.Stat(m)
					if info != nil {
						cfg.logf("    %s (%.1f MB)", filepath.Base(m), float64(info.Size())/(1024*1024))
					} else {
						cfg.logf("    %s (stat failed)", filepath.Base(m))
					}
				}
			}
		}

		// Try individual file paths first (bypasses wimlib glob expansion).
		// Collect all .esd files from the glob directories.
		var refPaths []string
		for _, g := range cfg.RefESDs {
			dir := filepath.Dir(g)
			entries, err := os.ReadDir(dir)
			if err != nil {
				cfg.logf("  WARNING: cannot read dir %s: %v", dir, err)
				continue
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".esd") {
					p := filepath.Join(dir, e.Name())
					refPaths = append(refPaths, p)
				}
			}
		}

		cfg.logf("resolved %d individual ESD file(s) for reference", len(refPaths))
		for _, p := range refPaths {
			info, _ := os.Stat(p)
			if info != nil {
				cfg.logf("  ref: %s (%.1f MB)", filepath.Base(p), float64(info.Size())/(1024*1024))
			}
		}

		if len(refPaths) > 0 {
			cfg.logf("calling wimlib_reference_resource_files with %d individual paths (no glob)", len(refPaths))
			if err := esd.ReferenceResourceFilePaths(refPaths); err != nil {
				cfg.logf("WARNING: individual-path reference failed: %v", err)
				cfg.logf("falling back to glob-based reference")
				if err := esd.ReferenceResourceFiles(cfg.RefESDs); err != nil {
					return fmt.Errorf("referencing component ESDs: %w", err)
				}
			} else {
				cfg.logf("individual-path reference succeeded")
			}
		} else {
			cfg.logf("no individual ESD files found, trying glob reference")
			if err := esd.ReferenceResourceFiles(cfg.RefESDs); err != nil {
				return fmt.Errorf("referencing component ESDs: %w", err)
			}
		}
	}

	imageCount, err := esd.ImageCount()
	if err != nil {
		return fmt.Errorf("counting ESD images: %w", err)
	}
	cfg.logf("ESD contains %d image(s)", imageCount)
	for i := 1; i <= imageCount; i++ {
		desc, _ := esd.ImageDescription(i)
		cfg.logf("  image %d: %s", i, desc)
	}

	if imageCount < 3 {
		return fmt.Errorf("ESD has %d image(s), expected at least 3 (setup media, boot env, OS)", imageCount)
	}

	cfg.logf("extracting setup media (image 1) → %s", stageDir)
	if err := esd.ExtractImage(1, stageDir, nil); err != nil {
		return fmt.Errorf("extracting setup media (image 1): %w", err)
	}
	cfg.logf("image 1 extraction complete")

	sourcesDir := filepath.Join(stageDir, "sources")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return fmt.Errorf("creating sources dir: %w", err)
	}

	bootWimPath := filepath.Join(sourcesDir, "boot.wim")
	installWimPath := filepath.Join(sourcesDir, "install.wim")

	// boot.wim: two images — WinPE (image 1) + Windows Setup (image 2, bootable).
	// Matches CrystalFetch converter/convert.sh: both images start from ESD image 2,
	// but image 2 gets setup.exe and sources injected so it boots into Windows Setup
	// instead of WinRE recovery.
	cfg.logf("creating boot.wim (LZX compression) with 2 images")
	bootWim, err := wimlib.CreateWIM(wimlib.LZX)
	if err != nil {
		return fmt.Errorf("creating boot.wim: %w", err)
	}
	defer bootWim.Close()

	cfg.logf("exporting WinPE (ESD image 2) → boot.wim image 1")
	if err := esd.ExportImage(2, bootWim, wimlib.LZX); err != nil {
		return fmt.Errorf("exporting WinPE (image 2): %w", err)
	}

	cfg.logf("exporting Windows Setup (ESD image 2) → boot.wim image 2")
	if err := esd.ExportImage(2, bootWim, wimlib.LZX); err != nil {
		return fmt.Errorf("exporting Setup (image 2 copy): %w", err)
	}

	// Name images to match standard Windows ISOs.
	if err := bootWim.SetImageName(1, "Microsoft Windows PE", "Microsoft Windows PE"); err != nil {
		cfg.logf("WARNING: failed to set image 1 name: %v", err)
	}
	if err := bootWim.SetImageName(2, "Microsoft Windows Setup", "Microsoft Windows Setup"); err != nil {
		cfg.logf("WARNING: failed to set image 2 name: %v", err)
	}

	// Delete winpeshl.ini from image 2 — prevents WinRE recovery from launching.
	cfg.logf("removing winpeshl.ini from boot.wim image 2 (prevents WinRE recovery launch)")
	if err := bootWim.UpdateImageDelete(2, "/Windows/System32/winpeshl.ini"); err != nil {
		cfg.logf("WARNING: failed to delete winpeshl.ini from image 2: %v (may not exist)", err)
	}

	// Inject setup files from staging dir into boot.wim image 2.
	// setup.exe at root is the entry point; sources/ files are its dependencies.
	setupExe := filepath.Join(stageDir, "setup.exe")
	if _, err := os.Stat(setupExe); err == nil {
		cfg.logf("injecting setup.exe → boot.wim image 2")
		if err := bootWim.UpdateImageAdd(2, setupExe, "/setup.exe"); err != nil {
			return fmt.Errorf("injecting setup.exe into boot.wim: %w", err)
		}
	} else {
		cfg.logf("WARNING: setup.exe not found in staging dir — boot.wim image 2 may not boot correctly")
	}

	injected := 0
	for _, relPath := range bootWimSetupFiles {
		srcPath := filepath.Join(stageDir, relPath)
		if _, err := os.Stat(srcPath); err != nil {
			continue
		}
		wimPath := "/" + relPath
		if err := bootWim.UpdateImageAdd(2, srcPath, wimPath); err != nil {
			cfg.logf("WARNING: failed to inject %s into boot.wim: %v", relPath, err)
			continue
		}
		injected++
	}
	cfg.logf("injected %d setup files into boot.wim image 2", injected)

	// Set FLAGS on images (9 = WinPE, 2 = Setup) and mark image 2 as boot.
	if err := bootWim.SetImageProperty(1, "FLAGS", "9"); err != nil {
		cfg.logf("WARNING: failed to set FLAGS=9 on image 1: %v", err)
	}
	if err := bootWim.SetImageProperty(2, "FLAGS", "2"); err != nil {
		cfg.logf("WARNING: failed to set FLAGS=2 on image 2: %v", err)
	}
	if err := bootWim.SetBootImage(2); err != nil {
		return fmt.Errorf("setting boot image to 2: %w", err)
	}

	cfg.logf("writing boot.wim → %s", bootWimPath)
	if err := bootWim.Write(bootWimPath); err != nil {
		return fmt.Errorf("writing boot.wim: %w", err)
	}

	bootInfo, _ := os.Stat(bootWimPath)
	if bootInfo != nil {
		cfg.logf("boot.wim size: %.1f MB", float64(bootInfo.Size())/(1024*1024))
	}

	// install.wim: contains the OS image(s) — image 3+ from the ESD
	cfg.logf("creating install.wim (LZMS compression, %d image(s))", imageCount-2)
	installWim, err := wimlib.CreateWIM(wimlib.LZMS)
	if err != nil {
		return fmt.Errorf("creating install.wim: %w", err)
	}
	defer installWim.Close()

	for i := 3; i <= imageCount; i++ {
		desc, _ := esd.ImageDescription(i)
		if desc == "" {
			desc = fmt.Sprintf("image %d", i)
		}
		cfg.logf("exporting %s (image %d/%d) → install.wim [this image requires component ESD resources]", desc, i, imageCount)
		if err := esd.ExportImage(i, installWim, wimlib.LZMS); err != nil {
			cfg.logf("ERROR: wimlib_export_image(%d) failed: %v", i, err)
			cfg.logf("  This means the component ESDs were not successfully referenced.")
			cfg.logf("  Check that all component ESDs are present in the download directory")
			cfg.logf("  and that wimlib_reference_resource_files actually opened them.")
			cfg.logf("  The install ESD: %s", esdPath)
			return fmt.Errorf("exporting image %d (%s): %w", i, desc, err)
		}
		cfg.logf("image %d export complete", i)
	}

	cfg.logf("writing install.wim → %s", installWimPath)
	if err := installWim.Write(installWimPath); err != nil {
		return fmt.Errorf("writing install.wim: %w", err)
	}

	installInfo, _ := os.Stat(installWimPath)
	if installInfo != nil {
		cfg.logf("install.wim size: %.1f MB", float64(installInfo.Size())/(1024*1024))
	}

	cfg.logf("creating ISO: %s", cfg.ISOPath)
	if err := isokit.CreateWindowsISO(cfg.ISOPath, stageDir, cfg.Label); err != nil {
		return fmt.Errorf("creating ISO: %w", err)
	}

	isoInfo, _ := os.Stat(cfg.ISOPath)
	if isoInfo != nil {
		cfg.logf("ISO created: %s (%.1f MB)", cfg.ISOPath, float64(isoInfo.Size())/(1024*1024))
	}
	return nil
}
