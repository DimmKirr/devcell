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

// The pass4 script registers the transplanted WSL engine at boot and runs
// the first WSL command. Files alone are inert — WinPE has no Windows
// Installer, so the script does the MSI's ServiceInstall/registration work
// with standard tools: New-Service and regsvr32 executing the vendor's own
// declared data.

func TestGenerateWSLBootScript_RegistersTheEngine(t *testing.T) {
	script := string(GenerateWSLBootScript())

	// Each pass boots a fresh ramdisk: the VMP runtime registration ladder
	// runs again before anything WSL.
	assert.Contains(t, script, "drvload")
	assert.Contains(t, script, "wvid.inf")
	assert.Contains(t, script, "vmcompute")
	assert.Contains(t, script, "VMCOMPUTE_START=")

	// WSLService, exactly as the MSI's ServiceInstall row declares it:
	// own-process service, demand start, LocalSystem.
	assert.Contains(t, script, "New-Service")
	assert.Contains(t, script, "WSLService")
	assert.Contains(t, script, "wslservice.exe")
	assert.Contains(t, script, "WSLSERVICE_REGISTER=")
	assert.Contains(t, script, "WSLSERVICE_START=")

	// The COM proxy stub self-registers — its DllRegisterServer writes the
	// ILxssUserSession interface entries the MSI Registry table carries.
	assert.Contains(t, script, "regsvr32")
	assert.Contains(t, script, "wslserviceproxystub.dll")
	assert.Contains(t, script, "REGSVR32_PROXYSTUB=")

	// No HNS in WinPE — networking must be off or wslservice wedges.
	assert.Contains(t, script, ".wslconfig")
	assert.Contains(t, script, "networkingMode=none")

	// The engine dir joins PATH so wsl.exe resolves to the MSI payload.
	assert.Contains(t, script, `Program Files\WSL`)

	// First contact: --status proves wslservice answers over COM.
	assert.Contains(t, script, "--status")
	assert.Contains(t, script, "WSL_STATUS_EXIT=")

	// The distro proof, gated on the rootfs being shipped on the volume:
	// import alpine and run uname inside it — "Linux" in the output is the
	// whole point of the exercise.
	assert.Contains(t, script, "--import")
	assert.Contains(t, script, WSLDistroName)
	assert.Contains(t, script, WSLRootfsVolName)
	assert.Contains(t, script, "WSL_IMPORT_EXIT=")
	assert.Contains(t, script, "uname")
	assert.Contains(t, script, "WSL_UNAME=")

	assert.Contains(t, script, WSLBootBanner)
	assert.Contains(t, script, WSLBootComplete)

	// CRLF throughout — this runs in a Windows guest.
	for i, line := range strings.Split(script, "\n") {
		if line == "" {
			continue
		}
		assert.True(t, strings.HasSuffix(line, "\r"),
			"line %d lacks CR: %q", i+1, line)
	}
}

func TestWSLBootScriptCommand_RunsTheScript(t *testing.T) {
	cmd := WSLBootScriptCommand()
	assert.Contains(t, cmd, WSLBootScriptName)
}

func TestGenerateWSLBootScript_ParsesAsPowerShell(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed; skipping syntax validation")
	}

	scriptPath := filepath.Join(t.TempDir(), WSLBootScriptName)
	require.NoError(t, os.WriteFile(scriptPath, GenerateWSLBootScript(), 0644))

	check := `$errs = $null
[System.Management.Automation.Language.Parser]::ParseFile('` + scriptPath + `', [ref]$null, [ref]$errs) | Out-Null
if ($errs) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 }
Write-Output 'SYNTAX_OK'`

	out, err := exec.Command("pwsh", "-NoProfile", "-Command", check).CombinedOutput()
	require.NoError(t, err, "wsl-boot script has PowerShell syntax errors:\n%s", out)
	assert.Contains(t, string(out), "SYNTAX_OK")
}
