package runner

import (
	"runtime"
	"strings"
	"testing"
)

const (
	testCoreImage  = "ghcr.io/test/devcell:v0.0.0-core"
	testContainer  = "devcell-thin-builder"
	testVolume     = "devcell-nix-store"
	testNixhome    = "/home/bob/nixhome"
	testThinTag    = "devcell-user:base-thin"
	testStack      = "base"
)

func TestThinBuildArgv_DockerRunStructure(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64")
	if argv[0] != "docker" || argv[1] != "run" || argv[2] != "--rm" {
		t.Errorf("should start with docker run --rm, got: %v", argv[:min(3, len(argv))])
	}
}

func TestThinBuildArgv_MountsNixVolume(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	found := false
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && argv[i+1] == "devcell-nix-store:/nix" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v devcell-nix-store:/nix in argv: %v", argv)
	}
}

func TestThinBuildArgv_NixDbOnVolume(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if strings.Contains(script, "COPY nix_var/") {
		t.Error("inner Dockerfile should NOT copy nix DB — it lives on the volume with the store")
	}
}

func TestThinBuildArgv_MountsDockerSocket(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	found := false
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && argv[i+1] == "/var/run/docker.sock:/var/run/docker.sock" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected docker socket mount in argv: %v", argv)
	}
}

func TestThinBuildArgv_MountsNixhome(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	found := false
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && argv[i+1] == "/home/bob/nixhome:/opt/nixhome" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected nixhome mount in argv: %v", argv)
	}
}

func TestThinBuildArgv_RunsHomeManagerSwitch(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "home-manager switch") {
		t.Errorf("should run home-manager switch, got: %s", script)
	}
}

func TestThinBuildArgv_RunsDockerBuild(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "docker build") {
		t.Errorf("should run docker build inside container, got: %s", script)
	}
	if !strings.Contains(script, testThinTag) {
		t.Errorf("inner docker build should tag as %s, got: %s", testThinTag, script)
	}
}

func TestThinBuildArgv_InnerDockerfileFromNixCore(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "FROM "+testCoreImage) {
		t.Errorf("inner Dockerfile should FROM %s, got script", testCoreImage)
	}
}

func TestThinBuildArgv_ArchSuffix(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "devcell-base-aarch64") {
		t.Errorf("aarch64 should include arch suffix, got: %s", script)
	}
}

func TestThinBuildArgv_NoArchSuffixForX86(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	// The flake ref should NOT have an arch suffix for x86_64
	if strings.Contains(script, "devcell-base-x86") {
		t.Errorf("x86_64 flake ref should NOT have arch suffix, got: %s", script)
	}
	if !strings.Contains(script, "#devcell-base\n") && !strings.Contains(script, "#devcell-base ") {
		// Just verify it doesn't have aarch64 suffix
		if strings.Contains(script, "devcell-base-aarch64") {
			t.Errorf("x86_64 should not have aarch64 suffix")
		}
	}
}

func TestThinBuildArgv_CopiesConfigInContext(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	for _, dir := range []string{"opt_devcell", "etc_devcell", "lib"} {
		if !strings.Contains(script, dir) {
			t.Errorf("should copy %s to build context, got: %s", dir, script)
		}
	}
}

func TestThinBuildArgv_NixConfDaemonMode(t *testing.T) {
	nativeArch := "x86_64"
	if runtime.GOARCH == "arm64" {
		nativeArch = "aarch64"
	}
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, nativeArch)
	script := argv[len(argv)-1]
	if !strings.Contains(script, "sandbox = true") {
		t.Error("should set sandbox = true (isolate builds)")
	}
	if !strings.Contains(script, "DEVCELL_NIX_MAX_JOBS:-auto") {
		t.Error("should set max-jobs from DEVCELL_NIX_MAX_JOBS (default auto)")
	}
	if !strings.Contains(script, "nix-daemon") {
		t.Error("should start nix-daemon (avoids /homeless-shelter race)")
	}
	if !strings.Contains(script, "NIX_REMOTE=daemon") {
		t.Error("should set NIX_REMOTE=daemon to use nix daemon")
	}
}

