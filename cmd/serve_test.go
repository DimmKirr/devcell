package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestServe_DryRun verifies "cell serve --dry-run" prints config without starting.
func TestServe_DryRun(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "serve", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("serve --dry-run failed: %v\noutput: %s", err, out)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "port=8484") {
		t.Errorf("expected default port in output, got: %s", output)
	}
}

// TestServe_PortFlag verifies "cell serve --port 9090 --dry-run" shows configured port.
func TestServe_PortFlag(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "serve", "--port", "9090", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("serve --port 9090 --dry-run failed: %v\noutput: %s", err, out)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "port=9090") {
		t.Errorf("expected port=9090 in output, got: %s", output)
	}
}

// TestServe_DryRun_ShowsAPIKey verifies dry-run prints the API key.
func TestServe_DryRun_ShowsAPIKey(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "serve", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home, "DEVCELL_API_KEY=test-secret-123")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("serve --dry-run failed: %v\noutput: %s", err, out)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "api_key=test-secret-123") {
		t.Errorf("expected api_key in output, got: %s", output)
	}
}

// TestServe_DryRun_GeneratesKeyWhenUnset verifies a key is auto-generated.
func TestServe_DryRun_GeneratesKeyWhenUnset(t *testing.T) {
	home := scaffoldedHome(t)

	// Explicitly unset DEVCELL_API_KEY by filtering it out.
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "DEVCELL_API_KEY=") {
			env = append(env, e)
		}
	}
	env = append(env, "CELL_ID=1", "HOME="+home)

	cmd := exec.Command(binaryPath, "serve", "--dry-run")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("serve --dry-run failed: %v\noutput: %s", err, out)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "api_key=dcl-") {
		t.Errorf("expected auto-generated api_key with dcl- prefix, got: %s", output)
	}
}
