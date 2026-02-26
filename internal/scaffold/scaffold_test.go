package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/version"
)

func TestScaffold_CreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatalf("Scaffold failed: %v", err)
	}
	for _, name := range []string{"Dockerfile", "flake.nix", "devcell.toml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing file %s: %v", name, err)
		}
	}
}

func TestScaffold_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	// Overwrite Dockerfile with sentinel content
	sentinel := "# SENTINEL CONTENT\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(sentinel), 0644); err != nil {
		t.Fatal(err)
	}
	// Scaffold again — must not overwrite
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sentinel {
		t.Error("Scaffold overwrote existing Dockerfile — should be idempotent")
	}
}

func TestScaffold_DockerfileStartsWithFROM(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	want := "FROM ghcr.io/dimmkirr/devcell:" + version.Version
	if !strings.HasPrefix(strings.TrimSpace(string(data)), want) {
		t.Errorf("Dockerfile should start with %s, got: %s", want, string(data)[:80])
	}
}

func TestScaffold_DevcellTomlIsValidTOML(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "devcell.toml"))
	var v interface{}
	if _, err := toml.Decode(string(data), &v); err != nil {
		t.Errorf("devcell.toml is not valid TOML: %v\ncontent:\n%s", err, string(data))
	}
}

func TestScaffold_FlakeNixContainsUpstreamURL(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(data), "DimmKirr/devcell") {
		t.Errorf("flake.nix should reference DimmKirr/devcell, got:\n%s", string(data))
	}
}