func TestThinBuildArgv_RemovesHomelessShelter(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "rm -rf /homeless-shelter") {
		t.Error("should rm -rf /homeless-shelter before nix runs")
	}
}

func TestThinBuildArgv_CreatesDevcellUser(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "devcell") && !strings.Contains(script, "1000") {
		t.Error("should create devcell user with UID 1000")
	}
	if !strings.Contains(script, "chown -R 1000:1000 /opt/devcell") {
		t.Error("should chown /opt/devcell to devcell user")
	}
}

func TestThinBuildArgv_InstallsSystemToolsOnVolume(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "devcell-tools") {
		t.Error("should install system tools into a dedicated profile on the volume")
	}
	for _, pkg := range []string{"shadow", "sudo", "gosu", "tini", "docker", "nix-ld"} {
		if !strings.Contains(script, "nixpkgs#"+pkg) {
			t.Errorf("builder should install nixpkgs#%s on the volume", pkg)
		}
	}
}

// CELL-76: fontconfig must be installed into the devcell-tools profile so its
// fonts.conf lives at the stable path /nix/var/nix/profiles/devcell-tools/etc/fonts/fonts.conf
// (no store hash), mirroring the LOCALE_ARCHIVE pattern.
func TestThinBuildArgv_InstallsFontconfigOnVolume(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	// ^out is load-bearing: fontconfig's default output is `bin`; fonts.conf
	// lives in `out` ("$out contains all the config" per nixpkgs).
	if !strings.Contains(script, "nixpkgs#fontconfig^out") {
		t.Error("builder should install nixpkgs#fontconfig^out into devcell-tools — the `out` output carries etc/fonts/fonts.conf")
	}
}

// CELL-76: without FONTCONFIG_FILE, fontconfig has no main config in thin cells
// (FONTCONFIG_PATH points at a dir with only conf.d/) and every fc-* call fails with
// "Cannot load default config file" — fonts are installed but unresolvable.
func TestThinBuildArgv_SetsFontconfigFile(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ENV FONTCONFIG_FILE=/nix/var/nix/profiles/devcell-tools/etc/fonts/fonts.conf") {
		t.Error("inner Dockerfile should set ENV FONTCONFIG_FILE to the devcell-tools fonts.conf (mirrors full image's image.nix fontconfig env)")
	}
}

// CELL-75: mise-native shared installs — the image env must point mise at
// the baked install dir so fresh cells resolve declared tools read-only
// instead of re-downloading them into every cell home.
func TestThinBuildArgv_SetsMiseSharedInstallDirs(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ENV MISE_SHARED_INSTALL_DIRS=/opt/devcell/.local/share/mise/installs") {
		t.Error("inner Dockerfile should set ENV MISE_SHARED_INSTALL_DIRS to the baked mise install dir (mise ≥2026.3.9 shared installs)")
	}
}

// CELL-76: pkgs.fontconfig's fonts.conf includes ONLY /etc/fonts/conf.d.
// Without the bridge symlink to home-manager's conf.d, none of the hm font
// setup loads (font dirs, default aliases) and apps see only dejavu-minimal
// (1 font). Mirrors the full image's bridge in image.nix.
func TestThinBuildArgv_FontconfigConfDBridge(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ln -sfn /opt/devcell/.config/fontconfig/conf.d /etc/fonts/conf.d") {
		t.Error("inner Dockerfile should symlink /etc/fonts/conf.d → home-manager's fontconfig conf.d")
	}
}

func TestThinBuildArgv_InnerDockerfileNoNixInstall(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if strings.Contains(script, "nix profile install --priority") {
		t.Error("inner Dockerfile should NOT install nix packages — all tools live on the volume")
	}
}

func TestThinBuildArgv_SetsNixLdInterpreter(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ld-linux") && !strings.Contains(script, "nix-ld") {
		t.Error("inner Dockerfile should set nix-ld as /lib/ld-linux-* interpreter")
	}
}

func TestThinBuildArgv_SetsUserDevcell(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "USER=devcell") {
		t.Error("should set USER=devcell for home-manager")
	}
}

