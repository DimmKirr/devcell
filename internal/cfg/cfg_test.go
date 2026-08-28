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

func TestVolumeMount_Resolved(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/host/path:/container/path", "/host/path:/container/path"},
		{"/host:/container:ro", "/host:/container:ro"},
		{"/Users/dmitry/dev/evercars/evercars-backend", "/Users/dmitry/dev/evercars/evercars-backend:/Users/dmitry/dev/evercars/evercars-backend"},
		{"", ""},
	}
	for _, tc := range cases {
		got := cfg.VolumeMount{Mount: tc.in}.Resolved()
		if got != tc.want {
			t.Errorf("Resolved(%q) = %q, want %q", tc.in, got, tc.want)
		}
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

func TestMerge_VolumesDedupByContainerPath(t *testing.T) {
	global := cfg.CellConfig{Volumes: []cfg.VolumeMount{
		{Mount: "/host/a:/container/shared"},
	}}
	project := cfg.CellConfig{Volumes: []cfg.VolumeMount{
		{Mount: "/host/b:/container/shared"},
	}}
	merged := cfg.Merge(global, project)
	if len(merged.Volumes) != 1 {
		t.Fatalf("want 1 volume (deduped), got %d: %+v", len(merged.Volumes), merged.Volumes)
	}
	if merged.Volumes[0].Mount != "/host/b:/container/shared" {
		t.Errorf("project should win on conflict, got %q", merged.Volumes[0].Mount)
	}
}

func TestMerge_VolumesDedupShorthand(t *testing.T) {
	global := cfg.CellConfig{Volumes: []cfg.VolumeMount{
		{Mount: "/Users/dmitry/dev/skills"},
	}}
	project := cfg.CellConfig{Volumes: []cfg.VolumeMount{
		{Mount: "/Users/dmitry/dev/skills"},
	}}
	merged := cfg.Merge(global, project)
	if len(merged.Volumes) != 1 {
		t.Errorf("want 1 volume (deduped shorthand), got %d: %+v", len(merged.Volumes), merged.Volumes)
	}
}

func TestVolumeMount_ContainerPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/host:/container", "/container"},
		{"/host:/container:ro", "/container"},
		{"/Users/dmitry/dev/skills", "/Users/dmitry/dev/skills"},
		{"/Users/dmitry/dev/skills/", "/Users/dmitry/dev/skills"},
		{"/host/:/container/", "/container"},
		{"", ""},
	}
	for _, tc := range cases {
		got := cfg.VolumeMount{Mount: tc.in}.ContainerPath()
		if got != tc.want {
			t.Errorf("ContainerPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
[nix]
nixhome = "~/dev/nixhome"
`)
	c, err := cfg.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Nix.NixhomePath != "~/dev/nixhome" {
		t.Errorf("want ~/dev/nixhome, got %q", c.Nix.NixhomePath)
	}
}

func TestApplyEnv_NixhomePathOverride(t *testing.T) {
	c := cfg.CellConfig{Nix: cfg.NixSection{NixhomePath: "~/dev/nixhome"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "DEVCELL_NIXHOME_PATH" {
			return "/override/nixhome"
		}
		return ""
	})
	if c.Nix.NixhomePath != "/override/nixhome" {
		t.Errorf("env should override toml: want /override/nixhome, got %q", c.Nix.NixhomePath)
	}
}

func TestApplyEnv_NixhomePathNoOverrideWhenEnvEmpty(t *testing.T) {
	c := cfg.CellConfig{Nix: cfg.NixSection{NixhomePath: "~/dev/nixhome"}}
	cfg.ApplyEnv(&c, func(string) string { return "" })
	if c.Nix.NixhomePath != "~/dev/nixhome" {
		t.Errorf("toml value should persist: want ~/dev/nixhome, got %q", c.Nix.NixhomePath)
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
	c, err := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
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

// --- [gui] section ---

func TestLoadFile_GUISectionEnabled(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
enabled = true
wm = "fluxbox"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.GUI.ResolvedEnabled() {
		t.Error("expected GUI.ResolvedEnabled()=true")
	}
	if c.GUI.ResolvedWM() != "fluxbox" {
		t.Errorf("expected WM=fluxbox, got %q", c.GUI.ResolvedWM())
	}
}

func TestLoadFile_GUISectionDisabled(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
enabled = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedEnabled() {
		t.Error("expected GUI.ResolvedEnabled()=false")
	}
}

func TestLoadFile_GUISectionDefaultWM(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
enabled = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedWM() != "icewm" {
		t.Errorf("expected default WM=icewm, got %q", c.GUI.ResolvedWM())
	}
}

func TestLoadFile_GUILegacyCellGUIMigratesToSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
gui = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedEnabled() {
		t.Error("expected legacy [cell] gui=false to migrate to GUI.Enabled=false")
	}
}

func TestLoadFile_GUISectionWinsOverLegacy(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
gui = false

[gui]
enabled = true
wm = "fluxbox"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.GUI.ResolvedEnabled() {
		t.Error("expected [gui] enabled=true to win over [cell] gui=false")
	}
}

func TestMerge_GUISectionProjectWMOverridesGlobal(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{WM: "icewm"}}
	project := cfg.CellConfig{GUI: cfg.GUISection{WM: "fluxbox"}}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedWM() != "fluxbox" {
		t.Errorf("expected project wm=fluxbox to win, got %q", merged.GUI.ResolvedWM())
	}
}

func TestMerge_GUISectionGlobalWMKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{WM: "fluxbox"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedWM() != "fluxbox" {
		t.Errorf("expected global wm=fluxbox preserved, got %q", merged.GUI.ResolvedWM())
	}
}

func TestLoadFile_GUISectionResolution(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
resolution = "2560x1440x24"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedResolution() != "2560x1440x24" {
		t.Errorf("expected resolution=2560x1440x24, got %q", c.GUI.ResolvedResolution())
	}
}

func TestLoadFile_GUISectionDefaultResolution(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
enabled = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedResolution() != "1920x1080x24" {
		t.Errorf("expected default resolution=1920x1080x24, got %q", c.GUI.ResolvedResolution())
	}
}

func TestMerge_GUISectionProjectResolutionOverridesGlobal(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{Resolution: "1920x1080x24"}}
	project := cfg.CellConfig{GUI: cfg.GUISection{Resolution: "2560x1440x24"}}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedResolution() != "2560x1440x24" {
		t.Errorf("expected project resolution to win, got %q", merged.GUI.ResolvedResolution())
	}
}

func TestMerge_GUISectionGlobalResolutionKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{Resolution: "2560x1440x24"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedResolution() != "2560x1440x24" {
		t.Errorf("expected global resolution preserved, got %q", merged.GUI.ResolvedResolution())
	}
}

func TestLoadFile_GUISectionScale(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
resolution = "1800x1169x24"
scale = 2
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedScale() != 2 {
		t.Errorf("expected scale=2, got %d", c.GUI.ResolvedScale())
	}
	if c.GUI.ResolvedDPI() != 192 {
		t.Errorf("expected DPI=192, got %d", c.GUI.ResolvedDPI())
	}
	if c.GUI.ResolvedFramebufferResolution() != "3600x2338x24" {
		t.Errorf("expected framebuffer=3600x2338x24, got %q", c.GUI.ResolvedFramebufferResolution())
	}
}

func TestLoadFile_GUISectionDefaultScale(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
enabled = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedScale() != 1 {
		t.Errorf("expected default scale=1, got %d", c.GUI.ResolvedScale())
	}
	if c.GUI.ResolvedDPI() != 96 {
		t.Errorf("expected default DPI=96, got %d", c.GUI.ResolvedDPI())
	}
	if c.GUI.ResolvedFramebufferResolution() != "1920x1080x24" {
		t.Errorf("expected default framebuffer=1920x1080x24, got %q", c.GUI.ResolvedFramebufferResolution())
	}
}

func TestLoadFile_GUISectionScale1(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[gui]
resolution = "1800x1169x24"
scale = 1
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUI.ResolvedScale() != 1 {
		t.Errorf("expected scale=1, got %d", c.GUI.ResolvedScale())
	}
	if c.GUI.ResolvedDPI() != 96 {
		t.Errorf("expected DPI=96, got %d", c.GUI.ResolvedDPI())
	}
	if c.GUI.ResolvedFramebufferResolution() != "1800x1169x24" {
		t.Errorf("expected framebuffer=1800x1169x24, got %q", c.GUI.ResolvedFramebufferResolution())
	}
}

func TestMerge_GUISectionProjectScaleOverridesGlobal(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{Scale: 1}}
	project := cfg.CellConfig{GUI: cfg.GUISection{Scale: 2}}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedScale() != 2 {
		t.Errorf("expected project scale to win, got %d", merged.GUI.ResolvedScale())
	}
}

func TestMerge_GUISectionGlobalScaleKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{GUI: cfg.GUISection{Scale: 3}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedScale() != 3 {
		t.Errorf("expected global scale preserved, got %d", merged.GUI.ResolvedScale())
	}
}

func TestMerge_GUISectionLegacyCellGUIMigrates(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{GUI: boolPtr(false)}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.GUI.ResolvedEnabled() {
		t.Error("expected legacy [cell] gui=false to migrate to GUI.Enabled=false in merge")
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

// --- [docker] section ---

func TestLoadFile_DockerSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[docker]
privileged = true
cap_add = ["SYS_ADMIN", "NET_ADMIN"]
mem_limit = "8g"
cpu_limit = "4"
shm_size = "2g"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Docker.Privileged {
		t.Error("privileged: want true")
	}
	if len(c.Docker.CapAdd) != 2 || c.Docker.CapAdd[0] != "SYS_ADMIN" || c.Docker.CapAdd[1] != "NET_ADMIN" {
		t.Errorf("cap_add: want [SYS_ADMIN NET_ADMIN], got %v", c.Docker.CapAdd)
	}
	if c.Docker.MemLimit != "8g" {
		t.Errorf("mem_limit: want 8g, got %q", c.Docker.MemLimit)
	}
	if c.Docker.CPULimit != "4" {
		t.Errorf("cpu_limit: want 4, got %q", c.Docker.CPULimit)
	}
	if c.Docker.ShmSize != "2g" {
		t.Errorf("shm_size: want 2g, got %q", c.Docker.ShmSize)
	}
}

func TestDockerSection_ResolvedMemLimit_Default(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_MEM_LIMIT", "")
	got := cfg.DockerSection{}.ResolvedMemLimit()
	if got != "4g" {
		t.Errorf("want default 4g, got %q", got)
	}
}

func TestDockerSection_ResolvedMemLimit_TOML(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_MEM_LIMIT", "")
	got := cfg.DockerSection{MemLimit: "16g"}.ResolvedMemLimit()
	if got != "16g" {
		t.Errorf("want 16g from toml, got %q", got)
	}
}

func TestDockerSection_ResolvedMemLimit_EnvWins(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_MEM_LIMIT", "32g")
	got := cfg.DockerSection{MemLimit: "16g"}.ResolvedMemLimit()
	if got != "32g" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestDockerSection_ResolvedMemLimit_ZeroUncaps(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_MEM_LIMIT", "")
	got := cfg.DockerSection{MemLimit: "0"}.ResolvedMemLimit()
	if got != "0" {
		t.Errorf("want 0 (uncapped), got %q", got)
	}
}

func TestDockerSection_ResolvedCPULimit_Default(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_CPU_LIMIT", "")
	got := cfg.DockerSection{}.ResolvedCPULimit()
	if got != "2" {
		t.Errorf("want default 2, got %q", got)
	}
}

func TestDockerSection_ResolvedCPULimit_TOML(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_CPU_LIMIT", "")
	got := cfg.DockerSection{CPULimit: "8"}.ResolvedCPULimit()
	if got != "8" {
		t.Errorf("want 8 from toml, got %q", got)
	}
}

func TestDockerSection_ResolvedCPULimit_EnvWins(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_CPU_LIMIT", "16")
	got := cfg.DockerSection{CPULimit: "8"}.ResolvedCPULimit()
	if got != "16" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestDockerSection_ResolvedShmSize_Default(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_SHM_SIZE", "")
	got := cfg.DockerSection{}.ResolvedShmSize()
	if got != "1g" {
		t.Errorf("want default 1g, got %q", got)
	}
}

func TestDockerSection_ResolvedShmSize_TOML(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_SHM_SIZE", "")
	got := cfg.DockerSection{ShmSize: "4g"}.ResolvedShmSize()
	if got != "4g" {
		t.Errorf("want 4g from toml, got %q", got)
	}
}

func TestDockerSection_ResolvedShmSize_EnvWins(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_SHM_SIZE", "8g")
	got := cfg.DockerSection{ShmSize: "4g"}.ResolvedShmSize()
	if got != "8g" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestMerge_DockerProjectWins(t *testing.T) {
	global := cfg.CellConfig{Docker: cfg.DockerSection{MemLimit: "4g", CPULimit: "2", ShmSize: "1g", CapAdd: []string{"SYS_ADMIN"}}}
	project := cfg.CellConfig{Docker: cfg.DockerSection{Privileged: true, CapAdd: []string{"NET_ADMIN"}, MemLimit: "16g", CPULimit: "8"}}
	merged := cfg.Merge(global, project)
	if !merged.Docker.Privileged {
		t.Error("privileged: project true should win")
	}
	if len(merged.Docker.CapAdd) != 2 {
		t.Errorf("cap_add: want union [SYS_ADMIN NET_ADMIN], got %v", merged.Docker.CapAdd)
	}
	if merged.Docker.MemLimit != "16g" {
		t.Errorf("mem_limit: project should win, got %q", merged.Docker.MemLimit)
	}
	if merged.Docker.CPULimit != "8" {
		t.Errorf("cpu_limit: project should win, got %q", merged.Docker.CPULimit)
	}
	if merged.Docker.ShmSize != "1g" {
		t.Errorf("shm_size: global should be kept when project empty, got %q", merged.Docker.ShmSize)
	}
}

func TestMerge_DockerGlobalKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Docker: cfg.DockerSection{CapAdd: []string{"SYS_ADMIN"}, MemLimit: "8g", CPULimit: "4", ShmSize: "2g"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Docker.Privileged {
		t.Error("privileged: should stay false when neither sets it")
	}
	if len(merged.Docker.CapAdd) != 1 || merged.Docker.CapAdd[0] != "SYS_ADMIN" {
		t.Errorf("cap_add: global should be preserved, got %v", merged.Docker.CapAdd)
	}
	if merged.Docker.MemLimit != "8g" {
		t.Errorf("mem_limit: global should be preserved, got %q", merged.Docker.MemLimit)
	}
	if merged.Docker.CPULimit != "4" {
		t.Errorf("cpu_limit: global should be preserved, got %q", merged.Docker.CPULimit)
	}
	if merged.Docker.ShmSize != "2g" {
		t.Errorf("shm_size: global should be preserved, got %q", merged.Docker.ShmSize)
	}
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
	// Sorted at load (CELL-331), not TOML order.
	if c.Cell.Modules[0] != "desktop" || c.Cell.Modules[1] != "electronics" {
		t.Errorf("modules: want [desktop electronics], got %v", c.Cell.Modules)
	}
}

// CELL-331: [a,b] and [b,a] must not produce different image tags or
// home-manager closures. Modules are sorted at load so every consumer
// (tag derivation, modules CSV, flake args) sees one canonical order.
func TestLoadFile_ModulesSorted(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
modules = ["node", "electronics", "desktop"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"desktop", "electronics", "node"}
	if len(c.Cell.Modules) != len(want) {
		t.Fatalf("want %d modules, got %d", len(want), len(c.Cell.Modules))
	}
	for i, m := range want {
		if c.Cell.Modules[i] != m {
			t.Fatalf("modules must be sorted: want %v, got %v", want, c.Cell.Modules)
		}
	}
}

func TestLoadLayered_ModulesSortedAfterMerge(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
modules = ["scraping"]
`)
	writeTOML(t, dir, ".devcell.toml", `
[cell]
modules = ["desktop"]
`)
	c, err := cfg.LoadLayered(
		filepath.Join(dir, "devcell.toml"),
		filepath.Join(dir, ".devcell.toml"),
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"desktop", "scraping"}
	if len(c.Cell.Modules) != len(want) {
		t.Fatalf("want %v, got %v", want, c.Cell.Modules)
	}
	for i, m := range want {
		if c.Cell.Modules[i] != m {
			t.Fatalf("merged modules must be sorted: want %v, got %v", want, c.Cell.Modules)
		}
	}
}

// CELL-391: [cell] stale_warning = false silences the "cell is behind —
// parallel reality" nudge at start. Default (absent) is enabled.
func TestCellSection_StaleWarningDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cell.StaleWarningEnabled() {
		t.Error("stale warning must default to enabled")
	}
}

