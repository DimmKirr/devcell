package config

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all runtime variables resolved from environment and cwd.
type Config struct {
	Bunk        string
	AppName       string
	CellName   string
	CellHome      string
	ConfigDir     string
	BuildDir      string // build context dir: .devcell/ when project config exists, else ConfigDir
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
	bunk := resolveBunk(getenv)
	cellName := resolveCellName(getenv)
	portPrefix := resolvePortPrefix(getenv, bunk)
	appName := filepath.Base(cwd) + "-" + bunk
	home := getenv("HOME")
	imageTag := "v0.0.0-ultimate"

	if tag := getenv("IMAGE_TAG"); tag != "" {
		imageTag = tag
	}

	configDir := resolveConfigDir(getenv)
	return Config{
		Bunk:        bunk,
		AppName:       appName,
		CellName:   cellName,
		CellHome:      home + "/.devcell/" + cellName,
		ConfigDir:     configDir,
		BuildDir:      configDir,
		ImageTag:      imageTag,
		Image:         "ghcr.io/devcell-sh/devcell:" + imageTag,
		ContainerName: "cell-" + appName + "-run",
		Hostname:      "cell-" + appName,
		PortPrefix:    portPrefix,
		VNCPort:       clampPort(portPrefix + "50"),
		RDPPort:       clampPort(portPrefix + "89"),
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
	c := Load(cwd, os.Getenv)
	_, statErr := os.Stat(filepath.Join(cwd, ".devcell.toml"))
	c.BuildDir = ResolveBuildDir(cwd, c.ConfigDir, statErr == nil)
	return c, nil
}

// ResolveBuildDir returns the build context directory.
// When projectConfigExists is true, returns cwd/.devcell (project-local).
// Otherwise falls back to configDir (global).
func ResolveBuildDir(cwd, configDir string, projectConfigExists bool) string {
	if projectConfigExists {
		return filepath.Join(cwd, ".devcell")
	}
	return configDir
}

// resolveBunk derives the per-pane id that seeds the VNC/RDP port prefix.
// Priority: explicit DEVCELL_BUNK, then the active terminal multiplexer's pane id,
// then "0". tmux is checked before zellij so a tmux pane nested inside zellij
// (whose ZELLIJ* vars leak into the child) still keys off its real pane.
//   - tmux:   TMUX_PANE="%5"        → "5"
//   - zellij: ZELLIJ=0, ZELLIJ_PANE_ID="5" → "5"
func resolveBunk(getenv func(string) string) string {
	if v := getenv("DEVCELL_BUNK"); v != "" {
		return v
	}
	if pane := getenv("TMUX_PANE"); pane != "" {
		return strings.TrimPrefix(pane, "%")
	}
	// ZELLIJ is set (to "0") only inside a zellij session; guard on it so a
	// stray ZELLIJ_PANE_ID can't hijack the fallback.
	if getenv("ZELLIJ") != "" {
		if pane := getenv("ZELLIJ_PANE_ID"); pane != "" {
			return pane
		}
	}
	return "0"
}

func resolveCellName(getenv func(string) string) string {
	if s := getenv("DEVCELL_CELL_NAME"); s != "" {
		return s
	}
	if s := getenv("TMUX_SESSION_NAME"); s != "" {
		return s
	}
	return "main"
}

func resolvePortPrefix(getenv func(string) string, bunk string) string {
	return getenv("SESSION_PORT_PREFIX") + bunk
}

func resolveConfigDir(getenv func(string) string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg + "/devcell"
	}
	return getenv("HOME") + "/.config/devcell"
}

// EnsureBuildDir creates the build context directory if it doesn't exist.
func EnsureBuildDir(buildDir string) error {
	return os.MkdirAll(buildDir, 0755)
}

// ResolveAvailablePorts checks whether VNCPort and RDPPort are free and
// replaces them with nearby available ports when they are already bound.
func (c *Config) ResolveAvailablePorts() {
	// Gather docker-allocated host ports once: on Docker Desktop (linuxkit VM)
	// and native Linux with userland-proxy disabled, published ports never open
	// a host-side socket, so net.Listen alone can't see them (CELL-119).
	taken := dockerAllocatedPorts()
	c.VNCPort = resolveAvailablePort(c.VNCPort, taken)
	c.RDPPort = resolveAvailablePort(c.RDPPort, taken)
}

