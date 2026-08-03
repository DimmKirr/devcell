package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guest tree is REAL PowerShell (no Go templating): it is linted,
// runnable standalone on a guest, and free of the interpolation bug class
// that cost four multi-hour runs (CELL-402).
func TestGuestModule_ExportsTheSharedHelpers(t *testing.T) {
	mod, err := GuestFile("Devcell.psm1")
	require.NoError(t, err, "the module must ship in the embedded guest tree")
	s := string(mod)

	for _, fn := range []string{
		"function Write-DevcellLog",
		"function Invoke-DevcellStep",
		"function Get-DevcellControlVolume",
		"function Assert-DevcellExitCode",
	} {
		assert.Contains(t, s, fn, "the module must define %s", fn)
	}
	assert.Contains(t, s, "Add-Content",
		"per-line append is what makes a running stage readable (proven 20260803T073911)")
	assert.NotContains(t, s, "{{", "guest code must contain no Go template syntax")
}

// Every stage script is a real parameterised PowerShell file.
func TestGuestStage_IsRealPowerShellWithParams(t *testing.T) {
	src, err := GuestFile("stages/wsl2-enable.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.True(t, strings.HasPrefix(strings.TrimSpace(s), "#") || strings.Contains(s, "param("),
		"a stage script starts with a comment header or a param block")
	assert.Contains(t, s, "Import-Module", "stages consume the shared module")
	assert.Contains(t, s, "Invoke-DevcellStep", "stage work is wrapped so it is timed and logged")
	assert.NotContains(t, s, "{{", "no Go template syntax in guest code")
}

// The control volume must carry the module and every stage script the stage
// table references — a missing file must fail the build, not a guest an hour
// into a run.
func TestGuestPayload_CarriesModuleAndEveryReferencedStage(t *testing.T) {
	payload, err := GuestPayload()
	require.NoError(t, err)

	assert.Contains(t, payload, "/devcell/Devcell.psm1")
	for _, st := range devEnvStages("dmitry", "devcell", "Z:") {
		if st.ScriptFile == "" {
			continue // still on the legacy rendered path
		}
		assert.Contains(t, payload, "/devcell/stages/"+st.ScriptFile,
			"stage %q references %s, which must ship on the control volume", st.Name, st.ScriptFile)
	}
}

// Resumed runs boot from a checkpoint that already carries the distro.
// Checking the registry FIRST is what makes those runs cheap: run
// 20260803T075624 spent 84s on the releases API and a 577MB asset check
// before discovering the distro was already imported.
func TestNixOSImportStage_ChecksRegistryBeforeNetwork(t *testing.T) {
	src, err := GuestFile("stages/nixos-import.ps1")
	require.NoError(t, err)
	s := string(src)

	registryAt := strings.Index(s, "wsl.exe --list --quiet")
	apiAt := strings.Index(s, "api.github.com")
	require.Positive(t, registryAt, "the stage must consult the WSL registry")
	require.Positive(t, apiAt, "the stage must know how to fetch the image")
	assert.Less(t, registryAt, apiAt,
		"the registry check must come BEFORE any network call")

	assert.Contains(t, s, "if (-not $registered)",
		"fetch and import must be skipped entirely when the distro exists")
	assert.Contains(t, s, "nixos.aarch64.wsl", "ARM64 guests need the aarch64 image")
}

// Stages that declare a reboot must be able to withdraw it: on a resumed
// run the work is usually already done, and a TCG reboot costs ~8 minutes.
func TestRebootingStages_CanReportNoChange(t *testing.T) {
	for _, f := range []string{"stages/wsl-engine-install.ps1", "stages/wsl2-enable.ps1"} {
		src, err := GuestFile(f)
		require.NoError(t, err)
		assert.Contains(t, string(src), NoChangeMarker,
			"%s declares RebootAfter, so it must signal a no-op run", f)
	}
}