func TestCellSection_StaleWarningFalseDisables(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
stale_warning = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.StaleWarningEnabled() {
		t.Error("stale_warning = false must disable the nudge")
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

// --- StackExplicit (CELL-43) ---

func TestCellSection_StackExplicit_FalseWhenUnset(t *testing.T) {
	c := cfg.CellSection{}
	if c.StackExplicit() {
		t.Error("empty Stack must report not explicit")
	}
}

func TestCellSection_StackExplicit_TrueWhenSet(t *testing.T) {
	c := cfg.CellSection{Stack: "ultimate"}
	if !c.StackExplicit() {
		t.Error("non-empty Stack must report explicit")
	}
}

// --- DescribeModulesSource (CELL-48) ---

func TestDescribeModulesSource_Default(t *testing.T) {
	c := cfg.CellSection{}
	got := c.DescribeModulesSource()
	want := "default (base stack, no extra modules)"
	if got != want {
		t.Errorf("default: want %q, got %q", want, got)
	}
}

func TestDescribeModulesSource_StackOnly(t *testing.T) {
	c := cfg.CellSection{Stack: "go"}
	got := c.DescribeModulesSource()
	want := "stack=go"
	if got != want {
		t.Errorf("stack-only: want %q, got %q", want, got)
	}
}

func TestDescribeModulesSource_ModulesOnly(t *testing.T) {
	c := cfg.CellSection{Modules: []string{"a", "b"}}
	got := c.DescribeModulesSource()
	want := "modules=[a,b]"
	if got != want {
		t.Errorf("modules-only: want %q, got %q", want, got)
	}
}

func TestDescribeModulesSource_Merged(t *testing.T) {
	c := cfg.CellSection{Stack: "go", Modules: []string{"a", "b"}}
	got := c.DescribeModulesSource()
	want := "stack=go + modules=[a,b] (merged)"
	if got != want {
		t.Errorf("merged: want %q, got %q", want, got)
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

// Modules merge: UNION with dedup, global order preserved.
// Project's explicit empty list ([]) clears global as escape hatch.
// See CELL-67 for rationale.

func TestMerge_ModulesProjectUnionsWithGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a"}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"b", "c"}}}
	merged := cfg.Merge(global, project)
	got := merged.Cell.Modules
	want := []string{"a", "b", "c"}
	if !equalStrings(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestMerge_ModulesGlobalKeptWhenProjectNil(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a"}}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if !equalStrings(merged.Cell.Modules, []string{"a"}) {
		t.Errorf("want [a], got %v", merged.Cell.Modules)
	}
}

func TestMerge_ModulesProjectOnlyWhenGlobalNil(t *testing.T) {
	global := cfg.CellConfig{}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"x", "y"}}}
	merged := cfg.Merge(global, project)
	if !equalStrings(merged.Cell.Modules, []string{"x", "y"}) {
		t.Errorf("want [x y], got %v", merged.Cell.Modules)
	}
}

func TestMerge_ModulesBothNil(t *testing.T) {
	global := cfg.CellConfig{}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if len(merged.Cell.Modules) != 0 {
		t.Errorf("want empty, got %v", merged.Cell.Modules)
	}
}

func TestMerge_ModulesDedupPreservesGlobalOrder(t *testing.T) {
	// Project re-lists items already in global → dedup, but global order wins.
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"kicad", "yahoo-finance"}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"yahoo-finance", "kicad"}}}
	merged := cfg.Merge(global, project)
	want := []string{"kicad", "yahoo-finance"}
	if !equalStrings(merged.Cell.Modules, want) {
		t.Errorf("want %v (global order preserved), got %v", want, merged.Cell.Modules)
	}
}

