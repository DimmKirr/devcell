package runner

import (
	"strings"
	"testing"
)

func TestClassifyDockerRuntime(t *testing.T) {
	tests := []struct {
		name   string
		daemon string
		os     string
		labels []string
		want   string
	}{
		{name: "desktop OS", daemon: "docker-desktop", os: "Docker Desktop", want: "docker-desktop"},
		{name: "desktop label", daemon: "linux", labels: []string{"com.docker.desktop.address=x"}, want: "docker-desktop"},
		{name: "colima", daemon: "colima", os: "Ubuntu", want: "colima"},
		{name: "plain docker", daemon: "builder-1", os: "Ubuntu", want: "docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDockerRuntime(tt.daemon, tt.os, tt.labels); got != tt.want {
				t.Fatalf("classifyDockerRuntime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerBindProbeArgvUsesStrictBindMount(t *testing.T) {
	argv := DockerBindProbeArgv("devcell:test", "devcell-nix-store", "/Users/me/project", ".devcell.toml")
	joined := strings.Join(argv, "\n")
	if !strings.Contains(joined, "type=bind,src=/Users/me/project,dst=/__devcell_bind_probe,readonly") {
		t.Fatalf("probe must use strict --mount bind syntax: %v", argv)
	}
	if strings.Contains(joined, "/Users/me/project:/__devcell_bind_probe") {
		t.Fatalf("probe must not use legacy -v for the host path: %v", argv)
	}
	if !strings.Contains(joined, "devcell-nix-store:/nix") {
		t.Fatalf("thin probe must attach the Nix volume: %v", argv)
	}
	if !strings.Contains(joined, "/__devcell_bind_probe/.devcell.toml") {
		t.Fatalf("probe marker missing: %v", argv)
	}
}
