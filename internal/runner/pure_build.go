package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Parallel nix2container build path.
//
// `cell build --pure` invokes nix2container instead of `docker build`,
// producing a content-addressed OCI image from the nixhome flake. The
// image is then loaded into the local Docker daemon via nix2container's
// native `copyToDockerDaemon` attribute (a writeShellApplication wrapper
// over `skopeo copy nix:... docker-daemon:...`).
//
// Strictly nix2container — no docker-build fallback. If anything fails,
// surface the error; do not silently revert.

// PureBuildSpec describes a pure-image build invocation.
type PureBuildSpec struct {
	// FlakeRef is the full flake reference to build against (e.g.
	// "path:/abs/nixhome" or "github:DimmKirr/devcell/main?dir=nixhome").
	// When set, takes precedence over NixhomePath — the per-stack output
	// suffix is appended directly. This is the seam that lets the pure
	// path fall back to a remote flake when no local nixhome exists,
	// mirroring the docker path's flake input fallback (scaffold.go:130-140).
	FlakeRef string
	// NixhomePath is a local directory containing the nixhome flake.
	// Used only when FlakeRef is empty (legacy/back-compat callers).
	NixhomePath string
	// StackName is the devcell stack to build (e.g. "base", "python", "ultimate").
	StackName string
	// Arch is the nix system identifier (e.g. "aarch64-linux", "x86_64-linux").
	// If empty, detected from runtime.GOARCH.
	Arch string
	// OutLink is the symlink target for the build output. Optional; defaults
	// to a path under the nixhome dir.
	OutLink string
	// Verbose enables -L (print build logs) and -v on the nix invocation,
	// so users running `cell <agent> --pure --debug` see real progress
	// instead of just a spinner.
	Verbose bool
	// Thin builds a thin image (no /nix/store layers). The nix store is
	// expected to be provided at runtime via a Docker named volume.
	Thin bool
}

// PureBuildArgv composes the `nix build` argv targeting the per-stack
// pure-image flake output. Pure function — does not invoke nix.
func PureBuildArgv(spec PureBuildSpec) []string {
	arch := spec.Arch
	if arch == "" {
		arch = detectNixArch()
	}
	outLink := spec.OutLink
	if outLink == "" {
		// Anchor result-<stack>-pure next to the local nixhome dir when
		// we have one; otherwise stash it under the cwd (--out-link forbids
		// an empty value, and we can't write inside a github: ref).
		anchor := spec.NixhomePath
		if anchor == "" {
			anchor = "."
		}
		outLink = filepath.Join(filepath.Dir(anchor), fmt.Sprintf("result-%s-pure", spec.StackName))
	}
	// FlakeRef wins when set — supports github:, git+https:, etc. Falls back
	// to wrapping a local NixhomePath in "path:" for legacy callers.
	flakeBase := spec.FlakeRef
	if flakeBase == "" {
		flakeBase = "path:" + spec.NixhomePath
	}
	imageSuffix := "pure-image"
	if spec.Thin {
		imageSuffix = "thin-image"
	}
	flakeRef := fmt.Sprintf("%s#packages.%s.devcell-%s-%s",
		flakeBase, arch, spec.StackName, imageSuffix)
	argv := []string{
		"nix", "build",
		"--extra-experimental-features", "nix-command flakes",
		// --impure: lets image.nix read DEVCELL_BUILD_DATE / DEVCELL_BUILD_REV
		// via builtins.getEnv so the resulting image's OCI manifest carries a
		// real timestamp + git rev instead of placeholders. Without --impure
		// these env vars are invisible to nix eval and we'd fall back to
		// "1970-01-01T00:00:00Z" / "unknown".
		"--impure",
	}
	if spec.Verbose {
		// -L           — stream per-derivation build logs as they're produced
		// -vv          — double-verbose: emits eval traces and narinfo queries
		//                during the otherwise-silent eval + cache-query phase
		//                (a full ultimate-stack cold build can spend 3-5 min
		//                with zero output before any drv runs).
		// --show-trace — print full eval stack on errors, not just one line.
		argv = append(argv, "-L", "-vv", "--show-trace")
	}
	argv = append(argv, flakeRef, "--out-link", outLink)
	return argv
}

