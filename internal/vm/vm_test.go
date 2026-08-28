package vm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm"
	"github.com/DimmKirr/devcell/internal/vm/hyperv"
)

// The libvirt engine is real since CELL-377 — its contract lives in
// internal/vm/libvirt/engine_test.go. Only hyperv remains a placeholder.

func TestHypervPreflight_NotImplemented(t *testing.T) {
	e := hyperv.New()
	err := e.Preflight()
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}

func TestHypervBoot_NotImplemented(t *testing.T) {
	e := hyperv.New()
	err := e.Boot(context.Background())
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}

func TestHypervShutdown_NotImplemented(t *testing.T) {
	e := hyperv.New()
	err := e.Shutdown(context.Background())
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}
