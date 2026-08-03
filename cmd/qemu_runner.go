package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// runQemuAgent is the qemu-engine equivalent of runTartAgent.
//
// Lifecycle (managed Windows VM via QEMU):
//  1. Acquire VM (clone template or auto-build if missing)
//  2. Boot VM, wait for SSH
//  3. Exec into VM via SSH (PowerShell)
//
// On non-darwin with --debug: mock/simulate every step with [MOCK] prefix.
// On darwin with --debug: real execution with ux.Debugf logging.
func runQemuAgent(
	binary string,
	defaultFlags, userArgs []string,
	cellCfg cfg.CellConfig,
	baseDir, hostHome, cellName string,
	dryRun, background, debug bool,
) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if runtime.GOOS != "darwin" && !debug && !dryRun {
		return fmt.Errorf("qemu engine requires macOS (use --debug to simulate on %s)", runtime.GOOS)
	}
	mock := runtime.GOOS != "darwin" && !dryRun

	logf := func(format string, args ...any) {
		if mock {
			fmt.Printf("[MOCK %s]: %s\n", runtime.GOOS, fmt.Sprintf(format, args...))
		} else {
			ux.Debugf("qemu: "+format, args...)
		}
	}

	if mock {
		logf("runtime.GOOS=%s (not darwin) — entering mock mode", runtime.GOOS)
	}
	logf("binary=%q  defaultFlags=%v  userArgs=%v", binary, defaultFlags, userArgs)
	logf("cellName=%q  baseDir=%q  hostHome=%q", cellName, baseDir, hostHome)
	logf("background=%v  dryRun=%v  debug=%v", background, dryRun, debug)

	// --- env var assembly ---
	logf("assembling env vars to forward into VM")
	envVars := buildQemuEnvVars(cellCfg, cellName)
	for _, kv := range envVars {
		logf("  env: %s", kv)
	}

	// --- consume --force and --stack from userArgs (qemu-specific) ---
	force := false
	stackOverride := ""
	var filteredUserArgs []string
	for _, a := range userArgs {
		if a == "--force" {
			force = true
			continue
		}
		if strings.HasPrefix(a, "--stack=") {
			stackOverride = strings.TrimPrefix(a, "--stack=")
			continue
		}
		filteredUserArgs = append(filteredUserArgs, a)
	}
	userArgs = filteredUserArgs

	stack := cellCfg.Cell.ResolvedStack()
	if stackOverride != "" {
		logf("--stack=%s overrides resolved stack %q", stackOverride, stack)
		stack = stackOverride
	}

	// --- resolve paths ---
	instanceDir := qemu.InstanceDir(hostHome, cellName)
	templateDir := qemu.TemplateDir(hostHome, stack, cellCfg.Cell.Modules)
	templateDisk := filepath.Join(templateDir, qemu.ImageName(stack, cellCfg.Cell.Modules))
	instanceDisk := filepath.Join(instanceDir, "disk.qcow2")
	varsPath := filepath.Join(instanceDir, "vars.fd")
	sshKeyPath := filepath.Join(qemuKeyDir(hostHome, cellName), "id_ed25519")

	// Port allocation — same bunk-based scheme as Docker runner (CELL-352)
	c := config.Load(baseDir, os.Getenv)
	taken := config.DockerAllocatedPorts()
	ports := qemu.AllocatePorts(c.PortPrefix, taken)

	sshPort := ports.SSHPortUint16()
	if cellCfg.Cell.QemuSSHPort > 0 || os.Getenv("DEVCELL_QEMU_SSH_PORT") != "" {
		sshPort = uint16(cellCfg.Cell.ResolvedQemuSSHPort())
	}

	spec := qemu.Spec{
		VMName:       qemu.InstanceVMName(cellName),
		CPUs:         uint(cellCfg.Cell.ResolvedQemuCPUs()),
		MemoryGB:     uint64(cellCfg.Cell.ResolvedQemuMemoryGB()),
		DiskPath:     instanceDisk,
		FirmwarePath: qemu.FirmwarePath(),
		VarsPath:     varsPath,
		SSHPort:      sshPort,
		VNCPort:      ports.VNCPortUint16(),
		RDPPort:      ports.RDPPortUint16(),
		SSHHost:      cellCfg.Cell.ResolvedQemuSSHHost(),
		SSHUser:      "devcell",
		SSHKeyPath:   sshKeyPath,
		MACAddr:      qemu.DeterministicMAC(cellName),
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		EnvVars:      envVars,
		ProjectDir:   baseDir,
		DisplayType:  cellCfg.Cell.ResolvedQemuDisplay(),
		QMPSocketDir: instanceDir,
		KVM:          cellCfg.Cell.ResolvedKVM(),
	}
	spec.ApplyDefaults()

	logf("templateDir=%s instanceDir=%s", templateDir, instanceDir)
	logf("templateDisk=%s instanceDisk=%s", templateDisk, instanceDisk)
	logf("spec: cpus=%d mem=%dGB ssh=%s:%d vnc=%d rdp=%d display=%s", spec.CPUs, spec.MemoryGB, spec.SSHHost, spec.SSHPort, spec.VNCPort, spec.RDPPort, spec.DisplayType)
	logf("accel: %s — %s", spec.Accel, spec.AccelReason)

	// --- lifecycle: acquire VM (real or mock) ---
	if !dryRun && !mock {
		if force {
			logf("--force: removing existing instance disk and vars")
			os.Remove(instanceDisk)
			os.Remove(varsPath)
		}

		// Detect managed VM: PID file + QMP state query
		qemu.CleanStalePIDFile(instanceDir)
		vmRunning := false
		if pid, err := qemu.ReadPIDFile(instanceDir); err == nil {
			qmpSock := qemu.QMPSocketPath(spec)
			if state, err := qemu.QueryVMState(qmpSock); err == nil && state == qemu.StateRunning {
				logf("detected running VM (PID %d, QMP=%s)", pid, state)
				vmRunning = true
			}
		}

		_, diskErr := os.Stat(instanceDisk)
		_, tplErr := os.Stat(templateDisk)
		actions := qemu.DecideLaunchActions(qemu.LaunchInputs{
			ExplicitBuild:  force,
			DiskExists:     diskErr == nil,
			TemplateExists: tplErr == nil,
			VMRunning:      vmRunning,
		})
		logf("launch actions: %v", actions)

		attachMode := false
		for _, action := range actions {
			switch action {
			case qemu.ActionAttach:
				logf("attaching to running VM (skipping boot)")
				attachMode = true

			case qemu.ActionBuild:
				logf("auto-build: template not found — running build with stack=%q", stack)
				if err := runBuildQemu(cellName, hostHome, baseDir, stack, force, false, false, cellCfg.Cell); err != nil {
					return fmt.Errorf("auto-build failed: %w", err)
				}
				if err := os.MkdirAll(instanceDir, 0755); err != nil {
					return fmt.Errorf("creating instance dir: %w", err)
				}
				if err := qemu.CloneDisk(templateDisk, instanceDisk); err != nil {
					return fmt.Errorf("cloning template disk: %w", err)
				}
				if err := qemu.PrepareVarsFile(spec.FirmwarePath, varsPath); err != nil {
					return fmt.Errorf("preparing UEFI vars: %w", err)
				}
				logf("instance disk cloned from template")

			case qemu.ActionClone:
				logf("cloning template to instance")
				if err := os.MkdirAll(instanceDir, 0755); err != nil {
					return fmt.Errorf("creating instance dir: %w", err)
				}
				if err := qemu.CloneDisk(templateDisk, instanceDisk); err != nil {
					return fmt.Errorf("cloning template disk: %w", err)
				}
				if err := qemu.PrepareVarsFile(spec.FirmwarePath, varsPath); err != nil {
					return fmt.Errorf("preparing UEFI vars: %w", err)
				}
				logf("instance disk cloned from template")

			case qemu.ActionUseLocal:
				logf("using existing instance disk: %s", instanceDisk)
				if _, err := os.Stat(varsPath); err != nil {
					logf("vars file missing — preparing from firmware")
					if err := qemu.PrepareVarsFile(spec.FirmwarePath, varsPath); err != nil {
						return fmt.Errorf("preparing UEFI vars: %w", err)
					}
				}
			}
		}

		if !attachMode {
			// Check provisioned marker
			marker := qemu.ProvisionedMarker(hostHome, stack, cellCfg.Cell.Modules)
			if _, err := os.Stat(marker); err != nil {
				return fmt.Errorf("VM template not provisioned — run `cell build --engine=qemu`")
			}
			logf("provisioned marker verified: %s", marker)

			// Boot VM
			vm := qemu.NewVM(spec, qemu.NopObserver{}, instanceDir)
			logf("starting QEMU VM: %s", spec.VMName)
			if err := vm.Start(ctx); err != nil {
				return fmt.Errorf("starting QEMU: %w", err)
			}
			defer func() {
				logf("shutting down QEMU VM")
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := vm.Shutdown(ctx); err != nil {
					logf("graceful shutdown failed: %v — force stopping", err)
					vm.ForceStop()
				}
			}()
		}

		// Write port metadata for discovery by cell vnc/rdp (CELL-352)
		if err := qemu.WritePortMeta(instanceDir, qemu.PortMeta{
			SSHPort: spec.SSHPort,
			VNCPort: spec.VNCPort,
			RDPPort: spec.RDPPort,
		}); err != nil {
			logf("warning: failed to write port metadata: %v", err)
		}

		// Wait for SSH (both attach and fresh boot need this)
		logf("waiting for SSH on %s:%d", spec.SSHHost, spec.SSHPort)
		if err := qemu.WaitForSSH(spec.SSHHost, spec.SSHPort, 5*time.Minute, 3*time.Second, qemu.NopObserver{}); err != nil {
			return fmt.Errorf("waiting for SSH: %w", err)
		}
		logf("SSH ready")
	}

	// --- mock mode ---
	if mock {
		logf("[qemu] preflight: GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH)
		logf("[qemu] would boot QEMU VM and connect via SSH")
		logf("[qemu] guest SSH ready (simulated)")
	}

	// --- build SSH command ---
	sshArgv := qemu.BuildSSHArgv(spec)
	logf("ssh command: %s", strings.Join(sshArgv, " "))

	if dryRun {
		fmt.Printf("%s\n", strings.Join(sshArgv, " "))
		return nil
	}

	if mock {
		logf("would exec: %s", strings.Join(sshArgv, " "))
		logf("skipping exec (mock mode)")
		return nil
	}

	// --- project sync (CELL-383) ---
	syncMode := cellCfg.Cell.ResolvedQemuProjectSync()
	if syncMode != "off" {
		if err := runProjectSync(qemu.BuildProjectPushArgv(spec), "pushing project into guest"); err != nil {
			return err
		}
	}

	// --- exec SSH into VM ---
	logf("connecting via SSH...")
	cmd := exec.Command(sshArgv[0], sshArgv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	if syncMode == "two-way" {
		if err := runProjectSync(qemu.BuildProjectPullArgv(spec), "pulling project back from guest"); err != nil {
			logf("%v", err)
		}
	}

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return runErr
	}
	return nil
}

