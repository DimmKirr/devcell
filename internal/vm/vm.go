package vm

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by placeholder engine implementations.
var ErrNotImplemented = errors.New("engine not implemented")

// Engine is the common interface for VM backends (tart, libvirt, hyperv).
type Engine interface {
	Preflight() error
	Boot(ctx context.Context) error
	Shutdown(ctx context.Context) error
	SSHArgv(binary string, flags, args []string) []string
}
