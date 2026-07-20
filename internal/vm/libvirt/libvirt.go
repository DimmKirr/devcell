package libvirt

import (
	"context"

	"github.com/DimmKirr/devcell/internal/vm"
)

// Libvirt is a placeholder for a future libvirt/QEMU VM engine.
type Libvirt struct{}

func New() *Libvirt                                            { return &Libvirt{} }
func (l *Libvirt) Preflight() error                            { return vm.ErrNotImplemented }
func (l *Libvirt) Boot(ctx context.Context) error              { return vm.ErrNotImplemented }
func (l *Libvirt) Shutdown(ctx context.Context) error          { return vm.ErrNotImplemented }
func (l *Libvirt) SSHArgv(binary string, flags, args []string) []string { return nil }
