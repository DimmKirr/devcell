package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

func baseConfig() config.Config {
	return config.Load("/home/bob/myproject", func(k string) string {
		m := map[string]string{
			"CELL_ID": "3",
			"HOME":    "/home/bob",
			"USER":    "bob",
			"TERM":    "xterm-256color",
		}
		return m[k]
	})
}

func noopFS() runner.FS {
	return runner.FSFunc(func(path string) error {
		return os.ErrNotExist
	})
}

func existFS(paths ...string) runner.FS {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return runner.FSFunc(func(path string) error {
		if set[path] {
			return nil
		}
		return os.ErrNotExist
	})
}

func noopLookPath(string) (string, error) { return "", os.ErrNotExist }
func opLookPath(bin string) (string, error) {
	if bin == "op" {
		return "/usr/bin/op", nil
	}
	return "", os.ErrNotExist
}

func buildArgv(t *testing.T, extra ...func(*runner.RunSpec)) []string {
	t.Helper()
	spec := runner.RunSpec{
		Config:       baseConfig(),
		CellCfg:      cfg.CellConfig{},
		Binary:       "claude",
		DefaultFlags: []string{"--dangerously-skip-permissions"},
		UserArgs:     nil,
	}
	for _, fn := range extra {
		fn(&spec)
	}
	return runner.BuildArgv(spec, noopFS(), noopLookPath)
}

func hasArg(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}

func hasConsecutive(argv []string, a, b string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b {
			return true
		}
	}
	return false
}

func findFlag(argv []string, flag string) (string, bool) {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1], true
		}
	}
	return "", false
}

// --- Structure ---

func TestArgv_StartsWithDockerRunFlags(t *testing.T) {
	argv := buildArgv(t)
	if len(argv) < 4 || argv[0] != "docker" || argv[1] != "run" {
		t.Errorf("argv should start with 'docker run': %v", argv[:min(4, len(argv))])
	}
	if !hasArg(argv, "--rm") {
		t.Error("missing --rm")
	}
	if !hasArg(argv, "-it") {
		t.Error("missing -it")
	}
}

func TestArgv_ContainerName(t *testing.T) {
	argv := buildArgv(t)
	name, ok := findFlag(argv, "--name")
	if !ok {
		t.Fatal("missing --name flag")
	}
	if name != "cell-myproject-3-run" {
		t.Errorf("want cell-myproject-3-run, got %q", name)
	}
}

// --- Mandatory env vars ---

func TestArgv_MandatoryEnvVars(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = boolPtr(true)
	})
	mustHaveEnv := []string{
		"APP_NAME=myproject-3",
		"HOST_USER=bob",
		"HOME=/home/bob",
		"IS_SANDBOX=1",
		"WORKSPACE=/myproject-3",
		"EXT_VNC_PORT=350",
	}
	for _, e := range mustHaveEnv {
		if !hasArg(argv, e) {
			t.Errorf("missing -e %s", e)
		}
	}
}

func TestArgv_UserAndGroupAdd(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "--user", "0") {
		t.Error("missing --user 0")
	}
	if !hasConsecutive(argv, "--group-add", "0") {
		t.Error("missing --group-add 0")
	}
}

// --- labels ---

func TestArgv_Labels(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "--label", "devcell.basedir=/home/bob/myproject") {
		t.Errorf("missing --label devcell.basedir in argv: %v", argv)
	}
	if !hasConsecutive(argv, "--label", "devcell.cellid=3") {
		t.Errorf("missing --label devcell.cellid in argv: %v", argv)
	}
}

// --- env-file ---