// NixSystemFor maps a Go (GOOS, GOARCH) pair to nix's system identifier
// (e.g. "aarch64-darwin", "x86_64-linux"). Pure function for testability.
//
// This identifies the HOST running `cell`, not the image target. The flake
// exposes `packages.<hostSystem>.devcell-<stack>-pure-image` for every
// supported host: on Darwin hosts that package wires n2c with darwin pkgs
// (so IFD helpers and the bundled copy-to-docker-daemon are darwin-runnable)
// while keeping image content (copyToRoot) at aarch64-linux. On Linux hosts
// host == target, so everything is Linux.
func NixSystemFor(goos, goarch string) string {
	var arch string
	switch goarch {
	case "arm64":
		arch = "aarch64"
	case "amd64":
		arch = "x86_64"
	default:
		arch = goarch
	}
	var os string
	switch goos {
	case "darwin":
		os = "darwin"
	default:
		os = "linux"
	}
	return arch + "-" + os
}

// PickSkopeoBin walks the multi-line output of `nix build --print-out-paths`
// and returns the first store path that actually contains `bin/skopeo`.
//
// Why this exists: skopeo (and skopeo-nix2container, which inherits skopeo's
// outputs) is a multi-output derivation — at least `out` and `man`, sometimes
// more. `--print-out-paths` prints one path per output, one per line. Naively
// joining the whole stdout with `/bin/skopeo` produces a path with an embedded
// newline; the kernel then rejects it with "fork/exec ...-man\n/nix/store/...".
//
// The `exists` predicate is injected so this is testable without touching the
// real store; production callers pass a wrapper around os.Stat.
func PickSkopeoBin(printOutPathsOutput string, exists func(string) bool) (string, error) {
	for _, line := range strings.Split(printOutPathsOutput, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		candidate := filepath.Join(p, "bin", "skopeo")
		if exists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no bin/skopeo in any output path:\n%s", printOutPathsOutput)
}

// detectNixArch returns the nix system identifier for the host running cell.
// Despite the legacy name it now returns OS+arch (e.g. "aarch64-darwin"), to
// match the flake's per-host `packages.<system>` keys that decouple
// helper-runtime arch from image content arch.
func detectNixArch() string {
	return NixSystemFor(runtime.GOOS, runtime.GOARCH)
}

// LinuxBuilderProbe captures the result of inspecting the local nix config for
// a Linux remote builder. Populated by CheckNixLinuxBuilder so the caller can
// surface the diagnostic context in user-facing errors.
type LinuxBuilderProbe struct {
	OK             bool   // can we build aarch64-linux / x86_64-linux?
	Source         string // "env", "linux-host", "nix-config-show", "nix.conf", "machines-file", "none"
	ConfigCmd      string // the nix command we ran (or "" if we didn't)
	ConfigErr      string // error from running nix config show (if any)
	BuildersLine   string // exact `builders = ...` line nix reported
	ExtraPlatforms string // exact `extra-platforms = ...` line nix reported
	NixConfPath    string // path to nix.conf if read as fallback
	MachinesFile   string // path to /etc/nix/machines if BuildersLine references it
	MachinesLines  string // first ~3 non-comment lines from machines file
}

// CheckNixLinuxBuilder probes nix to determine whether aarch64-linux is buildable
// and returns full diagnostic info for the error message.
func CheckNixLinuxBuilder() LinuxBuilderProbe {
	if os.Getenv("DEVCELL_PURE_SKIP_PREFLIGHT") == "1" {
		return LinuxBuilderProbe{OK: true, Source: "env"}
	}
	if runtime.GOOS != "darwin" {
		return LinuxBuilderProbe{OK: true, Source: "linux-host"}
	}

	probe := LinuxBuilderProbe{Source: "nix-config-show"}

	// `nix config show` (new) → `nix show-config` (old). Try both.
	tryCmd := func(args ...string) ([]byte, string, error) {
		c := exec.Command("nix", args...)
		out, err := c.CombinedOutput()
		return out, "nix " + strings.Join(args, " "), err
	}
	out, cmdStr, err := tryCmd("config", "show", "--extra-experimental-features", "nix-command")
	if err != nil {
		out, cmdStr, err = tryCmd("show-config", "--extra-experimental-features", "nix-command")
	}
	probe.ConfigCmd = cmdStr
	if err != nil {
		probe.ConfigErr = strings.TrimSpace(string(out))
		// Fall through to nix.conf read.
	} else {
		for _, line := range strings.Split(string(out), "\n") {
			lower := strings.ToLower(line)
			switch {
			case strings.HasPrefix(lower, "extra-platforms"):
				probe.ExtraPlatforms = strings.TrimSpace(line)
			case strings.HasPrefix(lower, "builders ") || strings.HasPrefix(lower, "builders="):
				probe.BuildersLine = strings.TrimSpace(line)
			}
		}
		probe.OK = looksLikeLinuxBuilder(probe.BuildersLine) ||
			looksLikeLinuxBuilder(probe.ExtraPlatforms)
		// If `builders = @/path/to/machines`, that's a reference to a file
		// listing remote builders. nix-darwin's linux-builder uses this form,
		// and `extra-platforms` may not list Linux even though the builder
		// supports it. Read the file to confirm.
		if !probe.OK {
			if path := buildersFilePath(probe.BuildersLine); path != "" {
				probe.MachinesFile = path
				if data, ferr := os.ReadFile(path); ferr == nil {
					probe.MachinesLines = firstNonCommentLines(string(data), 3)
					if looksLikeLinuxBuilder(probe.MachinesLines) {
						probe.OK = true
						probe.Source = "machines-file"
					}
				}
			}
		}
		if probe.OK || probe.ConfigErr == "" {
			return probe
		}
	}

	// Fallback: read nix.conf directly.
	probe.Source = "nix.conf"
	probe.NixConfPath = "/etc/nix/nix.conf"
	conf, fileErr := os.ReadFile(probe.NixConfPath)
	if fileErr != nil {
		probe.ConfigErr = fileErr.Error()
		return probe
	}
	for _, line := range strings.Split(string(conf), "\n") {
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "extra-platforms"):
			probe.ExtraPlatforms = strings.TrimSpace(line)
		case strings.HasPrefix(lower, "builders ") || strings.HasPrefix(lower, "builders="):
			probe.BuildersLine = strings.TrimSpace(line)
		}
	}
	probe.OK = looksLikeLinuxBuilder(probe.BuildersLine) ||
		looksLikeLinuxBuilder(probe.ExtraPlatforms)
	return probe
}

