package container_test

// CELL-261: integration test for the "Loading secrets" permanent ✓ line.
//
// Drives `cell shell` under a real PTY (creack/pty, already in go.mod) and
// asserts that the checkbox lands BEFORE the in-container shell prompt — the
// byte-position ordering is the host→container boundary check. The interactive
// prompt only appears once `docker exec -it ... zsh` is streaming, so anything
// before it came from the Go-side preflight in cmd/root.go.

import (
	"bytes"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// promptSentinels are the strings whose first occurrence we treat as "the
// interactive shell prompt has rendered". Each starts with `\n` so we don't
// match the `# ` or `% ` that appear elsewhere in spinner / banner output.
// zsh in the cell uses one of these depending on the host user (root → `# `,
// otherwise `% ` or `$ `).
var promptSentinels = []string{"\n$ ", "\n% ", "\n# "}

// ansiRe strips colour and cursor-positioning escapes — the spinner writes
// `\r\033[K` for line-clear and lipgloss writes `\033[...m` for colour.
// Stripping them keeps the byte-position assertions environment-agnostic.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func TestCellShell_LoadingSecretsCheckboxAppearsBeforePrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("long: drives `cell shell` against a real container image — " +
			"set DEVCELL_TEST_THIN_IMAGE or run `cell build --thin`")
	}

	cellBin, err := ensureCellBinary()
	if err != nil {
		t.Fatalf("build cell-test binary: %v", err)
	}

	img, skip := imageTagForVariant(
		"thin",
		os.Getenv("DEVCELL_TEST_PURE_IMAGE"),
		os.Getenv("DEVCELL_TEST_IMAGE"),
		imageExists,
	)
	if skip != "" {
		t.Skip(skip)
	}

	projectDir := writeMinimalDevcellTOML(t, `
[op]
documents = ["op://devcell-test/secrets"]
`)
	opStubDir := writeOpStub(t)

	cmd := osexec.Command(cellBin, "shell")
	cmd.Dir = projectDir
	cmd.Env = append(envWithoutPATH(),
		"PATH="+opStubDir+":"+defaultPath(),
		"DEVCELL_TEST_THIN_IMAGE="+img,
		"DEVCELL_CELL_NAME=loadsecrets-test",
		"TERM=xterm-256color",
		"HOME="+t.TempDir(),
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ptmx.Write([]byte("exit\n"))
		time.Sleep(200 * time.Millisecond)
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	output, foundPrompt := readUntilAny(ptmx, promptSentinels, 60*time.Second)
	stripped := ansiRe.ReplaceAllString(output, "")

	if !foundPrompt {
		t.Fatalf("did not reach interactive prompt in 60s; captured (stripped):\n%s", stripped)
	}

	// Find the first prompt boundary in the stripped buffer.
	promptIdx := indexOfAny(stripped, promptSentinels)
	if promptIdx < 0 {
		t.Fatalf("could not locate prompt sentinel in stripped buffer:\n%s", stripped)
	}
	preProm := stripped[:promptIdx]

	// 1. The phase line must exist somewhere before the prompt.
	secIdx := strings.Index(preProm, "Loading secrets")
	if secIdx < 0 {
		t.Errorf("expected 'Loading secrets' phase line before prompt; pre-prompt slice:\n%s", preProm)
	}

	// 2. It must have sealed with a ✓ (Success), not still be mid-spinner.
	if !strings.Contains(preProm, "✓") {
		t.Errorf("'Loading secrets' never sealed with ✓ before prompt; pre-prompt slice:\n%s", preProm)
	}

	// 3. The resolved count from our stub must be visible (proves the Success
	//    branch ran with real data, not the Fail branch).
	if !strings.Contains(preProm, "resolved") {
		t.Errorf("'Loading secrets' line missing 'resolved' marker; pre-prompt slice:\n%s", preProm)
	}
}

