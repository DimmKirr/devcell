package tart_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/vm/tart"
)

func TestWaitForSSHObserved_FailsFastOnVMError(t *testing.T) {
	// Start a listener that accepts but never completes SSH handshake isn't
	// needed — we use a port that nothing listens on so every dial fails.
	// The key behavior: with a VMStateFunc returning "error", the function
	// should bail out much faster than the full timeout.

	timeout := 30 * time.Second
	interval := 100 * time.Millisecond

	callCount := 0
	stateFunc := func() string {
		callCount++
		if callCount >= 2 {
			return "error"
		}
		return "running"
	}

	start := time.Now()
	err := tart.WaitForSSHObserved("127.0.0.1", 19999, timeout, interval, tart.NopObserver{}, stateFunc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "VM state") {
		t.Errorf("error should mention VM state, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("should have failed fast, took %s", elapsed)
	}
}

func TestWaitForSSHObserved_FailsFastOnVMStopped(t *testing.T) {
	stateFunc := func() string { return "stopped" }

	start := time.Now()
	err := tart.WaitForSSHObserved("127.0.0.1", 19999, 30*time.Second, 100*time.Millisecond, tart.NopObserver{}, stateFunc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error should mention 'stopped', got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("should have failed fast, took %s", elapsed)
	}
}

func TestWaitForSSHObserved_SucceedsWithStateFunc(t *testing.T) {
	// Start a real TCP listener to simulate SSH port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("creating listener: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := uint16(0)
	for _, c := range portStr {
		port = port*10 + uint16(c-'0')
	}

	stateFunc := func() string { return "running" }

	err = tart.WaitForSSHObserved("127.0.0.1", port, 5*time.Second, 100*time.Millisecond, tart.NopObserver{}, stateFunc)
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestWaitForSSHObserved_NilStateFuncOK(t *testing.T) {
	// No state func (backwards compat) — just times out normally.
	start := time.Now()
	err := tart.WaitForSSHObserved("127.0.0.1", 19999, 500*time.Millisecond, 100*time.Millisecond, tart.NopObserver{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Should have used the full timeout, not failed fast.
	if elapsed < 400*time.Millisecond {
		t.Errorf("should have waited near full timeout, only took %s", elapsed)
	}
}
