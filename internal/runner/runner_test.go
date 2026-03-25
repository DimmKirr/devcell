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
		s.CellCfg.Cell.GUI = true
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

// --- Network and port ---

func TestArgv_VNCPort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = true
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

func TestArgv_GUIEnabled(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.GUI = true
	})
	if !hasArg(argv, "DEVCELL_GUI_ENABLED=true") {
		t.Errorf("expected DEVCELL_GUI_ENABLED=true in argv: %v", argv)
	}
}

func TestArgv_GUIDisabledByDefault(t *testing.T) {
	argv := buildArgv(t)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
