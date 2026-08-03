package libvirt

import (
	"net"
	"os"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// Probes are the environment signals behind the qemu→libvirt auto-default
// (CELL-378). Injectable so the decision matrix is unit-testable.
type Probes struct {
	// InContainer reports whether the CLI runs inside a container.
	InContainer func() bool
	// HostResolves reports whether the Docker host gateway name resolves.
	HostResolves func() bool
	// KVMUsable reports whether /dev/kvm can actually be opened.
	KVMUsable func() error
}

// DefaultProbes wires the production signals: /.dockerenv, a DNS lookup of
// host.docker.internal, and qemu.ProbeKVM. All three are one cheap syscall
// or lookup — this runs on every launch.
func DefaultProbes() Probes {
	return Probes{
		InContainer: func() bool {
			_, err := os.Stat("/.dockerenv")
			return err == nil
		},
		HostResolves: func() bool {
			_, err := net.LookupHost("host.docker.internal")
			return err == nil
		},
		KVMUsable: func() error { return qemu.ProbeKVM() },
	}
}

// ShouldDefaultToLibvirt decides whether an --engine=qemu launch should
// upgrade to libvirt remote mode, and why.
//
// Authority ordering follows accel.go: explicit intent always wins (--local
// pins the in-container path; any engine other than qemu is untouched), and
// the upgrade fires only when every probe agrees the environment is a Docker
// cell on a Mac where local qemu can only mean TCG.
func ShouldDefaultToLibvirt(engine string, forceLocal bool, p Probes) (bool, string) {
	if engine != "qemu" || forceLocal {
		return false, ""
	}
	if !p.InContainer() {
		return false, ""
	}
	if !p.HostResolves() {
		return false, ""
	}
	if p.KVMUsable() == nil {
		return false, ""
	}
	return true, "in a container with no usable /dev/kvm — local qemu would run TCG (10–20× slower); using libvirt remote mode on the Docker host instead (pin with --local)"
}
