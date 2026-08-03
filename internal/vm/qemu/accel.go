package qemu

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// KVMDevice is the character device QEMU opens to use hardware virtualization
// on Linux. Inside a container it is present only when the launcher passed
// --device=/dev/kvm (see `[cell] kvm` in .devcell.toml).
const KVMDevice = "/dev/kvm"

// DefaultTCGAccel is the software-emulation fallback. thread=multi lets TCG
// spread guest vCPUs across host threads, which is the single largest win
// available without hardware virtualization.
const DefaultTCGAccel = "tcg,thread=multi"

// probeDevice reports whether path can be opened read-write — the same check
// QEMU performs before it will use an accelerator. It deliberately opens
// rather than stats: a passed-through /dev/kvm is present but unreadable until
// the session user joins the device's group, and stat cannot tell those apart.
//
// Cost is one open + one close, so it is cheap enough to run on every launch.
func probeDevice(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// ProbeKVM reports whether /dev/kvm is usable by the current process.
func ProbeKVM() error { return probeDevice(KVMDevice) }

// ResolveAccel picks the QEMU accelerator and returns the choice plus a
// human-readable reason for the launch log.
//
// Order of authority:
//  1. an explicit Spec.Accel — callers (notably tests) always win;
//  2. darwin — HVF is the host hypervisor and /dev/kvm never exists;
//  3. `[cell] kvm = true` AND the device actually opens — KVM;
//  4. otherwise TCG.
//
// Both conditions in (3) are load-bearing. Config alone is not enough: it
// describes intent, and a launch that trusts it on a host without nested
// virtualization dies with "Could not access KVM kernel module". A usable
// device alone is not enough either — config stays the authority, so an
// unrequested accelerator is never silently adopted.
func ResolveAccel(explicit string, kvmRequested bool, goos string, probe func() error) (accel, reason string) {
	if explicit != "" {
		return explicit, "explicit Spec.Accel override"
	}
	if goos == "darwin" {
		return accelerator(goos), "darwin: HVF is the host hypervisor"
	}
	if !kvmRequested {
		return DefaultTCGAccel, "software emulation: set `[cell] kvm = true` (and pass --device=/dev/kvm) to use hardware virtualization"
	}
	if err := probe(); err != nil {
		return DefaultTCGAccel, fmt.Sprintf("software emulation: kvm requested but %s is unusable: %v", KVMDevice, err)
	}
	return accelerator(goos), fmt.Sprintf("hardware virtualization: kvm requested and %s opened", KVMDevice)
}

// PreferredAccel returns hardware virtualization when the host can provide it,
// and the caller's TCG string otherwise.
//
// It differs from ResolveAccel in who grants consent: there is no cfg layer in
// a test binary, so a usable device *is* the consent. The fallback stays a
// caller argument because TCG tuning is workload-specific — tb-size=512 is
// worth it for a 70-minute Windows install and meaningless elsewhere, and it is
// rejected outright when passed alongside an accel of kvm.
func PreferredAccel(tcgFallback string) string {
	return preferredAccel(tcgFallback, runtime.GOOS, ProbeKVM)
}

func preferredAccel(tcgFallback, goos string, probe func() error) string {
	accel, _ := ResolveAccel("", true, goos, probe)
	if strings.HasPrefix(accel, "tcg") {
		return tcgFallback
	}
	return accel
}

// resolveAccel pins the spec's accelerator so machineType, cpuType and the
// argv builder cannot disagree, and so the probe runs once per launch rather
// than once per caller.
func (s *Spec) resolveAccel(goos string, probe func() error) {
	s.Accel, s.AccelReason = ResolveAccel(s.Accel, s.KVM, goos, probe)
}
