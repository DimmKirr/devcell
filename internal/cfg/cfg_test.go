package cfg_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

func writeTOML(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFile_Missing(t *testing.T) {
	c, err := cfg.LoadFile("/no/such/file.toml")
	if err != nil {
		t.Fatalf("missing file should return nil error, got: %v", err)
	}
	if c.Cell.ImageTag != "" || len(c.Env) != 0 || len(c.Volumes) != 0 {
		t.Errorf("missing file should return zero value, got: %+v", c)
	}
}

func TestLoadFile_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
image_tag = "v0.0.0-go"

[env]
MY_TOKEN = "abc123"
OTHER = "val"

[[volumes]]
mount = "~/work/secrets:/run/secrets:ro"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.ImageTag != "v0.0.0-go" {
		t.Errorf("image_tag: want v0.0.0-go, got %q", c.Cell.ImageTag)
	}
	if c.Env["MY_TOKEN"] != "abc123" {
		t.Errorf("MY_TOKEN: want abc123, got %q", c.Env["MY_TOKEN"])
	}
	if c.Env["OTHER"] != "val" {
		t.Errorf("OTHER: want val, got %q", c.Env["OTHER"])
	}
	if len(c.Volumes) != 1 || c.Volumes[0].Mount != "~/work/secrets:/run/secrets:ro" {
		t.Errorf("volumes: unexpected %+v", c.Volumes)
	}
}

func TestMerge_ProjectWinsOnScalar(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "v0.0.0-ultimate"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "v0.0.0-go"}}
	merged := cfg.Merge(global, project)
	if merged.Cell.ImageTag != "v0.0.0-go" {
		t.Errorf("want v0.0.0-go, got %q", merged.Cell.ImageTag)
	}
}

func TestMerge_GlobalScalarKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "v0.0.0-ultimate"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Cell.ImageTag != "v0.0.0-ultimate" {
		t.Errorf("want v0.0.0-ultimate, got %q", merged.Cell.ImageTag)
	}
}

func TestMerge_EnvAccumulates(t *testing.T) {
	global := cfg.CellConfig{Env: map[string]string{"A": "1", "B": "global"}}
	project := cfg.CellConfig{Env: map[string]string{"B": "project", "C": "3"}}
	merged := cfg.Merge(global, project)
	if merged.Env["A"] != "1" {
		t.Errorf("A should be 1, got %q", merged.Env["A"])
	}
	if merged.Env["B"] != "project" {
		t.Errorf("B: project should win, got %q", merged.Env["B"])
	}
	if merged.Env["C"] != "3" {
		t.Errorf("C should be 3, got %q", merged.Env["C"])
	}
}

func TestMerge_VolumesAccumulate(t *testing.T) {
	global := cfg.CellConfig{Volumes: []cfg.VolumeMount{{Mount: "a:b"}}}
	project := cfg.CellConfig{Volumes: []cfg.VolumeMount{{Mount: "c:d:ro"}}}
	merged := cfg.Merge(global, project)
	if len(merged.Volumes) != 2 {
		t.Errorf("want 2 volumes, got %d: %+v", len(merged.Volumes), merged.Volumes)
	}
}

func TestApplyEnv_ImageTagOverride(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "v0.0.0-ultimate"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "IMAGE_TAG" {
			return "v0.0.0-go"
		}
		return ""
	})
	if c.Cell.ImageTag != "v0.0.0-go" {
		t.Errorf("want v0.0.0-go, got %q", c.Cell.ImageTag)
	}
}

func TestLoadFile_NixhomePath(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, "test.toml", `
[cell]
nixhome = "~/dev/nixhome"
`)
	c, err := cfg.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.NixhomePath != "~/dev/nixhome" {
		t.Errorf("want ~/dev/nixhome, got %q", c.Cell.NixhomePath)
	}
}

