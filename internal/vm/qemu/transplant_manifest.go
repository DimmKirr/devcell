package qemu

// TransplantService names one service to transplant from a donor source
// into boot.wim: the Services registry key to clone and the binary backing
// it.
//
// The set is the ground truth from a live ARM64 Windows 11 VM running WSL2
// with only VirtualMachinePlatform enabled (Microsoft-Hyper-V disabled).
// DISM cannot enable these features in WinPE (CBS requires
// Microsoft-Windows-Foundation-Package as parent; boot.wim has
// Microsoft-Windows-WinPE-Package), so the transplant bypasses CBS: clone
// the service keys, copy the files. SCM and winload read only the registry
// and the file signatures.
//
// The donor is an install.wim with VirtualMachinePlatform enabled via DISM,
// so all binaries are materialized at their System32 paths.
type TransplantService struct {
	Name string // Services key name, exact registry casing
	File string // path in boot.wim and in the donor (forward slashes)
}

// VMPTransplantServices returns the VirtualMachinePlatform service set:
// the minimal stack WSL2 needs, per the live-VM reference.
func VMPTransplantServices() []TransplantService {
	return []TransplantService{
		{Name: "vmbus", File: "Windows/System32/drivers/vmbus.sys"},
		{Name: "vmbusr", File: "Windows/System32/drivers/vmbusr.sys"},
		{Name: "vmbusproxy", File: "Windows/System32/drivers/vmbusproxy.sys"},
		{Name: "hvservice", File: "Windows/System32/drivers/hvservice.sys"},
		{Name: "hvcrash", File: "Windows/System32/drivers/hvcrash.sys"},
		{Name: "hvsocketcontrol", File: "Windows/System32/drivers/hvsocketcontrol.sys"},
		{Name: "vmgid", File: "Windows/System32/drivers/vmgid.sys"},
		{Name: "VMSP", File: "Windows/System32/drivers/vmswitch.sys"},
		{Name: "VmsProxy", File: "Windows/System32/drivers/VmsProxy.sys"},
		{Name: "VMSNPXY", File: "Windows/System32/drivers/VmsProxyHNic.sys"},
		{Name: "vmcompute", File: "Windows/System32/vmcompute.exe"},
		{Name: "HvHost", File: "Windows/System32/hvhostsvc.dll"},
		{Name: "Vid", File: "Windows/System32/drivers/Vid.sys"},
		{Name: "wcifs", File: "Windows/System32/drivers/wcifs.sys"},
	}
}

// vmpBootStart sets Start for every transplanted service: hvservice and
// vmbus at boot start so a ramdisk WinPE — which has no PnP-driven service
// start — brings the hypervisor up itself, and everything else Manual.
//
// It is exhaustive rather than inheriting from the export so the values are
// visible and reviewable in one place; the exported Start is easy to miss
// and several entries are non-Manual there.
//
// This exact configuration booted and launched the hypervisor in the
// 2026-08-22 pass2-boot run (HYPERVISOR_PRESENT=True). The reference
// machine's own values (VmsProxy=0, VMSNPXY=0, hvsocketcontrol=1, VMSP=2)
// were tried and hung winload before user mode: those drivers bind to
// devices that never enumerate under WinPE. Do not promote a service here
// without a green pass2-boot run to back it.
//
// Start values: 0=Boot, 1=System, 2=Auto, 3=Manual, 4=Disabled.
var vmpBootStart = map[string]uint32{
	"hvservice":       0, // hypervisor driver: proven to load in WinPE (2026-08-14)
	"vmbus":           0, // VM bus: proven to load in WinPE (2026-08-14)
	"Vid":             3, // partition manager: Start=0 hangs winload under TCG (2026-08-25)
	"vmbusr":          3,
	"VmsProxy":        3,
	"VMSNPXY":         3,
	"hvsocketcontrol": 3,
	"vmbusproxy":      3,
	"hvcrash":         3,
	"vmgid":           3,
	"VMSP":            3,
	"vmcompute":       3,
	"HvHost":          3,
	"wcifs":           3, // container isolation FS: vmcompute DependOnService
}