func TestMerge_ModulesDedupWithOverlapAppendsNewItems(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a", "b"}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"b", "c"}}}
	merged := cfg.Merge(global, project)
	want := []string{"a", "b", "c"}
	if !equalStrings(merged.Cell.Modules, want) {
		t.Errorf("want %v, got %v", want, merged.Cell.Modules)
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

func TestMerge_ModulesGlobalEmptyProjectHas(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Modules: []string{"a"}}}
	merged := cfg.Merge(global, project)
	if !equalStrings(merged.Cell.Modules, []string{"a"}) {
		t.Errorf("want [a], got %v", merged.Cell.Modules)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	c, err := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Stack != "go" {
		t.Errorf("stack: want go, got %q", c.Cell.Stack)
	}
	// Modules UNION (CELL-67): global [desktop] + project [electronics] → [desktop, electronics]
	if !equalStrings(c.Cell.Modules, []string{"desktop", "electronics"}) {
		t.Errorf("modules: want [desktop electronics], got %v", c.Cell.Modules)
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
	// CELL-292: `core` prepended as the smallest first-class stack (just
	// home-manager + one tiny package). Modules 2.0 (CELL-63): `dev`
	// between base and the legacy stacks.
	want := []string{"core", "base", "dev", "go", "node", "python", "fullstack", "electronics", "ultimate"}
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

func TestLoadFile_PortsPublishIP(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[ports]
publish_ip = "0.0.0.0"
forward = ["3000"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Ports.PublishIP != "0.0.0.0" {
		t.Errorf("publish_ip: want %q, got %q", "0.0.0.0", c.Ports.PublishIP)
	}
}

func TestLoadFile_PortsPublishIPDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Ports.PublishIP != "" {
		t.Errorf("publish_ip should default empty, got %q", c.Ports.PublishIP)
	}
}

func TestMerge_PortsPublishIP_ProjectWins(t *testing.T) {
	global := cfg.CellConfig{Ports: cfg.PortsSection{PublishIP: "127.0.0.1"}}
	project := cfg.CellConfig{Ports: cfg.PortsSection{PublishIP: "0.0.0.0"}}
	merged := cfg.Merge(global, project)
	if merged.Ports.PublishIP != "0.0.0.0" {
		t.Errorf("project publish_ip should win: want 0.0.0.0, got %q", merged.Ports.PublishIP)
	}
}

func TestMerge_PortsPublishIP_GlobalKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Ports: cfg.PortsSection{PublishIP: "127.0.0.1"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Ports.PublishIP != "127.0.0.1" {
		t.Errorf("global publish_ip should be retained when project empty, got %q", merged.Ports.PublishIP)
	}
}

func TestResolvedPublishIP(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to all-interfaces", "", "0.0.0.0"},
		{"explicit 0.0.0.0 passes through", "0.0.0.0", "0.0.0.0"},
		{"loopback override", "127.0.0.1", "127.0.0.1"},
		{"specific NIC override", "192.168.1.50", "192.168.1.50"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.PortsSection{PublishIP: tc.in}.ResolvedPublishIP()
			if got != tc.want {
				t.Errorf("ResolvedPublishIP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- Hostname resolver ---

func TestResolvedHostname_DefaultsToComputed(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "")
	got := cfg.CellSection{}.ResolvedHostname("cell-myapp-0")
	if got != "cell-myapp-0" {
		t.Errorf("want computed default, got %q", got)
	}
}

func TestResolvedHostname_TOMLOverridesComputed(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "")
	got := cfg.CellSection{Hostname: "from-toml"}.ResolvedHostname("cell-myapp-0")
	if got != "from-toml" {
		t.Errorf("toml value should win over computed, got %q", got)
	}
}

func TestResolvedHostname_EnvOverridesTOML(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "from-env")
	got := cfg.CellSection{Hostname: "from-toml"}.ResolvedHostname("cell-myapp-0")
	if got != "from-env" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestLoadFile_HostnameTOMLKey(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
hostname = "custom-host"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Hostname != "custom-host" {
		t.Errorf("want custom-host, got %q", c.Cell.Hostname)
	}
}

// Project [cell] hostname must survive Merge so that LoadLayered ->
// LoadFromOS exposes the value to runner.BuildArgv. Previously Hostname
// was loaded by LoadFile but dropped by Merge, so cell shell silently
// used the computed default.
func TestMerge_HostnameProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Hostname: "from-global"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Hostname: "from-project"}}
	got := cfg.Merge(global, project)
	if got.Cell.Hostname != "from-project" {
		t.Errorf("project hostname must override global; got %q", got.Cell.Hostname)
	}
}

func TestMerge_MacAddressProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{MacAddress: "aa:aa:aa:aa:aa:aa"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{MacAddress: "e2:2d:42:13:81:d2"}}
	got := cfg.Merge(global, project)
	if got.Cell.MacAddress != "e2:2d:42:13:81:d2" {
		t.Errorf("project mac_address must override global; got %q", got.Cell.MacAddress)
	}
}

func TestMerge_MacAddressInheritsGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{MacAddress: "aa:aa:aa:aa:aa:aa"}}
	project := cfg.CellConfig{}
	got := cfg.Merge(global, project)
	if got.Cell.MacAddress != "aa:aa:aa:aa:aa:aa" {
		t.Errorf("global mac_address must survive when project leaves it empty; got %q", got.Cell.MacAddress)
	}
}

