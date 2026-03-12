package vnc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vncPasswdBytes is the DES-encrypted VNC password for "vnc".
// Pre-computed using the standard VNC fixed key — avoids needing vncpasswd at runtime.
var vncPasswdBytes = []byte{0x91, 0xbc, 0x75, 0xc1, 0x8d, 0x3d, 0x85, 0xa7}

// VNCPasswdFile returns the path to a VNC password file containing the
// encrypted password "vnc". Creates the file on first call.
func VNCPasswdFile() string {
	p := filepath.Join(os.TempDir(), "devcell-vnc-passwd")
	if _, err := os.Stat(p); err != nil {
		os.WriteFile(p, vncPasswdBytes, 0600)
	}
	return p
}

// ContainerMatch holds data about a container matching a host-dir bind mount.
type ContainerMatch struct {
	Name    string // full container name, e.g. "cell-devcell-73-run"
	AppName string // stripped name, e.g. "devcell-73"
	Port    string // VNC host port
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

// FindContainersByBind parses docker inspect JSON (one or many containers)
// and returns all that have hostDir as the source of a bind mount and have
// port 5900 published.
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
		bindings, ok := r.NetworkSettings.Ports["5900/tcp"]
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

// VNCUrl returns a VNC URL for macOS Screen Sharing.
func VNCUrl(port string) string {
	return "vnc://:vnc@127.0.0.1:" + port
}

// RoyalTSXVNCUrl returns a Royal TSX URI for a VNC connection.
func RoyalTSXVNCUrl(port string) string {
	return "rtsx://vnc://:vnc@127.0.0.1:" + port
}

// ParseDockerPS parses the output of:
//
//	docker ps --filter "name=cell-" --format "{{.Names}}\t{{.Ports}}"
//
// and returns a map of AppName → VNC host port.
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
		name := parts[0]   // e.g. cell-myproject-3-run
		ports := parts[1]  // e.g. 0.0.0.0:350->5900/tcp

		// Extract host port for 5900
		hostPort, ok := extract5900Port(ports)
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

// extract5900Port finds a host port mapping for container port 5900 in a docker ps Ports string.
// Format: "0.0.0.0:350->5900/tcp, 0.0.0.0:8080->80/tcp"
func extract5900Port(ports string) (string, bool) {
	for _, segment := range strings.Split(ports, ",") {
		segment = strings.TrimSpace(segment)
		if !strings.Contains(segment, "->5900/") {
			continue
		}
		// "0.0.0.0:350->5900/tcp" → hostPort "350"
		arrow := strings.Index(segment, "->")
		if arrow < 0 {
			continue
		}
		hostPart := segment[:arrow] // "0.0.0.0:350"
		colon := strings.LastIndex(hostPart, ":")
		if colon < 0 {
			continue
		}
		return hostPart[colon+1:], true
	}
	return "", false
}

// inspectPort5900 is the structure we decode from docker inspect JSON.
type inspectResult struct {
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIp   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// ParseInspectPort extracts the host port for container port 5900 from docker inspect JSON output.
func ParseInspectPort(inspectJSON string) (string, error) {
	var results []inspectResult
	if err := json.Unmarshal([]byte(inspectJSON), &results); err != nil {
		return "", fmt.Errorf("parse inspect JSON: %w", err)
	}
	if len(results) == 0 {
		return "", fmt.Errorf("empty inspect result")
	}
	bindings, ok := results[0].NetworkSettings.Ports["5900/tcp"]
	if !ok || len(bindings) == 0 {
		return "", fmt.Errorf("5900/tcp not published")
	}
	return bindings[0].HostPort, nil
}
