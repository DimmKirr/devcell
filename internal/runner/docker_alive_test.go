package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// CELL-44: DockerDaemonReachable preempts the confusing "Pulling core image
// failed" message when docker isn't running, by probing the daemon once with
// `docker info`. Failure translates to a single-line actionable error.

func TestDockerDaemonReachable_ReturnsNilOnSuccess(t *testing.T) {
	orig := dockerInfoExec
	t.Cleanup(func() { dockerInfoExec = orig })
	dockerInfoExec = func(context.Context) error { return nil }

	if err := DockerDaemonReachable(context.Background()); err != nil {
		t.Errorf("want nil, got: %v", err)
	}
}

func TestDockerDaemonReachable_ReturnsActionableErrorOnFailure(t *testing.T) {
	orig := dockerInfoExec
	t.Cleanup(func() { dockerInfoExec = orig })
	dockerInfoExec = func(context.Context) error {
		return errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")
	}

	err := DockerDaemonReachable(context.Background())
	if err == nil {
		t.Fatal("want err when docker info fails, got nil")
	}
	if !strings.Contains(err.Error(), "Docker") {
		t.Errorf("error must mention Docker: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "start") {
		t.Errorf("error must include an actionable 'start' hint: %v", err)
	}
}
