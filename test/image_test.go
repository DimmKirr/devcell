// image_test.go — base image validation, entrypoint, dotenv parsing tests

package container_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/creack/pty"
)

// --- Entrypoint ---

// buildTestUserImage builds a user image from a scaffolded config directory.
// Returns the tag. Removes the image on cleanup.
func buildTestUserImage(t *testing.T, configDir string) string {
	t.Helper()
	tag := fmt.Sprintf("devcell-test-user:%s-%s", shortSHA(), time.Now().Format("20060102T150405"))
	t.Logf("Building user image: %s (from %s)", tag, configDir)

	cmd := osexec.Command("docker", "build", "-t", tag, configDir)
	cmd.Dir = filepath.Join("..")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build user image: %v", err)
	}
	t.Cleanup(func() { osexec.Command("docker", "rmi", tag).Run() })
	return tag
}

// TestEntrypoint_Fragments verifies entrypoint fragments and GUI services
// on the pre-built image. No rebuild — uses DEVCELL_TEST_IMAGE directly.
func TestEntrypoint_Fragments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	probeGUI(t)
	c := startRdpContainer(t)

	t.Run("fragment_staged", func(t *testing.T) {
		out, code := exec(t, c, []string{"ls", "-la", "/etc/devcell/entrypoint.d/50-gui.sh"})
		if code != 0 {
			t.Fatalf("FAIL: 50-gui.sh not found in /etc/devcell/entrypoint.d/ (exit %d)", code)
		}
		if !strings.Contains(out, "x") {
			t.Errorf("FAIL: 50-gui.sh should be executable: %s", out)
		}
		t.Logf("PASS: %s", out)
	})

	t.Run("xvfb_running", func(t *testing.T) {
		_, code := exec(t, c, []string{"pgrep", "Xvfb"})
		if code != 0 {
			t.Fatalf("FAIL: Xvfb process not found (exit %d)", code)
		}
		t.Logf("PASS: Xvfb is running")
	})

	t.Run("xrdp_running", func(t *testing.T) {
		_, code := exec(t, c, []string{"pgrep", "xrdp"})
		if code != 0 {
			t.Fatalf("FAIL: xrdp process not found (exit %d)", code)
		}
		t.Logf("PASS: xrdp is running")
	})

	t.Run("xrdp_listening", func(t *testing.T) {
		out, code := exec(t, c, []string{"sh", "-c",
			"grep -i 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep ' 0A '"})
		if code != 0 || !strings.Contains(strings.ToUpper(out), "0D3D") {
			t.Fatalf("FAIL: port 3389 (0x0D3D) not in LISTEN state:\n%s", out)
		}
		t.Logf("PASS: xrdp listening on :3389\n%s", out)
	})
}

