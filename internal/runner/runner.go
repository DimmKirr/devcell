package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

// FS abstracts filesystem stat for testability.
type FS interface {
	Stat(path string) error
}

// FSFunc is a function that implements FS.
type FSFunc func(string) error

func (f FSFunc) Stat(path string) error { return f(path) }

// OsFS is the real filesystem implementation.
var OsFS FS = FSFunc(func(path string) error {
	_, err := os.Stat(path)
	return err
})

// RunSpec holds everything needed to build the docker run argv.
type RunSpec struct {
	Config       config.Config
	CellCfg      cfg.CellConfig
	Binary       string
	DefaultFlags []string
	UserArgs     []string
	Debug        bool // pass DEVCELL_DEBUG=true into the container
}

// BuildArgv constructs the full docker run argv for the given spec.
// It is pure given injectable FS and LookPath.
func BuildArgv(spec RunSpec, fs FS, lookPath func(string) (string, error)) []string {
	c := spec.Config

	var argv []string

	// 1Password passthrough
	if opPath, err := lookPath("op"); err == nil && opPath != "" {
		argv = append(argv, "op", "run", "--")
	}

	argv = append(argv, "docker", "run", "--rm", "-it")

	// Identity
	argv = append(argv, "--name", c.ContainerName)
	argv = append(argv, "--hostname", c.Hostname)
	argv = append(argv, "--user", "0")
	argv = append(argv, "--group-add", "0")

	// Labels for VNC lookup: filter by basedir+cellid without inspecting all containers
	argv = append(argv, "--label", "devcell.basedir="+c.BaseDir)
	argv = append(argv, "--label", "devcell.cellid="+c.CellID)

	// Core env vars
	e := func(k, v string) { argv = append(argv, "-e", k+"="+v) }
	e("APP_NAME", c.AppName)
	e("HOST_USER", c.HostUser)
	e("HOME", "/home/"+c.HostUser)
	e("IS_SANDBOX", "1")
	e("WORKSPACE", "/"+c.AppName)
	e("TERM", os.Getenv("TERM"))
	e("HISTFILE", "/home/"+c.HostUser+"/zsh_history_"+c.AppName)
	e("TMPDIR", "/home/"+c.HostUser+"/tmp")
	e("CODEX_OSS_BASE_URL", envOrDefault("CODEX_OSS_BASE_URL", "http://host.docker.internal:1234/v1"))
	e("GIT_AUTHOR_NAME", envOrDefault("GIT_AUTHOR_NAME", "DevCell"))
	e("GIT_AUTHOR_EMAIL", envOrDefault("GIT_AUTHOR_EMAIL", "devcell@devcell.io"))
	e("GIT_COMMITTER_NAME", envOrDefault("GIT_COMMITTER_NAME", "DevCell"))
	e("GIT_COMMITTER_EMAIL", envOrDefault("GIT_COMMITTER_EMAIL", "devcell@devcell.io"))

	// Optional .env file
	envFile := filepath.Join(c.BaseDir, ".env")
	if err := fs.Stat(envFile); err == nil {
		argv = append(argv, "--env-file", envFile)
	}

	// GUI flag — only publish VNC port when GUI is enabled
	if spec.CellCfg.Cell.GUI {
		argv = append(argv, "-e", "DEVCELL_GUI_ENABLED=true")
		argv = append(argv, "-e", "EXT_VNC_PORT="+c.VNCPort)
	}

	// Debug flag — enables verbose entrypoint logging inside the container
	if spec.Debug {
		argv = append(argv, "-e", "DEVCELL_DEBUG=true")
	}

	// cfg [env] entries
	for k, v := range spec.CellCfg.Env {
		argv = append(argv, "-e", k+"="+v)
	}

	// Standard volumes
	v := func(mount string) { argv = append(argv, "-v", mount) }
	v(c.BaseDir + ":" + c.BaseDir)
	v(c.BaseDir + ":/" + c.AppName)
	v(c.CellHome + ":/home/" + c.HostUser)
	v("/var/run/docker.sock:/var/run/docker.sock")
	v(c.HostHome + "/.claude/commands:/home/" + c.HostUser + "/.claude/commands:ro")
	v(c.HostHome + "/.claude/agents:/home/" + c.HostUser + "/.claude/agents:ro")
	v(c.HostHome + "/.claude/skills:/home/" + c.HostUser + "/.claude/skills")

	// cfg [[volumes]] entries
	for _, vol := range spec.CellCfg.Volumes {
		argv = append(argv, "-v", vol.Mount)
	}

	// Port mapping — only when GUI is enabled
	if spec.CellCfg.Cell.GUI {
		argv = append(argv, "-p", c.VNCPort+":5900")
	}

	// Network
	argv = append(argv, "--network", "devcell-network")

	// Workdir
	argv = append(argv, "--workdir", "/"+c.AppName)

	// Image
	argv = append(argv, "devcell-local")

	// Binary + flags + user args
	argv = append(argv, spec.Binary)
	argv = append(argv, spec.DefaultFlags...)
	argv = append(argv, spec.UserArgs...)

	return argv
}

// RemoveOrphanedContainer removes a stopped container with the given name if it exists.
// Returns nil if the container doesn't exist or was successfully removed.
// Returns an error if the container is currently running.
func RemoveOrphanedContainer(ctx context.Context, name string) error {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Status}}", name).Output()
	if err != nil {
		// Container doesn't exist — nothing to do.
		return nil
	}
	status := strings.TrimSpace(string(out))
	if status == "running" {
		return fmt.Errorf("container %q is already running — stop it first with: docker stop %s", name, name)
	}
	if err := exec.CommandContext(ctx, "docker", "rm", name).Run(); err != nil {
		return fmt.Errorf("remove orphaned container %q: %w", name, err)
	}
	return nil
}

// EnsureNetwork creates the devcell-network docker network if it doesn't exist.
func EnsureNetwork(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "network", "create", "devcell-network")
	// Ignore error — network likely already exists.
	_ = cmd.Run()
	return nil
}

// BuildImage runs docker build to build devcell-local from configDir.
// verbose=true streams plain-text output to out; verbose=false suppresses all
// docker output (quiet mode) and captures stderr to out for error replay.
func BuildImage(ctx context.Context, configDir string, noCache bool, verbose bool, out io.Writer) error {
	progress := "--progress=quiet"
	if verbose {
		progress = "--progress=plain"
	}
	args := []string{"build", "-t", "devcell-local", progress}
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, configDir)
	cmd := exec.CommandContext(ctx, "docker", args...)
	// Detach from the controlling TTY so Docker Desktop's BuildKit progress
	// writer cannot open /dev/tty and write directly to the terminal.
	// Also sets Setpgid so we can kill the whole process group on cancel.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// When context is cancelled, kill the entire process group (docker + buildkit).
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	if verbose {
		cmd.Stdout = out
		cmd.Stderr = out
	} else {
		// Suppress progress output; capture stderr so we can replay it on failure.
		cmd.Stdout = io.Discard
		cmd.Stderr = out
		// Belt-and-suspenders: also tell BuildKit via env to use quiet mode.
		cmd.Env = append(os.Environ(), "BUILDKIT_PROGRESS=quiet")
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("docker build: interrupted")
		}
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

// ImageExists returns true if a Docker image with the given tag exists locally.
func ImageExists(ctx context.Context, tag string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
