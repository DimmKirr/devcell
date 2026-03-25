package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
)

func TestScaffold_CreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
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
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	// Overwrite Dockerfile with sentinel content
	sentinel := "# SENTINEL CONTENT\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(sentinel), 0644); err != nil {
		t.Fatal(err)
	}
	// Scaffold again — must not overwrite
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
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
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	want := "FROM " + runner.BaseImageTag()
	if !strings.HasPrefix(strings.TrimSpace(string(data)), want) {
		t.Errorf("Dockerfile should start with %s, got: %s", want, string(data)[:80])
	}
}

// TestScaffold_DefaultBaseImageIsRemote — without DEVCELL_BASE_IMAGE, new users
// must get the remote registry tag (not base-local which requires local build).
func TestScaffold_DefaultBaseImageIsRemote(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "") // clear any override
	tag := runner.BaseImageTag()
	if strings.Contains(tag, "-local") {
		t.Errorf("default base image must not be a local tag: %s", tag)
	}
	if !strings.HasPrefix(tag, "ghcr.io/dimmkirr/devcell:") {
		t.Errorf("default base image must be from ghcr.io registry: %s", tag)
	}
}

func TestScaffold_BaseImageOverride(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "myregistry.io/devcell:test-v42")
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	want := "FROM myregistry.io/devcell:test-v42"
	if !strings.HasPrefix(strings.TrimSpace(string(data)), want) {
		t.Errorf("Dockerfile should start with %s, got: %s", want, string(data)[:80])
	}
}

// TestScaffold_DockerfileDoesNotInstallHomeManager — home-manager is
// pre-installed in the base image; scaffold must NOT duplicate it.
func TestScaffold_DockerfileDoesNotInstallHomeManager(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	s := string(data)
	if strings.Contains(s, "nix profile install") {
		t.Errorf("Dockerfile should NOT install home-manager (it's in the base image), got:\n%s", s)
	}
}

// TestScaffold_DockerfileRunsHomeManagerSwitch — user Dockerfile must run
// home-manager switch to activate the stack from the user flake.
func TestScaffold_DockerfileRunsHomeManagerSwitch(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(string(data), "home-manager switch") {
		t.Errorf("Dockerfile must contain home-manager switch, got:\n%s", string(data))
	}
}

// TestScaffold_FlakeNixUsesGitHubURL — user flake must fetch nixhome from
// GitHub (not path:/opt/nixhome), so users can point to any nixhome source.
func TestScaffold_FlakeNixUsesGitHubURL(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	s := string(data)
	if !strings.Contains(s, "github:") {
		t.Errorf("flake.nix must use github: URL, got:\n%s", s)
	}
	if strings.Contains(s, "path:/opt/nixhome") {
		t.Errorf("flake.nix must NOT use path:/opt/nixhome (couples to base image internals), got:\n%s", s)
	}
}

func TestScaffold_DevcellTomlIsValidTOML(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
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
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(data), "DimmKirr/devcell") {
		t.Errorf("flake.nix should reference DimmKirr/devcell, got:\n%s", string(data))
	}
}

func TestScaffold_FlakeNixVersionSubstituted(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	s := string(data)
	if strings.Contains(s, "{{VERSION}}") {
		t.Errorf("unreplaced {{VERSION}} placeholder in flake.nix:\n%s", s)
	}
	// Default version.Version is v0.0.0 in tests
	if !strings.Contains(s, "DimmKirr/devcell/v0.0.0?dir=nixhome") {
		t.Errorf("flake.nix should contain versioned URL with v0.0.0, got:\n%s", s)
	}
}

// --- ScaffoldVagrantfile ---

func TestScaffoldVagrantfile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "my-box", ""); err != nil {
		t.Fatalf("ScaffoldVagrantfile failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Vagrantfile")); err != nil {
		t.Errorf("Vagrantfile not created: %v", err)
	}
}

func TestScaffoldVagrantfile_BoxNameSubstituted(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "devcell-macOS26", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	if !strings.Contains(string(data), "devcell-macOS26") {
		t.Errorf("box name not found in Vagrantfile:\n%s", string(data))
	}
	if strings.Contains(string(data), "{{VAGRANT_BOX}}") {
		t.Error("unreplaced {{VAGRANT_BOX}} placeholder found in Vagrantfile")
	}
}

