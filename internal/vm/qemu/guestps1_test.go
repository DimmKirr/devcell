package qemu

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Syntax gate for the guest PowerShell tree (CELL-402).
//
// Go-templated PowerShell cost four multi-hour runs: lost quotes turned a nix
// invocation into "no subcommand", an interpolated colon broke icacls, and
// quote stripping killed two more. Every one of those was a SYNTAX fault that
// a parser would have caught in milliseconds — but the only parser that
// counts is PowerShell's own, and the guest was the only place it existed.
// So each fault cost a full Windows boot under TCG to discover.
//
// Now that stages are real .ps1 files, pwsh on the HOST can parse them before
// any VM starts. This is the cheapest deterministic check in the project and
// it gates the 12 stages still to convert.

// pwshPath returns the host PowerShell binary, or "" when none is installed.
// Absence is a skip rather than a failure: the suite must stay green on a
// machine that has no pwsh, while the check runs wherever one exists.
func pwshPath() string {
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p
	}
	// The devcell container installs it into the default nix profile.
	const nixPwsh = "/nix/var/nix/profiles/default/bin/pwsh"
	if _, err := os.Stat(nixPwsh); err == nil {
		return nixPwsh
	}
	return ""
}

// parsePowerShell returns the parse errors pwsh reports for one file, or an
// empty string when it parses clean.
func parsePowerShell(t *testing.T, pwsh, file string) string {
	t.Helper()
	// The path is inlined rather than passed as an argument: `pwsh -Command
	// <script> <arg>` does NOT populate $args — -Command consumes the rest of
	// the line as command text, so the script saw an empty path and pwsh sat
	// waiting on stdin. Single quotes with doubling is PowerShell's own
	// escaping, and the paths here are test-owned temp dirs.
	script := `
$tokens = $null; $errs = $null
[System.Management.Automation.Language.Parser]::ParseFile(` + quotePowerShell(file) + `, [ref]$tokens, [ref]$errs) | Out-Null
if ($errs -and $errs.Count) {
    $errs | ForEach-Object { "line $($_.Extent.StartLineNumber): $($_.Message)" }
}`
	cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "pwsh itself failed on %s: %s", file, out)
	return strings.TrimSpace(string(out))
}

// Every embedded guest script must parse. A stage that cannot even be parsed
// would fail an hour into a TCG boot with a message about the wrong thing.
func TestGuestPowerShell_AllScriptsParse(t *testing.T) {
	pwsh := pwshPath()
	if pwsh == "" {
		t.Skip("no pwsh on this host: install it (nix profile add nixpkgs#powershell) to run the syntax gate")
	}

	dir := t.TempDir()
	checked := 0
	require.NoError(t, fs.WalkDir(guestFS, "guest", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := path.Ext(p); ext != ".ps1" && ext != ".psm1" {
			return nil
		}
		data, readErr := guestFS.ReadFile(p)
		require.NoError(t, readErr)

		// Parse from a real file: ParseFile is the API the guest effectively
		// uses when it invokes a script by path, and it reports the line
		// numbers a failure will cite.
		out := filepath.Join(dir, filepath.Base(p))
		require.NoError(t, os.WriteFile(out, data, 0o644))

		if errs := parsePowerShell(t, pwsh, out); errs != "" {
			t.Errorf("%s does not parse:\n%s", p, errs)
		}
		checked++
		return nil
	}))

	assert.GreaterOrEqual(t, checked, 6,
		"the guest tree should hold the shared module plus one script per converted stage")
	t.Logf("parsed %d guest PowerShell files with %s", checked, pwsh)
}

// A gate that cannot fail is not a gate. The four historical faults were all
// broken quoting, so prove the checker actually rejects that shape rather
// than reporting green because it parsed nothing.
func TestGuestPowerShell_ParserRejectsBrokenQuoting(t *testing.T) {
	pwsh := pwshPath()
	if pwsh == "" {
		t.Skip("no pwsh on this host")
	}

	bad := filepath.Join(t.TempDir(), "broken.ps1")
	// An unterminated string: exactly what a lost quote leaves behind.
	require.NoError(t, os.WriteFile(bad,
		[]byte("Write-Output 'unterminated\nwsl -d NixOS -- nix --version\n"), 0o644))

	errs := parsePowerShell(t, pwsh, bad)
	assert.NotEmpty(t, errs,
		"the parser must reject an unterminated string — otherwise the gate is vacuous")
	t.Logf("parser correctly rejected broken quoting:\n%s", errs)
}