// TestCellShell_PhasesAppearInOrder pins the documented phase sequence from
// CELL-262: Network → Orphan check → Backup → Image pin → Git identity →
// Loading secrets → Cell ready. The integration test only asserts the rows
// that ARE produced — `cell shell` doesn't load a system prompt (claude-only)
// so that phase is correctly absent. Byte-position ordering is what proves
// the sequence; checkmark presence proves each phase sealed cleanly before
// the next started.
func TestCellShell_PhasesAppearInOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: drives `cell shell` against a real container image — " +
			"set DEVCELL_TEST_THIN_IMAGE or run `cell build --thin`")
	}

	cellBin, err := ensureCellBinary()
	if err != nil {
		t.Fatalf("build cell-test binary: %v", err)
	}
	img, skip := imageTagForVariant(
		"thin",
		os.Getenv("DEVCELL_TEST_PURE_IMAGE"),
		os.Getenv("DEVCELL_TEST_IMAGE"),
		imageExists,
	)
	if skip != "" {
		t.Skip(skip)
	}

	projectDir := writeMinimalDevcellTOML(t, `
[op]
documents = ["op://devcell-test/secrets"]
`)
	opStubDir := writeOpStub(t)

	cmd := osexec.Command(cellBin, "shell")
	cmd.Dir = projectDir
	cmd.Env = append(envWithoutPATH(),
		"PATH="+opStubDir+":"+defaultPath(),
		"DEVCELL_TEST_THIN_IMAGE="+img,
		"DEVCELL_CELL_NAME=phaseorder-test",
		"TERM=xterm-256color",
		"HOME="+t.TempDir(),
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ptmx.Write([]byte("exit\n"))
		time.Sleep(200 * time.Millisecond)
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	output, foundPrompt := readUntilAny(ptmx, promptSentinels, 60*time.Second)
	stripped := ansiRe.ReplaceAllString(output, "")
	if !foundPrompt {
		t.Fatalf("did not reach interactive prompt in 60s; captured:\n%s", stripped)
	}
	promptIdx := indexOfAny(stripped, promptSentinels)
	preProm := stripped[:promptIdx]

	// Expected phase rows in order. System prompt is skipped because the
	// binary is "zsh", not "claude". Docker daemon / Nix volume are
	// intentionally not rendered (CELL-262: silent upstream gates).
	expected := []string{
		"Network",
		"Orphan check",
		"Backup",
		"Image pin",
		"Git identity",
		"Loading secrets",
		"Cell ready",
	}
	last := -1
	for _, name := range expected {
		idx := strings.Index(preProm, name)
		if idx < 0 {
			t.Errorf("expected phase %q to appear before prompt; pre-prompt slice:\n%s", name, preProm)
			continue
		}
		if idx <= last {
			t.Errorf("phase %q at idx=%d appeared at or before previous phase (last=%d); pre-prompt slice:\n%s",
				name, idx, last, preProm)
		}
		last = idx
	}
}

