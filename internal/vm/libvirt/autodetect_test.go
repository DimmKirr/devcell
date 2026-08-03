package libvirt

import (
	"errors"
	"strings"
	"testing"
)

// --- Auto-default detection (CELL-378) ---
//
// In a Docker cell on a Mac there is no HVF and no /dev/kvm: --engine=qemu
// can only mean TCG, 10–20× slower than the host's HVF behind libvirtd. When
// every probe agrees on that environment, qemu upgrades to libvirt remote
// mode. Authority ordering follows accel.go: an explicit --local always wins,
// and any probe disagreeing means no upgrade.

func probes(inContainer, hostResolves bool, kvmErr error) Probes {
	return Probes{
		InContainer:  func() bool { return inContainer },
		HostResolves: func() bool { return hostResolves },
		KVMUsable:    func() error { return kvmErr },
	}
}

var noKVM = errors.New("open /dev/kvm: no such file or directory")

func TestShouldDefaultToLibvirt_Matrix(t *testing.T) {
	cases := []struct {
		name       string
		engine     string
		forceLocal bool
		p          Probes
		want       bool
	}{
		{"docker-on-mac, no kvm, qemu", "qemu", false, probes(true, true, noKVM), true},
		{"explicit --local wins", "qemu", true, probes(true, true, noKVM), false},
		{"not in a container", "qemu", false, probes(false, true, noKVM), false},
		{"no docker host gateway", "qemu", false, probes(true, false, noKVM), false},
		{"kvm usable — local is fast", "qemu", false, probes(true, true, nil), false},
		{"docker engine untouched", "docker", false, probes(true, true, noKVM), false},
		{"libvirt engine untouched", "libvirt", false, probes(true, true, noKVM), false},
		{"tart engine untouched", "tart", false, probes(true, true, noKVM), false},
		{"empty engine untouched", "", false, probes(true, true, noKVM), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := ShouldDefaultToLibvirt(tc.engine, tc.forceLocal, tc.p)
			if got != tc.want {
				t.Errorf("ShouldDefaultToLibvirt(%q, local=%v) = %v, want %v",
					tc.engine, tc.forceLocal, got, tc.want)
			}
		})
	}
}

func TestShouldDefaultToLibvirt_ReasonExplainsChoice(t *testing.T) {
	ok, reason := ShouldDefaultToLibvirt("qemu", false, probes(true, true, noKVM))
	if !ok {
		t.Fatal("expected upgrade")
	}
	// The reason is user-facing (accel.go's "choice + reason" pattern): it
	// must say why local qemu would be slow and where the VM goes instead.
	for _, want := range []string{"TCG", "libvirt"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason must mention %q, got: %q", want, reason)
		}
	}
}

func TestDefaultProbes_AreWired(t *testing.T) {
	p := DefaultProbes()
	if p.InContainer == nil || p.HostResolves == nil || p.KVMUsable == nil {
		t.Fatal("DefaultProbes must wire all three probes")
	}
	// Smoke: they must be callable without panicking; results depend on env.
	_ = p.InContainer()
	_ = p.HostResolves()
	_ = p.KVMUsable()
}
