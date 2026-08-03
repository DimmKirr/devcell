package qemu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Accelerator selection (CELL-352 follow-up).
//
// Before this, effectiveAccel() returned a bare "kvm" on every linux host, so
// QEMU aborted at launch with "Could not access KVM kernel module" whenever
// /dev/kvm was absent or unreadable. The only reason that never surfaced is
// that every test hardcodes Accel: "tcg,...". The decision now has two inputs:
// the `[cell] kvm` config intent, and a probe that the device is actually
// openable — the same open(2) QEMU itself performs.

func okProbe() error  { return nil }
func badProbe() error { return errors.New("permission denied") }

func TestResolveAccel_ExplicitOverrideWins(t *testing.T) {
	accel, reason := ResolveAccel("tcg,thread=multi,tb-size=512", true, "linux", okProbe)
	assert.Equal(t, "tcg,thread=multi,tb-size=512", accel)
	assert.Contains(t, reason, "explicit")
}

func TestResolveAccel_DarwinUsesHVFRegardlessOfKVM(t *testing.T) {
	// HVF is the host hypervisor on macOS; /dev/kvm never exists there.
	accel, _ := ResolveAccel("", true, "darwin", badProbe)
	assert.Equal(t, "hvf", accel)
}

func TestResolveAccel_LinuxKVMRequestedAndUsable(t *testing.T) {
	accel, reason := ResolveAccel("", true, "linux", okProbe)
	assert.Equal(t, "kvm", accel)
	assert.Contains(t, reason, "kvm")
}

func TestResolveAccel_LinuxKVMRequestedButUnusableFallsBackToTCG(t *testing.T) {
	// The whole point: a config asking for KVM on a host that cannot provide
	// it must degrade to emulation, not abort the launch.
	accel, reason := ResolveAccel("", true, "linux", badProbe)
	assert.Equal(t, DefaultTCGAccel, accel)
	assert.Contains(t, reason, "permission denied", "the reason must carry the probe error, not just say 'unavailable'")
}

func TestResolveAccel_LinuxKVMNotRequestedStaysTCG(t *testing.T) {
	// Config is the authority: an unrequested KVM is not silently adopted even
	// when the device happens to be usable.
	accel, reason := ResolveAccel("", false, "linux", okProbe)
	assert.Equal(t, DefaultTCGAccel, accel)
	assert.Contains(t, reason, "kvm = true")
}

// --- the probe itself ---

func TestProbeDevice_OpenableDevice(t *testing.T) {
	assert.NoError(t, probeDevice("/dev/null"))
}

func TestProbeDevice_MissingDevice(t *testing.T) {
	err := probeDevice(filepath.Join(t.TempDir(), "definitely-not-here"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
}

func TestProbeDevice_UnreadableDevice(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	p := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.WriteFile(p, nil, 0o644))
	require.NoError(t, os.Chmod(p, 0o000))
	assert.Error(t, probeDevice(p), "mode 0000 must fail the probe — this is the group-membership case")
}

// --- Spec wiring ---

func TestApplyDefaults_ResolvesAccelOnce(t *testing.T) {
	var s Spec
	s.ApplyDefaults()
	assert.NotEmpty(t, s.Accel, "ApplyDefaults must pin the accelerator so machineType/cpuType/argv cannot disagree")
	assert.NotEmpty(t, s.AccelReason, "the decision must be explainable in the launch log")
}

func TestApplyDefaults_KeepsExplicitAccel(t *testing.T) {
	s := Spec{Accel: "tcg,thread=multi,tb-size=512"}
	s.ApplyDefaults()
	assert.Equal(t, "tcg,thread=multi,tb-size=512", s.Accel)
}

func TestApplyDefaults_KVMSpecGetsKVMArgvWhenUsable(t *testing.T) {
	s := testSpec()
	s.Accel, s.AccelReason = ResolveAccel("", true, "linux", okProbe)
	joined := strings.Join(BuildRunCommand(s), " ")

	assert.Contains(t, joined, "-accel kvm")
	// EL2 for the guest needs nested virt the host cannot provide under an
	// already-nested KVM ("host kernel KVM does not support providing
	// Virtualization extensions to the guest CPU"), so virtualization=true
	// must NOT appear. pauth-impdef is a TCG-only speed hack.
	assert.NotContains(t, joined, "virtualization=true")
	assert.NotContains(t, joined, "pauth-impdef")
	assert.Contains(t, joined, "-cpu max") // QEMU maps max→host under KVM
}

func TestSpec_KVMFieldDrivesResolution(t *testing.T) {
	s := Spec{KVM: true}
	s.resolveAccel("linux", okProbe)
	assert.Equal(t, "kvm", s.Accel)

	s2 := Spec{KVM: false}
	s2.resolveAccel("linux", okProbe)
	assert.Equal(t, DefaultTCGAccel, s2.Accel)
}

// --- PreferredAccel (test/tool path: a usable device is the consent) ---

func TestPreferredAccel_KeepsTunedTCGStringOnFallback(t *testing.T) {
	// tb-size is TCG-only and QEMU rejects it alongside accel=kvm, so the
	// caller's tuned string must survive the fallback verbatim.
	got := preferredAccel("tcg,thread=multi,tb-size=512", "linux", badProbe)
	assert.Equal(t, "tcg,thread=multi,tb-size=512", got)
}

func TestPreferredAccel_UsesKVMWhenUsable(t *testing.T) {
	got := preferredAccel("tcg,thread=multi,tb-size=512", "linux", okProbe)
	assert.Equal(t, "kvm", got)
	assert.NotContains(t, got, "tb-size", "tb-size alongside kvm is rejected by QEMU")
}

func TestPreferredAccel_DarwinUsesHVF(t *testing.T) {
	assert.Equal(t, "hvf", preferredAccel("tcg,thread=multi", "darwin", badProbe))
}

// --- Spec.CPU override (single-variable CPU experiments) ---

func TestBaseCommand_CPUOverride(t *testing.T) {
	s := testSpec()
	s.Accel = "tcg,thread=multi"
	s.CPU = "max,pauth-impdef=on,pmu=off"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-cpu max,pauth-impdef=on,pmu=off")
	assert.Equal(t, 1, strings.Count(joined, "-cpu "), "override must replace the default, not add a second -cpu")
}

func TestBaseCommand_EmptyCPUKeepsAcceleratorDefault(t *testing.T) {
	s := testSpec()
	s.Accel = "tcg,thread=multi"
	joined := strings.Join(BuildRunCommand(s), " ")
	assert.Contains(t, joined, "-cpu max,pauth-impdef=on")
}