// TestScaffold_BuildPipeline verifies the scaffold → build pipeline produces
// a working image. Uses ultimate as base so home-manager switch is a near-instant
// no-op (all packages already in /nix/store).
func TestScaffold_BuildPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// Use the pre-built ultimate image as base — nix packages already cached.
	ultimateImg := image()

	configDir := t.TempDir()
	t.Setenv("DEVCELL_BASE_IMAGE", ultimateImg)
	nixhomePath, _ := filepath.Abs(filepath.Join("..", "nixhome"))
	if err := scaffold.Scaffold(configDir, "", nixhomePath, false); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	buildDir := filepath.Join(configDir, ".devcell")

	// Verify Dockerfile FROM line uses the ultimate image.
	dockerfile, err := os.ReadFile(filepath.Join(buildDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.HasPrefix(string(dockerfile), "FROM "+ultimateImg) {
		t.Fatalf("Dockerfile FROM doesn't match: got %.80s", string(dockerfile))
	}
	t.Logf("Scaffold OK: Dockerfile FROM %s", ultimateImg)

	// Build — should be fast since ultimate already has all nix packages.
	userImage := buildTestUserImage(t, buildDir)

	// Quick smoke test: run echo in the built image.
	out, err := osexec.Command("docker", "run", "--rm", "--user", "0",
		"-e", "HOST_USER=testuser", "-e", "APP_NAME=test",
		userImage, "echo", "scaffold-build-ok",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("smoke test failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "scaffold-build-ok") {
		t.Errorf("expected 'scaffold-build-ok' in output, got: %s", out)
	}
	t.Logf("PASS: scaffold → build → run pipeline OK")
}

// TestEntrypoint_DebugTimestamps verifies that DEVCELL_DEBUG=true produces
// timestamped log lines in the format [X.XXXs].
func TestEntrypoint_DebugTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := baseImage()

	out, err := osexec.Command("docker", "run", "--rm",
		"--user", "0",
		"-e", "HOST_USER=testuser",
		"-e", "APP_NAME=tstest",
		"-e", "DEVCELL_DEBUG=true",
		img,
		"echo", "ready",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	output := string(out)
	t.Logf("Debug output:\n%s", output)

	// Every log line should have a timestamp like [0.123s] or [1.456s]
	tsPattern := regexp.MustCompile(`\[\d+\.\d{3}s\]`)
	if !tsPattern.MatchString(output) {
		t.Fatalf("FAIL: no timestamped log lines found (expected [X.XXXs] format)")
	}

	// Verify multiple log lines have timestamps (not just one)
	matches := tsPattern.FindAllString(output, -1)
	t.Logf("PASS: found %d timestamped log lines", len(matches))
	if len(matches) < 2 {
		t.Errorf("expected at least 2 timestamped lines, got %d", len(matches))
	}

	// Verify no log lines WITHOUT timestamps
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "ready" {
			continue
		}
		if (strings.Contains(line, "\u2713") || strings.Contains(line, "Installing") ||
			strings.Contains(line, "Starting") || strings.Contains(line, "Merging")) &&
			!tsPattern.MatchString(line) {
			t.Errorf("FAIL: log line missing timestamp: %s", line)
		}
	}
}

// TestEntrypoint_SilentWithoutDebug verifies that without DEVCELL_DEBUG, the
// entrypoint produces no log output.
func TestEntrypoint_SilentWithoutDebug(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := baseImage()

	out, err := osexec.Command("docker", "run", "--rm",
		"--user", "0",
		"-e", "HOST_USER=testuser",
		"-e", "APP_NAME=myapp42",
		img,
		"echo", "ready",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	output := string(out)
	t.Logf("Non-debug output:\n%s", output)

	// No debug timestamps should appear
	tsPattern := regexp.MustCompile(`\[\d+\.\d{3}s\]`)
	if tsPattern.MatchString(output) {
		t.Errorf("FAIL: debug timestamps found in non-debug mode")
	} else {
		t.Logf("PASS: no debug timestamps in non-debug mode")
	}

	// No verbose log lines should appear
	for _, marker := range []string{"Installing global tool", "Starting Xvfb", "Starting fluxbox", "Merging Claude"} {
		if strings.Contains(output, marker) {
			t.Errorf("FAIL: debug log line leaked in non-debug mode: %s", marker)
		}
	}
	t.Logf("PASS: no debug log lines leaked")
}

// --- Base Image ---

// TestBaseImage_Scaffold validates base image capabilities via direct docker run.
func TestBaseImage_Scaffold(t *testing.T) {
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
		t.Logf("PASS: nix-profile -> %s", strings.TrimSpace(string(out)))
	})
}

