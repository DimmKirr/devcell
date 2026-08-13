package qemu

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// errTimedOut marks a CLI invocation the test killed rather than one the CLI
// itself failed — the distinction matters when reading the failure.
var errTimedOut = errors.New("cell command exceeded the test's own timeout")

// TestCellBuildWindows_QEMU drives the real CLI end to end:
//
//	setup     — `cell init --engine=qemu` scaffolds config and SSH keys
//	test      — `cell build --engine=qemu --debug` installs Windows
//	winddown  — destroy the VM (deliberately disabled, see below)
//
// Subtests by accelerator mirror TestWinPECDVisibility:
//
//	go test -run TestCellBuildWindows_QEMU/tcg  -timeout 8h -v ./internal/vm/qemu/
//	go test -run TestCellBuildWindows_QEMU/hvf  -timeout 8h -v ./internal/vm/qemu/
//
// Long test — a TCG install ran 2h42m on this host. Run explicitly:
//
//	DEVCELL_TEST_INSTALL=1 go test -run TestCellBuildWindows_QEMU -timeout 8h -v ./internal/vm/qemu/
func TestCellBuildWindows_QEMU(t *testing.T) {
	if testing.Short() {
		t.Skip("long: full unattended Windows install driven through the CLI")
	}
	if os.Getenv("DEVCELL_TEST_INSTALL") == "" {
		t.Skip("set DEVCELL_TEST_INSTALL=1 to run the multi-hour unattended install")
	}

	requireQEMUBin(t)
	isoPath := requireWindowsISO(t)

	// A HOME of the test's own — `cell init` writes keys under $HOME/.devcell
	// and `cell build` puts the template under $HOME/.devcell/windows/<stack>,
	// so a real HOME would mean competing with the user's own cells — but a
	// *stable* one, not t.TempDir(). A temp HOME is deleted when the test ends,
	// which silently threw away a 16GB template and made every run pay the full
	// 2h47m install again, winddown or no winddown.
	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	require.NoError(t, os.MkdirAll(home, 0o755))
	cellBin := buildCellCLI(t)

	seedMediaCache(t, home, requireVirtioISO(t), isoPath)

	// `cell init` is accel-independent (scaffolds SSH keys + config).
	// Run once at the top level so subtests share the same keys.
	initProjectDir := t.TempDir()
	initResultsDir := testResultsDir(t)
	initEnv := append(os.Environ(),
		"HOME="+home,
		"DEVCELL_CELL_NAME=main",
		"DEVCELL_QEMU_WINDOWS_ISO="+isoPath,
	)
	setupOut := runCellCommand(t, cellBin, initProjectDir, initResultsDir, initEnv, 90*time.Minute,
		"init", "--engine=qemu")
	writeArtifact(t, initResultsDir, "cell-init.log", setupOut)
	require.FileExists(t, filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519"),
		"`cell init --engine=qemu` must leave an SSH key for the build to bake into the guest")

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}

			projectDir := t.TempDir()
			resultsDir := testResultsDir(t)

			qemuAccel := "tcg,thread=multi"
			if accel == "hvf" {
				qemuAccel = "hvf"
			}

			env := append(os.Environ(),
				"HOME="+home,
				"DEVCELL_CELL_NAME=main",
				"DEVCELL_QEMU_WINDOWS_ISO="+isoPath,
				"DEVCELL_QEMU_ACCEL="+qemuAccel,
			)

			templateDir := TemplateDir(home, "base", nil)
			answerImg := filepath.Join(templateDir, "autounattend.img")
			marker := ProvisionedMarker(home, "base", nil)
			templateDisk := filepath.Join(templateDir, ImageName("base", nil))

			if _, err := os.Stat(marker); err == nil && os.Getenv("DEVCELL_TEST_REBUILD") == "" {
				t.Logf("template already provisioned (%s) — set DEVCELL_TEST_REBUILD=1 to reinstall", marker)
				info, statErr := os.Stat(templateDisk)
				require.NoError(t, statErr, "provisioned marker without a template disk")
				require.Greater(t, info.Size(), int64(1<<30),
					"a provisioned template must hold an installed Windows, got %d bytes", info.Size())
				return
			}

			buildArgs := []string{"build", "--engine=qemu", "--debug"}
			if _, err := os.Stat(templateDisk); err == nil {
				t.Log("template from an earlier run — rebuilding with --force")
				buildArgs = append(buildArgs, "--force")
			}

			buildDone := make(chan buildResult, 1)
			go func() {
				out := runCellCommandNoFail(t, cellBin, projectDir, resultsDir, env, 8*time.Hour, buildArgs...)
				buildDone <- out
			}()

			qmpSock := QMPSocketPath(Spec{VMName: "devcell-qemu-build", QMPSocketDir: templateDir})
			stopWatch := make(chan struct{})
			watchDone := make(chan struct{})
			go func() {
				defer close(watchDone)
				watchGuest(t, qmpSock, resultsDir, stopWatch)
			}()

			result := <-buildDone
			close(stopWatch)
			<-watchDone

			writeArtifact(t, resultsDir, "cell-build.log", result.output)

			if ports := extractLine(result.output, "Ports:"); ports != "" {
				writeArtifact(t, resultsDir, "ports.txt", ports+"\n")
				t.Log(ports)
			} else {
				t.Error("the build must report the ports it allocated")
			}

			for _, name := range []string{"serial.log", "guest-progress.log"} {
				src := filepath.Join(projectDir, ".context", "debug", name)
				data, err := os.ReadFile(src)
				if err != nil {
					t.Logf("%s: not captured (%v)", name, err)
					continue
				}
				writeArtifact(t, resultsDir, name, string(data))
				t.Logf("%s: %d bytes saved", name, len(data))
			}

			for _, l := range CollectGuestLogs(answerImg) {
				if l.Err != nil {
					t.Logf("%s: %v", l.Name, l.Err)
					continue
				}
				writeArtifact(t, resultsDir, l.Name, string(l.Content))
				t.Logf("%s: %d bytes saved", l.Name, len(l.Content))
			}
			if _, err := os.Stat(qemuImgTool(t)); err == nil {
				collectPantherLogs(t, qemuImgToolBase(t), filepath.Join(templateDir, ImageName("base", nil)), resultsDir)
			}

			if transcript, err := readGuestLog(answerImg, BootstrapLogName); err == nil {
				steps := ParseBootstrapSteps(transcript)
				t.Logf("bootstrap: %d ok, %d failed, %d unfinished", len(steps.OK), len(steps.Failed), len(steps.Unfinished))
				require.Empty(t, steps.Failed, "bootstrap steps failed in the guest")
				require.Empty(t, steps.Unfinished, "bootstrap steps started but never reported — the guest died mid-step")
				require.True(t, steps.SSHReady(),
					"bootstrap never got sshd installed and started; ok steps: %v", steps.OK)
			} else {
				t.Errorf("no bootstrap transcript on the answer volume: %v", err)
			}

			require.NoError(t, result.err,
				"`cell build --engine=qemu --debug` failed — guest logs above and artifacts in %s", resultsDir)
			require.FileExists(t, ProvisionedMarker(home, "base", nil),
				"a successful build must stamp the provisioned marker")
		})
	}
}