func TestThinBuildArgv_CreatesNixLdShim(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "nix-ld") {
		t.Error("should create nix-ld shim for mise binaries")
	}
	if !strings.Contains(script, "ld-linux") {
		t.Error("should create /lib/ld-linux-* symlink")
	}
}

func TestThinBuildArgv_UsesEntrypointSh(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	found := false
	for i, a := range argv {
		if a == "--entrypoint" && i+1 < len(argv) && argv[i+1] == "sh" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --entrypoint sh in argv: %v", argv)
	}
}

func TestThinBuildArgv_PreservesDefaultProfile(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if strings.Contains(script, "rm -f /nix/var/nix/profiles/default") {
		t.Error("must NOT delete default profile — it provides sh for subsequent container starts")
	}
}

func TestThinBuildArgv_RootOwnsHomeForNixEnv(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "chown -R 0:0 /opt/devcell") {
		t.Error("should chown /opt/devcell to root so nix-env uses user profile, not root's default")
	}
}

func TestThinBuildArgv_SudoShim(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "/usr/local/bin/sudo") {
		t.Error("should create sudo shim — home-manager activation calls sudo but builder runs as root")
	}
}

func TestThinBuildArgv_SavesNixPathBeforeCleanup(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "NIX_DIR=") {
		t.Error("should save nix store path before any cleanup")
	}
}

func TestThinBuildArgv_InstallsDockerClient(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "docker-client") {
		t.Error("should install docker-client via nix for inner docker build")
	}
}

func TestThinBuildArgv_SslCertInNixConf(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ssl-cert-file") {
		t.Error("nix.conf must include ssl-cert-file for daemon to reach cache.nixos.org")
	}
}

func TestDockerHostPath_PassthroughWhenNoMapping(t *testing.T) {
	got := DockerHostPath("/Users/dmitry/dev/proj/nixhome")
	if got != "/Users/dmitry/dev/proj/nixhome" {
		t.Errorf("should pass through host paths unchanged, got: %s", got)
	}
}

func TestDockerHostPath_ResolvesContainerAlias(t *testing.T) {
	t.Setenv("DEVCELL_HOST_PROJECT_DIR", "/Users/dmitry/dev/dimmkirr/devcell")
	got := DockerHostPath("/devcell-256/nixhome")
	if got != "/Users/dmitry/dev/dimmkirr/devcell/nixhome" {
		t.Errorf("should resolve container alias to host path, got: %s", got)
	}
}

func TestDockerHostPath_ResolvesBaseDir(t *testing.T) {
	t.Setenv("DEVCELL_HOST_PROJECT_DIR", "/Users/bob/projects/myapp")
	got := DockerHostPath("/devcell-42/some/sub/path")
	if got != "/Users/bob/projects/myapp/some/sub/path" {
		t.Errorf("should resolve any /devcell-NNN prefix, got: %s", got)
	}
}

func TestDockerHostPath_NoEnvNoChange(t *testing.T) {
	t.Setenv("DEVCELL_HOST_PROJECT_DIR", "")
	got := DockerHostPath("/devcell-256/nixhome")
	if got != "/devcell-256/nixhome" {
		t.Errorf("without env var should pass through, got: %s", got)
	}
}

// The canonical container-local profile path
// (/opt/devcell/.local/state/nix/profiles/profile) MUST resolve to an
// immutable /nix/store path, NOT to /nix/var/nix/profiles/per-user/root/profile.
// The per-user/root profile lives on the shared devcell-nix-store docker
// volume — every thin build's `home-manager switch` overwrites it, so a
// container labelled `ultimate` will lose packages the moment someone builds
// a leaner stack against the same volume (see the CELL-322 kirr.dev-540
// clobber where chromium/patchright disappeared from PATH).
//
// The builder captures the just-switched target's realpath and bakes THAT
// store path into the image. Once baked, no cross-container switch can touch
// what this image sees.
func TestThinBuildArgv_ProfileSymlinkResolvesToStorePath(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]

	// MUST NOT symlink into the mutable shared-volume slot.
	bad := "ln -sfT /nix/var/nix/profiles/per-user/root/profile /opt/devcell/.local/state/nix/profiles/profile"
	if strings.Contains(script, bad) {
		t.Errorf("builder must NOT redirect container profile into the shared-volume mutable slot; found: %q", bad)
	}

	// MUST resolve the profile's realpath after home-manager switch.
	if !strings.Contains(script, "readlink -f /nix/var/nix/profiles/per-user/root/profile") {
		t.Error("builder must resolve the just-switched profile's realpath (readlink -f) before baking the container-local symlink")
	}

	// MUST symlink the container-local profile to that resolved store path.
	// Match the shell expression that assigns realpath into a var and then uses it as the ln target.
	if !strings.Contains(script, `ln -sfT "$HM_PROFILE" /opt/devcell/.local/state/nix/profiles/profile`) {
		t.Error("builder must symlink /opt/devcell/.local/state/nix/profiles/profile → $HM_PROFILE (the resolved store path)")
	}
}

