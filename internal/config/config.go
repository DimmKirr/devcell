package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all runtime variables resolved from environment and cwd.
type Config struct {
	CellID        string
	AppName       string
	SessionName   string
	CellHome      string
	ConfigDir     string
	ImageTag      string
	Image         string
	ContainerName string
	Hostname      string
	PortPrefix    string
	VNCPort       string
	RDPPort       string
	BaseDir       string
	HostUser      string
	HostHome      string
	LocalMode     bool // DEVCELL_LOCAL_MODE=true — always rebuild image on run
}

// Load resolves all config fields from cwd and an environment lookup function.
// Pure — no os.* calls inside.
func Load(cwd string, getenv func(string) string) Config {
	cellID := resolveCellID(getenv)
	sessionName := resolveSessionName(getenv)
	portPrefix := resolvePortPrefix(getenv, cellID)
	appName := filepath.Base(cwd) + "-" + cellID
	home := getenv("HOME")
	imageTag := "latest-ultimate"

	if tag := getenv("IMAGE_TAG"); tag != "" {
		imageTag = tag
	}

	return Config{
		CellID:        cellID,
		AppName:       appName,
		SessionName:   sessionName,
		CellHome:      home + "/.devcell/" + sessionName,
		ConfigDir:     resolveConfigDir(getenv),
		ImageTag:      imageTag,
		Image:         "ghcr.io/dimmkirr/devcell:" + imageTag,
		ContainerName: "cell-" + appName + "-run",
		Hostname:      "cell-" + appName,
		PortPrefix:    portPrefix,
		VNCPort:       portPrefix + "50",
		RDPPort:       portPrefix + "89",
		BaseDir:       cwd,
		HostUser:      getenv("USER"),
		HostHome:      home,
		LocalMode:     getenv("DEVCELL_LOCAL_MODE") == "true",
	}
}

// LoadFromOS resolves config using the real OS environment and working directory.
func LoadFromOS() (Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("getwd: %w", err)
	}
	return Load(cwd, os.Getenv), nil
}

func resolveCellID(getenv func(string) string) string {
	if v := getenv("CELL_ID"); v != "" {
		return v
	}
	if pane := getenv("TMUX_PANE"); pane != "" {
		return strings.TrimPrefix(pane, "%")
	}
	return "0"
}

func resolveSessionName(getenv func(string) string) string {
	if s := getenv("DEVCELL_SESSION_NAME"); s != "" {
		return s
	}
	if s := getenv("TMUX_SESSION_NAME"); s != "" {
		return s
	}
	return "main"
}

func resolvePortPrefix(getenv func(string) string, cellID string) string {
	return getenv("SESSION_PORT_PREFIX") + cellID
}

func resolveConfigDir(getenv func(string) string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg + "/devcell"
	}
	return getenv("HOME") + "/.config/devcell"
}

// ResolveAvailablePorts checks whether VNCPort and RDPPort are free and
// replaces them with nearby available ports when they are already bound.
func (c *Config) ResolveAvailablePorts() {
	c.VNCPort = resolveAvailablePort(c.VNCPort)
	c.RDPPort = resolveAvailablePort(c.RDPPort)
}

// resolveAvailablePort returns preferred if it's free, otherwise scans
// upward (up to 100 attempts) for the next available port.
func resolveAvailablePort(preferred string) string {
	port, err := strconv.Atoi(preferred)
	if err != nil {
		return preferred
	}
	for i := 0; i < 100; i++ {
		candidate := port + i
		if candidate > 65535 {
			break
		}
		if isPortAvailable(candidate) {
			return strconv.Itoa(candidate)
		}
	}
	return preferred
}

// isPortAvailable reports whether a TCP port can be bound on all interfaces.
func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