func TestArgv_EnvFileSelfRef(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.devcell")
	os.WriteFile(envFile, []byte("# comment\nMY_SECRET=${MY_SECRET}\nLITERAL=hello\n"), 0644)
	spec := runner.RunSpec{
		Config: config.Load(dir, func(k string) string {
			if k == "USER" {
				return "bob"
			}
			if k == "HOME" {
				return "/home/bob"
			}
			return ""
		}),
		CellCfg: cfg.CellConfig{},
		Binary:  "bash",
	}
	argv := runner.BuildArgv(spec, noopFS(), noopLookPath)
	// Self-referencing KEY=${KEY} → just -e KEY (Docker inherits from host)
	if !hasConsecutive(argv, "-e", "MY_SECRET") {
		t.Errorf("expected -e MY_SECRET (inherit) in argv: %v", argv)
	}
	// Literal KEY=value → -e KEY=value
	if !hasConsecutive(argv, "-e", "LITERAL=hello") {
		t.Errorf("expected -e LITERAL=hello in argv: %v", argv)
	}
	// Should NOT have --env-file anymore
	if hasArg(argv, "--env-file") {
		t.Error("should not use --env-file; vars should be passed individually")
	}
}

func TestArgv_EnvFileAbsent(t *testing.T) {
	argv := buildArgv(t)
	if hasArg(argv, "--env-file") {
		t.Error("--env-file should not be present when .env.devcell does not exist")
	}
}

// --- InheritEnv ---

func TestArgv_InheritEnv(t *testing.T) {
	spec := runner.RunSpec{
		Config:     baseConfig(),
		CellCfg:    cfg.CellConfig{},
		Binary:     "bash",
		InheritEnv: []string{"SECRET_A", "SECRET_B"},
	}
	argv := runner.BuildArgv(spec, noopFS(), noopLookPath)
	if !hasConsecutive(argv, "-e", "SECRET_A") {
		t.Errorf("expected -e SECRET_A (inherit) in argv: %v", argv)
	}
	if !hasConsecutive(argv, "-e", "SECRET_B") {
		t.Errorf("expected -e SECRET_B (inherit) in argv: %v", argv)
	}
	// Values should NOT appear in argv (security: no secrets in ps aux)
	for _, a := range argv {
		if a == "SECRET_A=" || a == "SECRET_B=" {
			t.Errorf("secret value should not appear in argv: %v", argv)
		}
	}
}

// --- op passthrough ---

func TestArgv_OpPrefixWhenOpFound(t *testing.T) {
	spec := runner.RunSpec{
		Config:       baseConfig(),
		CellCfg:      cfg.CellConfig{},
		Binary:       "claude",
		DefaultFlags: []string{"--dangerously-skip-permissions"},
	}
	argv := runner.BuildArgv(spec, noopFS(), opLookPath)
	if argv[0] != "op" || argv[1] != "run" || argv[2] != "--" {
		t.Errorf("expected op run -- prefix, got: %v", argv[:min(3, len(argv))])
	}
}

func TestArgv_NoOpPrefixWhenOpMissing(t *testing.T) {
	argv := buildArgv(t)
	if argv[0] == "op" {
		t.Error("op prefix should be absent when op not in PATH")
	}
}

// --- cfg env and volumes ---

func TestArgv_CfgEnvVarsInArgv(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Env = map[string]string{"MY_TOKEN": "abc123"}
	})
	if !hasArg(argv, "MY_TOKEN=abc123") {
		t.Errorf("expected MY_TOKEN=abc123 in argv: %v", argv)
	}
}

func TestArgv_CfgVolumes(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Volumes = []cfg.VolumeMount{{Mount: "/host/path:/container/path"}}
	})
	if !hasConsecutive(argv, "-v", "/host/path:/container/path") {
		t.Errorf("expected -v /host/path:/container/path in argv: %v", argv)
	}
}

func TestArgv_ReadonlyVolume(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Volumes = []cfg.VolumeMount{{Mount: "/host:/container:ro"}}
	})
	if !hasConsecutive(argv, "-v", "/host:/container:ro") {
		t.Errorf("expected -v /host:/container:ro in argv: %v", argv)
	}
}

// --- cfg mise ---

