//go:build darwin && arm64

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// runBuildQemu creates a fully provisioned Windows VM template via QEMU.
//
// Mirrors the tart build flow: init scaffolds config/keys, build creates and
// provisions the template image. The VM is booted for Windows installation +
// provisioning and shut down when done — cell shell clones and starts it again.
func runBuildQemu(cellName, hostHome, baseDir, stack string, force, noCache, dryRun bool, cellCfg cfg.CellSection) error {
	templateDir := qemu.TemplateDir(hostHome, stack, nil)
	templateDisk := filepath.Join(templateDir, qemu.ImageName(stack, nil))
	varsPath := filepath.Join(templateDir, "vars.fd")
	sshDir := filepath.Join(hostHome, ".devcell", cellName, "qemu")
	privKeyPath := filepath.Join(sshDir, "id_ed25519")
	pubKeyPath := filepath.Join(sshDir, "id_ed25519.pub")
	marker := qemu.ProvisionedMarker(hostHome, stack, nil)

	ux.Debugf("build qemu: cell=%s stack=%s force=%v noCache=%v", cellName, stack, force, noCache)
	ux.Debugf("templateDir=%s templateDisk=%s", templateDir, templateDisk)

	if dryRun {
		fmt.Printf("Would build Windows VM template: %s\n", qemu.TemplateVMName(stack, nil))
		fmt.Printf("  Stack: %s\n", stack)
		fmt.Printf("  Template disk: %s\n", templateDisk)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	pr := &ux.PhaseRunner{}
	obs := &phaseObserver{logf: ux.Debugf, runner: pr}

	// --- Phase 1: Ensure SSH keys exist ---
	if _, err := os.Stat(privKeyPath); err != nil {
		ux.Debugf("SSH key not found — running auto-init")
		fmt.Println(ux.StyleSection.Render(" SSH keys not found — running init"))
		if initErr := runInitQemu(cellName, hostHome, stack, false); initErr != nil {
			return fmt.Errorf("auto-init failed: %w", initErr)
		}
	}

	pubKeyBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("reading SSH public key: %w", err)
	}
	pubKey := strings.TrimSpace(string(pubKeyBytes))
	ux.Debugf("loaded SSH pub key from %s", pubKeyPath)

	if homeDir, _ := os.UserHomeDir(); homeDir != "" {
		if extra := collectSSHPubKeys(filepath.Join(homeDir, ".ssh")); extra != "" {
			pubKey = pubKey + "\n" + extra
			ux.Debugf("added existing ~/.ssh pub keys")
		}
	}

	// --- Phase 2: Check existing template ---
	if _, err := os.Stat(templateDisk); err == nil {
		if !force {
			return fmt.Errorf("template %s already exists — use --force to rebuild", templateDisk)
		}
		ux.Debugf("template exists, --force — removing")
		os.Remove(templateDisk)
		os.Remove(varsPath)
		os.Remove(marker)
	}

	// --- Phase 3: Download VirtIO drivers ---
	var virtioISO string
	if err := pr.PhaseDetailed("Downloading VirtIO drivers", func() (string, error) {
		path, err := qemu.DownloadVirtioDrivers(ctx, hostHome, noCache, obs)
		if err != nil {
			return "", err
		}
		virtioISO = path
		return path, nil
	}); err != nil {
		return err
	}

	// --- Phase 4: Ensure Windows ISO ---
	var windowsISO string
	if err := pr.PhaseDetailed("Ensuring Windows ISO", func() (string, error) {
		if envISO := os.Getenv("DEVCELL_QEMU_WINDOWS_ISO"); envISO != "" {
			if _, err := os.Stat(envISO); err != nil {
				return "", fmt.Errorf("Windows ISO not found at %s: %w", envISO, err)
			}
			if err := qemu.ValidateISO(envISO); err != nil {
				return "", fmt.Errorf("invalid ISO at %s: %w", envISO, err)
			}
			windowsISO = envISO
		} else {
			path, err := qemu.DownloadWindowsISO(ctx, hostHome, "en-us", noCache, obs)
			if err != nil {
				return "", err
			}
			windowsISO = path
		}
		meta := qemu.ParseISOFilename(filepath.Base(windowsISO))
		detail := windowsISO
		if meta.Version != "" {
			detail = fmt.Sprintf("%s (version %s, %s)", windowsISO, meta.Version, meta.Arch)
		}
		return detail, nil
	}); err != nil {
		return err
	}

	// --- Phase 5: Preflight check ---
	if err := pr.PhaseDetailed("QEMU preflight check", func() (string, error) {
		if err := qemu.PreflightCheckHost(); err != nil {
			return "", err
		}
		binPath, err := qemu.QEMUBinaryPath()
		if err != nil {
			return "", err
		}
		ver, _ := qemu.QEMUVersion(binPath)
		accel := qemu.Accelerator()
		return fmt.Sprintf("QEMU %s (%s)", ver, accel), nil
	}); err != nil {
		return err
	}

	// --- Phase 6: Create template disk ---
	diskSizeGB := 64
	if err := pr.PhaseDetailed("Creating template disk", func() (string, error) {
		if err := os.MkdirAll(templateDir, 0755); err != nil {
			return "", fmt.Errorf("creating template dir: %w", err)
		}
		if err := qemu.CreateDisk(templateDisk, diskSizeGB); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s (%dGB)", templateDisk, diskSizeGB), nil
	}); err != nil {
		return err
	}

	// --- Phase 7: Prepare UEFI firmware vars ---
	firmwarePath := qemu.FirmwarePath()
	if err := pr.PhaseDetailed("Preparing UEFI firmware", func() (string, error) {
		if _, err := os.Stat(firmwarePath); err != nil {
			return "", fmt.Errorf("EDK2 UEFI firmware not found at %s — install QEMU (brew install qemu)", firmwarePath)
		}
		if err := qemu.PrepareVarsFile(firmwarePath, varsPath); err != nil {
			return "", err
		}
		return firmwarePath, nil
	}); err != nil {
		return err
	}

	// --- Phase 8: Generate autounattend ISO ---
	var autounattendISO string
	if err := pr.PhaseDetailed("Generating autounattend ISO", func() (string, error) {
		cfg := qemu.DefaultAutounattendConfig()
		cfg.SSHPubKey = pubKey

		xmlBytes := qemu.GenerateAutounattendXML(cfg)

		imgPath := filepath.Join(templateDir, "autounattend.img")
		if err := qemu.WriteAutounattendImage(xmlBytes, imgPath); err != nil {
			return "", fmt.Errorf("writing autounattend image: %w", err)
		}
		autounattendISO = imgPath
		return imgPath, nil
	}); err != nil {
		return err
	}

	// --- Phase 9: Install Windows ---
	c := config.Load(baseDir, os.Getenv)
	taken := config.DockerAllocatedPorts()
	ports := qemu.AllocatePorts(c.PortPrefix, taken)

	buildSpec := qemu.Spec{
		VMName:       "devcell-qemu-build",
		CPUs:         4,
		MemoryGB:     4,
		DiskPath:     templateDisk,
		FirmwarePath: firmwarePath,
		VarsPath:     varsPath,
		VirtioISO:    virtioISO,
		SSHPort:      ports.SSHPortUint16(),
		SSHHost:      cellCfg.ResolvedQemuSSHHost(),
		SSHUser:      "devcell",
		SSHKeyPath:   privKeyPath,
		MACAddr:      qemu.DeterministicMAC("build-" + stack),
		DisplayType:  "none",
		QMPSocketDir: templateDir,
	}
	buildSpec.ApplyDefaults()

	var vm *qemu.VM
	if err := pr.PhaseDetailed("Installing Windows (this may take 20-40 minutes)", func() (string, error) {
		vm = qemu.NewVM(buildSpec, obs, "")
		if err := vm.StartInstall(ctx, windowsISO, autounattendISO); err != nil {
			return "", fmt.Errorf("starting Windows install: %w", err)
		}
		return "VM started, waiting for install to complete", nil
	}); err != nil {
		return err
	}

	stopVM := func() {
		ux.Debugf("stopping build VM")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if err := vm.Shutdown(shutCtx); err != nil {
			ux.Debugf("graceful shutdown failed: %v — forcing", err)
			vm.ForceStop()
		}
	}

	go func() {
		select {
		case <-sigCh:
			ux.Debugf("caught signal — stopping build VM")
			stopVM()
			cancel()
		case <-ctx.Done():
		}
	}()

	// --- Phase 10: Wait for SSH (installation + first-boot + SSH setup) ---
	// Capture periodic screenshots via QMP while waiting for Windows install
	screenshotDir := filepath.Join(baseDir, ".context", "debug", "screenshots")
	os.MkdirAll(screenshotDir, 0755)
	screenshotStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		qmpSock := vm.QMPSockPath()
		for {
			select {
			case <-screenshotStop:
				return
			case <-ticker.C:
				ts := time.Now().UTC().Format("20060102T150405Z")
				ppmFile := filepath.Join(screenshotDir, ts+".ppm")
				if err := qemu.QMPScreendump(qmpSock, ppmFile); err != nil {
					ux.Debugf("screenshot failed: %v", err)
					continue
				}
				pngFile := filepath.Join(screenshotDir, ts+".png")
				if err := qemu.ConvertPPMtoPNG(ppmFile, pngFile); err != nil {
					ux.Debugf("PPM→PNG conversion failed: %v", err)
				} else {
					os.Remove(ppmFile)
					ux.Debugf("screenshot saved: %s", pngFile)
				}
			}
		}
	}()

	if err := pr.PhaseDetailed("Waiting for SSH (Windows install + first boot)", func() (string, error) {
		ux.Debugf("waiting for SSH on %s:%d (timeout 45m for Windows install)", buildSpec.SSHHost, buildSpec.SSHPort)
		if err := qemu.WaitForSSH(buildSpec.SSHHost, buildSpec.SSHPort, 45*time.Minute, 10*time.Second, obs, vm.State); err != nil {
			if lastOut := vm.LastOutput(); lastOut != "" {
				ux.Debugf("QEMU output at failure:\n%s", lastOut)
			}
			return "", fmt.Errorf("SSH not available after Windows install: %w", err)
		}
		return "SSH ready", nil
	}); err != nil {
		close(screenshotStop)
		stopVM()
		return err
	}
	close(screenshotStop)

	// --- Phase 11: Provision via SSH ---
	steps := qemu.DefaultProvisionSteps(pubKey, "devcell", "devcell")
	ux.Debugf("provisioning: %d steps via SSH", len(steps))

	for i, step := range steps {
		stepName := step.Name
		stepIdx := i
		label := fmt.Sprintf("Provisioning (%d/%d): %s", stepIdx+1, len(steps), stepName)
		if err := pr.PhaseDetailed(label, func() (string, error) {
			ux.Debugf("provision [%d/%d] %s", stepIdx+1, len(steps), stepName)

			var lastErr error
			for attempt := 0; attempt <= step.Retries; attempt++ {
				sshArgv := qemu.BuildSSHExecArgv(
					buildSpec.SSHHost, buildSpec.SSHPort,
					buildSpec.SSHUser, buildSpec.SSHKeyPath,
					fmt.Sprintf("powershell -NoProfile -Command '%s'", escapePowerShellForSSH(step.Script)),
				)
				cmd := exec.CommandContext(ctx, sshArgv[0], sshArgv[1:]...)
				var stdout, stderr strings.Builder
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				lastErr = cmd.Run()
				if lastErr == nil {
					if out := strings.TrimSpace(stdout.String()); out != "" {
						ux.Debugf("provision [%d/%d] %s output: %s", stepIdx+1, len(steps), stepName, out)
					}
					ux.Debugf("provision [%d/%d] %s OK", stepIdx+1, len(steps), stepName)
					return "", nil
				}
				ux.Debugf("provision [%d/%d] %s attempt %d/%d failed: %v (stderr: %s)",
					stepIdx+1, len(steps), stepName, attempt+1, step.Retries+1, lastErr,
					strings.TrimSpace(stderr.String()))
				if attempt < step.Retries {
					time.Sleep(5 * time.Second)
				}
			}
			return "", fmt.Errorf("%s: %w", stepName, lastErr)
		}); err != nil {
			stopVM()
			return err
		}
	}

	// --- Phase 12: Stamp provisioned marker ---
	if err := pr.PhaseDetailed("Stamping provisioned marker", func() (string, error) {
		if err := os.WriteFile(marker, []byte("provisioned\n"), 0644); err != nil {
			return "", fmt.Errorf("writing marker: %w", err)
		}
		ux.Debugf("provisioned marker: %s", marker)
		return marker, nil
	}); err != nil {
		stopVM()
		return err
	}

	// --- Phase 13: Shutdown ---
	if err := pr.PhaseDetailed("Shutting down build VM", func() (string, error) {
		stopVM()
		return "shutdown complete", nil
	}); err != nil {
		return err
	}

	pr.Seal(fmt.Sprintf("qemu template %s built", qemu.TemplateVMName(stack, nil)))
	return nil
}

// escapePowerShellForSSH escapes single quotes in a PowerShell script for SSH transport.
func escapePowerShellForSSH(script string) string {
	return strings.ReplaceAll(script, "'", "''")
}
