package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build (or rebuild) the local devcell image",
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().Bool("update", false, "update nix flake inputs and rebuild without cache")
	buildCmd.Flags().Bool("no-generate", false, "skip regenerating build context (flake.nix, Dockerfile, etc.)")
	// Post-2026-05-15 flip (CELL-183): pure is the default. --impure (CELL-165
	// canonical name) opts into the legacy Dockerfile path. --debian is the
	// deprecated alias retained for one release. --pure is a silent no-op
	// (same as default).
	buildCmd.Flags().Bool("impure", false, "build via legacy Dockerfile path (default is nix2container/pure)")
	buildCmd.Flags().Bool("debian", false, "deprecated alias for --impure (will be removed)")
	buildCmd.Flags().Bool("pure", false, "silent no-op (kept for back-compat; pure is the default after CELL-183)")
	// CELL-93: one-shot stack override. Precedence: --stack > $DEVCELL_STACK >
	// [cell].stack in TOML > default (ResolvedStack → "base").
	buildCmd.Flags().String("stack", "", "override [cell].stack for this build (base, go, node, python, fullstack, electronics, ultimate)")
	buildCmd.Flags().String("image", "", "override the built image tag (e.g. devcell-user:dev-thin); env DEVCELL_BUILD_IMAGE has lower precedence")
	// CELL-156: thin image mode — nix store on Docker volume, not baked into image.
	buildCmd.Flags().Bool("thin", false, "build thin image (default; kept for explicitness)")
	buildCmd.Flags().Bool("no-thin", false, "build thick image (nix store baked into image)")
	buildCmd.Flags().Bool("thick", false, "alias for --no-thin")
}

