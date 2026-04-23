package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/version"
)

const (
	// DefaultRegistry is the fallback registry prefix for devcell images.
	DefaultRegistry = "public.ecr.aws/w1l3v2k8/devcell"
)

// Registry is the active container registry. Set via cfg.ResolvedRegistry()
// at startup; defaults to DefaultRegistry.
var Registry = DefaultRegistry

// Stack is the resolved nix stack name (e.g. "ultimate", "go").
// Set from CellConfig at startup; defaults to "base".
var Stack = "base"

// Modules is the list of extra nix modules composed on top of the stack.
// Set from CellConfig at startup.
var Modules []string

// PerSessionImage tags user images per tmux session instead of per stack.
// Set from CellConfig at startup; defaults to false (stack-based).
var PerSessionImage bool

// BaseImageTag returns the base image tag used in scaffold FROM,
// allowing override via DEVCELL_BASE_IMAGE env var (local dev, CI, tests).
func BaseImageTag() string {
	if tag := os.Getenv("DEVCELL_BASE_IMAGE"); tag != "" {
		return tag
	}
	return fmt.Sprintf("%s:%s-core", Registry, version.Version)
}

// UserImageTag returns the user image tag.
// Default (stack-based): devcell-user:<stack> or devcell-user:<stack>-<mod1>-<mod2>-<sha8>
// Legacy (per_session_image=true): devcell-user:<session> (one image per tmux session)
// Override with DEVCELL_USER_IMAGE env var (used by tests).
func UserImageTag() string {
	if tag := os.Getenv("DEVCELL_USER_IMAGE"); tag != "" {
		return tag
	}
	if PerSessionImage {
		return "devcell-user:" + resolveSession()
	}
	tag := Stack
	if tag == "" {
		tag = "base"
	}
	if len(Modules) > 0 {
		sorted := make([]string, len(Modules))
		copy(sorted, Modules)
		sort.Strings(sorted)
		tag += "-" + strings.Join(sorted, "-")
		h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
		tag += "-" + hex.EncodeToString(h[:])[:8]
	}
	return "devcell-user:" + tag
}

// resolveSession returns the session name from env vars (legacy per-session mode).
func resolveSession() string {
	if s := os.Getenv("DEVCELL_SESSION_NAME"); s != "" {
		return s
	}
	if s := os.Getenv("TMUX_SESSION_NAME"); s != "" {
		return s
	}
	return "main"
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
	Debug        bool                // pass DEVCELL_DEBUG=true into the container
	Image        string              // image ID or tag to run; defaults to UserImageTag
	ExtraEnv     map[string]string   // additional env vars injected by the command handler
	InheritEnv   []string            // env var names to inherit from host (passed as -e KEY with no value)
	Getenv       func(string) string // env lookup; defaults to os.Getenv when nil
}

