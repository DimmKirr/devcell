package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOpencode_ScaffoldsOpenCodeJSON checks that running cell opencode creates
// CellHome/opencode.json when it does not exist.
func TestOpencode_ScaffoldsOpenCodeJSON(t *testing.T) {
	home := scaffoldedHome(t)
	cellHome := filepath.Join(home, ".devcell", "main")
	if err := os.MkdirAll(cellHome, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	target := filepath.Join(cellHome, "opencode.json")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("opencode.json not created at %s: %v", target, err)
	}
}

// TestOpencode_OpenCodeJSONIsValidJSON checks the scaffolded file is valid JSON.
func TestOpencode_OpenCodeJSONIsValidJSON(t *testing.T) {
	home := scaffoldedHome(t)
	cellHome := filepath.Join(home, ".devcell", "main")
	if err := os.MkdirAll(cellHome, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(cellHome, "opencode.json"))
	if err != nil {
		t.Fatalf("opencode.json not found: %v", err)
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Errorf("opencode.json is not valid JSON: %v\ncontent: %s", err, data)
	}
}

// TestOpencode_OpenCodeJSONNotOverwritten checks that an existing opencode.json
// is not overwritten by the lazy scaffold.
func TestOpencode_OpenCodeJSONNotOverwritten(t *testing.T) {
	home := scaffoldedHome(t)
	cellHome := filepath.Join(home, ".devcell", "main")
	if err := os.MkdirAll(cellHome, 0755); err != nil {
		t.Fatal(err)
	}

	sentinel := `{"sentinel": true}`
	target := filepath.Join(cellHome, "opencode.json")
	if err := os.WriteFile(target, []byte(sentinel), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sentinel {
		t.Errorf("opencode.json was overwritten; want %q, got %q", sentinel, string(data))
	}
}
