package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
)

func TestScaffold_CreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatalf("Scaffold failed: %v", err)
	}
	// .devcell.toml in project root
	if _, err := os.Stat(filepath.Join(dir, ".devcell.toml")); err != nil {
		t.Errorf("missing .devcell.toml in project root: %v", err)
	}
	// Build artifacts in .devcell/ subdir
	for _, name := range []string{"Dockerfile", "flake.nix"} {
		if _, err := os.Stat(filepath.Join(dir, ".devcell", name)); err != nil {
			t.Errorf("missing %s in .devcell/: %v", name, err)
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
	if err := os.WriteFile(filepath.Join(dir, ".devcell", "Dockerfile"), []byte(sentinel), 0644); err != nil {
		t.Fatal(err)
	}
	// Scaffold again — must not overwrite
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
	want := "FROM " + runner.BaseImageTag()
	if !strings.HasPrefix(strings.TrimSpace(string(data)), want) {
		t.Errorf("Dockerfile should start with %s, got: %s", want, string(data)[:80])
	}
}

// TestScaffold_DefaultBaseImageIsRemote — without DEVCELL_BASE_IMAGE, new users
// must get the remote registry tag (not core-local which requires local build).
func TestScaffold_DefaultBaseImageIsRemote(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "") // clear any override
	tag := runner.BaseImageTag()
	if strings.Contains(tag, "-local") {
		t.Errorf("default base image must not be a local tag: %s", tag)
	}
	if !strings.HasPrefix(tag, "public.ecr.aws/w1l3v2k8/devcell:") {
		t.Errorf("default base image must be from ECR registry: %s", tag)
	}
}

func TestScaffold_BaseImageOverride(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "myregistry.io/devcell:test-v42")
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	var v interface{}
	if _, err := toml.Decode(string(data), &v); err != nil {
		t.Errorf(".devcell.toml is not valid TOML: %v\ncontent:\n%s", err, string(data))
	}
}

func TestScaffold_FlakeNixContainsUpstreamURL(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
	if !strings.Contains(string(data), "DimmKirr/devcell") {
		t.Errorf("flake.nix should reference DimmKirr/devcell, got:\n%s", string(data))
	}
}

