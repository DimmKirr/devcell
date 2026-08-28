package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Host KVM capability probe — exists to answer one question about the KVM
// firmware hang: does this (nested) host's KVM offer PMUv3 at all? Windows
// bootmgr reads PMCR_EL0; on a vCPU without a PMU, KVM injects UNDEF, and
// bootmgr's sync-exception vector is a `b .` dead loop.

func TestKVMHostCapsSummary(t *testing.T) {
	c := KVMHostCaps{APIVersion: 12, PMUv3: false, IPABits: 40, MaxVCPUs: 8}
	s := c.Summary()
	assert.Contains(t, s, "api=12")
	assert.Contains(t, s, "pmuv3=false")
	assert.Contains(t, s, "ipa=40")
	assert.Contains(t, s, "max_vcpus=8")
}

// KVM_CAP_ARM_VM_IPA_SIZE returning 0 means the cap predates the kernel — the
// architected default is 40 bits, and the summary must say so rather than
// print a bare 0 that reads like "no address space".
func TestKVMHostCapsSummary_IPACapUnsupported(t *testing.T) {
	c := KVMHostCaps{APIVersion: 12, IPABits: 0}
	assert.Contains(t, c.Summary(), "ipa=40 (cap unsupported, kernel default)")
}

func TestQueryKVMHostCaps_MissingDevice(t *testing.T) {
	_, err := QueryKVMHostCaps("/definitely/not/kvm")
	assert.Error(t, err)
}

// Real-device sanity: only runs where /dev/kvm is usable (the sudo KVM test
// session). The KVM userspace API version has been pinned at 12 since 2.6.22 —
// any other value means the ioctl plumbing is wrong, not the kernel.
func TestQueryKVMHostCaps_RealDevice(t *testing.T) {
	if err := ProbeKVM(); err != nil {
		t.Skipf("%s unusable: %v", KVMDevice, err)
	}
	caps, err := QueryKVMHostCaps(KVMDevice)
	require.NoError(t, err)
	assert.Equal(t, 12, caps.APIVersion, "KVM stable API is 12; anything else is an ioctl bug")
}

// WindowsBootBlocker encodes the 2026-07-30 root cause: bootmgr reads
// PMCR_EL0 unconditionally, so a PMU-less host KVM can never boot Windows
// ARM64 — the test must skip with the reason rather than burn a timeout
// rediscovering it.
func TestWindowsBootBlocker_NoPMU(t *testing.T) {
	c := KVMHostCaps{APIVersion: 12, PMUv3: false}
	reason := c.WindowsBootBlocker()
	assert.Contains(t, reason, "PMUv3")
	assert.Contains(t, reason, "PMCR_EL0", "the reason must name the faulting register, not just say 'unsupported'")
}

func TestWindowsBootBlocker_PMUPresent(t *testing.T) {
	c := KVMHostCaps{APIVersion: 12, PMUv3: true}
	assert.Empty(t, c.WindowsBootBlocker(), "a PMU-capable host has no known blocker — the test must run")
}