// buildQemuEnvVars collects env vars to forward into the Windows VM via SSH.
func buildQemuEnvVars(cellCfg cfg.CellConfig, cellName string) []string {
	var envs []string
	e := func(k, v string) {
		if v != "" {
			envs = append(envs, k+"="+v)
		}
	}

	e("TERM", os.Getenv("TERM"))
	e("DEVCELL_CELL_NAME", cellName)

	gitCfg := cellCfg.Git
	hostGitEnv := os.Getenv("GIT_AUTHOR_NAME") != "" ||
		os.Getenv("GIT_AUTHOR_EMAIL") != "" ||
		os.Getenv("GIT_COMMITTER_NAME") != "" ||
		os.Getenv("GIT_COMMITTER_EMAIL") != ""
	if hostGitEnv {
		e("GIT_AUTHOR_NAME", os.Getenv("GIT_AUTHOR_NAME"))
		e("GIT_AUTHOR_EMAIL", os.Getenv("GIT_AUTHOR_EMAIL"))
		e("GIT_COMMITTER_NAME", os.Getenv("GIT_COMMITTER_NAME"))
		e("GIT_COMMITTER_EMAIL", os.Getenv("GIT_COMMITTER_EMAIL"))
	} else if gitCfg.HasIdentity() {
		e("GIT_AUTHOR_NAME", gitCfg.AuthorName)
		e("GIT_AUTHOR_EMAIL", gitCfg.AuthorEmail)
		e("GIT_COMMITTER_NAME", gitCfg.ResolvedCommitterName())
		e("GIT_COMMITTER_EMAIL", gitCfg.ResolvedCommitterEmail())
	} else {
		if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
			e("GIT_AUTHOR_NAME", trimNL(string(out)))
			e("GIT_COMMITTER_NAME", trimNL(string(out)))
		}
		if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
			e("GIT_AUTHOR_EMAIL", trimNL(string(out)))
			e("GIT_COMMITTER_EMAIL", trimNL(string(out)))
		}
	}

	tz := cellCfg.Cell.Timezone
	if tz == "" {
		tz = os.Getenv("TZ")
	}
	e("TZ", tz)

	locale := cellCfg.Cell.Locale
	if locale == "" {
		locale = os.Getenv("LANG")
	}
	e("LANG", locale)

	return envs
}
