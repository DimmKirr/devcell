package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

func buildBehaviourArgv(cwd string, envPairs []string, binary string, defaultFlags, userArgs []string, cellCfg cfg.CellConfig) []string {
	e := makeEnv(envPairs...)
	c := config.Load(cwd, e)
	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cellCfg,
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
	}
	return runner.BuildArgv(spec,
		runner.FSFunc(func(string) error { return os.ErrNotExist }),
		func(string) (string, error) { return "", os.ErrNotExist },
	)
}

// Scenario A: cwd=/tmp/myproject, TMUX_PANE=%3
func TestScenarioA_ContainerNameAndVNC(t *testing.T) {
	guiCfg := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	argv := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"},
		"claude", []string{"--dangerously-skip-permissions"}, nil, guiCfg)

	if !hasConsecutive(argv, "--name", "cell-myproject-3-run") {
		t.Errorf("expected --name cell-myproject-3-run: %v", argv)
	}
	if !hasConsecutive(argv, "-p", "350:5900") {
		t.Errorf("expected -p 350:5900: %v", argv)
	}
}

// Scenario B: two panes — names and VNC ports differ
func TestScenarioB_TwoPanesNamesAndPortsDiffer(t *testing.T) {
	guiCfg := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	argv3 := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"},
		"claude", nil, nil, guiCfg)
	argv4 := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%4"},
		"claude", nil, nil, guiCfg)

	name3 := findFlagVal(argv3, "--name")
	name4 := findFlagVal(argv4, "--name")
	if name3 == name4 {
		t.Errorf("container names should differ: %q == %q", name3, name4)
	}

	port3 := extractPort(argv3)
	port4 := extractPort(argv4)
	if port3 == port4 {
		t.Errorf("VNC ports should differ: %q == %q", port3, port4)
	}
}

// Scenario C: no tmux env vars → AppName=myproject-0
func TestScenarioC_NoTmux(t *testing.T) {
	argv := buildBehaviourArgv("/tmp/myproject", nil,
		"claude", nil, nil, cfg.CellConfig{})
	name := findFlagVal(argv, "--name")
	if name != "cell-myproject-0-run" {
		t.Errorf("want cell-myproject-0-run, got %q", name)
	}
}

// Scenario D: CELL_ID=99 overrides TMUX_PANE
func TestScenarioD_ExplicitCellID(t *testing.T) {
	argv := buildBehaviourArgv("/tmp/myproject", []string{"CELL_ID", "99", "TMUX_PANE", "%3"},
		"claude", nil, nil, cfg.CellConfig{})
	name := findFlagVal(argv, "--name")
	if !strings.Contains(name, "99") {
		t.Errorf("expected CellID=99 in container name, got %q", name)
	}
}

// Scenario E: .devcell.toml [env] MY_TOKEN appears as -e MY_TOKEN=abc
func TestScenarioE_CfgEnvInArgv(t *testing.T) {
	ccfg := cfg.CellConfig{
		Env: map[string]string{"MY_TOKEN": "abc"},
	}
	argv := buildBehaviourArgv("/tmp/myproject", nil,
		"claude", nil, nil, ccfg)
	if !hasArg(argv, "MY_TOKEN=abc") {
		t.Errorf("expected MY_TOKEN=abc in argv: %v", argv)
	}
}

func hasConsecutive(argv []string, a, b string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b {
			return true
		}
	}
	return false
}

// Scenario: GUI=true publishes both VNC and RDP ports
func TestScenarioA_RDPPortPublished(t *testing.T) {
	guiCfg := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	argv := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"},
		"claude", nil, nil, guiCfg)

	if !hasConsecutive(argv, "-p", "389:3389") {
		t.Errorf("expected -p 389:3389: %v", argv)
	}
	if !hasArg(argv, "EXT_RDP_PORT=389") {
		t.Errorf("expected EXT_RDP_PORT=389 in argv: %v", argv)
	}
}

// GUI=true mounts xrdp cert dir from global config
func TestScenarioA_XrdpCertVolume(t *testing.T) {
	guiCfg := cfg.CellConfig{Cell: cfg.CellSection{GUI: true}}
	argv := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"},
		"claude", nil, nil, guiCfg)

	found := false
	for _, a := range argv {
		if strings.Contains(a, "/xrdp:/etc/devcell/xrdp") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected xrdp cert volume mount: %v", argv)
	}
}

func TestScenarioA_RDPPortNotPublishedWithoutGUI(t *testing.T) {
	argv := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"},
		"claude", nil, nil, cfg.CellConfig{})

	for i, a := range argv {
		if a == "-p" && i+1 < len(argv) && strings.Contains(argv[i+1], "3389") {
			t.Errorf("RDP port should not be published without GUI: %v", argv)
		}
	}
}

// TestDebugEnvNotSetWithoutFlag — DEVCELL_DEBUG must NOT appear in argv
// unless Debug=true in the RunSpec.
func TestDebugEnvNotSetWithoutFlag(t *testing.T) {
	argv := buildBehaviourArgv("/tmp/myproject", nil,
		"claude", nil, nil, cfg.CellConfig{})
	for _, a := range argv {
		if strings.Contains(a, "DEVCELL_DEBUG") {
			t.Errorf("DEVCELL_DEBUG should not be in argv without --debug: %v", argv)
		}
	}
}

// TestDebugEnvSetWithFlag — DEVCELL_DEBUG=true must appear when Debug=true.
func TestDebugEnvSetWithFlag(t *testing.T) {
	e := makeEnv()
	c := config.Load("/tmp/myproject", e)
	spec := runner.RunSpec{
		Config:  c,
		CellCfg: cfg.CellConfig{},
		Binary:  "claude",
		Debug:   true,
	}
	argv := runner.BuildArgv(spec,
		runner.FSFunc(func(string) error { return os.ErrNotExist }),
		func(string) (string, error) { return "", os.ErrNotExist },
	)
	if !hasArg(argv, "DEVCELL_DEBUG=true") {
		t.Errorf("expected DEVCELL_DEBUG=true in argv: %v", argv)
	}
}

func findFlagVal(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}