// --- helpers ---------------------------------------------------------------

type buildResult struct {
	output string
	err    error
}

// buildCellCLI compiles the CLI under test. Building it rather than assuming an
// installed `cell` guarantees the binary matches the working tree.
func buildCellCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cell")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building the cell CLI: %s", out)
	return bin
}

func runCellCommand(t *testing.T, bin, dir, resultsDir string, env []string, timeout time.Duration, args ...string) string {
	t.Helper()
	r := runCellCommandNoFail(t, bin, dir, resultsDir, env, timeout, args...)
	require.NoError(t, r.err, "cell %s failed:\n%s", strings.Join(args, " "), r.output)
	return r.output
}

// teeToFile returns a writer that mirrors everything into path and into mem.
//
// The file is what makes a running build observable: `tail -f` it while the
// install grinds, instead of waiting hours for the process to exit and only
// then learning why it failed.
func teeToFile(path string, mem io.Writer) (io.Writer, func() error, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening live log %s: %w", path, err)
	}
	return io.MultiWriter(f, mem), f.Close, nil
}

func runCellCommandNoFail(t *testing.T, bin, dir, resultsDir string, env []string, timeout time.Duration, args ...string) buildResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env

	// Stream to disk as it happens. Buffering with CombinedOutput meant a
	// multi-hour build revealed nothing until it exited — the single biggest
	// diagnostic gap of 2026-07-31.
	var mem strings.Builder
	live := liveLogPath(resultsDir, args)
	w, closeLog, err := teeToFile(live, io.MultiWriter(&mem, os.Stdout))
	if err != nil {
		t.Logf("live log unavailable (%v) — falling back to buffered output", err)
		w, closeLog = io.MultiWriter(&mem, os.Stdout), func() error { return nil }
	} else {
		t.Logf("live log: tail -f %s", live)
	}
	cmd.Stdout, cmd.Stderr = w, w

	done := make(chan buildResult, 1)
	go func() {
		runErr := cmd.Run()
		_ = closeLog()
		done <- buildResult{output: mem.String(), err: runErr}
	}()
	select {
	case r := <-done:
		return r
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Keep whatever was streamed: a killed command's output is exactly
		// what explains why it had to be killed.
		return buildResult{output: mem.String(), err: errTimedOut}
	}
}