func TestScaffoldVagrantfile_EmptyBoxKeepsEnvFallback(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	// With empty box, the env-var fallback line must still be present
	if !strings.Contains(string(data), "MACOS_BOX") {
		t.Errorf("MACOS_BOX env fallback missing from Vagrantfile:\n%s", string(data))
	}
}

func TestScaffoldVagrantfile_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "first-box", ""); err != nil {
		t.Fatal(err)
	}
	// Second call with different box name must not overwrite
	if err := scaffold.ScaffoldVagrantfile(dir, "second-box", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	if !strings.Contains(string(data), "first-box") {
		t.Error("ScaffoldVagrantfile overwrote existing Vagrantfile — should be idempotent")
	}
}

// --- Scaffold with models snippet ---

func TestScaffold_WithModelsSnippet_InjectsIntoToml(t *testing.T) {
	dir := t.TempDir()
	snippet := "# [models]\n# default = \"ollama/deepseek-r1:70b\"\n# [models.providers.ollama]\n# models = [\"deepseek-r1:70b\", \"qwen3:32b\"]\n"
	if err := scaffold.Scaffold(dir, snippet, "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "devcell.toml"))
	s := string(data)
	if !strings.Contains(s, "deepseek-r1:70b") {
		t.Errorf("expected detected models in devcell.toml, got:\n%s", s)
	}
	if !strings.Contains(s, "qwen3:32b") {
		t.Errorf("expected qwen3:32b in devcell.toml, got:\n%s", s)
	}
}

func TestScaffold_EmptySnippet_UsesDefaultModelsSection(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "devcell.toml"))
	s := string(data)
	// Default template has the generic commented example
	if !strings.Contains(s, "# [llm.models]") {
		t.Errorf("expected default llm.models section in devcell.toml, got:\n%s", s)
	}
}

func TestScaffold_WithSnippet_StillValidTOML(t *testing.T) {
	dir := t.TempDir()
	snippet := "# [llm.models]\n# default = \"ollama/deepseek-r1:70b\"\n# [llm.models.providers.ollama]\n# models = [\"deepseek-r1:70b\"]\n"
	if err := scaffold.Scaffold(dir, snippet, "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "devcell.toml"))
	var v interface{}
	if _, err := toml.Decode(string(data), &v); err != nil {
		t.Errorf("devcell.toml is not valid TOML: %v\ncontent:\n%s", err, string(data))
	}
}

func TestScaffoldVagrantfile_CellHomeUsesDevcell(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	s := string(data)
	if strings.Contains(s, ".claude-sandbox") {
		t.Error("Vagrantfile still references stale .claude-sandbox path")
	}
	if !strings.Contains(s, ".devcell") {
		t.Errorf("Vagrantfile should reference .devcell path, got:\n%s", s)
	}
}

func TestScaffoldVagrantfile_NixhomePathSubstituted(t *testing.T) {
	dir := t.TempDir()
	nixhome := "/Users/dmitry/dev/dimmkirr/devcell/nixhome"
	if err := scaffold.ScaffoldVagrantfile(dir, "", nixhome); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	s := string(data)
	if !strings.Contains(s, nixhome) {
		t.Errorf("nixhome path not found in Vagrantfile:\n%s", s)
	}
	if strings.Contains(s, "{{NIXHOME_PATH}}") {
		t.Error("unreplaced {{NIXHOME_PATH}} placeholder found in Vagrantfile")
	}
}

func TestScaffoldVagrantfile_EmptyNixhomeKeepsEnvFallback(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldVagrantfile(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	if !strings.Contains(string(data), "NIXHOME_PATH") {
		t.Error("NIXHOME_PATH env fallback missing from Vagrantfile")
	}
}

// --- DEVCELL_NIXHOME_PATH support ---

// TestScaffold_WithNixhomePath_FlakeUsesPathInput — when nixhomePath is set,
// flake.nix must use path:./nixhome instead of GitHub URL.
func TestScaffold_WithNixhomePath_FlakeUsesPathInput(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "/some/local/nixhome", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	s := string(data)
	if !strings.Contains(s, `inputs.devcell.url = "path:./nixhome"`) {
		t.Errorf("flake.nix must have inputs.devcell.url = path:./nixhome when nixhomePath is set, got:\n%s", s)
	}
	// The active (non-comment) URL line must not be a github: URL.
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "inputs.devcell.url") && strings.Contains(trimmed, "github:") {
			t.Errorf("active inputs.devcell.url must not use github: when nixhomePath is set, got line: %s", trimmed)
		}
	}
}