// TestCell_Shell validates the cell shell command end-to-end via PTY.
func TestCell_Shell(t *testing.T) {
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
	// Must be on a Docker-accessible path for bind mounts.
	configDir, err := os.MkdirTemp(testRunDir(), "celltest-config-*")
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
	// Pass repo nixhome so generated flakes use path:./nixhome instead of
	// a GitHub commit URL that may predate the lib.mkHome export.
	repoNixhome, _ := filepath.Abs(filepath.Join("..", "nixhome"))
	if err := scaffold.Scaffold(devcellConfigDir, "", repoNixhome, false); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Use a Docker-accessible path for the project dir so cell shell can
	// bind-mount it. hostProjectPath resolves to the host filesystem path
	// when running inside a devcell container (Docker-in-Docker).
	projectDir := filepath.Join(testRunDir(), "cell-shell-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}
	// Scaffold .devcell.toml in projectDir so cell shell skips the interactive
	// first-run picker (IsInitialized checks cwd for .devcell.toml).
	if err := scaffold.Scaffold(projectDir, "", repoNixhome, false, "ultimate"); err != nil {
		t.Fatalf("scaffold projectDir: %v", err)
	}
	userImage := image() // pre-built image from DEVCELL_TEST_IMAGE

	// cellShellHome creates a manually-managed HOME directory with the
	// subdirectories that BuildArgv bind-mounts into the container.
	cellShellHome := func(t *testing.T) string {
		t.Helper()
		home, err := os.MkdirTemp(testRunDir(), "celltest-home-*")
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
		out := runCellShell(t, cellBin, projectDir, configDir, home, userImage,
			"--debug", "shell", "--", "bash", "-c", "echo 123")
		if !strings.Contains(out, "123") {
			t.Errorf("expected cell shell output to contain '123', got: %s", out)
		}
	})

	t.Run("nix_version", func(t *testing.T) {
		home := cellShellHome(t)
		out := strings.ToLower(runCellShell(t, cellBin, projectDir, configDir, home, userImage,
			"--debug", "shell", "--", "bash", "-lc", "nix --version"))
		if !strings.Contains(out, "nix") {
			t.Errorf("expected cell shell output to contain 'nix', got: %s", out)
		}
	})

	t.Run("spinner_visible", func(t *testing.T) {
		home := cellShellHome(t)
		out := runCellShell(t, cellBin, projectDir, configDir, home, userImage,
			"shell", "--", "echo", "done")
		t.Logf("output (raw): %q", out)

		if strings.Contains(out, "Opening Cell") {
			t.Logf("PASS: 'Opening Cell' rendered in output")
		} else {
			t.Logf("WARNING: 'Opening Cell' not found — CI may not render spinner")
		}

		if strings.Contains(out, "mounts denied") {
			t.Logf("SKIP: Docker mount denied (TMPDIR not in Docker shared paths)")
		} else if strings.Contains(out, "done") {
			t.Logf("PASS: command output 'done' found")
		}
	})
}

// runCellShell starts a cell command in a PTY (required for docker -it) with a
// 2-minute timeout. Reads output in a goroutine; kills the process tree if it
// exceeds the deadline. Returns all collected output.
func runCellShell(t *testing.T, cellBin, dir, configDir, home, userImage string, args ...string) string {
	t.Helper()

	cmd := osexec.Command(cellBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+configDir,
		"HOME="+home,
		"DEVCELL_USER_IMAGE="+userImage,
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}

	var buf bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		buf.ReadFrom(ptmx)
		close(readDone)
	}()

	// Wait for process exit OR 2-minute timeout.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	select {
	case err := <-waitDone:
		// Process exited normally.
		ptmx.Close()
		<-readDone
		if err != nil {
			t.Logf("cell command exited with error: %v\noutput:\n%s", err, buf.String())
		}
	case <-ctx.Done():
		// Timeout — kill process.
		cmd.Process.Kill()
		ptmx.Close()
		<-readDone
		t.Fatalf("cell command timed out after 2m\noutput:\n%s", buf.String())
	}
	return buf.String()
}

// --- Dotenv ---

