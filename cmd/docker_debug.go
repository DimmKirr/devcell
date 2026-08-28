package main

import (
	"context"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
)

func logDockerDiagnostics(ctx context.Context, c config.Config) {
	if !ux.Verbose {
		return
	}
	info, err := runner.CollectDockerDebugInfo(ctx)
	if err != nil {
		ux.Debugf("docker diagnostics: %v", err)
		return
	}
	ux.Debugf("docker client: context=%q endpoint=%q DOCKER_HOST=%q",
		info.Context, info.Endpoint, info.DockerHostEnv)
	ux.Debugf("docker daemon: runtime=%s name=%q version=%s os=%q arch=%s cpus=%d memory=%s root=%s",
		info.Runtime, info.Name, info.ServerVersion, info.OperatingOS,
		info.Architecture, info.CPUs, runner.HumanBytes(info.MemoryBytes), info.RootDir)
	ux.Debugf("docker socket: path=%q resolved=%q", info.Socket, info.SocketTarget)

	hostProject := os.Getenv("DEVCELL_HOST_PROJECT_DIR")
	ux.Debugf("docker paths: base=%q build=%q DEVCELL_HOST_PROJECT_DIR=%q daemon-build-source=%q",
		c.BaseDir, c.BuildDir, hostProject, runner.DockerHostPath(c.BuildDir))
	volume := runner.ThinStoreVolume()
	if detail := runner.DockerVolumeDebug(ctx, volume); detail != "" {
		ux.Debugf("docker nix volume: %s", detail)
	} else {
		ux.Debugf("docker nix volume: name=%s absent", volume)
	}
}
