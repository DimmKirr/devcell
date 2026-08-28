package qemu

// Tests that lived in test files whose subjects moved to go-winkit, but whose
// own subjects (Spec defaults, the dev-tools provisioning script) stay with
// the engine.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyDefaults_SSHUserFollowsHostUser(t *testing.T) {
	// The install test connects as Spec.SSHUser; it must match the account
	// the answer file actually creates.
	t.Setenv("USER", "dmitry")
	s := Spec{DiskPath: "/tmp/d.qcow2", FirmwarePath: "/tmp/f.fd"}
	s.ApplyDefaults()
	assert.Equal(t, "dmitry", s.SSHUser)
}

// The Chocolatey openssh package is deprecated by its own maintainers: "The
// primary Microsoft distribution mechanism for OpenSSH is through Windows."
// We install OpenSSH properly (capability, then Microsoft's signed
// Win32-OpenSSH release), so pulling it again from Chocolatey would install a
// second, stale copy over the working one.
func TestGenerateDevToolsScript_DoesNotInstallDeprecatedChocolateyOpenSSH(t *testing.T) {
	script := GenerateDevToolsScript()

	assert.NotContains(t, script, "choco install -y git openssh",
		"openssh must not come from the deprecated Chocolatey package")
	assert.Contains(t, script, "choco install", "git still comes from Chocolatey")
}