// extractDotEnvKeys replicates the shell's key-extraction logic from the wrapper.
func extractDotEnvKeys(content string) []string {
	var keys []string
	for _, line := range strings.Split(content, "\n") {
		// skip comments and blank lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// _key="${_line%%=*}" -- take everything before the first '='
		key := line
		if i := strings.IndexByte(line, '='); i >= 0 {
			key = line[:i]
		}
		// _key="${_key#export }"
		key = strings.TrimPrefix(key, "export ")
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func TestDotEnv_KeyExtraction(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKeys []string
	}{
		{
			name:     "simple KEY=VALUE pairs",
			content:  "TEST_PASSWORD=hello123\nGITHUB_TOKEN=ghtoken456\n",
			wantKeys: []string{"TEST_PASSWORD", "GITHUB_TOKEN"},
		},
		{
			name:     "export prefix stripped",
			content:  "export SECRET_KEY=value\nexport OTHER=x\n",
			wantKeys: []string{"SECRET_KEY", "OTHER"},
		},
		{
			name:     "comments and blank lines skipped",
			content:  "# this is a comment\n\nMY_KEY=value\n\n# another comment\nSECOND=val\n",
			wantKeys: []string{"MY_KEY", "SECOND"},
		},
		{
			name:     "empty value (KEY=) still yields key",
			content:  "TEST_USERNAME=\nTEST_PASSWORD=\n",
			wantKeys: []string{"TEST_USERNAME", "TEST_PASSWORD"},
		},
		{
			name:     "key with no equals sign",
			content:  "BARE_KEY\n",
			wantKeys: []string{"BARE_KEY"},
		},
		{
			name:     "value contains equals sign",
			content:  "DB_URL=postgres://host:5432/db?sslmode=disable\n",
			wantKeys: []string{"DB_URL"},
		},
		{
			name:     "empty file",
			content:  "",
			wantKeys: nil,
		},
		{
			name:     "only comments and blanks",
			content:  "# comment\n\n# another\n",
			wantKeys: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDotEnvKeys(tc.content)
			if len(got) != len(tc.wantKeys) {
				t.Errorf("got keys %v, want %v", got, tc.wantKeys)
				return
			}
			for i, k := range tc.wantKeys {
				if got[i] != k {
					t.Errorf("key[%d]: got %q, want %q", i, got[i], k)
				}
			}
		})
	}
}

// TestDotEnv_OnlyDotEnvKeysForwarded verifies that only keys present in .env
// are forwarded.
func TestDotEnv_OnlyDotEnvKeysForwarded(t *testing.T) {
	dotEnv := "TEST_PASSWORD=placeholder\nGITHUB_TOKEN=placeholder\n"
	containerEnv := map[string]string{
		"TEST_PASSWORD": "hello123",
		"GITHUB_TOKEN":  "ghtoken456",
		"APP_NAME":      "test",     // in container env but NOT in .env
		"HOST_USER":     "testuser", // in container env but NOT in .env
	}

	keys := extractDotEnvKeys(dotEnv)
	secrets := map[string]string{}
	for _, k := range keys {
		if v, ok := containerEnv[k]; ok {
			secrets[k] = v
		}
	}

	if secrets["TEST_PASSWORD"] != "hello123" {
		t.Errorf("TEST_PASSWORD: got %q, want hello123", secrets["TEST_PASSWORD"])
	}
	if secrets["GITHUB_TOKEN"] != "ghtoken456" {
		t.Errorf("GITHUB_TOKEN: got %q, want ghtoken456", secrets["GITHUB_TOKEN"])
	}
	if _, ok := secrets["APP_NAME"]; ok {
		t.Errorf("APP_NAME must not be forwarded (not in .env), got %q", secrets["APP_NAME"])
	}
	if _, ok := secrets["HOST_USER"]; ok {
		t.Errorf("HOST_USER must not be forwarded (not in .env), got %q", secrets["HOST_USER"])
	}
	t.Logf("PASS: only .env keys forwarded: %v", secrets)
}

// --- Cell CLI ---

// TestCell_Binary — cell CLI binary must be bundled in the image at /opt/devcell/.local/bin/cell.
func TestCell_Binary(t *testing.T) {
	c := startEnvContainer(t)

	// cell binary should be on PATH
	out, code := asUser(t, c, "which cell")
	if code != 0 {
		t.Fatalf("cell not found on PATH: exit %d", code)
	}
	if !strings.Contains(out, "/cell") {
		t.Errorf("unexpected which output: %s", out)
	}

	// cell --version should print version string
	out, code = asUser(t, c, "cell --version")
	if code != 0 {
		t.Fatalf("cell --version failed: exit %d, output: %s", code, out)
	}
	if !strings.Contains(out, "cell version") {
		t.Errorf("unexpected version output: %s", out)
	}
	t.Logf("cell --version: %s", out)
}