func TestMerge_HostnameFromProjectOnly(t *testing.T) {
	global := cfg.CellConfig{}
	project := cfg.CellConfig{Cell: cfg.CellSection{Hostname: "from-project"}}
	got := cfg.Merge(global, project)
	if got.Cell.Hostname != "from-project" {
		t.Errorf("project hostname must propagate when global is empty; got %q", got.Cell.Hostname)
	}
}

func TestMerge_HostnameInheritsGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Hostname: "from-global"}}
	project := cfg.CellConfig{}
	got := cfg.Merge(global, project)
	if got.Cell.Hostname != "from-global" {
		t.Errorf("global hostname must survive when project leaves it empty; got %q", got.Cell.Hostname)
	}
}

// --- DefaultCommand ---

func TestLoadFile_DefaultCommand(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
default_command = "claude"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.DefaultCommand != "claude" {
		t.Errorf("want claude, got %q", c.Cell.DefaultCommand)
	}
}

func TestResolvedDefaultCommand_Empty(t *testing.T) {
	t.Setenv("DEVCELL_DEFAULT_COMMAND", "")
	got := cfg.CellSection{}.ResolvedDefaultCommand()
	if got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestResolvedDefaultCommand_TOML(t *testing.T) {
	t.Setenv("DEVCELL_DEFAULT_COMMAND", "")
	got := cfg.CellSection{DefaultCommand: "shell"}.ResolvedDefaultCommand()
	if got != "shell" {
		t.Errorf("want shell, got %q", got)
	}
}

func TestResolvedDefaultCommand_EnvOverridesTOML(t *testing.T) {
	t.Setenv("DEVCELL_DEFAULT_COMMAND", "codex")
	got := cfg.CellSection{DefaultCommand: "shell"}.ResolvedDefaultCommand()
	if got != "codex" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestMerge_DefaultCommandProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{DefaultCommand: "shell"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{DefaultCommand: "claude"}}
	got := cfg.Merge(global, project)
	if got.Cell.DefaultCommand != "claude" {
		t.Errorf("project default_command must override global; got %q", got.Cell.DefaultCommand)
	}
}

func TestMerge_DefaultCommandInheritsGlobal(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{DefaultCommand: "shell"}}
	project := cfg.CellConfig{}
	got := cfg.Merge(global, project)
	if got.Cell.DefaultCommand != "shell" {
		t.Errorf("global default_command must survive when project leaves it empty; got %q", got.Cell.DefaultCommand)
	}
}

func TestApplyEnv_DefaultCommand(t *testing.T) {
	c := cfg.CellConfig{Cell: cfg.CellSection{DefaultCommand: "shell"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "DEVCELL_DEFAULT_COMMAND" {
			return "claude"
		}
		return ""
	})
	if c.Cell.DefaultCommand != "claude" {
		t.Errorf("ApplyEnv should override default_command; got %q", c.Cell.DefaultCommand)
	}
}

func TestValidateDefaultCommand_Valid(t *testing.T) {
	for _, cmd := range cfg.KnownDefaultCommands() {
		if err := cfg.ValidateDefaultCommand(cmd); err != nil {
			t.Errorf("valid command %q should not error: %v", cmd, err)
		}
	}
}

func TestValidateDefaultCommand_Empty(t *testing.T) {
	if err := cfg.ValidateDefaultCommand(""); err != nil {
		t.Errorf("empty should be valid: %v", err)
	}
}

func TestValidateDefaultCommand_Invalid(t *testing.T) {
	err := cfg.ValidateDefaultCommand("notacommand")
	if err == nil {
		t.Fatal("expected error for invalid command")
	}
	if !strings.Contains(err.Error(), "notacommand") {
		t.Errorf("error should mention the invalid command: %v", err)
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

// --- [stealth] section ---

func TestLoadFile_StealthSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[stealth]
arch = "arm"
platform = "Linux"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stealth.Arch != "arm" {
		t.Errorf("stealth.arch: want arm, got %q", c.Stealth.Arch)
	}
	if c.Stealth.Platform != "Linux" {
		t.Errorf("stealth.platform: want Linux, got %q", c.Stealth.Platform)
	}
}

func TestStealthSection_ResolvedArch_DefaultFromRuntime(t *testing.T) {
	s := cfg.StealthSection{}
	arch := s.ResolvedArch()
	// Default must detect from runtime — on arm64 host → "arm", on amd64 → "x86"
	if arch != "arm" && arch != "x86" {
		t.Errorf("ResolvedArch() default should be arm or x86, got %q", arch)
	}
}

func TestStealthSection_ResolvedArch_ExplicitOverride(t *testing.T) {
	s := cfg.StealthSection{Arch: "x86"}
	if got := s.ResolvedArch(); got != "x86" {
		t.Errorf("ResolvedArch() with explicit x86: want x86, got %q", got)
	}
}

func TestStealthSection_ResolvedPlatform_Default(t *testing.T) {
	s := cfg.StealthSection{}
	if got := s.ResolvedPlatform(); got != "Linux" {
		t.Errorf("ResolvedPlatform() default: want Linux, got %q", got)
	}
}

func TestStealthSection_ResolvedPlatform_ExplicitOverride(t *testing.T) {
	s := cfg.StealthSection{Platform: "macOS"}
	if got := s.ResolvedPlatform(); got != "macOS" {
		t.Errorf("ResolvedPlatform() explicit: want macOS, got %q", got)
	}
}

func TestStealthSection_ResolvedUserAgent_ContainsArch(t *testing.T) {
	s := cfg.StealthSection{}
	ua := s.ResolvedUserAgent()
	if ua == "" {
		t.Fatal("ResolvedUserAgent() should return a non-empty default Chrome UA string")
	}
	// Default on arm64 host: UA should contain the platform indicator
	if !strings.Contains(ua, "Chrome/") {
		t.Errorf("ResolvedUserAgent() should contain Chrome/ version, got %q", ua)
	}
}

func TestMerge_StealthProjectWins(t *testing.T) {
	global := cfg.CellConfig{Stealth: cfg.StealthSection{Arch: "x86", Platform: "Linux"}}
	project := cfg.CellConfig{Stealth: cfg.StealthSection{Arch: "arm", Platform: "macOS"}}
	merged := cfg.Merge(global, project)
	if merged.Stealth.Arch != "arm" {
		t.Errorf("stealth.arch: project should win, got %q", merged.Stealth.Arch)
	}
	if merged.Stealth.Platform != "macOS" {
		t.Errorf("stealth.platform: project should win, got %q", merged.Stealth.Platform)
	}
}

// --- [nix] section ---

func TestLoadFile_NixSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[nix]
image = "nixos/nix:2.35.0"
nixhome = "~/dev/nixhome"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Nix.Image != "nixos/nix:2.35.0" {
		t.Errorf("nix.image: want nixos/nix:2.35.0, got %q", c.Nix.Image)
	}
	if c.Nix.NixhomePath != "~/dev/nixhome" {
		t.Errorf("nix.nixhome: want ~/dev/nixhome, got %q", c.Nix.NixhomePath)
	}
}