func TestArgv_MiseEnvVars(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Mise = map[string]string{"trusted_config_paths": "/"}
	})
	if !hasArg(argv, "MISE_TRUSTED_CONFIG_PATHS=/") {
		t.Errorf("expected MISE_TRUSTED_CONFIG_PATHS=/ in argv: %v", argv)
	}
}

// --- Port forwarding from config ---

func TestArgv_CfgPortsSinglePort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"3000"}}
	})
	if !hasConsecutive(argv, "-p", "3000:3000") {
		t.Errorf("expected -p 3000:3000 for bare port '3000': %v", argv)
	}
}

func TestArgv_CfgPortsMappedPort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"8080:3000"}}
	})
	if !hasConsecutive(argv, "-p", "8080:3000") {
		t.Errorf("expected -p 8080:3000: %v", argv)
	}
}

func TestArgv_CfgPortsMultiple(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"3000", "8080:3000"}}
	})
	if !hasConsecutive(argv, "-p", "3000:3000") {
		t.Errorf("expected -p 3000:3000: %v", argv)
	}
	if !hasConsecutive(argv, "-p", "8080:3000") {
		t.Errorf("expected -p 8080:3000: %v", argv)
	}
}

func TestArgv_CfgPortsEmpty(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = boolPtr(false)
	})
	// No -p flags when no ports configured and GUI explicitly off
	for i, a := range argv {
		if a == "-p" && i+1 < len(argv) {
			t.Errorf("unexpected -p flag when no ports configured: -p %s", argv[i+1])
		}
	}
}

// --- Network and port ---

func TestArgv_VNCPort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = boolPtr(true)
	})
	if !hasConsecutive(argv, "-p", "350:5900") {
		t.Errorf("expected -p 350:5900 in argv: %v", argv)
	}
}

func TestArgv_Network(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "--network", "devcell-network") {
		t.Errorf("expected --network devcell-network: %v", argv)
	}
}

// --- Workdir and image ---

func TestArgv_WorkdirAndImage(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "--workdir", "/myproject-3") {
		t.Errorf("expected --workdir /myproject-3: %v", argv)
	}
	if !hasArg(argv, runner.UserImageTag()) {
		t.Error("missing devcell-local image name")
	}
}

// --- Binary and user args at end ---

func TestArgv_BinaryAndDefaultFlagsAtEnd(t *testing.T) {
	argv := buildArgv(t)
	// Find devcell-local image, then expect binary after it
	imgIdx := -1
	for i, a := range argv {
		if a == runner.UserImageTag() {
			imgIdx = i
			break
		}
	}
	if imgIdx < 0 {
		t.Fatal("devcell-local image not found")
	}
	rest := argv[imgIdx+1:]
	if len(rest) == 0 || rest[0] != "claude" {
		t.Errorf("expected 'claude' after image, got: %v", rest)
	}
	if !hasArg(rest, "--dangerously-skip-permissions") {
		t.Errorf("missing default flag in trailing args: %v", rest)
	}
}

func TestArgv_UserArgsAppended(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.UserArgs = []string{"--resume", "abc"}
	})
	if !strings.HasSuffix(strings.Join(argv, " "), "claude --dangerously-skip-permissions --resume abc") {
		t.Errorf("unexpected tail: %v", argv[len(argv)-5:])
	}
}

// --- GUI flag ---

func boolPtr(b bool) *bool { return &b }

func TestArgv_GUIEnabledByDefault(t *testing.T) {
	// GUI defaults to true when not set (nil)
	argv := buildArgv(t)
	if !hasArg(argv, "DEVCELL_GUI_ENABLED=true") {
		t.Errorf("expected DEVCELL_GUI_ENABLED=true by default: %v", argv)
	}
}

func TestArgv_GUIExplicitTrue(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = boolPtr(true)
	})
	if !hasArg(argv, "DEVCELL_GUI_ENABLED=true") {
		t.Errorf("expected DEVCELL_GUI_ENABLED=true in argv: %v", argv)
	}
}

