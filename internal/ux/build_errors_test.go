package ux

import (
	"strings"
	"testing"
)

func TestClassifyBuildOutput_NoSpace(t *testing.T) {
	output := `#8 0.128 mktemp: failed to create directory via template '/tmp/home-manager-build.XXXXXXXXXX': No space left on device
#8 ERROR: process did not complete successfully: exit code: 1`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for no-space output, got nil")
	}
	if !strings.Contains(hint.Title, "No space") {
		t.Errorf("unexpected title: %q", hint.Title)
	}
	if len(hint.Fixes) == 0 {
		t.Error("expected at least one fix suggestion")
	}
}

func TestClassifyBuildOutput_FetchError(t *testing.T) {
	output := `error: failed to fetch https://cache.nixos.org/abc.narinfo: connection refused`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for fetch error, got nil")
	}
	if !strings.Contains(hint.Title, "fetch") {
		t.Errorf("unexpected title: %q", hint.Title)
	}
}

func TestClassifyBuildOutput_NetworkError(t *testing.T) {
	output := `dial tcp 1.2.3.4:443: i/o timeout`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for network error, got nil")
	}
	if !strings.Contains(strings.ToLower(hint.Title), "network") {
		t.Errorf("unexpected title: %q", hint.Title)
	}
}

func TestClassifyBuildOutput_NixAttribute(t *testing.T) {
	output := `error: attribute 'pkgs.alejnadra' missing`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for nix attribute error, got nil")
	}
}

func TestClassifyBuildOutput_NoMatch(t *testing.T) {
	output := `some completely unknown build failure text`
	hint := ClassifyBuildOutput(output)
	if hint != nil {
		t.Errorf("expected nil for unrecognized output, got %+v", hint)
	}
}

func TestClassifyBuildOutput_CaseInsensitive(t *testing.T) {
	output := `NO SPACE LEFT ON DEVICE`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for uppercase pattern, got nil")
	}
}

func TestClassifyBuildOutput_DockerDaemon(t *testing.T) {
	output := `ERROR: Cannot connect to the Docker daemon at unix:///Users/dmitry/.docker/run/docker.sock. Is the docker daemon running?
Error: docker build: exit status 1`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint for docker daemon error, got nil")
	}
	if !strings.Contains(strings.ToLower(hint.Title), "daemon") {
		t.Errorf("unexpected title: %q", hint.Title)
	}
}

func TestClassifyBuildOutput_GenericDockerFallback(t *testing.T) {
	output := `Error: docker build: exit status 1`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected fallback hint for generic docker build failure, got nil")
	}
	if !strings.Contains(strings.ToLower(hint.Title), "docker build failed") {
		t.Errorf("unexpected title: %q", hint.Title)
	}
}

func TestClassifyBuildOutput_DockerDaemonBeforeFallback(t *testing.T) {
	// daemon error must match before the generic fallback even though both patterns present
	output := `Cannot connect to the Docker daemon at unix:///var/run/docker.sock
Error: docker build: exit status 1`
	hint := ClassifyBuildOutput(output)
	if hint == nil {
		t.Fatal("expected hint, got nil")
	}
	if !strings.Contains(strings.ToLower(hint.Title), "daemon") {
		t.Errorf("daemon pattern should win over fallback, got title: %q", hint.Title)
	}
}