func (s RunSpec) getenv(key string) string {
	if s.Getenv != nil {
		return s.Getenv(key)
	}
	return os.Getenv(key)
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

	dockerRunFlags := []string{"--rm", "-it", "--shm-size=1g"}
	if spec.CellCfg.Cell.DockerPrivileged {
		dockerRunFlags = append(dockerRunFlags, "--privileged")
	}
	argv = append(argv, "docker", "run")
	argv = append(argv, dockerRunFlags...)

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

	// Volume mount helper (defined early for use in git identity fallback)
	v := func(mount string) { argv = append(argv, "-v", mount) }

	// Git identity: host env > [git] toml > mount ~/.config/git/config:ro > hardcoded defaults
	gitCfg := spec.CellCfg.Git
	hostGitEnv := spec.getenv("GIT_AUTHOR_NAME") != "" ||
		spec.getenv("GIT_AUTHOR_EMAIL") != "" ||
		spec.getenv("GIT_COMMITTER_NAME") != "" ||
		spec.getenv("GIT_COMMITTER_EMAIL") != ""

	if hostGitEnv {
		e("GIT_AUTHOR_NAME", envOrDefaultFn(spec.getenv, "GIT_AUTHOR_NAME", "DevCell"))
		e("GIT_AUTHOR_EMAIL", envOrDefaultFn(spec.getenv, "GIT_AUTHOR_EMAIL", "devcell@devcell.io"))
		e("GIT_COMMITTER_NAME", envOrDefaultFn(spec.getenv, "GIT_COMMITTER_NAME", "DevCell"))
		e("GIT_COMMITTER_EMAIL", envOrDefaultFn(spec.getenv, "GIT_COMMITTER_EMAIL", "devcell@devcell.io"))
	} else if gitCfg.HasIdentity() {
		e("GIT_AUTHOR_NAME", gitCfg.AuthorName)
		e("GIT_AUTHOR_EMAIL", gitCfg.AuthorEmail)
		e("GIT_COMMITTER_NAME", gitCfg.ResolvedCommitterName())
		e("GIT_COMMITTER_EMAIL", gitCfg.ResolvedCommitterEmail())
	} else {
		e("GIT_AUTHOR_NAME", "DevCell")
		e("GIT_AUTHOR_EMAIL", "devcell@devcell.io")
		e("GIT_COMMITTER_NAME", "DevCell")
		e("GIT_COMMITTER_EMAIL", "devcell@devcell.io")
	}

	// Optional .env file — resolve self-referencing vars (KEY=${KEY}) by passing
	// -e KEY so Docker inherits the real value from the host environment.
	// Literal KEY=value lines are passed as-is via -e KEY=value.
	// Comments and blank lines are skipped.
	envFile := filepath.Join(c.BaseDir, ".env.devcell")
	if envData, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && parts[1] == "${"+parts[0]+"}" {
				// Self-referencing: KEY=${KEY} → inherit from host env
				argv = append(argv, "-e", parts[0])
			} else {
				argv = append(argv, "-e", line)
			}
		}
	}

	// GUI flag — only publish VNC port when GUI is enabled (default: true)
	if spec.CellCfg.Cell.ResolvedGUI() {
		argv = append(argv, "-e", "DEVCELL_GUI_ENABLED=true")
		argv = append(argv, "-e", "EXT_VNC_PORT="+c.VNCPort)
		argv = append(argv, "-e", "EXT_RDP_PORT="+c.RDPPort)
	}

	// Debug flag — enables verbose entrypoint logging inside the container
	if spec.Debug {
		argv = append(argv, "-e", "DEVCELL_DEBUG=true")
	}

	// Timezone: config wins, then host $TZ
	if tz := spec.CellCfg.Cell.Timezone; tz != "" {
		argv = append(argv, "-e", "TZ="+tz)
	} else if tz := os.Getenv("TZ"); tz != "" {
		argv = append(argv, "-e", "TZ="+tz)
	}

	// Locale: config wins, then host $LANG, then default en_US.UTF-8
	// LOCALE_ARCHIVE must be set at container start (before shell init) so
	// entrypoint bash can find the locale data from nix's glibcLocales.
	if loc := spec.CellCfg.Cell.Locale; loc != "" {
		argv = append(argv, "-e", "LANG="+loc, "-e", "LC_ALL="+loc)
	} else if loc := os.Getenv("LANG"); loc != "" && loc != "POSIX" && loc != "C" {
		argv = append(argv, "-e", "LANG="+loc, "-e", "LC_ALL="+loc)
	} else {
		argv = append(argv, "-e", "LANG=en_US.UTF-8", "-e", "LC_ALL=en_US.UTF-8")
	}

	// AWS read-only credential scoping — nix-managed config with credential_process
	if spec.CellCfg.Aws.ResolvedReadOnly() {
		e("AWS_CONFIG_FILE", "/opt/devcell/.aws/config")
		e("AWS_READ_OPERATIONS_ONLY", "true")
		e("READ_OPERATIONS_ONLY", "true") // consumed by aws-api MCP server
	}

	// cfg [env] entries
	for k, v := range spec.CellCfg.Env {
		argv = append(argv, "-e", k+"="+v)
	}

	// cfg [mise] entries → MISE_<UPPER_KEY>=value
	for k, v := range spec.CellCfg.Mise {
		argv = append(argv, "-e", "MISE_"+strings.ToUpper(k)+"="+v)
	}

	// Command-specific extra env vars (e.g. OPENCODE_CONFIG_CONTENT)
	for k, v := range spec.ExtraEnv {
		argv = append(argv, "-e", k+"="+v)
	}

	// Inherit env vars from host (secrets resolved by caller, set via os.Setenv)
	for _, k := range spec.InheritEnv {
		argv = append(argv, "-e", k)
	}

	// Tell the entrypoint which env vars are op-resolved secrets (for Playwright MCP)
	if len(spec.InheritEnv) > 0 {
		argv = append(argv, "-e", "DEVCELL_SECRET_KEYS="+strings.Join(spec.InheritEnv, ","))
	}

	// Standard volumes
	v(c.BaseDir + ":" + c.BaseDir)
	v(c.BaseDir + ":/" + c.AppName)
	v(c.CellHome + ":/home/" + c.HostUser)
	v("/var/run/docker.sock:/var/run/docker.sock")
	v(c.HostHome + "/.claude/commands:/home/" + c.HostUser + "/.claude/commands")
	v(c.HostHome + "/.claude/agents:/home/" + c.HostUser + "/.claude/agents:ro")
	v(c.HostHome + "/.claude/skills:/home/" + c.HostUser + "/.claude/skills")
	v(c.ConfigDir + ":/etc/devcell/config")
	v(c.ConfigDir + ":/home/" + c.HostUser + "/.config/devcell")

	// cfg [[volumes]] entries
	for _, vol := range spec.CellCfg.Volumes {
		argv = append(argv, "-v", vol.Mount)
	}

	// cfg [ports] entries
	for _, port := range spec.CellCfg.Ports.Forward {
		if !strings.Contains(port, ":") {
			port = port + ":" + port
		}
		argv = append(argv, "-p", port)
	}

	// GUI port mapping
	if spec.CellCfg.Cell.ResolvedGUI() {
		argv = append(argv, "-p", c.VNCPort+":5900")
		argv = append(argv, "-p", c.RDPPort+":3389")
	}

	// In-memory secrets mount — Playwright MCP reads .secrets-playwright from here
	argv = append(argv, "--tmpfs", "/run/secrets:mode=700,noexec,nosuid,size=1m")

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
	// Always use plain progress so the full build log (including nix errors)
	// is captured. In non-verbose mode the output goes to a buffer and is
	// only displayed on failure.
	progress := "--progress=plain"
	args := []string{"build", "-t", UserImageTag(), progress,
		"--build-arg", "GIT_COMMIT=" + version.GitCommit,
	}
	if noCache {
		args = append(args, "--no-cache", "--build-arg", "NIX_REFRESH=--refresh")
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
	cmd.Stdout = out
	cmd.Stderr = out
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

// StackImageTag returns the registry tag for a pre-built stack image.
// e.g. "go" → "ghcr.io/dimmkirr/devcell:v1.2.3-go"
func StackImageTag(stack string) string {
	return fmt.Sprintf("%s:%s-%s", Registry, version.Version, stack)
}

// PullImage attempts to pull a Docker image. Returns nil on success.
// When verbose is true, docker pull output is streamed to os.Stderr.
func PullImage(ctx context.Context, tag string, verbose bool) error {
	cmd := exec.CommandContext(ctx, "docker", "pull", tag)
	if verbose {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	return cmd.Run()
}

// DockerfileChanged reports whether any build-input file in configDir
// (Dockerfile, flake.nix) is newer than the user image.
// Returns true when the user image doesn't exist or inspect fails.
func DockerfileChanged(configDir string) bool {
	_, changed := ChangedBuildFiles(configDir)
	return changed
}

// buildContextFiles lists the files tracked for staleness detection.
var buildContextFiles = []string{"Dockerfile", "flake.nix", "package.json", "pyproject.toml"}

// imagePathForFile maps build context files to their path inside the image.
var imagePathForFile = map[string]string{
	"Dockerfile":     "", // not copied into image
	"flake.nix":      "/opt/devcell/.config/devcell/flake.nix",
	"package.json":   "/opt/npm-tools/package.json",
	"pyproject.toml": "/opt/python-tools/pyproject.toml",
}

// ChangedBuildFiles returns which build context files are newer than the image.
// Returns the list of changed file names and true if any changed.
func ChangedBuildFiles(configDir string) ([]string, bool) {
	out, err := exec.Command("docker", "image", "inspect",
		UserImageTag(), "--format", "{{.Created}}").Output()
	if err != nil {
		return []string{"(image missing)"}, true
	}
	imageCreated, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
	if err != nil {
		return []string{"(image timestamp unparseable)"}, true
	}
	var changed []string
	for _, name := range buildContextFiles {
		info, err := os.Stat(filepath.Join(configDir, name))
		if err != nil {
			continue
		}
		if info.ModTime().After(imageCreated) {
			changed = append(changed, name)
		}
	}
	return changed, len(changed) > 0
}

// DiffBuildFile returns a unified diff between the local build context file
// and the version baked into the image. Returns "" if the file isn't in the
// image (e.g. Dockerfile) or if they're identical. Uses docker cp to extract.
func DiffBuildFile(configDir, name string) string {
	imagePath, ok := imagePathForFile[name]
	if !ok || imagePath == "" {
		return ""
	}

	localPath := filepath.Join(configDir, name)
	localData, err := os.ReadFile(localPath)
	if err != nil {
		return ""
	}

	// Create a throwaway container (no process started) to extract the file.
	cidOut, err := exec.Command("docker", "create", "--quiet", UserImageTag(), "true").Output()
	if err != nil {
		return ""
	}
	cid := strings.TrimSpace(string(cidOut))
	defer exec.Command("docker", "rm", "-f", cid).Run()

	// Copy file from container to a temp location.
	tmpDir, err := os.MkdirTemp("", "devcell-diff-*")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, name)
	if err := exec.Command("docker", "cp", cid+":"+imagePath, tmpFile).Run(); err != nil {
		// File doesn't exist in image (new file).
		return fmt.Sprintf("--- (image) %s\n+++ (local) %s\n@@ new file @@\n", name, name)
	}

	imageData, err := os.ReadFile(tmpFile)
	if err != nil {
		return ""
	}

	if string(localData) == string(imageData) {
		return ""
	}

	// Run diff (best-effort — falls back to summary if diff not available).
	diffOut, _ := exec.Command("diff", "-u",
		"--label", "(image) "+name,
		"--label", "(local) "+name,
		tmpFile, localPath,
	).CombinedOutput()
	if len(diffOut) > 0 {
		return string(diffOut)
	}
	return fmt.Sprintf("--- (image) %s\n+++ (local) %s\n(binary or empty diff)\n", name, name)
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

// ImageMetadata holds structured build metadata from /etc/devcell/metadata.json.
type ImageMetadata struct {
	BaseImage string   `json:"base_image"`
	Stack     string   `json:"stack"`
	Modules   []string `json:"modules"`
	GitCommit string   `json:"git_commit"`
	BuildDate string   `json:"build_date"`
	Packages  int      `json:"packages"`
}

// ParseImageMetadata parses JSON into ImageMetadata. Returns zero value on error.
func ParseImageMetadata(data []byte) ImageMetadata {
	var m ImageMetadata
	json.Unmarshal(data, &m)
	return m
}

// ImageMetadataFromContainer reads /etc/devcell/metadata.json from the user image.
// Falls back to legacy base-image-version + user-image-version files.
func ImageMetadataFromContainer(ctx context.Context) ImageMetadata {
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "sh",
		UserImageTag(), "-c",
		"cat /etc/devcell/metadata.json 2>/dev/null",
	).Output()
	if err != nil || len(out) == 0 {
		return ImageMetadata{}
	}
	return ParseImageMetadata(out)
}

// ImageVersions reads build metadata from the user image.
// Returns (base, user) strings for backward compatibility with callers.
func ImageVersions(ctx context.Context) (base, user string) {
	m := ImageMetadataFromContainer(ctx)
	if m.GitCommit != "" {
		return m.BaseImage, m.GitCommit + " " + m.BuildDate
	}
	// Fallback: legacy files (pre-metadata.json images).
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "sh",
		UserImageTag(), "-c",
		"cat /etc/devcell/base-image-version 2>/dev/null; echo '---'; cat /etc/devcell/user-image-version 2>/dev/null",
	).Output()
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "---", 2)
	if len(parts) == 2 {
		base = strings.TrimSpace(parts[0])
		user = strings.TrimSpace(parts[1])
	}
	return
}

