package container_test

import (
	"bytes"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
)

// workflowSetup builds the cell binary, runs cell init --yes, and returns
// the cell binary path and the scaffold config directory.
// It skips in short mode and registers cleanup to remove devcell-local image.
func workflowSetup(t *testing.T) (cellBin, configDir, projectDir string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping workflow test in short mode")
	}

	// Build cell binary.
	cellBin = filepath.Join(t.TempDir(), "cell")
	build := osexec.Command("go", "build", "-o", cellBin, "./cmd")
	build.Dir = filepath.Join("..")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build cell: %v", err)
	}

	// Run cell init --yes (scaffolds + docker build -t devcell-local).
	configDir = t.TempDir()
	projectDir = t.TempDir()

	cmd := osexec.Command(cellBin, "init", "--yes")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+configDir,
		"HOME="+t.TempDir(),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cell init --yes: %v", err)
	}

	t.Cleanup(func() {
		osexec.Command("docker", "rmi", "-f", "devcell-local").Run()
	})

	return cellBin, configDir, projectDir
}

// TestWorkflow_ImageContents tests: cell init → assert scaffold → docker run commands.
// Validates the built image directly without going through cell shell.
func TestWorkflow_ImageContents(t *testing.T) {
	_, configDir, _ := workflowSetup(t)
	devcellConfigDir := filepath.Join(configDir, "devcell")

	// Assert scaffold output files exist.
	for _, name := range []string{"Dockerfile", "flake.nix", "devcell.toml"} {
		path := filepath.Join(devcellConfigDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected scaffold file %s: %v", name, err)
		}
	}

	// Assert Dockerfile starts with the expected FROM.
	df, err := os.ReadFile(filepath.Join(devcellConfigDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.HasPrefix(string(df), "FROM ghcr.io/dimmkirr/devcell:v0.0.0") {
		t.Errorf("Dockerfile FROM mismatch; got: %s", strings.SplitN(string(df), "\n", 2)[0])
	}

	// bash echo — image runs and bash works.
	t.Run("bash_echo", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--user", "0",
			"-e", "HOST_USER=testuser",
			"-e", "APP_NAME=test",
			"devcell-local",
			"bash", "-c", "echo 123",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run bash echo: %v\noutput: %s", err, out)
		}
		if !strings.Contains(string(out), "123") {
			t.Errorf("expected output to contain '123', got: %s", out)
		}
	})

	// nix --version — nix is available in the image.
	t.Run("nix_version", func(t *testing.T) {
		out, err := osexec.Command("docker", "run", "--rm",
			"--user", "0",
			"-e", "HOST_USER=testuser",
			"-e", "APP_NAME=test",
			"devcell-local",
			"gosu", "testuser", "bash", "-lc", "nix --version",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run nix --version: %v\noutput: %s", err, out)
		}
		if !strings.Contains(strings.ToLower(string(out)), "nix") {
			t.Errorf("expected output to contain 'nix', got: %s", out)
		}
	})
}

// TestWorkflow_CellShell tests the actual cell shell command end-to-end.
// Uses a PTY because cell shell runs docker with -it.
func TestWorkflow_CellShell(t *testing.T) {
	cellBin, configDir, projectDir := workflowSetup(t)

	// cell shell -- bash -c "echo 123"
	t.Run("bash_echo", func(t *testing.T) {
		cmd := osexec.Command(cellBin, "shell", "--", "bash", "-c", "echo 123")
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+configDir,
			"HOME="+t.TempDir(),
		)

		ptmx, err := pty.Start(cmd)
		if err != nil {
			t.Fatalf("pty.Start cell shell: %v", err)
		}
		defer ptmx.Close()

		var buf bytes.Buffer
		io.Copy(&buf, ptmx) // reads until process exits / PTY closes
		cmd.Wait()

		out := buf.String()
		if !strings.Contains(out, "123") {
			t.Errorf("expected cell shell output to contain '123', got: %s", out)
		}
	})

	// cell shell -- bash -lc "nix --version"
	t.Run("nix_version", func(t *testing.T) {
		cmd := osexec.Command(cellBin, "shell", "--", "bash", "-lc", "nix --version")
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+configDir,
			"HOME="+t.TempDir(),
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