func runBuild(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	update, _ := cmd.Flags().GetBool("update")
	noGenerate, _ := cmd.Flags().GetBool("no-generate")
	impure, _ := cmd.Flags().GetBool("impure")
	// Back-compat: accept --debian as an alias for --impure.
	if !impure {
		debian, _ := cmd.Flags().GetBool("debian")
		impure = debian
	}
	// Allow --impure / --debian via the positional scanner too so
	// `cell claude --build --impure` (or --debian) works.
	if !impure {
		impure = scanFlag("--impure") || scanFlag("--debian")
	}

	noThin, _ := cmd.Flags().GetBool("no-thin")
	thick, _ := cmd.Flags().GetBool("thick")
	if !noThin {
		noThin = thick || scanFlag("--no-thin") || scanFlag("--thick")
	}

	thin := false
	if noThin {
		thin = false
	} else {
		thinFlag, _ := cmd.Flags().GetBool("thin")
		if thinFlag || scanFlag("--thin") {
			thin = true
		} else {
			c2, _ := config.LoadFromOS()
			if c2.ConfigDir != "" {
				thin = cfg.LoadFromOS(c2.ConfigDir, c2.BaseDir).Cell.ResolvedThin()
			} else {
				thin = true
			}
		}
	}
	if !thin {
		runner.WarnThickDeprecation()
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ── Thin image mode (CELL-156): nix store on Docker volume ──
	if thin {
		stackOverride, err := resolveStackOverride(cmd.Flags().Lookup("stack").Value.String(), os.Getenv)
		if err != nil {
			return err
		}
		imageOverride := cmd.Flags().Lookup("image").Value.String()
		if imageOverride == "" {
			imageOverride = os.Getenv("DEVCELL_BUILD_IMAGE")
		}
		return runBuildThin(c, stackOverride, imageOverride, update)
	}

	// ── Default: pure (nix2container) engine — strict, no docker-build fallback ──
	if !impure {
		stackOverride, err := resolveStackOverride(cmd.Flags().Lookup("stack").Value.String(), os.Getenv)
		if err != nil {
			return err
		}
		return runBuildPure(c, stackOverride)
	}

	// ── Vagrant engine ────────────────────────────────────────────────────────
	// cell build --engine=vagrant   → vagrant provision (re-applies nixhome flake)
	// cell build --update --engine=vagrant → nix flake update inside VM, then provision
	engine := scanStringFlag("--engine")
	if scanFlag("--macos") {
		engine = "vagrant"
	}
	if engine == "vagrant" {
		cellCfgVagrant := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		vagrantBox := scanStringFlag("--vagrant-box")
		if vagrantBox == "" {
			vagrantBox = "utm/bookworm"
		}
		vagrantProvider := scanStringFlag("--vagrant-provider")
		if vagrantProvider == "" {
			vagrantProvider = "utm"
		}
		// Scaffold Vagrantfile idempotently (same as runVagrantAgent step 1).
		nixhomeDir := resolveVagrantNixhome(c.BaseDir)
		if nixhomeDir == "" {
			nixhomeDir = c.BaseDir + "/nixhome"
		}
		vmConfigDir := os.Getenv("DEVCELL_CONFIG_DIR")
		if vmConfigDir == "" {
			vmConfigDir = c.HostHome + "/.config/devcell"
		}
		// Always regenerate Vagrantfile on build (ports, stack may have changed).
		os.Remove(c.BuildDir + "/Vagrantfile")
		if err := scaffold.ScaffoldLinuxVagrantfile(
			c.BuildDir, vagrantBox, vagrantProvider,
			cellCfgVagrant.Cell.ResolvedStack(),
			c.BaseDir, nixhomeDir,
			c.VNCPort, c.RDPPort,
			c.HostHome, vmConfigDir,
		); err != nil {
			fmt.Fprintf(os.Stderr, "warning: vagrantfile scaffold failed: %v\n", err)
		}
		return runVagrantBuild(c.BuildDir, c.BaseDir, cellCfgVagrant, update, scanFlag("--dry-run"))
	}

	// ── Docker engine (default) ───────────────────────────────────────────────
	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	ux.Debugf("BuildDir: %s", c.BuildDir)
	if cellCfg.Cell.NixhomePath != "" {
		ux.Debugf("NixhomePath: %s (from config/env)", cellCfg.Cell.NixhomePath)
	}

	// Sync local nixhome into build context when nixhome path is set.
	if nixhomePath := cellCfg.Cell.NixhomePath; nixhomePath != "" {
		ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
		if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}

	if !noGenerate {
		// Regenerate all build artifacts from merged config (flake.nix,
		// Dockerfile, package.json, pyproject.toml) so that stack/modules
		// changes in devcell.toml take effect without re-running cell init.
		if err := scaffold.RegenerateBuildContext(c.BuildDir, cellCfg); err != nil {
			return fmt.Errorf("regenerate build context: %w", err)
		}
	}

	if update {
		if err := updateFlakeLockWithSpinner(c.BuildDir, false, "Updating nix flake inputs"); err != nil {
			return err
		}
	}

	if err := buildImageWithSpinner(c.BuildDir, update, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}

// runBuildPure runs the strict nix2container path. No docker-build fallback.
//
// Nixhome resolution mirrors the docker path (scaffold.go:130-140):
//  1. [cell].nixhome (TOML / DEVCELL_NIXHOME_PATH) — synced into BuildDir.
//  2. <BaseDir>/nixhome on disk             — synced into BuildDir.
//  3. github:DimmKirr/devcell/<ver>?dir=nixhome — passed to nix directly,
//     no local sync. Nix fetches and caches under /nix/store.
//
// The flake ref is then composed by PureBuildArgv as
// "<ref>#packages.<arch>.devcell-<stack>-pure-image" and loaded into the
// local Docker daemon as runner.UserImageTagPure().
// resolveStackOverride collapses the --stack flag value and the DEVCELL_STACK
// env var into a single override string for runBuildPure. Precedence:
// flag > env > "" (empty → caller uses TOML / default).
//
// Empty flagValue means the user didn't pass --stack; in that case the env
// var is consulted. Returns an error if either value names an unknown stack.
// getenv is injected so tests can drive the env layer deterministically
// (avoids polluting the real process env).
func resolveStackOverride(flagValue string, getenv func(string) string) (string, error) {
	if flagValue != "" {
		if err := cfg.ValidateStack(flagValue); err != nil {
			return "", err
		}
		return flagValue, nil
	}
	if v := getenv("DEVCELL_STACK"); v != "" {
		if err := cfg.ValidateStack(v); err != nil {
			return "", err
		}
		return v, nil
	}
	return "", nil
}

// runBuildPure runs the nix2container build. stackOverride wins over the
// TOML-resolved stack when non-empty (CELL-93). Validation of the override
// happens at the caller (runBuild) so this function can stay focused on the
// build itself.
func runBuildPure(c config.Config, stackOverride string) error {
	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	stack := cellCfg.Cell.ResolvedStack()
	if stackOverride != "" {
		stack = stackOverride
	}

	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}

	resolved := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		TomlNixhome: cellCfg.Cell.NixhomePath,
		BaseDir:     c.BaseDir,
		Version:     version.Version,
	})

	// Local source: sync into BuildDir so the flake path is stable across
	// runs and the user can inspect/edit .devcell/nixhome/ for debugging.
	// Remote source: skip the sync — nix handles the fetch and cache.
	flakeRef := resolved.FlakeRef
	if !resolved.Remote {
		if err := scaffold.SyncNixhome(resolved.LocalPath, c.BuildDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
		flakeRef = "path:" + c.BuildDir + "/nixhome"
		ux.Debugf("Pure build using local nixhome: %s (synced from %s)", flakeRef, resolved.LocalPath)
	} else {
		ux.Debugf("Pure build using remote nixhome: %s", flakeRef)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var buf bytes.Buffer
	var out io.Writer = &buf
	if ux.Verbose {
		out = os.Stdout
	}
	// Wrap with a layer counter so the --debug summary below can report
	// new (transferred) vs cached (already-at-destination) blob counts
	// captured straight from skopeo's per-blob log lines.
	lc := runner.NewLayerCounter(out)
	out = lc

	explicitStack := stackOverride != "" || cellCfg.Cell.StackExplicit()
	label := runner.BuildLabel("Building devcell image (nix2container)", stack, explicitStack)
	sp := ux.NewProgressSpinner(label)
	err := runner.BuildImagePure(ctx, runner.PureBuildSpec{
		FlakeRef:  flakeRef,
		StackName: stack,
		// Anchor the out-link inside BuildDir so we don't drop result-*
		// symlinks in the user's cwd (especially relevant for the github:
		// fallback where there's no local nixhome dir to anchor next to).
		OutLink: filepath.Join(c.BuildDir, fmt.Sprintf("result-%s-pure", stack)),
	}, runner.UserImageTagPure(), ux.Verbose, out)
	if err != nil {
		sp.Fail(label + " failed")
		if !ux.Verbose && buf.Len() > 0 {
			fmt.Fprint(os.Stderr, buf.String())
		}
		return err
	}
	// Append the loaded image size to the spinner's success line.
	// skopeo's per-blob progress is empty when stdout isn't a TTY, so this
	// synthesizes a single summary number from `docker image inspect` —
	// always available regardless of skopeo's terminal heuristics.
	tag := runner.UserImageTagPure()
	successLabel := label
	if size := runner.LocalImageSize(ctx, tag); size > 0 {
		successLabel = fmt.Sprintf("%s — %s loaded", label, runner.HumanBytes(size))
	}
	sp.Success(successLabel)

	// --debug summary — answers "is this a fresh build, and how much was cached?"
	// Only fires under --debug (ux.Debugf is a no-op otherwise).
	if ux.Verbose {
		printBuildDebugSummary(ctx, tag, lc.Stats())
	}
	return nil
}

// runBuildThin builds a thin image (CELL-156):
//  1. Ensure core image exists (pull or use cached)
//  2. docker run core with nix volume + docker socket:
//     - home-manager switch (reuses volume-cached /nix/store)
//     - docker build (inside container, via socket) → thin image
func runBuildThin(c config.Config, stackOverride, imageOverride string, forceRecreateVolume bool) error {
	// Daemon preflight — surface the actionable error when docker is down
	// before any pull/build attempt (CELL-44). The thin auto-build gate in
	// cmd/root.go probes first; this guards direct `cell build --thin` callers.
	if err := runner.DockerDaemonReachable(context.Background()); err != nil {
		return err
	}

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	stack := cellCfg.Cell.ResolvedStack()
	if stackOverride != "" {
		stack = stackOverride
	}

	// Resolve nixhome source — shared with pure path:
	//   1. [cell].nixhome (TOML/env) — local path
	//   2. <BaseDir>/nixhome on disk — dev/dogfood convenience
	//   3. github:DimmKirr/devcell/<ver>?dir=nixhome — prebaked upstream (CELL-38)
	resolved := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		TomlNixhome: cellCfg.Cell.NixhomePath,
		BaseDir:     c.BaseDir,
		Version:     version.Version,
	})

	// Materialise nixhome locally into .devcell/nixhome (handles github refs
	// via git clone; local paths via cp). Gives us a known on-disk source for
	// home-manager AND a known entrypoint.sh location.
	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}
	syncSrc := resolved.LocalPath
	if resolved.Remote {
		syncSrc = resolved.FlakeRef // SyncNixhome routes github: refs through git clone
	}
	if err := scaffold.SyncNixhome(syncSrc, c.BuildDir); err != nil {
		return fmt.Errorf("sync nixhome: %w", err)
	}

	// Write the overlay flake at .devcell/flake.nix — same generator as pure
	// path. Imports path:./nixhome (the just-synced upstream) + enables the
	// merged TOML modules. home-manager will switch against this overlay's
	// `devcell-local<arch>` output, not the upstream stack outputs directly,
	// so [cell].modules takes effect in thin builds (CELL-38 + CELL-61).
	overlayFlake := scaffold.GenerateFlakeNix(stack, cellCfg.Cell.Modules, version.Version, true)
	overlayPath := filepath.Join(c.BuildDir, "flake.nix")
	if err := os.WriteFile(overlayPath, []byte(overlayFlake), 0o644); err != nil {
		return fmt.Errorf("write overlay flake: %w", err)
	}
	// Symlink entrypoint.sh up from synced nixhome so ThinBuildArgv's
	// `cp /opt/nixhome/entrypoint.sh` keeps working when mounting the overlay
	// dir at /opt/nixhome instead of the raw nixhome.
	entrypointLink := filepath.Join(c.BuildDir, "entrypoint.sh")
	_ = os.Remove(entrypointLink)
	if err := os.Symlink(filepath.Join("nixhome", "entrypoint.sh"), entrypointLink); err != nil {
		return fmt.Errorf("symlink entrypoint.sh: %w", err)
	}

	// What we hand ThinBuildArgv is the OVERLAY dir (.devcell), mounted at
	// /opt/nixhome inside the builder. home-manager target becomes
	// `devcell-local` (matches GenerateFlakeNix's homeConfigurations output).
	nixhomeRef := runner.DockerHostPath(c.BuildDir)
	homeManagerTarget := "local"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	coreImage := runner.NixCoreImage
	tag := runner.ResolveBuildTag(imageOverride, runner.UserImageTagThin())
	volumeName := runner.ThinStoreVolume()
	containerName := "devcell-thin-builder"

	// ── Ensure core image exists ────────────────────────────────────────────
	if !runner.ImageExists(ctx, coreImage) {
		pullLabel := fmt.Sprintf("Pulling core image %s", coreImage)
		sp := ux.NewProgressSpinner(pullLabel)
		if err := runner.PullImage(ctx, coreImage, ux.Verbose); err != nil {
			sp.Fail(pullLabel + " failed")
			return fmt.Errorf("pull core image: %w", err)
		}
		sp.Success(pullLabel)
	}

	// ── Volume management ──────────────────────────────────────────────────
	// Docker auto-populates named volumes from the image on first mount when
	// the volume is empty. Don't pre-create — let docker run create it
	// implicitly so auto-populate fires correctly.
	if forceRecreateVolume {
		_ = exec.CommandContext(ctx, "docker", "volume", "rm", "-f", volumeName).Run()
	}

	// ── Build thin image ────────────────────────────────────────────────────
	explicitStack := stackOverride != "" || cellCfg.Cell.StackExplicit()
	buildLabel := runner.BuildLabel("Building thin image", stack, explicitStack)
	sp := ux.NewProgressSpinner(buildLabel)

	_ = exec.CommandContext(ctx, "docker", "rm", "-f", containerName).Run()

	// CELL-41: pass the real user-facing stack name + modules CSV so the
	// container's metadata.json reports them truthfully. The HM target stays
	// "local" — that's a flake-output naming detail, not user content.
	modulesCSV := strings.Join(cellCfg.Cell.Modules, ",")
	// CELL-293: bake the host's already-built `cell` binary into every
	// produced devcell image. Resolved to an absolute path so the runner's
	// docker bind mount succeeds. Empty (skip COPY) when the binary can't
	// be located — local dev `go run` flow without a built binary.
	cellBinaryPath := resolveCellBinaryPath()
	argv := runner.ThinBuildArgvFull(coreImage, containerName, volumeName, nixhomeRef, tag, homeManagerTarget, runner.DetectArch(), stack, modulesCSV, cellBinaryPath)

	var buf bytes.Buffer
	var out io.Writer = &buf
	if ux.Verbose {
		out = os.Stdout
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		sp.Fail(buildLabel + " failed")
		if !ux.Verbose && buf.Len() > 0 {
			fmt.Fprint(os.Stderr, buf.String())
		}
		return fmt.Errorf("thin build: %w", err)
	}

	successLabel := buildLabel
	if size := runner.LocalImageSize(ctx, tag); size > 0 {
		successLabel = fmt.Sprintf("%s — %s", buildLabel, runner.HumanBytes(size))
	}
	sp.Success(successLabel)
	return nil
}