// A store path only reachable through a symlink baked inside an image is not
// a GC root — `nix-collect-garbage` on the shared volume can't see through
// image layers. Without a per-stack GC root, a later cleanup wipes the
// home-manager-path our container depends on, breaking every already-built
// image the next time it's started fresh.
//
// The builder must register the resolved profile as an indirect GC root
// under /nix/var/nix/gcroots/ so the store path stays alive on the volume.
func TestThinBuildArgv_ProfilePinnedAsGCRoot(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", testStack, "", "testproj")
	script := argv[len(argv)-1]

	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/") {
		t.Error("builder must register the resolved home-manager profile as a GC root under /nix/var/nix/gcroots/devcell/ so the shared-volume GC does not reap it")
	}

	// Per-project + per-stack GC root name — different projects and stacks must
	// not clobber each other's roots (CELL-320).
	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/testproj-local-profile") {
		t.Error("GC root name must be project-scoped: <project>-<hmTarget><arch>-profile")
	}
}

// CELL-320: GC roots must be scoped by project name so containers from
// different projects don't overwrite each other's roots. Without project
// scoping, the last project to build wins — its ln -sfT overwrites the
// previous project's root, and the next nix-collect-garbage reaps the
// now-unrooted derivations.
func TestThinBuildArgv_ProjectScopedGCRoots(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", testStack, "", "myproject")
	script := argv[len(argv)-1]

	// Profile root must include project name.
	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/myproject-local-profile") {
		t.Error("GC root for profile must be project-scoped: /nix/var/nix/gcroots/devcell/<project>-<hmTarget><arch>-profile")
	}

	// Generation root must include project name.
	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/myproject-local-generation") {
		t.Error("GC root for generation must be project-scoped: /nix/var/nix/gcroots/devcell/<project>-<hmTarget><arch>-generation")
	}
}

// Two different projects must produce different GC root paths.
func TestThinBuildArgv_DifferentProjectsDifferentRoots(t *testing.T) {
	argvA := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", testStack, "", "alpha")
	argvB := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", testStack, "", "beta")
	scriptA := argvA[len(argvA)-1]
	scriptB := argvB[len(argvB)-1]

	if !strings.Contains(scriptA, "alpha-local-profile") {
		t.Error("project alpha must have its own GC root")
	}
	if !strings.Contains(scriptB, "beta-local-profile") {
		t.Error("project beta must have its own GC root")
	}
	if strings.Contains(scriptA, "beta-local") {
		t.Error("project alpha must not reference project beta's root")
	}
}

// Arch suffix must appear in project-scoped GC root names.
func TestThinBuildArgv_ProjectScopedGCRootsWithArch(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "aarch64", testStack, "", "myproject")
	script := argv[len(argv)-1]

	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/myproject-local-aarch64-profile") {
		t.Error("GC root for profile must include arch suffix for aarch64")
	}
	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/myproject-local-aarch64-generation") {
		t.Error("GC root for generation must include arch suffix for aarch64")
	}
}