// watchGuest polls the CLI's QMP socket for screenshots and block-io stats.
// The socket only exists once the CLI has launched QEMU, so a missing socket
// early on is normal rather than a failure.
func watchGuest(t *testing.T, qmpSock, resultsDir string, stop <-chan struct{}) {
	const pollInterval = 60 * time.Second
	ppmPath := filepath.Join(t.TempDir(), "screen.ppm")
	prevStats := map[string]BlockDeviceStats{}
	attempt := 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(pollInterval):
		}
		if _, err := os.Stat(qmpSock); err != nil {
			continue
		}
		attempt++
		logInstallProgress(t, attempt, qmpSock, ppmPath, resultsDir, &prevStats)
	}
}

// seedMediaCache links already-downloaded media into a temp HOME's cache, so a
// test HOME does not mean a fresh download.
func seedMediaCache(t *testing.T, home, virtioISO, windowsISO string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(CacheDir(home), 0o755))
	seedCachedFile(t, virtioISO, VirtioISOPath(home))
	seedCachedFile(t, windowsISO, WindowsISOPath(home, "en-us"))
}

// seedCachedFile links src to dest and writes the .done marker beside it.
//
// The marker is not optional. The downloader treats a bare file as a partial
// download and re-fetches it — and because dest is a hard link, that download
// writes through to the shared inode and truncates the host's real cached ISO.
// That happened: a 789MB virtio-win.iso came back as a 300MB stub. The marker
// makes it a cache hit, so nothing ever opens the file for writing.
//
// The corollary is that no `cell` invocation here may pass a flag that clears
// the marker — `cell init --force` maps force onto noCache and would re-download
// straight through the link.
func seedCachedFile(t *testing.T, src, dest string) {
	t.Helper()
	needsLink := true
	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			// Broken symlink (stale nix store path) — replace it.
			// Stat follows the symlink; Lstat doesn't. A broken symlink
			// makes Stat fail while the inode still blocks Symlink.
			if _, statErr := os.Stat(dest); statErr != nil {
				os.Remove(dest)
			} else {
				needsLink = false
			}
		} else {
			needsLink = false
		}
	}
	if needsLink {
		if linkErr := os.Link(src, dest); linkErr != nil {
			require.NoError(t, os.Symlink(src, dest))
		}
	}
	require.NoError(t, os.WriteFile(dest+".done", nil, 0o644))
}

