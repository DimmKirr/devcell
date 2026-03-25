package rdp

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ContainerMatch holds data about a container with RDP port published.
type ContainerMatch struct {
	Name    string // full container name, e.g. "cell-devcell-73-run"
	AppName string // stripped name, e.g. "devcell-73"
	Port    string // RDP host port
}

// fullInspectResult is the structure decoded from docker inspect JSON.
type fullInspectResult struct {
	Name       string `json:"Name"`
	HostConfig struct {
		Binds []string `json:"Binds"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIp   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// RDPUrl returns an RDP URL for the given port.
// Uses percent-encoded format required by macOS Sonoma+ Windows App:
//
//	rdp://full%20address=s%3A127.0.0.1%3A<port>
func RDPUrl(port string) string {
	return "rdp://full%20address=s%3A127.0.0.1%3A" + port
}

// RoyalTSXUrl returns a Royal TSX URI for the given port and credentials.
// Note: macOS Royal TSX does not support property_* query params in adhoc URIs.
// Retina must be enabled via Application → Default Settings → Remote Desktop → Display.
func RoyalTSXUrl(port, user, password string) string {
	return "rtsx://rdp://" + user + ":" + password + "@127.0.0.1:" + port
}

// HasRoyalTSX checks if Royal TSX is installed on macOS.
func HasRoyalTSX() bool {
	paths := []string{
		"/Applications/Royal TSX.app",
		filepath.Join(os.Getenv("HOME"), "Applications", "Royal TSX.app"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// ClientBinary holds the resolved FreeRDP binary path and name.
type ClientBinary struct {
	Path string // absolute path from LookPath
	Name string // binary name (e.g. "sdl-freerdp3")
}

// freerdpCandidates returns binary names in preference order for the given OS.
// macOS: prefer SDL variants (no X11 needed); Linux: prefer X11 variants.
func freerdpCandidates(goos string) []string {
	if goos == "darwin" {
		return []string{"sdl-freerdp3", "sdl-freerdp", "xfreerdp3", "xfreerdp"}
	}
	return []string{"xfreerdp3", "xfreerdp", "sdl-freerdp3", "sdl-freerdp"}
}

// FindClient finds the best available FreeRDP client binary for the current platform.
func FindClient() (ClientBinary, bool) {
	return FindClientWith(runtime.GOOS, exec.LookPath)
}

// FindClientWith is like FindClient but accepts explicit OS and lookPath for testing.
func FindClientWith(goos string, lookPath func(string) (string, error)) (ClientBinary, bool) {
	for _, name := range freerdpCandidates(goos) {
		if path, err := lookPath(name); err == nil {
			return ClientBinary{Path: path, Name: name}, true
		}
	}
	return ClientBinary{}, false
}

// InstallHint returns platform-specific install instructions for FreeRDP.
func InstallHint() string {
	return "xfreerdp not found — install FreeRDP and retry:\n\n" +
		"  macOS:   brew install freerdp\n" +
		"  nixpkgs: nix profile install nixpkgs#freerdp\n" +
		"  Debian:  sudo apt install freerdp3-x11\n" +
		"  Fedora:  sudo dnf install freerdp\n" +
		"  Arch:    sudo pacman -S freerdp\n"
}

// CertFingerprint reads the xrdp server cert from configDir/xrdp/cert.pem
// and returns its SHA256 fingerprint as a hex string (colon-separated).
// Returns empty string if the cert doesn't exist or can't be parsed.
func CertFingerprint(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, "xrdp", "cert.pem"))
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = hex.EncodeToString([]byte{b})
	}
	return strings.Join(parts, ":")
}

// CertFlag returns the FreeRDP /cert: flag. Uses fingerprint verification
// if the cert exists, otherwise falls back to /cert:ignore.
func CertFlag(configDir string) string {
	if fp := CertFingerprint(configDir); fp != "" {
		return "/cert:fingerprint:sha256:" + fp
	}
	return "/cert:ignore"
}

// ParseDockerPS parses the output of:
//
//	docker ps --filter "name=cell-" --format "{{.Names}}\t{{.Ports}}"
//
// and returns a map of AppName -> RDP host port.
func ParseDockerPS(output string) (map[string]string, error) {
	result := map[string]string{}
	output = strings.TrimSpace(output)
	if output == "" {
		return result, nil
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]  // e.g. cell-myproject-3-run
		ports := parts[1] // e.g. 0.0.0.0:389->3389/tcp

		// Extract host port for 3389
		hostPort, ok := extract3389Port(ports)
		if !ok {
			continue
		}

		// Strip "cell-" prefix and "-run" suffix to get AppName
		appName := strings.TrimPrefix(name, "cell-")
		appName = strings.TrimSuffix(appName, "-run")
		result[appName] = hostPort
	}
	return result, nil
}

// extract3389Port finds a host port mapping for container port 3389 in a docker ps Ports string.
// Format: "0.0.0.0:389->3389/tcp, 0.0.0.0:8080->80/tcp"
func extract3389Port(ports string) (string, bool) {
	for _, segment := range strings.Split(ports, ",") {
		segment = strings.TrimSpace(segment)
		if !strings.Contains(segment, "->3389/") {
			continue
		}
		// "0.0.0.0:389->3389/tcp" -> hostPort "389"
		arrow := strings.Index(segment, "->")
		if arrow < 0 {
			continue
		}
		hostPart := segment[:arrow] // "0.0.0.0:389"
		colon := strings.LastIndex(hostPart, ":")
		if colon < 0 {
			continue
		}
		return hostPart[colon+1:], true
	}
	return "", false
}

// inspectResult is the structure we decode from docker inspect JSON.
type inspectResult struct {
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIp   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// ParseInspectPort extracts the host port for container port 3389 from docker inspect JSON output.
func ParseInspectPort(inspectJSON string) (string, error) {
	var results []inspectResult
	if err := json.Unmarshal([]byte(inspectJSON), &results); err != nil {
		return "", fmt.Errorf("parse inspect JSON: %w", err)
	}
	if len(results) == 0 {
		return "", fmt.Errorf("empty inspect result")
	}
	bindings, ok := results[0].NetworkSettings.Ports["3389/tcp"]
	if !ok || len(bindings) == 0 {
		return "", fmt.Errorf("3389/tcp not published")
	}
	return bindings[0].HostPort, nil
}

// FindContainersByBind parses docker inspect JSON (one or many containers)
// and returns all that have hostDir as the source of a bind mount and have
// port 3389 published.
func FindContainersByBind(inspectJSON, hostDir string) ([]ContainerMatch, error) {
	var results []fullInspectResult
	if err := json.Unmarshal([]byte(inspectJSON), &results); err != nil {
		return nil, fmt.Errorf("parse inspect JSON: %w", err)
	}
	var matches []ContainerMatch
	for _, r := range results {
		if !hasBindSource(r.HostConfig.Binds, hostDir) {
			continue
		}
		bindings, ok := r.NetworkSettings.Ports["3389/tcp"]
		if !ok || len(bindings) == 0 {
			continue
		}
		name := strings.TrimPrefix(r.Name, "/")
		appName := strings.TrimPrefix(name, "cell-")
		appName = strings.TrimSuffix(appName, "-run")
		matches = append(matches, ContainerMatch{
			Name:    name,
			AppName: appName,
			Port:    bindings[0].HostPort,
		})
	}
	return matches, nil
}

// hasBindSource reports whether any bind in the list starts with "hostDir:".
func hasBindSource(binds []string, hostDir string) bool {
	prefix := hostDir + ":"
	for _, b := range binds {
		if strings.HasPrefix(b, prefix) {
			return true
		}
	}
	return false
}
