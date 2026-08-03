package libvirt

import (
	"context"
	"errors"
	"fmt"
)

// Preflight verifies a libvirtd daemon answers at uri and completes the RPC
// handshake, mapping each failure mode to one actionable message (the
// CELL-44 pattern: read the error, know the next command).
func Preflight(ctx context.Context, uri string) error {
	c, err := Connect(ctx, uri)
	if err == nil {
		return c.Close()
	}

	switch {
	case errors.Is(err, ErrUnreachable):
		addr, _ := ParseURI(uri)
		return fmt.Errorf(`%w

libvirtd is not answering at %s. On the macOS host:
  brew install libvirt
  brew services start libvirt
and enable TCP listen for the daemon (listen_tcp = 1, auth_tcp = "none" in
libvirtd.conf — see the libvirt engine docs; qemu+ssh:// is the hardened
alternative). From inside a cell the host is host.docker.internal.`, err, addr)
	case errors.Is(err, ErrHandshake):
		return fmt.Errorf(`%w

The port answered but the libvirt RPC handshake failed. Either something
else is listening there, or the daemon requires authentication — devcell's
qemu+tcp transport needs auth_tcp = "none" in libvirtd.conf (or switch to
qemu+ssh://).`, err)
	default:
		return err
	}
}
