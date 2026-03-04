package container_test

import (
	"bytes"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/creack/pty"
)

// TestBaseImage validates base image capabilities via direct docker run.
// CI runs this with DEVCELL_IMAGE pointing to the base image.
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
// CI runs this with DEVCELL_IMAGE pointing to the ultimate image.
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
	configDir := t.TempDir()
	devcellConfigDir := filepath.Join(configDir, "devcell")
	if err := scaffold.Scaffold(devcellConfigDir, ""); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	projectDir := t.TempDir()
	userImage := image() // pre-built image from DEVCELL_IMAGE

	// cellShellHome creates a manually-managed HOME directory.
	// cell shell bind-mounts CellHome into the container, and the container
	// creates files owned by its internal user. t.TempDir() cleanup can't
	// remove those files (permission denied), so we clean up via docker.
	cellShellHome := func(t *testing.T) string {
		t.Helper()
		home, err := os.MkdirTemp("", "celltest-home-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
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

		ptmx, err := pty.Start(cmd)
		if err != nil {
			t.Fatalf("pty.Start cell shell: %v", err)
		}
		defer ptmx.Close()

		var buf bytes.Buffer
		io.Copy(&buf, ptmx)
		cmd.Wait()

		out := buf.String()
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

		ptmx, err := pty.Start(cmd)
		if err != nil {
			t.Fatalf("pty.Start cell shell: %v", err)
		}
		defer ptmx.Close()

		var buf bytes.Buffer
		io.Copy(&buf, ptmx)
		cmd.Wait()

		out := strings.ToLower(buf.String())
		if !strings.Contains(out, "nix") {
			t.Errorf("expected cell shell output to contain 'nix', got: %s", buf.String())
		}
	})
}