func TestScaffold_FlakeNixVersionSubstituted(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	s := string(data)
	if !strings.Contains(s, "deepseek-r1:70b") {
		t.Errorf("expected detected models in .devcell.toml, got:\n%s", s)
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	s := string(data)
	// Default template has the generic commented example
	if !strings.Contains(s, "# [llm.models]") {
		t.Errorf("expected default llm.models section in .devcell.toml, got:\n%s", s)
	}
}

func TestScaffold_WithSnippet_StillValidTOML(t *testing.T) {
	dir := t.TempDir()
	snippet := "# [llm.models]\n# default = \"ollama/deepseek-r1:70b\"\n# [llm.models.providers.ollama]\n# models = [\"deepseek-r1:70b\"]\n"
	if err := scaffold.Scaffold(dir, snippet, "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	var v interface{}
	if _, err := toml.Decode(string(data), &v); err != nil {
		t.Errorf(".devcell.toml is not valid TOML: %v\ncontent:\n%s", err, string(data))
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
	// Create a fake nixhome source so SyncNixhome succeeds.
	fakeNixhome := t.TempDir()
	os.WriteFile(filepath.Join(fakeNixhome, "flake.nix"), []byte("# fake"), 0644)
	if err := scaffold.Scaffold(dir, "", fakeNixhome, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
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
	fakeNixhome := t.TempDir()
	os.WriteFile(filepath.Join(fakeNixhome, "flake.nix"), []byte("# fake"), 0644)
	if err := scaffold.Scaffold(dir, "", fakeNixhome, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
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

// --- Scaffold with stack ---

func TestScaffold_WithStack_FlakeUsesChosenStack(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
	s := string(data)
	if !strings.Contains(s, "devcell.stacks.go") {
		t.Errorf("flake.nix should contain devcell.stacks.go, got:\n%s", s)
	}
	if strings.Contains(s, "devcell.stacks.ultimate") {
		t.Errorf("flake.nix should NOT contain devcell.stacks.ultimate when stack=go:\n%s", s)
	}
}

func TestScaffold_WithStack_TomlHasStack(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	s := string(data)
	if !strings.Contains(s, `stack = "go"`) {
		t.Errorf(".devcell.toml should contain stack = \"go\", got:\n%s", s)
	}
}

func TestScaffold_WithStack_TomlIsValidTOML(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "python"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell.toml"))
	var v interface{}
	if _, err := toml.Decode(string(data), &v); err != nil {
		t.Errorf(".devcell.toml is not valid TOML: %v\ncontent:\n%s", err, string(data))
	}
}

func TestScaffold_EmptyStack_UsesTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
	s := string(data)
	// Template-based flake should use github: URL (not GenerateFlakeNix output)
	if !strings.Contains(s, "github:") {
		t.Errorf("empty stack should use template with github: URL:\n%s", s)
	}
}

func TestScaffold_WithStack_AllStacks(t *testing.T) {
	stacks := []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"}
	for _, stack := range stacks {
		t.Run(stack, func(t *testing.T) {
			dir := t.TempDir()
			if err := scaffold.Scaffold(dir, "", "", false, stack); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
			want := "devcell.stacks." + stack
			if !strings.Contains(string(data), want) {
				t.Errorf("flake.nix should contain %s", want)
			}
		})
	}
}

// --- GenerateFlakeNix ---

// TestGenerateFlakeNix_DefaultStack — ultimate stack with no modules produces devcell.stacks.ultimate.
func TestGenerateFlakeNix_DefaultStack(t *testing.T) {
	content := scaffold.GenerateFlakeNix("ultimate", nil, "v1.0.0", false)
	if !strings.Contains(content, "devcell.stacks.ultimate") {
		t.Errorf("expected devcell.stacks.ultimate in flake.nix:\n%s", content)
	}
	if strings.Contains(content, "devcell.modules.") {
		t.Errorf("no modules expected in default flake.nix:\n%s", content)
	}
}

// TestGenerateFlakeNix_CustomStackWithModules — go stack + electronics module.
func TestGenerateFlakeNix_CustomStackWithModules(t *testing.T) {
	content := scaffold.GenerateFlakeNix("go", []string{"electronics"}, "v1.0.0", false)
	if !strings.Contains(content, "devcell.stacks.go") {
		t.Errorf("expected devcell.stacks.go:\n%s", content)
	}
	if !strings.Contains(content, "devcell.modules.electronics") {
		t.Errorf("expected devcell.modules.electronics:\n%s", content)
	}
}

// TestGenerateFlakeNix_MultipleModules — base stack + go + electronics + desktop.
func TestGenerateFlakeNix_MultipleModules(t *testing.T) {
	content := scaffold.GenerateFlakeNix("base", []string{"go", "electronics", "desktop"}, "v1.0.0", false)
	if !strings.Contains(content, "devcell.stacks.base") {
		t.Errorf("expected devcell.stacks.base:\n%s", content)
	}
	for _, mod := range []string{"go", "electronics", "desktop"} {
		if !strings.Contains(content, "devcell.modules."+mod) {
			t.Errorf("expected devcell.modules.%s:\n%s", mod, content)
		}
	}
}

// TestGenerateFlakeNix_BothArchitectures — must have devcell-local and devcell-local-aarch64.
func TestGenerateFlakeNix_BothArchitectures(t *testing.T) {
	content := scaffold.GenerateFlakeNix("go", nil, "v1.0.0", false)
	if !strings.Contains(content, `"devcell-local"`) {
		t.Errorf("expected devcell-local config:\n%s", content)
	}
	if !strings.Contains(content, `"devcell-local-aarch64"`) {
		t.Errorf("expected devcell-local-aarch64 config:\n%s", content)
	}
}

// TestGenerateFlakeNix_VersionSubstituted — version placeholder must be replaced.
func TestGenerateFlakeNix_VersionSubstituted(t *testing.T) {
	content := scaffold.GenerateFlakeNix("go", nil, "v2.3.4", false)
	if strings.Contains(content, "{{VERSION}}") {
		t.Errorf("unreplaced {{VERSION}} placeholder:\n%s", content)
	}
	if !strings.Contains(content, "DimmKirr/devcell/v2.3.4?dir=nixhome") {
		t.Errorf("expected versioned URL with v2.3.4:\n%s", content)
	}
}

// TestGenerateFlakeNix_NixhomePath — when nixhomePath set, uses path:./nixhome.
func TestGenerateFlakeNix_NixhomePath(t *testing.T) {
	content := scaffold.GenerateFlakeNix("go", nil, "v1.0.0", true)
	if !strings.Contains(content, `"path:./nixhome"`) {
		t.Errorf("expected path:./nixhome when nixhomePath set:\n%s", content)
	}
	// Should not have active github: URL
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "inputs.devcell.url") && strings.Contains(trimmed, "github:") {
			t.Errorf("active URL should not be github: when nixhomePath set: %s", trimmed)
		}
	}
}

// TestGenerateFlakeNix_NoPlaceholders — no {{ }} left in output.
func TestGenerateFlakeNix_NoPlaceholders(t *testing.T) {
	content := scaffold.GenerateFlakeNix("base", []string{"go", "python"}, "v1.0.0", false)
	if strings.Contains(content, "{{") {
		t.Errorf("unreplaced placeholder in flake.nix:\n%s", content)
	}
}

// TestGenerateFlakeNix_X86Architecture — devcell-local uses x86_64-linux.
func TestGenerateFlakeNix_X86Architecture(t *testing.T) {
	content := scaffold.GenerateFlakeNix("go", nil, "v1.0.0", false)
	if !strings.Contains(content, `"x86_64-linux"`) {
		t.Errorf("expected x86_64-linux in devcell-local config:\n%s", content)
	}
	if !strings.Contains(content, `"aarch64-linux"`) {
		t.Errorf("expected aarch64-linux in devcell-local-aarch64 config:\n%s", content)
	}
}

// TestGenerateFlakeNix_StackOnlyNoModules — should have stack but no modules lines.
func TestGenerateFlakeNix_StackOnlyNoModules(t *testing.T) {
	content := scaffold.GenerateFlakeNix("python", nil, "v1.0.0", false)
	if !strings.Contains(content, "devcell.stacks.python") {
		t.Errorf("expected devcell.stacks.python:\n%s", content)
	}
	if strings.Contains(content, "devcell.modules.") {
		t.Errorf("expected no module references:\n%s", content)
	}
}

// TestGenerateFlakeNix_AllStacks — each known stack produces correct stacks reference.
func TestGenerateFlakeNix_AllStacks(t *testing.T) {
	stacks := []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"}
	for _, stack := range stacks {
		t.Run(stack, func(t *testing.T) {
			content := scaffold.GenerateFlakeNix(stack, nil, "v1.0.0", false)
			want := "devcell.stacks." + stack
			if !strings.Contains(content, want) {
				t.Errorf("expected %s in output:\n%s", want, content)
			}
		})
	}
}

// --- GenerateDockerfile ---

// TestGenerateDockerfile_UsesLocalProfile — must reference devcell-local, not devcell-ultimate.
func TestGenerateDockerfile_UsesLocalProfile(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if !strings.Contains(content, "devcell-local${ARCH_SUFFIX}") {
		t.Errorf("expected devcell-local profile reference:\n%s", content)
	}
	if strings.Contains(content, "devcell-ultimate") {
		t.Errorf("should not reference devcell-ultimate:\n%s", content)
	}
}

// TestGenerateDockerfile_ConditionalNpmLayer — npm install guarded by `which npm`.
func TestGenerateDockerfile_ConditionalNpmLayer(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if !strings.Contains(content, "which npm") {
		t.Errorf("expected conditional npm layer with 'which npm':\n%s", content)
	}
}

// TestGenerateDockerfile_ConditionalPythonLayer — uv sync guarded by `which uv`.
func TestGenerateDockerfile_ConditionalPythonLayer(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if !strings.Contains(content, "which uv") {
		t.Errorf("expected conditional python layer with 'which uv':\n%s", content)
	}
}

// TestGenerateDockerfile_StartsWithFROM — base image line.
func TestGenerateDockerfile_StartsWithFROM(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if !strings.HasPrefix(strings.TrimSpace(content), "FROM ") {
		t.Errorf("Dockerfile should start with FROM:\n%s", content)
	}
}

// TestGenerateDockerfile_BaseImageOverride — custom base image.
func TestGenerateDockerfile_BaseImageOverride(t *testing.T) {
	content := scaffold.GenerateDockerfile("myregistry.io/devcell:custom")
	if !strings.HasPrefix(strings.TrimSpace(content), "FROM myregistry.io/devcell:custom") {
		t.Errorf("expected custom base image:\n%s", content)
	}
}

// TestGenerateDockerfile_DefaultBaseImage — uses runner.BaseImageTag when no override.
func TestGenerateDockerfile_DefaultBaseImage(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "")
	content := scaffold.GenerateDockerfile("")
	want := "FROM " + runner.BaseImageTag()
	if !strings.HasPrefix(strings.TrimSpace(content), want) {
		t.Errorf("expected %s, got:\n%s", want, content)
	}
}

// TestGenerateDockerfile_HomeManagerSwitch — must run home-manager switch.
func TestGenerateDockerfile_HomeManagerSwitch(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if !strings.Contains(content, "home-manager switch") {
		t.Errorf("expected home-manager switch:\n%s", content)
	}
}

// TestGenerateDockerfile_NixhomeCopyWhenPathSet — COPY nixhome/ line when nixhomePath active.
func TestGenerateDockerfile_NixhomeCopyWhenPathSet(t *testing.T) {
	content := scaffold.GenerateDockerfileWithNixhome("", true, "base", nil)
	if !strings.Contains(content, "COPY --chown=devcell:usergroup nixhome/") {
		t.Errorf("expected COPY nixhome/ when nixhomePath set:\n%s", content)
	}
}

// TestGenerateDockerfile_NoNixhomeCopyByDefault — no COPY nixhome/ when no nixhomePath.
func TestGenerateDockerfile_NoNixhomeCopyByDefault(t *testing.T) {
	content := scaffold.GenerateDockerfile("")
	if strings.Contains(content, "nixhome/") {
		t.Errorf("should not COPY nixhome/ by default:\n%s", content)
	}
}

// --- Metadata ARGs in generated Dockerfile ---

// TestGenerateDockerfile_HasMetadataARGs — generated Dockerfile must declare
// DEVCELL_BASE_IMAGE, DEVCELL_STACK, DEVCELL_MODULES ARGs for metadata.json.
func TestGenerateDockerfile_HasMetadataARGs(t *testing.T) {
	content := scaffold.GenerateDockerfileWithNixhome("ghcr.io/test:core", false, "go", []string{"desktop", "infra"})
	for _, arg := range []string{
		`ARG DEVCELL_BASE_IMAGE="ghcr.io/test:core"`,
		`ARG DEVCELL_STACK="go"`,
		`ARG DEVCELL_MODULES="desktop,infra"`,
	} {
		if !strings.Contains(content, arg) {
			t.Errorf("expected %q in Dockerfile:\n%s", arg, content)
		}
	}
}

// TestGenerateDockerfile_MetadataARGsEmptyModules — empty modules produces empty string.
func TestGenerateDockerfile_MetadataARGsEmptyModules(t *testing.T) {
	content := scaffold.GenerateDockerfileWithNixhome("", false, "base", nil)
	if !strings.Contains(content, `ARG DEVCELL_MODULES=""`) {
		t.Errorf("expected empty DEVCELL_MODULES ARG:\n%s", content)
	}
}

// TestGenerateDockerfile_NoMetadataJSONRunStep — metadata.json is written by nix
// activation (base.nix), NOT a Docker RUN step. The Dockerfile should only have
// the ARGs that propagate through home-manager switch to the nix activation script.
func TestGenerateDockerfile_NoMetadataJSONRunStep(t *testing.T) {
	content := scaffold.GenerateDockerfileWithNixhome("", false, "go", nil)
	// Must NOT have a RUN step writing metadata.json — nix owns this now.
	if strings.Contains(content, "tee /etc/devcell/metadata.json") {
		t.Errorf("Dockerfile should NOT write metadata.json (nix activation handles it):\n%s", content)
	}
	// ARGs must still be present (they propagate to nix via home-manager switch env).
	if !strings.Contains(content, "ARG DEVCELL_STACK=") {
		t.Errorf("expected DEVCELL_STACK ARG (propagates to nix activation):\n%s", content)
	}
}

// TestGenerateDockerfile_NoUserImageVersion — old user-image-version stamp is removed.
func TestGenerateDockerfile_NoUserImageVersion(t *testing.T) {
	content := scaffold.GenerateDockerfileWithNixhome("", false, "go", nil)
	if strings.Contains(content, "user-image-version") {
		t.Errorf("user-image-version should be replaced by metadata.json:\n%s", content)
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

// --- RegenerateBuildContext ---

// TestRegenerateBuildContext_WritesFlakeAndDockerfile — regenerates all build artifacts.
func TestRegenerateBuildContext_WritesFlakeAndDockerfile(t *testing.T) {
	dir := t.TempDir()
	// Scaffold initial config so devcell.toml exists (needed for package files).
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}

	// Read back config and change stack to python.
	cfg := cfg.CellConfig{
		Cell: cfg.CellSection{Stack: "python"},
		Packages: cfg.PackagesSection{
			Npm:    map[string]string{"codex": "^1.0.0"},
			Python: map[string]string{"httpie": "*"},
		},
	}

	if err := scaffold.RegenerateBuildContext(dir, cfg); err != nil {
		t.Fatal(err)
	}

	// flake.nix should reference python stack.
	flake, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(flake), "devcell.stacks.python") {
		t.Errorf("flake.nix should reference devcell.stacks.python:\n%s", string(flake))
	}

	// Dockerfile should reference devcell-local, not devcell-ultimate.
	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(string(df), "devcell-local") {
		t.Errorf("Dockerfile should reference devcell-local:\n%s", string(df))
	}
	if strings.Contains(string(df), "devcell-ultimate") {
		t.Errorf("Dockerfile should NOT reference devcell-ultimate:\n%s", string(df))
	}
}

// TestRegenerateBuildContext_IncludesModules — modules are appended in flake.nix.
func TestRegenerateBuildContext_IncludesModules(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}

	cfg := cfg.CellConfig{
		Cell: cfg.CellSection{
			Stack:   "go",
			Modules: []string{"electronics", "desktop"},
		},
	}

	if err := scaffold.RegenerateBuildContext(dir, cfg); err != nil {
		t.Fatal(err)
	}

	flake, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	for _, mod := range []string{"devcell.stacks.go", "devcell.modules.electronics", "devcell.modules.desktop"} {
		if !strings.Contains(string(flake), mod) {
			t.Errorf("flake.nix should contain %s:\n%s", mod, string(flake))
		}
	}
}

// TestRegenerateBuildContext_DefaultStack — empty stack defaults to base.
func TestRegenerateBuildContext_DefaultStack(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "base"); err != nil {
		t.Fatal(err)
	}

	cfg := cfg.CellConfig{
		Cell: cfg.CellSection{}, // Stack empty → ResolvedStack() returns "base"
	}

	if err := scaffold.RegenerateBuildContext(dir, cfg); err != nil {
		t.Fatal(err)
	}

	flake, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(flake), "devcell.stacks.base") {
		t.Errorf("empty stack should default to base:\n%s", string(flake))
	}
}

