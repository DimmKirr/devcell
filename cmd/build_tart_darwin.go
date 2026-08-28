//go:build darwin && arm64

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
	"github.com/DimmKirr/devcell/internal/vm/tart"
)

// runBuildTart creates a fully provisioned macOS VM from an OCI image.
//
// Mirrors the Docker build flow: init scaffolds config/keys (no images),
// build creates and provisions the image. The VM is booted for provisioning
// and shut down when done — cell shell starts it again for the session.
func runBuildTart(cellName, hostHome, projectDir, stack string, modules []string, force, noCache, dryRun bool, tartOCIImage string) error {
	cfg := tart.BuildConfig{
		CellName: cellName,
		HomeDir:  hostHome,
		Stack:    stack,
		Modules:  modules,
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	templateName := tart.TemplateVMName(stack, modules)
	buildVM := "devcell-build-tmp"

	nixhomeRef := runner.ResolveNixhomeRef(version.Version)

	ux.Debugf("build config: cell=%s stack=%s cpus=%d mem=%dGB sshPort=%d",
		cfg.CellName, cfg.Stack, cfg.CPUs, cfg.MemoryGB, cfg.SSHPort)
	ux.Debugf("template: %s  buildVM: %s  force=%v noCache=%v", templateName, buildVM, force, noCache)
	ux.Debugf("nixhome: %s  projectDir: %s", nixhomeRef, projectDir)

	if dryRun {
		fmt.Printf("Would build macOS VM template: %s\n", templateName)
		fmt.Printf("  OCI image: %s\n", tartOCIImage)
		fmt.Printf("  Stack: %s\n", cfg.Stack)
		fmt.Printf("  CPUs: %d  Memory: %dGB\n", cfg.CPUs, cfg.MemoryGB)
		if len(cfg.Modules) > 0 {
			fmt.Printf("  Modules: %v\n", cfg.Modules)
		}
		return nil
	}

	ctx := context.Background()
	pr := &ux.PhaseRunner{}

	// --- Phase 1: Ensure SSH keys exist ---
	sshPaths := tart.NewCellSSHPaths(hostHome, cellName)
	if _, err := os.Stat(sshPaths.PrivateKey); err != nil {
		ux.Debugf("SSH key not found at %s — running auto-init", sshPaths.PrivateKey)
		fmt.Println(ux.StyleSection.Render(" SSH keys not found — running init"))
		if initErr := runInitTart(cellName, hostHome, projectDir, stack, false, false); initErr != nil {
			return fmt.Errorf("auto-init failed: %w", initErr)
		}
	}

	// Assemble the public key(s) to inject into the VM's authorized_keys.
	// Primary: the generated per-cell key. Additional: any existing ~/.ssh/*.pub.
	pubKeyBytes, err := os.ReadFile(sshPaths.PublicKey)
	if err != nil {
		return fmt.Errorf("reading SSH public key from %s: %w", sshPaths.PublicKey, err)
	}
	pubKey := strings.TrimSpace(string(pubKeyBytes))
	ux.Debugf("loaded SSH pub key from %s", sshPaths.PublicKey)

	if homeDir, _ := os.UserHomeDir(); homeDir != "" {
		if extra := tart.CollectSSHPubKeys(filepath.Join(homeDir, ".ssh")); extra != "" {
			pubKey = pubKey + "\n" + extra
			ux.Debugf("added existing ~/.ssh pub keys")
		}
	}

	// --- Platform compatibility preflight ---
	if err := pr.PhaseDetailed("Platform compatibility check", func() (string, error) {
		flakeRef := runner.ResolveNixhomeRef(version.Version)
		if err := runner.PreflightPlatformCheck(ctx, flakeRef, "aarch64-darwin"); err != nil {
			return "", err
		}
		return "aarch64-darwin OK", nil
	}); err != nil {
		return err
	}

	// --- Ensure nix store volume (global) ---
	var nixVolumePath string
	if err := pr.PhaseDetailed("Preparing nix store volume", func() (string, error) {
		path, err := tart.EnsureNixVolume(hostHome)
		if err != nil {
			return "", err
		}
		nixVolumePath = path
		return path, nil
	}); err != nil {
		return err
	}

	// --- Ensure home directory (per-cell, VirtioFS mount) ---
	var cellHome string
	if err := pr.PhaseDetailed("Preparing home directory", func() (string, error) {
		path, err := tart.EnsureHomeDir(hostHome, cellName)
		if err != nil {
			return "", err
		}
		cellHome = path
		return path, nil
	}); err != nil {
		return err
	}

	// --- Phase 2: Clone OCI image → build VM ---
	if err := pr.PhaseDetailed("Cloning VM from OCI image", func() (string, error) {
		if _, getErr := tart.TartGet(ctx, templateName); getErr == nil {
			if !force {
				return "", fmt.Errorf("template %s already exists — use --force to rebuild", templateName)
			}
			ux.Debugf("template %s exists, --force — deleting", templateName)
			_ = tart.TartStop(ctx, templateName)
			_ = tart.TartDelete(ctx, templateName)
		}

		// Clean up any leftover build VM from a previous interrupted build.
		if _, getErr := tart.TartGet(ctx, buildVM); getErr == nil {
			ux.Debugf("stale build VM %s found — deleting", buildVM)
			_ = tart.TartStop(ctx, buildVM)
			_ = tart.TartDelete(ctx, buildVM)
		}

		ux.Debugf("cloning %s → %s (noCache=%v)", tartOCIImage, buildVM, noCache)
		args := []string{"clone"}
		if noCache {
			args = append(args, "--no-cache")
		}
		args = append(args, tartOCIImage, buildVM)
		cmd := exec.CommandContext(ctx, "tart", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("tart clone: %w", err)
		}
		return buildVM, nil
	}); err != nil {
		return err
	}

	cleanupVM := func() {
		ux.Debugf("cleanup: deleting build VM %s", buildVM)
		_ = tart.TartDelete(ctx, buildVM)
	}

	// --- Phase 3: Configure resources ---
	if err := pr.PhaseDetailed("Configuring VM resources", func() (string, error) {
		memMB := cfg.MemoryGB * 1024
		ux.Debugf("tart set %s: cpu=%d memory=%dMB", buildVM, cfg.CPUs, memMB)
		if err := tart.TartSet(ctx, buildVM, cfg.CPUs, memMB); err != nil {
			return "", fmt.Errorf("setting VM resources: %w", err)
		}
		return fmt.Sprintf("%d CPUs, %dGB memory", cfg.CPUs, cfg.MemoryGB), nil
	}); err != nil {
		cleanupVM()
		return err
	}

	// --- Pre-boot diagnostics (host side) ---
	ux.Debugf("tart version: %s", tart.TartVersion(ctx))
	{
		helpOut, _ := exec.CommandContext(ctx, "tart", "run", "--help").CombinedOutput()
		ux.Debugf("tart run --help:\n%s", string(helpOut))
	}
	{
		getOut, _ := exec.CommandContext(ctx, "tart", "get", buildVM).CombinedOutput()
		ux.Debugf("tart get %s (pre-boot):\n%s", buildVM, string(getOut))
	}
	// --- Phase 4: Boot VM ---
	sharedDirs := map[string]string{
		"nixhome": nixhomeRef,
		"home":    cellHome,
	}
	disks := []string{nixVolumePath}
	ux.Debugf("booting VM %s with shared dirs: %v, disks: %v", buildVM, sharedDirs, disks)

	var vm *tart.VM
	if err := pr.PhaseDetailed("Booting VM", func() (string, error) {
		var bootErr error
		vm, bootErr = tart.TartRun(ctx, buildVM, sharedDirs, disks)
		if bootErr != nil {
			return "", fmt.Errorf("starting VM: %w", bootErr)
		}
		return buildVM, nil
	}); err != nil {
		cleanupVM()
		return err
	}

	stopVM := func() {
		ux.Debugf("stopping VM %s", buildVM)
		if err := vm.Stop(); err != nil {
			ux.Debugf("graceful stop failed: %v — forcing", err)
			vm.ForceStop()
		}
	}

	// --- Phase 5: Wait for guest agent ---
	if err := pr.PhaseDetailed("Waiting for guest agent", func() (string, error) {
		ux.Debugf("waiting for tart guest agent (timeout 120s)")
		if err := waitForGuestAgent(ctx, buildVM, 120*time.Second); err != nil {
			return "", err
		}
		return "ready", nil
	}); err != nil {
		stopVM()
		return err
	}

	// --- Phase 6: Bootstrap passwordless sudo ---
	if err := pr.PhaseDetailed("Bootstrapping passwordless sudo", func() (string, error) {
		bootstrapCmd := fmt.Sprintf(
			"echo '%s' | sudo -S sh -c \"mkdir -p /etc/sudoers.d && echo '%s ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/%s && chmod 440 /etc/sudoers.d/%s\"",
			tartImagePassword, tartImageUser, tartImageUser, tartImageUser,
		)
		ux.Debugf("bootstrap: configuring passwordless sudo for %s", tartImageUser)
		if err := tart.TartExec(ctx, buildVM, []string{"bash", "-l", "-c", bootstrapCmd}, os.Stdout, os.Stderr); err != nil {
			return "", fmt.Errorf("bootstrap sudo: %w", err)
		}
		return tartImageUser, nil
	}); err != nil {
		stopVM()
		return err
	}

	// --- Diagnostic: host-side post-boot checks ---
	{
		if tartErr := vm.Stderr(); tartErr != "" {
			ux.Debugf("tart run stderr (post-boot): %s", strings.TrimSpace(tartErr))
		} else {
			ux.Debugf("tart run stderr (post-boot): (empty — no errors)")
		}
		getOut, _ := exec.CommandContext(ctx, "tart", "get", buildVM).CombinedOutput()
		ux.Debugf("tart get %s (post-boot):\n%s", buildVM, string(getOut))
		listOut, _ := exec.CommandContext(ctx, "tart", "list").CombinedOutput()
		ux.Debugf("tart list:\n%s", string(listOut))
	}

	// --- Diagnostic: guest-side VirtioFS and IO checks ---
	{
		diagScript := `echo "=== GUEST DIAGNOSTICS ==="
echo "--- uname ---"
uname -a
echo "--- diskutil list ---"
diskutil list
echo "--- mount ---"
mount
echo "--- /Volumes/ listing ---"
ls -la /Volumes/ 2>&1
echo "--- /Volumes/My Shared Files/ ---"
ls -la "/Volumes/My Shared Files/" 2>&1 || echo "NOT FOUND"
echo "--- /Volumes/My Shared Files/nixhome/ ---"
ls -la "/Volumes/My Shared Files/nixhome/" 2>&1 | head -10 || echo "NOT FOUND"
echo "=== END GUEST DIAGNOSTICS ==="`
		var diagOut, diagErr strings.Builder
		diagCmd := exec.CommandContext(ctx, "tart", "exec", buildVM, "bash", "-l", "-c", diagScript)
		diagCmd.Stdout = &diagOut
		diagCmd.Stderr = &diagErr
		if err := diagCmd.Run(); err != nil {
			ux.Debugf("guest diagnostics failed: %v\nstdout: %s\nstderr: %s",
				err, diagOut.String(), diagErr.String())
		} else {
			ux.Debugf("guest diagnostics:\n%s", diagOut.String())
		}
	}

	// --- Phase 7: Full provisioning via tart exec ---
	initCfg := tart.InitConfig{
		CellName: cellName,
		HomeDir:  hostHome,
		Stack:    stack,
		Username: tartImageUser,
		Password: tartImagePassword,
	}
	initCfg.ApplyDefaults()
	steps := tart.ProvisionSteps(initCfg, pubKey, false)
	ux.Debugf("provisioning: %d steps via tart exec", len(steps))

	const reformatMarker = "DEVCELL_REFORMAT_NEEDED:"
	for i, step := range steps {
		stepName := step.Name
		stepIdx := i
		stepCmd := step.Command
		label := fmt.Sprintf("Provisioning (%d/%d): %s", stepIdx+1, len(steps), stepName)
		streamOutput := ux.Verbose && strings.HasPrefix(stepName, "Activate nix-darwin")
		var reformatVolLabel string // set by callback if volume needs reformatting
		err := pr.PhaseDetailed(label, func() (string, error) {
			ux.Debugf("provision [%d/%d] %s", stepIdx+1, len(steps), stepName)
			cmd := exec.CommandContext(ctx, "tart", "exec", buildVM, "bash", "-l", "-c", stepCmd)
			var stdout, stderr strings.Builder
			if streamOutput {
				cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
				cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
			} else {
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
			}
			if err := cmd.Run(); err != nil {
				outStr := strings.TrimSpace(stdout.String())
				ux.Debugf("provision [%d/%d] %s FAILED: %v\nstdout: %s\nstderr: %s",
					stepIdx+1, len(steps), stepName, err,
					outStr,
					strings.TrimSpace(stderr.String()))
				if idx := strings.Index(outStr, reformatMarker); idx >= 0 {
					rest := outStr[idx+len(reformatMarker):]
					reformatVolLabel = strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
					return "", fmt.Errorf("volume %q needs reformatting", reformatVolLabel)
				}
				errDetail := strings.TrimSpace(stderr.String())
				return "", fmt.Errorf("%s: %w (stderr: %s)", stepName, err, errDetail)
			}
			if out := strings.TrimSpace(stdout.String()); out != "" {
				ux.Debugf("provision [%d/%d] %s output: %s", stepIdx+1, len(steps), stepName, out)
			}
			ux.Debugf("provision [%d/%d] %s OK", stepIdx+1, len(steps), stepName)
			return "", nil
		})
		if err != nil && reformatVolLabel != "" {
			var imgPath string
			if reformatVolLabel == tart.NixVolumeLabel {
				imgPath = tart.NixVolumePath(hostHome)
			}
			msg := fmt.Sprintf(
				"Volume %q (%s) cannot be mounted — APFS encryption keys changed after VM rebuild.\nReformat? Previous contents will be lost.",
				reformatVolLabel, imgPath)
			confirmed, confirmErr := ux.GetConfirmation(msg)
			if confirmErr != nil {
				stopVM()
				return fmt.Errorf("confirmation prompt: %w", confirmErr)
			}
			if !confirmed {
				stopVM()
				return fmt.Errorf("volume %q needs reformatting — declined by user", reformatVolLabel)
			}
			retryLabel := fmt.Sprintf("Provisioning (%d/%d): %s (reformatting)", stepIdx+1, len(steps), stepName)
			if retryErr := pr.PhaseDetailed(retryLabel, func() (string, error) {
				reformatCmd := "export DEVCELL_ALLOW_REFORMAT=1; " + stepCmd
				cmd2 := exec.CommandContext(ctx, "tart", "exec", buildVM, "bash", "-l", "-c", reformatCmd)
				var stdout2, stderr2 strings.Builder
				cmd2.Stdout = &stdout2
				cmd2.Stderr = &stderr2
				if err2 := cmd2.Run(); err2 != nil {
					ux.Debugf("provision [%d/%d] %s reformat FAILED: %v\nstdout: %s\nstderr: %s",
						stepIdx+1, len(steps), stepName, err2,
						strings.TrimSpace(stdout2.String()),
						strings.TrimSpace(stderr2.String()))
					return "", fmt.Errorf("%s (reformat): %w (stderr: %s)", stepName, err2, strings.TrimSpace(stderr2.String()))
				}
				if out := strings.TrimSpace(stdout2.String()); out != "" {
					ux.Debugf("provision [%d/%d] %s reformat output: %s", stepIdx+1, len(steps), stepName, out)
				}
				ux.Debugf("provision [%d/%d] %s OK (after reformat)", stepIdx+1, len(steps), stepName)
				return "reformatted", nil
			}); retryErr != nil {
				stopVM()
				return retryErr
			}
			continue
		}
		if err != nil {
			stopVM()
			return err
		}
	}

	// --- Phase 7b: Stamp provisioned marker ---
	if err := pr.PhaseDetailed("Stamping provisioned marker", func() (string, error) {
		markerScript := tart.GenerateProvisionedMarkerScript()
		ux.Debugf("marker script: %s", markerScript)
		var stdout, stderr strings.Builder
		cmd := exec.CommandContext(ctx, "tart", "exec", buildVM, "bash", "-l", "-c", markerScript)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			ux.Debugf("failed to stamp provisioned marker: %v (stdout: %s) (stderr: %s)",
				err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
			return "", fmt.Errorf("stamp provisioned marker: %w", err)
		}
		ux.Debugf("provisioned marker stamped (stdout: %s)", strings.TrimSpace(stdout.String()))
		return "", nil
	}); err != nil {
		stopVM()
		return err
	}

	// --- Phase 7c: Verify marker + flush disk before shutdown ---
	if err := pr.PhaseDetailed("Verifying provisioned marker", func() (string, error) {
		verifyScript := fmt.Sprintf(`ls -la %s && stat -f '%%z bytes, %%m mtime' %s && sudo sync`,
			tart.ProvisionedMarkerPath, tart.ProvisionedMarkerPath)
		ux.Debugf("verify script: %s", verifyScript)
		var stdout, stderr strings.Builder
		cmd := exec.CommandContext(ctx, "tart", "exec", buildVM, "bash", "-l", "-c", verifyScript)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			ux.Debugf("marker verification FAILED: %v (stdout: %s) (stderr: %s)",
				err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
			return "", fmt.Errorf("provisioned marker missing after stamp: %w", err)
		}
		ux.Debugf("marker verified before shutdown: %s", strings.TrimSpace(stdout.String()))
		return "verified", nil
	}); err != nil {
		stopVM()
		return err
	}

	// --- Phase 8: Shutdown build VM ---
	if err := pr.PhaseDetailed("Shutting down build VM", func() (string, error) {
		ux.Debugf("shutting down build VM %s", buildVM)
		if err := vm.Stop(); err != nil {
			ux.Debugf("graceful shutdown failed: %v — forcing", err)
			vm.ForceStop()
		}
		return buildVM, nil
	}); err != nil {
		return err
	}

	// --- Phase 9: Save as template ---
	if err := pr.PhaseDetailed("Saving template image", func() (string, error) {
		ux.Debugf("cloning build VM %s → template %s", buildVM, templateName)
		if err := tart.TartClone(ctx, buildVM, templateName); err != nil {
			return "", fmt.Errorf("saving template: %w", err)
		}
		ux.Debugf("deleting build VM %s", buildVM)
		_ = tart.TartDelete(ctx, buildVM)
		return templateName, nil
	}); err != nil {
		return err
	}

	pr.Seal(fmt.Sprintf("tart template %s built", templateName))
	return nil
}

// waitForGuestAgent polls `tart exec` until the guest agent responds.
func waitForGuestAgent(ctx context.Context, vmName string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	var lastErr error
	var lastStderr string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("tart guest agent not ready after %s (last error: %v, stderr: %s)", timeout, lastErr, lastStderr)
		case <-ticker.C:
			var stderr strings.Builder
			cmd := exec.CommandContext(ctx, "tart", "exec", vmName, "echo", "ready")
			cmd.Stdout = nil
			cmd.Stderr = &stderr
			lastErr = cmd.Run()
			lastStderr = strings.TrimSpace(stderr.String())
			if lastErr == nil {
				ux.Debugf("guest agent responsive")
				return nil
			}
			ux.Debugf("guest agent not ready yet: %v (stderr: %s)", lastErr, lastStderr)
		}
	}
}