// buildersFilePath extracts the file path from a `builders = @/path/file` line.
// Returns "" if the line doesn't reference a file.
func buildersFilePath(line string) string {
	const marker = "@"
	idx := strings.Index(line, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+1:])
	// Take up to whitespace/comma in case there are extra entries on the line.
	for i, r := range rest {
		if r == ' ' || r == '\t' || r == ',' {
			return rest[:i]
		}
	}
	return rest
}

// firstNonCommentLines returns up to n non-blank, non-comment lines from raw.
// Used to summarize /etc/nix/machines content for the diagnostic error.
func firstNonCommentLines(raw string, n int) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
		if len(out) >= n {
			break
		}
	}
	return strings.Join(out, " | ")
}

// orFallback returns s if non-empty, else fallback.
func orFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// probeErrSuffix appends a "  error: ..." line to the diagnostic block when
// the nix config probe itself failed.
func probeErrSuffix(p LinuxBuilderProbe) string {
	if p.ConfigErr == "" {
		return ""
	}
	return "\n  probe error:    " + strings.SplitN(p.ConfigErr, "\n", 2)[0]
}

// looksLikeLinuxBuilder returns true if the line mentions a Linux platform
// (aarch64-linux/x86_64-linux) or the nix-darwin linux-builder symbolic name.
// Lines that only mention `@/etc/nix/machines` are insufficient — the path
// might be empty.
func looksLikeLinuxBuilder(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "aarch64-linux") ||
		strings.Contains(l, "x86_64-linux") ||
		strings.Contains(l, "linux-builder")
}

