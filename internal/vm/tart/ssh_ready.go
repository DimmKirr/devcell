package tart

import (
	"fmt"
	"net"
	"time"
)

// VMStateFunc returns the current VM state (e.g. "running", "stopped", "error").
// Passed to polling functions so they can fail fast if the VM crashes.
type VMStateFunc func() string

// WaitForSSH polls a TCP connection to host:port until it succeeds or the
// timeout elapses. Returns nil on success, or an error describing the failure.
// Pure network check — no authentication, just verifies the port is accepting.
func WaitForSSH(host string, port uint16, timeout, interval time.Duration) error {
	return WaitForSSHObserved(host, port, timeout, interval, NopObserver{})
}

// WaitForSSHObserved is like WaitForSSH but reports each poll attempt to obs.
// An optional VMStateFunc checks VM liveness each iteration — if the VM enters
// "stopped" or "error" state, polling aborts immediately instead of waiting
// the full timeout.
func WaitForSSHObserved(host string, port uint16, timeout, interval time.Duration, obs Observer, stateFunc ...VMStateFunc) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	obs.Logf("polling SSH at %s (timeout %s, interval %s)", addr, timeout, interval)

	var checkState VMStateFunc
	if len(stateFunc) > 0 && stateFunc[0] != nil {
		checkState = stateFunc[0]
	}

	attempt := 0
	var lastErr error
	for time.Now().Before(deadline) {
		attempt++

		if checkState != nil {
			state := checkState()
			obs.Logf("SSH attempt %d: VM state=%s", attempt, state)
			if state == "stopped" || state == "error" {
				return fmt.Errorf("SSH polling aborted: VM state is %q after %d attempts", state, attempt)
			}
		}

		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			obs.Logf("SSH ready after %d attempts", attempt)
			return nil
		}
		lastErr = err
		elapsed := timeout - time.Until(deadline)
		obs.Logf("SSH attempt %d: %v (%.0fs elapsed)", attempt, err, elapsed.Seconds())
		obs.Progress(float64(elapsed)/float64(timeout), fmt.Sprintf("waiting for SSH (%d attempts)", attempt))
		time.Sleep(interval)
	}
	return fmt.Errorf("SSH not ready at %s after %s: %w", addr, timeout, lastErr)
}

// ProbeSSH does a single TCP dial to check if SSH is accepting connections.
func ProbeSSH(host string, port uint16) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("SSH not reachable at %s: %w", addr, err)
	}
	conn.Close()
	return nil
}