// The image's runtime ENV PATH must NOT include /nix/var/nix/profiles/per-user/root/profile/bin.
// That path lives on the shared devcell-nix-store volume and gets rewritten
// by every `home-manager switch` from any container — leaving it on PATH
// defeats the whole store-path-symlink fix because PATH lookup would still
// resolve tools through the mutable slot before the immutable one.
func TestThinBuildArgv_RuntimePathExcludesSharedProfileSlot(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]

	// Look at the `ENV PATH=` line specifically (the baked image env). The
	// build-time `export PATH=` lines inside the builder are fine — they only
	// affect the ephemeral builder container.
	envPathIdx := strings.Index(script, "ENV PATH=")
	if envPathIdx < 0 {
		t.Fatal("script must contain a baked ENV PATH= line")
	}
	envPathLine := script[envPathIdx:]
	if nl := strings.Index(envPathLine, "\n"); nl >= 0 {
		envPathLine = envPathLine[:nl]
	}
	if strings.Contains(envPathLine, "/nix/var/nix/profiles/per-user/root/profile/bin") {
		t.Errorf("baked ENV PATH must NOT include the mutable per-user/root profile bin; got: %s", envPathLine)
	}
	// Sanity: the container-local (now immutable) profile bin MUST still be on PATH.
	if !strings.Contains(envPathLine, "/opt/devcell/.local/state/nix/profiles/profile/bin") {
		t.Errorf("baked ENV PATH must still include /opt/devcell/.local/state/nix/profiles/profile/bin, got: %s", envPathLine)
	}
}

// home-manager activation (via the sudo shim) stages nix-managed agent
// configs to /etc/claude-code, /etc/codex, /etc/opencode, /etc/gemini inside
// the builder. The thin image must carry them, otherwise the entrypoint
// fragments (30-claude.sh etc.) silently skip the MCP merge.
func TestThinBuildArgv_ExportsAgentEtcDirs(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	for _, dir := range []string{"claude-code", "codex", "opencode", "gemini"} {
		ctxDir := "etc_" + strings.ReplaceAll(dir, "-", "_")
		if !strings.Contains(script, "/etc/"+dir+"/") {
			t.Errorf("builder script must export /etc/%s/ into the build context", dir)
		}
		if !strings.Contains(script, "COPY "+ctxDir+"/ /etc/"+dir+"/") {
			t.Errorf("inner Dockerfile must COPY %s/ to /etc/%s/", ctxDir, dir)
		}
	}
}

// Upstream prebuilt binaries (mise node/python, uv cpython) hardcode the FHS
// loader path /lib/ld-linux-*.so.* in their ELF headers. The builder plants
// nix-ld there for itself, but the final image (fresh FROM nixos/nix) never
// got it — every dynamically linked foreign binary failed exec with ENOENT.
// nix-ld is self-contained, so it is baked into the image as a real file and
// the interpreter paths are image-internal symlinks (no image→volume links).
func TestThinBuildArgv_BakesNixLdInterpreter(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, `install -m755 "$NIX_LD_BIN" /opt/devcell/.local/bin/nix-ld`) {
		t.Error("builder must bake the nix-ld binary into /opt/devcell/.local/bin (rides into image via opt_devcell COPY)")
	}
	for _, link := range []string{
		"ln -sf /opt/devcell/.local/bin/nix-ld /lib/ld-linux-aarch64.so.1",
		"ln -sf /opt/devcell/.local/bin/nix-ld /lib64/ld-linux-x86-64.so.2",
	} {
		if !strings.Contains(script, link) {
			t.Errorf("inner Dockerfile must create interpreter symlink: %s", link)
		}
	}
}

// CELL-38: when no local nixhome is available, the thin builder must use the
// prebaked github:DimmKirr/devcell ref directly with home-manager — no
// -v <path>:/opt/nixhome mount, --flake points at the github URL.

func TestThinBuildArgv_RemoteRefSkipsNixhomeMount(t *testing.T) {
	const remoteRef = "github:DimmKirr/devcell/main?dir=nixhome"
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, remoteRef, testThinTag, testStack, "x86_64")
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && strings.HasSuffix(argv[i+1], ":/opt/nixhome") {
			t.Errorf("remote ref must NOT mount nixhome dir, got: %s", argv[i+1])
		}
	}
}

