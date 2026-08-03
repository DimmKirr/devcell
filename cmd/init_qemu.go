//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// runInitQemu prepares directories, SSH keypair, and downloads VirtIO drivers
// for a QEMU Windows VM. Mirrors runInitTart: scaffold config, no VM creation.
// The actual VM creation happens in `cell build --engine=qemu`.
func runInitQemu(cellName, hostHome, stack string, force bool) error {
	sshDir := qemuKeyDir(hostHome, cellName)
	templateDir := qemu.TemplateDir(hostHome, stack, nil)
	instanceDir := qemu.InstanceDir(hostHome, cellName)

	ux.Debugf("init qemu: cell=%s stack=%s", cellName, stack)
	ux.Debugf("ssh dir: %s", sshDir)

	pr := &ux.PhaseRunner{}

	// --- Phase 1: Create directories ---
	if err := pr.PhaseDetailed("Preparing directories", func() (string, error) {
		for _, dir := range []string{sshDir, templateDir, instanceDir} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("creating %s: %w", dir, err)
			}
		}
		return sshDir, nil
	}); err != nil {
		return err
	}

	// --- Phase 2: Generate SSH keypair ---
	privKeyPath := filepath.Join(sshDir, "id_ed25519")
	pubKeyPath := filepath.Join(sshDir, "id_ed25519.pub")
	if err := pr.PhaseDetailed("Generating SSH keypair", func() (string, error) {
		if !force {
			if _, err := os.Stat(privKeyPath); err == nil {
				ux.Debugf("SSH keypair exists, skipping (use --force to regenerate)")
				return privKeyPath, nil
			}
		}

		os.Remove(privKeyPath)
		os.Remove(pubKeyPath)
		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", privKeyPath, "-N", "", "-q")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("ssh-keygen: %w\n%s", err, out)
		}
		ux.Debugf("SSH keypair generated: %s", privKeyPath)

		// Collect existing ~/.ssh pub keys to add to authorized_keys
		pubKey, err := os.ReadFile(pubKeyPath)
		if err != nil {
			return "", fmt.Errorf("reading public key: %w", err)
		}
		allKeys := strings.TrimSpace(string(pubKey))

		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			existing := collectSSHPubKeys(filepath.Join(homeDir, ".ssh"))
			if existing != "" {
				allKeys = allKeys + "\n" + existing
				ux.Debugf("added existing ~/.ssh pub keys")
			}
		}

		authKeysPath := filepath.Join(sshDir, "authorized_keys")
		if err := os.WriteFile(authKeysPath, []byte(allKeys+"\n"), 0644); err != nil {
			return "", fmt.Errorf("writing authorized_keys: %w", err)
		}

		return privKeyPath, nil
	}); err != nil {
		return err
	}

	// --- Phase 3: Download VirtIO drivers ---
	if err := pr.PhaseDetailed("Downloading VirtIO drivers", func() (string, error) {
		obs := &phaseObserver{logf: ux.Debugf, runner: pr}
		path, err := qemu.DownloadVirtioDrivers(context.Background(), hostHome, force, obs)
		if err != nil {
			return "", err
		}
		return path, nil
	}); err != nil {
		return err
	}

	// --- Phase 4: Download Windows ARM64 ISO ---
	if err := pr.PhaseDetailed("Downloading Windows ARM64 ISO", func() (string, error) {
		obs := &phaseObserver{logf: ux.Debugf, runner: pr}
		path, err := qemu.DownloadWindowsISO(context.Background(), hostHome, "en-us", force, obs)
		if err != nil {
			return "", err
		}
		return path, nil
	}); err != nil {
		return err
	}

	pr.Seal("qemu artifacts ready")
	fmt.Println("  Run: cell build --engine=qemu")
	return nil
}

// collectSSHPubKeys reads all *.pub files from sshDir.
func collectSSHPubKeys(sshDir string) string {
	matches, err := filepath.Glob(filepath.Join(sshDir, "*.pub"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	var keys []string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(data))
		if line != "" {
			keys = append(keys, line)
		}
	}
	return strings.Join(keys, "\n")
}

// phaseObserver adapts qemu.Observer to ux.Debugf + PhaseRunner spinner updates.
type phaseObserver struct {
	logf   func(string, ...any)
	runner *ux.PhaseRunner
}

// Stamped: build logs are read alongside guest stage logs, QEMU stderr and
// the screenshot series, all of which carry ISO-8601 UTC instants. A
// relative or bare line cannot be correlated with them.
func (o *phaseObserver) Logf(format string, args ...any) {
	o.logf("%s "+format,
		append([]any{time.Now().UTC().Format("2006-01-02T15:04:05Z")}, args...)...)
}
func (o *phaseObserver) Progress(_ float64, msg string) {
	if o.runner != nil {
		o.runner.UpdateText(msg)
	}
}
