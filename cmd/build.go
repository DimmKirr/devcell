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
	"strconv"
	"strings"
	"syscall"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/telemetry"
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
	buildCmd.Flags().String("stack", "", "override [cell].stack for this build (base, go, node, python, fullstack, electronics, ultimate)")
	buildCmd.Flags().String("image", "", "override the built image tag (e.g. devcell-user:dev-thin); env DEVCELL_BUILD_IMAGE has lower precedence")
	buildCmd.Flags().Bool("force", false, "recreate VM even if it already exists (tart only)")
	buildCmd.Flags().Bool("no-cache", false, "re-download OCI image, bypassing tart cache (tart only)")
}

func runBuild(cmd *cobra.Command, _ []string) error {
	applyOutputFlagsWithLog("build")

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cellCfgForEngine := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	engine, err := resolveEngine(scanStringFlag("--engine"), scanStringFlag("--os"), cellCfgForEngine.Cell.Engine, cellCfgForEngine.Cell.OS, scanFlag("--macos"))
	if err != nil {
		return err
	}

	telemetry.Track("build", map[string]any{
		"engine":     engine,
		"subcommand": "build",
		"update":     scanFlag("--update"),
		"no_cache":   scanFlag("--no-cache"),
		"force":      scanFlag("--force"),
	})

	// ── tart engine ──────────────────────────────────────────────────────────
	if engine == "tart" {
		cellCfgTart, cfgErr := cfg.LoadFromOSWithDirs(c.ConfigDir, c.BaseDir)
		if cfgErr != nil {
			return fmt.Errorf("loading config: %w", cfgErr)
		}
		stack := cellCfgTart.Cell.ResolvedStack()
		if s := cmd.Flags().Lookup("stack").Value.String(); s != "" {
			stack = s
		}
		force, _ := cmd.Flags().GetBool("force")
		noCache, _ := cmd.Flags().GetBool("no-cache")
		tartOCIImage := cellCfgTart.Cell.ResolvedTartOCIImage()
		return runBuildTart(c.CellName, c.HostHome, c.BaseDir, stack, nil, force, noCache, scanFlag("--dry-run"), tartOCIImage)
	}

	// ── qemu engine ─────────────────────────────────────────────────────────
	if engine == "qemu" {
		cellCfgQemu, cfgErr := cfg.LoadFromOSWithDirs(c.ConfigDir, c.BaseDir)
		if cfgErr != nil {
			return fmt.Errorf("loading config: %w", cfgErr)
		}
		stack := cellCfgQemu.Cell.ResolvedStack()
		if s := cmd.Flags().Lookup("stack").Value.String(); s != "" {
			stack = s
		}
		force, _ := cmd.Flags().GetBool("force")
		noCache, _ := cmd.Flags().GetBool("no-cache")
		return runBuildQemu(c.CellName, c.HostHome, c.BaseDir, stack, force, noCache, scanFlag("--dry-run"), cellCfgQemu.Cell)
	}

	// ── libvirt engine ───────────────────────────────────────────────────────
	// Template building over libvirt is deferred (CELL-379); the MVP scope
	// builds templates with --engine=qemu on the macOS host and libvirt only
	// boots them remotely.
	if engine == "libvirt" {
		cellCfgLibvirt, cfgErr := cfg.LoadFromOSWithDirs(c.ConfigDir, c.BaseDir)
		if cfgErr != nil {
			return fmt.Errorf("loading config: %w", cfgErr)
		}
		uri := cellCfgLibvirt.Cell.ResolvedLibvirtURI()
		if scanFlag("--dry-run") {
			fmt.Println("libvirt engine (dry-run)")
			fmt.Printf("  URI: %s\n", uri)
			fmt.Println("  Would boot a prepped template remotely; template builds stay on `cell build --engine=qemu` (macOS host)")
			return nil
		}
		return fmt.Errorf("cell build --engine=libvirt is not implemented — build the template with `cell build --engine=qemu` on the macOS host, then run with --engine=libvirt (CELL-379)")
	}

	// ── Vagrant engine ────────────────────────────────────────────────────────
	if engine == "vagrant" {
		cellCfgVagrant, cfgErr := cfg.LoadFromOSWithDirs(c.ConfigDir, c.BaseDir)
		if cfgErr != nil {
			return fmt.Errorf("loading config: %w", cfgErr)
		}
		vagrantBox := scanStringFlag("--vagrant-box")
		if vagrantBox == "" {
			vagrantBox = "utm/bookworm"
		}
		vagrantProvider := scanStringFlag("--vagrant-provider")
		if vagrantProvider == "" {
			vagrantProvider = "utm"
		}
		nixhomeDir := ""
		vmConfigDir := os.Getenv("DEVCELL_CONFIG_DIR")
		if vmConfigDir == "" {
			vmConfigDir = c.HostHome + "/.config/devcell"
		}
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
		return runVagrantBuild(c.BuildDir, c.BaseDir, cellCfgVagrant, scanFlag("--update"), scanFlag("--dry-run"))
	}

	// ── Docker engine (thin) ─────────────────────────────────────────────────
	stackOverride, err := resolveStackOverride(cmd.Flags().Lookup("stack").Value.String(), os.Getenv)
	if err != nil {
		return err
	}
	imageOverride := cmd.Flags().Lookup("image").Value.String()
	if imageOverride == "" {
		imageOverride = os.Getenv("DEVCELL_BUILD_IMAGE")
	}
	return runBuildThin(c, stackOverride, imageOverride, scanFlag("--update"))
}

