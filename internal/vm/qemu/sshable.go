package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SSHAbleTestImageName returns the versioned filename for a saved ssh-able
// image: windows-sshable-<compact ISO 8601 UTC>.qcow. Compact (no colons) so
// the name is filesystem-safe everywhere, and lexicographic order equals
// chronological order.
func SSHAbleTestImageName(ts time.Time) string {
	return "windows-sshable-" + ts.UTC().Format("20060102T150405Z") + ".qcow"
}

// WSLReadyTestImageName returns the versioned filename for a saved WSL-ready
// image: a guest that already has the virtio drivers, the project share, the
// WSL2 features and the WSL engine — everything up to the point where a
// distro can be imported. Checkpointing there turns a ~40-minute prelude into
// a ~1-minute boot when iterating on the distro itself.
func WSLReadyTestImageName(ts time.Time) string {
	return "windows-wsl-" + ts.UTC().Format("20060102T150405Z") + ".qcow"
}

// NixReadyTestImageName returns the versioned filename for a saved
// nix-ready image: a guest where Windows' own hypervisor launches (EL3
// machine), NixOS-WSL is imported and nix answers inside it. This is the
// furthest checkpoint and the base for nix-based tests — from here only
// in-distro work (home-manager and beyond) remains.
func NixReadyTestImageName(ts time.Time) string {
	return "windows-nix-" + ts.UTC().Format("20060102T150405Z") + ".qcow"
}

// LatestSSHAbleTestImage returns the newest windows-sshable-*.qcow in dir.
func LatestSSHAbleTestImage(dir string) (string, error) {
	return latestTestImage(dir, "windows-sshable-",
		"run TestSSHAble_ConnectAndListFiles first")
}

// LatestWSLReadyTestImage returns the newest windows-wsl-*.qcow in dir.
func LatestWSLReadyTestImage(dir string) (string, error) {
	return latestTestImage(dir, "windows-wsl-",
		"run TestWindowsDevEnv_QEMU from an ssh-able image to create one")
}

// LatestNixReadyTestImage returns the newest windows-nix-*.qcow in dir.
func LatestNixReadyTestImage(dir string) (string, error) {
	return latestTestImage(dir, "windows-nix-",
		"run TestWindowsWSL2NixOS_QEMU on an EL3 machine to create one")
}

// latestTestImage picks the newest image with the given prefix by name —
// compact ISO timestamps sort chronologically, so no mtime comparison is
// needed (and none would be trustworthy on a bind mount).
func latestTestImage(dir, prefix, hint string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".qcow") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no %s*.qcow in %s — %s", prefix, dir, hint)
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// SSHAbleImagePath returns where a verified template is promoted to. The
// "ssh-able" image is the contract between the install test and everything
// built on top of it: an installed Windows whose SSH access has actually been
// exercised, not merely stamped.
func SSHAbleImagePath(home, stack string, modules []string) string {
	return filepath.Join(TemplateDir(home, stack, modules), "ssh-able.qcow2")
}

// BaseProfileImagePath returns where the dev-env pipeline's finished product
// is saved: an ssh-able Windows with WSL1, nix and the nixhome base profile
// activated. This is the state `cell build --engine=qemu` is ultimately meant
// to end at for the base stack.
func BaseProfileImagePath(home, stack string, modules []string) string {
	return filepath.Join(TemplateDir(home, stack, modules), "base-profile.qcow2")
}

// SaveSSHAbleImage copies a verified template disk to dest as a standalone
// qcow2 — see saveStandaloneImage.
func SaveSSHAbleImage(templateDisk, dest string) error {
	return saveStandaloneImage(templateDisk, dest)
}

// SaveBaseProfileImage flattens a dev-env overlay (nix + home-manager on top
// of ssh-able) into a standalone image — see saveStandaloneImage. The VM
// writing the overlay must be shut down first.
func SaveBaseProfileImage(overlayDisk, dest string) error {
	return saveStandaloneImage(overlayDisk, dest)
}

// saveStandaloneImage converts src to a standalone qcow2 at dest. qemu-img
// convert rather than a file copy: it validates the source while reading it
// and flattens any backing chain, so dest survives both a --force rebuild
// deleting the template and a temp dir deleting an overlay's backing file.
func saveStandaloneImage(src, dest string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("source image: %w", err)
	}
	qemuImg, err := qemuImgPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating image directory: %w", err)
	}
	// Write to a sibling and rename, so a killed copy never leaves a
	// plausible-looking half image behind (same pattern as the downloaders).
	part := dest + ".part"
	cmd := exec.Command(qemuImg, "convert", "-O", "qcow2", src, part)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("qemu-img convert: %w\n%s", err, out)
	}
	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("promoting image: %w", err)
	}
	return nil
}
