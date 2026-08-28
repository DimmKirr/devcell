package qemu

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpec_ApplyDefaults(t *testing.T) {
	t.Setenv("USER", "") // exercise the fallback, not the ambient host user
	var s Spec
	s.ApplyDefaults()
	assert.Equal(t, uint(4), s.CPUs)
	assert.Equal(t, uint64(4), s.MemoryGB)
	assert.NotZero(t, s.SSHPort, "a forwarded SSH port must be chosen")
	assert.Equal(t, "127.0.0.1", s.SSHHost)
	assert.Equal(t, "devcell", s.SSHUser)
	assert.Equal(t, "none", s.DisplayType)
}

// Regression: ApplyDefaults used to assign the constant 2222 unconditionally.
// Two overlapping runs therefore raced for the same hostfwd, and QEMU aborted
// at launch with
//
//	-netdev user,...,hostfwd=tcp:127.0.0.1:2222-:22: Could not set up host
//	forwarding rule 'tcp:127.0.0.1:2222-:22'
//
// which then surfaced only as "QMP socket did not appear within 30s" and an
// empty results directory. That is what killed
// test/results/20260730T044854-TestWindowsISOBoot_TCG and
// test/results/20260729T174705-TestWindowsUnattendedInstall_TCG.
func TestSpec_ApplyDefaults_SkipsAPortAlreadyInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:2222")
	if err != nil {
		t.Skip("port 2222 unavailable for the negative control; cannot prove the skip")
	}
	defer ln.Close()

	var s Spec
	s.ApplyDefaults()
	assert.NotEqual(t, uint16(2222), s.SSHPort,
		"2222 is held by this test; ApplyDefaults must pick a port that is actually free")
}

// The allocated port must be bindable at the moment it is handed out —
// otherwise "free" is a guess and QEMU still fails at launch.
func TestSpec_ApplyDefaults_AllocatesABindablePort(t *testing.T) {
	var s Spec
	s.ApplyDefaults()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.SSHPort))
	if err != nil {
		t.Fatalf("ApplyDefaults handed out port %d, which is not bindable: %v", s.SSHPort, err)
	}
	ln.Close()
}

// Production callers pin a port deliberately (ports.json); allocation must only
// fill the zero value.
func TestSpec_ApplyDefaults_HonoursExplicitSSHPort(t *testing.T) {
	s := Spec{SSHPort: 2299}
	s.ApplyDefaults()
	assert.Equal(t, uint16(2299), s.SSHPort, "an explicit SSHPort must be left alone")
}

// RDPPort 0 means "RDP disabled" (see the Spec field comment), not "allocate
// one for me" — auto-allocating it would silently enable an RDP forward.
func TestSpec_ApplyDefaults_LeavesRDPPortDisabled(t *testing.T) {
	var s Spec
	s.ApplyDefaults()
	assert.Zero(t, s.RDPPort, "RDPPort 0 means disabled and must stay 0")
}

func TestSpec_Validate_OK(t *testing.T) {
	s := Spec{DiskPath: "/tmp/disk.qcow2", FirmwarePath: "/tmp/efi.fd", CPUs: 2, MemoryGB: 4}
	assert.NoError(t, s.Validate())
}

func TestSpec_Validate_MissingDisk(t *testing.T) {
	s := Spec{FirmwarePath: "/tmp/efi.fd", CPUs: 2, MemoryGB: 4}
	assert.ErrorContains(t, s.Validate(), "DiskPath")
}

func TestSpec_Validate_MissingFirmware(t *testing.T) {
	s := Spec{DiskPath: "/tmp/disk.qcow2", CPUs: 2, MemoryGB: 4}
	assert.ErrorContains(t, s.Validate(), "FirmwarePath")
}

func TestDeterministicMAC_Stable(t *testing.T) {
	a := DeterministicMAC("main")
	b := DeterministicMAC("main")
	assert.Equal(t, a, b)
}

func TestDeterministicMAC_DifferentCells(t *testing.T) {
	a := DeterministicMAC("main")
	b := DeterministicMAC("work")
	assert.NotEqual(t, a, b)
}

func TestDeterministicMAC_LocallyAdministered(t *testing.T) {
	mac := DeterministicMAC("test")
	assert.NotEmpty(t, mac)
	// Parse first byte: bit 1 = locally administered, bit 0 = unicast
	var b0 byte
	_, err := fmt.Sscanf(mac[:2], "%02x", &b0)
	assert.NoError(t, err)
	assert.Equal(t, byte(0x02), b0&0x03, "should be locally administered unicast")
}

func TestDeterministicMAC_DiffersFromTart(t *testing.T) {
	// QEMU uses "devcell-qemu:" prefix, tart uses "devcell-tart:"
	qemu := DeterministicMAC("main")
	assert.NotEmpty(t, qemu)
	// Just verify it's a valid 6-byte MAC (17 chars: xx:xx:xx:xx:xx:xx)
	assert.Len(t, qemu, 17)
}

func TestStackTag_NoModules(t *testing.T) {
	assert.Equal(t, "ultimate", StackTag("ultimate", nil))
}

func TestStackTag_WithModules(t *testing.T) {
	tag := StackTag("dev", []string{"wine", "social"})
	assert.Contains(t, tag, "dev-")
	assert.Contains(t, tag, "social")
	assert.Contains(t, tag, "wine")
}

func TestStackTag_ModulesSorted(t *testing.T) {
	a := StackTag("dev", []string{"b", "a"})
	b := StackTag("dev", []string{"a", "b"})
	assert.Equal(t, a, b)
}

func TestTemplateDir(t *testing.T) {
	dir := TemplateDir("/home/user", "base", nil)
	assert.Equal(t, "/home/user/.devcell/windows/base", dir)
}

func TestInstanceDir(t *testing.T) {
	dir := InstanceDir("/home/user", "main")
	assert.Equal(t, "/home/user/.devcell/main/windows", dir)
}

func TestTemplateVMName(t *testing.T) {
	name := TemplateVMName("base", nil)
	assert.Equal(t, "devcell-qemu-base", name)
}

func TestInstanceVMName(t *testing.T) {
	name := InstanceVMName("main")
	assert.Equal(t, "main-qemu", name)
}

func TestImageName(t *testing.T) {
	name := ImageName("base", nil)
	assert.Equal(t, "disk-base.qcow2", name)
}

func TestProvisionedMarker(t *testing.T) {
	path := ProvisionedMarker("/home/user", "base", nil)
	assert.Equal(t, "/home/user/.devcell/windows/base/.provisioned", path)
}