// resolveStackOverride collapses the --stack flag value and the DEVCELL_STACK
// env var into a single override string. Precedence:
// flag > env > "" (empty = caller uses TOML / default).
//
// getenv is injected so tests can drive the env layer deterministically.
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
	logDockerDiagnostics(context.Background(), c)

	cellCfg, cfgErr := cfg.LoadFromOSWithDirs(c.ConfigDir, c.BaseDir)
	if cfgErr != nil {
		return fmt.Errorf("loading config: %w", cfgErr)
	}
	stack := cellCfg.Cell.ResolvedStack()
	if stackOverride != "" {
		stack = stackOverride
	}

	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}
	nixhomeSrc := runner.ResolveNixhomeRef(version.Version)
	if err := scaffold.SyncNixhome(nixhomeSrc, c.BuildDir); err != nil {
		return fmt.Errorf("sync nixhome: %w", err)
	}

	// Validate [packages.nix] before generating the flake.
	if err := cfg.ValidateNixPackages(cellCfg.Packages.Nix); err != nil {
		return err
	}

	// Write the overlay flake at .devcell/flake.nix — same generator as pure
	// path. Imports path:./nixhome (the just-synced upstream) + enables the
	// merged TOML modules. home-manager will switch against this overlay's
	// `devcell-local<arch>` output, not the upstream stack outputs directly,
	// so [cell].modules takes effect in thin builds (CELL-38 + CELL-61).
	overlayFlake := scaffold.GenerateFlakeNixWithMcp(stack, cellCfg.Cell.Modules, version.Version, true, cellCfg.Mcp.Enabled, cellCfg.Packages.Nix)
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

	// ── Platform compatibility preflight ──────────────────────────────────
	{
		nixhomeFlake := "path:" + filepath.Join(c.BuildDir, "nixhome")
		targetSystem := runner.DetectArch() + "-linux"
		preLabel := fmt.Sprintf("Platform compatibility check (%s)", targetSystem)
		sp := ux.NewProgressSpinner(preLabel)
		if err := runner.PreflightPlatformCheck(context.Background(), nixhomeFlake, targetSystem); err != nil {
			sp.Fail(preLabel)
			return err
		}
		sp.Success(preLabel)
	}

	// What we hand ThinBuildArgv is the OVERLAY dir (.devcell), mounted at
	// /opt/nixhome inside the builder. home-manager target becomes
	// `devcell-local` (matches GenerateFlakeNix's homeConfigurations output).
	nixhomeRef := runner.DockerHostPath(c.BuildDir)
	homeManagerTarget := "local"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	coreImage := cellCfg.Nix.ResolvedImage()
	tag := runner.ResolveBuildTag(imageOverride, runner.UserImageTagThin())
	volumeName := runner.ThinStoreVolume()
	containerName := "devcell-thin-builder"

	// ── Ensure core image exists for target platform ───────────────────────
	targetPlatform := runner.DockerPlatform(runner.DetectArch())
	if !runner.ImageExistsForPlatform(ctx, coreImage, targetPlatform) {
		pullLabel := fmt.Sprintf("Pulling core image %s (%s)", coreImage, targetPlatform)
		sp := ux.NewProgressSpinner(pullLabel)
		if err := runner.PullImageForPlatform(ctx, coreImage, targetPlatform, ux.Verbose); err != nil {
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
	projectName := filepath.Base(c.BaseDir)

	// [build] TOML → env, before argv construction reads them. An explicit
	// env var wins over TOML (env > toml > derived default).
	applyBuildEnv := func(envVar, val string) {
		if val != "" && os.Getenv(envVar) == "" {
			os.Setenv(envVar, val)
		}
	}
	applyBuildEnv("DEVCELL_BUILD_MEMORY", cellCfg.Build.Memory)
	applyBuildEnv("DEVCELL_BUILD_CPUS", cellCfg.Build.CPUs)
	if cellCfg.Build.MaxJobs > 0 {
		applyBuildEnv("DEVCELL_NIX_MAX_JOBS", strconv.Itoa(cellCfg.Build.MaxJobs))
	}
	if cellCfg.Build.Cores > 0 {
		applyBuildEnv("DEVCELL_NIX_CORES", strconv.Itoa(cellCfg.Build.Cores))
	}

	argv := runner.ThinBuildArgvFull(coreImage, containerName, volumeName, nixhomeRef, tag, homeManagerTarget, runner.DetectArch(), stack, modulesCSV, projectName)

	// Log the resolved build resource config under --debug.
	if lim := runner.ResolveBuildLimits(); lim.Memory != "" || lim.CPUs != "" {
		maxJobs := "auto"
		if lim.MaxJobs > 0 {
			maxJobs = fmt.Sprintf("%d", lim.MaxJobs)
		}
		cores := "default"
		if lim.Cores > 0 {
			cores = fmt.Sprintf("%d", lim.Cores)
		}
		ux.Debugf("build limits: --memory=%s --cpus=%s nix max-jobs=%s cores=%s", lim.Memory, lim.CPUs, maxJobs, cores)
	} else {
		ux.Debugf("build limits: uncapped (daemon too small for a ceiling)")
	}

	// Stream the overlay through Docker stdin. Unlike a bind mount, this is
	// resolved by the local cell process and works when the selected daemon is
	// inside Docker Desktop, Colima, or on a remote host.
	archive, err := os.CreateTemp("", "devcell-thin-nixhome-*.tar")
	if err != nil {
		sp.Fail(buildLabel + " failed")
		return fmt.Errorf("create thin nixhome archive: %w", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	if err := runner.WriteThinBuildContext(archive, c.BuildDir); err != nil {
		sp.Fail(buildLabel + " failed")
		return fmt.Errorf("archive thin nixhome: %w", err)
	}
	size, _ := archive.Seek(0, io.SeekCurrent)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		sp.Fail(buildLabel + " failed")
		return fmt.Errorf("rewind thin nixhome archive: %w", err)
	}
	ux.Debugf("thin nixhome transport: tar-stdin source=%q bytes=%d", c.BuildDir, size)

	var buf bytes.Buffer
	var out io.Writer = &buf
	if ux.Verbose {
		out = os.Stdout
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = archive
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

