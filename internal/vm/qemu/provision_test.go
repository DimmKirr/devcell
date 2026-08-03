package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateSSHConfigScript_ContainsPubKey(t *testing.T) {
	script := GenerateSSHConfigScript("ssh-ed25519 AAAA... user@host")
	assert.Contains(t, script, "ssh-ed25519 AAAA... user@host")
	assert.Contains(t, script, "authorized_keys")
	assert.Contains(t, script, "DefaultShell")
	assert.Contains(t, script, "sshd")
}

// Run 20260731T221844 (run 8): bootstrap authorizes the user key at first
// logon and locks authorized_keys to (R) for the user. Configure SSH then
// re-appended the same key unconditionally and died on PermissionDenied —
// all three attempts, build aborted after a clean install. The step must be
// idempotent: append only when the key is missing, and take write access
// back before touching a file bootstrap deliberately made read-only.
func TestGenerateSSHConfigScript_IdempotentOverBootstrapsLockedKeyFile(t *testing.T) {
	key := "ssh-ed25519 AAAA... user@host"
	script := GenerateSSHConfigScript(key)

	assert.Contains(t, script, "-notcontains",
		"presence must be checked per key line — the CLI passes several newline-joined keys")
	assert.Contains(t, script, "+ ':(F)'",
		"must regain write access before appending: bootstrap locks the file to (R)")
	assert.Contains(t, script, "+ ':(R)'",
		"must relock after any append — sshd refuses loose ACLs")
	// PowerShell eats the colon in "$env:USERNAME:(F)" — the variable path
	// swallows it and icacls receives a bare "(F)": `Invalid parameter "(F)"`,
	// L1 check 2026-07-31. Concatenation (bootstrap's form) is the fix.
	assert.NotContains(t, script, `$env:USERNAME:(`,
		"inline interpolation expands to nothing — use ($env:USERNAME + ':(R)')")
	// The unconditional append is exactly what failed; it must be gone.
	assert.NotRegexp(t, `(?m)^Add-Content`, script,
		"a bare top-of-line Add-Content is the unconditional append that run 8 died on")
}

func TestGenerateCreateSessionUserScript_ContainsUsername(t *testing.T) {
	script := GenerateCreateSessionUserScript("devuser", "s3cret")
	assert.Contains(t, script, "devuser")
	assert.Contains(t, script, "s3cret")
	assert.Contains(t, script, "New-LocalUser")
	assert.Contains(t, script, "Administrators")
}

func TestGenerateDevToolsScript_ContainsGit(t *testing.T) {
	script := GenerateDevToolsScript()
	assert.Contains(t, script, "git")
	assert.Contains(t, script, "Git")
}

// Run 20260801T001059 (run 9): winget existed, printed "An unexpected error
// occurred" and installed nothing — and the step still reported ok, because
// winget's failure was discarded and Chocolatey only kicked in when winget
// was *absent*. The ssh-able boot then proved it: `git: NOT INSTALLED`.
// The step must verify its outcome and fall back on failure, not just on
// absence — and a missing git at the end must fail the step.
func TestGenerateDevToolsScript_FallsBackWhenWingetFailsAndVerifiesGit(t *testing.T) {
	script := GenerateDevToolsScript()

	assert.Contains(t, script, "Get-Command git",
		"the step must verify git actually landed, not trust the installer's exit")
	assert.Contains(t, script, "choco install",
		"Chocolatey is the fallback when winget fails, not only when it is missing")
	assert.Contains(t, script, "throw",
		"no git at the end of the step must fail the step")
	assert.NotContains(t, script, "2>$null",
		"discarding installer stderr is how run 9 shipped a template without git")
}

func TestGenerateProjectMountScript(t *testing.T) {
	script := GenerateProjectMountScript("myproject", "P")
	assert.Contains(t, script, "myproject")
	assert.Contains(t, script, "USERPROFILE")
}

func TestGenerateEnvSetupScript(t *testing.T) {
	vars := map[string]string{
		"DEVCELL_CELL_NAME": "main",
		"GOPATH":            `C:\Users\dev\go`,
	}
	script := GenerateEnvSetupScript(vars)
	assert.Contains(t, script, "DEVCELL_CELL_NAME")
	assert.Contains(t, script, "main")
	assert.Contains(t, script, "SetEnvironmentVariable")
}

func TestDefaultProvisionSteps(t *testing.T) {
	steps := DefaultProvisionSteps("ssh-ed25519 AAAA...", "devcell", "devcell")
	assert.Len(t, steps, 3)
	assert.Equal(t, "Configure SSH", steps[0].Name)
	assert.Equal(t, "Create session user", steps[1].Name)
	assert.Equal(t, "Install dev tools", steps[2].Name)
	assert.Contains(t, steps[0].Script, "ssh-ed25519 AAAA...")
	assert.Greater(t, steps[0].Retries, 0)
}
