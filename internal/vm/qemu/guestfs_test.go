package qemu

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
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

// The nix-daemon helper is a shell script that nix-verify invokes inside WSL
// when systemd socket activation is broken (WSL#13236). It must ship on the
// control volume so the stage can reference it by path instead of encoding it
// inline (which was the bug class that broke every quoting attempt).
func TestGuestPayload_CarriesNixDaemonHelper(t *testing.T) {
	payload, err := GuestPayload()
	require.NoError(t, err)

	const key = "/devcell/helpers/start-nix-daemon.sh"
	assert.Contains(t, payload, key,
		"the nix-daemon helper must ship on the control volume")
	sh := string(payload[key])
	assert.Contains(t, sh, "nix-daemon", "the helper starts the daemon")
	assert.Contains(t, sh, "SOCKET_OK", "the helper reports socket readiness")
	assert.Contains(t, sh, "STORE_EXIT", "the helper reports the store-write result")
	assert.Contains(t, sh, "TARGET_USER", "the helper accepts the WSL user as an argument")
}

// The user declaration must override NixOS-WSL's stock configuration.nix,
// which pins wsl.defaultUser = "nixos" at normal priority. Without mkForce
// nixos-rebuild dies on "conflicting definition values" (run 20260804,
// first-ever E2E of this stage) and the distro never adopts the host user.
func TestAdoptUserStage_ForcesDefaultUserOverride(t *testing.T) {
	src, err := GuestFile("stages/wsl-adopt-user.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.Contains(t, s, "lib.mkForce",
		"wsl.defaultUser must be forced past the stock config's own definition")
	assert.Regexp(t, `wsl\.defaultUser\s*=\s*lib\.mkForce`, s,
		"the force must apply to wsl.defaultUser specifically")
}

// nix-verify references the helper by its control volume path, not inline.
func TestNixVerifyStage_UsesControlVolumeHelper(t *testing.T) {
	src, err := GuestFile("stages/nix-verify.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.Contains(t, s, "start-nix-daemon.sh",
		"the daemon fallback must reference the shipped helper, not inline the script")
	assert.Contains(t, s, "helpers",
		"the helper lives in the helpers/ directory on the control volume")
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

	// The nix invocation now lives in the activation helper (one wsl.exe
	// session with the daemon), so the flag invariants are asserted THERE.
	helper, err := GuestFile("helpers/activate-home-manager.sh")
	require.NoError(t, err)
	h := string(helper)

	// Run 20260802T112212: inner double quotes do not survive
	// PowerShell -> wsl.exe -> sh -lc, and nix then reports "no subcommand
	// specified". The features must travel as REPEATED flags.
	assert.Equal(t, 2, strings.Count(h, "--extra-experimental-features"),
		"nix-command and flakes must be two separate flags, never one quoted pair")
	assert.Contains(t, h, "release-26.05",
		"the home-manager runner is pinned to the branch matching this NixOS")
	// Run 20260802: nix is on PATH only via /etc/profile in NixOS-WSL, so a
	// bare `wsl -- nix` exits 127 on a perfectly working distro.
	assert.Contains(t, s, "/bin/sh -lc", "every nix call needs a login shell")
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

// --- Stage 13/14 fixes proven interactively on 20260804-05 (first-ever E2E) ---

// The home-manager activation and the nix-daemon MUST share one wsl.exe
// process tree: WSL kills background processes when the session ends, so the
// daemon nix-verify started is dead by the time this stage runs. The helper
// carries the whole sequence — daemon ensure, nixhome extraction to ext4,
// switch as the distro user.
func TestGuestPayload_CarriesHomeManagerActivationHelper(t *testing.T) {
	payload, err := GuestPayload()
	require.NoError(t, err)

	const key = "/devcell/helpers/activate-home-manager.sh"
	require.Contains(t, payload, key,
		"the activation helper must ship on the control volume")
	sh := string(payload[key])
	assert.Contains(t, sh, "nix-daemon", "the helper ensures the daemon in-session")
	assert.Contains(t, sh, "tar -xzf",
		"nixhome must be extracted to ext4 — symlinks in the virtiofs+drvfs share do not readlink (run 20260804)")
	assert.Contains(t, sh, "home-manager/release-26.05", "the pinned runner branch")
	assert.Contains(t, sh, "switch -b backup", "the standalone-flake activation form")
	assert.Contains(t, sh, "wsl-base", "the WSL flake attribute family")
}

// The stage must hand the work to the helper in ONE wsl.exe call, and feed it
// the nixhome tarball from the control volume — never the share path, which
// nix rejects (dirty git tree ingestion + readlink failures, run 20260804).
func TestHomeManagerStage_ActivatesViaControlVolumeHelper(t *testing.T) {
	src, err := GuestFile("stages/home-manager.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.Contains(t, s, "activate-home-manager.sh",
		"activation must go through the shipped helper (daemon + switch, one session)")
	assert.Contains(t, s, "nixhome.tgz",
		"nixhome travels as a tarball; activating from the share path fails on symlinks")
	assert.NotContains(t, s, "--flake ./nixhome",
		"the share path must never be the flake source")
}

// NixhomeTarball is what puts nixhome.tgz on the control volume. Symlinks
// are the reason the tarball exists at all — flattening them would silently
// reintroduce the bug class the tarball solves.
func TestNixhomeTarball_PreservesSymlinks(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "icons"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "icons", "a.xpm"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink("a.xpm", filepath.Join(dir, "icons", "b.xpm")))

	data, err := NixhomeTarball(dir)
	require.NoError(t, err)

	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	entries := map[string]byte{}
	links := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		entries[hdr.Name] = hdr.Typeflag
		if hdr.Typeflag == tar.TypeSymlink {
			links[hdr.Name] = hdr.Linkname
		}
	}
	assert.Contains(t, entries, "nixhome/flake.nix",
		"contents must sit under nixhome/ so extraction recreates the expected layout")
	require.Contains(t, links, "nixhome/icons/b.xpm", "the symlink must survive as a symlink")
	assert.Equal(t, "a.xpm", links["nixhome/icons/b.xpm"], "with its original target")
}

