package qemu

import (
	"strings"
)

const (
	// VMPVerifyScriptName is the verify script's filename on the agent volume.
	VMPVerifyScriptName = `devcell-vmp-verify.ps1`

	// VMPVerifyBanner and VMPVerifyComplete bracket the output. The host
	// requires both: the banner alone would pass even if the script died
	// halfway through.
	VMPVerifyBanner   = `=== DEVCELL VMP VERIFY ===`
	VMPVerifyComplete = `=== DEVCELL VMP VERIFY COMPLETE ===`
)

// VMPVerifyScriptCommand is the agent command line that runs the verifier.
func VMPVerifyScriptCommand() string {
	return `& "$DevcellVol\` + VMPVerifyScriptName + `" $DevcellVol`
}

// GenerateVMPVerifyScript produces a script that runs inside a booted
// devcell.wim and reports whether the transplanted VirtualMachinePlatform
// stack is actually live.
//
// It first registers the runtime pieces (drvload the transplanted INFs,
// create the vmswitch service keys and the Virtualization config root),
// then asks the questions only a booted image can answer: does SCM
// recognise each cloned key, did the boot-start drivers load, and did
// winload start the hypervisor.
//
// Output is KEY=VALUE lines so the host can assert on them:
//
//	REG_DRVLOAD_VID=<exit> / REG_DRVLOAD_STORVSP=<exit>
//	REG_VMSWITCH_KEYS=OK|<error> / REG_VIRT_ROOT=OK|<error>
//	<service>_SC=RUNNING|STOPPED|NOT_EXIST|UNKNOWN
//	<service>_START=<n>|ABSENT
//	HYPERVISOR_PRESENT=True|False|UNKNOWN
func GenerateVMPVerifyScript() []byte {
	names := make([]string, 0, len(VMPTransplantServices()))
	for _, svc := range VMPTransplantServices() {
		names = append(names, svc.Name)
	}

	data := struct {
		StructPort string
		Banner     string
		Complete   string
		Services   []string
	}{
		StructPort: StructuredPortName,
		Banner:     VMPVerifyBanner,
		Complete:   VMPVerifyComplete,
		Services:   names,
	}

	out := renderTemplate("vmp-verify.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}