// --- resolveBaseImage (via RegenerateBuildContext) ---

// TestRegenerateBuildContext_BaseStackUsesCore — base stack doesn't attempt
// pre-built cache, always uses the core image.
func TestRegenerateBuildContext_BaseStackUsesCore(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "base"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVCELL_BASE_IMAGE", "")

	c := cfg.CellConfig{
		Cell: cfg.CellSection{Stack: "base"},
	}
	if err := scaffold.RegenerateBuildContext(dir, c); err != nil {
		t.Fatal(err)
	}

	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.HasPrefix(string(df), "FROM public.ecr.aws/w1l3v2k8/devcell:v0.0.0-core") {
		t.Errorf("base stack should use core image, got:\n%s", strings.SplitN(string(df), "\n", 2)[0])
	}
}

// TestRegenerateBuildContext_EnvOverrideWinsOverCache — DEVCELL_BASE_IMAGE
// takes precedence over pre-built stack cache.
func TestRegenerateBuildContext_EnvOverrideWinsOverCache(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVCELL_BASE_IMAGE", "my-custom:image")

	c := cfg.CellConfig{
		Cell: cfg.CellSection{Stack: "go"},
	}
	if err := scaffold.RegenerateBuildContext(dir, c); err != nil {
		t.Fatal(err)
	}

	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.HasPrefix(string(df), "FROM my-custom:image") {
		t.Errorf("DEVCELL_BASE_IMAGE should override cache, got:\n%s", strings.SplitN(string(df), "\n", 2)[0])
	}
}

