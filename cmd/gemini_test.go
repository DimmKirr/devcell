package main_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestGemini_DefaultFlags verifies "cell gemini --dry-run" produces a docker
// run argv whose tail invokes the gemini binary with --yolo (auto-approve).
// Mirrors TestCodex_NoOllama_NoOSSFlags / TestClaude_NoOllama_NoEnv shape.
func TestGemini_DefaultFlags(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "gemini", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gemini --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	// The argv ends with `<image> gemini --yolo`. We don't pin the image
	// tag (varies by stack); we just assert the binary + default flag
	// appear contiguously near the end.
	if !strings.Contains(argv, " gemini --yolo") {
		t.Errorf("expected ' gemini --yolo' in dry-run argv, got:\n%s", argv)
	}
}

// TestGemini_ForwardsUserArgs verifies args after the subcommand reach the
// gemini binary unchanged (DisableFlagParsing semantics, same as codex).
func TestGemini_ForwardsUserArgs(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "gemini",
		"--model=gemini-2.5-pro", "-p", "hi", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gemini --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "--model=gemini-2.5-pro") {
		t.Errorf("expected --model=gemini-2.5-pro forwarded:\n%s", argv)
	}
	// `-p hi` should appear after the binary, in order.
	if !strings.Contains(argv, "-p hi") {
		t.Errorf("expected '-p hi' forwarded:\n%s", argv)
	}
}

// TestGemini_StripCellFlags verifies devcell-only flags (--debug, --build,
// --dry-run, --ollama) do NOT leak into the gemini binary's argv.
func TestGemini_StripCellFlags(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "gemini", "--debug", "--ollama", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gemini --debug --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	// Find what comes after `gemini ` in the argv — that's the binary's args.
	argv := string(out)
	idx := strings.LastIndex(argv, " gemini ")
	if idx < 0 {
		t.Fatalf("could not locate ' gemini ' in argv:\n%s", argv)
	}
	tail := argv[idx+len(" gemini "):]
	for _, leaked := range []string{"--debug", "--ollama", "--dry-run", "--build"} {
		for _, field := range strings.Fields(tail) {
			if field == leaked {
				t.Errorf("%s should be stripped, but found in gemini argv tail:\n%s", leaked, tail)
			}
		}
	}
}

// TestGemini_RegisteredOnRoot verifies `cell --help` lists gemini alongside
// the other agent subcommands so users can discover it.
func TestGemini_RegisteredOnRoot(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("cell --help failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "gemini") {
		t.Errorf("expected 'gemini' subcommand in --help output:\n%s", out)
	}
}

// TestGemini_BinaryReportsVersion runs the actual gemini-cli binary (not the
// cell wrapper) and asserts it prints a semver string. Catches:
//   - pkgsEdge.gemini-cli vanishes from nixpkgs
//   - Derivation FTBFS (binary doesn't exist after build)
//   - Runtime/nodejs missing from the closure
//   - --version reporting breaks
//
// Skipped when `gemini` is not on $PATH. Operators get coverage by running
// inside a built devcell image (gemini installed via nix) or by wrapping
// the test with `nix shell nixpkgs#gemini-cli --command go test ...`.
func TestGemini_BinaryReportsVersion(t *testing.T) {
	bin, err := exec.LookPath("gemini")
	if err != nil {
		t.Skip("gemini not on $PATH — run inside built devcell image or via `nix shell nixpkgs#gemini-cli --command go test ...`")
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gemini --version failed: %v\noutput: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(got) {
		t.Errorf("expected semver from --version, got: %q", got)
	}
}

// TestGemini_BinaryAcceptsYoloFlag verifies `--yolo` is in gemini's --help —
// i.e. the auto-approve flag we hardcode in cmd/gemini.go is still
// recognized by the version pinned in pkgsEdge. Without this, `cell gemini`
// would silently pass an unrecognized flag and gemini-cli would error or
// (worse) ignore it and prompt for approval interactively.
//
// Same skip behavior as TestGemini_BinaryReportsVersion.
func TestGemini_BinaryAcceptsYoloFlag(t *testing.T) {
	bin, err := exec.LookPath("gemini")
	if err != nil {
		t.Skip("gemini not on $PATH")
	}
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("gemini --help failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "--yolo") {
		t.Errorf("--yolo flag missing from gemini --help — cell gemini hardcodes it as the auto-approve flag, this will break:\n%s", out)
	}
}