func TestArgv_GUIExplicitFalse(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = boolPtr(false)
	})
	if hasArg(argv, "DEVCELL_GUI_ENABLED=true") {
		t.Error("DEVCELL_GUI_ENABLED should not be present when gui=false")
	}
}

// --- Git identity ---

func TestArgv_GitEnvVarsFromHostEnv(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.Getenv = func(k string) string {
			m := map[string]string{
				"GIT_AUTHOR_NAME":  "EnvAlice",
				"GIT_AUTHOR_EMAIL": "env@alice.com",
			}
			return m[k]
		}
		s.CellCfg.Git = cfg.GitSection{
			AuthorName: "TomlBob", AuthorEmail: "toml@bob.com",
		}
	})
	if !hasArg(argv, "GIT_AUTHOR_NAME=EnvAlice") {
		t.Errorf("expected GIT_AUTHOR_NAME=EnvAlice: %v", argv)
	}
	if !hasArg(argv, "GIT_AUTHOR_EMAIL=env@alice.com") {
		t.Errorf("expected GIT_AUTHOR_EMAIL=env@alice.com: %v", argv)
	}
}

func TestArgv_GitEnvVarsFromToml(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.Getenv = func(string) string { return "" }
		s.CellCfg.Git = cfg.GitSection{
			AuthorName: "Alice", AuthorEmail: "alice@test.com",
		}
	})
	if !hasArg(argv, "GIT_AUTHOR_NAME=Alice") {
		t.Errorf("expected GIT_AUTHOR_NAME=Alice: %v", argv)
	}
	if !hasArg(argv, "GIT_COMMITTER_NAME=Alice") {
		t.Errorf("expected GIT_COMMITTER_NAME=Alice (defaulted from author): %v", argv)
	}
	if !hasArg(argv, "GIT_COMMITTER_EMAIL=alice@test.com") {
		t.Errorf("expected GIT_COMMITTER_EMAIL=alice@test.com (defaulted from author): %v", argv)
	}
}

func TestArgv_GitExtraEnvOverridesDefaults(t *testing.T) {
	// Git identity resolved by cmd/root.go is passed via ExtraEnv;
	// it should override the hardcoded "DevCell" defaults.
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.Getenv = func(string) string { return "" }
		s.ExtraEnv = map[string]string{
			"GIT_AUTHOR_NAME":     "Alice",
			"GIT_AUTHOR_EMAIL":    "alice@test.com",
			"GIT_COMMITTER_NAME":  "Alice",
			"GIT_COMMITTER_EMAIL": "alice@test.com",
		}
	})
	if !hasArg(argv, "GIT_AUTHOR_NAME=Alice") {
		t.Errorf("expected ExtraEnv GIT_AUTHOR_NAME=Alice: %v", argv)
	}
	if !hasArg(argv, "GIT_AUTHOR_EMAIL=alice@test.com") {
		t.Errorf("expected ExtraEnv GIT_AUTHOR_EMAIL=alice@test.com: %v", argv)
	}
}

func TestArgv_GitFallbackDefaults(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.Getenv = func(string) string { return "" }
	})
	if !hasArg(argv, "GIT_AUTHOR_NAME=DevCell") {
		t.Errorf("expected hardcoded fallback GIT_AUTHOR_NAME=DevCell: %v", argv)
	}
	if !hasArg(argv, "GIT_COMMITTER_EMAIL=devcell@devcell.io") {
		t.Errorf("expected hardcoded fallback GIT_COMMITTER_EMAIL: %v", argv)
	}
}

// --- tmpfs for secrets ---

func TestArgv_TmpfsSecretsMount(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "--tmpfs", "/run/secrets:mode=700,noexec,nosuid,size=1m") {
		t.Errorf("expected --tmpfs /run/secrets:mode=700,noexec,nosuid,size=1m in argv: %v", argv)
	}
}

