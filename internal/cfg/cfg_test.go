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
image_tag = "latest-go"

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
	if c.Cell.ImageTag != "latest-go" {
		t.Errorf("image_tag: want latest-go, got %q", c.Cell.ImageTag)
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
	global := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "latest-ultimate"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "latest-go"}}
	merged := cfg.Merge(global, project)
	if merged.Cell.ImageTag != "latest-go" {
		t.Errorf("want latest-go, got %q", merged.Cell.ImageTag)
	}
}

func TestMerge_GlobalScalarKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "latest-ultimate"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Cell.ImageTag != "latest-ultimate" {
		t.Errorf("want latest-ultimate, got %q", merged.Cell.ImageTag)
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
	c := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "latest-ultimate"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "IMAGE_TAG" {
			return "latest-go"
		}
		return ""
	})
	if c.Cell.ImageTag != "latest-go" {
		t.Errorf("want latest-go, got %q", c.Cell.ImageTag)
	}
}

func TestApplyEnv_NoOverrideWhenEmpty(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{ImageTag: "latest-ultimate"}}
	cfg.ApplyEnv(&c, func(string) string { return "" })
	if c.Cell.ImageTag != "latest-ultimate" {
		t.Errorf("want latest-ultimate, got %q", c.Cell.ImageTag)
	}
}

func TestLoadLayered_ProjectWins(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeTOML(t, dir, "global.toml", `
[cell]
image_tag = "latest-ultimate"
[env]
SHARED = "global"
`)
	projectPath := writeTOML(t, dir, "project.toml", `
[cell]
image_tag = "latest-go"
[env]
SHARED = "project"
EXTRA = "yes"
`)
	c := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if c.Cell.ImageTag != "latest-go" {
		t.Errorf("image_tag: want latest-go, got %q", c.Cell.ImageTag)
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
	if !c.Cell.GUI {
		t.Error("expected GUI=true after parsing gui=true")
	}
}

func TestLoadFile_GUIDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.GUI {
		t.Error("expected GUI=false when not set")
	}
}

func TestMerge_GUIProjectEnablesOverGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: false}}
	project := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	merged := cfg.Merge(global, project)
	if !merged.Cell.GUI {
		t.Error("expected project gui=true to win over global gui=false")
	}
}

func TestMerge_GUIGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if !merged.Cell.GUI {
		t.Error("expected global gui=true to be preserved when project has no gui setting")
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

// --- Op section ---

func TestLoadFile_OpSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[op]
items = ["prod-nmd-trips", "dev-api-keys"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Op.Items) != 2 {
		t.Fatalf("want 2 op items, got %d", len(c.Op.Items))
	}
	if c.Op.Items[0] != "prod-nmd-trips" || c.Op.Items[1] != "dev-api-keys" {
		t.Errorf("unexpected op items: %v", c.Op.Items)
	}
}

func TestLoadFile_OpDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Op.Items) != 0 {
		t.Errorf("expected no op items when [op] not set, got %v", c.Op.Items)
	}
}

func TestMerge_OpItemsAccumulateDeduped(t *testing.T) {
	global := cfg.CellConfig{Op: cfg.OpSection{Items: []string{"shared-keys", "global-only"}}}
	project := cfg.CellConfig{Op: cfg.OpSection{Items: []string{"shared-keys", "project-only"}}}
	merged := cfg.Merge(global, project)
	want := []string{"shared-keys", "global-only", "project-only"}
	if len(merged.Op.Items) != len(want) {
		t.Fatalf("want %v, got %v", want, merged.Op.Items)
	}
	for i, w := range want {
		if merged.Op.Items[i] != w {
			t.Errorf("item[%d]: want %q, got %q", i, w, merged.Op.Items[i])
		}
	}
}
