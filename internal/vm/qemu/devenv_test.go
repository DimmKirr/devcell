package qemu

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/unattend"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- dev-env provisioning scripts (Test B: agent, passthrough, WSL, nix) ----
//
// Every script here travels through PowerShellEncodedCommand, so quoting is
// transport-safe by construction; these tests pin the *commands* — the tools
// invoked and the arguments that matter — not incidental wording.

func TestGenerateVirtioAgentInstallScript_InstallsARM64DriversAndX64Agent(t *testing.T) {
	s := GenerateVirtioAgentInstallScript()

	// Drive letter must be probed, never hardcoded: CD letters move.
	assert.NotContains(t, s, "E:\\", "no hardcoded CD drive letter")
	assert.Contains(t, s, "pnputil", "drivers install via the inbox tool (VIRTIO.md)")
	assert.Contains(t, s, `vioserial\w11\ARM64`, "vioserial is the qemu-ga channel prerequisite")
	assert.Contains(t, s, `viofs\w11\ARM64`, "viofs is the passthrough prerequisite")
	// No ARM64 agent build exists (VIRTIO.md) — the x64 MSI under Win11's
	// emulation is the sanctioned path.
	assert.Contains(t, s, "qemu-ga-x86_64.msi")
	assert.Contains(t, s, "msiexec")
	assert.Contains(t, s, "/qn", "agent MSI must install unattended")
	assert.Contains(t, s, "QEMU-GA", "script must report the agent service state")
}

// The driver-trust story, distilled from dev-env iterations 3–8: viofs is
// Dev-signed (CN=Red Hat Inc., OU=Dev), so its signers must land in the
// MACHINE Root and TrustedPublisher stores (.NET X509Store — Import-Certificate
// throws over SSH and certutil falls back silently), every certificate must
// come from the signatures themselves (chain.Build without a trusted root
// omits the root), the .cat counts as much as the .sys, the stores must be
// read back (exit codes lied twice), and testsigning must be enabled — it is
// read at boot, hence this stage ends in a reboot.
func TestGenerateDriverTrustScript_TrustsSignersIntoMachineStores(t *testing.T) {
	s := GenerateDriverTrustScript()

	assert.Contains(t, s, "X509Store",
		"machine store writes go through the .NET API — cmdlet and certutil both mislead over SSH")
	assert.Contains(t, s, "'LocalMachine'",
		"driver trust reads the MACHINE stores; a user-store write is a silent no-op")
	assert.Contains(t, s, "machine ",
		"the store must be read back after writing — exit codes have lied twice")
	assert.Contains(t, s, "TrustedPublisher",
		"the signer certificate must be trusted for driver installation")
	assert.Contains(t, s, "*.cat",
		"driver install trusts the catalog's publisher — trust the .cat signer too")
	assert.Contains(t, s, "X509Certificate2Collection",
		"import ALL embedded signature certs — a chain built without its root omits the root")
	assert.Contains(t, s, "testsigning",
		"a Dev-signed kernel driver cannot be installed or loaded under enforced code integrity")
}

// Iteration 3: the script printed "driver installed" over pnputil's failure.
// Native tools only speak through exit codes; a rejected driver must fail
// the stage.
func TestGenerateVirtioAgentInstallScript_FailsOnRejectedDriver(t *testing.T) {
	s := GenerateVirtioAgentInstallScript()

	assert.Contains(t, s, "$LASTEXITCODE",
		"pnputil is native; only its exit code says whether the add worked")
	assert.Contains(t, s, "throw",
		"a rejected driver must fail the stage, not print 'installed'")
	assert.Contains(t, s, "testsigning state",
		"the stage must record the policy it ran under — iteration 8 ran under the wrong one")
}

func TestGenerateWinFspInstallScript_UnattendedFromRelease(t *testing.T) {
	s := GenerateWinFspInstallScript()

	assert.Contains(t, s, "winfsp", "WinFsp is the FUSE layer virtiofs.exe requires")
	assert.Contains(t, s, "msiexec")
	assert.Contains(t, s, "/qn")
	assert.Contains(t, s, "Invoke-WebRequest", "installer comes over the guest's own network")
}

func TestGenerateVirtioFSMountScript_MountsTagAndVerifies(t *testing.T) {
	s := GenerateVirtioFSMountScript("devcell", "Z:")

	assert.Contains(t, s, "virtiofs.exe", "the ARM64 service binary from the driver ISO")
	assert.Contains(t, s, "devcell", "must mount the host-side tag")
	assert.Contains(t, s, "Z:", "must surface the share as the requested drive")
	assert.Contains(t, s, "Get-ChildItem", "mounting without reading proves nothing")
}

// First Test B run (20260801T013317): virtiofsd logged "Client connected,
// servicing requests" — and Z: still never appeared. Whatever sc.exe had to
// say about why was piped to Out-Null. The mount script must keep service
// manager output, declare the documented dependency chain, and poll rather
// than hope a fixed 5s is enough under TCG.
func TestGenerateVirtioFSMountScript_KeepsServiceDiagnostics(t *testing.T) {
	s := GenerateVirtioFSMountScript("devcell", "Z:")

	// Iteration 2 hardcoded virtio-win's documented dependency string and got
	// error 1075: on this guest at least one of the two services does not
	// exist under that name. Candidates are probed and only existing ones
	// become dependencies.
	assert.Contains(t, s, "WinFsp.Launcher")
	assert.Contains(t, s, "VirtioFsDrv")
	assert.Contains(t, s, "Get-Service",
		"dependencies must be probed, not assumed — error 1075 taught that")
	assert.Contains(t, s, "sc.exe query VirtioFsSvc",
		"the service state is the first thing a mount failure needs")
	assert.Contains(t, s, "Get-PSDrive",
		"if the mount landed on another letter, the drive list says so")
	assert.NotRegexp(t, `sc\.exe [^\n]*\| Out-Null`, s,
		"discarding sc.exe output is how the first failure explained nothing")
}

// NixOS-WSL requires WSL2 (https://nix-community.github.io/NixOS-WSL/install.html
// — "WSL 2 is required, WSL 1 not supported"), so the guest must gain both
// features: the WSL subsystem and VirtualMachinePlatform, which is what
// carries the WSL2 utility VM.
func TestGenerateWSL2EnableScript_EnablesBothFeatures(t *testing.T) {
	s := GenerateWSL2EnableScript()

	assert.Contains(t, s, "Microsoft-Windows-Subsystem-Linux")
	assert.Contains(t, s, "VirtualMachinePlatform",
		"WSL2 — and therefore NixOS-WSL — cannot run without it")
	assert.Contains(t, s, "Enable-WindowsOptionalFeature")
	assert.Contains(t, s, "-NoRestart", "the caller owns the reboot, not the script")
}

