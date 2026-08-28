package main

import (
	"context"
	"fmt"
	"io"
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
	"github.com/DimmKirr/devcell/internal/vm/libvirt"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// DefaultLibvirtFirmware is the brew edk2 firmware path on the macOS host.
// The CLI never opens this file — it only lands in the domain XML — so it is
// a host path by definition. Override: DEVCELL_LIBVIRT_FIRMWARE.
const DefaultLibvirtFirmware = "/opt/homebrew/share/qemu/edk2-aarch64-code.fd"

// runLibvirtAgent boots a prepped Windows template on the machine behind
// libvirtd and execs into it over SSH (CELL-377).
//
// Unlike tart/qemu there is no platform stub: this path is designed to run
// inside a Linux cell, driving QEMU+HVF on the macOS host through
// qemu+tcp://host.docker.internal/session. Template building stays on
// `cell build --engine=qemu` (macOS host).
func runLibvirtAgent(
	binary string,
	defaultFlags, userArgs []string,
	cellCfg cfg.CellConfig,
	baseDir, hostHome, cellName string,
	dryRun, background, debug bool,
) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	uri := cellCfg.Cell.ResolvedLibvirtURI()
	firmware := os.Getenv("DEVCELL_LIBVIRT_FIRMWARE")
	if firmware == "" {
		firmware = DefaultLibvirtFirmware
	}

	pathMap := libvirtPathMap(cellCfg, firmware)

	stack := cellCfg.Cell.ResolvedStack()
	instanceDir := qemu.InstanceDir(hostHome, cellName)
	templateDir := qemu.TemplateDir(hostHome, stack, cellCfg.Cell.Modules)
	templateDisk := filepath.Join(templateDir, qemu.ImageName(stack, cellCfg.Cell.Modules))
	instanceDisk := filepath.Join(instanceDir, "disk.qcow2")
	varsPath := filepath.Join(instanceDir, "vars.fd")
	sshKeyPath := filepath.Join(hostHome, ".devcell", cellName, "qemu", "id_ed25519")

	c := config.Load(baseDir, os.Getenv)
	ports := qemu.AllocatePorts(c.PortPrefix, config.DockerAllocatedPorts())
	sshPort := ports.SSHPortUint16()
	if cellCfg.Cell.QemuSSHPort > 0 || os.Getenv("DEVCELL_QEMU_SSH_PORT") != "" {
		sshPort = uint16(cellCfg.Cell.ResolvedQemuSSHPort())
	}

	spec := qemu.Spec{
		VMName:       qemu.InstanceVMName(cellName),
		CPUs:         uint(cellCfg.Cell.ResolvedQemuCPUs()),
		MemoryGB:     uint64(cellCfg.Cell.ResolvedQemuMemoryGB()),
		DiskPath:     instanceDisk,
		FirmwarePath: firmware,
		VarsPath:     varsPath,
		SSHPort:      sshPort,
		VNCPort:      ports.VNCPortUint16(),
		RDPPort:      ports.RDPPortUint16(),
		SSHUser:      "devcell",
		SSHKeyPath:   sshKeyPath,
		MACAddr:      qemu.DeterministicMAC(cellName),
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		EnvVars:      buildQemuEnvVars(cellCfg, cellName),
		ProjectDir:   baseDir,
		DisplayType:  "none",
		Accel:        "hvf", // the VM runs on the macOS host regardless of where the CLI runs
	}

	engine := libvirt.NewEngine(uri, spec, pathMap)

	if dryRun {
		xml, err := engine.DomainXML()
		if err != nil {
			return fmt.Errorf("rendering domain XML: %w", err)
		}
		fmt.Println("libvirt engine (dry-run)")
		fmt.Printf("  URI:    %s\n", uri)
		fmt.Printf("  binary: %s\n", binary)
		fmt.Printf("  cell:   %s\n", cellName)
		fmt.Printf("%s\n", xml)
		fmt.Printf("%s\n", strings.Join(engine.SSHArgv(binary, defaultFlags, userArgs), " "))
		return nil
	}

	ux.Debugf("libvirt: preflight %s", uri)
	if err := libvirt.Preflight(ctx, uri); err != nil {
		return err
	}

	// --- acquire instance disk (files live on the shared mount) ---
	marker := qemu.ProvisionedMarker(hostHome, stack, cellCfg.Cell.Modules)
	if _, err := os.Stat(marker); err != nil {
		return fmt.Errorf("VM template not provisioned — run `cell build --engine=qemu` on the macOS host first (libvirt mode boots prepped templates only)")
	}
	if _, err := os.Stat(instanceDisk); err != nil {
		ux.Debugf("libvirt: cloning template disk %s → %s", templateDisk, instanceDisk)
		if err := qemu.CloneDisk(templateDisk, instanceDisk); err != nil {
			return fmt.Errorf("cloning template disk: %w", err)
		}
	}
	if _, err := os.Stat(varsPath); err != nil {
		// The CLI cannot read the host's firmware to seed a fresh var store;
		// copy the template's vars.fd, which `cell build --engine=qemu` left
		// on the shared mount.
		if err := copyFile(filepath.Join(templateDir, "vars.fd"), varsPath); err != nil {
			return fmt.Errorf("copying template UEFI vars: %w", err)
		}
	}

	if err := qemu.WritePortMeta(instanceDir, qemu.PortMeta{
		SSHPort: spec.SSHPort,
		VNCPort: spec.VNCPort,
		RDPPort: spec.RDPPort,
	}); err != nil {
		ux.Debugf("libvirt: warning: failed to write port metadata: %v", err)
	}

	ux.Debugf("libvirt: booting %s via %s", spec.VMName, uri)
	if err := engine.Boot(ctx); err != nil {
		return fmt.Errorf("booting domain via libvirt: %w", err)
	}
	if !background && !cellCfg.Cell.ResolvedBackground() {
		defer func() {
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer shutCancel()
			if err := engine.Shutdown(shutCtx); err != nil {
				ux.Debugf("libvirt: shutdown: %v", err)
			}
		}()
	}

	// Project sync (CELL-383): the guest's ~\<project> is otherwise empty.
	syncMode := cellCfg.Cell.ResolvedQemuProjectSync()
	syncSpec := spec
	syncSpec.SSHHost = engine.SSHHost()
	if syncMode != "off" {
		if err := runProjectSync(qemu.BuildProjectPushArgv(syncSpec), "pushing project into guest"); err != nil {
			return err
		}
	}

	sshArgv := engine.SSHArgv(binary, defaultFlags, userArgs)
	ux.Debugf("libvirt: exec %s", strings.Join(sshArgv, " "))
	cmd := exec.Command(sshArgv[0], sshArgv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	if syncMode == "two-way" {
		if err := runProjectSync(qemu.BuildProjectPullArgv(syncSpec), "pulling project back from guest"); err != nil {
			ux.Debugf("libvirt: %v", err)
		}
	}

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			ux.Debugf("libvirt: agent exited %d", exitErr.ExitCode())
			return nil
		}
		return runErr
	}
	return nil
}

// runProjectSync executes one scp sync leg; a nil argv (no project dir) is a
// no-op.
func runProjectSync(argv []string, what string) error {
	if argv == nil {
		return nil
	}
	ux.Debugf("%s: %s", what, strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// libvirtPathMap assembles the container→host path map from config, plus an
// identity mapping for the host firmware (already a host path — it must pass
// the strict translator untouched).
func libvirtPathMap(cellCfg cfg.CellConfig, firmware string) libvirt.PathMap {
	var m libvirt.PathMap
	for from, to := range cellCfg.Cell.LibvirtPathMap {
		m = append(m, libvirt.PathMapping{From: from, To: to})
	}
	if len(m) > 0 {
		dir := filepath.Dir(firmware)
		m = append(m, libvirt.PathMapping{From: dir, To: dir})
	}
	return m
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
