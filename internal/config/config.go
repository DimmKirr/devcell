package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	BaseDir       string
	HostUser      string
	HostHome      string
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
		BaseDir:       cwd,
		HostUser:      getenv("USER"),
		HostHome:      home,
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
	if s := getenv("TMUX_SESSION_NAME"); s != "" {
		return s
	}
	return "sandbox"
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