func TestLoadFile_NixSectionDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Nix.Image != "" {
		t.Errorf("expected empty nix.image when not set, got %q", c.Nix.Image)
	}
	if c.Nix.NixhomePath != "" {
		t.Errorf("expected empty nix.nixhome when not set, got %q", c.Nix.NixhomePath)
	}
}

func TestNixSection_ResolvedImage_Default(t *testing.T) {
	n := cfg.NixSection{}
	got := n.ResolvedImage()
	if got != cfg.DefaultNixImage {
		t.Errorf("want %q, got %q", cfg.DefaultNixImage, got)
	}
}

func TestNixSection_ResolvedImage_Explicit(t *testing.T) {
	n := cfg.NixSection{Image: "nixos/nix:2.35.0"}
	got := n.ResolvedImage()
	if got != "nixos/nix:2.35.0" {
		t.Errorf("want nixos/nix:2.35.0, got %q", got)
	}
}

func TestNixSection_ResolvedImage_EnvOverride(t *testing.T) {
	t.Setenv("DEVCELL_NIX_IMAGE", "nixos/nix:2.36.0")
	n := cfg.NixSection{Image: "nixos/nix:2.35.0"}
	got := n.ResolvedImage()
	if got != "nixos/nix:2.36.0" {
		t.Errorf("env should win over toml, got %q", got)
	}
}

func TestMerge_NixProjectWins(t *testing.T) {
	global := cfg.CellConfig{Nix: cfg.NixSection{Image: "nixos/nix:2.34.7", NixhomePath: "/global/nixhome"}}
	project := cfg.CellConfig{Nix: cfg.NixSection{Image: "nixos/nix:2.35.0", NixhomePath: "/project/nixhome"}}
	merged := cfg.Merge(global, project)
	if merged.Nix.Image != "nixos/nix:2.35.0" {
		t.Errorf("project nix.image should win, got %q", merged.Nix.Image)
	}
	if merged.Nix.NixhomePath != "/project/nixhome" {
		t.Errorf("project nix.nixhome should win, got %q", merged.Nix.NixhomePath)
	}
}

func TestMerge_NixGlobalKeptWhenProjectEmpty(t *testing.T) {
	global := cfg.CellConfig{Nix: cfg.NixSection{Image: "nixos/nix:2.34.7", NixhomePath: "/global/nixhome"}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Nix.Image != "nixos/nix:2.34.7" {
		t.Errorf("global nix.image should be preserved, got %q", merged.Nix.Image)
	}
	if merged.Nix.NixhomePath != "/global/nixhome" {
		t.Errorf("global nix.nixhome should be preserved, got %q", merged.Nix.NixhomePath)
	}
}

func TestApplyEnv_NixImageOverride(t *testing.T) {
	c := cfg.CellConfig{Nix: cfg.NixSection{Image: "nixos/nix:2.34.7"}}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "DEVCELL_NIX_IMAGE" {
			return "nixos/nix:2.36.0"
		}
		return ""
	})
	if c.Nix.Image != "nixos/nix:2.36.0" {
		t.Errorf("env should override toml: want nixos/nix:2.36.0, got %q", c.Nix.Image)
	}
}

func TestApplyEnv_NixhomePathOnNixSection(t *testing.T) {
	c := cfg.CellConfig{}
	cfg.ApplyEnv(&c, func(k string) string {
		if k == "DEVCELL_NIXHOME_PATH" {
			return "/env/nixhome"
		}
		return ""
	})
	if c.Nix.NixhomePath != "/env/nixhome" {
		t.Errorf("DEVCELL_NIXHOME_PATH should set Nix.NixhomePath, got %q", c.Nix.NixhomePath)
	}
}

func TestLoadFile_NixhomeFromNixSection(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, "test.toml", `
[nix]
nixhome = "~/dev/nixhome"
`)
	c, err := cfg.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Nix.NixhomePath != "~/dev/nixhome" {
		t.Errorf("want ~/dev/nixhome, got %q", c.Nix.NixhomePath)
	}
}

// --- TOML parse error surfacing ---

// When the project .devcell.toml has an invalid TOML escape (e.g. \~ in a
// volume mount path copied from a shell), LoadLayered must return an error
// instead of silently discarding the entire project config. Without this,
// [cell] stack = "ultimate" is silently dropped and the build falls back to
// "base" — the user gets the wrong image with no warning.
func TestLoadLayered_ProjectParseError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeTOML(t, dir, "global.toml", `[cell]`)
	// \~ is an invalid TOML escape inside a basic (double-quoted) string
	projectPath := writeTOML(t, dir, "project.toml", `[cell]
stack = "ultimate"

[[volumes]]
mount = "/Users/dmitry/Library/Mobile\ Documents/com\~apple\~CloudDocs/foo:/bar"
`)
	_, err := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if err == nil {
		t.Fatal("LoadLayered must return an error when project TOML has a parse error; got nil")
	}
	if !strings.Contains(err.Error(), "project.toml") && !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention the file or parse failure: %v", err)
	}
}

// Missing project file is NOT an error — it's the normal case for projects
// without a .devcell.toml.
func TestLoadLayered_MissingProjectFile_NoError(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeTOML(t, dir, "global.toml", `[cell]
stack = "go"
`)
	_, err := cfg.LoadLayered(globalPath, dir+"/nonexistent.toml", func(string) string { return "" })
	if err != nil {
		t.Errorf("missing project file should not be an error: %v", err)
	}
}

// LoadFromOS must propagate project parse errors.
// --- Background field (CELL-314) ---

func TestResolvedBackground_DefaultFalse(t *testing.T) {
	t.Setenv("DEVCELL_BACKGROUND", "")
	c := cfg.CellSection{}
	if c.ResolvedBackground() {
		t.Error("ResolvedBackground() should default to false")
	}
}

func TestResolvedBackground_TOMLTrue(t *testing.T) {
	t.Setenv("DEVCELL_BACKGROUND", "")
	c := cfg.CellSection{Background: boolPtr(true)}
	if !c.ResolvedBackground() {
		t.Error("ResolvedBackground() should return true when TOML sets background=true")
	}
}

func TestResolvedBackground_TOMLFalse(t *testing.T) {
	t.Setenv("DEVCELL_BACKGROUND", "")
	c := cfg.CellSection{Background: boolPtr(false)}
	if c.ResolvedBackground() {
		t.Error("ResolvedBackground() should return false when TOML sets background=false")
	}
}

