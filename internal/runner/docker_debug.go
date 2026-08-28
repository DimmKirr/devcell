package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DockerDebugInfo describes the client-to-daemon connection used by Cell.
// It deliberately excludes registry and authentication configuration.
type DockerDebugInfo struct {
	Context       string
	Endpoint      string
	DockerHostEnv string
	Runtime       string
	Name          string
	ServerVersion string
	OperatingOS   string
	Architecture  string
	RootDir       string
	CPUs          int
	MemoryBytes   int64
	Socket        string
	SocketTarget  string
}

// CollectDockerDebugInfo reports the actual daemon selected by the current
// Docker CLI environment. This matters inside a cell, where /var/run/docker.sock
// was fixed when the outer container was created.
func CollectDockerDebugInfo(ctx context.Context) (DockerDebugInfo, error) {
	var info DockerDebugInfo
	info.DockerHostEnv = strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	info.Context = dockerOutput(ctx, "context", "show")
	if info.DockerHostEnv != "" {
		info.Endpoint = info.DockerHostEnv
	} else {
		info.Endpoint = dockerOutput(ctx, "context", "inspect", info.Context,
			"--format", `{{(index .Endpoints "docker").Host}}`)
	}

	raw, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").Output()
	if err != nil {
		return info, fmt.Errorf("docker info: %w", err)
	}
	var daemon struct {
		Name            string
		ServerVersion   string
		OperatingSystem string
		Architecture    string
		DockerRootDir   string
		NCPU            int
		MemTotal        int64
		Labels          []string
	}
	if err := json.Unmarshal(raw, &daemon); err != nil {
		return info, fmt.Errorf("decode docker info: %w", err)
	}
	info.Name = daemon.Name
	info.ServerVersion = daemon.ServerVersion
	info.OperatingOS = daemon.OperatingSystem
	info.Architecture = daemon.Architecture
	info.RootDir = daemon.DockerRootDir
	info.CPUs = daemon.NCPU
	info.MemoryBytes = daemon.MemTotal
	info.Runtime = classifyDockerRuntime(daemon.Name, daemon.OperatingSystem, daemon.Labels)

	if strings.HasPrefix(info.Endpoint, "unix://") {
		info.Socket = strings.TrimPrefix(info.Endpoint, "unix://")
	} else if info.Endpoint == "" || info.Endpoint == "unix:///var/run/docker.sock" {
		info.Socket = "/var/run/docker.sock"
	}
	if info.Socket != "" {
		if target, err := filepath.EvalSymlinks(info.Socket); err == nil {
			info.SocketTarget = target
		}
	}
	return info, nil
}

func dockerOutput(ctx context.Context, args ...string) string {
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func classifyDockerRuntime(name, operatingSystem string, labels []string) string {
	haystack := strings.ToLower(name + " " + operatingSystem + " " + strings.Join(labels, " "))
	switch {
	case strings.Contains(haystack, "docker desktop"),
		strings.Contains(haystack, "docker-desktop"),
		strings.Contains(haystack, "docker.desktop"):
		return "docker-desktop"
	case strings.Contains(haystack, "colima"):
		return "colima"
	default:
		return "docker"
	}
}

// ProbeDockerBind verifies both that the daemon accepts source and that marker
// is visible inside a container. It is intended for --debug diagnostics only.
func ProbeDockerBind(ctx context.Context, image, volume, source, marker string) (string, error) {
	args := DockerBindProbeArgv(image, volume, source, marker)
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// DockerBindProbeArgv composes the non-mutating debug probe command.
func DockerBindProbeArgv(image, volume, source, marker string) []string {
	const destination = "/__devcell_bind_probe"
	args := []string{
		"docker", "run", "--rm", "--network", "none", "--user", "0",
		"--mount", "type=bind,src=" + source + ",dst=" + destination + ",readonly",
	}
	if volume != "" {
		args = append(args, "-v", volume+":/nix")
	}
	args = append(args,
		"--entrypoint", "/bin/sh", image, "-c",
		`test -f "$1" && printf 'visible:%s\n' "$1" || { ls -la "`+destination+`" >&2; exit 42; }`,
		"probe", destination+"/"+marker,
	)
	return args
}

// DockerVolumeDebug returns daemon-side metadata for a named volume.
func DockerVolumeDebug(ctx context.Context, volume string) string {
	return dockerOutput(ctx, "volume", "inspect", volume, "--format",
		`name={{.Name}} driver={{.Driver}} scope={{.Scope}} mountpoint={{.Mountpoint}}`)
}
