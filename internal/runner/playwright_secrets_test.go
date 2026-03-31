package runner_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlaywrightSecrets_GeneratesFile verifies the entrypoint fragment
// writes DEVCELL_SECRET_KEYS env vars to /run/secrets/devcell.
func TestPlaywrightSecrets_GeneratesFile(t *testing.T) {
	secretsDir := t.TempDir()

	script := `
set -e
log() { :; }
chown() { :; }
HOST_USER=testuser
export BANK_USER=john@example.com
export BANK_PASS='s3cret!'
export DEVCELL_SECRET_KEYS=BANK_USER,BANK_PASS
` + fragmentScript(t, secretsDir) + `
cat "` + secretsDir + `/devcell"
`
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\noutput: %s", err, out)
	}

	content := string(out)
	if !strings.Contains(content, "BANK_USER=john@example.com") {
		t.Errorf("expected BANK_USER=john@example.com in output, got: %s", content)
	}
	if !strings.Contains(content, "BANK_PASS=s3cret!") {
		t.Errorf("expected BANK_PASS=s3cret! in output, got: %s", content)
	}

	// Verify file permissions
	info, err := os.Stat(filepath.Join(secretsDir, "devcell"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}
}

// TestPlaywrightSecrets_SkipsWhenNoKeys verifies the fragment is a
// no-op when DEVCELL_SECRET_KEYS is not set.
func TestPlaywrightSecrets_SkipsWhenNoKeys(t *testing.T) {
	secretsDir := t.TempDir()

	script := `
set -e
log() { :; }
chown() { :; }
HOST_USER=testuser
` + fragmentScript(t, secretsDir)

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = filterEnv(os.Environ(), "DEVCELL_SECRET_KEYS")
	cmd.Env = append(cmd.Env, "HOST_USER=testuser")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\noutput: %s", err, out)
	}

	if _, err := os.Stat(filepath.Join(secretsDir, "devcell")); !os.IsNotExist(err) {
		t.Error("expected no devcell secrets file when DEVCELL_SECRET_KEYS not set")
	}
}

// TestPlaywrightSecrets_SkipsWhenDirMissing verifies the fragment is a
// no-op when /run/secrets is not mounted.
func TestPlaywrightSecrets_SkipsWhenDirMissing(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "nonexistent")

	script := `
set -e
log() { :; }
chown() { :; }
HOST_USER=testuser
export DEVCELL_SECRET_KEYS=FOO
export FOO=bar
` + fragmentScriptCustomDir(t, missingDir)

	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\noutput: %s", err, out)
	}

	if _, err := os.Stat(filepath.Join(missingDir, "devcell")); !os.IsNotExist(err) {
		t.Error("expected no file when secrets dir is missing")
	}
}

// TestPlaywrightSecrets_OnlyWritesDeclaredKeys verifies that only keys
// listed in DEVCELL_SECRET_KEYS are written, not all env vars.
func TestPlaywrightSecrets_OnlyWritesDeclaredKeys(t *testing.T) {
	secretsDir := t.TempDir()

	script := `
set -e
log() { :; }
chown() { :; }
HOST_USER=testuser
export SECRET_A=alpha
export SECRET_B=beta
export NOT_A_SECRET=should-not-appear
export DEVCELL_SECRET_KEYS=SECRET_A,SECRET_B
` + fragmentScript(t, secretsDir) + `
cat "` + secretsDir + `/devcell"
`
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\noutput: %s", err, out)
	}

	content := string(out)
	if !strings.Contains(content, "SECRET_A=alpha") {
		t.Errorf("expected SECRET_A=alpha, got: %s", content)
	}
	if !strings.Contains(content, "SECRET_B=beta") {
		t.Errorf("expected SECRET_B=beta, got: %s", content)
	}
	if strings.Contains(content, "NOT_A_SECRET") {
		t.Errorf("NOT_A_SECRET should not be in secrets file, got: %s", content)
	}
}

func fragmentScript(t *testing.T, dir string) string {
	t.Helper()
	return fragmentScriptCustomDir(t, dir)
}

func fragmentScriptCustomDir(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile("../../nixhome/modules/fragments/21-secrets.sh")
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	script := strings.ReplaceAll(string(data), "/run/secrets", dir)
	script = strings.ReplaceAll(script, "return 0", "exit 0")
	return script
}

func filterEnv(env []string, prefix string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}