// The home-manager stage is the last one in the default span still rendered
// from Go, and the only stage that has never executed in ANY recorded run —
// the riskiest code in the pipeline living in its most fragile form. As a real
// .ps1 it is covered by the host-side pwsh parser gate before its first run.
func TestHomeManagerStage_IsFileBacked(t *testing.T) {
	var stage GuestStage
	for _, st := range DevEnvStages("dmitry", "devcell", "Z:") {
		if st.Name == "activate nixhome home-manager" {
			stage = st
		}
	}
	require.NotEmpty(t, stage.Name, "stage not found")

	assert.Equal(t, "home-manager.ps1", stage.ScriptFile,
		"the stage must run a real PowerShell file, not Go-rendered text")
	assert.Empty(t, stage.Script, "the legacy rendered payload must be gone from the table")
	assert.Equal(t, WSLDistroUser, stage.Args["User"])
	assert.Equal(t, NixOSWSLDistro, stage.Args["Distro"])
	assert.Equal(t, "Z:", stage.Args["Drive"])
}

// The details below each cost a multi-hour run to isolate. They are asserted
// on the FILE so a future edit cannot quietly drop one.
func TestHomeManagerStage_KeepsTheHardWonInvocationDetails(t *testing.T) {
	src, err := GuestFile("stages/home-manager.ps1")
	require.NoError(t, err)
	s := string(src)

	// Run 20260802T112212: inner double quotes do not survive
	// PowerShell -> wsl.exe -> sh -lc, and nix then reports "no subcommand
	// specified". The features must travel as REPEATED flags.
	assert.Equal(t, 2, strings.Count(s, "--extra-experimental-features"),
		"nix-command and flakes must be two separate flags, never one quoted pair")
	// Run 20260802: nix is on PATH only via /etc/profile in NixOS-WSL, so a
	// bare `wsl -- nix` exits 127 on a perfectly working distro.
	assert.Contains(t, s, "/bin/sh -lc", "every nix call needs a login shell")
	assert.Contains(t, s, "release-26.05",
		"the home-manager runner is pinned to the branch matching this NixOS")
	// The repo path must be derived from the $User PARAMETER, never baked in:
	// the whole point of the file-backed stage is that the distro user is
	// passed, not interpolated by Go.
	assert.Contains(t, s, `"/home/$User/dev/dimmkirr/devcell"`,
		"the repo path is built from the parameter the caller passes")
}

// The share is the stage's only external dependency and its failure was
// SWALLOWED: `mount ... 2>/dev/null; true` meant a missing Z: surfaced ~30
// minutes later as an unexplained `ls .../nixhome` error instead of at the
// mount itself.
func TestHomeManagerStage_DoesNotSwallowTheMountFailure(t *testing.T) {
	src, err := GuestFile("stages/home-manager.ps1")
	require.NoError(t, err)
	s := string(src)

	mountAt := strings.Index(s, "drvfs")
	require.Positive(t, mountAt, "the stage must still mount the share")
	assert.NotContains(t, s[mountAt:mountAt+200], "2>/dev/null",
		"a failed mount must be reported, not discarded")
	assert.Contains(t, s, "Assert-DevcellExitCode",
		"native commands do not throw — only $LASTEXITCODE knows they failed")
}

// On a resumed run the distro is already imported, and booting the utility
// VM just to print nixos-version costs ~7 minutes under TCG while proving
// nothing the later "verify nix" stage does not. The import stage must do
// the minimum that only it can do: register the distro.
func TestNixOSImportStage_SkipsRedundantBootWhenAlreadyRegistered(t *testing.T) {
	src, err := GuestFile("stages/nixos-import.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.Contains(t, s, "if ($registered) {",
		"an already-registered distro must short-circuit")
	skipAt := strings.Index(s, "if ($registered) {")
	proveAt := strings.Index(s, "prove the freshly imported")
	require.Positive(t, proveAt)
	assert.Less(t, skipAt, proveAt,
		"the short-circuit must come before the verification boot")
	assert.Contains(t, s, NoChangeMarker, "a no-op import must not cost a reboot either")
}
