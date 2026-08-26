//go:build wimlib

package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/wimlib"
)

// TransplantWSLIntoBootWim lays the WSL engine into a WinPE boot.wim.
//
// The engine ships as an MSI and WinPE has no Windows Installer service,
// so this places the trimmed payload at the MSI's own destination
// (Program Files/WSL) from an msiextract of the release, plus the inbox
// client shim out of install.wim's System32. Registration — WSLService,
// the proxy stub COM class — happens at boot in the pass4 script; files
// alone are inert, which is what makes this safe to always inject.
//
// wslDir is the extracted MSI's WSL directory (PFiles64/WSL).
func TransplantWSLIntoBootWim(bootWimPath, wslDir, installWimPath string) error {
	return TransplantWSLIntoBootWimLogged(bootWimPath, wslDir, installWimPath, nil)
}

// TransplantWSLIntoBootWimLogged is TransplantWSLIntoBootWim with a hook
// for per-step events. onEvent may be nil.
func TransplantWSLIntoBootWimLogged(bootWimPath, wslDir, installWimPath string, onEvent func(TransplantEvent)) error {
	emit := func(e TransplantEvent) {
		if onEvent == nil {
			return
		}
		e.TS = time.Now().Format(time.RFC3339Nano)
		onEvent(e)
	}
	fail := func(stage string, err error) error {
		emit(TransplantEvent{Event: stage, Status: "fail", Error: err.Error()})
		return err
	}

	engine := WSLEngineFiles()
	shim := WSLInboxShim()
	emit(TransplantEvent{Event: "wsl_transplant_start", File: bootWimPath,
		Count: len(engine) + len(shim)})

	// The engine payload must exist on disk before we touch the wim.
	for _, f := range engine {
		if _, err := os.Stat(filepath.Join(wslDir, filepath.FromSlash(f))); err != nil {
			return fail("check_engine", fmt.Errorf("engine payload incomplete: %w", err))
		}
	}

	shimStaging, err := os.MkdirTemp("", "devcell-wsl-shim-stage-*")
	if err != nil {
		return fmt.Errorf("shim staging dir: %w", err)
	}
	defer os.RemoveAll(shimStaging)

	if err := ExtractParityFiles(installWimPath, shim, shimStaging); err != nil {
		return fail("stage_shim", fmt.Errorf("staging inbox shim: %w", err))
	}
	emit(TransplantEvent{Event: "stage_shim", Status: "ok", Count: len(shim)})

	wim, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fail("open_wim", fmt.Errorf("opening boot.wim: %w", err))
	}
	defer wim.Close()

	for _, f := range engine {
		local := filepath.Join(wslDir, filepath.FromSlash(f))
		dest := WSLEngineDestDir + "/" + f
		wimPath := `\` + strings.ReplaceAll(dest, "/", `\`)

		var size int
		if info, err := os.Stat(local); err == nil {
			size = int(info.Size())
		}
		if err := wim.UpdateImageAdd(2, local, wimPath); err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", dest, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			File: dest, Source: "wsl-msi", Bytes: size})
	}

	for _, f := range shim {
		local := filepath.Join(shimStaging, filepath.FromSlash(f.Dest))
		wimPath := `\` + strings.ReplaceAll(f.Dest, "/", `\`)

		var size int
		if info, err := os.Stat(local); err == nil {
			size = int(info.Size())
		}
		if err := wim.UpdateImageAdd(2, local, wimPath); err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", f.Dest, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			File: f.Dest, Source: "install.wim System32", Bytes: size})
	}

	if err := wim.Overwrite(); err != nil {
		return fail("commit", fmt.Errorf("committing boot.wim: %w", err))
	}
	emit(TransplantEvent{Event: "commit", Status: "ok", File: bootWimPath})
	emit(TransplantEvent{Event: "wsl_transplant_complete", Status: "ok",
		Count: len(engine) + len(shim)})
	return nil
}