func TestResolvedBackground_EnvOverridesToTrue(t *testing.T) {
	t.Setenv("DEVCELL_BACKGROUND", "1")
	c := cfg.CellSection{Background: boolPtr(false)}
	if !c.ResolvedBackground() {
		t.Error("DEVCELL_BACKGROUND=1 should override TOML background=false")
	}
}

func TestResolvedBackground_EnvOverridesToFalse(t *testing.T) {
	t.Setenv("DEVCELL_BACKGROUND", "0")
	c := cfg.CellSection{Background: boolPtr(true)}
	if c.ResolvedBackground() {
		t.Error("DEVCELL_BACKGROUND=0 should override TOML background=true")
	}
}

func TestLoadFile_BackgroundTrue(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
background = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Background == nil || !*c.Cell.Background {
		t.Error("expected background=true after parsing")
	}
}

func TestLoadFile_BackgroundFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
background = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Background == nil || *c.Cell.Background {
		t.Error("expected background=false after parsing")
	}
}

func TestLoadFile_BackgroundDefaultNil(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.Background != nil {
		t.Error("expected Background=nil when not set in TOML")
	}
}

func TestMerge_BackgroundProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Background: boolPtr(false)}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Background: boolPtr(true)}}
	merged := cfg.Merge(global, project)
	if merged.Cell.Background == nil || !*merged.Cell.Background {
		t.Error("project background=true should override global false")
	}
}

func TestMerge_BackgroundGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Background: boolPtr(true)}}
	project := cfg.CellConfig{}
	merged := cfg.Merge(global, project)
	if merged.Cell.Background == nil || !*merged.Cell.Background {
		t.Error("global background=true should be preserved when project unset")
	}
}

// --- Tart SSH config fields ---

func TestResolvedTartSSHPort_Default22(t *testing.T) {
	c := cfg.CellSection{}
	if p := c.ResolvedTartSSHPort(); p != 22 {
		t.Errorf("expected default port 22, got %d", p)
	}
}

func TestResolvedTartSSHPort_TOML(t *testing.T) {
	c := cfg.CellSection{TartSSHPort: 2222}
	if p := c.ResolvedTartSSHPort(); p != 2222 {
		t.Errorf("expected port 2222, got %d", p)
	}
}

func TestResolvedTartSSHPort_EnvOverrides(t *testing.T) {
	t.Setenv("DEVCELL_TART_SSH_PORT", "3333")
	c := cfg.CellSection{TartSSHPort: 2222}
	if p := c.ResolvedTartSSHPort(); p != 3333 {
		t.Errorf("expected env port 3333, got %d", p)
	}
}

func TestResolvedTartSSHHost_Default(t *testing.T) {
	c := cfg.CellSection{}
	if h := c.ResolvedTartSSHHost(); h != "localhost" {
		t.Errorf("expected default host localhost, got %q", h)
	}
}

func TestResolvedTartSSHHost_TOML(t *testing.T) {
	c := cfg.CellSection{TartSSHHost: "192.168.64.2"}
	if h := c.ResolvedTartSSHHost(); h != "192.168.64.2" {
		t.Errorf("expected host 192.168.64.2, got %q", h)
	}
}

func TestResolvedTartSSHHost_EnvOverrides(t *testing.T) {
	t.Setenv("DEVCELL_TART_SSH_HOST", "10.0.0.5")
	c := cfg.CellSection{TartSSHHost: "192.168.64.2"}
	if h := c.ResolvedTartSSHHost(); h != "10.0.0.5" {
		t.Errorf("expected env host 10.0.0.5, got %q", h)
	}
}

func TestResolvedTartSSHUser_Default(t *testing.T) {
	c := cfg.CellSection{}
	if u := c.ResolvedTartSSHUser(); u != "admin" {
		t.Errorf("expected default user admin, got %q", u)
	}
}

func TestResolvedTartSSHUser_TOML(t *testing.T) {
	c := cfg.CellSection{TartSSHUser: "admin"}
	if u := c.ResolvedTartSSHUser(); u != "admin" {
		t.Errorf("expected user admin, got %q", u)
	}
}

func TestResolvedTartSSHKey_DefaultEmpty(t *testing.T) {
	c := cfg.CellSection{}
	if k := c.ResolvedTartSSHKey(); k != "" {
		t.Errorf("expected empty default key, got %q", k)
	}
}

func TestResolvedTartSSHKey_TOML(t *testing.T) {
	c := cfg.CellSection{TartSSHKey: "/path/to/key"}
	if k := c.ResolvedTartSSHKey(); k != "/path/to/key" {
		t.Errorf("expected key /path/to/key, got %q", k)
	}
}

func TestResolvedTartSSHKey_EnvOverrides(t *testing.T) {
	t.Setenv("DEVCELL_TART_SSH_KEY", "/env/key")
	c := cfg.CellSection{TartSSHKey: "/toml/key"}
	if k := c.ResolvedTartSSHKey(); k != "/env/key" {
		t.Errorf("expected env key /env/key, got %q", k)
	}
}

func TestLoadFile_TartSSH(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
tart_ssh_port = 2222
tart_ssh_host = "192.168.64.2"
tart_ssh_user = "admin"
tart_ssh_key = "/path/to/key"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.TartSSHPort != 2222 {
		t.Errorf("expected port 2222, got %d", c.Cell.TartSSHPort)
	}
	if c.Cell.TartSSHHost != "192.168.64.2" {
		t.Errorf("expected host 192.168.64.2, got %q", c.Cell.TartSSHHost)
	}
	if c.Cell.TartSSHUser != "admin" {
		t.Errorf("expected user admin, got %q", c.Cell.TartSSHUser)
	}
	if c.Cell.TartSSHKey != "/path/to/key" {
		t.Errorf("expected key /path/to/key, got %q", c.Cell.TartSSHKey)
	}
}

func TestMerge_TartSSHProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{TartSSHPort: 22, TartSSHHost: "global-host"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{TartSSHPort: 2222, TartSSHHost: "project-host"}}
	merged := cfg.Merge(global, project)
	if merged.Cell.TartSSHPort != 2222 {
		t.Errorf("expected project port 2222, got %d", merged.Cell.TartSSHPort)
	}
	if merged.Cell.TartSSHHost != "project-host" {
		t.Errorf("expected project host, got %q", merged.Cell.TartSSHHost)
	}
}

func TestResolvedTartOCIImage_Default(t *testing.T) {
	c := cfg.CellSection{}
	if got := c.ResolvedTartOCIImage(); got != cfg.DefaultTartOCIImage {
		t.Errorf("expected default %q, got %q", cfg.DefaultTartOCIImage, got)
	}
}

func TestResolvedTartOCIImage_TOML(t *testing.T) {
	c := cfg.CellSection{TartOCIImage: "ghcr.io/custom/image:v1"}
	if got := c.ResolvedTartOCIImage(); got != "ghcr.io/custom/image:v1" {
		t.Errorf("expected TOML value, got %q", got)
	}
}

func TestResolvedTartOCIImage_EnvOverrides(t *testing.T) {
	t.Setenv("DEVCELL_TART_OCI_IMAGE", "ghcr.io/env/image:v2")
	c := cfg.CellSection{TartOCIImage: "ghcr.io/toml/image:v1"}
	if got := c.ResolvedTartOCIImage(); got != "ghcr.io/env/image:v2" {
		t.Errorf("expected env override, got %q", got)
	}
}

func TestMerge_TartOCIImageProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{TartOCIImage: "ghcr.io/global:v1"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{TartOCIImage: "ghcr.io/project:v2"}}
	merged := cfg.Merge(global, project)
	if merged.Cell.TartOCIImage != "ghcr.io/project:v2" {
		t.Errorf("expected project OCI image, got %q", merged.Cell.TartOCIImage)
	}
}

