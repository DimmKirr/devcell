package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateWimBuilderScript_ContainsAllOps(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: append(HyperVPrepOps(), OpenSSHPrepOps()...),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// Must contain DISM commands for each feature/capability.
	assert.Contains(t, script, "Microsoft-Hyper-V")
	assert.Contains(t, script, "VirtualMachinePlatform")
	assert.Contains(t, script, "OpenSSH.Server~~~~0.0.1.0")
	assert.Contains(t, script, "OpenSSH.Client~~~~0.0.1.0")

	// Feature ops use /Enable-Feature, capability ops use /Add-Capability.
	assert.Contains(t, script, "/Enable-Feature /FeatureName:Microsoft-Hyper-V")
	assert.Contains(t, script, "/Add-Capability /CapabilityName:OpenSSH.Server")

	// Must use offline servicing (/Image:) not /Online.
	assert.NotContains(t, script, "/Online")
	assert.Contains(t, script, "/Image:C:\\mnt\\boot")

	// Must reference install.wim as the source.
	assert.Contains(t, script, "/Source:C:\\mnt\\install")

	// Must produce devcell.wim output.
	assert.Contains(t, script, "devcell.wim")

	// Must write success/fail marker.
	assert.Contains(t, script, WimBuilderDoneFile)
}

func TestGenerateWimBuilderScript_DefaultImageIndex(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Feature: "TestFeature"}},
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Index:2")
}

func TestGenerateWimBuilderScript_CustomImageIndex(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:           []WimPrepOp{{Feature: "TestFeature"}},
		WimImageIndex: 1,
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Index:1")
}

func TestGenerateWimBuilderScript_PackageOp(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{
			{Package: `Windows\WinSxS\Manifests\amd64_some_package.mum`},
		},
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Add-Package /PackagePath:C:\\mnt\\install\\")
	assert.Contains(t, script, "amd64_some_package.mum")
}

func TestGenerateWimBuilderScript_CRLFLineEndings(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		require.True(t, strings.HasSuffix(line, "\r"),
			"line %d must end with \\r\\n: %q", i+1, line)
	}
}

func TestGenerateWimBuilderScript_ErrorHandling(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// Must handle missing Windows ISO.
	assert.Contains(t, script, "sources\\install.wim")
	assert.Contains(t, script, "Windows ISO not found")

	// Must handle missing boot.wim on shared volume.
	assert.Contains(t, script, "boot.wim not found")

	// Must handle mount failures.
	assert.Contains(t, script, "Failed to mount boot.wim")

	// Must handle commit failure.
	assert.Contains(t, script, "Failed to commit boot.wim")
}

func TestGenerateWimBuilderScript_InternetCheckAndCapabilityRetry(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: OpenSSHPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// Internet check
	assert.Contains(t, script, "Checking internet connectivity")
	assert.Contains(t, script, "ping -n 1 -w 3000")
	assert.Contains(t, script, "set HAS_INET=")

	// Capability ops try offline first, then Windows Update
	assert.Contains(t, script, "/LimitAccess")
	assert.Contains(t, script, "failed offline")
	assert.Contains(t, script, "Retrying")
	assert.Contains(t, script, "via Windows Update")
	assert.Contains(t, script, "no internet")
}

func TestGenerateWimBuilderScript_DiskpartWorkVolume(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "diskpart /s")
	assert.Contains(t, script, "format fs=ntfs quick")
	assert.Contains(t, script, "assign letter=C")
	assert.Contains(t, script, "diskpart failed")
}

func TestHyperVPrepOps(t *testing.T) {
	ops := HyperVPrepOps()
	require.Len(t, ops, 2)
	assert.Equal(t, "Microsoft-Hyper-V", ops[0].Feature)
	assert.Equal(t, "VirtualMachinePlatform", ops[1].Feature)
}

func TestOpenSSHPrepOps(t *testing.T) {
	ops := OpenSSHPrepOps()
	require.Len(t, ops, 2)
	assert.Equal(t, "OpenSSH.Server~~~~0.0.1.0", ops[0].Capability)
	assert.Equal(t, "OpenSSH.Client~~~~0.0.1.0", ops[1].Capability)
}

func TestWimBuilderScriptCommand(t *testing.T) {
	cmd := WimBuilderScriptCommand()
	assert.Contains(t, cmd, WimBuilderScriptName)
	assert.Contains(t, cmd, "%DEVCELL_VOL%")
}