// resolveCellBinaryPath returns the absolute filesystem path of the running
// cell binary so the thin builder can bake it into every produced image.
// Tries os.Executable() first (works for installed binaries); falls back to
// `bin/cell` in cwd (CI's `task cell:build` output). Returns "" when no
// binary can be located — runner skips the COPY in that case.
func resolveCellBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "bin", "cell")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// printBuildDebugSummary prints the post-build debug block: image ID +
// created timestamp + size + total layers + new/cached split. Surfaces the
// "did my rebuild actually produce a new image, or is the cache stale?"
// question that motivated the feature (CELL-86 debugging session).
func printBuildDebugSummary(ctx context.Context, tag string, layers runner.LayerStats) {
	info, err := runner.InspectImageDebug(ctx, tag)
	if err != nil {
		ux.Debugf("post-build inspect failed: %v", err)
		return
	}
	ux.Debugf("image:        %s", info.Tag)
	ux.Debugf("image ID:     %s", shortID(info.ID))
	ux.Debugf("created:      %s", info.Created)
	ux.Debugf("size:         %s", runner.HumanBytes(info.SizeBytes))
	ux.Debugf("layers total: %d", info.LayerCount)
	// New + Cached may not sum to LayerCount: skopeo's log is only emitted
	// for the registry-push leg, and skipped-line wording varies across
	// versions. We surface raw counts and the leftover as "unaccounted"
	// rather than fabricate a guarantee that doesn't hold.
	ux.Debugf("layers new:   %d", layers.New)
	ux.Debugf("layers cached:%d", layers.Cached)
	if rest := info.LayerCount - layers.New - layers.Cached; rest > 0 {
		ux.Debugf("layers other: %d  (not classified by skopeo log)", rest)
	}
}

// shortID renders sha256:abcdef… as abcdef12 to match `docker images`.
func shortID(id string) string {
	const prefix = "sha256:"
	s := id
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		s = s[len(prefix):]
	}
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
