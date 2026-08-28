package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Host-side verification of the nixhome half of the WSL2 chain (CELL-405).
//
// The goal splits into two independent claims:
//
//	(A) the nixhome content is right — the flake builds and ACTIVATES for the
//	    distro user on aarch64-linux
//	(B) the Windows/WSL plumbing delivers it — virtiofs, drvfs, login-shell PATH
//
// (A) needs no VM: NixOS-WSL is NixOS and this host is aarch64, so the exact
// target the guest activates can be built and run here. That matters because
// (A) silently sank every E2E that ever reached the final stage — nixhome
// pinned `nixos` while the stages renamed the distro to the Windows $USER, and
// home-manager's guard rejected the mismatch:
//
//	Error: USER is set to "dmitry" but we expect "nixos"
//
// Discovering that cost a 40-minute TCG boot and looked like a stage failure.
// Here it takes about a second and names the real problem.

// nixBin returns a usable nix binary, or "" when none is available.
func nixBin() string {
	if p, err := exec.LookPath("nix"); err == nil {
		return p
	}
	const daemonNix = "/nix/var/nix/profiles/default/bin/nix"
	if _, err := os.Stat(daemonNix); err == nil {
		return daemonNix
	}
	return ""
}

// wslActivationTarget is the flake attribute the guest's home-manager stage
// activates, for this machine's architecture. It mirrors the ARCH_SUFFIX the
// guest computes from `uname -m` in home-manager.ps1 — the point of this test
// is to exercise the SAME attribute the guest will.
func wslActivationTarget() string {
	suffix := ""
	if runtime.GOARCH == "arm64" {
		suffix = "-aarch64"
	}
	return "./nixhome#homeConfigurations.wsl-base" + suffix + ".activationPackage"
}

// The activation guard must accept WSLDistroUser. This is the assertion that
// would have caught the contradiction before any VM booted.
func TestNixhomeWSLProfile_ActivatesAsTheDistroUser(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a home-manager generation — minutes on a cold store")
	}
	nix := nixBin()
	if nix == "" {
		t.Skip("no nix on this host")
	}

	out := filepath.Join(t.TempDir(), "result")
	build := exec.Command(nix, "build",
		"--extra-experimental-features", "nix-command",
		"--extra-experimental-features", "flakes",
		wslActivationTarget(), "--out-link", out)
	build.Dir = repoRoot(t)
	if b, err := build.CombinedOutput(); err != nil {
		// A dead nix daemon looks nothing like a config error; say which it is.
		if strings.Contains(string(b), "daemon-socket") {
			t.Skipf("nix daemon unavailable: %s", b)
		}
		require.NoError(t, err, "building %s: %s", wslActivationTarget(), b)
	}

	// Activation is guarded on USER first and HOME second. Point HOME at a
	// throwaway dir: reaching the HOME check proves the USER check passed,
	// which is the fact under test — and it keeps the run from touching a
	// real home directory.
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	act := exec.Command(filepath.Join(out, "activate"))
	act.Env = append(os.Environ(), "USER="+WSLDistroUser, "HOME="+home)
	got, _ := act.CombinedOutput()

	assert.NotContains(t, string(got), `USER is set to`,
		"the profile must accept WSLDistroUser (%q) — a username mismatch here is "+
			"exactly what made home-manager unactivatable in the guest", WSLDistroUser)
	t.Logf("activation output as USER=%s:\n%s", WSLDistroUser, strings.TrimSpace(string(got)))
}

// A gate that cannot fail is not a gate: prove the guard actually fires for a
// user the profile was NOT built for, so the test above is not passing merely
// because activation never got that far.
func TestNixhomeWSLProfile_RejectsAForeignUser(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a home-manager generation — minutes on a cold store")
	}
	nix := nixBin()
	if nix == "" {
		t.Skip("no nix on this host")
	}

	out := filepath.Join(t.TempDir(), "result")
	build := exec.Command(nix, "build",
		"--extra-experimental-features", "nix-command",
		"--extra-experimental-features", "flakes",
		wslActivationTarget(), "--out-link", out)
	build.Dir = repoRoot(t)
	if b, err := build.CombinedOutput(); err != nil {
		if strings.Contains(string(b), "daemon-socket") {
			t.Skipf("nix daemon unavailable: %s", b)
		}
		require.NoError(t, err, "building %s: %s", wslActivationTarget(), b)
	}

	const foreign = "definitely-not-the-profile-user"
	act := exec.Command(filepath.Join(out, "activate"))
	act.Env = append(os.Environ(), "USER="+foreign, "HOME="+t.TempDir())
	got, _ := act.CombinedOutput()

	assert.Contains(t, string(got), "USER is set to",
		"the guard must reject a foreign user — otherwise the sibling test is vacuous")
}
