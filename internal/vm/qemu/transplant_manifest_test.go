package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The VMP transplant manifest is derived from a live ARM64 Windows 11 VM
// running WSL2 with only VirtualMachinePlatform enabled (see
// testdata/live-vm-vmp-services.txt). It lists every service key to clone
// from the registry export into boot.wim's SYSTEM hive, plus the backing
// binary sourced from a donor install.wim with VMP enabled.

func TestVMPTransplantServices_CoversLiveVMSet(t *testing.T) {
	services := VMPTransplantServices()

	want := []string{
		"vmbus", "vmbusr", "vmbusproxy",
		"hvservice", "hvcrash", "hvsocketcontrol", "vmgid",
		"VMSP", "VmsProxy", "VMSNPXY",
		"vmcompute", "HvHost",
		"Vid", "wcifs",
	}
	var got []string
	for _, s := range services {
		got = append(got, s.Name)
	}
	assert.ElementsMatch(t, want, got,
		"manifest must cover exactly the service set proven on the live VM")
}

func TestVMPTransplantServices_FilesAreImageRelative(t *testing.T) {
	for _, s := range VMPTransplantServices() {
		require.NotEmpty(t, s.File, "service %s must name its backing file", s.Name)
		assert.True(t, strings.HasPrefix(s.File, "Windows/System32/"),
			"service %s: file %q must be image-relative under Windows/System32/", s.Name, s.File)
		if strings.HasSuffix(s.File, ".sys") {
			assert.True(t, strings.HasPrefix(s.File, "Windows/System32/drivers/"),
				"service %s: driver %q must live under drivers/", s.Name, s.File)
		}
	}
}

func TestVMPTransplantServices_KnownBinaries(t *testing.T) {
	byName := map[string]string{}
	for _, s := range VMPTransplantServices() {
		byName[s.Name] = s.File
	}
	assert.Equal(t, "Windows/System32/vmcompute.exe", byName["vmcompute"])
	assert.Equal(t, "Windows/System32/hvhostsvc.dll", byName["HvHost"])
	assert.Equal(t, "Windows/System32/drivers/vmswitch.sys", byName["VMSP"])
	assert.Equal(t, "Windows/System32/drivers/VmsProxyHNic.sys", byName["VMSNPXY"])
}

// Start is set explicitly for every service rather than inherited from the
// export, so the values stay visible in one reviewable place.
func TestVMPBootStart_CoversEveryServiceExplicitly(t *testing.T) {
	for _, svc := range VMPTransplantServices() {
		_, ok := vmpBootStart[svc.Name]
		assert.True(t, ok, "%s must have an explicit Start", svc.Name)
	}
}

func TestVMPBootStart_HypervisorDriversLoadAtBoot(t *testing.T) {
	for _, name := range []string{"hvservice", "vmbus"} {
		assert.Equal(t, uint32(0), vmpBootStart[name],
			"%s must be boot-start in a ramdisk WinPE", name)
	}
}

func TestVMPBootStart_OnlyProvenDriversBootStart(t *testing.T) {
	proven := map[string]uint32{"hvservice": 0, "vmbus": 0}
	for name, start := range vmpBootStart {
		if want, ok := proven[name]; ok {
			assert.Equal(t, want, start, "%s must stay boot-start", name)
			continue
		}
		assert.Equal(t, uint32(3), start,
			"%s must stay Manual until a pass2-boot run proves otherwise", name)
	}
}

func TestVMPBootStart_ValuesAreInRange(t *testing.T) {
	for name, start := range vmpBootStart {
		assert.LessOrEqual(t, start, uint32(4),
			"%s has Start=%d; valid range is 0..4", name, start)
	}
}
