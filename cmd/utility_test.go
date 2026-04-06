package main_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

// --- shell ---

func TestShell_NoBinaryArgs(t *testing.T) {
	argv := buildTestArgv("zsh", nil, nil)
	tail := trailingAfterImage(argv)
	if len(tail) == 0 || tail[0] != "zsh" {
		t.Errorf("expected zsh at end, got: %v", tail)
	}
	if len(tail) != 1 {
		t.Errorf("expected no extra args for plain shell, got: %v", tail)
	}
}

func TestShell_WithPassthroughArgs(t *testing.T) {
	argv := buildTestArgv("zsh", nil, []string{"ls", "-la"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if joined != "zsh ls -la" {
		t.Errorf("expected 'zsh ls -la', got: %q", joined)
	}
}

func TestShell_WithPythonScript(t *testing.T) {
	argv := buildTestArgv("zsh", nil, []string{"python3", "script.py"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if joined != "zsh python3 script.py" {
		t.Errorf("expected 'zsh python3 script.py', got: %q", joined)
	}
}

func TestShell_NoDefaultFlags(t *testing.T) {
	argv := buildTestArgv("zsh", nil, nil)
	tail := trailingAfterImage(argv)
	// zsh must be the only item (no injected flags)
	if len(tail) != 1 {
		t.Errorf("shell should have no default flags, got tail: %v", tail)
	}
}

// --- behavioural: VNC port determinism ---

func TestVNCPort_SamePaneSamePort(t *testing.T) {
	argv1 := buildTestArgv("claude", nil, nil, "TMUX_PANE", "%3")
	argv2 := buildTestArgv("bash", nil, nil, "TMUX_PANE", "%3")
	port1 := extractPort(argv1)
	port2 := extractPort(argv2)
	if port1 != port2 {
		t.Errorf("same TMUX_PANE should yield same VNCPort: %q != %q", port1, port2)
	}
}

func TestVNCPort_DifferentPanesDifferentPorts(t *testing.T) {
	guiCfg := cfg.CellConfig{Cell: cfg.CellSection{GUI: ptrBool(true)}}
	argv3 := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%3"}, "claude", nil, nil, guiCfg)
	argv4 := buildBehaviourArgv("/tmp/myproject", []string{"TMUX_PANE", "%4"}, "claude", nil, nil, guiCfg)
	port3 := extractPort(argv3)
	port4 := extractPort(argv4)
	if port3 == port4 {
		t.Errorf("different panes should yield different VNCPorts: both %q", port3)
	}
}

func extractPort(argv []string) string {
	for i, a := range argv {
		if a == "-p" && i+1 < len(argv) {
			p := argv[i+1]
			// "350:5900" → "350"
			colon := strings.Index(p, ":")
			if colon > 0 {
				return p[:colon]
			}
		}
	}
	return ""
}
