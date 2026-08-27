package hyperv

import (
	"context"

	"github.com/DimmKirr/devcell/internal/vm"
)

// HyperV is a placeholder for a future Hyper-V VM engine.
type HyperV struct{}

func New() *HyperV                                                     { return &HyperV{} }
func (h *HyperV) Preflight() error                                     { return vm.ErrNotImplemented }
func (h *HyperV) Boot(ctx context.Context) error                       { return vm.ErrNotImplemented }
func (h *HyperV) Shutdown(ctx context.Context) error                   { return vm.ErrNotImplemented }
func (h *HyperV) SSHArgv(binary string, flags, args []string) []string { return nil }
