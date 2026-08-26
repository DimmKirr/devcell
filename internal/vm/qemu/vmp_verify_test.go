package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verify script runs inside a booted devcell.wim and reports whether the
// transplanted stack is actually live: registered with SCM, loaded, and
// whether winload started the hypervisor.

func TestGenerateVMPVerifyScript_ProbesEveryService(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	for _, svc := range VMPTransplantServices() {
		assert.Contains(t, script, "'"+svc.Name+"'",
			"verify script must probe %s", svc.Name)
	}
}

func TestGenerateVMPVerifyScript_EmitsParseableMarkers(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	// The host asserts on these, so the shapes must not drift silently.
	for _, marker := range []string{
		"_SC=", "_START=", "HYPERVISOR_PRESENT=",
		VMPVerifyBanner, VMPVerifyComplete,
	} {
		assert.Contains(t, script, marker, "verify script must emit %q", marker)
	}
}

func TestGenerateVMPVerifyScript_ChecksExpectedStartValues(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	// hvservice and vmbus are boot-start; the script reports the value it
	// finds so a drifted transplant is visible rather than silently wrong.
	assert.Contains(t, script, "hvservice")
	assert.Contains(t, script, "vmbus")
	assert.True(t, strings.Contains(script, "reg query") || strings.Contains(script, "Get-ItemProperty"),
		"verify script must read Start from the registry")
}

// This WinPE ships no sc.exe — the image has reg.exe and driverquery.exe but
// not sc.exe, so any call to it is a terminating error that leaves the run
// silent. ServiceController comes from pwsh's own libraries and covers both
// services and kernel drivers, which matters because most of the VMP stack
// registers as a driver.
func TestGenerateVMPVerifyScript_DoesNotDependOnSCExe(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	assert.NotContains(t, script, "sc.exe",
		"sc.exe is absent from WinPE; the probe must not call it")
	assert.Contains(t, script, "System.ServiceProcess.ServiceController",
		"services must be read through ServiceController")
	assert.Contains(t, script, "GetDevices()",
		"kernel drivers do not appear in GetServices(); most of the VMP stack is drivers")
}

// build.jsonl is fed by a second virtio-serial port that only the WIM builder
// used to open, so a verify pass produced an empty file and looked like it
// had logged nothing at all.
func TestGenerateVMPVerifyScript_WritesStructuredLog(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	assert.Contains(t, script, StructuredPortName,
		"verify must open the structured port so build.jsonl is not empty")
	assert.Contains(t, script, "ConvertTo-Json",
		"structured output must be JSON")

	for _, event := range []string{"verify_start", "probe_service", "verify_complete"} {
		assert.Contains(t, script, event, "must emit a %q event", event)
	}
}

func TestVMPVerifyScriptCommand_RunsTheScript(t *testing.T) {
	cmd := VMPVerifyScriptCommand()
	assert.Contains(t, cmd, VMPVerifyScriptName,
		"the agent command must invoke the verify script by name")
}

// The script only ever runs inside a 25-minute VM boot, so a syntax error
// would cost a full run to discover. Parse it with PowerShell here instead.
func TestGenerateVMPVerifyScript_ParsesAsPowerShell(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed; skipping syntax validation")
	}

	scriptPath := filepath.Join(t.TempDir(), VMPVerifyScriptName)
	require.NoError(t, os.WriteFile(scriptPath, GenerateVMPVerifyScript(), 0644))

	check := `$errs = $null
[System.Management.Automation.Language.Parser]::ParseFile('` + scriptPath + `', [ref]$null, [ref]$errs) | Out-Null
if ($errs) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 }
Write-Output 'SYNTAX_OK'`

	out, err := exec.Command("pwsh", "-NoProfile", "-Command", check).CombinedOutput()
	require.NoError(t, err, "verify script has PowerShell syntax errors:\n%s", out)
	assert.Contains(t, string(out), "SYNTAX_OK")
}

func TestGenerateVMPVerifyScript_RegistersRuntimeServices(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	// Vid and storvsp are INF-backed — WinPE's own drvload installs them
	// and creates their service keys, no hive patching involved.
	assert.Contains(t, script, `drvload`)
	assert.Contains(t, script, `wvid.inf`)
	assert.Contains(t, script, `wstorvsp.inf`)

	// The two extra vmswitch service keys a VMP-enabled Windows carries.
	// This WinPE has no sc.exe, so they are typed registry values —
	// reviewable, from the live reference inventory.
	for _, svc := range []string{"VMSVSF", "VMSVSP"} {
		assert.Contains(t, script, svc)
	}
	assert.Contains(t, script, `System32\drivers\vmswitch.sys`)

	// vmwp/HCS read the Virtualization config root.
	assert.Contains(t, script, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization`)
	assert.Contains(t, script, "CurrentVmVersion")

	// Registration reports its outcome so the host can assert on it.
	assert.Contains(t, script, "REG_DRVLOAD_VID=")
	assert.Contains(t, script, "REG_DRVLOAD_STORVSP=")
	assert.Contains(t, script, "REG_VMSWITCH_KEYS=")
	assert.Contains(t, script, "REG_VIRT_ROOT=")
}

// Reporting a service's registry Start value proves the hive is right; it
// says nothing about whether the file behind it is loadable. Attempting the
// start and surfacing the raw Win32 code is what distinguishes a valid
// binary from a component-store delta stub, and it costs one boot instead
// of a full HCS pass.
func TestGenerateVMPVerifyScript_ReportsWin32StartErrors(t *testing.T) {
	script := string(GenerateVMPVerifyScript())

	assert.Contains(t, script, "_TRYSTART=")
	assert.Contains(t, script, "win32=")

	// ServiceController surfaces the inner Win32Exception; Start-Service
	// flattens it into an unusable "cannot be started" sentence.
	assert.Contains(t, script, "ServiceController")
	assert.Contains(t, script, "NativeErrorCode")
}