// The release ships one image per architecture; the asset name must follow
// the guest. Run 20260802: the hardcoded nixos.wsl (x86_64) imported cleanly
// on ARM64 Windows and then every exec inside the distro died with ENOEXEC
// (execv errno 8) — the utility VM, kernel, and mounts were all fine.
func TestGenerateNixOSWSLImportScript_PicksAssetByGuestArch(t *testing.T) {
	s := GenerateNixOSWSLImportScript()

	assert.Contains(t, s, "nixos.aarch64.wsl",
		"ARM64 Windows needs the aarch64 image — the x86_64 one imports fine and init dies with ENOEXEC")
	assert.Contains(t, s, "PROCESSOR_ARCHITECTURE",
		"the asset must be chosen by the guest's architecture, not hardcoded")
}

// WSL's defaults assume real hardware. Under TCG double emulation the
// utility-VM kernel needs far more than the default 30s KernelBootTimeout
// (WslCoreConfig.h), and vGPU setup (the FlexibleIov device WSLg adds) has
// no partitionable GPU to bind. Both must be configured before any wsl.exe
// VM operation, so the engine-install stage owns writing .wslconfig.
func TestGenerateWSLEngineInstallScript_ConfiguresWslForEmulatedHosts(t *testing.T) {
	s := GenerateWSLEngineInstallScript()

	assert.Contains(t, s, ".wslconfig",
		"the settings live in the user's .wslconfig, written before first VM start")
	assert.Contains(t, s, "kernelBootTimeout=3600000",
		"15 min was still short on a loaded host (run 20260802T125133 timed out at ~19 min)")
	assert.Contains(t, s, "distributionStartTimeout",
		"distro start shares the same emulation slowness as kernel boot")
	assert.Contains(t, s, "gpuSupport=false",
		"vGPU hot-add is the last HCS operation before wslservice died with E_UNEXPECTED")
	assert.Contains(t, s, "guiApplications=false",
		"WSLg has no display to serve in a headless cell and drags vGPU back in")
	assert.Contains(t, s, "processors=4",
		"TCG ARM64 degrades above 4 vCPUs (Linaro benchmarks) — 4 is the sweet spot")
	assert.Contains(t, s, "memory=4GB",
		"WSL needs enough RAM for the NixOS utility VM under double emulation")
}

// The distro is NixOS-WSL's own image, imported as WSL2 per the project's
// install docs. Nothing is "installed into" it: NixOS ships nix, so a
// separate nix-install stage would be both redundant and non-idiomatic.
func TestGenerateNixOSWSLImportScript_ImportsOfficialImageAsWSL2(t *testing.T) {
	s := GenerateNixOSWSLImportScript()

	assert.Contains(t, s, "wsl engine still missing",
		"import must verify the engine the previous stage claimed to install")
	assert.Contains(t, s, "WSL_UTF8", "wsl.exe output is unreadable UTF-16 without it")
	assert.Contains(t, s, "$LASTEXITCODE",
		"with 'Stop' unusable around wsl.exe, exit codes are the only failure signal")

	assert.Contains(t, s, "api.github.com/repos/nix-community/NixOS-WSL",
		"the image comes from the NixOS-WSL releases, not a generic rootfs")
	assert.Contains(t, s, "nixos.wsl", "current releases ship nixos.wsl")
	assert.Contains(t, s, "wsl --set-default-version 2",
		"NixOS-WSL does not support WSL1")
	assert.Contains(t, s, "--version 2", "the import must be a WSL2 import")
	assert.Contains(t, s, "--from-file",
		"WSL 2.4.4+ installs a .wsl image directly — that is the documented path")
	assert.Contains(t, s, "nixos-version",
		"the proof is NixOS answering, not merely an import that returned 0")
	// The WSL1 vocabulary must be gone.
	assert.NotContains(t, s, "--set-default-version 1")
	assert.NotContains(t, s, "ubuntu")
}

// The cell's user is the host's user on every engine (Docker cells create
// $HOST_USER; the Windows session is $HOST_USER). The WSL distro must match
// — NixOS-WSL otherwise defaults to "nixos", leaving the repo symlink at
// /home/<hostuser> owned by a user that does not exist inside the distro.
// Official procedure: nix-community.github.io/NixOS-WSL/how-to/change-username.html
func TestGenerateWSLUserScript_SetsDefaultUserToSessionUser(t *testing.T) {
	s := GenerateWSLUserScript("dmitry")

	assert.Contains(t, s, "wsl.defaultUser", "the option that renames the distro's default user")
	assert.Contains(t, s, "dmitry")
	assert.Contains(t, s, "nixos-rebuild boot",
		"the docs are explicit: boot, not switch — switch misconfigures the account")
	assert.NotContains(t, s, "nixos-rebuild switch")
	assert.Contains(t, s, "--terminate",
		"the distro must be cycled for the new generation's user to take effect")
	assert.Contains(t, s, "extraGroups", "the cell user needs sudo (wheel) inside the distro")
}

// --- the WSL distro user is NOT the Windows session user ---------------------

// Two identities, deliberately separate:
//
//   - the WINDOWS account is the host's $USER (autounattend creates it, SSH
//     lands as it) — unattend.SessionUsername()
//   - the WSL DISTRO user is whoever nixhome's home-manager config was built
//     for — WSLDistroUser
//
// Conflating them is what made home-manager unactivatable: nixhome pins
// `wslUser = {username = "nixos"; ...}` (nixhome/flake.nix:210) while the
// stages renamed the distro to the Windows user, and home-manager's activation
// guard rejects a config whose username differs from the invoking user:
//
//	Error: USER is set to "dmitry" but we expect "nixos"
//
// Verified on the host: the shipped wsl-base-aarch64 activation package clears
// the username guard as USER=nixos. So the WSL-side stages must address the
// distro user, and only the distro user.
func TestWSLDistroUser_MatchesTheNixhomeWSLConfig(t *testing.T) {
	assert.Equal(t, "nixos", WSLDistroUser,
		"nixhome's wslUser pins this name; changing one without the other "+
			"breaks home-manager activation")
}

func TestDevEnvStages_WSLStagesAddressTheDistroUserNotTheWindowsUser(t *testing.T) {
	const windowsUser = "dmitry"
	require.NotEqual(t, windowsUser, WSLDistroUser,
		"this test is meaningless unless the two identities actually differ")

	byName := map[string]GuestStage{}
	for _, st := range DevEnvStages(windowsUser, "devcell", "Z:") {
		byName[st.Name] = st
	}

	wslUser, ok := byName["set WSL default user"]
	require.True(t, ok, "stage not found")
	assert.Equal(t, WSLDistroUser, wslUser.Args["User"],
		"the rename target is the distro user nixhome was built for")

	hm, ok := byName["activate nixhome home-manager"]
	require.True(t, ok, "stage not found")
	assert.Equal(t, WSLDistroUser, hm.Args["User"],
		"home-manager activates into the distro user's home")

	// The stage is file-backed, so what travels over SSH is an INVOCATION.
	// The distro user must reach the script as a parameter, and the Windows
	// user must not appear anywhere in it.
	payload := stagePayload(hm)
	assert.Contains(t, payload, "-User '"+WSLDistroUser+"'")
	assert.NotContains(t, payload, windowsUser,
		"the Windows user has no home inside the distro")
}

