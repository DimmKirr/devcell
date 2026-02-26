package config_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/config"
)

func env(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

// --- CellID ---

func TestCellID_ExplicitCellID(t *testing.T) {
	c := config.Load("/cwd", env("CELL_ID", "3"))
	if c.CellID != "3" {
		t.Errorf("want 3, got %q", c.CellID)
	}
}

func TestCellID_FromTmuxPane(t *testing.T) {
	c := config.Load("/cwd", env("TMUX_PANE", "%5"))
	if c.CellID != "5" {
		t.Errorf("want 5, got %q", c.CellID)
	}
}

func TestCellID_TmuxPaneMultiDigit(t *testing.T) {
	c := config.Load("/cwd", env("TMUX_PANE", "%12"))
	if c.CellID != "12" {
		t.Errorf("want 12, got %q", c.CellID)
	}
}

func TestCellID_FallbackZero(t *testing.T) {
	c := config.Load("/cwd", env())
	if c.CellID != "0" {
		t.Errorf("want 0, got %q", c.CellID)
	}
}

func TestCellID_CellIDTakesPriorityOverTmux(t *testing.T) {
	c := config.Load("/cwd", env("CELL_ID", "7", "TMUX_PANE", "%3"))
	if c.CellID != "7" {
		t.Errorf("want 7, got %q", c.CellID)
	}
}

// --- AppName ---

func TestAppName_Basic(t *testing.T) {
	c := config.Load("/Users/bob/dev/myproject", env("CELL_ID", "3"))
	if c.AppName != "myproject-3" {
		t.Errorf("want myproject-3, got %q", c.AppName)
	}
}

func TestAppName_WithSpaces(t *testing.T) {
	c := config.Load("/Users/bob/My Project", env("CELL_ID", "0"))
	// Should not crash; AppName should be non-empty
	if c.AppName == "" {
		t.Error("AppName must not be empty for path with spaces")
	}
}

// --- SessionName / CellHome ---

func TestCellHome_WithTmuxSession(t *testing.T) {
	c := config.Load("/cwd", env("TMUX_SESSION_NAME", "work", "HOME", "/home/bob"))
	if c.CellHome != "/home/bob/.devcell/work" {
		t.Errorf("want /home/bob/.devcell/work, got %q", c.CellHome)
	}
}

func TestCellHome_DefaultSandbox(t *testing.T) {
	c := config.Load("/cwd", env("HOME", "/home/bob"))
	if c.CellHome != "/home/bob/.devcell/sandbox" {
		t.Errorf("want /home/bob/.devcell/sandbox, got %q", c.CellHome)
	}
}

// --- ConfigDir ---

func TestConfigDir_WithXDG(t *testing.T) {
	c := config.Load("/cwd", env("XDG_CONFIG_HOME", "/tmp/xdg"))
	if c.ConfigDir != "/tmp/xdg/devcell" {
		t.Errorf("want /tmp/xdg/devcell, got %q", c.ConfigDir)
	}
}

func TestConfigDir_DefaultHome(t *testing.T) {
	c := config.Load("/cwd", env("HOME", "/home/bob"))
	if c.ConfigDir != "/home/bob/.config/devcell" {
		t.Errorf("want /home/bob/.config/devcell, got %q", c.ConfigDir)
	}
}

// --- PortPrefix / VNCPort ---

func TestPortPrefix_NoPrefixCellID3(t *testing.T) {
	c := config.Load("/cwd", env("CELL_ID", "3"))
	if c.PortPrefix != "3" {
		t.Errorf("want 3, got %q", c.PortPrefix)
	}
}

func TestPortPrefix_WithPrefix(t *testing.T) {
	c := config.Load("/cwd", env("SESSION_PORT_PREFIX", "1", "CELL_ID", "3"))
	if c.PortPrefix != "13" {
		t.Errorf("want 13, got %q", c.PortPrefix)
	}
}

func TestVNCPort_CellID3(t *testing.T) {
	c := config.Load("/cwd", env("CELL_ID", "3"))
	if c.VNCPort != "350" {
		t.Errorf("want 350, got %q", c.VNCPort)
	}
}

func TestVNCPort_CellID12(t *testing.T) {
	c := config.Load("/cwd", env("CELL_ID", "12"))
	if c.VNCPort != "1250" {
		t.Errorf("want 1250, got %q", c.VNCPort)
	}
}

func TestVNCPort_ParseableAsUint16(t *testing.T) {
	for _, cellID := range []string{"0", "1", "3", "9", "12"} {
		c := config.Load("/cwd", env("CELL_ID", cellID))
		n, err := strconv.ParseUint(c.VNCPort, 10, 16)
		if err != nil || n == 0 {
			t.Errorf("CellID=%s VNCPort=%q is not a valid uint16 port", cellID, c.VNCPort)
		}
	}
}

// --- ContainerName ---

func TestContainerName(t *testing.T) {
	c := config.Load("/myproject", env("CELL_ID", "3"))
	if c.ContainerName != "cell-myproject-3-run" {
		t.Errorf("want cell-myproject-3-run, got %q", c.ContainerName)
	}
}

func TestContainerName_NoSpacesOrSlashes(t *testing.T) {
	c := config.Load("/some/deep/path", env("CELL_ID", "0"))
	if strings.ContainsAny(c.ContainerName, " /") {
		t.Errorf("ContainerName must not contain spaces or slashes: %q", c.ContainerName)
	}
}

// --- Image ---

func TestImage_Default(t *testing.T) {
	c := config.Load("/cwd", env())
	if c.Image != "ghcr.io/dimmkirr/devcell:latest-ultimate" {
		t.Errorf("unexpected default image: %q", c.Image)
	}
}

// --- Purity ---

func TestLoad_Idempotent(t *testing.T) {
	e := env("CELL_ID", "5", "HOME", "/home/bob", "TMUX_SESSION_NAME", "work")
	c1 := config.Load("/myproject", e)
	c2 := config.Load("/myproject", e)
	if c1 != c2 {
		t.Errorf("Load not idempotent: %+v != %+v", c1, c2)
	}
}