// TestScaffold_WithNixhomePath_DockerfileCopiesNixhome — when nixhomePath is set,
// Dockerfile must COPY nixhome/ into the build context before flake.nix.
func TestScaffold_WithNixhomePath_DockerfileCopiesNixhome(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "/some/local/nixhome", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	s := string(data)
	nixhomeCopyLine := "COPY --chown=devcell:usergroup nixhome/"
	if !strings.Contains(s, nixhomeCopyLine) {
		t.Errorf("Dockerfile must COPY nixhome/ when nixhomePath is set, got:\n%s", s)
	}
	// nixhome COPY must appear before flake.* COPY
	nixhomeIdx := strings.Index(s, nixhomeCopyLine)
	flakeCopyIdx := strings.Index(s, "COPY --chown=devcell:usergroup flake.*")
	if nixhomeIdx < 0 || flakeCopyIdx < 0 || nixhomeIdx > flakeCopyIdx {
		t.Errorf("nixhome/ COPY must appear before flake.* COPY in Dockerfile")
	}
}

// TestScaffold_WithoutNixhomePath_DockerfileNoCopyNixhome — when nixhomePath is empty,
// Dockerfile must NOT contain a COPY nixhome/ line (no regression).
func TestScaffold_WithoutNixhomePath_DockerfileNoCopyNixhome(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	s := string(data)
	if strings.Contains(s, "COPY") && strings.Contains(s, "nixhome/") {
		t.Errorf("Dockerfile must NOT COPY nixhome/ when nixhomePath is empty, got:\n%s", s)
	}
}

// TestSyncNixhome_CopiesDirectory — SyncNixhome copies nixhome dir into configDir/nixhome/.
func TestSyncNixhome_CopiesDirectory(t *testing.T) {
	// Create a fake nixhome source with a marker file
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "flake.nix"), []byte("# nixhome flake"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "modules", "base.nix"), []byte("# base"), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	if err := scaffold.SyncNixhome(srcDir, configDir); err != nil {
		t.Fatalf("SyncNixhome failed: %v", err)
	}

	// Verify files were copied
	dest := filepath.Join(configDir, "nixhome", "flake.nix")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", dest, err)
	}
	if string(data) != "# nixhome flake" {
		t.Errorf("expected copied flake.nix content, got: %s", string(data))
	}

	// Verify subdirectory was copied
	subDest := filepath.Join(configDir, "nixhome", "modules", "base.nix")
	if _, err := os.Stat(subDest); err != nil {
		t.Errorf("expected %s to exist: %v", subDest, err)
	}
}

// TestSyncNixhome_ErrorOnMissingPath — SyncNixhome returns error for non-existent source.
func TestSyncNixhome_ErrorOnMissingPath(t *testing.T) {
	configDir := t.TempDir()
	err := scaffold.SyncNixhome("/nonexistent/nixhome", configDir)
	if err == nil {
		t.Error("expected error for non-existent nixhome path, got nil")
	}
}

// TestSyncNixhome_OverwritesExisting — SyncNixhome replaces previous nixhome copy (fresh each build).
func TestSyncNixhome_OverwritesExisting(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "flake.nix"), []byte("# v2"), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	// Pre-populate with stale content
	staleDir := filepath.Join(configDir, "nixhome")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "flake.nix"), []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := scaffold.SyncNixhome(srcDir, configDir); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(configDir, "nixhome", "flake.nix"))
	if string(data) != "# v2" {
		t.Errorf("SyncNixhome should overwrite stale content, got: %s", string(data))
	}
}