// UpdateFlakeLock runs nix flake lock (or update) inside a temp base container
// with configDir bind-mounted. When lockOnly is true, runs "nix flake lock"
// (resolves inputs, generates lock if missing, doesn't update existing pins).
// When lockOnly is false, runs "nix flake update" (pulls latest for all inputs).
func UpdateFlakeLock(ctx context.Context, configDir string, lockOnly bool, verbose bool, out io.Writer) error {
	nixCmd := "nix flake update"
	if lockOnly {
		nixCmd = "nix flake lock"
	}
	args := []string{
		"run", "--rm",
		"-v", configDir + ":/opt/devcell/.config/devcell",
		"--entrypoint", "sh",
		BaseImageTag(),
		"-c", "cd /opt/devcell/.config/devcell && " + nixCmd,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
		cmd.Stdout = io.Discard
		cmd.Stderr = out
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("nix flake: interrupted")
		}
		return fmt.Errorf("nix flake: %w", err)
	}
	return nil
}

// DiscoverStacks runs nix flake lock + discovers available stacks from the
// locked devcell input inside a Docker container. Returns stack names (e.g. "base", "go").
// Falls back to nil on error (caller should use hardcoded defaults).
func DiscoverStacks(ctx context.Context, configDir string, out io.Writer) ([]string, error) {
	// Combined: lock the flake, then find the devcell input source path and list stacks/*.nix.
	// nix output goes to stderr (visible in --debug); stack names go to stdout (parsed).
	script := `cd /opt/devcell/.config/devcell && nix flake lock >&2 && \
SRC=$(nix eval --raw --impure --expr '(builtins.getFlake "path:'"$(pwd)"'").inputs.devcell' 2>&1 >&2) && \
ls "$SRC/stacks/" 2>/dev/null | sed 's/\.nix$//' | sort`
	args := []string{
		"run", "--rm",
		"-v", configDir + ":/opt/devcell/.config/devcell",
		"--entrypoint", "sh",
		BaseImageTag(),
		"-c", script,
	}
	fmt.Fprintf(out, "[debug] docker %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("discover stacks: interrupted")
		}
		return nil, fmt.Errorf("discover stacks: %w", err)
	}
	var stacks []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			stacks = append(stacks, name)
		}
	}
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks found in nixhome")
	}
	return stacks, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrDefaultFn(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}