func writeArtifact(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Logf("saving %s: %v", name, err)
	}
}

func qemuImgToolBase(t *testing.T) string { return requireQEMUBin(t) }
func qemuImgTool(t *testing.T) string     { return requireQEMUBin(t) + "-img" }

// extractLine returns the first line containing marker, trimmed. Used to lift
// facts the CLI reports (ports, accelerator) out of its output so they can be
// stored beside the run they belong to.
func extractLine(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// readGuestLog pulls one log off the answer volume by name.
func readGuestLog(answerImg, name string) (string, error) {
	for _, l := range CollectGuestLogs(answerImg) {
		if l.Name == name {
			if l.Err != nil {
				return "", l.Err
			}
			return string(l.Content), nil
		}
	}
	return "", errNoSuchGuestLog
}

// A three-hour build that reveals nothing until it exits is how a failure went
// undiagnosed for most of 2026-07-31: the CLI's output was buffered by
// CombinedOutput, the guest's FAT transcript had not flushed since 07:29, and
// the only live view was a screenshot. The log has to be on disk while the
// build runs, not after it.
func TestTeeWriter_MakesOutputReadableWhileTheCommandRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.log")
	var buf strings.Builder

	w, closeFn, err := teeToFile(path, &buf)
	require.NoError(t, err)

	_, err = w.Write([]byte("first line\n"))
	require.NoError(t, err)

	// The point of the exercise: readable now, before the command ends.
	onDisk, readErr := os.ReadFile(path)
	require.NoError(t, readErr, "the log must exist while the command is still running")
	require.Equal(t, "first line\n", string(onDisk))

	_, err = w.Write([]byte("second line\n"))
	require.NoError(t, err)
	require.NoError(t, closeFn())

	// And the in-memory copy still holds everything, for assertions.
	require.Equal(t, "first line\nsecond line\n", buf.String())
}

// A path that cannot be created must not silently disable the live log.
func TestTeeWriter_ReportsAnUnusablePath(t *testing.T) {
	_, _, err := teeToFile(filepath.Join(t.TempDir(), "nope", "live.log"), &strings.Builder{})
	require.Error(t, err, "an unwritable log path is a setup error, not something to swallow")
}

// liveLogPath places the live log inside the run's own results directory.
//
// It takes resultsDir rather than calling testResultsDir: that helper mints a
// fresh timestamped directory on every call, so calling it again from here
// scattered one run's artifacts across two directories a second apart. Named
// after the subcommand so init and build do not overwrite each other.
func liveLogPath(resultsDir string, args []string) string {
	name := "cell.live.log"
	if len(args) > 0 {
		name = "cell-" + args[0] + ".live.log"
	}
	return filepath.Join(resultsDir, name)
}

// testResultsDir mints a fresh timestamped directory on every call, so calling
// it twice in one test scatters that run's artifacts across two directories —
// which is exactly what happened when the live log was first wired in: the
// build's own artifacts landed in one dir and its live logs in another, a
// second apart. A run's artifacts must live together or they cannot be read
// together.
func TestLiveLogPath_StaysInTheRunsOwnResultsDir(t *testing.T) {
	results := t.TempDir()

	got := liveLogPath(results, []string{"build", "--engine=qemu"})

	require.Equal(t, filepath.Join(results, "cell-build.live.log"), got)
}

// init and build must not overwrite each other's live log.
func TestLiveLogPath_NamesTheSubcommand(t *testing.T) {
	results := t.TempDir()

	require.NotEqual(t,
		liveLogPath(results, []string{"init"}),
		liveLogPath(results, []string{"build"}))
	require.Equal(t, filepath.Join(results, "cell.live.log"), liveLogPath(results, nil))
}