// flakeBaseOf returns the base flake reference for a PureBuildSpec — the
// part before `#<output>`. FlakeRef wins when set; otherwise composes
// "path:<NixhomePath>". Centralized so the image build, the skopeo build,
// and any future output realization all use the same base.
func flakeBaseOf(spec PureBuildSpec) string {
	if spec.FlakeRef != "" {
		return spec.FlakeRef
	}
	return "path:" + spec.NixhomePath
}

// BuildImagePure runs `nix build` for the pure-image flake output and loads
// the result into the Docker daemon as UserImageTag(). Strictly nix2container —
// never falls back to docker build.
func BuildImagePure(ctx context.Context, spec PureBuildSpec, tag string, verbose bool, out io.Writer) error {
	if spec.FlakeRef == "" && spec.NixhomePath == "" {
		return fmt.Errorf("pure build: FlakeRef or NixhomePath required")
	}
	if spec.StackName == "" {
		return fmt.Errorf("pure build: StackName required")
	}

	// Preflight: catch macOS host trying to build Linux image without a
	// Linux remote builder. Avoids fetching ~3 GB before failing. Exported
	// as PreflightNixBuilder so callers can run it independently to decide
	// whether to attempt a pure build at all.
	if err := PreflightNixBuilder(spec.StackName); err != nil {
		return err
	}

	// Propagate the verbose flag into the spec so PureBuildArgv emits -L -v.
	// Callers can also set spec.Verbose directly; the OR wins.
	spec.Verbose = spec.Verbose || verbose

	argv := PureBuildArgv(spec)

	// Heartbeat in verbose mode: nix's eval + cache-substitutability phase
	// can be silent for 3-5 min on a cold cross-arch build. Wrapping `out`
	// emits "… still working" every 30s of silence so users can tell live
	// from wedged without leaving the shell.
	sink := out
	var hb *heartbeatWriter
	if spec.Verbose {
		hb = newHeartbeatWriter(out, 30*time.Second, time.Now)
		sink = hb
	}

	// Use sudo if available; nix daemon owns /nix/var/nix/db/big-lock.
	cmd := buildNixCmd(ctx, argv)
	cmd.Stdout = sink
	cmd.Stderr = sink
	// DEVCELL_BUILD_DATE / DEVCELL_BUILD_REV are read by image.nix via
	// builtins.getEnv (under --impure, added by PureBuildArgv). They stamp
	// the OCI manifest's `created` field + `org.opencontainers.image.*`
	// labels + /etc/devcell/metadata.json — so `docker inspect <image>`
	// and the entrypoint log line both surface the actual build wall-clock.
	// Layer blobs are content-addressed by contents, NOT by these env vars,
	// so layer caches stay warm even though the image SHA changes per build.
	cmd.Env = append(os.Environ(),
		"DEVCELL_BUILD_DATE="+time.Now().UTC().Format(time.RFC3339),
		"DEVCELL_BUILD_REV="+pureBuildRev(spec),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	err := cmd.Run()
	if hb != nil {
		hb.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("nix build: interrupted")
		}
		return fmt.Errorf("nix build (pure): %w", err)
	}

	// Load into Docker via nix2container's patched skopeo.
	//
	// We can't use the image's bundled `copyToDockerDaemon` script directly:
	// that script's bash + jq + skopeo are pinned to the IMAGE's architecture
	// (aarch64-linux), so on a Mac host the kernel rejects them with "Exec
	// format error" at shebang resolution. Instead, build the patched skopeo
	// for the HOST architecture (nix auto-picks via `path:#skopeo-nix2container`)
	// and invoke it directly with the local nix manifest path.
	//
	// Flow:
	//   1. `nix build path:nixhome#skopeo-nix2container` → host-arch skopeo
	//   2. `<skopeo>/bin/skopeo copy nix:<manifest> docker-daemon:<tag>` → loads
	//      the (Linux) image into the local Docker daemon over the Unix socket.
	skopeoArgv := []string{
		"nix", "build", "--no-link", "--print-out-paths",
		"--extra-experimental-features", "nix-command flakes",
		// `^out` restricts realization to the main output; skopeo in nixpkgs
		// is multi-output (out + man) and without this nix prints both paths
		// on separate lines, which would defeat the parsing in PickSkopeoBin.
		// We still parse defensively below in case a future nix/skopeo
		// revision drops or adds outputs.
		//
		// Same flake base as the image build, so a github: fallback also
		// resolves skopeo from the remote flake — no local path required.
		fmt.Sprintf("%s#skopeo-nix2container^out", flakeBaseOf(spec)),
	}
	skopeoCmd := buildNixCmd(ctx, skopeoArgv)
	var skopeoBuf bytes.Buffer
	skopeoCmd.Stdout = &skopeoBuf
	skopeoCmd.Stderr = out
	if err := skopeoCmd.Run(); err != nil {
		return fmt.Errorf("build host skopeo: %w", err)
	}
	skopeoBin, err := PickSkopeoBin(skopeoBuf.String(), func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && !fi.IsDir()
	})
	if err != nil {
		return fmt.Errorf("build host skopeo: %w", err)
	}

	// Resolve out-link to the image manifest (already on local /nix/store).
	// When NixhomePath is set, anchor next to it (legacy back-compat).
	// Otherwise fall back to cwd — runBuildPure should set OutLink explicitly
	// for the remote (github) case so we never write into the user's cwd.
	outLink := spec.OutLink
	if outLink == "" {
		anchor := spec.NixhomePath
		if anchor == "" {
			anchor = "."
		}
		outLink = filepath.Join(filepath.Dir(anchor), fmt.Sprintf("result-%s-pure", spec.StackName))
	}

	// Skip skopeo copy if the image manifest (nix store path) hasn't changed
	// since the last successful load. The stamp file records the resolved
	// store path of the last loaded image.
	resolvedLink, err := filepath.EvalSymlinks(outLink)
	if err != nil {
		return fmt.Errorf("pure build: resolve outLink: %w", err)
	}
	stampFile := outLink + ".loaded"
	if prev, err := os.ReadFile(stampFile); err == nil && string(prev) == resolvedLink {
		// Verify the tag still exists in Docker (user may have pruned).
		checkCmd := exec.CommandContext(ctx, "docker", "image", "inspect", tag)
		if checkCmd.Run() == nil {
			fmt.Fprintf(out, "Image unchanged (%s) — skipping load\n", filepath.Base(resolvedLink))
			return nil
		}
	}

	// Start an ephemeral OCI registry for layer dedup. Unchanged layers are
	// served from the disk cache instead of being re-serialized from nix.
	reg := &EphemeralRegistry{}
	if err := reg.Start(registryCacheDir()); err != nil {
		return fmt.Errorf("pure build: start registry: %w", err)
	}
	defer reg.Stop()

	regRef := fmt.Sprintf("docker://%s/devcell:%s", reg.Addr(), spec.StackName)

	// Write a temporary registries.conf that marks localhost as HTTP-only.
	// The nix2container-patched skopeo may not support --dest-tls-verify.
	regConf := filepath.Join(os.TempDir(), fmt.Sprintf("devcell-reg-%d.conf", reg.Port))
	regConfContent := fmt.Sprintf(`[[registry]]
location = "%s"
insecure = true
`, reg.Addr())
	if err := os.WriteFile(regConf, []byte(regConfContent), 0644); err != nil {
		return fmt.Errorf("pure build: write registries.conf: %w", err)
	}
	defer os.Remove(regConf)

	skopeoArgs := []string{"--insecure-policy", "--registries-conf", regConf, "copy", "nix:" + outLink, regRef}
	fmt.Fprintf(out, "Pushing image to ephemeral registry (%s → %s)...\n", filepath.Base(resolvedLink), reg.Addr())
	loadStart := time.Now()
	pushCmd := exec.CommandContext(ctx, skopeoBin, skopeoArgs...)
	pushCmd.Stdout = out
	pushCmd.Stderr = out
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("pure build (registry push): %w", err)
	}
	fmt.Fprintf(out, "Push complete in %s\n", time.Since(loadStart).Round(time.Second))

	// Copy from ephemeral registry to Docker daemon via skopeo. We can't use
	// `docker pull` because on macOS the Docker daemon runs in a VM and can't
	// reach the host's localhost registry. Skopeo runs on the host and can
	// reach both the registry and the Docker daemon socket.
	fmt.Fprintf(out, "Loading image into Docker daemon (%s)...\n", tag)
	pullStart := time.Now()
	loadArgs := []string{"--insecure-policy", "--registries-conf", regConf, "copy",
		regRef, "docker-daemon:" + tag}
	loadCmd := exec.CommandContext(ctx, skopeoBin, loadArgs...)
	loadCmd.Stdout = out
	loadCmd.Stderr = out
	if err := loadCmd.Run(); err != nil {
		return fmt.Errorf("pure build (registry→daemon): %w", err)
	}
	fmt.Fprintf(out, "Image loaded in %s\n", time.Since(pullStart).Round(time.Second))

	// Record the loaded store path for future skip detection.
	_ = os.WriteFile(stampFile, []byte(resolvedLink), 0644)
	return nil
}

