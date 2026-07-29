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
