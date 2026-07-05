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
			"DEVCELL_BUNK": "3",
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

func TestArgv_HostnameDefault(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "")
	argv := buildArgv(t)
	host, ok := findFlag(argv, "--hostname")
	if !ok {
		t.Fatal("missing --hostname flag")
	}
	if host != "cell-myproject-3" {
		t.Errorf("want default cell-myproject-3, got %q", host)
	}
}

func TestArgv_HostnameTOMLOverride(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "")
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.Hostname = "from-toml"
	})
	host, ok := findFlag(argv, "--hostname")
	if !ok {
		t.Fatal("missing --hostname flag")
	}
	if host != "from-toml" {
		t.Errorf("want from-toml, got %q", host)
	}
}

func TestArgv_MacAddressAbsentByDefault(t *testing.T) {
	argv := buildArgv(t)
	if _, ok := findFlag(argv, "--mac-address"); ok {
		t.Error("--mac-address should not appear when cell.mac_address is empty (let docker auto-assign)")
	}
}

func TestArgv_MacAddressFromTOML(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.MacAddress = "e2:2d:42:13:81:d2"
	})
	got, ok := findFlag(argv, "--mac-address")
	if !ok {
		t.Fatal("missing --mac-address flag")
	}
	if got != "e2:2d:42:13:81:d2" {
		t.Errorf("want e2:2d:42:13:81:d2, got %q", got)
	}
}

func TestArgv_HostnameEnvOverridesTOML(t *testing.T) {
	t.Setenv("DEVCELL_HOSTNAME", "from-env")
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Cell.Hostname = "from-toml"
	})
	host, ok := findFlag(argv, "--hostname")
	if !ok {
		t.Fatal("missing --hostname flag")
	}
	if host != "from-env" {
		t.Errorf("env should win over toml, got %q", host)
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

// --- stealth env vars ---

func TestArgv_StealthEnvVars_Default(t *testing.T) {
	argv := buildArgv(t)
	// Even with no [stealth] config, resolved defaults must be passed
	foundArch := false
	foundPlatform := false
	for _, a := range argv {
		if strings.HasPrefix(a, "DEVCELL_STEALTH_ARCH=") {
			foundArch = true
		}
		if strings.HasPrefix(a, "DEVCELL_STEALTH_PLATFORM=") {
			foundPlatform = true
		}
		if strings.HasPrefix(a, "DEVCELL_STEALTH_USER_AGENT=") {
			t.Error("DEVCELL_STEALTH_USER_AGENT should not be passed — UA is derived from arch+platform in the wrapper")
		}
	}
	if !foundArch {
		t.Error("missing DEVCELL_STEALTH_ARCH env var in argv")
	}
	if !foundPlatform {
		t.Error("missing DEVCELL_STEALTH_PLATFORM env var in argv")
	}
}

func TestArgv_StealthEnvVars_Explicit(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Stealth = cfg.StealthSection{Arch: "arm", Platform: "macOS"}
	})
	if !hasArg(argv, "DEVCELL_STEALTH_ARCH=arm") {
		t.Errorf("expected DEVCELL_STEALTH_ARCH=arm in argv: %v", argv)
	}
	if !hasArg(argv, "DEVCELL_STEALTH_PLATFORM=macOS") {
		t.Errorf("expected DEVCELL_STEALTH_PLATFORM=macOS in argv: %v", argv)
	}
}

// --- Port forwarding from config ---

func TestArgv_CfgPortsSinglePort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"3000"}}
	})
	if !hasConsecutive(argv, "-p", "0.0.0.0:3000:3000") {
		t.Errorf("expected -p 0.0.0.0:3000:3000 for bare port '3000': %v", argv)
	}
}

func TestArgv_CfgPortsMappedPort(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"8080:3000"}}
	})
	if !hasConsecutive(argv, "-p", "0.0.0.0:8080:3000") {
		t.Errorf("expected -p 0.0.0.0:8080:3000: %v", argv)
	}
}

func TestArgv_CfgPortsMultiple(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"3000", "8080:3000"}}
	})
	if !hasConsecutive(argv, "-p", "0.0.0.0:3000:3000") {
		t.Errorf("expected -p 0.0.0.0:3000:3000: %v", argv)
	}
	if !hasConsecutive(argv, "-p", "0.0.0.0:8080:3000") {
		t.Errorf("expected -p 0.0.0.0:8080:3000: %v", argv)
	}
}