func TestThinBuildArgv_RemoteRefUsedInHomeManagerSwitch(t *testing.T) {
	const remoteRef = "github:DimmKirr/devcell/main?dir=nixhome"
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, remoteRef, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	want := "home-manager switch --flake " + remoteRef + "#devcell-" + testStack
	if !strings.Contains(script, want) {
		t.Errorf("script must include `%s`, got:\n%s", want, script)
	}
}

func TestThinBuildArgv_LocalPathStillMounts(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	found := false
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && argv[i+1] == testNixhome+":/opt/nixhome" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("local path must still mount -v %s:/opt/nixhome", testNixhome)
	}
}

// CELL-41: thin build must thread the user-facing stack name and modules
// list into DEVCELL_STACK / DEVCELL_MODULES so the running container's
// metadata.json reflects what the user configured — not the home-manager
// target name ("local"), which is an implementation detail.

func TestThinBuildArgv_SetsDevcellStackFromCaller(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", "ultimate", "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "export DEVCELL_STACK=ultimate") {
		t.Errorf("script must set DEVCELL_STACK to the user-facing stack name (not the HM target), got script without that export")
	}
	if !strings.Contains(script, "home-manager switch --flake /opt/nixhome#devcell-local") {
		t.Errorf("HM target must stay separate from DEVCELL_STACK — flake URL should reference devcell-local")
	}
}

func TestThinBuildArgv_SetsDevcellModulesFromCaller(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", "", "foo,bar", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "export DEVCELL_MODULES=foo,bar") {
		t.Error("script must set DEVCELL_MODULES to the user-facing module CSV")
	}
}

func TestThinBuildArgv_BakesStackAndModulesEnvIntoImage(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", "ultimate", "foo,bar", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "ENV DEVCELL_STACK=ultimate") {
		t.Error("inner Dockerfile must bake ENV DEVCELL_STACK so the running container's writeMetadata sees it")
	}
	if !strings.Contains(script, "ENV DEVCELL_MODULES=foo,bar") {
		t.Error("inner Dockerfile must bake ENV DEVCELL_MODULES so writeMetadata captures the module list")
	}
}

func TestThinBuildArgv_EmptyStackAndModulesIsExplicit(t *testing.T) {
	// Empty values still get exported — the entrypoint's writeMetadata
	// distinguishes empty (modules: []) from missing (skip metadata write).
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, "local", "x86_64", "", "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "export DEVCELL_STACK=") {
		t.Error("empty stack must still appear as `export DEVCELL_STACK=` so writeMetadata fires")
	}
	if !strings.Contains(script, "export DEVCELL_MODULES=") {
		t.Error("empty modules must still appear as `export DEVCELL_MODULES=`")
	}
}

// --- Cross-architecture (DEVCELL_ARCH) ---

func TestThinBuildArgv_PlatformFlagOnBuilderContainer(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64", testStack, "", "testproj")
	found := false
	for i, a := range argv {
		if a == "--platform" && i+1 < len(argv) && argv[i+1] == "linux/amd64" {
			found = true
			break
		}
	}
	if !found {
		t.Error("builder docker run must include --platform linux/amd64 for x86_64 arch")
	}
}

func TestThinBuildArgv_PlatformFlagArm64(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64", testStack, "", "testproj")
	found := false
	for i, a := range argv {
		if a == "--platform" && i+1 < len(argv) && argv[i+1] == "linux/arm64" {
			found = true
			break
		}
	}
	if !found {
		t.Error("builder docker run must include --platform linux/arm64 for aarch64 arch")
	}
}

func TestThinBuildArgv_InnerDockerBuildPlatform(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64", testStack, "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "--platform linux/amd64") {
		t.Error("inner docker build must include --platform linux/amd64 for x86_64 arch")
	}
}

func TestThinBuildArgv_InnerDockerBuildPlatformArm64(t *testing.T) {
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "aarch64", testStack, "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "--platform linux/arm64") {
		t.Error("inner docker build must include --platform linux/arm64 for aarch64 arch")
	}
}

// GNU coreutils `cp` refuses to write through a dangling symlink — the builder
// must rm -f the dangling link before cp, otherwise the /etc/{passwd,group,...}
// materialisation fails with "cp: not writing through dangling symlink".
func TestThinBuildArgv_DanglingSymlinkFixRemovesBeforeCopy(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, `rm -f "$f"`) {
		t.Error(`dangling symlink fix must rm -f "$f" before cp — plain cp fails with "not writing through dangling symlink" (GNU coreutils)`)
	}
}

