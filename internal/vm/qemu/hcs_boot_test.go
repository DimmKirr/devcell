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

// The pass3 script boots a nested VM through HCS inside a booted
// devcell.wim — the runtime proof that the transplanted VMP stack hosts
// real VMs, not just registers services.

func TestGenerateHCSBootScript_RunsTheLadder(t *testing.T) {
	script := string(GenerateHCSBootScript())

	// Same runtime registration as the verify pass — each boot is a fresh
	// ramdisk, nothing persists between passes.
	assert.Contains(t, script, "drvload")
	assert.Contains(t, script, "wvid.inf")

	// vmcompute must be up before any HCS call can land.
	assert.Contains(t, script, "Start-Service")
	assert.Contains(t, script, "vmcompute")
	assert.Contains(t, script, "VMCOMPUTE_START=")

	// The nested-VM smoke test itself, shipped on the shared volume.
	assert.Contains(t, script, "hcsboot.exe")
	assert.Contains(t, script, "HCSBOOT_EXIT=")

	// vmms leg is tolerated-absent: it only exists for the thumbnail.
	assert.Contains(t, script, "vmms.exe")
	assert.Contains(t, script, "mofcomp")
	assert.Contains(t, script, "MOFCOMP_EXIT=")
	assert.Contains(t, script, "VMMS_START=")
	assert.Contains(t, script, "GetVirtualSystemThumbnailImage")
	assert.Contains(t, script, "THUMBNAIL=")

	assert.Contains(t, script, HCSBootBanner)
	assert.Contains(t, script, HCSBootComplete)

	// CRLF throughout — this runs in a Windows guest.
	for i, line := range strings.Split(string(script), "\n") {
		if line == "" {
			continue
		}
		assert.True(t, strings.HasSuffix(line, "\r"),
			"line %d lacks CR: %q", i+1, line)
	}
}

func TestHCSBootScriptCommand_RunsTheScript(t *testing.T) {
	cmd := HCSBootScriptCommand()
	assert.Contains(t, cmd, HCSBootScriptName)
}

func TestGenerateHCSBootScript_ParsesAsPowerShell(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed; skipping syntax validation")
	}

	scriptPath := filepath.Join(t.TempDir(), HCSBootScriptName)
	require.NoError(t, os.WriteFile(scriptPath, GenerateHCSBootScript(), 0644))

	check := `$errs = $null
[System.Management.Automation.Language.Parser]::ParseFile('` + scriptPath + `', [ref]$null, [ref]$errs) | Out-Null
if ($errs) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 }
Write-Output 'SYNTAX_OK'`

	out, err := exec.Command("pwsh", "-NoProfile", "-Command", check).CombinedOutput()
	require.NoError(t, err, "hcs-boot script has PowerShell syntax errors:\n%s", out)
	assert.Contains(t, string(out), "SYNTAX_OK")
}
