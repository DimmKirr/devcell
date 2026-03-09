package container_test

import (
	"bytes"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/creack/pty"
)

// TestBaseImage validates base image capabilities via direct docker run.
// CI runs this with DEVCELL_TEST_IMAGE pointing to the base image.
func TestBaseImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := image()

	t.Run("bash_echo", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--entrypoint", "bash",
			img,
			"-c", "echo 123",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run bash echo: %v\noutput: %s", err, out)
		}
		if !strings.Contains(string(out), "123") {
			t.Errorf("expected output to contain '123', got: %s", out)
		}
		t.Logf("PASS: %s", strings.TrimSpace(string(out)))
	})

	t.Run("nix_version", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--entrypoint", "bash",
			img,
			"-lc", "nix --version",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run nix --version: %v\noutput: %s", err, out)
		}
		if !strings.Contains(strings.ToLower(string(out)), "nix") {
			t.Errorf("expected output to contain 'nix', got: %s", out)
		}
		t.Logf("PASS: %s", strings.TrimSpace(string(out)))
	})

	t.Run("home_manager", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--entrypoint", "bash",
			img,
			"-lc", "home-manager --version",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run home-manager --version: %v\noutput: %s", err, out)
		}
		if !strings.Contains(string(out), ".") {
			t.Errorf("expected home-manager version with '.', got: %s", out)
		}
		t.Logf("PASS: home-manager %s", strings.TrimSpace(string(out)))
	})

	t.Run("nix_profile_activated", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--entrypoint", "bash",
			img,
			"-lc", "readlink -f /opt/devcell/.nix-profile",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("readlink nix-profile: %v\noutput: %s", err, out)
		}
		if !strings.Contains(string(out), "/nix/store/") {
			t.Errorf("expected nix-profile to point into /nix/store/, got: %s", out)
		}
		t.Logf("PASS: nix-profile → %s", strings.TrimSpace(string(out)))
	})
}

// TestCellShell validates the cell shell command end-to-end via PTY.
// CI runs this with DEVCELL_TEST_IMAGE pointing to the ultimate image.
func TestCellShell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// Build cell binary.
	cellBin := filepath.Join(t.TempDir(), "cell")
	build := osexec.Command("go", "build", "-o", cellBin, "./cmd")
	build.Dir = filepath.Join("..")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build cell: %v", err)
	}

	// Scaffold config directory (cell shell needs devcell.toml).
	// Use manual cleanup via docker because the container creates
	// root-owned files (e.g. xrdp/cert.pem) that t.TempDir() can't remove.
	configDir, err := os.MkdirTemp("", "celltest-config-*")
	if err != nil {
		t.Fatalf("mkdtemp config: %v", err)
	}
	t.Cleanup(func() {
		osexec.Command("docker", "run", "--rm",
			"-v", configDir+":"+configDir,
			"alpine", "rm", "-rf", configDir,
		).Run()
		os.RemoveAll(configDir)
	})
	devcellConfigDir := filepath.Join(configDir, "devcell")
	if err := scaffold.Scaffold(devcellConfigDir, ""); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	projectDir := t.TempDir()
	userImage := image() // pre-built image from DEVCELL_TEST_IMAGE

	// cellShellHome creates a manually-managed HOME directory with the
	// subdirectories that BuildArgv bind-mounts into the container.
	// cell shell bind-mounts CellHome into the container, and the container
	// creates files owned by its internal user. t.TempDir() cleanup can't
	// remove those files (permission denied), so we clean up via docker.
	cellShellHome := func(t *testing.T) string {
		t.Helper()
		home, err := os.MkdirTemp("", "celltest-home-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		// Create directories that BuildArgv mounts from $HOME.
		for _, sub := range []string{".claude/commands", ".claude/agents", ".claude/skills"} {
			if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
		}
		t.Cleanup(func() {
			osexec.Command("docker", "run", "--rm",
				"-v", home+":"+home,
				"alpine", "rm", "-rf", home,
			).Run()
			os.RemoveAll(home)
		})
		return home
	}

	t.Run("bash_echo", func(t *testing.T) {
		home := cellShellHome(t)
		cmd := osexec.Command(cellBin, "shell", "--", "bash", "-c", "echo 123")
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+configDir,
			"HOME="+home,
			"DEVCELL_USER_IMAGE="+userImage,
		)

		out := runPTY(t, cmd)
		if !strings.Contains(out, "123") {
			t.Errorf("expected cell shell output to contain '123', got: %s", out)
		}
	})

	t.Run("nix_version", func(t *testing.T) {
		home := cellShellHome(t)
		cmd := osexec.Command(cellBin, "shell", "--", "bash", "-lc", "nix --version")
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+configDir,
			"HOME="+home,
			"DEVCELL_USER_IMAGE="+userImage,
		)

		out := strings.ToLower(runPTY(t, cmd))
		if !strings.Contains(out, "nix") {
			t.Errorf("expected cell shell output to contain 'nix', got: %s", out)
		}
	})

	t.Run("spinner_visible", func(t *testing.T) {
		home := cellShellHome(t)
		// No --debug flag, so the Go spinner should render in PTY output.
		cmd := osexec.Command(cellBin, "shell", "--", "echo", "done")
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+configDir,
			"HOME="+home,
			"DEVCELL_USER_IMAGE="+userImage,
		)

		out := runPTY(t, cmd)
		t.Logf("PTY output (raw): %q", out)

		// Check for the "Opening Cell" status line.
		if !strings.Contains(out, "Opening Cell") {
			t.Fatalf("'Opening Cell' text not found in PTY output")
		}
		t.Logf("PASS: 'Opening Cell' rendered in PTY output")

		// If Docker successfully started the container, verify command output.
		// Docker may refuse the mount when TMPDIR is not in shared paths
		// (e.g. Docker Desktop file sharing). The spinner still renders.
		if strings.Contains(out, "mounts denied") {
			t.Logf("SKIP: Docker mount denied (TMPDIR not in Docker shared paths) — spinner verified")
		} else if !strings.Contains(out, "done") {
			t.Errorf("expected command output 'done' in PTY output")
		}
	})
}

// runPTY starts cmd in a PTY, collects output, and returns it.
// It runs io.Copy in a goroutine so cmd.Wait() can proceed even if the
// PTY slave side isn't closed yet (common with docker-backed commands).
func runPTY(t *testing.T, cmd *osexec.Cmd) string {
	t.Helper()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		buf.ReadFrom(ptmx)
		close(done)
	}()

	if err := cmd.Wait(); err != nil {
		t.Logf("cmd.Wait: %v (output so far: %s)", err, buf.String())
	}
	ptmx.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Log("warning: PTY read didn't finish within 5s after process exit")
	}
	return buf.String()
}
