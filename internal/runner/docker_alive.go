package runner

import (
	"context"
	"errors"
	"os/exec"
)

// ErrDockerDaemonUnreachable is returned by DockerDaemonReachable when the
// docker daemon doesn't respond. Stable so callers can match on it.
var ErrDockerDaemonUnreachable = errors.New("Docker is not running. Please start Docker and retry")

// DockerDaemonReachable probes the daemon with a single `docker info` call.
// Cheap (~30ms warm) and avoids the confusing fallout (`Pulling core image
// nixos/nix:latest failed`) when the socket is unreachable. Call once at the
// top of any path that will later invoke docker (CELL-44).
func DockerDaemonReachable(ctx context.Context) error {
	if err := dockerInfoExec(ctx); err != nil {
		return ErrDockerDaemonUnreachable
	}
	return nil
}

// dockerInfoExec is swappable for tests. `--format '{{.ID}}'` keeps output
// short on success and never prints the full daemon info block.
var dockerInfoExec = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Run()
}