// The cell must finally run as the HOST user, like every other engine.
//
// Docker gets there by building the profile for a fixed user (`devcell`) and
// remapping in the entrypoint; WSL builds for `nixos` and never remaps, so
// `whoami` inside the distro answers "nixos" (run 20260803T231223). The fix is
// the WSL analogue of that entrypoint step, and its ORDER is load-bearing:
//
//   - home-manager's activation guard is `checkUsername <baked-in name>`,
//     compared against $USER at activation time only. nixhome pins
//     wslUser.username = "nixos", so activation MUST run as nixos.
//   - activation is a one-time build step. Afterwards the result is store
//     paths plus symlinks in /home/nixos, and the guard never runs again —
//     so the host user can be introduced safely after it.
//
// Renaming before activation is what produced
// `Error: USER is set to "dmitry" but we expect "nixos"`.
func TestDevEnvStages_HostUserBecomesTheDistroUserAfterActivation(t *testing.T) {
	const windowsUser = "dmitry"
	stages := DevEnvStages(windowsUser, "devcell", "Z:")

	idx := func(name string) int {
		for i, st := range stages {
			if st.Name == name {
				return i
			}
		}
		return -1
	}

	activate := idx("activate nixhome home-manager")
	require.GreaterOrEqual(t, activate, 0, "activation stage not found")

	adopt := idx("adopt the host user in the distro")
	require.GreaterOrEqual(t, adopt, 0,
		"no stage makes the host user the distro's user — `whoami` stays %q",
		WSLDistroUser)

	assert.Greater(t, adopt, activate,
		"the host user must be adopted AFTER activation; before it, "+
			"home-manager's checkUsername guard rejects the config")

	assert.Equal(t, windowsUser, stages[adopt].Args["User"],
		"the stage adopts the HOST user, not the build-time distro user")
}

// NixOS ships nix; the old curl|sh single-user install has no place here.
// This stage only proves the toolchain the image already carries.
func TestGenerateNixVerifyScript_UsesTheDistrosOwnNix(t *testing.T) {
	s := GenerateNixVerifyScript()

	assert.Contains(t, s, "nix --version")
	assert.Contains(t, s, NixOSWSLDistro)
	assert.NotContains(t, s, "nixos.org/nix/install",
		"NixOS already has nix — installing it again is not idiomatic")
	assert.NotContains(t, s, "--no-daemon", "that is the non-NixOS single-user path")
}

// The stage must record the distro's environment: USER/HOME/PATH decide
// where home-manager activates and whether its CLI is reachable, and
// Windows-interop entries on PATH are what let the cell call Windows tools.
// Recording beats asking a running guest — SSH is unusable while a stage
// saturates it.
func TestGenerateNixVerifyScript_RecordsGuestEnvironment(t *testing.T) {
	s := GenerateNixVerifyScript()

	for _, want := range []string{"$USER", "$HOME", "$PATH"} {
		assert.Contains(t, s, want, "the stage log must carry %s for later diagnosis", want)
	}
}

func TestGenerateHomeManagerScript_ActivatesNixhomeViaShare(t *testing.T) {
	s := GenerateHomeManagerScript("dmitry", "Z:")

	assert.Contains(t, s, "/mnt/z", "WSL sees the mounted share as a drvfs drive")
	assert.Contains(t, s, "/home/dmitry/dev/dimmkirr/devcell",
		"the repo must appear at the agreed path inside WSL")
	assert.Contains(t, s, "nixhome", "activation targets the repo's nixhome")
	assert.Contains(t, s, "home-manager")
	assert.Contains(t, s, "$LASTEXITCODE",
		"native wsl calls fail via exit code, not exceptions")
}

// A failing activation must fail the stage.
//
// Run 20260803T231223 lost 9 minutes to this: the activation command ended in
// `| tail -40`, and in a shell pipeline $? is the LAST command's status. tail
// always succeeds, so nix's
//
//	error: opening lock file "/nix/var/nix/db/big-lock": Permission denied
//
// exited non-zero into a pipe that reported success. Assert-DevcellExitCode
// saw 0 and the step logged "ok in 36s". `set -e` does not catch it either —
// the pipeline as a whole succeeded. Only the NEXT step (home-manager
// --version, exit 127) revealed that nothing had been activated.
// "verify nix" must verify what the next stage needs.
//
// Run 20260803T231223: the verify stage passed on `nix --version` and the
// activation then died with
//
//	error: opening lock file "/nix/var/nix/db/big-lock": Permission denied
//	This command may have been run as non-root in a single-user Nix
//	installation, or the Nix daemon may have crashed.
//
// NixOS is a MULTI-user store: nix-daemon mediates every write, and nothing
// in the pipeline configures or checks it. `nix --version` answers fine on a
// store the invoking user cannot write, so the verify stage certified a
// distro that could not build — 13 minutes before the stage that needed it.
func TestGenerateNixVerifyScript_ProvesTheStoreIsWritableNotJustThatNixAnswers(t *testing.T) {
	s := GenerateNixVerifyScript()

	assert.Contains(t, s, "nix --version", "sanity: this is the verify script")

	assert.True(t,
		strings.Contains(s, "nix-daemon") || strings.Contains(s, "systemctl"),
		"the stage must record whether nix-daemon is reachable — without it a "+
			"non-root build fails on /nix/var/nix/db/big-lock, and `nix --version` "+
			"cannot tell you that")

	assert.True(t,
		strings.Contains(s, "nix build") || strings.Contains(s, "nix-build") ||
			strings.Contains(s, "nix-store --add") || strings.Contains(s, "nix store add"),
		"verification must exercise a real store WRITE as the invoking user; "+
			"otherwise the next stage is the first thing to discover the store "+
			"is read-only to it")
}

func TestGenerateHomeManagerScript_PipeCannotSwallowAFailedActivation(t *testing.T) {
	s := GenerateHomeManagerScript("dmitry", "Z:")

	require.Contains(t, s, "home-manager", "sanity: this is the activation script")

	// Truncating output is fine; losing the status is not. Either drop the
	// pipe or make the shell propagate the left-hand status. Matches a real
	// pipe-into-command, not the `||` in the arch-suffix expression.
	pipedSwitch := regexp.MustCompile(`switch[^\n]*[^|]\|[^|]\s*\w`)
	if loc := pipedSwitch.FindString(s); loc != "" {
		assert.True(t,
			strings.Contains(s, "pipefail") || strings.Contains(s, "PIPESTATUS"),
			"the activation pipes its output (%q) without pipefail/PIPESTATUS — "+
				"$? becomes the pipe's last command and a failed "+
				"`home-manager switch` reports success", loc)
	}
}

