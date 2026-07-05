package main_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// cell build --pure (and cell claude --build --pure) must invoke the
// nix2container path strictly — no fallback to docker build. The runner
// composes an `nix build` command targeting the per-stack pure-image flake
// output, then loads the result into the Docker daemon.
//
// Tests the *argv composition* (pure unit, no nix/docker involvement).

func TestPureBuildArgv_TargetsNix2ContainerOutput(t *testing.T) {
	argv := runner.PureBuildArgv(runner.PureBuildSpec{
		NixhomePath: "/path/to/nixhome",
		StackName:   "base",
		Arch:        "aarch64-linux",
	})
	want := []string{
		"nix", "build",
		"--extra-experimental-features", "nix-command flakes",
		"path:/path/to/nixhome#packages.aarch64-linux.devcell-base-pure-image",
	}
	for _, w := range want {
		found := false
		for _, a := range argv {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("argv missing %q\ngot: %v", w, argv)
		}
	}
	// CRITICAL: must NOT shell out to docker build
	for _, a := range argv {
		if a == "docker" || strings.HasPrefix(a, "docker") {
			t.Errorf("--pure path leaked into docker build (argv contains %q): %v", a, argv)
		}
	}
}

// After the 2026-05-15 flip (CELL-189), UserImageTag() is unchanged
// (bare devcell-user:<stack>); the pure variant is reached via
// UserImageTagPure() with the -pure suffix.
func TestUserImageTag_BareLocal_PureSuffixOnVariant(t *testing.T) {
	saved := os.Getenv("DEVCELL_USER_IMAGE")
	defer os.Setenv("DEVCELL_USER_IMAGE", saved)
	os.Unsetenv("DEVCELL_USER_IMAGE")
	savedPure := os.Getenv("DEVCELL_USER_IMAGE_PURE")
	defer os.Setenv("DEVCELL_USER_IMAGE_PURE", savedPure)
	os.Unsetenv("DEVCELL_USER_IMAGE_PURE")

	savedStack := runner.Stack
	defer func() { runner.Stack = savedStack }()
	runner.Stack = "ultimate"

	bare := runner.UserImageTag()
	pure := runner.UserImageTagPure()

	if bare != "devcell-user:ultimate" {
		t.Errorf("UserImageTag = %q, want devcell-user:ultimate (bare, unchanged)", bare)
	}
	if pure != "devcell-user:ultimate-pure" {
		t.Errorf("UserImageTagPure = %q, want devcell-user:ultimate-pure", pure)
	}
	if pure != bare+"-pure" {
		t.Errorf("UserImageTagPure must equal UserImageTag + '-pure': pure=%q bare=%q", pure, bare)
	}
}

// PickImageTag — post-flip direction (CELL-183) + CELL-165 vocab:
//   false (default) → pure tag
//   true (--impure, alias --debian) → bare tag
func TestPickImageTag_FlippedDirection(t *testing.T) {
	saved := os.Getenv("DEVCELL_USER_IMAGE")
	defer os.Setenv("DEVCELL_USER_IMAGE", saved)
	os.Unsetenv("DEVCELL_USER_IMAGE")
	savedPure := os.Getenv("DEVCELL_USER_IMAGE_PURE")
	defer os.Setenv("DEVCELL_USER_IMAGE_PURE", savedPure)
	os.Unsetenv("DEVCELL_USER_IMAGE_PURE")

	savedStack := runner.Stack
	defer func() { runner.Stack = savedStack }()
	runner.Stack = "ultimate"

	pure := runner.PickImageTag(false)
	bare := runner.PickImageTag(true)

	if pure != "devcell-user:ultimate-pure" {
		t.Errorf("PickImageTag(false) = %q, want devcell-user:ultimate-pure (default)", pure)
	}
	if bare != "devcell-user:ultimate" {
		t.Errorf("PickImageTag(true) = %q, want devcell-user:ultimate (impure / --impure path)", bare)
	}
	if pure == bare {
		t.Errorf("pure and bare must differ: pure=%s bare=%s", pure, bare)
	}
}

// When --debug is on, nix build invocation must include flags that
// surface per-derivation build logs (-L), double-verbose eval/cache-query
// traces (-vv), and full eval stack traces on errors (--show-trace), so the
// user sees real progress during the long silent eval/substitution phase
// instead of just the spinner.
func TestPureBuildArgv_VerbosePassesDebugFlagsToNix(t *testing.T) {
	verbose := runner.PureBuildArgv(runner.PureBuildSpec{
		NixhomePath: "/x",
		StackName:   "base",
		Arch:        "aarch64-linux",
		Verbose:     true,
	})
	quiet := runner.PureBuildArgv(runner.PureBuildSpec{
		NixhomePath: "/x",
		StackName:   "base",
		Arch:        "aarch64-linux",
		Verbose:     false,
	})
	wantInVerbose := []string{"-L", "-vv", "--show-trace"}
	for _, w := range wantInVerbose {
		if !slices.Contains(verbose, w) {
			t.Errorf("verbose argv missing %q\ngot: %v", w, verbose)
		}
	}
	// quiet mode must not leak any of the debug flags
	for _, w := range wantInVerbose {
		if slices.Contains(quiet, w) {
			t.Errorf("quiet argv leaked debug flag %q\ngot: %v", w, quiet)
		}
	}
	// -v alone (single-v) must NOT appear when we ask for double-verbose;
	// nix accepts both but the intent is the louder one.
	if slices.Contains(verbose, "-v") {
		t.Errorf("verbose argv should use -vv (double), not -v\ngot: %v", verbose)
	}
}