// TestRegenerateBuildContext_NonBaseStackFallsBackToCore — when pre-built
// stack image is not available, falls back to core.
func TestRegenerateBuildContext_NonBaseStackFallsBackToCore(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false, "go"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVCELL_BASE_IMAGE", "")

	c := cfg.CellConfig{
		Cell: cfg.CellSection{Stack: "go"},
	}
	if err := scaffold.RegenerateBuildContext(dir, c); err != nil {
		t.Fatal(err)
	}

	// In test env, docker images aren't available — should fall back to core.
	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	fromLine := strings.SplitN(string(df), "\n", 2)[0]
	if !strings.HasPrefix(fromLine, "FROM public.ecr.aws/w1l3v2k8/devcell:v0.0.0-core") {
		t.Errorf("should fall back to core when pre-built not available, got:\n%s", fromLine)
	}
}

// --- RegenerateBuildContext detects nixhome on disk ---

func TestRegenerateBuildContext_DetectsNixhomeOnDisk(t *testing.T) {
	dir := t.TempDir()
	// Create nixhome/ directory to simulate SyncNixhome having run.
	os.MkdirAll(filepath.Join(dir, "nixhome"), 0755)

	cellCfg := cfg.CellConfig{Cell: cfg.CellSection{Stack: "go"}}
	if err := scaffold.RegenerateBuildContext(dir, cellCfg); err != nil {
		t.Fatal(err)
	}

	// flake.nix should use path:./nixhome (not github:)
	flake, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(flake), `path:./nixhome`) {
		t.Errorf("flake.nix should use path:./nixhome when nixhome/ exists on disk:\n%s", string(flake))
	}
	// Dockerfile should COPY nixhome/
	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(string(df), "COPY") || !strings.Contains(string(df), "nixhome/") {
		t.Errorf("Dockerfile should COPY nixhome/ when it exists on disk:\n%s", string(df))
	}
}

