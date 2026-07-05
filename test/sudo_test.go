// sudo_test.go — CELL-86: sudo works in pure (nix2container) images.
//
// Background: `pkgs.sudo.override { withPam = false; }` strips PAM linkage
// from the main sudo binary but does NOT propagate to the sudoers.so policy
// plugin, which is built in a separate derivation step. The plugin remains
// PAM-linked and calls pam_start("sudo", ...) at load time. Without a
// /etc/pam.d/sudo present in the image, plugin init aborts with
// "unable to initialize PAM: Critical error - immediate abort" and every
// `sudo` invocation dies before any policy check runs.
//
// Fix: pure-image builder (nixhome/image.nix) stages a permissive
// /etc/pam.d/sudo pointing at pam_permit.so so pam_start succeeds.
//
// L1: file-content wiring on image.nix.
// L2: container exec — `sudo whoami` returns "root" in a fresh pure cell.

package container_test

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// L1 — Wiring checks (file content)
// ---------------------------------------------------------------------------

// TestSudo_ImageNixStagesPamStub asserts the pure-image build stages a
// /etc/pam.d/sudo PAM stub that lets the sudoers.so plugin's pam_start
// succeed. Without this stub, the plugin aborts before any sudo policy
// check can run (CELL-86).
func TestSudo_ImageNixStagesPamStub(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	if !strings.Contains(imgNix, "/etc/pam.d/sudo") {
		t.Fatal("image.nix doesn't stage /etc/pam.d/sudo — sudoers.so plugin's pam_start will abort, breaking every `sudo` call in pure cells")
	}
	if !strings.Contains(imgNix, "pam_permit.so") {
		t.Fatal("image.nix references /etc/pam.d/sudo but doesn't use pam_permit.so — without a permissive module, plugin init still fails")
	}
	if !strings.Contains(imgNix, "pkgs.linux-pam") &&
		!strings.Contains(imgNix, "${pkgs.linux-pam}") &&
		!strings.Contains(imgNix, "linux-pam}") {
		t.Fatal("image.nix uses pam_permit.so but doesn't reference pkgs.linux-pam — module path won't resolve to a nix store path")
	}
}

// TestSudo_SudoersPreservesNixEnv pins env_keep for nix-related vars.
// Without these env_keep entries, `Defaults env_reset` (correct behavior in
// multi-user contexts) strips SSL_CERT_FILE / NIX_SSL_CERT_FILE across sudo,
// and `sudo nix profile add nixpkgs#foo` then fails with "SSL peer
// certificate ... was not OK" against cache.nixos.org. Single-user cell
// with NOPASSWD:ALL means env_reset's privilege-escalation protection
// is moot for these vars — keeping them is a UX fix, not a security regression.
func TestSudo_SudoersPreservesNixEnv(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	// Block: the sudoers heredoc. Search the env_keep line within it.
	sudoersStart := strings.Index(imgNix, "cat > $out/etc/sudoers <<EOF")
	if sudoersStart == -1 {
		t.Fatal("sudoers heredoc anchor missing — image.nix shape changed")
	}
	sudoersEnd := strings.Index(imgNix[sudoersStart:], "chmod 0440 $out/etc/sudoers")
	if sudoersEnd == -1 {
		t.Fatal("sudoers heredoc end (chmod 0440) missing")
	}
	block := imgNix[sudoersStart : sudoersStart+sudoersEnd]

	if !strings.Contains(block, "env_reset") {
		t.Fatal("sudoers missing `Defaults env_reset` — security baseline broken")
	}
	if !strings.Contains(block, "env_keep") {
		t.Fatal("sudoers missing `Defaults env_keep` — nix env vars will be stripped across sudo, breaking `sudo nix profile add nixpkgs#foo`")
	}
	// Pin the minimum set we need preserved. Adding more is fine; dropping
	// one of these is the regression we're guarding against.
	for _, v := range []string{"SSL_CERT_FILE", "NIX_SSL_CERT_FILE", "NIX_PATH", "LOCALE_ARCHIVE"} {
		if !strings.Contains(block, v) {
			t.Errorf("env_keep missing %q — `sudo nix ...` may fail or emit warnings", v)
		}
	}
}

// TestSudo_PamStubCoversAllPamPhases asserts the stub covers all four PAM
// phases (auth, account, session, password). Missing any phase causes
// pam_acct_mgmt / pam_open_session / etc. to fail with "no module".
func TestSudo_PamStubCoversAllPamPhases(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	// Find the /etc/pam.d/sudo heredoc block. Be permissive about the marker
	// name (PAMEOF, PAM_EOF, etc.) — assert each phase keyword appears in a
	// `<phase> sufficient` form near the pam_permit.so reference.
	idx := strings.Index(imgNix, "/etc/pam.d/sudo")
	if idx == -1 {
		t.Fatal("no /etc/pam.d/sudo block — TestSudo_ImageNixStagesPamStub should also fail")
	}
	// Scan forward ~1500 bytes for the heredoc body.
	end := idx + 1500
	if end > len(imgNix) {
		end = len(imgNix)
	}
	block := imgNix[idx:end]

	for _, phase := range []string{"auth", "account", "session", "password"} {
		if !strings.Contains(block, phase+" ") && !strings.Contains(block, phase+"\t") {
			t.Errorf("PAM stub missing `%s` phase — sudo will fail when sudoers.so calls pam_%s_mgmt", phase, phase)
		}
	}
}