// /etc/nix/nix.conf may be a symlink into the read-only nix store (when the
// volume already has the base-system path). The script must rm + overwrite
// rather than append, otherwise both sed and cat silently fail on the
// read-only target.
func TestThinBuildArgv_OverwritesNixConf(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "rm -f /etc/nix/nix.conf") {
		t.Error("must rm -f /etc/nix/nix.conf before writing — it may be a symlink to a read-only store path")
	}
	if !strings.Contains(script, "cat > /etc/nix/nix.conf") {
		t.Error("must overwrite (>) nix.conf, not append (>>) — ensures our sandbox setting takes effect")
	}
}

// QEMU can't translate seccomp BPF programs, so cross-arch builds must disable
// Nix's sandbox — otherwise home-manager switch fails with
// "unable to load seccomp BPF program: Invalid argument".
func TestThinBuildArgv_CrossArchDisablesSandboxAndFilterSyscalls(t *testing.T) {
	crossArch := "x86_64"
	if runtime.GOARCH == "amd64" {
		crossArch = "aarch64"
	}
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, crossArch, testStack, "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "sandbox = false") {
		t.Error("cross-arch build must set sandbox = false")
	}
	if !strings.Contains(script, "filter-syscalls = false") {
		t.Error("cross-arch build must set filter-syscalls = false — QEMU can't translate seccomp BPF syscall numbers")
	}
}

// QEMU can't handle personality(PER_LINUX32) — cross-arch builds must clear
// extra-platforms so Nix doesn't attempt to build i686-linux derivations.
func TestThinBuildArgv_CrossArchDisablesExtraPlatforms(t *testing.T) {
	crossArch := "x86_64"
	if runtime.GOARCH == "amd64" {
		crossArch = "aarch64"
	}
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, crossArch, testStack, "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "extra-platforms =") {
		t.Error("cross-arch build must set extra-platforms = (empty) — QEMU can't handle personality() for i686")
	}
}

func TestThinBuildArgv_NativeArchOmitsExtraPlatforms(t *testing.T) {
	nativeArch := "x86_64"
	if runtime.GOARCH == "arm64" {
		nativeArch = "aarch64"
	}
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, nativeArch, testStack, "", "testproj")
	script := argv[len(argv)-1]
	if strings.Contains(script, "extra-platforms") {
		t.Error("native-arch build should NOT set extra-platforms — Nix defaults handle i686 correctly on real hardware")
	}
}

func TestThinBuildArgv_NativeArchEnablesSandboxAndFilterSyscalls(t *testing.T) {
	nativeArch := "x86_64"
	if runtime.GOARCH == "arm64" {
		nativeArch = "aarch64"
	}
	argv := ThinBuildArgvFull(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, nativeArch, testStack, "", "testproj")
	script := argv[len(argv)-1]
	if !strings.Contains(script, "sandbox = true") {
		t.Error("native-arch build should keep sandbox = true")
	}
	if !strings.Contains(script, "filter-syscalls = true") {
		t.Error("native-arch build should keep filter-syscalls = true")
	}
}

// NIX_LD must be baked as image ENV — nix-ld at the interpreter path reads it
// to find the real glibc loader. Shell rc sets it for interactive shells, but
// MCP servers and other non-login spawns need it from the container env.
func TestThinBuildArgv_BakesNixLdEnv(t *testing.T) {
	argv := ThinBuildArgv(testCoreImage, testContainer, testVolume, testNixhome, testThinTag, testStack, "x86_64")
	script := argv[len(argv)-1]
	if !strings.Contains(script, `--build-arg "NIX_LD=$NIX_LD"`) {
		t.Error("docker build must pass NIX_LD from the builder env as a build arg")
	}
	if !strings.Contains(script, "ARG NIX_LD") || !strings.Contains(script, "ENV NIX_LD=$NIX_LD") {
		t.Error("inner Dockerfile must accept ARG NIX_LD and bake ENV NIX_LD")
	}
}