func TestRegenerateBuildContext_NoNixhomeOnDisk(t *testing.T) {
	dir := t.TempDir()
	// No nixhome/ directory — should use github URL.
	cellCfg := cfg.CellConfig{Cell: cfg.CellSection{Stack: "go"}}
	if err := scaffold.RegenerateBuildContext(dir, cellCfg); err != nil {
		t.Fatal(err)
	}

	flake, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(flake), "github:") {
		t.Errorf("flake.nix should use github: when nixhome/ doesn't exist:\n%s", string(flake))
	}
	if strings.Contains(string(flake), "path:./nixhome") {
		t.Errorf("flake.nix should NOT use path:./nixhome:\n%s", string(flake))
	}
	df, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if strings.Contains(string(df), "nixhome/") {
		t.Errorf("Dockerfile should NOT COPY nixhome/ when it doesn't exist:\n%s", string(df))
	}
}

// --- Scaffold local-first ---

func TestScaffold_WritesDotDevcellToml(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".devcell.toml")); err != nil {
		t.Error(".devcell.toml should exist in project root")
	}
}

func TestScaffold_BuildArtifactsInDotDevcellDir(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Dockerfile", "flake.nix", "package.json", "pyproject.toml"} {
		path := filepath.Join(dir, ".devcell", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s in .devcell/ subdir: %v", name, err)
		}
	}
}

