package vm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm"
	"github.com/DimmKirr/devcell/internal/vm/hyperv"
	"github.com/DimmKirr/devcell/internal/vm/libvirt"
)

func TestLibvirtPreflight_NotImplemented(t *testing.T) {
	e := libvirt.New()
	err := e.Preflight()
	if err == nil {
		t.Fatal("expected error from libvirt placeholder")
	}
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}

func TestLibvirtBoot_NotImplemented(t *testing.T) {
	e := libvirt.New()
	err := e.Boot(context.Background())
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}

func TestLibvirtShutdown_NotImplemented(t *testing.T) {
	e := libvirt.New()
	err := e.Shutdown(context.Background())
	if !errors.Is(err, vm.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got: %v", err)
	}
}

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
