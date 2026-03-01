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
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

const (
	// defaultBaseImageTag is the base image tag used in scaffold FROM.
	defaultBaseImageTag = "ghcr.io/dimmkirr/devcell:base-local"
	// defaultUserImageTag is the user-built image tag produced by cell build.
	defaultUserImageTag = "ghcr.io/dimmkirr/devcell:user-local"
)

// BaseImageTag returns the base image tag used in scaffold FROM,
// allowing override via DEVCELL_BASE_IMAGE env var (used by CI).
func BaseImageTag() string {
	if tag := os.Getenv("DEVCELL_BASE_IMAGE"); tag != "" {
		return tag
	}
	return defaultBaseImageTag
}

// UserImageTag returns the user image tag, allowing override via
// DEVCELL_USER_IMAGE env var (used by tests to avoid clobbering real images).
func UserImageTag() string {
	if tag := os.Getenv("DEVCELL_USER_IMAGE"); tag != "" {
		return tag
	}
	return defaultUserImageTag
}

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
	Debug        bool   // pass DEVCELL_DEBUG=true into the container
	Image        string // image ID or tag to run; defaults to UserImageTag
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

	// Image — use pinned digest when available, fall back to mutable tag
	image := spec.Image
	if image == "" {
		image = UserImageTag()
	}
	argv = append(argv, image)

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

// BuildImage runs docker build to build UserImageTag from configDir.
// verbose=true streams plain-text output to out; verbose=false suppresses all
// docker output (quiet mode) and captures stderr to out for error replay.
// --pull is always passed so Docker checks for a newer base image digest and
// busts the layer cache when the upstream image has been updated.
func BuildImage(ctx context.Context, configDir string, noCache bool, verbose bool, out io.Writer) error {
	progress := "--progress=quiet"
	if verbose {
		progress = "--progress=plain"
	}
	args := []string{"build", "-t", UserImageTag(), progress}
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

// DockerfileChanged reports whether any build-input file in configDir
// (Dockerfile, flake.nix) is newer than the user image.
// Returns true when the user image doesn't exist or inspect fails.
func DockerfileChanged(configDir string) bool {
	out, err := exec.Command("docker", "image", "inspect",
		UserImageTag(), "--format", "{{.Created}}").Output()
	if err != nil {
		return true // image missing or inspect failed — treat as changed
	}
	imageCreated, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
	if err != nil {
		return true
	}
	for _, name := range []string{"Dockerfile", "flake.nix"} {
		info, err := os.Stat(filepath.Join(configDir, name))
		if err != nil {
			continue
		}
		if info.ModTime().After(imageCreated) {
			return true
		}
	}
	return false
}

// LocalImageID returns the full image ID (sha256:...) of the user image.
// Used to pin the running container to the exact image just built,
// rather than the mutable tag which could race with a concurrent build.
func LocalImageID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect",
		UserImageTag(), "--format", "{{.Id}}").Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", UserImageTag(), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