// nixos-rebuild as root uses the LOCAL store by default, and this image's
// local-mode temproot handling is broken (ENOENT on
// /nix/var/nix/temproots/<pid> across three runs, 20260805). Every
// daemon-mediated operation worked, so the rebuild must run with
// NIX_REMOTE=daemon — and the daemon ensured in the same session.
func TestGuestPayload_CarriesRebuildBootHelper(t *testing.T) {
	payload, err := GuestPayload()
	require.NoError(t, err)

	const key = "/devcell/helpers/nixos-rebuild-boot.sh"
	require.Contains(t, payload, key,
		"the rebuild helper must ship on the control volume")
	sh := string(payload[key])
	assert.Contains(t, sh, "NIX_REMOTE=daemon",
		"root's local-mode store is broken in this image; the daemon path is the proven one")
	assert.Contains(t, sh, "temproots",
		"the image ships without /nix/var/nix/temproots")
	assert.Contains(t, sh, "nix-daemon", "the daemon must be ensured in this same session")
	assert.Contains(t, sh, "nixos-rebuild boot",
		"boot, never switch — the upstream change-username procedure is explicit")
}

// The adopt stage's rebuild must go through the helper, and the home
// ownership fix must wait for the cycle: the new user does not exist until
// the new generation boots, so a pre-cycle chown dies with "invalid spec"
// (run 20260805) and the assert would sink the stage.
func TestAdoptUserStage_RebuildViaHelperAndChownAfterCycle(t *testing.T) {
	src, err := GuestFile("stages/wsl-adopt-user.ps1")
	require.NoError(t, err)
	s := string(src)

	assert.Contains(t, s, "nixos-rebuild-boot.sh",
		"the rebuild needs the daemon + NIX_REMOTE=daemon wrapper")

	cycleAt := strings.Index(s, "wsl.exe --terminate")
	chownAt := strings.Index(s, "chown -R")
	require.Positive(t, cycleAt, "the stage must cycle the distro")
	require.Positive(t, chownAt, "the stage must own the carried home to the new user")
	assert.Less(t, cycleAt, chownAt,
		"chown must follow the cycle — the user only exists once the new generation boots")
}

// The tarball must ride the same control volume as the stage that consumes
// it — a payload without it means stage 13 fails an hour into a build.
func TestGuestPayloadWithNixhome_CarriesTheTarball(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644))

	payload, err := GuestPayloadWithNixhome(dir)
	require.NoError(t, err)

	require.Contains(t, payload, "/devcell/nixhome.tgz",
		"nixhome.tgz must land beside the stage scripts")
	assert.Contains(t, payload, "/devcell/helpers/activate-home-manager.sh",
		"the base guest tree must still be present")
	assert.NotEmpty(t, payload["/devcell/nixhome.tgz"])
}
