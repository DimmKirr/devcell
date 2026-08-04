package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The qemu engine used to be gated to darwin/arm64 at compile time, so the only
// way to exercise a Windows install in a Linux dev container was to bypass the
// CLI and drive internal/vm/qemu directly from a test. That divergence is
// exactly the kind that hides bugs: the argv, spec and provisioning the test
// proved were never the ones `cell build` builds.
//
// The engine is portable — PreflightCheck already accepts linux, FirmwarePath
// already resolves a Linux firmware, and QEMUBinaryPath is a PATH lookup — so
// the gate belongs at runtime (no accelerator, wrong arch), not at compile time.
func TestBuildQemu_RunsOnLinuxNotJustDarwin(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("qemu engine is supported on linux and darwin, not %s", runtime.GOOS)
	}
	bin := buildCellBinary(t)

	out, _ := runCell(t, bin, t.TempDir(), "build", "--engine=qemu", "--dry-run")

	assertNotContains(t, out, "requires macOS on Apple Silicon",
		"the qemu engine must not refuse to run on this platform")
	assertContains(t, out, "Windows VM template",
		"--dry-run must describe the template it would build")
}

// A Linux dev container has no /dev/kvm (Docker Desktop cannot provide it), so
// the engine falls back to TCG. TCG is ~20x slower: the darwin-tuned 45-minute
// SSH deadline expires mid-install, and 4 GB was already shown to invite the
// OOM killer under TCG's translation overhead. The plan must say so.
func TestBuildQemu_DryRunReportsAcceleratorAndTCGBudget(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("TCG budget reporting is only interesting where hardware virt is absent")
	}
	bin := buildCellBinary(t)

	out, _ := runCell(t, bin, t.TempDir(), "build", "--engine=qemu", "--dry-run", "--debug")

	assertContains(t, out, "Accelerator:", "the plan must name the accelerator it resolved")
	if strings.Contains(out, "tcg") {
		assertContains(t, out, "SSH deadline:",
			"a TCG build must state the deadline it allows for the install")
	}
}

// --- helpers ---------------------------------------------------------------

// buildCellBinary compiles the real CLI once per test binary. Tests that assert
// on CLI behaviour must run the CLI: asserting on the functions behind it is
// how the engine drifted from the test in the first place.
func buildCellBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cell")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRootFromTest(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building cell binary: %v\n%s", err, out)
	}
	return bin
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// cmd/ is the main package directory.
	return wd
}

func runCell(t *testing.T, bin, projectDir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func assertContains(t *testing.T, haystack, needle, msg string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s\nwant substring: %q\ngot:\n%s", msg, needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle, msg string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s\nunwanted substring: %q\ngot:\n%s", msg, needle, haystack)
	}
}

// TCG needs 8 GB: QEMU's RSS under TCG runs well past guest RAM (translation
// buffers + block cache), and 4 GB starved the install. Values above 8 risked
// the OOM killer on shared hosts, so 8 is the sweet spot.
func TestTCGBudget_Allocates8GB(t *testing.T) {
	src, err := os.ReadFile("build_qemu_budget.go")
	if err != nil {
		t.Fatalf("reading build_qemu_budget.go: %v", err)
	}
	if !regexp.MustCompile(`emulatedMemoryGB\s*=\s*8\b`).Match(src) {
		t.Error("emulatedMemoryGB must be 8 — TCG needs the headroom for translation buffers")
	}
}

// TCG builds must use cache=unsafe to eliminate sync flushes that are pure
// waste under software emulation (no data-integrity benefit in a build VM
// that gets discarded on failure).
func TestQEMUBuildSpec_SetsDiskCacheModeForTCG(t *testing.T) {
	src, err := os.ReadFile("build_qemu.go")
	if err != nil {
		t.Fatalf("reading build_qemu.go: %v", err)
	}
	if !regexp.MustCompile(`DiskCacheMode:`).Match(src) {
		t.Error("cell build must set Spec.DiskCacheMode so TCG builds use cache=unsafe")
	}
}

// The machine features Windows' hypervisor needs must be set by the CLI, not
// only by the dev-env test: `cell build --engine=qemu` is what users run, and
// a guest built without them cannot start WSL2 no matter what the test proves.
func TestQEMUBuildSpec_RequestsNestedVirt(t *testing.T) {
	src, err := os.ReadFile("build_qemu.go")
	if err != nil {
		t.Fatalf("reading build_qemu.go: %v", err)
	}
	// Match the field, not gofmt's alignment.
	if !regexp.MustCompile(`NestedVirt:\s+true`).Match(src) {
		t.Error("cell build must set Spec.NestedVirt so the guest can host a hypervisor")
	}
}
