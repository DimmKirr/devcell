//go:build darwin && arm64

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/tart"
)

// Cirrus Labs OCI images ship with this default user/password.
const tartImageUser = "admin"
const tartImagePassword = "admin"

// runInitTart prepares the local artifact directory and SSH keypair for a tart
// VM. It mirrors what Docker init does: scaffold config, no images, no
// containers. The actual VM creation happens in `cell build --engine=tart`.
func runInitTart(cellName, hostHome, projectDir, stack, nixhomePath string, force, noCache bool) error {
	cfg := tart.InitConfig{
		CellName: cellName,
		HomeDir:  hostHome,
		Stack:    stack,
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	sshPaths := tart.NewCellSSHPaths(hostHome, cellName)

	ux.Debugf("init config: cell=%s stack=%s", cfg.CellName, cfg.Stack)
	ux.Debugf("ssh dir: %s", sshPaths.Dir)

	pr := &ux.PhaseRunner{}

	// --- Phase 1: Prepare SSH directory ---
	if err := pr.PhaseDetailed("Preparing SSH directory", func() (string, error) {
		if err := tart.PrepareArtifactDir(sshPaths.Dir); err != nil {
			return "", fmt.Errorf("creating SSH dir: %w", err)
		}
		return sshPaths.Dir, nil
	}); err != nil {
		return err
	}

	// --- Phase 2: Generate SSH keypair ---
	if err := pr.PhaseDetailed("Generating SSH keypair", func() (string, error) {
		if !force {
			if _, err := os.Stat(sshPaths.PrivateKey); err == nil {
				ux.Debugf("SSH keypair already exists, skipping (use --force to regenerate)")
				return sshPaths.PrivateKey, nil
			}
		}

		pubKey, err := tart.GenerateSSHKeyPair(sshPaths.Dir)
		if err != nil {
			return "", fmt.Errorf("generating SSH keys: %w", err)
		}
		ux.Debugf("SSH public key: %s", pubKey)

		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			existing := tart.CollectSSHPubKeys(filepath.Join(homeDir, ".ssh"))
			if existing != "" {
				pubKey = strings.TrimSpace(pubKey) + "\n" + existing
				ux.Debugf("added existing ~/.ssh pub keys (%d total lines)",
					len(strings.Split(pubKey, "\n")))
			}
		}

		pubKeyPath := filepath.Join(sshPaths.Dir, "authorized_keys")
		if err := os.WriteFile(pubKeyPath, []byte(pubKey), 0644); err != nil {
			return "", fmt.Errorf("writing authorized_keys: %w", err)
		}
		ux.Debugf("wrote authorized_keys to %s", pubKeyPath)

		return sshPaths.PrivateKey, nil
	}); err != nil {
		return err
	}

	pr.Seal("tart artifacts ready")
	fmt.Println("  Run: cell build --engine=tart")
	return nil
}