func TestApplyEnv_NixhomePathOverride(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{NixhomePath: "~/dev/nixhome"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "DEVCELL_NIXHOME_PATH" {
			return "/override/nixhome"
		}
		return ""
	})
	if c.Cell.NixhomePath != "/override/nixhome" {
		t.Errorf("env should override toml: want /override/nixhome, got %q", c.Cell.NixhomePath)
	}
}

func TestApplyEnv_NixhomePathNoOverrideWhenEnvEmpty(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{NixhomePath: "~/dev/nixhome"}}
	cfg.ApplyEnv(&c, func(string) string { return "" })
	if c.Cell.NixhomePath != "~/dev/nixhome" {
		t.Errorf("toml value should persist: want ~/dev/nixhome, got %q", c.Cell.NixhomePath)
	}
}

func TestApplyEnv_NoOverrideWhenEmpty(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "v0.0.0-ultimate"}}
	cfg.ApplyEnv(&c, func(string) string { return "" })
	if c.Cell.ImageTag != "v0.0.0-ultimate" {
		t.Errorf("want v0.0.0-ultimate, got %q", c.Cell.ImageTag)
	}
}

func TestLoadLayered_ProjectWins(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeTOML(t, dir, "global.toml", `
[cell]
image_tag = "v0.0.0-ultimate"
[env]
SHARED = "global"
`)
	projectPath := writeTOML(t, dir, "project.toml", `
[cell]
image_tag = "v0.0.0-go"
[env]
SHARED = "project"
EXTRA = "yes"
`)
	c := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if c.Cell.ImageTag != "v0.0.0-go" {
		t.Errorf("image_tag: want v0.0.0-go, got %q", c.Cell.ImageTag)
	}
	if c.Env["SHARED"] != "project" {
		t.Errorf("SHARED: want project, got %q", c.Env["SHARED"])
	}
	if c.Env["EXTRA"] != "yes" {
		t.Errorf("EXTRA: want yes, got %q", c.Env["EXTRA"])
	}
}

// --- Mise section ---

func TestLoadFile_MiseSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[mise]
idiomatic_version_file = "true"
trusted_config_paths = "/"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Mise["idiomatic_version_file"] != "true" {
		t.Errorf("idiomatic_version_file: want true, got %q", c.Mise["idiomatic_version_file"])
	}
	if c.Mise["trusted_config_paths"] != "/" {
		t.Errorf("trusted_config_paths: want /, got %q", c.Mise["trusted_config_paths"])
	}
}

func TestMerge_MiseAccumulates(t *testing.T) {
	global := cfg.CellConfig{Mise: map[string]string{"A": "1", "B": "global"}}
	project := cfg.CellConfig{Mise: map[string]string{"B": "project", "C": "3"}}
	merged := cfg.Merge(global, project)
	if merged.Mise["A"] != "1" {
		t.Errorf("A should be 1, got %q", merged.Mise["A"])
	}
	if merged.Mise["B"] != "project" {
		t.Errorf("B: project should win, got %q", merged.Mise["B"])
	}
	if merged.Mise["C"] != "3" {
		t.Errorf("C should be 3, got %q", merged.Mise["C"])
	}
}

// --- GUI field ---

func boolPtr(b bool) *bool { return &b }

func TestLoadFile_GUITrue(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
gui = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cell.ResolvedGUI() {
		t.Error("expected ResolvedGUI()=true after parsing gui=true")
	}
}

func TestLoadFile_GUIFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
gui = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.ResolvedGUI() {
		t.Error("expected ResolvedGUI()=false after parsing gui=false")
	}
}

func TestLoadFile_GUIDefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.GUI != nil {
		t.Error("expected GUI=nil when not set in TOML")
	}
	if !c.Cell.ResolvedGUI() {
		t.Error("expected ResolvedGUI()=true when gui not set (default on)")
	}
}

func TestMerge_GUIProjectTrueOverGlobalFalse(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(false)}}
	project := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(true)}}
	merged := cfg.Merge(global, project)
	if !merged.Cell.ResolvedGUI() {
		t.Error("expected project gui=true to win over global gui=false")
	}
}