func TestScaffold_NoBuildArtifactsInProjectRoot(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	// Dockerfile and flake.nix should NOT be in project root
	for _, name := range []string{"Dockerfile", "flake.nix"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s should NOT be in project root, only in .devcell/", name)
		}
	}
}

func TestScaffold_NoOldStyleDevcellToml(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	// Old-style devcell.toml (without dot) should NOT be created
	if _, err := os.Stat(filepath.Join(dir, "devcell.toml")); err == nil {
		t.Error("old-style devcell.toml should NOT be created")
	}
}

func TestScaffold_IsInitializedAfterScaffold(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.Scaffold(dir, "", "", false); err != nil {
		t.Fatal(err)
	}
	if !scaffold.IsInitialized(dir) {
		t.Error("IsInitialized should return true after Scaffold")
	}
}

// --- IsInitialized ---

func TestIsInitialized_TrueWhenDotDevcellTomlExists(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".devcell.toml"), []byte("[cell]\n"), 0644)
	if !scaffold.IsInitialized(dir) {
		t.Error("IsInitialized should return true when .devcell.toml exists")
	}
}

func TestIsInitialized_FalseWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	if scaffold.IsInitialized(dir) {
		t.Error("IsInitialized should return false in empty dir")
	}
}

func TestIsInitialized_FalseWhenOnlyGlobalTomlExists(t *testing.T) {
	dir := t.TempDir()
	// Old-style devcell.toml (without dot) should NOT count as initialized
	os.WriteFile(filepath.Join(dir, "devcell.toml"), []byte("[cell]\n"), 0644)
	if scaffold.IsInitialized(dir) {
		t.Error("IsInitialized should return false for old-style devcell.toml (without dot)")
	}
}