// When running on macOS without a Linux remote builder configured,
// BuildImagePure must fail fast with an actionable error message (mentioning
// the linux-builder remediation) instead of fetching ~3 GB then erroring.
//
// This test is host-aware: on macOS without linux-builder, expect the
// preflight error. On Linux/CI, the preflight passes and we just verify
// the function doesn't bail spuriously.
func TestBuildImagePure_DarwinPreflight(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(tmpDir+"/bin", 0o755)
	// minimal env so spec validation doesn't bail before preflight.
	spec := runner.PureBuildSpec{
		NixhomePath: tmpDir,
		StackName:   "base",
	}
	err := runner.BuildImagePure(context.Background(), spec, "test:tag", false, io.Discard)
	if runtime.GOOS == "darwin" {
		// On macOS, expect either preflight bail (when no linux-builder) or
		// real nix invocation (when configured). Bail must be actionable.
		if err != nil && !strings.Contains(err.Error(), "linux-builder") &&
			!strings.Contains(err.Error(), "pure build") {
			t.Errorf("darwin error must mention linux-builder or pure build: %v", err)
		}
	}
	// On Linux, just ensure it doesn't false-positive bail.
	if runtime.GOOS == "linux" && err != nil &&
		strings.Contains(err.Error(), "macOS") {
		t.Errorf("linux host should not hit darwin preflight: %v", err)
	}
}

// On macOS, the host running `cell` must build a flake package whose
// nix2container *helpers* (copy-to-docker-daemon, layer packers, IFD bash
// scripts) are host-arch (Darwin). If those helpers are aarch64-linux,
// `nix build` for the image fails with "Exec format error" the moment nix
// tries to run them locally for IFD. So the system identifier we feed to the
// flake — the one that selects which `packages.<system>` attrset to use —
// must be the HOST system, not the image target.
//
// The image content stays Linux (Docker Desktop's VM kernel) regardless;
// the flake's mkImagePackagesFor hostSystem targetSystem decouples helpers
// from image content. This test pins the Go-side contract: GOOS+GOARCH →
// nix host system identifier.
func TestNixSystemFor_DetectsHostOSAndArch(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "aarch64-darwin"},
		{"darwin", "amd64", "x86_64-darwin"},
		{"linux", "arm64", "aarch64-linux"},
		{"linux", "amd64", "x86_64-linux"},
	}
	for _, c := range cases {
		got := runner.NixSystemFor(c.goos, c.goarch)
		if got != c.want {
			t.Errorf("NixSystemFor(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// PureBuildArgv must honor an explicit FlakeRef (e.g. a github: URL from
// the resolver's fallback path) and skip prepending "path:". This is the
// seam that makes `cell build --pure` work without a local nixhome dir,
// mirroring the docker path's flake input fallback.
func TestPureBuildArgv_HonorsExplicitFlakeRef(t *testing.T) {
	argv := runner.PureBuildArgv(runner.PureBuildSpec{
		FlakeRef:  "github:DimmKirr/devcell/main?dir=nixhome",
		StackName: "base",
		Arch:      "aarch64-linux",
	})
	want := "github:DimmKirr/devcell/main?dir=nixhome#packages.aarch64-linux.devcell-base-pure-image"
	found := false
	for _, a := range argv {
		if a == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("argv missing %q\ngot: %v", want, argv)
	}
	// Must NOT prepend path: when FlakeRef is explicit
	for _, a := range argv {
		if strings.HasPrefix(a, "path:github:") {
			t.Errorf("FlakeRef leaked into path: prefix: %v", argv)
		}
	}
}

// Back-compat: when FlakeRef is empty, derive from NixhomePath as before.
func TestPureBuildArgv_NixhomePathFallback(t *testing.T) {
	argv := runner.PureBuildArgv(runner.PureBuildSpec{
		NixhomePath: "/abs/nixhome",
		StackName:   "base",
		Arch:        "aarch64-linux",
	})
	want := "path:/abs/nixhome#packages.aarch64-linux.devcell-base-pure-image"
	found := false
	for _, a := range argv {
		if a == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("argv missing %q\ngot: %v", want, argv)
	}
}

func TestPureBuildArgv_StackParam(t *testing.T) {
	// Different stacks must produce different flake outputs.
	a1 := runner.PureBuildArgv(runner.PureBuildSpec{NixhomePath: "/x", StackName: "python", Arch: "x86_64-linux"})
	a2 := runner.PureBuildArgv(runner.PureBuildSpec{NixhomePath: "/x", StackName: "base", Arch: "x86_64-linux"})
	joined1 := strings.Join(a1, " ")
	joined2 := strings.Join(a2, " ")
	if !strings.Contains(joined1, "devcell-python-pure-image") {
		t.Errorf("python stack not in argv: %s", joined1)
	}
	if !strings.Contains(joined2, "devcell-base-pure-image") {
		t.Errorf("base stack not in argv: %s", joined2)
	}
	if strings.Contains(joined1, "devcell-base-pure-image") {
		t.Errorf("python stack argv leaked base output: %s", joined1)
	}
}

func TestVet(t *testing.T) {
	cmd := exec.Command("go", "vet", "./...")
	cmd.Env = append(os.Environ(), "GOMODCACHE=/tmp/gomodcache", "GOPATH=/tmp/gopath")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet failed:\n%s", out)
	}
}

func TestBuildBinarySize(t *testing.T) {
	tmp, err := os.MkdirTemp("", "cell-dist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	binPath := tmp + "/cell"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMODCACHE=/tmp/gomodcache", "GOPATH=/tmp/gopath")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	const maxSize = 50 * 1024 * 1024 // 50 MB
	if info.Size() > maxSize {
		t.Errorf("binary size %d exceeds 50MB limit", info.Size())
	}
}
