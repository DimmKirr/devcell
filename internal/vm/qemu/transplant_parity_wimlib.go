//go:build wimlib

package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-wimlib"
)

// resolveWinSxS finds the WinSxS component directory matching prefix and
// returns the WIM path to the target file inside it. When multiple versions
// exist, the last one in lexicographic order wins (versions are dot-separated
// decimals, so lex order matches numeric order within the same major scheme).
func resolveWinSxS(wim *wimlib.WIM, imageNum int, component, filename, sxsFile string) (string, error) {
	dirs, err := wim.ListChildren(imageNum, `\Windows\WinSxS`)
	if err != nil {
		return "", fmt.Errorf("listing WinSxS: %w", err)
	}

	prefix := "arm64_" + component + "_"
	var matches []string
	for _, d := range dirs {
		if strings.HasPrefix(d, prefix) && !strings.Contains(d, ".resources") {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no arm64 WinSxS component matching %q", component)
	}

	// Pick highest version: last in sorted order. The naming scheme
	// arm64_<name>_<pubkey>_<version>_none_<hash> sorts correctly because
	// version segments are zero-padded.
	best := matches[0]
	for _, m := range matches[1:] {
		if m > best {
			best = m
		}
	}

	// Find the target file inside the component directory.
	target := filename
	if sxsFile != "" {
		target = sxsFile
	}

	children, err := wim.ListChildren(imageNum, `\Windows\WinSxS\`+best)
	if err != nil {
		return "", fmt.Errorf("listing %s: %w", best, err)
	}

	for _, child := range children {
		if strings.EqualFold(child, target) {
			return `\Windows\WinSxS\` + best + `\` + child, nil
		}
		// Handle GUID-prefixed names (e.g. "07409496-..._HyperV-ComputeNetwork.dll")
		if sxsFile != "" && strings.HasSuffix(strings.ToLower(child), "_"+strings.ToLower(target)) {
			return `\Windows\WinSxS\` + best + `\` + child, nil
		}
	}

	return "", fmt.Errorf("%s not found in component %s (children: %v)", target, best, children)
}

// ExtractParityFiles extracts each parity file from a stock install.wim by
// resolving its WinSxS component path and decompressing any DCS stubs.
// No prior DISM enablement is needed: files are sourced directly from the
// component store and decompressed natively via wimlib LZMS.
func ExtractParityFiles(donorWimPath string, files []ParityFile, destDir string) error {
	wim, err := wimlib.OpenWIM(donorWimPath)
	if err != nil {
		return fmt.Errorf("opening donor wim: %w", err)
	}
	defer wim.Close()

	staging, err := os.MkdirTemp("", "devcell-parity-*")
	if err != nil {
		return fmt.Errorf("staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, f := range files {
		basename := filepath.Base(filepath.FromSlash(f.Dest))

		var wimPath string
		if f.Component != "" {
			resolved, err := resolveWinSxS(wim, 1, f.Component, basename, f.SxSFile)
			if err != nil {
				return fmt.Errorf("%s: %w", f.Dest, err)
			}
			wimPath = resolved
		} else {
			wimPath = `\` + strings.ReplaceAll(f.Dest, "/", `\`)
		}

		// Extract to a per-file staging dir to avoid collisions from
		// WinSxS paths vs System32 paths.
		fileStaging, err := os.MkdirTemp(staging, "f-*")
		if err != nil {
			return fmt.Errorf("%s: staging dir: %w", f.Dest, err)
		}

		if err := wim.ExtractPaths(1, fileStaging, []string{wimPath}); err != nil {
			return fmt.Errorf("%s: extracting %s: %w", f.Dest, wimPath, err)
		}

		// Find the extracted file: wimlib preserves the full path structure.
		var extractedPath string
		if err := filepath.Walk(fileStaging, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.EqualFold(info.Name(), basename) {
				extractedPath = path
			}
			// For SxSFile (GUID-prefixed), also match by suffix.
			if !info.IsDir() && f.SxSFile != "" &&
				strings.HasSuffix(strings.ToLower(info.Name()), "_"+strings.ToLower(f.SxSFile)) {
				extractedPath = path
			}
			return nil
		}); err != nil {
			return fmt.Errorf("%s: walking staging: %w", f.Dest, err)
		}
		if extractedPath == "" {
			return fmt.Errorf("%s: extracted file not found in staging", f.Dest)
		}

		data, err := os.ReadFile(extractedPath)
		if err != nil {
			return fmt.Errorf("%s: reading extracted: %w", f.Dest, err)
		}

		// Decompress DCS stubs. Text files (INF, MOF) pass through unchanged.
		if wimlib.IsDCS(data) {
			data, err = wimlib.DecompressDCS(data)
			if err != nil {
				return fmt.Errorf("%s: DCS decompress: %w", f.Dest, err)
			}
		}

		dest := filepath.Join(destDir, filepath.FromSlash(f.Dest))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("%s: creating dir: %w", f.Dest, err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("%s: writing: %w", f.Dest, err)
		}
	}

	return nil
}

// CopyParityFilesFromDir copies parity files from a pre-harvested donor
// directory into destDir. The donor directory must contain files at their
// Windows/ paths (e.g. Windows/System32/vmwp.exe).
func CopyParityFilesFromDir(donorDir string, files []ParityFile, destDir string) error {
	for _, f := range files {
		src := filepath.Join(donorDir, filepath.FromSlash(f.Dest))
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("%s: reading from donor: %w", f.Dest, err)
		}

		dest := filepath.Join(destDir, filepath.FromSlash(f.Dest))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("%s: creating dir: %w", f.Dest, err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("%s: writing: %w", f.Dest, err)
		}
	}
	return nil
}