// dockerAllocatedPorts returns the set of host ports currently published by
// running docker containers. Degrades gracefully: any error (docker absent,
// daemon down) yields an empty set, falling back to net.Listen-only probing.
func dockerAllocatedPorts() map[int]struct{} {
	out, err := exec.Command("docker", "ps", "--format", "{{.Ports}}").Output()
	if err != nil {
		return map[int]struct{}{}
	}
	return parseDockerPublishedPorts(string(out))
}

// parseDockerPublishedPorts extracts host ports from `docker ps --format
// '{{.Ports}}'` output. Each container's mappings are comma-separated, e.g.
// "0.0.0.0:25689->3389/tcp, :::25689->3389/tcp". The host port is the number
// after the last ":" before "->" — which handles IPv4 (0.0.0.0:P), IPv6
// (:::P or [::]:P) alike. Mappings without a "->" (exposed-but-not-published)
// and non-numeric host ports (ranges) are skipped. Pure — no I/O.
func parseDockerPublishedPorts(psOutput string) map[int]struct{} {
	ports := map[int]struct{}{}
	for _, line := range strings.Split(psOutput, "\n") {
		for _, mapping := range strings.Split(line, ",") {
			mapping = strings.TrimSpace(mapping)
			arrow := strings.Index(mapping, "->")
			if arrow < 0 {
				continue
			}
			hostPart := mapping[:arrow]
			colon := strings.LastIndex(hostPart, ":")
			if colon < 0 {
				continue
			}
			p, err := strconv.Atoi(hostPart[colon+1:])
			if err != nil {
				continue
			}
			ports[p] = struct{}{}
		}
	}
	return ports
}

// resolveAvailablePort returns preferred if it's free, otherwise scans
// upward (up to 100 attempts) for the next available port.
//
// Privileged-range fix (2026-05-15): single-digit TMUX panes produce ports
// like 489/450 (pane "4" + "89"/"50"), which are below 1024 — the kernel
// only lets root bind there. `net.Listen` from a user-mode `cell` fails
// with EACCES, and `isPortAvailable` can't distinguish that from EADDRINUSE
// → every candidate looks "unavailable" → fallback to the conflicting port.
// Bump <1024 to ≥1024 BEFORE the scan so dockerd's bind actually succeeds
// (dockerd is root but the host already has the port allocated to another
// container, which is the real collision we need to detect).
func resolveAvailablePort(preferred string, taken map[int]struct{}) string {
	port, err := strconv.Atoi(preferred)
	if err != nil {
		return preferred
	}
	// Hoist privileged ports into the user range. +10000 keeps the
	// last-3-digits-by-pane intuition while moving above the wall.
	if port < 1024 {
		port += 10000
	}
	for i := 0; i < 100; i++ {
		candidate := port + i
		if candidate > 65535 {
			break
		}
		// A candidate is unavailable if docker already published it (taken)
		// OR the kernel won't let us bind it. The taken check catches the
		// Docker Desktop case where the port has no host socket (CELL-119).
		if _, used := taken[candidate]; used {
			continue
		}
		if isPortAvailable(candidate) {
			return strconv.Itoa(candidate)
		}
	}
	return strconv.Itoa(port)
}

// clampPort ensures a port string represents a valid TCP port (1024–65535).
// If the value exceeds 65535, it subtracts 65535 repeatedly until it fits,
// then floors at 1024 to stay out of the privileged range.
// Pure arithmetic — no I/O. Port availability is handled by ResolveAvailablePorts.
func clampPort(s string) string {
	p, err := strconv.Atoi(s)
	if err != nil || p <= 65535 {
		return s
	}
	for p > 65535 {
		p -= 65535
	}
	if p < 1024 {
		p += 1024
	}
	return strconv.Itoa(p)
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
