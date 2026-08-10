package qemu

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// guestFS is the guest-side PowerShell tree: a shared module plus one script
// per stage. Unlike templates/, nothing here is rendered — it is real
// PowerShell, embedded verbatim and delivered on the per-run control volume.
//
// That distinction is the point of CELL-402: Go-interpolated PowerShell is
// never linted, never runnable standalone, and fails only on a live guest
// minutes-to-hours in (lost quotes killed a 40-minute pipeline; an
// interpolated colon broke icacls). Real files remove the bug class.
//
//go:embed guest
var guestFS embed.FS

// GuestControlDir is where the guest tree lands on the control volume.
const GuestControlDir = "/devcell"

// GuestFile returns one file from the embedded guest tree, addressed the way
// stages refer to it ("Devcell.psm1", "stages/wsl2-enable.ps1").
func GuestFile(name string) ([]byte, error) {
	data, err := guestFS.ReadFile(path.Join("guest", name))
	if err != nil {
		return nil, fmt.Errorf("guest file %s: %w", name, err)
	}
	return data, nil
}

// GuestPayload returns the whole guest tree keyed by its path on the control
// volume, ready for BuildControlVolume. Everything ships every run: the
// volume is built on the host and attached at boot, so it can never drift
// from the repo the way a copy written into the qcow2 would.
func GuestPayload() (map[string][]byte, error) {
	payload := map[string][]byte{}
	err := fs.WalkDir(guestFS, "guest", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := guestFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		payload[GuestControlDir+"/"+strings.TrimPrefix(p, "guest/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collecting guest payload: %w", err)
	}
	return payload, nil
}

// NixhomeTarball packs a nixhome directory for control-volume delivery, all
// contents under a top-level "nixhome/" so extraction recreates the layout.
//
// A tarball, not a live reference: activating straight from the project
// share fails twice over — nix ingests the surrounding repo as a dirty
// git+file input, and the share's symlinks (36 in the icewm theme alone) die
// on readlink across virtiofs+drvfs (run 20260804). Inside a tarball the
// symlinks are just entries; extracted onto the distro's ext4 they work.
func NixhomeTarball(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := path.Join("nixhome", filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = name
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("packing nixhome from %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GuestPayloadWithNixhome is GuestPayload plus the nixhome tarball the
// home-manager stage extracts inside the distro — one control volume carries
// both the scripts and the config they activate.
func GuestPayloadWithNixhome(nixhomeDir string) (map[string][]byte, error) {
	payload, err := GuestPayload()
	if err != nil {
		return nil, err
	}
	tgz, err := NixhomeTarball(nixhomeDir)
	if err != nil {
		return nil, err
	}
	payload[GuestControlDir+"/nixhome.tgz"] = tgz
	return payload, nil
}