// pureBuildRev resolves the source git revision for the image's
// `org.opencontainers.image.revision` label. Order of precedence:
//  1. DEVCELL_BUILD_REV — explicit override (CI passes the build commit).
//  2. `git rev-parse HEAD` in the nixhome dir — works for local builds
//     out of a checkout. Appends "-dirty" when the worktree is unclean.
//  3. "unknown" — flake refs without a working tree (github:..., git+https:..)
//     or when git isn't on PATH.
func pureBuildRev(spec PureBuildSpec) string {
	if v := os.Getenv("DEVCELL_BUILD_REV"); v != "" {
		return v
	}
	if spec.NixhomePath == "" {
		return "unknown"
	}
	rev, err := exec.Command("git", "-C", spec.NixhomePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	out := strings.TrimSpace(string(rev))
	if out == "" {
		return "unknown"
	}
	// "-dirty" suffix when the worktree has uncommitted changes (matches
	// nix's own self.dirtyRev convention).
	if err := exec.Command("git", "-C", spec.NixhomePath, "diff", "--quiet", "HEAD").Run(); err != nil {
		out += "-dirty"
	}
	return out
}

// buildNixCmd composes the nix invocation. By default it runs without sudo —
// matches single-user nix installs and Determinate's nix where the calling
// user is already in trusted-users.
//
// Set DEVCELL_NIX_SUDO=1 to wrap with sudo (multi-user daemon installs where
// only root can manage the store). Useful inside devcell containers themselves
// where the nix daemon owns /nix/var/nix/db/big-lock.
func buildNixCmd(ctx context.Context, argv []string) *exec.Cmd {
	nixBin := argv[0]
	if !strings.Contains(nixBin, "/") {
		if p, err := exec.LookPath(nixBin); err == nil {
			nixBin = p
		}
	}
	useSudo := os.Getenv("DEVCELL_NIX_SUDO") == "1" && os.Geteuid() != 0
	if useSudo {
		// --preserve-env keeps DEVCELL_BUILD_DATE / DEVCELL_BUILD_REV alive
		// across the sudo boundary so image.nix's `builtins.getEnv` (under
		// --impure) can read them. Sudo strips most of the env by default.
		full := append([]string{"--preserve-env=DEVCELL_BUILD_DATE,DEVCELL_BUILD_REV", "--", nixBin}, argv[1:]...)
		return exec.CommandContext(ctx, "sudo", full...)
	}
	full := append([]string{nixBin}, argv[1:]...)
	return exec.CommandContext(ctx, full[0], full[1:]...)
}