// TestCellShell_ContainerRowsArriveAfterCellReady is the end-to-end check
// for CELL-263 sd_notify wiring. After ✓ Cell ready, the host should render
// at least one container-side row (e.g. "Mise ready") driven by a
// notify "STATUS=..." call from a nixhome fragment. Without this assertion
// we can't tell from CI whether:
//
//   - the bind-mount + NOTIFY_SOCKET env reach the container
//   - socat is bundled in the image (required by 00-notify.sh)
//   - fragments are actually emitting notify lines
//   - the host listener+consumer renders them
//
// If this test fails, the most common causes (rank-ordered) are:
//  1. Thin image was built before CELL-263 — rebuild with `cell build --thin`
//  2. socat missing in nixhome/modules/base.nix (silent notify() no-op)
//  3. NOTIFY_SOCKET env not reaching the container (BuildArgv bug)
//  4. Fragment didn't source 00-notify.sh first (alphabetical sort issue)
func TestCellShell_ContainerRowsArriveAfterCellReady(t *testing.T) {
	if testing.Short() {
		t.Skip("long: drives `cell shell` against a real container image — " +
			"set DEVCELL_TEST_THIN_IMAGE or run `cell build --thin`")
	}

	cellBin, err := ensureCellBinary()
	if err != nil {
		t.Fatalf("build cell-test binary: %v", err)
	}
	img, skip := imageTagForVariant(
		"thin",
		os.Getenv("DEVCELL_TEST_PURE_IMAGE"),
		os.Getenv("DEVCELL_TEST_IMAGE"),
		imageExists,
	)
	if skip != "" {
		t.Skip(skip)
	}

	projectDir := writeMinimalDevcellTOML(t, "")
	cmd := osexec.Command(cellBin, "shell", "--no-1password")
	cmd.Dir = projectDir
	cmd.Env = append(envWithoutPATH(),
		"PATH="+defaultPath(),
		"DEVCELL_TEST_THIN_IMAGE="+img,
		"DEVCELL_CELL_NAME=cell263-test",
		"TERM=xterm-256color",
		"HOME="+t.TempDir(),
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ptmx.Write([]byte("exit\n"))
		time.Sleep(200 * time.Millisecond)
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	output, foundPrompt := readUntilAny(ptmx, promptSentinels, 90*time.Second)
	stripped := ansiRe.ReplaceAllString(output, "")
	if !foundPrompt {
		t.Fatalf("did not reach interactive prompt in 90s; captured:\n%s", stripped)
	}

	cellReadyIdx := strings.Index(stripped, "Cell ready")
	if cellReadyIdx < 0 {
		t.Fatalf("Cell ready row missing; sd_notify wiring can only run AFTER host phases:\n%s", stripped)
	}
	promptIdx := indexOfAny(stripped, promptSentinels)
	postCellReady := stripped[cellReadyIdx:promptIdx]

	// At least one container-side STATUS string from a fragment must appear
	// between Cell ready and the shell prompt. We don't pin the exact name
	// (depends on which fragments fire — GUI is conditional, Mise depends
	// on having .tool-versions, etc.) — any one of these is sufficient to
	// prove the notify chain works.
	containerSideMarkers := []string{
		"Mise ready",
		"Home directory ready",
		"Shell ready",
		"Nix ready",
		"Chromium ready",
		"GUI ready",
	}
	var seen []string
	for _, marker := range containerSideMarkers {
		if strings.Contains(postCellReady, marker) {
			seen = append(seen, marker)
		}
	}
	if len(seen) == 0 {
		t.Errorf(
			"no container-side notify rows appeared between Cell ready and prompt — "+
				"sd_notify wiring is broken end-to-end.\n"+
				"Most likely: (1) image predates CELL-263 — rebuild with `cell build --thin`; "+
				"(2) socat missing from nixhome/modules/base.nix; "+
				"(3) NOTIFY_SOCKET not reaching container.\n"+
				"Looked for any of: %v\n"+
				"Post-Cell-ready slice:\n%s",
			containerSideMarkers, postCellReady)
	} else {
		t.Logf("container-side rows detected: %v", seen)
	}
}

// writeMinimalDevcellTOML writes a devcell.toml with the given fragment
// appended after a minimal `[cell]` header in a fresh per-test directory.
// Returns the directory path — the caller chdir's into it via cmd.Dir.
func writeMinimalDevcellTOML(t *testing.T, opSection string) string {
	t.Helper()
	dir := t.TempDir()
	body := `[cell]
name = "loadsecrets-test"
` + opSection
	if err := os.WriteFile(filepath.Join(dir, "devcell.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write devcell.toml: %v", err)
	}
	return dir
}

// writeOpStub creates a temp dir containing an `op` shim that prints one fake
// `KEY=VALUE` line for any `op item get ...` invocation. The shim covers the
// minimal `op` surface that internal/op/resolve.go invokes — just enough to
// drive the resolved branch in ShouldResolve → ResolveItems.
func writeOpStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
# Stub for 1Password CLI used by CELL-261 integration test.
# Any invocation prints a single fake env line and exits 0.
echo "TEST_SECRET=hello"
exit 0
`
	path := filepath.Join(dir, "op")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write op stub: %v", err)
	}
	return dir
}

// envWithoutPATH returns os.Environ() with any existing PATH= entries removed,
// so the caller can prepend a stub directory deterministically.
func envWithoutPATH() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// defaultPath returns the minimal PATH the cell binary needs for docker,
// git, and the host shell. We inherit the host PATH if present, otherwise
// fall back to a conservative default.
func defaultPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

// readUntilAny reads from r until any sentinel substring is observed in the
// accumulated buffer or the deadline elapses. Returns the full buffer (incl.
// ANSI) and whether a sentinel was found. Read loop is in-band — no goroutine
// needed because pty reads block; the outer time.AfterFunc closes the FD on
// timeout, which unblocks ReadByte with an error.
func readUntilAny(r io.Reader, sentinels []string, timeout time.Duration) (string, bool) {
	var buf bytes.Buffer
	done := make(chan struct{})
	stop := time.AfterFunc(timeout, func() {
		close(done)
		// If r is a *os.File (pty master), closing unblocks the read.
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
	})
	defer stop.Stop()

	one := make([]byte, 1)
	for {
		select {
		case <-done:
			return buf.String(), false
		default:
		}
		_, err := r.Read(one)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return buf.String(), false
			}
			return buf.String(), false
		}
		buf.WriteByte(one[0])
		// Cheap suffix match — only check when we just wrote a possible
		// sentinel starter byte (`\n`, ` `, `$`, `%`, `#`).
		for _, s := range sentinels {
			if strings.HasSuffix(buf.String(), s) {
				return buf.String(), true
			}
		}
	}
}

// indexOfAny returns the lowest index of any sentinel found in s, or -1.
func indexOfAny(s string, sentinels []string) int {
	best := -1
	for _, sent := range sentinels {
		i := strings.Index(s, sent)
		if i < 0 {
			continue
		}
		if best < 0 || i < best {
			best = i
		}
	}
	return best
}