func TestArgv_CfgPortsUDP(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"54321/udp"}}
	})
	// docker -p format: hostPort:containerPort/proto — proto on container side only
	if !hasConsecutive(argv, "-p", "0.0.0.0:54321:54321/udp") {
		t.Errorf("expected -p 0.0.0.0:54321:54321/udp for UDP port: %v", argv)
	}
}

func TestArgv_CfgPortsMappedUDP(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.CellCfg.Ports = cfg.PortsSection{Forward: []string{"9999:54321/udp"}}
	})
	if !hasConsecutive(argv, "-p", "0.0.0.0:9999:54321/udp") {
		t.Errorf("expected -p 0.0.0.0:9999:54321/udp for mapped UDP port: %v", argv)
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
	if !hasConsecutive(argv, "-p", "0.0.0.0:350:5900") {
		t.Errorf("expected -p 0.0.0.0:350:5900 in argv: %v", argv)
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
	// BuildArgv's default image is the Debian variant — callers using --pure
	// (the default after the CELL-189 flip) override Image explicitly on the
	// RunSpec, so the default path tested here is the legacy --debian one.
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

// --- UserImageTag stack-based (legacy bare tag, used by --debian) ---
//
// UserImageTag() is unchanged across the 2026-05-15 flip — it remains the
// user's "current image" concept (bare devcell-user:<stack>). After CELL-189
// it's reached only via `cell <agent> --debian` (legacy Dockerfile path);
// the new default reaches UserImageTagPure() via PickImageTag(false).

func withCleanImageState(t *testing.T) {
	t.Helper()
	t.Setenv("DEVCELL_USER_IMAGE", "")
	t.Setenv("DEVCELL_USER_IMAGE_PURE", "")
	t.Setenv("DEVCELL_CELL_NAME", "")
	t.Setenv("TMUX_SESSION_NAME", "")
	origStack := runner.Stack
	origModules := runner.Modules
	origPerSession := runner.PerCellImage
	t.Cleanup(func() {
		runner.Stack = origStack
		runner.Modules = origModules
		runner.PerCellImage = origPerSession
	})
	runner.Stack = "base"
	runner.Modules = nil
	runner.PerCellImage = false
}

func TestUserImageTag_DefaultStack(t *testing.T) {
	withCleanImageState(t)
	got := runner.UserImageTag()
	if got != "devcell-user:base" {
		t.Errorf("default stack: want devcell-user:base, got %q", got)
	}
}

func TestUserImageTag_UltimateStack(t *testing.T) {
	withCleanImageState(t)
	runner.Stack = "ultimate"
	got := runner.UserImageTag()
	if got != "devcell-user:ultimate" {
		t.Errorf("ultimate stack: want devcell-user:ultimate, got %q", got)
	}
}

func TestUserImageTag_StackWithModules(t *testing.T) {
	withCleanImageState(t)
	runner.Stack = "ultimate"
	runner.Modules = []string{"nixos", "electronics"}
	got := runner.UserImageTag()
	// Modules sorted: electronics, nixos
	if !strings.HasPrefix(got, "devcell-user:ultimate-electronics-nixos-") {
		t.Errorf("stack+modules: want prefix devcell-user:ultimate-electronics-nixos-, got %q", got)
	}
	// sha8 suffix
	parts := strings.Split(got, "-")
	sha := parts[len(parts)-1]
	if len(sha) != 8 {
		t.Errorf("sha suffix: want 8 chars, got %d in %q", len(sha), got)
	}
}

func TestUserImageTag_ModuleOrderDoesNotMatter(t *testing.T) {
	withCleanImageState(t)
	runner.Stack = "go"
	runner.Modules = []string{"b", "a", "c"}
	tag1 := runner.UserImageTag()
	runner.Modules = []string{"c", "a", "b"}
	tag2 := runner.UserImageTag()
	if tag1 != tag2 {
		t.Errorf("module order should not matter: %q != %q", tag1, tag2)
	}
}

func TestUserImageTag_EnvOverrideWins(t *testing.T) {
	withCleanImageState(t)
	t.Setenv("DEVCELL_USER_IMAGE", "custom:override")
	runner.Stack = "ultimate"
	got := runner.UserImageTag()
	if got != "custom:override" {
		t.Errorf("override: want custom:override, got %q", got)
	}
}

// --- UserImageTag per-session (legacy) ---

func TestUserImageTag_PerSession_Default(t *testing.T) {
	withCleanImageState(t)
	runner.PerCellImage = true
	got := runner.UserImageTag()
	if got != "devcell-user:main" {
		t.Errorf("per-session default: want devcell-user:main, got %q", got)
	}
}

func TestUserImageTag_PerSession_TmuxFallback(t *testing.T) {
	withCleanImageState(t)
	runner.PerCellImage = true
	t.Setenv("TMUX_SESSION_NAME", "DIMM")
	got := runner.UserImageTag()
	if got != "devcell-user:DIMM" {
		t.Errorf("per-session tmux: want devcell-user:DIMM, got %q", got)
	}
}

func TestUserImageTag_PerSession_ExplicitBeatssTmux(t *testing.T) {
	withCleanImageState(t)
	runner.PerCellImage = true
	t.Setenv("DEVCELL_CELL_NAME", "explicit")
	t.Setenv("TMUX_SESSION_NAME", "tmux-session")
	got := runner.UserImageTag()
	if got != "devcell-user:explicit" {
		t.Errorf("per-session precedence: want devcell-user:explicit, got %q", got)
	}
}

// --- ParseImageMetadata ---

func TestParseImageMetadata_ValidJSON(t *testing.T) {
	input := `{"base_image":"ghcr.io/devcell-sh/devcell:v1.2.3-go","stack":"go","modules":["desktop"],"git_commit":"a3f2e1","build_date":"2026-03-26T10:15:30Z","packages":142}`
	m := runner.ParseImageMetadata([]byte(input))
	if m.BaseImage != "ghcr.io/devcell-sh/devcell:v1.2.3-go" {
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

// --- ImageMetadataFromInspect (label-based, 2026-05-16 flip) ---
//
// New source-of-truth for build date / commit / stack: OCI manifest labels +
// the manifest's Created field, NOT /etc/devcell/metadata.json. Pinning
// metadata.json static eliminates the per-build 3.9GB customization-layer
// re-push that real-timestamp interpolation was causing.

func TestImageMetadataFromInspect_LabelsPopulated(t *testing.T) {
	m := runner.ImageMetadataFromInspectExport(
		"2026-05-16T21:33:48Z",
		map[string]string{
			"devcell.built-with":                  "nix2container",
			"devcell.stack":                       "ultimate",
			"org.opencontainers.image.created":    "2026-05-16T21:33:48Z",
			"org.opencontainers.image.revision":   "abc123",
		},
		nil,
	)
	if m.Stack != "ultimate" {
		t.Errorf("Stack = %q, want ultimate", m.Stack)
	}
	if m.GitCommit != "abc123" {
		t.Errorf("GitCommit = %q, want abc123", m.GitCommit)
	}
	if m.BuildDate != "2026-05-16T21:33:48Z" {
		t.Errorf("BuildDate = %q, want 2026-05-16T21:33:48Z", m.BuildDate)
	}
	if m.BaseImage != "nix2container" {
		t.Errorf("BaseImage = %q, want nix2container", m.BaseImage)
	}
}

// When the org.opencontainers.image.created label is missing (older images
// from before the 2026-05-16 label addition), the OCI manifest's Created
// field should be the fallback — every pure build sets it via the
// nix2container `created` parameter.
func TestImageMetadataFromInspect_NoLabelDateFallsBackToCreated(t *testing.T) {
	m := runner.ImageMetadataFromInspectExport(
		"2026-05-16T12:00:00Z",
		map[string]string{"devcell.stack": "go"},
		nil,
	)
	if m.BuildDate != "2026-05-16T12:00:00Z" {
		t.Errorf("BuildDate = %q, want fallback to Created", m.BuildDate)
	}
}

// Stack label missing → fall back to DEVCELL_PROFILE env var (the image's
// own config Env). This is the path for very old images without devcell.stack.
func TestImageMetadataFromInspect_StackFromEnvFallback(t *testing.T) {
	m := runner.ImageMetadataFromInspectExport(
		"2026-05-16T12:00:00Z",
		nil,
		[]string{"PATH=/usr/bin", "DEVCELL_PROFILE=devcell-python", "HOME=/root"},
	)
	if m.Stack != "python" {
		t.Errorf("Stack = %q, want python (from DEVCELL_PROFILE env)", m.Stack)
	}
}

// Verify ImageVersions formats output sensibly given the new metadata shape.
// This is what the CLI prints in the "User image: ..." line on error or at
// `cell status` / `cell run` boot. With both date and real commit:
//
//	cell vX.X.X-... built 2026-05-16T...Z
func TestImageVersions_Format(t *testing.T) {
	// Direct call to the formatter via exposed helper: we synthesize an
	// ImageMetadata and pass it through the same shape ImageVersions uses.
	// The format string ImageVersions emits is "<commit> built <date>"
	// when both fields are real, " built <date>" when only date, etc.
	cases := []struct {
		name   string
		m      runner.ImageMetadata
		wantHas string // substring we expect in the formatted "user" output
	}{
		{"commit+date", runner.ImageMetadata{GitCommit: "abc123", BuildDate: "2026-05-16T21:33:48Z", BaseImage: "nix2container"}, "abc123 built 2026-05-16T21:33:48Z"},
		{"date only",   runner.ImageMetadata{GitCommit: "unknown", BuildDate: "2026-05-16T21:33:48Z", BaseImage: "nix2container"}, "built 2026-05-16T21:33:48Z"},
		{"epoch date",  runner.ImageMetadata{GitCommit: "abc123", BuildDate: "1970-01-01T00:00:00Z"}, "abc123"},
		{"placeholders only", runner.ImageMetadata{GitCommit: "unknown", BuildDate: "1970-01-01T00:00:00Z"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runner.FormatImageVersionUserExport(tc.m)
			if tc.wantHas == "" {
				if got != "" {
					t.Errorf("want empty for placeholder-only, got %q", got)
				}
				return
			}
			if got != tc.wantHas {
				t.Errorf("got %q, want %q", got, tc.wantHas)
			}
		})
	}
}

// --- StackImageTagImpure / StackImageTagPure ---
//
// Pre-flip there was a single bare StackImageTag(stack). After CELL-189 both
// variants are explicit so scaffold's base-image fallback picks the right one.
// CELL-165 renamed the impure variant from `-debian` to `-impure`. The
// `StackImageTagDebian` deprecated alias was removed after callers migrated.

func TestStackImageTagImpure_GoStack(t *testing.T) {
	got := runner.StackImageTagImpure("go")
	// version.Version is v0.0.0 in tests → v0.0.0-go-impure
	if got != "ghcr.io/devcell-sh/devcell:v0.0.0-go-impure" {
		t.Errorf("want ghcr.io/devcell-sh/devcell:v0.0.0-go-impure, got %q", got)
	}
}

func TestStackImageTagImpure_UltimateStack(t *testing.T) {
	got := runner.StackImageTagImpure("ultimate")
	if got != "ghcr.io/devcell-sh/devcell:v0.0.0-ultimate-impure" {
		t.Errorf("want ghcr.io/devcell-sh/devcell:v0.0.0-ultimate-impure, got %q", got)
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
	if got != "ghcr.io/devcell-sh/devcell:v0.0.0-core" {
		t.Errorf("want ghcr.io/devcell-sh/devcell:v0.0.0-core, got %q", got)
	}
}

// --- Thin image volume mount ---

func TestArgv_ThinImageMountsNixStoreVolume(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.ThinImage = true
	})
	if !hasConsecutive(argv, "-v", "devcell-nix-store:/nix") {
		t.Errorf("expected -v devcell-nix-store:/nix for thin image, argv: %v", argv)
	}
}

func TestArgv_NonThinImageNoNixStoreVolume(t *testing.T) {
	argv := buildArgv(t)
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && strings.Contains(argv[i+1], "devcell-nix-store") {
			t.Errorf("devcell-nix-store volume should NOT appear for non-thin image, argv: %v", argv)
		}
	}
}

func TestArgv_PassesHostProjectDir(t *testing.T) {
	argv := buildArgv(t)
	if !hasConsecutive(argv, "-e", "DEVCELL_HOST_PROJECT_DIR=/home/bob/myproject") {
		t.Errorf("should pass DEVCELL_HOST_PROJECT_DIR for thin build path resolution, argv: %v", argv)
	}
}