// The activation must follow the official standalone-flake path
// (https://nix-community.github.io/home-manager/installation.html) on this
// guest: nix through a login shell, the home-manager release branch matching
// the NixOS release, and the flake attr the nixhome flake actually defines
// for this architecture ("-aarch64" suffix per its own comment).
func TestGenerateHomeManagerScript_OfficialFlakePathForThisGuest(t *testing.T) {
	s := GenerateHomeManagerScript("dmitry", "Z:")

	assert.Contains(t, s, "-lc",
		"nix is only on PATH in a login shell (run 20260802: bare wsl -- nix is exit 127)")
	assert.Contains(t, s, "home-manager/release-",
		"the runner must be pinned to the release branch matching NixOS, not floating master")
	assert.Contains(t, s, "--extra-experimental-features nix-command --extra-experimental-features flakes",
		"repeat the flag: an inner-quoted \"nix-command flakes\" loses its quotes crossing "+
			"PowerShell -> wsl.exe -> sh -lc and nix sees no subcommand (run 20260802T112212)")
	assert.NotRegexp(t, `--extra-experimental-features "`, s,
		"no embedded double quotes in the guest command line")
	assert.Contains(t, s, "wsl-base",
		"WSL activates the wsl-* configs: their user is the distro default (nixos), not the Docker cell's devcell")
	assert.Contains(t, s, "aarch64",
		"aarch64 guests need the -aarch64 config suffix (flake.nix's own contract)")
	assert.Contains(t, s, "uname -m",
		"the suffix must follow the guest architecture, not be hardcoded")
}

// "Installed" means the CLI answers afterwards: the stage must assert
// `home-manager --version` prints an actual semantic version, not merely
// that the switch exited 0.
func TestGenerateHomeManagerScript_AssertsSemanticVersion(t *testing.T) {
	s := GenerateHomeManagerScript("dmitry", "Z:")

	assert.Contains(t, s, "home-manager --version",
		"the proof of installation is the CLI answering from the activated profile")
	assert.Regexp(t, `grep -E.*[0-9].*\\.`, s,
		"the version output must be matched against a semantic-version pattern")
	assert.Contains(t, s, "throw",
		"a missing or unversioned home-manager must fail the stage")
}

// The engine install tears the SSH session down mid-MSI (iteration 10) — the
// stage must declare that so the harness treats the drop as expected rather
// than a failure, and must fetch the ARM64 package from microsoft/WSL.
func TestGenerateWSLEngineInstallScript_FetchesARM64Engine(t *testing.T) {
	s := GenerateWSLEngineInstallScript()

	assert.Contains(t, s, "api.github.com/repos/microsoft/WSL",
		"the WSL engine package comes from the microsoft/WSL releases")
	assert.Contains(t, s, "arm64.msi", "the guest is ARM64 — so is the WSL package")
	assert.Contains(t, s, "msiexec")
	// Iteration 11: the probe itself threw — 'Stop' turns native stderr into
	// an exception, and wsl.exe speaks UTF-16 unless told otherwise.
	assert.Contains(t, s, "WSL_UTF8",
		"wsl.exe output is UTF-16 null soup without WSL_UTF8=1 — nothing matches it")
	assert.Contains(t, s, "$ErrorActionPreference = 'Continue'",
		"the probe must not throw on the stderr message it exists to read")

	for _, st := range DevEnvStages("dmitry", "devcell", "Z:") {
		if st.Name == "install WSL engine" {
			assert.True(t, st.ToleratesDisconnect,
				"the MSI kills the SSH session — the stage must say so")
			assert.True(t, st.RebootAfter,
				"engine services want a clean boot before first use")
			return
		}
	}
	t.Fatal("no 'install WSL engine' stage in DevEnvStages")
}