func TestMerge_GUIProjectFalseOverGlobalTrue(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(true)}}
	project := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(false)}}
	merged := cfg.Merge(global, project)
	if merged.Cell.ResolvedGUI() {
		t.Error("expected project gui=false to win over global gui=true")
	}
}

func TestMerge_GUIGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(true)}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if !merged.Cell.ResolvedGUI() {
		t.Error("expected global gui=true to be preserved when project has no gui setting")
	}
}

func TestMerge_GUIGlobalFalseKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(false)}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Cell.ResolvedGUI() {
		t.Error("expected global gui=false to be preserved when project unset")
	}
}

func TestMerge_GUIBothUnsetDefaultsTrue(t *testing.T) {
	global := cfg.CellConfig{}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if !merged.Cell.ResolvedGUI() {
		t.Error("expected ResolvedGUI()=true when neither global nor project set gui")
	}
}

func TestVolumeMount_PassThrough(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[[volumes]]
mount = "~/work/secrets:/run/secrets:ro"
`)
	c, _ := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if c.Volumes[0].Mount != "~/work/secrets:/run/secrets:ro" {
		t.Errorf("volume mount not passed through: %q", c.Volumes[0].Mount)
	}
}

// --- LLM section (replaces [claude] + [models]) ---

func TestLoadFile_LLMSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[llm]
use_ollama = true
system_prompt = "This project uses Go 1.22."

[llm.models]
default = "ollama/deepseek-r1:32b"

[llm.models.providers.ollama]
models = ["deepseek-r1:32b", "qwen3:8b"]

[llm.models.providers.lmstudio]
base_url = "http://host.docker.internal:1235/v1"
models = ["deepseek-r1:32b"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.LLM.UseOllama {
		t.Error("expected UseOllama=true")
	}
	if c.LLM.SystemPrompt != "This project uses Go 1.22." {
		t.Errorf("system_prompt: got %q", c.LLM.SystemPrompt)
	}
	if c.LLM.Models.Default != "ollama/deepseek-r1:32b" {
		t.Errorf("default: want ollama/deepseek-r1:32b, got %q", c.LLM.Models.Default)
	}
	ollama, ok := c.LLM.Models.Providers["ollama"]
	if !ok {
		t.Fatal("ollama provider not found")
	}
	if len(ollama.Models) != 2 || ollama.Models[0] != "deepseek-r1:32b" {
		t.Errorf("ollama models: %v", ollama.Models)
	}
	if ollama.BaseURL != "" {
		t.Errorf("ollama base_url should be empty (use default), got %q", ollama.BaseURL)
	}
	lms, ok := c.LLM.Models.Providers["lmstudio"]
	if !ok {
		t.Fatal("lmstudio provider not found")
	}
	if lms.BaseURL != "http://host.docker.internal:1235/v1" {
		t.Errorf("lmstudio base_url: got %q", lms.BaseURL)
	}
	if len(lms.Models) != 1 {
		t.Errorf("lmstudio models: %v", lms.Models)
	}
}

func TestLoadFile_LLMMultilineSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[llm]
system_prompt = """
This project uses PostgreSQL 16 with pgx/v5.
API endpoints follow REST conventions at /api/v2/.
"""
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.SystemPrompt == "" {
		t.Fatal("expected non-empty system_prompt")
	}
	if !contains(c.LLM.SystemPrompt, "PostgreSQL 16") {
		t.Errorf("system_prompt missing PostgreSQL 16: %q", c.LLM.SystemPrompt)
	}
	if !contains(c.LLM.SystemPrompt, "/api/v2/") {
		t.Errorf("system_prompt missing /api/v2/: %q", c.LLM.SystemPrompt)
	}
}

func TestLoadFile_LLMDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.UseOllama {
		t.Error("expected UseOllama=false when not set")
	}
	if c.LLM.SystemPrompt != "" {
		t.Errorf("expected empty system_prompt, got %q", c.LLM.SystemPrompt)
	}
	if c.LLM.Models.Default != "" {
		t.Errorf("expected empty default, got %q", c.LLM.Models.Default)
	}
	if len(c.LLM.Models.Providers) != 0 {
		t.Errorf("expected no providers, got %v", c.LLM.Models.Providers)
	}
}

func TestMerge_LLMUseOllamaProjectWins(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{UseOllama: false}}
	project := cfg.CellConfig{LLM: cfg.LLMSection{UseOllama: true}}
	merged := cfg.Merge(global, project)
	if !merged.LLM.UseOllama {
		t.Error("expected project use_ollama=true to win over global false")
	}
}

func TestMerge_LLMGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{UseOllama: true}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if !merged.LLM.UseOllama {
		t.Error("expected global use_ollama=true to be preserved when project unset")
	}
}

func TestMerge_LLMSystemPromptProjectReplaces(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "global context"}}
	project := cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "project context"}}
	merged := cfg.Merge(global, project)
	if merged.LLM.SystemPrompt != "project context" {
		t.Errorf("want project context, got %q", merged.LLM.SystemPrompt)
	}
}

func TestMerge_LLMSystemPromptGlobalKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "global context"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.LLM.SystemPrompt != "global context" {
		t.Errorf("want global context, got %q", merged.LLM.SystemPrompt)
	}
}

func TestMerge_LLMModelsProjectWins(t *testing.T) {
	global := cfg.CellConfig{
		LLM: cfg.LLMSection{
			Models: cfg.LLMModelsSection{
				Default: "ollama/qwen3:8b",
				Providers: map[string]cfg.LLMProvider{
					"ollama": {Models: []string{"qwen3:8b"}},
				},
			},
		},
	}
	project := cfg.CellConfig{
		LLM: cfg.LLMSection{
			Models: cfg.LLMModelsSection{
				Default: "ollama/deepseek-r1:32b",
				Providers: map[string]cfg.LLMProvider{
					"ollama":   {Models: []string{"deepseek-r1:32b"}},
					"lmstudio": {Models: []string{"deepseek-r1:32b"}},
				},
			},
		},
	}
	merged := cfg.Merge(global, project)
	if merged.LLM.Models.Default != "ollama/deepseek-r1:32b" {
		t.Errorf("default: project should win, got %q", merged.LLM.Models.Default)
	}
	if len(merged.LLM.Models.Providers) != 2 {
		t.Errorf("want 2 providers, got %d", len(merged.LLM.Models.Providers))
	}
	if merged.LLM.Models.Providers["ollama"].Models[0] != "deepseek-r1:32b" {
		t.Errorf("ollama models should be project's, got %v", merged.LLM.Models.Providers["ollama"].Models)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && len(sub) > 0 && strings.Contains(s, sub)
}

// --- Git section ---

func TestLoadFile_GitSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[git]
author_name = "Alice"
author_email = "alice@example.com"
committer_name = "Bob"
committer_email = "bob@example.com"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Git.AuthorName != "Alice" {
		t.Errorf("author_name: want Alice, got %q", c.Git.AuthorName)
	}
	if c.Git.AuthorEmail != "alice@example.com" {
		t.Errorf("author_email: want alice@example.com, got %q", c.Git.AuthorEmail)
	}
	if c.Git.CommitterName != "Bob" {
		t.Errorf("committer_name: want Bob, got %q", c.Git.CommitterName)
	}
	if c.Git.CommitterEmail != "bob@example.com" {
		t.Errorf("committer_email: want bob@example.com, got %q", c.Git.CommitterEmail)
	}
}

func TestLoadFile_GitDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Git.HasIdentity() {
		t.Error("expected no git identity when [git] not set")
	}
}

func TestMerge_GitProjectWins(t *testing.T) {
	global := cfg.CellConfig{Git: cfg.GitSection{AuthorName: "Global", AuthorEmail: "global@test.com"}}
	project := cfg.CellConfig{Git: cfg.GitSection{AuthorName: "Project"}}
	merged := cfg.Merge(global, project)
	if merged.Git.AuthorName != "Project" {
		t.Errorf("want Project, got %q", merged.Git.AuthorName)
	}
	if merged.Git.AuthorEmail != "global@test.com" {
		t.Errorf("email should be preserved from global, got %q", merged.Git.AuthorEmail)
	}
}

func TestMerge_GitGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Git: cfg.GitSection{AuthorName: "Global", AuthorEmail: "global@test.com"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Git.AuthorName != "Global" {
		t.Errorf("want Global, got %q", merged.Git.AuthorName)
	}
}

func TestGitSection_HasIdentity(t *testing.T) {
	if (cfg.GitSection{}).HasIdentity() {
		t.Error("empty GitSection should not have identity")
	}
	if !(cfg.GitSection{AuthorEmail: "a@b.com"}).HasIdentity() {
		t.Error("GitSection with author_email should have identity")
	}
}

func TestGitSection_CommitterDefaultsToAuthor(t *testing.T) {
	g := cfg.GitSection{AuthorName: "Alice", AuthorEmail: "alice@test.com"}
	if g.ResolvedCommitterName() != "Alice" {
		t.Errorf("want Alice, got %q", g.ResolvedCommitterName())
	}
	if g.ResolvedCommitterEmail() != "alice@test.com" {
		t.Errorf("want alice@test.com, got %q", g.ResolvedCommitterEmail())
	}
}

func TestGitSection_ExplicitCommitterOverridesAuthor(t *testing.T) {
	g := cfg.GitSection{
		AuthorName: "Alice", AuthorEmail: "alice@test.com",
		CommitterName: "Bot", CommitterEmail: "bot@ci.com",
	}
	if g.ResolvedCommitterName() != "Bot" {
		t.Errorf("want Bot, got %q", g.ResolvedCommitterName())
	}
	if g.ResolvedCommitterEmail() != "bot@ci.com" {
		t.Errorf("want bot@ci.com, got %q", g.ResolvedCommitterEmail())
	}
}

// --- Stack and Modules fields ---

func TestLoadFile_StackField(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
stack = "go"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Stack != "go" {
		t.Errorf("stack: want go, got %q", c.Cell.Stack)
	}
}

func TestLoadFile_ModulesField(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
modules = ["electronics", "desktop"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Cell.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(c.Cell.Modules))
	}
	if c.Cell.Modules[0] != "electronics" || c.Cell.Modules[1] != "desktop" {
		t.Errorf("modules: want [electronics desktop], got %v", c.Cell.Modules)
	}
}

func TestLoadFile_StackDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Stack != "" {
		t.Errorf("expected empty stack when not set, got %q", c.Cell.Stack)
	}
}

func TestLoadFile_ModulesDefaultsNil(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Modules != nil {
		t.Errorf("expected nil modules when not set, got %v", c.Cell.Modules)
	}
}

func TestLoadFile_StackAndModulesTogether(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
stack = "base"
modules = ["go", "electronics", "desktop"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Stack != "base" {
		t.Errorf("stack: want base, got %q", c.Cell.Stack)
	}
	if len(c.Cell.Modules) != 3 {
		t.Fatalf("want 3 modules, got %d", len(c.Cell.Modules))
	}
}

func TestLoadFile_EmptyModulesArray(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
stack = "go"
modules = []
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Stack != "go" {
		t.Errorf("stack: want go, got %q", c.Cell.Stack)
	}
	// Empty array should parse as non-nil empty slice
	if c.Cell.Modules == nil {
		t.Error("expected non-nil empty modules for explicit empty array")
	}
	if len(c.Cell.Modules) != 0 {
		t.Errorf("want 0 modules, got %d", len(c.Cell.Modules))
	}
}

func TestLoadFile_SingleModule(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
modules = ["python"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Cell.Modules) != 1 || c.Cell.Modules[0] != "python" {
		t.Errorf("modules: want [python], got %v", c.Cell.Modules)
	}
}

func TestLoadFile_AllStacks(t *testing.T) {
	stacks := []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"}
	for _, stack := range stacks {
		t.Run(stack, func(t *testing.T) {
			dir := t.TempDir()
			writeTOML(t, dir, "devcell.toml", `
[cell]
stack = "`+stack+`"
`)
			c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if c.Cell.Stack != stack {
				t.Errorf("stack: want %s, got %q", stack, c.Cell.Stack)
			}
		})
	}
}

// --- ResolvedStack ---

func TestCellSection_ResolvedStack_Default(t *testing.T) {
	c := cfg.CellSection{}
	if c.ResolvedStack() != "base" {
		t.Errorf("want base, got %q", c.ResolvedStack())
	}
}

func TestCellSection_ResolvedStack_Explicit(t *testing.T) {
	c := cfg.CellSection{Stack: "go"}
	if c.ResolvedStack() != "go" {
		t.Errorf("want go, got %q", c.ResolvedStack())
	}
}

func TestCellSection_ResolvedStack_Base(t *testing.T) {
	c := cfg.CellSection{Stack: "base"}
	if c.ResolvedStack() != "base" {
		t.Errorf("want base, got %q", c.ResolvedStack())
	}
}

// --- Stack/Modules merge ---

func TestMerge_StackProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Stack: "ultimate"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Stack: "go"}}
	merged := cfg.Merge(global, project)
	if merged.Cell.Stack != "go" {
		t.Errorf("want go, got %q", merged.Cell.Stack)
	}
}

func TestMerge_StackGlobalKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Stack: "go"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Cell.Stack != "go" {
		t.Errorf("want go, got %q", merged.Cell.Stack)
	}
}

func TestMerge_ModulesProjectReplaces(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a"}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"b", "c"}}}
	merged := cfg.Merge(global, project)
	if len(merged.Cell.Modules) != 2 || merged.Cell.Modules[0] != "b" || merged.Cell.Modules[1] != "c" {
		t.Errorf("want [b c], got %v", merged.Cell.Modules)
	}
}

func TestMerge_ModulesGlobalKeptWhenProjectNil(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a"}}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if len(merged.Cell.Modules) != 1 || merged.Cell.Modules[0] != "a" {
		t.Errorf("want [a], got %v", merged.Cell.Modules)
	}
}

func TestMerge_ModulesProjectEmptyArrayClearsGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a", "b"}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{}}}
	merged := cfg.Merge(global, project)
	if len(merged.Cell.Modules) != 0 {
		t.Errorf("explicit empty modules should clear global, got %v", merged.Cell.Modules)
	}
}

func TestMerge_StackAndModulesFromLayeredTOML(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeTOML(t, dir, "global.toml", `
[cell]
stack = "ultimate"
modules = ["desktop"]
`)
	projectPath := writeTOML(t, dir, "project.toml", `
[cell]
stack = "go"
modules = ["electronics"]
`)
	c := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if c.Cell.Stack != "go" {
		t.Errorf("stack: want go, got %q", c.Cell.Stack)
	}
	if len(c.Cell.Modules) != 1 || c.Cell.Modules[0] != "electronics" {
		t.Errorf("modules: want [electronics], got %v", c.Cell.Modules)
	}
}

// --- Validation ---

func TestValidateStack_ValidNames(t *testing.T) {
	valid := []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := cfg.ValidateStack(name); err != nil {
				t.Errorf("valid stack %q rejected: %v", name, err)
			}
		})
	}
}

func TestValidateStack_InvalidName(t *testing.T) {
	err := cfg.ValidateStack("rust")
	if err == nil {
		t.Fatal("expected error for invalid stack 'rust'")
	}
	s := err.Error()
	if !strings.Contains(s, "rust") {
		t.Errorf("error should mention invalid name 'rust': %s", s)
	}
	// Error should list available stacks
	for _, valid := range []string{"base", "go", "node", "python", "ultimate"} {
		if !strings.Contains(s, valid) {
			t.Errorf("error should list available stack %q: %s", valid, s)
		}
	}
}

func TestValidateStack_EmptyIsValid(t *testing.T) {
	// Empty stack means "use default (base)" — not an error
	if err := cfg.ValidateStack(""); err != nil {
		t.Errorf("empty stack should be valid (defaults to ultimate): %v", err)
	}
}

// --- KnownStacks ---

func TestKnownStacks_ReturnsExpectedList(t *testing.T) {
	stacks := cfg.KnownStacks()
	want := []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"}
	if len(stacks) != len(want) {
		t.Fatalf("want %d stacks, got %d: %v", len(want), len(stacks), stacks)
	}
	for i, w := range want {
		if stacks[i] != w {
			t.Errorf("stack[%d]: want %q, got %q", i, w, stacks[i])
		}
	}
}

func TestKnownStacks_ReturnsCopy(t *testing.T) {
	stacks := cfg.KnownStacks()
	stacks[0] = "mutated"
	fresh := cfg.KnownStacks()
	if fresh[0] == "mutated" {
		t.Error("KnownStacks should return a copy, not a reference to internal slice")
	}
}

// --- Ports section ---

func TestLoadFile_PortsSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[ports]
forward = ["3000", "8080:3000", "9090:9090"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ports.Forward) != 3 {
		t.Fatalf("want 3 ports, got %d", len(c.Ports.Forward))
	}
	if c.Ports.Forward[0] != "3000" {
		t.Errorf("port[0]: want 3000, got %q", c.Ports.Forward[0])
	}
	if c.Ports.Forward[1] != "8080:3000" {
		t.Errorf("port[1]: want 8080:3000, got %q", c.Ports.Forward[1])
	}
}

func TestLoadFile_PortsDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ports.Forward) != 0 {
		t.Errorf("expected no ports when [ports] not set, got %v", c.Ports.Forward)
	}
}

func TestMerge_PortsAccumulate(t *testing.T) {
	global := cfg.CellConfig{Ports: cfg.PortsSection{Forward: []string{"3000"}}}
	project := cfg.CellConfig{Ports: cfg.PortsSection{Forward: []string{"8080:3000"}}}
	merged := cfg.Merge(global, project)
	if len(merged.Ports.Forward) != 2 {
		t.Fatalf("want 2 ports, got %d: %v", len(merged.Ports.Forward), merged.Ports.Forward)
	}
	if merged.Ports.Forward[0] != "3000" || merged.Ports.Forward[1] != "8080:3000" {
		t.Errorf("want [3000 8080:3000], got %v", merged.Ports.Forward)
	}
}

func TestMerge_PortsDeduped(t *testing.T) {
	global := cfg.CellConfig{Ports: cfg.PortsSection{Forward: []string{"3000", "4000"}}}
	project := cfg.CellConfig{Ports: cfg.PortsSection{Forward: []string{"3000", "5000"}}}
	merged := cfg.Merge(global, project)
	if len(merged.Ports.Forward) != 3 {
		t.Fatalf("want 3 ports (deduped), got %d: %v", len(merged.Ports.Forward), merged.Ports.Forward)
	}
}

// --- Op section ---

func TestLoadFile_OpDocuments(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[op]
documents = ["prod-nmd-trips", "dev-api-keys"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	docs := c.Op.ResolvedDocuments()
	if len(docs) != 2 {
		t.Fatalf("want 2 op documents, got %d", len(docs))
	}
	if docs[0] != "prod-nmd-trips" || docs[1] != "dev-api-keys" {
		t.Errorf("unexpected op documents: %v", docs)
	}
}

func TestLoadFile_OpLegacyItems(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[op]
items = ["legacy-secret"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	docs := c.Op.ResolvedDocuments()
	if len(docs) != 1 || docs[0] != "legacy-secret" {
		t.Errorf("legacy items should be resolved via ResolvedDocuments: %v", docs)
	}
}

func TestLoadFile_OpDocumentsAndItemsMerged(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[op]
documents = ["new-doc"]
items = ["legacy-item", "new-doc"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	docs := c.Op.ResolvedDocuments()
	// new-doc from documents, legacy-item from items, "new-doc" deduped
	if len(docs) != 2 {
		t.Fatalf("want 2 (deduped), got %v", docs)
	}
	if docs[0] != "new-doc" || docs[1] != "legacy-item" {
		t.Errorf("unexpected merged documents: %v", docs)
	}
}

func TestLoadFile_OpDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Op.ResolvedDocuments()) != 0 {
		t.Errorf("expected no op documents when [op] not set, got %v", c.Op.ResolvedDocuments())
	}
}

func TestMerge_OpDocumentsAccumulateDeduped(t *testing.T) {
	global := cfg.CellConfig{Op: cfg.OpSection{Documents: []string{"shared-keys", "global-only"}}}
	project := cfg.CellConfig{Op: cfg.OpSection{Documents: []string{"shared-keys", "project-only"}}}
	merged := cfg.Merge(global, project)
	want := []string{"shared-keys", "global-only", "project-only"}
	docs := merged.Op.ResolvedDocuments()
	if len(docs) != len(want) {
		t.Fatalf("want %v, got %v", want, docs)
	}
	for i, w := range want {
		if docs[i] != w {
			t.Errorf("doc[%d]: want %q, got %q", i, w, docs[i])
		}
	}
}

func TestMerge_OpLegacyItemsMergedWithDocuments(t *testing.T) {
	global := cfg.CellConfig{Op: cfg.OpSection{Items: []string{"legacy-global"}}}
	project := cfg.CellConfig{Op: cfg.OpSection{Documents: []string{"new-project"}}}
	merged := cfg.Merge(global, project)
	docs := merged.Op.ResolvedDocuments()
	if len(docs) != 2 {
		t.Fatalf("want 2, got %v", docs)
	}
}

// --- [aws] section ---

func TestLoadFile_AwsReadOnlyTrue(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[aws]
read_only = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Aws.ReadOnly == nil || !*c.Aws.ReadOnly {
		t.Error("expected aws.read_only = true")
	}
}

func TestLoadFile_AwsReadOnlyFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[aws]
read_only = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Aws.ReadOnly == nil || *c.Aws.ReadOnly {
		t.Error("expected aws.read_only = false")
	}
}

func TestLoadFile_AwsDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Aws.ReadOnly != nil {
		t.Errorf("expected nil (defaults to false via ResolvedReadOnly), got %v", *c.Aws.ReadOnly)
	}
	if c.Aws.ResolvedReadOnly() {
		t.Error("ResolvedReadOnly should return false when ReadOnly is nil")
	}
}

func TestAwsSection_ResolvedReadOnly(t *testing.T) {
	trueVal := true
	falseVal := false
	tests := []struct {
		name string
		ptr  *bool
		want bool
	}{
		{"nil defaults false", nil, false},
		{"explicit true", &trueVal, true},
		{"explicit false", &falseVal, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := cfg.AwsSection{ReadOnly: tt.ptr}
			if got := s.ResolvedReadOnly(); got != tt.want {
				t.Errorf("ResolvedReadOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMerge_AwsProjectWins(t *testing.T) {
	trueVal := true
	falseVal := false
	global := cfg.CellConfig{Aws: cfg.AwsSection{ReadOnly: &trueVal}}
	project := cfg.CellConfig{Aws: cfg.AwsSection{ReadOnly: &falseVal}}
	merged := cfg.Merge(global, project)
	if merged.Aws.ReadOnly == nil || *merged.Aws.ReadOnly {
		t.Error("project aws.read_only=false should override global true")
	}
}

func TestMerge_AwsGlobalKeptWhenProjectUnset(t *testing.T) {
	falseVal := false
	global := cfg.CellConfig{Aws: cfg.AwsSection{ReadOnly: &falseVal}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Aws.ReadOnly == nil || *merged.Aws.ReadOnly {
		t.Error("global aws.read_only=false should be kept when project unset")
	}
}