func TestLoadFromOS_ProjectParseError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Write a valid global config
	globalDir := t.TempDir()
	writeTOML(t, globalDir, "devcell.toml", `[cell]`)
	// Write a broken project .devcell.toml
	writeTOML(t, dir, ".devcell.toml", `[cell]
stack = "ultimate"

[[volumes]]
mount = "/path/with\~invalid/escape:/bar"
`)
	_, err := cfg.LoadFromOSWithDirs(globalDir, dir)
	if err == nil {
		t.Fatal("LoadFromOSWithDirs must return an error when project TOML has a parse error")
	}
}

// The append surface is separate from system_prompt: after CELL-408,
// system_prompt replaces Claude Code's built-in prompt, so a distinct key is
// needed for text that layers on top of whichever base is in effect.
func TestLoadFile_LLMAppendSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devcell.toml")
	if err := os.WriteFile(path, []byte(`
[llm]
append_system_prompt = "always run gofmt"
append_system_prompt_file = "prompts/extra.md"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := cfg.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.LLM.AppendSystemPrompt != "always run gofmt" {
		t.Errorf("append_system_prompt = %q", c.LLM.AppendSystemPrompt)
	}
	if c.LLM.AppendSystemPromptFile != "prompts/extra.md" {
		t.Errorf("append_system_prompt_file = %q", c.LLM.AppendSystemPromptFile)
	}
}

// Merge is hand-written per LLM field, so a new key silently ignores the
// project value unless an explicit override line is added.
func TestMerge_LLMAppendSystemPromptProjectOverridesGlobal(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{
		AppendSystemPrompt:     "global append",
		AppendSystemPromptFile: "global.md",
	}}
	project := cfg.CellConfig{LLM: cfg.LLMSection{
		AppendSystemPrompt:     "project append",
		AppendSystemPromptFile: "project.md",
	}}

	out := cfg.Merge(global, project)

	if out.LLM.AppendSystemPrompt != "project append" {
		t.Errorf("append_system_prompt = %q, want project value", out.LLM.AppendSystemPrompt)
	}
	if out.LLM.AppendSystemPromptFile != "project.md" {
		t.Errorf("append_system_prompt_file = %q, want project value", out.LLM.AppendSystemPromptFile)
	}
}

// An unset project value must not blank out the global one.
func TestMerge_LLMAppendSystemPromptGlobalSurvivesEmptyProject(t *testing.T) {
	global := cfg.CellConfig{LLM: cfg.LLMSection{AppendSystemPrompt: "global append"}}

	out := cfg.Merge(global, cfg.CellConfig{})

	if out.LLM.AppendSystemPrompt != "global append" {
		t.Errorf("append_system_prompt = %q, want global value preserved", out.LLM.AppendSystemPrompt)
	}
}

// ── CELL-446: Packages merge ────────────────────────────────────────────────

func TestMerge_PackagesNpmAccumulates(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Npm: map[string]string{"prettier": "*", "eslint": "8"},
	}}
	project := cfg.CellConfig{Packages: cfg.PackagesSection{
		Npm: map[string]string{"eslint": "9", "typescript": "*"},
	}}
	merged := cfg.Merge(global, project)
	if merged.Packages.Npm["prettier"] != "*" {
		t.Errorf("prettier should be *, got %q", merged.Packages.Npm["prettier"])
	}
	if merged.Packages.Npm["eslint"] != "9" {
		t.Errorf("eslint: project should win, got %q", merged.Packages.Npm["eslint"])
	}
	if merged.Packages.Npm["typescript"] != "*" {
		t.Errorf("typescript should be *, got %q", merged.Packages.Npm["typescript"])
	}
}

func TestMerge_PackagesPythonAccumulates(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Python: map[string]string{"pre-commit": "*"},
	}}
	project := cfg.CellConfig{Packages: cfg.PackagesSection{
		Python: map[string]string{"black": "*"},
	}}
	merged := cfg.Merge(global, project)
	if merged.Packages.Python["pre-commit"] != "*" {
		t.Errorf("pre-commit should be *, got %q", merged.Packages.Python["pre-commit"])
	}
	if merged.Packages.Python["black"] != "*" {
		t.Errorf("black should be *, got %q", merged.Packages.Python["black"])
	}
}

func TestMerge_PackagesGlobalSurvivesEmptyProject(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Npm: map[string]string{"prettier": "*"},
	}}
	merged := cfg.Merge(global, cfg.CellConfig{})
	if merged.Packages.Npm["prettier"] != "*" {
		t.Errorf("global npm packages should survive empty project, got %q", merged.Packages.Npm["prettier"])
	}
}

// ── CELL-445: NixPackages parsing and merge ─────────────────────────────────

func TestLoadFile_NixPackages(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[packages.nix]
stable = ["tmux", "htop"]
unstable = ["some-tool"]
edge = ["bleeding-edge"]
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wantStable := []string{"tmux", "htop"}
	if len(c.Packages.Nix.Stable) != 2 || c.Packages.Nix.Stable[0] != wantStable[0] || c.Packages.Nix.Stable[1] != wantStable[1] {
		t.Errorf("stable = %v, want %v", c.Packages.Nix.Stable, wantStable)
	}
	if len(c.Packages.Nix.Unstable) != 1 || c.Packages.Nix.Unstable[0] != "some-tool" {
		t.Errorf("unstable = %v, want [some-tool]", c.Packages.Nix.Unstable)
	}
	if len(c.Packages.Nix.Edge) != 1 || c.Packages.Nix.Edge[0] != "bleeding-edge" {
		t.Errorf("edge = %v, want [bleeding-edge]", c.Packages.Nix.Edge)
	}
}

func TestLoadFile_NixPackagesEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[packages.nix]
stable = []
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Packages.Nix.Stable == nil || len(c.Packages.Nix.Stable) != 0 {
		t.Errorf("stable should be empty non-nil slice, got %v (nil=%v)", c.Packages.Nix.Stable, c.Packages.Nix.Stable == nil)
	}
}

func TestMerge_NixPackagesUnionDedup(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Nix: cfg.NixPackages{
			Stable:   []string{"tmux", "htop"},
			Unstable: []string{"tool-a"},
		},
	}}
	project := cfg.CellConfig{Packages: cfg.PackagesSection{
		Nix: cfg.NixPackages{
			Stable:   []string{"htop", "cowsay"},
			Unstable: []string{"tool-b"},
			Edge:     []string{"edge-pkg"},
		},
	}}
	merged := cfg.Merge(global, project)
	wantStable := []string{"cowsay", "htop", "tmux"}
	if strings.Join(merged.Packages.Nix.Stable, ",") != strings.Join(wantStable, ",") {
		t.Errorf("stable = %v, want %v (union, deduped, sorted)", merged.Packages.Nix.Stable, wantStable)
	}
	wantUnstable := []string{"tool-a", "tool-b"}
	if strings.Join(merged.Packages.Nix.Unstable, ",") != strings.Join(wantUnstable, ",") {
		t.Errorf("unstable = %v, want %v", merged.Packages.Nix.Unstable, wantUnstable)
	}
	wantEdge := []string{"edge-pkg"}
	if strings.Join(merged.Packages.Nix.Edge, ",") != strings.Join(wantEdge, ",") {
		t.Errorf("edge = %v, want %v", merged.Packages.Nix.Edge, wantEdge)
	}
}

func TestMerge_NixPackagesEscapeHatch(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Nix: cfg.NixPackages{Stable: []string{"tmux", "htop"}},
	}}
	project := cfg.CellConfig{Packages: cfg.PackagesSection{
		Nix: cfg.NixPackages{Stable: []string{}},
	}}
	merged := cfg.Merge(global, project)
	if len(merged.Packages.Nix.Stable) != 0 {
		t.Errorf("explicit empty stable in project should clear global, got %v", merged.Packages.Nix.Stable)
	}
}

func TestMerge_NixPackagesGlobalSurvivesNilProject(t *testing.T) {
	global := cfg.CellConfig{Packages: cfg.PackagesSection{
		Nix: cfg.NixPackages{Stable: []string{"tmux"}},
	}}
	merged := cfg.Merge(global, cfg.CellConfig{})
	if len(merged.Packages.Nix.Stable) != 1 || merged.Packages.Nix.Stable[0] != "tmux" {
		t.Errorf("global nix stable should survive nil project, got %v", merged.Packages.Nix.Stable)
	}
}

// --- Wireguard ---

func TestLoadFile_WireguardSection(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[[wireguard]]
name = "proton-pt"
enabled = true
config = """
[Interface]
Address = 10.2.0.2/32
"""
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Wireguard) != 1 {
		t.Fatalf("expected 1 wireguard entry, got %d", len(c.Wireguard))
	}
	wg := c.Wireguard[0]
	if wg.Name != "proton-pt" {
		t.Errorf("name: want proton-pt, got %q", wg.Name)
	}
	if !wg.Enabled {
		t.Error("expected enabled=true")
	}
	if !strings.Contains(wg.Config, "10.2.0.2/32") {
		t.Errorf("config should contain address, got %q", wg.Config)
	}
}

func TestLoadFile_WireguardMultiple(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[[wireguard]]
name = "tunnel-a"
enabled = true
config = "config-a"

[[wireguard]]
name = "tunnel-b"
enabled = false
config = "config-b"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Wireguard) != 2 {
		t.Fatalf("expected 2 wireguard entries, got %d", len(c.Wireguard))
	}
	if c.Wireguard[0].Name != "tunnel-a" || c.Wireguard[1].Name != "tunnel-b" {
		t.Errorf("unexpected names: %q, %q", c.Wireguard[0].Name, c.Wireguard[1].Name)
	}
	if !c.Wireguard[0].Enabled || c.Wireguard[1].Enabled {
		t.Error("expected first enabled, second disabled")
	}
}

func TestValidateWireguard_EnabledRequiresConfig(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "test", Enabled: true, Config: ""}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error when enabled=true but config is empty")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error should mention config, got: %v", err)
	}
}

func TestValidateWireguard_EnabledRequiresName(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "", Enabled: true, Config: "some config"}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error when enabled=true but name is empty")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention name, got: %v", err)
	}
}

func TestValidateWireguard_DisabledSkipsValidation(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "", Enabled: false, Config: ""}},
	}
	if err := cfg.ValidateWireguard(c); err != nil {
		t.Errorf("disabled entry should not be validated, got: %v", err)
	}
}

func TestValidateWireguard_NoEntries(t *testing.T) {
	c := cfg.CellConfig{}
	if err := cfg.ValidateWireguard(c); err != nil {
		t.Errorf("no wireguard entries should pass validation, got: %v", err)
	}
}

func TestMerge_WireguardAccumulates(t *testing.T) {
	global := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "global-tun", Enabled: true, Config: "g"}},
	}
	project := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "project-tun", Enabled: true, Config: "p"}},
	}
	merged := cfg.Merge(global, project)
	if len(merged.Wireguard) != 2 {
		t.Fatalf("expected 2 wireguard entries after merge, got %d", len(merged.Wireguard))
	}
	if merged.Wireguard[0].Name != "global-tun" || merged.Wireguard[1].Name != "project-tun" {
		t.Errorf("unexpected order: %q, %q", merged.Wireguard[0].Name, merged.Wireguard[1].Name)
	}
}

func TestMerge_WireguardDedupByName(t *testing.T) {
	global := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "tun", Enabled: false, Config: "old"}},
	}
	project := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "tun", Enabled: true, Config: "new"}},
	}
	merged := cfg.Merge(global, project)
	if len(merged.Wireguard) != 1 {
		t.Fatalf("expected dedup to 1 entry, got %d", len(merged.Wireguard))
	}
	if !merged.Wireguard[0].Enabled || merged.Wireguard[0].Config != "new" {
		t.Error("project entry should win on name conflict")
	}
}

func TestWireguardEnabled_NoneEnabled(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "tun", Enabled: false, Config: "c"}},
	}
	if cfg.WireguardEnabled(c) {
		t.Error("expected WireguardEnabled=false when no entry is enabled")
	}
}

func TestWireguardEnabled_OneEnabled(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{Name: "tun", Enabled: true, Config: "c"}},
	}
	if !cfg.WireguardEnabled(c) {
		t.Error("expected WireguardEnabled=true when an entry is enabled")
	}
}

func TestWireguardEnabled_Empty(t *testing.T) {
	c := cfg.CellConfig{}
	if cfg.WireguardEnabled(c) {
		t.Error("expected WireguardEnabled=false when no wireguard entries")
	}
}

func TestValidateWireguard_ValidProtonVPNConfig(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "proton-pt",
			Enabled: true,
			Config: `[Interface]
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1
PostUp = wg set %i private-key /run/secrets/wg-private-key

[Peer]
PublicKey = fkBdrgo6NaOI9ICRd+i2mDbieKUzEXkj4vX3ItZ+5lM=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 79.127.131.222:51820
PersistentKeepalive = 25`,
		}},
	}
	if err := cfg.ValidateWireguard(c); err != nil {
		t.Fatalf("valid ProtonVPN config should pass, got: %v", err)
	}
}

func TestValidateWireguard_MissingPeer(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "no-peer",
			Enabled: true,
			Config: `[Interface]
Address = 10.2.0.2/32
DNS = 10.2.0.1`,
		}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error when config has no [Peer] section")
	}
	if !strings.Contains(err.Error(), "peer") && !strings.Contains(err.Error(), "Peer") {
		t.Errorf("error should mention peer, got: %v", err)
	}
}

func TestValidateWireguard_MissingPublicKey(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "no-pubkey",
			Enabled: true,
			Config: `[Interface]
Address = 10.2.0.2/32

[Peer]
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0`,
		}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error when peer has no PublicKey")
	}
	if !strings.Contains(err.Error(), "PublicKey") {
		t.Errorf("error should mention PublicKey, got: %v", err)
	}
}

func TestValidateWireguard_InvalidPublicKey(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "bad-key",
			Enabled: true,
			Config: `[Interface]
Address = 10.2.0.2/32

[Peer]
PublicKey = not-valid-base64!!!
AllowedIPs = 0.0.0.0/0`,
		}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error for invalid base64 PublicKey")
	}
}

func TestValidateWireguard_MissingAddress(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "no-addr",
			Enabled: true,
			Config: `[Interface]
DNS = 10.2.0.1

[Peer]
PublicKey = fkBdrgo6NaOI9ICRd+i2mDbieKUzEXkj4vX3ItZ+5lM=
AllowedIPs = 0.0.0.0/0`,
		}},
	}
	err := cfg.ValidateWireguard(c)
	if err == nil {
		t.Fatal("expected error when config has no Address")
	}
	if !strings.Contains(err.Error(), "Address") {
		t.Errorf("error should mention Address, got: %v", err)
	}
}

func TestValidateWireguard_PrivateKeyNotRequired(t *testing.T) {
	c := cfg.CellConfig{
		Wireguard: []cfg.WireguardEntry{{
			Name:    "no-privkey",
			Enabled: true,
			Config: `[Interface]
Address = 10.2.0.2/32
PostUp = wg set %i private-key /run/secrets/wg-private-key

[Peer]
PublicKey = fkBdrgo6NaOI9ICRd+i2mDbieKUzEXkj4vX3ItZ+5lM=
AllowedIPs = 0.0.0.0/0`,
		}},
	}
	if err := cfg.ValidateWireguard(c); err != nil {
		t.Fatalf("PrivateKey should not be required (loaded via PostUp), got: %v", err)
	}
}
