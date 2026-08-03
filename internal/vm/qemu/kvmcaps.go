package qemu

import (
	"fmt"
	"os"
	"syscall"
)

// KVM userspace API, from linux/kvm.h. The ioctl numbers are _IO(0xAE, nr) —
// no payload, so the request is just (0xAE<<8)|nr on every architecture.
const (
	kvmGetAPIVersion  = 0xAE00 // KVM_GET_API_VERSION
	kvmCheckExtension = 0xAE03 // KVM_CHECK_EXTENSION

	kvmCapMaxVCPUs     = 66  // KVM_CAP_MAX_VCPUS
	kvmCapArmPMUv3     = 126 // KVM_CAP_ARM_PMU_V3
	kvmCapArmVMIPASize = 165 // KVM_CAP_ARM_VM_IPA_SIZE
)

// KVMHostCaps holds the host KVM properties that decide whether a Windows
// ARM64 guest can run at all.
//
// PMUv3 is the load-bearing one: a nested host (macOS vz → Colima) gets no PMU
// from Apple's hypervisor, the Linux kernel then has no arm_pmu driver, and
// KVM cannot virtualize a PMU it does not have. Guests on such a host see
// ID_AA64DFR0_EL1.PMUVer=0 and take an UNDEF on any PMU register access.
type KVMHostCaps struct {
	APIVersion int
	PMUv3      bool
	IPABits    int // 0 = cap unsupported → architected default of 40
	MaxVCPUs   int
}

// QueryKVMHostCaps interrogates /dev/kvm directly. It needs only the device
// fd — no VM is created — so it is safe to run before (or while) a guest is
// up.
func QueryKVMHostCaps(devPath string) (KVMHostCaps, error) {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return KVMHostCaps{}, err
	}
	defer f.Close()

	ioc := func(req, arg uintptr) (int, error) {
		r1, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, arg)
		if errno != 0 {
			return 0, errno
		}
		return int(int64(r1)), nil
	}

	ver, err := ioc(kvmGetAPIVersion, 0)
	if err != nil {
		return KVMHostCaps{}, fmt.Errorf("KVM_GET_API_VERSION: %w", err)
	}
	caps := KVMHostCaps{APIVersion: ver}

	// KVM_CHECK_EXTENSION returns 0 for unsupported capabilities rather than
	// erroring, so a failed ioctl here is a plumbing problem worth surfacing.
	pmu, err := ioc(kvmCheckExtension, kvmCapArmPMUv3)
	if err != nil {
		return caps, fmt.Errorf("KVM_CHECK_EXTENSION(ARM_PMU_V3): %w", err)
	}
	caps.PMUv3 = pmu > 0

	if ipa, err := ioc(kvmCheckExtension, kvmCapArmVMIPASize); err == nil {
		caps.IPABits = ipa
	}
	if n, err := ioc(kvmCheckExtension, kvmCapMaxVCPUs); err == nil {
		caps.MaxVCPUs = n
	}
	return caps, nil
}

// WindowsBootBlocker returns a non-empty reason when this host's KVM cannot
// boot Windows ARM64 regardless of QEMU configuration, or "" when no known
// blocker applies.
//
// Root-caused 2026-07-30 (TestWindowsISOBoot_KVM stall diagnostics): bootmgr
// reads PMCR_EL0 unconditionally; on a PMU-less vCPU KVM injects UNDEF and
// bootmgr parks in its `b .` panic vector (slot +0x200, DAIF masked).
// Reproduced without KVM: TCG + pmu=off hangs identically, TCG + PMU boots.
// No QEMU flag can add a PMU that the host kernel does not have.
func (c KVMHostCaps) WindowsBootBlocker() string {
	if !c.PMUv3 {
		return "host KVM offers no PMUv3 (nested hosts get no PMU from the outer hypervisor); " +
			"Windows bootmgr reads PMCR_EL0, takes the UNDEF, and parks in its panic vector"
	}
	return ""
}

// Summary is the one-line form for run-info.txt and the test log.
func (c KVMHostCaps) Summary() string {
	ipa := fmt.Sprintf("ipa=%d bits", c.IPABits)
	if c.IPABits == 0 {
		ipa = "ipa=40 (cap unsupported, kernel default)"
	}
	return fmt.Sprintf("api=%d pmuv3=%t %s max_vcpus=%d",
		c.APIVersion, c.PMUv3, ipa, c.MaxVCPUs)
}
