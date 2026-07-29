package qemu

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"
)

// Engine implements vm.Engine for QEMU Windows VMs.
type Engine struct {
	Spec Spec
	obs  Observer
	vm   *VM
}

// NewEngine creates a new QEMU engine with the given spec.
func NewEngine(spec Spec, obs Observer) *Engine {
	return &Engine{
		Spec: spec,
		obs:  obs,
	}
}

// Preflight validates the host can run QEMU Windows VMs.
func (e *Engine) Preflight() error {
	if err := PreflightCheck(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	if _, err := QEMUBinaryPath(); err != nil {
		return err
	}
	return nil
}

// Boot starts the QEMU VM and waits for SSH to become available.
func (e *Engine) Boot(ctx context.Context) error {
	e.Spec.ApplyDefaults()
	if err := e.Spec.Validate(); err != nil {
		return fmt.Errorf("invalid spec: %w", err)
	}

	e.vm = NewVM(e.Spec, e.obs, "")
	if err := e.vm.Start(ctx); err != nil {
		return err
	}

	e.obs.Logf("waiting for SSH on %s:%d", e.Spec.SSHHost, e.Spec.SSHPort)
	return WaitForSSH(e.Spec.SSHHost, e.Spec.SSHPort, 5*time.Minute, 3*time.Second, e.obs)
}

// Shutdown gracefully stops the QEMU VM.
func (e *Engine) Shutdown(ctx context.Context) error {
	if e.vm == nil {
		return nil
	}
	return e.vm.Shutdown(ctx)
}

// SSHArgv constructs the SSH argv for running a command inside the VM.
func (e *Engine) SSHArgv(binary string, flags, args []string) []string {
	spec := e.Spec
	spec.Binary = binary
	spec.DefaultFlags = flags
	spec.UserArgs = args
	return BuildSSHArgv(spec)
}

// VMStateFunc returns the current VM state. WaitForSSH uses it to bail early
// when the QEMU process exits (e.g. drive collision, missing firmware).
type VMStateFunc func() VMState

// WaitForSSH polls until the SSH port accepts connections.
// If vmState is non-nil, returns immediately when the VM is no longer running.
func WaitForSSH(host string, port uint16, timeout, interval time.Duration, obs Observer, vmState ...VMStateFunc) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	obs.Logf("polling SSH at %s (timeout %s)", addr, timeout)

	var checkVM VMStateFunc
	if len(vmState) > 0 {
		checkVM = vmState[0]
	}

	attempt := 0
	var lastErr error
	for time.Now().Before(deadline) {
		if checkVM != nil {
			if s := checkVM(); s != StateRunning {
				return fmt.Errorf("VM exited (state=%s) while waiting for SSH after %d attempts", s, attempt)
			}
		}
		attempt++
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			scanner := bufio.NewScanner(conn)
			gotBanner := false
			if scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "SSH-") {
					gotBanner = true
					obs.Logf("SSH banner: %s", line)
				}
			}
			conn.Close()
			if gotBanner {
				obs.Logf("SSH ready after %d attempts", attempt)
				return nil
			}
			lastErr = fmt.Errorf("TCP open but no SSH banner at %s", addr)
		}
		lastErr = err
		elapsed := timeout - time.Until(deadline)
		obs.Progress(float64(elapsed)/float64(timeout),
			fmt.Sprintf("waiting for SSH (%d attempts)", attempt))
		time.Sleep(interval)
	}
	return fmt.Errorf("SSH not ready at %s after %s: %w", addr, timeout, lastErr)
}
