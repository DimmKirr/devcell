//go:build darwin || linux

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
	// Modules fork the template: a cell with extra nix modules gets different
	// guest contents, so it needs its own disk and its own provisioned marker.
	// Passing nil here collapsed every module set onto one template, where the
	// first build won and the rest silently reused or clobbered it.
	modules := cellCfg.Modules
	templateDir := qemu.TemplateDir(hostHome, stack, modules)
	templateDisk := filepath.Join(templateDir, qemu.ImageName(stack, modules))
	varsPath := filepath.Join(templateDir, "vars.fd")
	sshDir := qemuKeyDir(hostHome, cellName)
	privKeyPath := filepath.Join(sshDir, "id_ed25519")
	pubKeyPath := filepath.Join(sshDir, "id_ed25519.pub")
	marker := qemu.ProvisionedMarker(hostHome, stack, modules)

	ux.Debugf("build qemu: cell=%s stack=%s force=%v noCache=%v", cellName, stack, force, noCache)
	ux.Debugf("templateDir=%s templateDisk=%s", templateDir, templateDisk)

	budget := qemuBuildBudget(cellCfg)

	if dryRun {
		fmt.Printf("Would build Windows VM template: %s\n", qemu.TemplateVMName(stack, modules))
		fmt.Printf("  Stack: %s\n", stack)
		fmt.Printf("  Template disk: %s\n", templateDisk)
		fmt.Printf("  Accelerator: %s (%s)\n", budget.Accel, budget.AccelReason)
		fmt.Printf("  Memory: %d GB\n", budget.MemoryGB)
		fmt.Printf("  SSH deadline: %s\n", budget.SSHDeadline)
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

	// --- Phase 3b: OpenSSH release ---
	// Windows servicing cannot install OpenSSH Server from this media (the
	// capability is Staged with no payload, failing 0x80070002 even with
	// Windows Update reachable), so the standalone release ships with the
	// answer file. A download failure is not fatal: the bootstrap still tries
	// the capability, and the guest reports which path it took.
	var opensshPayload []byte
	if err := pr.PhaseDetailed("Fetching OpenSSH release", func() (string, error) {
		path, err := qemu.DownloadOpenSSH(ctx, hostHome, noCache, obs)
		if err != nil {
			ux.Debugf("OpenSSH release unavailable (%v) — bootstrap will fall back to the capability", err)
			return "unavailable, will fall back to Add-WindowsCapability", nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			ux.Debugf("reading OpenSSH payload: %v", readErr)
			return "unreadable, will fall back to Add-WindowsCapability", nil
		}
		opensshPayload = data
		return fmt.Sprintf("%s (%.1f MB)", path, float64(len(data))/(1024*1024)), nil
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
		// The guest's ComputerName was the literal "devcell-win" for every
		// template and every cell. Name it after the cell, the way Docker cells
		// are named, and honour the same override chain
		// (DEVCELL_HOSTNAME > [cell] hostname > computed).
		cfg.Hostname = cellCfg.ResolvedHostname(qemu.GuestHostname(cellName))
		// The template is what `cell rdp` connects to, and the host side
		// (port allocation, forwarding, discovery) already ships — RDP just
		// has to be on inside Windows (CELL-369).
		cfg.EnableRDP = true
		cfg.VirtIODrivers = qemu.NetKVMDriverPaths()
		if len(opensshPayload) > 0 {
			cfg.OpenSSHPayload = qemu.OpenSSHPayloadName
			cfg.OpenSSHPayloadData = opensshPayload
			cfg.OpenSSHPayloadSize = len(opensshPayload)
		}

		imgPath := filepath.Join(templateDir, "autounattend.img")
		if err := qemu.BuildAnswerVolume(cfg, imgPath); err != nil {
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

	// The two channels a guest can use before it has a network. Created up
	// front so the firmware's very first line is captured — by the time a boot
	// has failed, there is nothing left to attach to.
	debugDir := filepath.Join(baseDir, ".context", "debug")
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("creating debug dir: %w", err)
	}
	serialLog, guestProgressLog := qemuDiagnosticPaths(debugDir)
	ux.Debugf("serial log: %s", serialLog)
	ux.Debugf("guest progress log: %s", guestProgressLog)
	// Not just for --dry-run: the accelerator decides whether this build takes
	// 30 minutes or 3 hours, and a user watching a slow install deserves to see
	// which one they got without re-reading the plan.
	fmt.Printf("  Accelerator: %s (%s)\n", budget.Accel, budget.AccelReason)
	fmt.Printf("  Memory: %d GB\n", budget.MemoryGB)
	fmt.Printf("  SSH deadline: %s\n", budget.SSHDeadline)
	fmt.Printf("  Ports: %s\n", formatAllocatedPorts(ports))
	buildPorts := qemuBuildSpecPorts(ports)

	buildSpec := qemu.Spec{
		VMName:               "devcell-qemu-build",
		CPUs:                 4,
		SerialLogPath:        serialLog,
		GuestProgressLogPath: guestProgressLog,
		// Windows cells run WSL2/Hyper-V inside the guest, which needs more
		// than EL2: a GICv3 with ITS and a secure world. Set here so
		// `cell build --engine=qemu` produces the same machine the dev-env
		// pipeline is validated against.
		NestedVirt:   true,
		MemoryGB:     budget.MemoryGB,
		DiskPath:     templateDisk,
		FirmwarePath: firmwarePath,
		VarsPath:     varsPath,
		VirtioISO:    virtioISO,
		SSHPort:      buildPorts.SSHPort,
		VNCPort:      buildPorts.VNCPort,
		RDPPort:      buildPorts.RDPPort,
		SSHHost:      cellCfg.ResolvedQemuSSHHost(),
		SSHUser:      qemuBuildSSHUser(),
		SSHKeyPath:   privKeyPath,
		MACAddr:      qemu.DeterministicMAC("build-" + stack),
		DisplayType:  qemuBuildDisplay(cellCfg),
		QMPSocketDir: templateDir,
		KVM:          cellCfg.ResolvedKVM(),
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

	// Fail fast on a guest that never starts installing. Without this the
	// build waits out its whole deadline: a VM that booted the wrong device sat
	// at the UEFI prompt for five hours before the SSH wait gave up, with the
	// evidence — zero bytes written — available from the first minute.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		qmpSock := vm.QMPSockPath()
		start := time.Now()
		progress := &qemu.WriteProgressTracker{Window: installStallWindow}
		for {
			select {
			case <-screenshotStop:
				return
			case <-ticker.C:
				stats, err := qemu.QMPBlockStats(qmpSock)
				if err != nil {
					continue // the socket is not up yet, or the VM is gone
				}
				var written int64
				for _, s := range stats {
					written += s.WriteBytes
				}
				if progress.Observe(written, time.Since(start)) {
					ux.Debugf("install stalled: %s", progress.Reason())
					fmt.Printf("\n%s\n%s\n", ux.StyleSection.Render(" Install stalled"), progress.Reason())
					vm.ForceStop()
					return
				}
			}
		}
	}()

	if err := pr.PhaseDetailed("Waiting for SSH (Windows install + first boot)", func() (string, error) {
		ux.Debugf("waiting for SSH on %s:%d (deadline %s, accelerator %s)",
			buildSpec.SSHHost, buildSpec.SSHPort, budget.SSHDeadline, budget.Accel)
		if err := qemu.WaitForSSH(buildSpec.SSHHost, buildSpec.SSHPort, budget.SSHDeadline, 10*time.Second, obs, vm.State); err != nil {
			if lastOut := vm.LastOutput(); lastOut != "" {
				ux.Debugf("QEMU output at failure:\n%s", lastOut)
			}
			// The guest cannot talk to us, so the only account of what it did
			// is what it wrote to the answer volume in WinPE and at first
			// logon. Surface it here rather than making the caller go dig.
			dumpGuestLogs(autounattendISO)
			return "", fmt.Errorf("SSH not available after Windows install: %w", err)
		}
		return "SSH ready", nil
	}); err != nil {
		close(screenshotStop)
		stopVM()
		return err
	}
	close(screenshotStop)

	// SSH answering proves first logon happened (sshd only starts from
	// bootstrap's first-logon run), so OOBE is over. Windows auto-opens the
	// Start menu at that first sign-in and an unattended VM never sends the
	// input that would close it — it sits over every later screenshot. One
	// Esc dismisses it. Best-effort: screen cosmetics must not fail a build.
	if err := qemu.QMPDismissFirstLogonUI(vm.QMPSockPath()); err != nil {
		ux.Debugf("dismiss first-logon UI: %v", err)
	} else {
		ux.Debugf("OOBE finished (first logon reached) — sent Esc to close the Start menu")
	}

	// The guest reached us, but what it did before that is still only written
	// on the answer volume. Under --debug, print it: it is the record of the
	// unattended pass, the driver install and first-logon provisioning.
	if ux.Verbose {
		dumpGuestLogs(autounattendISO)
	}

	// --- Phase 11: Provision via SSH ---
	// One runner for all guest-side work (internal/vm/qemu.RunGuestStages):
	// retries, reboots, disconnect-tolerant stages, per-stage deadlines and
	// component logs streamed while they run. The dev-env test drives the same
	// function, so what it proves is what this command does — previously each
	// had its own loop and this one was the weaker.
	steps := qemu.DefaultProvisionSteps(pubKey, qemu.SessionUsername(), qemu.DefaultSessionUser)
	ux.Debugf("provisioning: %d steps via SSH", len(steps))

	if err := pr.PhaseDetailed(fmt.Sprintf("Provisioning (%d steps)", len(steps)), func() (string, error) {
		runErr := qemu.RunGuestStages(ctx, buildSpec, steps, qemu.StageRunOptions{
			SSHUser:    buildSpec.SSHUser,
			SSHKeyPath: buildSpec.SSHKeyPath,
			LogDir:     filepath.Dir(guestProgressLog),
			Observer:   obs,
			Reboot: func(ctx context.Context, reason string) error {
				ux.Debugf("guest reboot requested: %s", reason)
				return qemu.GuestReboot(ctx, buildSpec, buildSpec.SSHUser,
					buildSpec.SSHKeyPath, budget.SSHDeadline, obs, vm.State)
			},
		})
		if runErr != nil {
			return "", runErr
		}
		return fmt.Sprintf("%d steps", len(steps)), nil
	}); err != nil {
		stopVM()
		return err
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

	// --- Phase 14: Finalize dev environment (WSL2 + NixOS + nix + home-manager) ---
	// The same disk, rebooted on the EL3 machine (secure=on + kernel-loaded
	// relocatable firmware) — the only environment Windows' hypervisor, and
	// therefore WSL2, runs in (docs/spec/QEMU-ARM64-WINDOWS11-WSL2-NIX.md).
	// Both prerequisites are host artifacts; without them the build still
	// yields the classic ssh-able template, and says exactly what to install.
	kernelFW, fwErr := qemu.KernelFirmwarePath()
	fsdBin, fsdErr := qemu.VirtiofsdPath()
	if fwErr != nil || fsdErr != nil {
		if err := pr.PhaseDetailed("Finalizing dev environment", func() (string, error) {
			reason := fwErr
			if reason == nil {
				reason = fsdErr
			}
			return fmt.Sprintf("skipped — %v", reason), nil
		}); err != nil {
			return err
		}
		pr.Seal(fmt.Sprintf("qemu template %s built (ssh-able; dev-env finalization skipped)",
			qemu.TemplateVMName(stack, modules)))
		return nil
	}

	if err := runQemuDevEnvFinalize(ctx, pr, obs, buildSpec, kernelFW, fsdBin,
		hostHome, baseDir, stack, modules, budget.SSHDeadline); err != nil {
		return err
	}

	pr.Seal(fmt.Sprintf("qemu template %s built (WSL2 + nix + home-manager)", qemu.TemplateVMName(stack, modules)))
	return nil
}

// runQemuDevEnvFinalize boots the template on the EL3 machine and runs the
// production dev-env stage table, ending in a guest-clean power-off and the
// base-profile image save. Separated so the phase list above stays readable.
func runQemuDevEnvFinalize(ctx context.Context, pr *ux.PhaseRunner, obs qemu.Observer,
	buildSpec qemu.Spec, kernelFW, fsdBin, hostHome, baseDir, stack string,
	modules []string, sshDeadline time.Duration) error {

	const shareTag = "devcell"
	const shareDrive = "Z:"
	templateDir := qemu.TemplateDir(hostHome, stack, modules)
	debugDir := filepath.Join(baseDir, ".context", "debug")

	fin := qemu.FinalizeSpec(buildSpec, kernelFW)
	fin.VirtioFSSocketPath = filepath.Join(templateDir, "virtiofs.sock")
	fin.VirtioFSTag = shareTag
	fin.ApplyDefaults()
	if err := fin.Validate(); err != nil {
		return fmt.Errorf("finalize spec: %w", err)
	}

	// Host side of the project share. virtiofsd exits when its client
	// disconnects, so it belongs to exactly this VM boot.
	_ = os.Remove(fin.VirtioFSSocketPath)
	fsd := qemu.VirtiofsdCommand(fsdBin, fin.VirtioFSSocketPath, baseDir)
	fsdLog, err := os.OpenFile(filepath.Join(debugDir, "virtiofsd.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("virtiofsd log: %w", err)
	}
	defer fsdLog.Close()
	fsd.Stdout, fsd.Stderr = fsdLog, fsdLog
	if err := fsd.Start(); err != nil {
		return fmt.Errorf("starting virtiofsd: %w", err)
	}
	defer func() {
		if fsd.Process != nil {
			_ = fsd.Process.Kill()
		}
		_ = fsd.Wait()
	}()

	var vm *qemu.VM
	if err := pr.PhaseDetailed("Booting on the WSL2 machine (secure=on)", func() (string, error) {
		vm = qemu.NewVM(fin, obs, "")
		if err := vm.Start(ctx); err != nil {
			return "", fmt.Errorf("starting finalize VM: %w", err)
		}
		if err := qemu.WaitForSSH(fin.SSHHost, fin.SSHPort, sshDeadline,
			5*time.Second, obs, vm.State); err != nil {
			_ = vm.ForceStop()
			return "", err
		}
		return "EL3 machine up, SSH ready", nil
	}); err != nil {
		return err
	}
	defer func() { _ = vm.ForceStop() }()

	steps := qemu.DevEnvStages(qemu.SessionUsername(), shareTag, shareDrive)
	if err := pr.PhaseDetailed(fmt.Sprintf("Dev environment (%d stages)", len(steps)), func() (string, error) {
		runErr := qemu.RunGuestStages(ctx, fin, steps, qemu.StageRunOptions{
			SSHUser:    fin.SSHUser,
			SSHKeyPath: fin.SSHKeyPath,
			LogDir:     debugDir,
			Observer:   obs,
			Reboot: func(ctx context.Context, reason string) error {
				ux.Debugf("guest reboot requested: %s", reason)
				return qemu.GuestReboot(ctx, fin, fin.SSHUser, fin.SSHKeyPath,
					sshDeadline, obs, vm.State)
			},
		})
		if runErr != nil {
			return "", runErr
		}
		return fmt.Sprintf("%d stages", len(steps)), nil
	}); err != nil {
		return err
	}

	// Guest-clean power-off before the disk is copied: NTFS must be quiesced,
	// and a TCG guest can take up to 25 minutes to get there. SIGTERM-ing
	// QEMU here would trade a finished build for a dirty image.
	if err := pr.PhaseDetailed("Saving base-profile image", func() (string, error) {
		offArgv := qemu.BuildSSHExecArgv(fin.SSHHost, fin.SSHPort, fin.SSHUser, fin.SSHKeyPath,
			qemu.PowerShellEncodedCommand("Stop-Computer -Force"))
		_ = exec.CommandContext(ctx, offArgv[0], offArgv[1:]...).Run()
		deadline := time.Now().Add(25 * time.Minute)
		for vm.State() == qemu.StateRunning {
			if time.Now().After(deadline) {
				_ = vm.ForceStop()
				return "", fmt.Errorf("guest did not power off in 25m — not saving a dirty image")
			}
			time.Sleep(5 * time.Second)
		}
		dest := qemu.BaseProfileImagePath(hostHome, stack, modules)
		if err := qemu.SaveBaseProfileImage(buildSpec.DiskPath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}); err != nil {
		return err
	}
	return nil
}