// The stages must run in dependency order: drivers before WinFsp before the
// mount that needs both; WSL feature before the import that needs it; nix
// before home-manager.
func TestDevEnvStages_Order(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")

	var names []string
	for _, st := range stages {
		names = append(names, st.Name)
		// A stage is executable either as a real script file on the control
		// volume (CELL-402) or as legacy rendered PowerShell — never neither.
		require.NotEmpty(t, stagePayload(st), "stage %s has no executable payload", st.Name)
	}
	joined := strings.Join(names, " → ")

	require.Less(t, indexOf(names, "trust driver signers"), indexOf(names, "install virtio drivers and guest agent"), joined)
	require.Less(t, indexOf(names, "install virtio drivers and guest agent"), indexOf(names, "install WinFsp"), joined)
	require.Less(t, indexOf(names, "install WinFsp"), indexOf(names, "mount project share"), joined)
	require.Less(t, indexOf(names, "enable WSL2 features"), indexOf(names, "install WSL engine"), joined)
	require.Less(t, indexOf(names, "install WSL engine"), indexOf(names, "import NixOS-WSL distro"), joined)
	require.Less(t, indexOf(names, "import NixOS-WSL distro"), indexOf(names, "verify nix in NixOS-WSL"), joined)

	// nix verification is part of the WSL component: NixOS-WSL ships nix, so
	// it proves the import, it does not install anything.
	for _, st := range stages {
		if st.Name == "verify nix in NixOS-WSL" {
			require.Equal(t, "WSL", st.Component,
				"verifying nix proves the NixOS-WSL import — it is not its own phase")
		}
	}
	require.Less(t, indexOf(names, "verify nix in NixOS-WSL"), indexOf(names, "activate nixhome home-manager"), joined)
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// TestWindowsDevEnv_QEMU builds the dev environment on top of the verified
// ssh-able image: virtio drivers + guest agent, project passthrough over
// virtio-fs, WSL1, nix, and the repo's nixhome home-manager profile.
//
// It boots an overlay — the ssh-able image itself is never written.
//
// Run explicitly, after TestSSHAble_ConnectAndListFiles has produced the image:
//
//	DEVCELL_TEST_DEVENV=1 go test -run TestWindowsDevEnv_QEMU -timeout 6h -v ./internal/vm/qemu/
func TestWindowsDevEnv_QEMU(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the ssh-able Windows image and provisions a dev environment")
	}
	if os.Getenv("DEVCELL_TEST_DEVENV") == "" {
		t.Skip("set DEVCELL_TEST_DEVENV=1 to run the dev-env provisioning test")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")

	// Resume from the furthest checkpoint available. A WSL-ready image already
	// carries the drivers, the share and the WSL engine, so iterating on the
	// distro itself costs a boot instead of the ~40-minute prelude.
	baseImage, err := LatestWSLReadyTestImage(testdataDir(t))
	resumeAt := wslReadyCheckpointStage
	if err != nil {
		t.Logf("no WSL-ready checkpoint (%v) — starting from ssh-able", err)
		baseImage, err = LatestSSHAbleTestImage(testdataDir(t))
		if err != nil {
			t.Skipf("no ssh-able image: %v", err)
		}
		resumeAt = ""
	}
	t.Logf("building on image: %s (resume at: %q)", baseImage, resumeAt)

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()
	repo := repoRoot(t)

	overlay := filepath.Join(workDir, "devenv.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))
	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err)
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	// Host side of the passthrough. Without virtiofsd the mount stage cannot
	// pass — surface that as a stage failure with a clear message, not a
	// silent skip: the passthrough is part of what this test exists to prove.
	const shareTag = "devcell"
	virtioFSSock := filepath.Join(workDir, "virtiofs.sock")
	virtiofsd := os.Getenv("DEVCELL_VIRTIOFSD")
	if virtiofsd == "" {
		virtiofsd, _ = exec.LookPath("virtiofsd")
	}
	require.NotEmpty(t, virtiofsd,
		"virtiofsd not found: set DEVCELL_VIRTIOFSD or put it on PATH (nix build nixpkgs#virtiofsd)")
	// virtiofsd is not a start-once service: it exits as soon as its client
	// disconnects ("Client disconnected, shutting down"), so every time the VM
	// goes away — the checkpoint power-off included — it must be started again
	// or the next QEMU finds a dead vhost-user socket and never boots.
	//
	// host- prefix, no sequence number: this is a host service that spans the
	// whole run, not a pipeline stage. Sequence numbers mean "position in the
	// stage order" and would be a lie here.
	startFSD := func() {
		t.Helper()
		fsd := exec.Command(virtiofsd,
			"--socket-path", virtioFSSock,
			"--shared-dir", repo,
			"--sandbox", "none")
		fsdLog, err := os.OpenFile(filepath.Join(resultsDir, "host-virtiofsd.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		require.NoError(t, err)
		fsd.Stdout, fsd.Stderr = fsdLog, fsdLog
		require.NoError(t, fsd.Start())
		t.Cleanup(func() {
			if fsd.Process != nil {
				_ = fsd.Process.Kill()
			}
			_ = fsd.Wait()
			_ = fsdLog.Close()
		})
	}
	startFSD()

	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := unattend.SessionUsername()
	const shareDrive = "Z:"

	// The FAT log volume: guest-side stage transcripts the host can read off
	// the image even when SSH and the run are gone — install-test logic,
	// shared with every other QEMU test via attachGuestLogVolume.
	allStages := DevEnvStages(user, shareTag, shareDrive)
	stages := stagesFrom(t, allStages, resumeAt)
	stageLogNames := StageLogNames(stages)
	logVolume := attachGuestLogVolume(t, workDir, resultsDir, stageLogNames)

	spec := Spec{
		VMName:               "devcell-qemu-devenv",
		CPUs:                 4,
		MemoryGB:             6,
		DiskPath:             overlay,
		FirmwarePath:         FirmwarePath(),
		VarsPath:             varsPath,
		VirtioISO:            VirtioISOPath(home),
		SSHHost:              "127.0.0.1",
		SSHPort:              freeTCPPort(10222),
		MACAddr:              DeterministicMAC("devcell-qemu-devenv"),
		QMPSocketDir:         workDir,
		DiskCacheMode:        "unsafe",
		GuestAgentSocketPath: filepath.Join(workDir, "qga.sock"),
		VirtioFSSocketPath:   virtioFSSock,
		VirtioFSTag:          shareTag,
		LogVolumePath:        logVolume,
		NestedVirt:           true,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmDone := startVM(t, spec)
	defer vmDone.stop()

	qmpSock := QMPSocketPath(spec)
	waitSSH := func(phase string, timeout time.Duration) {
		require.NoError(t,
			WaitForSSH(spec.SSHHost, spec.SSHPort, timeout, 5*time.Second, testLogObserver{t}, vmStateFn(qmpSock)),
			"SSH must come back: %s", phase)
	}
	waitSSH("initial boot of the ssh-able image", time.Hour)

	// The stage table drives subtests: each reports pass/fail under its own
	// sequenced name (`-run 'TestWindowsDevEnv_QEMU/03-install-WinFsp'` to
	// re-read one), while its output appends to the component's log — so a
	// subtest identifies the step and the log covers the whole subsystem.
	for i, stage := range stages {
		i, stage := i, stage
		logName := stageLogNames[i]
		subtestName := fmt.Sprintf("%02d-%s", i+1, strings.ReplaceAll(stage.Name, " ", "-"))
		ok := t.Run(subtestName, func(t *testing.T) {
			// Streamed, not buffered: a long stage (nix install) must be
			// observable while it runs — `tail -f` the artifact.
			livePath := filepath.Join(resultsDir, logName)
			out, runErr := sshStream(spec, user, keyPath, stage.Script, livePath, stageTimeout)
			if runErr != nil && stage.ToleratesDisconnect && strings.Contains(out, "closed by remote host") {
				t.Logf("dropped the SSH session as expected — the next stage verifies the outcome")
			} else {
				require.NoError(t, runErr, "stage %q failed:\n%s", stage.Name, out)
			}
			t.Logf("output:\n%s", tailLines(out, 15))

			// The stage before the resume point is the checkpoint: its reboot
			// becomes a clean shutdown, so the overlay can be saved as a
			// WSL-ready image and every later run skips straight to here.
			if resumeAt == "" && i+1 < len(stages) && stages[i+1].Name == wslReadyCheckpointStage {
				t.Logf("checkpoint — powering off to save a WSL-ready image")
				_, _ = sshTry(spec, user, keyPath, "Stop-Computer -Force")
				select {
				case <-vmDone.done:
					t.Log("guest powered off cleanly")
				case <-time.After(guestShutdownTimeout):
					t.Logf("guest did not power off in %s — forcing stop; checkpoint may be dirty",
						guestShutdownTimeout)
					vmDone.stop()
				}
				dest := filepath.Join(testdataDir(t), WSLReadyTestImageName(time.Now()))
				require.NoError(t, SaveBaseProfileImage(overlay, dest))
				info, statErr := os.Stat(dest)
				require.NoError(t, statErr)
				t.Logf("WSL-ready image saved: %s (%.1f GB) — later runs resume at %q",
					dest, float64(info.Size())/(1<<30), wslReadyCheckpointStage)

				startFSD() // the old one exited with the VM
				vmDone = startVM(t, spec)
				waitSSH("boot after checkpoint", 45*time.Minute)
				return
			}
			if stage.RebootAfter {
				t.Logf("needs a reboot — restarting the guest")
				_, _ = sshTry(spec, user, keyPath, "Restart-Computer -Force")
				time.Sleep(30 * time.Second) // let the old sshd actually go down
				waitSSH("reboot after "+stage.Name, 45*time.Minute)
			}
		})
		// Stages are strictly dependent: continuing past a failure only
		// produces confusing downstream errors.
		if !ok {
			t.Fatalf("stage %d/%d %q failed — see %s", i+1, len(stages), stage.Name,
				filepath.Join(resultsDir, logName))
		}
	}

	// The final proof the user asked for: the repo is visible inside WSL at
	// the agreed path, through the passthrough.
	out := sshCapture(t, spec, user, keyPath,
		fmt.Sprintf(`$env:WSL_UTF8='1'; wsl -d devcell -u %s -- ls /home/%s/dev/dimmkirr/devcell`, user, user))
	require.Contains(t, out, "go.mod", "the repo must be readable inside WSL through the share")
	// result- prefix: an assertion's evidence, not a stage log or a service log.
	writeArtifact(t, resultsDir, "result-wsl-repo-listing.txt", out)

	// Everything proved: this overlay now holds the finished dev environment
	// — the "base profile" state `cell build --engine=qemu` is meant to end
	// at. Shut the guest down cleanly (NTFS must be quiesced before the disk
	// is copied) and flatten the overlay into the base-profile image.
	t.Log("all stages green — shutting down to save the base-profile image")
	_, _ = sshTry(spec, user, keyPath, "Stop-Computer -Force")
	select {
	case <-vmDone.done:
		t.Log("guest powered off cleanly")
	case <-time.After(guestShutdownTimeout):
		t.Logf("guest did not power off in %s — forcing stop; image save may be dirty",
			guestShutdownTimeout)
		vmDone.stop()
	}

	dest := BaseProfileImagePath(home, "base", nil)
	require.NoError(t, SaveBaseProfileImage(overlay, dest))
	info, err := os.Stat(dest)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(1<<30),
		"base-profile image should hold Windows + WSL + nix, got %d bytes", info.Size())
	t.Logf("base-profile image saved: %s (%.1f GB)", dest, float64(info.Size())/(1<<30))
}

// --- shared VM harness helpers ----------------------------------------------

type vmHandle struct {
	cmd  *exec.Cmd
	done chan error
}

func (h *vmHandle) stop() {
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	<-h.done
}

func startVM(t *testing.T, spec Spec) *vmHandle {
	t.Helper()
	exclusiveQEMU(t)
	argv := BuildRunCommand(spec)
	t.Logf("booting: %s", strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...)
	// QEMU's dying words go to stderr. Run 20260802T083354 lost its VM-exit
	// cause because this was discarded — persist it with the run's evidence.
	qemuLog, err := os.OpenFile(filepath.Join(testResultsDir(t), "qemu-stderr.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		cmd.Stdout, cmd.Stderr = qemuLog, qemuLog
		t.Cleanup(func() { _ = qemuLog.Close() })
	}
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// The exit status is evidence: "signal: killed" with an empty stderr
		// means an external kill (OOM et al.), not a QEMU error. Written to
		// the file, not t.Logf — the test may already be past its end.
		if qemuLog != nil {
			fmt.Fprintf(qemuLog, "\n=== qemu exited: %v (%s)\n", err, time.Now().UTC().Format(time.RFC3339))
		}
		done <- err
	}()
	return &vmHandle{cmd: cmd, done: done}
}

// vmStateFn adapts QueryVMState for WaitForSSH, treating "socket not up yet"
// as still-running so early boot does not read as VM death.
func vmStateFn(qmpSock string) VMStateFunc {
	return func() VMState {
		s, err := QueryVMState(qmpSock)
		if err != nil {
			return StateRunning
		}
		return s
	}
}

// sshTry runs a script in the guest and returns output + error without
// failing the test — stages own their error reporting.
func sshTry(spec Spec, user, keyPath, script string) (string, error) {
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, user, keyPath,
		PowerShellEncodedCommand(script))
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// appendToFile is teeToFile in append mode: component logs accumulate across
// the stages that belong to them instead of each stage truncating the last.
func appendToFile(path string, mem io.Writer) (io.Writer, func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("opening component log %s: %w", path, err)
	}
	return io.MultiWriter(f, mem), f.Close, nil
}

// wslReadyCheckpointStage is where a WSL-ready image resumes; everything
// before it — drivers, the share, the WSL2 features — is baked in.
//
// It deliberately stops short of the engine install. That stage tolerates its
// SSH session dropping (`wsl --install` tears it down), so "it did not fail"
// is not the same as "it finished": checkpointing after it saved a guest whose
// engine was half-registered, and every resume then started from that broken
// state with no way to repair it. A checkpoint may only follow a stage whose
// success was *verified* — here, `state VirtualMachinePlatform: Enabled` read
// back from the guest. The engine install is cheap and self-verifying, so it
// re-runs on every resume.
const wslReadyCheckpointStage = "enable Hyper-V hypervisor"

// stagesFrom drops the stages a checkpoint image has already been through.
// An empty name runs everything; an unknown name is a programming error, not
// a reason to silently run the whole pipeline.
func stagesFrom(t *testing.T, stages []GuestStage, name string) []GuestStage {
	t.Helper()
	if name == "" {
		return stages
	}
	for i, st := range stages {
		if st.Name == name {
			return stages[i:]
		}
	}
	t.Fatalf("resume stage %q is not in the pipeline", name)
	return nil
}

// guestShutdownTimeout bounds a graceful guest power-off. Windows shutdown
// under TCG is far slower than the 5 minutes first allowed: the checkpoint in
// run 20260801T081640 timed out and had to force-stop, leaving a dirty image.
const guestShutdownTimeout = 25 * time.Minute

// stageTimeout bounds a single dev-env stage. The slowest legitimate stage
// (nix install under TCG) runs well under an hour; iteration 12 sat wedged
// for three, because nothing bounded it. Keepalives now catch a dead peer,
// this catches everything else.
const stageTimeout = 90 * time.Minute

// sshStream is sshTry with the output mirrored to livePath as it arrives, so
// a multi-hour stage can be watched with `tail -f` instead of revealing
// nothing until it exits (the same lesson teeToFile encodes for cell-build),
// and with a hard bound so a hung stage fails the run instead of stalling it.
func sshStream(spec Spec, user, keyPath, script, livePath string, timeout time.Duration) (string, error) {
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, user, keyPath,
		PowerShellEncodedCommand(script))
	cmd := exec.Command(argv[0], argv[1:]...)
	var mem strings.Builder
	// Append: several stages share one component log, and each contributes
	// its own section rather than truncating the previous one.
	w, closeFn, err := appendToFile(livePath, &mem)
	if err != nil {
		w, closeFn = &mem, func() error { return nil }
	}
	cmd.Stdout, cmd.Stderr = w, w

	if err := cmd.Start(); err != nil {
		_ = closeFn()
		return mem.String(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		runErr = fmt.Errorf("stage exceeded %s and was killed — see %s for where it stopped",
			timeout, livePath)
	}
	_ = closeFn()
	return mem.String(), runErr
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Guard against the harness dialing a dead port forever: the reboot wait must
// tolerate the guest being down, which WaitForSSH already does — this pins
// that net.Dial failure inside the wait loop is not fatal.
func TestVMStateFn_TreatsMissingSocketAsRunning(t *testing.T) {
	fn := vmStateFn(filepath.Join(t.TempDir(), "never-created.sock"))
	require.Equal(t, StateRunning, fn())
}

// --- answer-volume log channel (same logic as the install test's) ----------

func TestBuildGuestLogVolume_MarkerRoundTrips(t *testing.T) {
	img := filepath.Join(t.TempDir(), "guest-logs.img")

	require.NoError(t, BuildGuestLogVolume(img))

	data, err := isokit.ReadFileFromFAT(img, "/"+GuestLogVolumeMarker)
	require.NoError(t, err, "the marker is how the guest finds the volume — it must exist")
	require.Contains(t, string(data), "devcell guest control volume")
}

// Every stage must transcript itself onto the log volume: the SSH stream dies
// with the connection, but FAT survives anything short of the host losing the
// image file — the same reasoning as the install's answer volume.
// Logs group by component, not by SSH execution: every WSL step — feature,
// engine, distro import — appends to one 00N-devenv-WSL.log, so reading "what
// happened with WSL" is one file, not three. The number is the component's
// position in the pipeline, so a results dir still reads in execution order.
func TestStageLogName_SequencedPerComponent(t *testing.T) {
	assert.Equal(t, "001-devenv-drivers.log", StageLogName(1, "drivers"))
	assert.Equal(t, "004-devenv-WSL.log", StageLogName(4, "WSL"))
}

func TestStageLogNames_ShareOneLogPerComponent(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")
	names := StageLogNames(stages)

	require.Len(t, names, len(stages))

	byComponent := map[string]map[string]bool{}
	for i, st := range stages {
		if byComponent[st.Component] == nil {
			byComponent[st.Component] = map[string]bool{}
		}
		byComponent[st.Component][names[i]] = true
	}
	for comp, set := range byComponent {
		assert.Len(t, set, 1, "component %q must write exactly one log, got %v", comp, set)
	}

	// The WSL component covers three stages — they must all land in one file.
	var wslLogs []string
	for i, st := range stages {
		if st.Component == "WSL" {
			wslLogs = append(wslLogs, names[i])
		}
	}
	require.Greater(t, len(wslLogs), 1, "WSL spans several stages")
	for _, n := range wslLogs {
		assert.Equal(t, wslLogs[0], n)
		assert.Contains(t, n, "devenv-WSL.log")
	}
}

// WSL VM starts are transiently flaky under TCG: run 20260802T103055 got
// Wsl/Service/CreateInstance/CreateVm/E_ABORT on a stage that had passed
// identically the run before. The stages that start the utility VM are
// idempotent, so they must carry retries rather than fail the pipeline on
// the first transient abort.
func TestDevEnvStages_WSLVMStagesRetryTransientAborts(t *testing.T) {
	stages := devEnvStages("dmitry", "devcell", "Z:")
	for _, name := range []string{"import NixOS-WSL distro", "verify nix in NixOS-WSL"} {
		found := false
		for _, st := range stages {
			if st.Name == name {
				found = true
				assert.GreaterOrEqual(t, st.Retries, 2,
					"stage %q starts the WSL utility VM — transient CreateVm aborts need retries", name)
			}
		}
		assert.True(t, found, "stage %q must exist", name)
	}
}

func TestDevEnvStages_TranscriptsCarryTheirComponentLog(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")
	names := StageLogNames(stages)

	for i, st := range stages {
		if st.ScriptFile != "" {
			// File-backed stages receive their component log as a parameter
			// and do their own logging inside the script.
			assert.Equal(t, names[i], st.Args["LogName"],
				"stage %s must be told which component log to write", st.Name)
			continue
		}
		assert.Contains(t, st.Script, names[i],
			"stage %q must transcript into its component log", st.Name)
		// The 20MB CLIXML progress dumps of iteration 12 came from
		// Invoke-WebRequest's progress records travelling over SSH.
		assert.Contains(t, st.Script, "$ProgressPreference = 'SilentlyContinue'",
			"stage %q must suppress progress records — they ballooned logs to 11MB", st.Name)
	}
}

// Before WSL is installed the run must record whether this nested guest can
// host a hypervisor at all: WSL2 needs one, and our accelerators may not
// provide a usable one. Measured, not assumed.
func TestGenerateVirtualizationProbeScript_ReportsHypervisorCapability(t *testing.T) {
	s := GenerateVirtualizationProbeScript()

	assert.Contains(t, s, "HyperVRequirement", "the OS's own hypervisor-capability report")
	assert.Contains(t, s, "hypervisor visible to the guest",
		"HypervisorPresent means we run UNDER one, not that we can host one — say so")
	assert.Contains(t, s, "QUERY FAILED",
		"a failed feature query must not read as \"feature absent\" (iteration 14)")
	assert.Contains(t, s, "VirtualMachinePlatform",
		"the feature WSL2 needs — its state must be recorded")
	assert.NotContains(t, s, "Enable-WindowsOptionalFeature",
		"a probe observes; enabling is a separate, explicit decision")
}

func TestDevEnvStages_ProbeVirtualizationBeforeWSL(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")
	var names []string
	for _, st := range stages {
		names = append(names, st.Name)
	}

	require.Less(t, indexOf(names, "probe virtualization support"), indexOf(names, "enable WSL2 features"),
		"the hypervisor question must be answered before WSL is chosen")
}

func TestDevEnvStages_TranscriptToLogVolume(t *testing.T) {
	for _, st := range DevEnvStages("dmitry", "devcell", "Z:") {
		if st.ScriptFile != "" {
			continue // file-backed: logging lives in the script, not a wrapper
		}
		assert.Contains(t, st.Script, "Start-Transcript",
			"stage %q must transcript to the log volume", st.Name)
		assert.Contains(t, st.Script, GuestLogVolumeMarker,
			"stage %q must locate the volume by marker, not drive letter", st.Name)
		assert.Contains(t, st.Script, "Stop-Transcript",
			"stage %q must flush its transcript", st.Name)
	}
}

func TestBuildRunCommand_AttachesLogVolume(t *testing.T) {
	spec := testSpec()
	spec.LogVolumePath = "/tmp/devenv-logs.img"

	joined := strings.Join(BuildRunCommand(spec), " ")

	assert.Contains(t, joined, "file=/tmp/devenv-logs.img,format=raw")
	assert.Contains(t, joined, "usb-storage")
	assert.Contains(t, joined, "removable=true")
}

func TestBuildRunCommand_NoLogVolumeByDefault(t *testing.T) {
	joined := strings.Join(BuildRunCommand(testSpec()), " ")
	assert.NotContains(t, joined, "format=raw,if=none,id=usbfat0")
}

// Absence must be reported per stage, same contract as CollectGuestLogs: "the
// guest never wrote it" is a finding, not a silent skip.
func TestCollectVolumeLogs_ReportsAbsence(t *testing.T) {
	img := filepath.Join(t.TempDir(), "guest-logs.img")
	require.NoError(t, BuildGuestLogVolume(img))

	logs := CollectVolumeLogs(img, []string{
		StageLogName(1, "drivers"),
		StageLogName(2, "virtiofs"),
	})

	require.Len(t, logs, 2)
	for _, l := range logs {
		require.Error(t, l.Err, "an unwritten log must carry its absence, not vanish")
	}
}

// One table type, one logging contract: build provisioning and dev-env setup
// are both GuestStage tables, so both get component-grouped guest transcripts
// without either table mentioning logs.
func TestDefaultProvisionSteps_AreAGuestStageTableWithLogging(t *testing.T) {
	steps := DefaultProvisionSteps("ssh-ed25519 AAAA...", "devcell", "devcell")

	names := StageLogNames(steps)
	for i, st := range steps {
		assert.Equal(t, "provisioning", st.Component,
			"build provisioning is one component — one log for the whole phase")
		assert.Equal(t, "001-devenv-provisioning.log", names[i])
		if st.ScriptFile != "" {
			continue // file-backed: logging lives in the script, not a wrapper
		}
		assert.Contains(t, st.Script, "Start-Transcript",
			"step %q must transcript like every other guest stage", st.Name)
	}
}

// A build VM under TCG wastes ~3 GB on WerFault instances and Defender scans
// that serve no purpose in a disposable build environment. The provisioning
// pipeline must include a hardening step that disables both.
func TestDefaultProvisionSteps_IncludesEmulationHardening(t *testing.T) {
	steps := DefaultProvisionSteps("ssh-ed25519 AAAA...", "devcell", "devcell")

	var found bool
	for _, st := range steps {
		if st.Name == "Harden for emulation" {
			found = true
			assert.Contains(t, st.Script, "WerFault",
				"hardening step must disable WerFault")
			assert.Contains(t, st.Script, "DisableRealtimeMonitoring",
				"hardening step must disable Defender real-time monitoring")
			break
		}
	}
	assert.True(t, found, "DefaultProvisionSteps must include a 'Harden for emulation' stage")
}

// A checkpoint may only follow a stage whose success was verified. The engine
// install tolerates its SSH session dropping, so "did not fail" does not mean
// "finished" — checkpointing after it once saved a half-registered engine that
// every later resume inherited.
func TestWSLReadyCheckpoint_FollowsAVerifiedStage(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")

	idx := -1
	for i, st := range stages {
		if st.Name == wslReadyCheckpointStage {
			idx = i
			break
		}
	}
	require.Greater(t, idx, 0, "resume stage %q must exist and not be first", wslReadyCheckpointStage)

	preceding := stages[idx-1]
	require.False(t, preceding.ToleratesDisconnect,
		"the checkpoint would capture unverified state: %q may drop its SSH session", preceding.Name)
	require.True(t, stages[idx].ToleratesDisconnect || stages[idx].RebootAfter,
		"the stage resumed into should be the flaky one, re-run each time, not baked in")
}

// The WSL2 utility VM is created on a running hypervisor, so Hyper-V must be
// installed and launched BEFORE the WSL2 features and the distro import.
// Run 20260801T090038 proved the cost of getting this wrong: the import died
// with Wsl/Service/RegisterDistro/CreateVm/HCS/HCS_E_HYPERV_NOT_INSTALLED
// while Microsoft-Hyper-V was absent and hypervisorlaunchtype was unset.
func TestDevEnvStages_HyperVBeforeWSL(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")
	var names []string
	for _, st := range stages {
		names = append(names, st.Name)
	}

	hv := indexOf(names, "enable Hyper-V hypervisor")
	require.GreaterOrEqual(t, hv, 0, "the pipeline must enable the hypervisor")
	require.Less(t, hv, indexOf(names, "enable WSL2 features"),
		"the hypervisor is a prerequisite of the WSL2 platform, not a follow-up")
	require.Less(t, hv, indexOf(names, "import NixOS-WSL distro"), "must precede the import")

	require.True(t, stages[hv].RebootAfter,
		"hypervisorlaunchtype is read at boot — the stage is worthless without a reboot")
}

func TestGenerateHyperVEnableScript_InstallsAndRequestsLaunch(t *testing.T) {
	s := GenerateHyperVEnableScript()

	assert.Contains(t, s, "Microsoft-Hyper-V-Hypervisor",
		"VirtualMachinePlatform alone does not launch a hypervisor")
	assert.Contains(t, s, "bcdedit /set hypervisorlaunchtype auto",
		"the hypervisor must be told to launch at boot")
	assert.Contains(t, s, "ENABLE FAILED",
		"if the payload is missing from our media, say so instead of continuing quietly")
	// State is asserted after the reboot, by the verify stage — not here,
	// where the answer could only ever be "pending".
	assert.NotContains(t, s, "hyperv started",
		"this stage requests; the verify stage judges")
}

// Installed and started fail for different reasons — a missing payload versus
// a hypervisor that cannot launch on emulated EL2 — so they are separate
// assertions with separate messages.
func TestGenerateHyperVVerifyScript_AssertsInstalledAndStartedSeparately(t *testing.T) {
	s := GenerateHyperVVerifyScript()

	assert.Contains(t, s, "hyperv installed: ", "must report the installed fact")
	assert.Contains(t, s, "hyperv started: ", "must report the started fact")
	assert.Contains(t, s, "throw 'hyperv installed: False", "installed failure throws on its own")
	assert.Contains(t, s, "throw 'hyperv started: False", "started failure throws on its own")

	// Evidence, not a single ambiguous signal: HypervisorPresent is True merely
	// because we run under QEMU, so the started verdict also needs the launch
	// type and the Host Compute Service.
	assert.Contains(t, s, "hypervisorlaunchtype")
	assert.Contains(t, s, "vmcompute")
	// Iteration 20: launchtype+vmcompute reported started:True while HCS still
	// said HYPERV_NOT_INSTALLED. Only the hypervisor's own log proves it booted.
	assert.Contains(t, s, "Hyper-V-Hypervisor-Operational",
		"the started verdict must rest on the hypervisor's own launch log")
}

// The hypervisor must be installed, rebooted into, and verified before the
// WSL2 features and the distro import that depend on it.
func TestDevEnvStages_HyperVVerifiedBeforeWSLFeatures(t *testing.T) {
	stages := DevEnvStages("dmitry", "devcell", "Z:")
	var names []string
	for _, st := range stages {
		names = append(names, st.Name)
	}

	enable := indexOf(names, "enable Hyper-V hypervisor")
	verify := indexOf(names, "verify Hyper-V running")
	require.GreaterOrEqual(t, enable, 0)
	require.Less(t, enable, verify, "enable, reboot, then judge")
	require.Less(t, verify, indexOf(names, "enable WSL2 features"),
		"no point enabling the WSL2 platform on a machine with no hypervisor")
	require.True(t, stages[enable].RebootAfter,
		"hypervisorlaunchtype is read at boot — the stage is worthless without a reboot")
}
