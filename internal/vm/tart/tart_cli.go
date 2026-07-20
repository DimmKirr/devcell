package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
)

// VM wraps a tart VM managed via the tart CLI.
type VM struct {
	Name      string
	cmd       *exec.Cmd    // tart run process (nil if not started by us)
	stderrBuf bytes.Buffer // captures tart run stderr for diagnostics
}

// TartRun starts a VM headlessly via `tart run --no-graphics`.
// dirs are VirtioFS shared directories (tag → host path).
// disks are raw disk image paths attached as VirtIO block devices.
// The returned VM holds the background process; the caller must call Stop().
func TartRun(ctx context.Context, name string, dirs map[string]string, disks []string) (*VM, error) {
	args := []string{"run", "--no-graphics"}
	for tag, path := range dirs {
		args = append(args, "--dir", fmt.Sprintf("%s:%s", tag, path))
	}
	for _, disk := range disks {
		args = append(args, "--disk", disk)
	}
	args = append(args, name)
	ux.Debugf("tart command: tart %s", strings.Join(args, " "))
	vm := &VM{Name: name}
	cmd := exec.CommandContext(ctx, "tart", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &vm.stderrBuf)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("tart run %s: %w", name, err)
	}
	vm.cmd = cmd
	return vm, nil
}

// Stderr returns any stderr output captured from the tart run process.
func (vm *VM) Stderr() string {
	return vm.stderrBuf.String()
}

// Stop gracefully shuts down the VM via `tart stop`.
func (vm *VM) Stop() error {
	err := exec.Command("tart", "stop", vm.Name).Run()
	if err != nil {
		return fmt.Errorf("tart stop %s: %w", vm.Name, err)
	}
	if vm.cmd != nil {
		vm.cmd.Wait()
	}
	return nil
}

// ForceStop kills the tart run process immediately.
func (vm *VM) ForceStop() error {
	if vm.cmd != nil && vm.cmd.Process != nil {
		if err := vm.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("killing tart run: %w", err)
		}
		vm.cmd.Wait()
	}
	return nil
}

// State returns the current VM state by querying `tart get`.
func (vm *VM) State() string {
	info, err := TartGet(context.Background(), vm.Name)
	if err != nil {
		return "unknown"
	}
	if info.State != "" {
		return strings.ToLower(info.State)
	}
	if info.Running {
		return "running"
	}
	return "stopped"
}

// IP returns the guest IP address via `tart ip`.
func (vm *VM) IP(ctx context.Context) (string, error) {
	return TartIP(ctx, vm.Name)
}

// WaitForIP polls `tart ip` until it returns an IP or the timeout expires.
func (vm *VM) WaitForIP(ctx context.Context, timeout, interval time.Duration) (string, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("tart ip %s: timed out after %s", vm.Name, timeout)
		case <-ticker.C:
			ip, err := TartIP(ctx, vm.Name)
			if err == nil && ip != "" {
				return ip, nil
			}
		}
	}
}

// TartIP runs `tart ip <name>` and returns the guest IP.
func TartIP(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, "tart", "ip", name).Output()
	if err != nil {
		return "", fmt.Errorf("tart ip %s: %w", name, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("tart ip %s: empty response", name)
	}
	return ip, nil
}

// TartVersion returns the tart CLI version string.
func TartVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "tart", "--version").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	return strings.TrimSpace(string(out))
}

// TartStop runs `tart stop <name>`.
func TartStop(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "tart", "stop", name).Run()
}

// TartExec runs a command inside a running VM via `tart exec`.
func TartExec(ctx context.Context, name string, command []string, stdout, stderr *os.File) error {
	args := []string{"exec", name}
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, "tart", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// TartExecInteractive runs an interactive PTY session via `tart exec -t -i`.
func TartExecInteractive(ctx context.Context, name string, command []string) error {
	args := []string{"exec", "-t", "-i", name}
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, "tart", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TartSet configures VM resources via `tart set`.
func TartSet(ctx context.Context, name string, cpus uint, memoryMB uint64) error {
	args := []string{"set", name}
	if cpus > 0 {
		args = append(args, "--cpu", fmt.Sprintf("%d", cpus))
	}
	if memoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%d", memoryMB))
	}
	cmd := exec.CommandContext(ctx, "tart", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TartCreate creates a VM from an IPSW file via `tart create`.
func TartCreate(ctx context.Context, name, ipswPath string, diskSizeGB int) error {
	args := []string{"create", name, "--from-ipsw", ipswPath}
	if diskSizeGB > 0 {
		args = append(args, "--disk-size", fmt.Sprintf("%d", diskSizeGB))
	}
	cmd := exec.CommandContext(ctx, "tart", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TartList returns running VM names by parsing `tart list --format json`.
func TartList(ctx context.Context) ([]TartListEntry, error) {
	out, err := exec.CommandContext(ctx, "tart", "list", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("tart list: %w", err)
	}
	// tart list outputs one JSON object per line (JSONL format)
	var entries []TartListEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e TartListEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// TartListEntry represents one VM in `tart list` output.
type TartListEntry struct {
	Name   string `json:"Name"`
	State  string `json:"State"`
	Source string `json:"Source"`
	Disk   int    `json:"Disk"`
}