// TestSudo_DockerfileStagesEnvKeep pins the impure (Debian-based) Dockerfile's
// sudoers config. Mirrors TestSudo_SudoersPreservesNixEnv from the pure path
// (image.nix) — the env_keep set must be the SAME on both image variants,
// because `TestSudo_PreservesNixEnv` (L2) runs against whatever image CI ships,
// which today is the impure Dockerfile output (`docker-build` → `docker-bake`
// → images/Dockerfile). Without this, Debian's stock /etc/sudoers (env_reset
// + no env_keep) strips SSL_CERT_FILE / NIX_SSL_CERT_FILE / LOCALE_ARCHIVE on
// every `sudo` invocation and `sudo nix profile add nixpkgs#foo` fails on
// cert validation against cache.nixos.org.
func TestSudo_DockerfileStagesEnvKeep(t *testing.T) {
	df := readImagesDockerfile(t)

	if !strings.Contains(df, "env_keep") {
		t.Fatal("images/Dockerfile doesn't add `Defaults env_keep += ...` to /etc/sudoers — Debian's stock sudoers env_reset will strip SSL_CERT_FILE/NIX_SSL_CERT_FILE/LOCALE_ARCHIVE across sudo, breaking `sudo nix profile add nixpkgs#foo` on cert validation")
	}
	// Same minimum set as the pure path (image.nix). Adding more is fine;
	// dropping one is the regression we're guarding against.
	for _, v := range []string{"SSL_CERT_FILE", "NIX_SSL_CERT_FILE", "NIX_PATH", "LOCALE_ARCHIVE"} {
		if !strings.Contains(df, v) {
			t.Errorf("images/Dockerfile sudoers config missing %q — `sudo nix ...` will lose this var across the privilege boundary", v)
		}
	}
}

// TestSudo_DockerfileSetsNixSSLEnv asserts the impure image's OCI Env carries
// SSL_CERT_FILE / NIX_SSL_CERT_FILE / LOCALE_ARCHIVE so docker exec sessions
// inherit them. Without these on the image config, even a correct env_keep
// has nothing to keep — sudo's parent env doesn't contain the vars in the
// first place.
func TestSudo_DockerfileSetsNixSSLEnv(t *testing.T) {
	df := readImagesDockerfile(t)

	for _, v := range []string{"SSL_CERT_FILE=", "NIX_SSL_CERT_FILE=", "LOCALE_ARCHIVE="} {
		// Match an `ENV <var>=...` line (Dockerfile ENV syntax). Substring
		// check is sufficient because the only places these tokens legitimately
		// appear with a trailing `=` are ENV directives or shell assignments
		// inside RUN.
		if !strings.Contains(df, "ENV "+v) && !strings.Contains(df, "ENV "+strings.TrimSuffix(v, "=")+" ") {
			t.Errorf("images/Dockerfile doesn't set %s via ENV — docker exec sessions won't inherit it; sudo env_keep then has nothing to keep, so TestSudo_PreservesNixEnv fails", v)
		}
	}
}

// ---------------------------------------------------------------------------
// L2 — Container behavior (requires docker; skip otherwise)
// ---------------------------------------------------------------------------

// TestSudo_WorksInFreshCell pins the user-visible bug. Pre-CELL-86, this
// fails with "unable to initialize PAM: Critical error - immediate abort"
// before sudo even reads /etc/sudoers. With the PAM stub in place, the
// sudoers plugin proceeds to its policy check, sees NOPASSWD:ALL for the
// session user, and runs the command.
func TestSudo_WorksInFreshCell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, nil)

	out, code := exec(t, c, []string{"sudo", "whoami"})
	if code != 0 {
		t.Fatalf("`sudo whoami` failed (exit=%d) — CELL-86 PAM stub not in image: %s", code, out)
	}
	if strings.TrimSpace(out) != "root" {
		t.Fatalf("`sudo whoami` returned %q, want \"root\"", strings.TrimSpace(out))
	}
}

// TestSudo_NoPamInitErrorOnPlainSudo specifically asserts that the
// pre-CELL-86 error message no longer appears anywhere in sudo's output.
// Catches regressions where the stub is present but malformed.
func TestSudo_NoPamInitErrorOnPlainSudo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, nil)

	out, _ := exec(t, c, []string{"sudo", "-n", "true"})
	pamErrors := []string{
		"unable to initialize PAM",
		"Critical error - immediate abort",
		"PAM account management error",
	}
	for _, msg := range pamErrors {
		if strings.Contains(out, msg) {
			t.Fatalf("sudo emitted pre-CELL-86 PAM error %q: %s", msg, out)
		}
	}
}

// TestSudo_PreservesNixEnv exercises the L2 effect of the env_keep entries:
// nix-related env vars set by the image (NIX_SSL_CERT_FILE, SSL_CERT_FILE,
// LOCALE_ARCHIVE) must survive a sudo invocation. Without env_keep,
// `sudo nix profile add nixpkgs#foo` fails on cert validation against
// cache.nixos.org — that's the user-visible bug this test pins.
//
// Cheap test (no network): just inspect sudo's env. The full
// "sudo nix profile add" round-trip is covered by TestSudo_NixInstallHtop
// behind the integration build tag.
func TestSudo_PreservesNixEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, nil)

	out, code := exec(t, c, []string{"sudo", "env"})
	if code != 0 {
		t.Fatalf("`sudo env` failed (exit=%d): %s", code, out)
	}
	for _, want := range []string{"NIX_SSL_CERT_FILE=", "SSL_CERT_FILE=", "LOCALE_ARCHIVE="} {
		if !strings.Contains(out, want) {
			t.Errorf("sudo env missing %q — env_keep not effective; sudo nix install paths will fail on cert/locale issues\n--- sudo env output ---\n%s", want, out)
		}
	}
}