func TestArgv_SecretKeysEnvVar(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.InheritEnv = []string{"DB_PASS", "API_KEY"}
	})
	if !hasArg(argv, "DEVCELL_SECRET_KEYS=DB_PASS,API_KEY") {
		t.Errorf("expected DEVCELL_SECRET_KEYS=DB_PASS,API_KEY in argv: %v", argv)
	}
}

func TestArgv_SecretKeysEmpty_NoEnvVar(t *testing.T) {
	argv := buildArgv(t)
	for _, a := range argv {
		if strings.HasPrefix(a, "DEVCELL_SECRET_KEYS=") {
			t.Errorf("DEVCELL_SECRET_KEYS should not be present when InheritEnv is empty: %v", argv)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- UserImageTag per-session ---

func TestUserImageTag_DefaultSession(t *testing.T) {
	t.Setenv("DEVCELL_USER_IMAGE", "")
	t.Setenv("DEVCELL_SESSION_NAME", "")
	t.Setenv("TMUX_SESSION_NAME", "")
	got := runner.UserImageTag()
	if got != "devcell-user:main" {
		t.Errorf("default session: want devcell-user:main, got %q", got)
	}
}

func TestUserImageTag_SessionName(t *testing.T) {
	t.Setenv("DEVCELL_USER_IMAGE", "")
	t.Setenv("DEVCELL_SESSION_NAME", "webdev")
	t.Setenv("TMUX_SESSION_NAME", "")
	got := runner.UserImageTag()
	if got != "devcell-user:webdev" {
		t.Errorf("session name: want devcell-user:webdev, got %q", got)
	}
}

func TestUserImageTag_TmuxSessionFallback(t *testing.T) {
	t.Setenv("DEVCELL_USER_IMAGE", "")
	t.Setenv("DEVCELL_SESSION_NAME", "")
	t.Setenv("TMUX_SESSION_NAME", "tmux-dev")
	got := runner.UserImageTag()
	if got != "devcell-user:tmux-dev" {
		t.Errorf("tmux fallback: want devcell-user:tmux-dev, got %q", got)
	}
}

func TestUserImageTag_SessionNameBeatssTmux(t *testing.T) {
	t.Setenv("DEVCELL_USER_IMAGE", "")
	t.Setenv("DEVCELL_SESSION_NAME", "explicit")
	t.Setenv("TMUX_SESSION_NAME", "tmux-session")
	got := runner.UserImageTag()
	if got != "devcell-user:explicit" {
		t.Errorf("precedence: want devcell-user:explicit, got %q", got)
	}
}

func TestUserImageTag_EnvOverrideWins(t *testing.T) {
	t.Setenv("DEVCELL_USER_IMAGE", "custom:override")
	t.Setenv("DEVCELL_SESSION_NAME", "ignored")
	got := runner.UserImageTag()
	if got != "custom:override" {
		t.Errorf("override: want custom:override, got %q", got)
	}
}

// --- ParseImageMetadata ---

func TestParseImageMetadata_ValidJSON(t *testing.T) {
	input := `{"base_image":"ghcr.io/dimmkirr/devcell:v1.2.3-go","stack":"go","modules":["desktop"],"git_commit":"a3f2e1","build_date":"2026-03-26T10:15:30Z","packages":142}`
	m := runner.ParseImageMetadata([]byte(input))
	if m.BaseImage != "ghcr.io/dimmkirr/devcell:v1.2.3-go" {
		t.Errorf("base_image: want v1.2.3-go, got %q", m.BaseImage)
	}
	if m.Stack != "go" {
		t.Errorf("stack: want go, got %q", m.Stack)
	}
	if len(m.Modules) != 1 || m.Modules[0] != "desktop" {
		t.Errorf("modules: want [desktop], got %v", m.Modules)
	}
	if m.GitCommit != "a3f2e1" {
		t.Errorf("git_commit: want a3f2e1, got %q", m.GitCommit)
	}
	if m.Packages != 142 {
		t.Errorf("packages: want 142, got %d", m.Packages)
	}
}

func TestParseImageMetadata_EmptyInput(t *testing.T) {
	m := runner.ParseImageMetadata(nil)
	if m.Stack != "" || m.BaseImage != "" {
		t.Errorf("empty input should return zero value, got %+v", m)
	}
}

func TestParseImageMetadata_InvalidJSON(t *testing.T) {
	m := runner.ParseImageMetadata([]byte("not json"))
	if m.Stack != "" {
		t.Errorf("invalid JSON should return zero value, got %+v", m)
	}
}

// --- StackImageTag ---

func TestStackImageTag_GoStack(t *testing.T) {
	got := runner.StackImageTag("go")
	// version.Version is v0.0.0 in tests → v0.0.0-go
	if got != "ghcr.io/dimmkirr/devcell:v0.0.0-go" {
		t.Errorf("want ghcr.io/dimmkirr/devcell:v0.0.0-go, got %q", got)
	}
}

func TestStackImageTag_UltimateStack(t *testing.T) {
	got := runner.StackImageTag("ultimate")
	if got != "ghcr.io/dimmkirr/devcell:v0.0.0-ultimate" {
		t.Errorf("want ghcr.io/dimmkirr/devcell:v0.0.0-ultimate, got %q", got)
	}
}

// --- AWS read-only ---

func TestArgv_AwsReadOnlyDefault(t *testing.T) {
	// Default (nil) → read-only disabled
	argv := buildArgv(t)
	if hasArg(argv, "AWS_CONFIG_FILE=/opt/devcell/.aws/config") {
		t.Error("AWS_CONFIG_FILE should not be present when aws.read_only defaults false")
	}
	if hasArg(argv, "AWS_READ_OPERATIONS_ONLY=true") {
		t.Error("AWS_READ_OPERATIONS_ONLY should not be present when aws.read_only defaults false")
	}
	if hasArg(argv, "READ_OPERATIONS_ONLY=true") {
		t.Error("READ_OPERATIONS_ONLY should not be present when aws.read_only defaults false")
	}
}

func TestArgv_AwsReadOnlyExplicitTrue(t *testing.T) {
	trueVal := true
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Aws = cfg.AwsSection{ReadOnly: &trueVal}
	})
	if !hasArg(argv, "AWS_CONFIG_FILE=/opt/devcell/.aws/config") {
		t.Errorf("expected AWS_CONFIG_FILE: %v", argv)
	}
	if !hasArg(argv, "AWS_READ_OPERATIONS_ONLY=true") {
		t.Errorf("expected AWS_READ_OPERATIONS_ONLY=true: %v", argv)
	}
}

func TestArgv_AwsReadOnlyFalse(t *testing.T) {
	falseVal := false
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Aws = cfg.AwsSection{ReadOnly: &falseVal}
	})
	if hasArg(argv, "AWS_CONFIG_FILE=/opt/devcell/.aws/config") {
		t.Error("AWS_CONFIG_FILE should not be present when aws.read_only=false")
	}
	if hasArg(argv, "AWS_READ_OPERATIONS_ONLY=true") {
		t.Error("AWS_READ_OPERATIONS_ONLY should not be present when aws.read_only=false")
	}
	if hasArg(argv, "READ_OPERATIONS_ONLY=true") {
		t.Error("READ_OPERATIONS_ONLY should not be present when aws.read_only=false")
	}
}

func TestBaseImageTag_DefaultIsVersioned(t *testing.T) {
	t.Setenv("DEVCELL_BASE_IMAGE", "")
	got := runner.BaseImageTag()
	if got != "ghcr.io/dimmkirr/devcell:v0.0.0-core" {
		t.Errorf("want ghcr.io/dimmkirr/devcell:v0.0.0-core, got %q", got)
	}
}
