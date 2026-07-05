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
	"time"

	"github.com/DimmKirr/devcell/internal/backup"
	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/op"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/session"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "cell",
	SilenceUsage: true, // don't dump usage after handled errors
	Short:        "Run AI coding agents in a devcell container",
	Long: `cell launches AI coding agents (claude, codex, opencode) and utility
tools inside a consistent Docker dev environment.`,
	Args: cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			fmt.Fprintf(os.Stderr, "cell %s\n", version.Full())
		}
		// Set runner globals BEFORE any subcommand RunE so that
		// runner.UserImageTag() / UserImageTagPure() / PickImageTag() reflect
		// the project's stack from .devcell.toml. Without this, `cell build`
		// (which fires buildCmd.RunE, NOT rootCmd.RunE) leaves Stack="" and
		// tags every image as devcell-user:base-pure regardless of what
		// stack the nix derivation actually built.
		//
		// Best-effort: silently skips when config can't be loaded (e.g.,
		// `cell --help` before cwd has a .devcell.toml, or stray cwd).
		// Subcommands that need a working config fail with a better error
		// later in their own RunE.
		if c, err := config.LoadFromOS(); err == nil {
			cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
			runner.Stack = cellCfg.Cell.ResolvedStack()
			runner.Modules = cellCfg.Cell.Modules
			runner.PerCellImage = cellCfg.Cell.ResolvedPerCellImage()
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown command %q — run 'cell --help' for usage", args[0])
		}
		return cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\n cell %s\n", version.Full())
		baseVer, userVer := runner.ImageVersions(context.Background())
		if baseVer != "" {
			fmt.Fprintf(os.Stderr, " Base image: %s\n", baseVer)
		}
		if userVer != "" {
			fmt.Fprintf(os.Stderr, " User image: %s\n", userVer)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.Version = version.Full()
	rootCmd.PersistentFlags().Bool("build", false, "rebuild image before running (forces --no-cache)")
	rootCmd.PersistentFlags().Bool("dry-run", false, "print docker run argv and exit without running")
	rootCmd.PersistentFlags().Bool("plain-text", false, "disable spinners, use plain log output (for CI/non-TTY)")
	rootCmd.PersistentFlags().Bool("debug", false, "plain-text mode plus stream full build log to stdout")
	rootCmd.PersistentFlags().String("format", "text", "output format: text, yaml, or json")
	rootCmd.PersistentFlags().String("engine", "docker", "execution engine: docker or vagrant")
	rootCmd.PersistentFlags().Bool("macos", false, "use macOS VM via Vagrant (alias for --engine=vagrant)")
	rootCmd.PersistentFlags().String("vagrant-provider", "utm", "Vagrant provider (e.g. utm)")
	rootCmd.PersistentFlags().String("vagrant-box", "", "Vagrant box name override")
	rootCmd.PersistentFlags().String("base-image", "", "core image for scaffold Dockerfile (default: ghcr.io/devcell-sh/devcell:core-local)")
	rootCmd.PersistentFlags().String("cell-name", "", "cell name for persistent home (~/.devcell/<name>)")
	rootCmd.AddCommand(
		claudeCmd,
		codexCmd,
		opencodeCmd,
		geminiCmd,
		shellCmd,
		chromeCmd,
		loginCmd,
		buildCmd,
		initCmd,
		vncCmd,
		rdpCmd,
		modelsCmd,
		modulesCmd,
		serveCmd,
		authCmd,
	)
}

// applyOutputFlags reads --plain-text and --debug and sets ux globals.
// Must be called at the start of each RunE (PersistentPreRun is skipped
// for commands with DisableFlagParsing=true).
// applyOutputFlags scans os.Args for --plain-text and --debug.
// We cannot use cobra's flag parsing here because agent subcommands set
// DisableFlagParsing=true, which prevents cobra from parsing persistent
// flags on the root command.
func applyOutputFlags() {
	for _, arg := range osArgs {
		switch arg {
		case "--plain-text":
			ux.LogPlainText = true
		case "--debug":
			ux.LogPlainText = true
			ux.Verbose = true
		}
	}
	if f := scanStringFlag("--format"); f != "" {
		ux.OutputFormat = f
	}
}

// cellBoolFlags are boolean flags consumed by devcell: strip the flag token only.
var cellBoolFlags = map[string]bool{
	"--build":      true,
	"--dry-run":    true,
	"--plain-text": true,
	"--debug":      true,
	"--macos":      true,
	"--ollama":     true,
	"--impure":     true, // legacy Dockerfile path (CELL-165 canonical name)
	"--debian":     true, // deprecated alias for --impure (kept stripping for one release)
	"--pure":       true, // silent no-op after flip; kept stripped from forwarded args
	"--nix-daemon": true, // enable nix-daemon inside container for runtime package installs
	"--thin":       true, // thin image mode — nix store on Docker volume (CELL-156)
	"--no-thin":    true, // disable thin mode (thick image)
	"--thick":      true, // alias for --no-thin
	"--no-1password": true, // skip [op] documents resolution at cell-open (CELL-42)
}

// cellStringFlags are string flags consumed by devcell: strip the flag token
// AND its value (handles both "--flag value" and "--flag=value" forms).
var cellStringFlags = map[string]bool{
	"--engine":           true,
	"--vagrant-provider": true,
	"--vagrant-box":      true,
	"--base-image":       true,
	"--cell-name":        true,
	"--format":           true,
}

// stripCellFlags removes devcell-specific flags (and their values) from args
// so they are not forwarded to the inner binary.
func stripCellFlags(args []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if cellBoolFlags[a] {
			continue
		}
		if cellStringFlags[a] {
			skipNext = true
			continue
		}
		// "--flag=value" form for string flags
		stripped := false
		for f := range cellStringFlags {
			if strings.HasPrefix(a, f+"=") {
				stripped = true
				break
			}
		}
		if stripped {
			continue
		}
		out = append(out, a)
	}
	return out
}

// runAgent is the shared pre-exec sequence for all agent and shell commands.
// extraEnv is an optional map of additional env vars injected into the container
// (e.g. OPENCODE_CONFIG_CONTENT). Pass nil when not needed.
func runAgent(binary string, defaultFlags, userArgs []string, extraEnv map[string]string) error {
	userArgs = stripCellFlags(userArgs)
	applyOutputFlags()
	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Override base image tag for scaffold Dockerfile if --base-image is set.
	if bi := scanStringFlag("--base-image"); bi != "" {
		os.Setenv("DEVCELL_BASE_IMAGE", bi)
	}

	// Override cell name via --cell-name flag.
	if sn := scanStringFlag("--cell-name"); sn != "" {
		os.Setenv("DEVCELL_CELL_NAME", sn)
	}

	// First-run: scaffold .devcell.toml + .devcell/ files. Image acquisition
	// is owned by the unified pure-path orchestrator below — scaffolding
	// must not eagerly invoke a docker build that the next step won't even
	// use (the orchestrator's first try is a registry pull of the pure tag).
	if !scaffold.IsInitialized(c.BaseDir) {
		globalCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		result, err := RunInitFlow(InitFlowOptions{
			BaseDir:    c.BaseDir,
			ConfigDir:  c.ConfigDir,
			NixhomeSrc: globalCfg.Cell.NixhomePath,
			Yes:        false,
		})
		if err != nil {
			return err
		}
		c.BuildDir = config.ResolveBuildDir(c.BaseDir, c.ConfigDir, true)
		fmt.Printf(" First run — scaffolding %s (stack: %s)\n", c.BaseDir, result.Stack)
	}

	// Vagrant engine branch
	// Priority: CLI flag > [cell] config > default.
	cellCfgForEngine := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	engine := scanStringFlag("--engine")
	if engine == "" {
		engine = cellCfgForEngine.Cell.Engine
	}
	if scanFlag("--macos") {
		engine = "vagrant"
	}
	if engine == "vagrant" {
		vagrantBox := scanStringFlag("--vagrant-box")
		if vagrantBox == "" {
			vagrantBox = cellCfgForEngine.Cell.VagrantBox
		}
		if vagrantBox == "" {
			vagrantBox = "utm/bookworm"
		}
		vagrantProvider := scanStringFlag("--vagrant-provider")
		if vagrantProvider == "" {
			vagrantProvider = cellCfgForEngine.Cell.VagrantProvider
		}
		if vagrantProvider == "" {
			vagrantProvider = "utm"
		}
		cellCfgForVagrant := cellCfgForEngine
		return runVagrantAgent(
			binary, defaultFlags, userArgs,
			c.BuildDir, c.BaseDir,
			cellCfgForVagrant,
			vagrantBox, vagrantProvider,
			c.VNCPort, c.RDPPort,
			c.HostHome,
			scanFlag("--dry-run"),
		)
	}

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)

	// Expand ${VAR}/$VAR references in [env] against the host shell.
	// Strict-miss: any unset (or empty) reference aborts boot with a
	// consolidated error listing every miss + its [env].<key> path —
	// fixing the user's shell, not the TOML, is the intended remedy.
	if err := cfg.ExpandEnv(cellCfg.Env, os.LookupEnv); err != nil {
		return fmt.Errorf("%w", err)
	}

	// Set stack/modules so UserImageTag() produces stack-based tags.
	runner.Stack = cellCfg.Cell.ResolvedStack()
	runner.Modules = cellCfg.Cell.Modules
	runner.PerCellImage = cellCfg.Cell.ResolvedPerCellImage()

	// After the 2026-05-15 flip (CELL-183), pure is the default for every
	// agent (claude, shell, codex, gemini). `--impure` (CELL-165 canonical;
	// `--debian` is a deprecated alias) opts into the legacy Dockerfile
	// build path. `--pure` is kept as a silent no-op (same as default).
	impure := scanFlag("--impure") || scanFlag("--debian")
	thin := !scanFlag("--no-thin") && !scanFlag("--thick") && (scanFlag("--thin") || cellCfg.Cell.ResolvedThin())
	if !thin {
		runner.WarnThickDeprecation()
	}
	imageTag := func() string {
		if thin {
			return runner.PickImageTagThin()
		}
		return runner.PickImageTag(impure)
	}
	dryRun := scanFlag("--dry-run")
	explicitBuild := scanFlag("--build")

	// Resolve available GUI ports — probe and bump if already bound
	if cellCfg.Cell.ResolvedGUI() {
		c.ResolveAvailablePorts()
	}

	// ── Image acquisition ────────────────────────────────────────────────────
	// Default (pure): runner.AcquireImage walks the fallback chain —
	// local → pull-pure → pull-impure → build (pure if host nix, otherwise
	// impure docker build). Each closure performs its action; on the last
	// action's failure the user sees a joined chain error.
	//
	// --impure (legacy CLI flag): autoDetect (missing image) + staleness check.
	// Staleness is not consulted for the pure path: pure images are
	// content-addressed, so a local tag equals what a rebuild would produce
	// from the same flake.lock.
	//
	// Daemon preflight: surface a single actionable error if docker is down
	// before any pull/build attempt (CELL-44). Skip in dry-run.
	if !dryRun {
		if err := runner.DockerDaemonReachable(context.Background()); err != nil {
			return err
		}
	}
	// ── Thin image path (CELL-156) ──────────────────────────────────────────
	if thin {
		needsBuild := false
		reason := ""
		switch {
		case explicitBuild:
			needsBuild = true
		case dryRun:
			// no-op
		case !runner.ImageExists(context.Background(), imageTag()):
			needsBuild, reason = true, fmt.Sprintf(" No %s image found — building automatically (thin mode)", imageTag())
		case !runner.VolumeHydrated(runner.ThinStoreVolume(), runner.ThinEntrypointSentinel,
			func(v string) bool { return runner.VolumeExists(context.Background(), v) },
			func(v, p string) bool { return runner.VolumeContains(context.Background(), v, p) }):
			needsBuild, reason = true, " /nix volume is missing or unpopulated — rebuilding (thin mode, CELL-38)"
		}
		if needsBuild {
			if reason != "" {
				fmt.Println(reason)
			}
			if err := runBuildThin(c, "", "", false); err != nil {
				return err
			}
		}
	} else if !impure {
		// HasNix means "nix is on PATH AND can build the target arch from
		// this host" (the preflight catches macOS-without-linux-builder).
		// When false the orchestrator skips ActionBuildPure and runs
		// ActionBuildImpure instead — docker build still works without nix
		// on the host because nix runs inside the build.
		_, nixErr := exec.LookPath("nix")
		hasNix := nixErr == nil && runner.PreflightNixBuilder(runner.Stack) == nil

		err := runner.AcquireImage(context.Background(), runner.AcquireDeps{
			Inputs: runner.LaunchInputs{
				DryRun:        dryRun,
				ExplicitBuild: explicitBuild,
				LocalExists:   runner.ImageExists(context.Background(), imageTag()),
				HasNix:        hasNix,
			},
			PullPure: pullWithSpinner(
				runner.StackImageTagPure(runner.Stack), runner.PullAndTagPure),
			PullImpure: pullWithSpinner(
				runner.StackImageTagImpure(runner.Stack), runner.PullAndTagImpure),
			BuildPure: func(context.Context) error {
				// Passing "" means runBuildPure falls back to the TOML-resolved
				// stack (see CELL-93). The user overrides via `cell build
				// --stack <name>` explicitly.
				return runBuildPure(c, "")
			},
			BuildImpure: func(ctx context.Context) error {
				return runFallbackImpureBuild(ctx, c, cellCfg)
			},
		})
		if err != nil {
			return err
		}
	} else {
		needsBuild := explicitBuild && !dryRun
		autoDetect := !dryRun && !explicitBuild &&
			!runner.ImageExists(context.Background(), imageTag())
		var changedFiles []string
		staleImage := false
		if !dryRun && !explicitBuild && !autoDetect {
			changedFiles, staleImage = runner.ChangedBuildFiles(c.BuildDir)
		}
		if needsBuild || autoDetect || staleImage {
			if autoDetect {
				fmt.Printf(" No %s image found — building automatically\n", imageTag())
			} else if staleImage {
				fmt.Printf(" Build context changed (%s in %s) — rebuilding %s\n",
					strings.Join(changedFiles, ", "), c.BuildDir, imageTag())
				if ux.Verbose {
					for _, f := range changedFiles {
						if diff := runner.DiffBuildFile(c.BuildDir, f); diff != "" {
							fmt.Printf("\n%s\n", diff)
						}
					}
				}
			}
			if err := config.EnsureBuildDir(c.BuildDir); err != nil {
				return fmt.Errorf("ensure build dir: %w", err)
			}
			if err := syncNixhomeWithConfirmation(c, cellCfg); err != nil {
				return err
			}
			if err := scaffold.RegenerateBuildContext(c.BuildDir, cellCfg); err != nil {
				return fmt.Errorf("regenerate build context: %w", err)
			}
			if err := buildImageWithSpinner(c.BuildDir, needsBuild, "Building devcell image", false); err != nil {
				return err
			}
		}
	}

	// Cell-open banner — CELL-48. Always print the compact header so users
	// see "which cell · which project · which pane" at every launch. The cell
	// name is always shown (including the `main` default) — it's a real
	// persistent identity with its own `~/.devcell/<name>/` home, not a
	// placeholder, and surfacing it teaches the cell model.
	project := filepath.Base(c.BaseDir)
	fmt.Println(" " + ux.Banner(c.CellName, project, c.Bunk))

	if ux.Verbose {
		fmt.Println()
		const keyW = 8 // longest key is "Timezone" / "Modules" / "Network"
		// Project / Cell
		fmt.Println("   " + ux.KV(keyW, "Project", project+ux.StyleMuted.Render("  "+c.BaseDir)))
		if c.CellName != "" {
			fmt.Println("   " + ux.KV(keyW, "Cell", c.CellName+ux.StyleMuted.Render("  "+c.CellHome)))
		}
		// Image — current tag + size, more useful than the in-container
		// /etc/devcell/*-image-version strings (which can be missing).
		tag := imageTag()
		imgLine := tag
		if size := runner.LocalImageSize(context.Background(), tag); size > 0 {
			imgLine += ux.StyleMuted.Render("  " + runner.HumanBytes(size))
		}
		fmt.Println("   " + ux.KV(keyW, "Image", imgLine))
		// Modules source — CELL-48 core ask.
		fmt.Println("   " + ux.KV(keyW, "Modules", cellCfg.Cell.DescribeModulesSource()))
		// Identity / network — surfaces the values bot-detection-relevant
		// settings will resolve to inside the container, so the user can
		// confirm at boot whether MAC / hostname / TZ / locale match the
		// expected persistent identity.
		mac := cellCfg.Cell.MacAddress
		if mac == "" {
			mac = "auto"
		}
		hostname := cellCfg.Cell.ResolvedHostname(c.AppName)
		if envHost := os.Getenv("DEVCELL_HOSTNAME"); envHost != "" {
			hostname = envHost
		}
		fmt.Println("   " + ux.KV(keyW, "Network", "devcell-network"+ux.StyleMuted.Render(" · hostname "+hostname+" · MAC "+mac)))
		// Locale + timezone — combine on one row.
		tz := cellCfg.Cell.Timezone
		if tz == "" {
			if envTZ := os.Getenv("TZ"); envTZ != "" {
				tz = envTZ + " (from host $TZ)"
			} else {
				tz = "(container default)"
			}
		}
		locale := cellCfg.Cell.Locale
		if locale == "" {
			if envLang := os.Getenv("LANG"); envLang != "" && envLang != "POSIX" && envLang != "C" {
				locale = envLang + " (from host $LANG)"
			} else {
				locale = "en_US.UTF-8 (default)"
			}
		}
		fmt.Println("   " + ux.KV(keyW, "Locale", locale))
		fmt.Println("   " + ux.KV(keyW, "Timezone", tz))
		fmt.Println("   " + ux.KV(keyW, "Ports", "VNC localhost:"+c.VNCPort+ux.StyleMuted.Render(" · ")+"RDP localhost:"+c.RDPPort))
		// Boot dir — where the BootDirWatcher polls for in-container sentinels.
		// Useful in --debug: `ls $bootdir/` after a boot shows the chain of
		// fragments that fired (post-mortem). CELL-264.
		fmt.Println("   " + ux.KV(keyW, "Boot", filepath.Join(c.CellHome, "boot")))
		fmt.Println()
	}

	// CELL-262: cell-open phases as a permanent checklist via PhaseRunner.
	// Each row lands as `✓ <name> [— <detail>] <elapsed>` and persists across
	// the docker exec handoff, so the user sees the full boot story above
	// claude's first prompt. Replaces the prior "Opening Cell" spinner +
	// inline stderr warnings + silent successes mix.
	//
	// 7-phase set (Docker daemon and Volume hydrated stay as silent
	// upstream gates — surfacing them as ✓ rows for work that already ran
	// reads as noise). Non-fatal phases discard the returned error with `_ =`;
	// fatal phases propagate via `if err := ...; err != nil { return err }`.
	pr := &ux.PhaseRunner{}
	ctx := context.Background()

	_ = pr.Phase("Network", func() error { return runner.EnsureNetwork(ctx) })

	if err := pr.Phase("Orphan check", func() error {
		return runner.RemoveOrphanedContainer(ctx, c.ContainerName)
	}); err != nil {
		return err
	}

	_ = pr.Phase("Backup", func() error { return backup.Backup(c.CellHome, time.Now()) })

	// Pin the container to the exact image ID so a concurrent `cell build`
	// can't swap the tag under us mid-launch. Falls back to the mutable tag
	// on failure (current behaviour) — kept silent inside the closure so the
	// row stays a ✓ either way.
	var imageID string
	_ = pr.PhaseDetailed("Image pin", func() (string, error) {
		id, idErr := runner.LocalImageIDFor(ctx, imageTag())
		if idErr != nil {
			imageID = imageTag()
			return imageID, nil
		}
		imageID = id
		short := id
		if len(short) > 19 { // "sha256:abcdef012345" = 19 chars
			short = short[:19]
		}
		return short, nil
	})

	// Inject system prompt for Claude Code — container context (mounts, host
	// paths, constraints) plus the operator/project prompt resolved from env
	// vars and devcell.toml. See runner.AssembleSystemPrompt for the full
	// source-precedence chain. Fatal: a bad system prompt produces a broken
	// claude session, fail loudly here.
	if binary == "claude" {
		if err := pr.PhaseDetailed("System prompt", func() (string, error) {
			prompt, spErr := runner.AssembleSystemPrompt(c, cellCfg, runner.ResolveOpts{
				EnvFile:    os.Getenv("DEVCELL_SYSTEM_PROMPT_FILE"),
				EnvInline:  os.Getenv("DEVCELL_SYSTEM_PROMPT"),
				CellCfg:    cellCfg,
				CfgBaseDir: c.BaseDir,
			})
			if spErr != nil {
				return "", spErr
			}
			defaultFlags = append(defaultFlags, "--append-system-prompt", prompt)
			return fmt.Sprintf("%d bytes", len(prompt)), nil
		}); err != nil {
			return fmt.Errorf("system prompt: %w", err)
		}
	}

	// Resolve git identity from host config — only when neither env nor TOML
	// already provides it. Non-fatal; row is "not configured" when both
	// `git config user.name` and `user.email` are absent.
	if os.Getenv("GIT_AUTHOR_NAME") == "" && !cellCfg.Git.HasIdentity() {
		_ = pr.PhaseDetailed("Git identity", func() (string, error) {
			var name, email string
			if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
				name = strings.TrimSpace(string(out))
			}
			if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
				email = strings.TrimSpace(string(out))
			}
			if name == "" && email == "" {
				return "not configured", nil
			}
			if extraEnv == nil {
				extraEnv = make(map[string]string)
			}
			if name != "" {
				extraEnv["GIT_AUTHOR_NAME"] = name
				extraEnv["GIT_COMMITTER_NAME"] = name
			}
			if email != "" {
				extraEnv["GIT_AUTHOR_EMAIL"] = email
				extraEnv["GIT_COMMITTER_EMAIL"] = email
			}
			switch {
			case name != "" && email != "":
				return name + " <" + email + ">", nil
			case name != "":
				return name, nil
			default:
				return email, nil
			}
		})
	}

	// Loading secrets — CELL-261 phase, now expressed through PhaseRunner.
	// Suppressed entirely when no [op].documents are configured, or when the
	// user opted out via --no-1password / DEVCELL_NO_1PASSWORD.
	var inheritEnv []string
	opDocs := cellCfg.Op.ResolvedDocuments()
	skipOp := scanFlag("--no-1password")
	noOpEnv := os.Getenv("DEVCELL_NO_1PASSWORD")
	switch {
	case op.ShouldResolve(skipOp, noOpEnv, opDocs):
		ux.Debugf("1Password: resolving %d document(s): %v", len(opDocs), opDocs)
		_ = pr.PhaseDetailedRunning("Loading secrets (please authorize 1Password)", "Loaded secrets", func() (string, error) {
			if _, err := exec.LookPath("op"); err != nil {
				return "", fmt.Errorf("1Password CLI not installed")
			}
			resolved, errs := op.ResolveItems(opDocs)
			for _, e := range errs {
				ux.Debugf("1Password: %v", e)
			}
			keys := make([]string, 0, len(resolved))
			for k, v := range resolved {
				os.Setenv(k, v)
				inheritEnv = append(inheritEnv, k)
				keys = append(keys, k)
			}
			ux.Debugf("1Password: resolved %d secret(s) from %d document(s) (%d failed): %v",
				len(keys), len(opDocs)-len(errs), len(errs), keys)
			// Total failure (every item errored, nothing resolved) is a real
			// boot failure — surface it as ✗ instead of a green ✓ with a
			// misleading "0 resolved" detail. Partial success still renders
			// as ✓ because the cell can boot with whatever secrets landed.
			if len(resolved) == 0 && len(errs) > 0 {
				if len(opDocs) == 1 {
					return "", fmt.Errorf("could not read %q from 1Password", opDocs[0])
				}
				return "", fmt.Errorf("could not read any of %d 1Password documents", len(opDocs))
			}
			return ux.FormatSecretsPhase(len(resolved), len(errs)), nil
		})
	case len(opDocs) > 0 && (skipOp || noOpEnv != ""):
		ux.Debugf("1Password: skipped (--no-1password / DEVCELL_NO_1PASSWORD)")
	}

	// Final ✓ row before docker exec takes the TTY. The phase checklist
	// stays on screen — the child TUI (claude, codex, …) draws on the row
	// immediately below `✓ Cell ready`, so users keep the full boot story
	// as scrollback above their session.
	pr.Seal("Cell ready")

	// CELL-264: in-container progress via fsnotify sentinel files. Start
	// a BootDirWatcher on a per-cell directory BEFORE docker run so the
	// container's entrypoint fragments can `touch $DEVCELL_BOOT_DIR/<name>`
	// as they boot. Each file CREATE becomes a row on the host between
	// Cell ready and the TTY handoff.
	//
	// Directory bind-mounts work universally on every Docker platform —
	// Linux native, macOS/Windows Docker Desktop, Lima, OrbStack — which
	// is why we ditched CELL-263 (sd_notify unix-socket bind-mounts had
	// transport issues through Docker Desktop's virtiofs).
	//
	// Stale-state hygiene: wipe the dir at the start of each launch so
	// leftover sentinels from a crashed prior run don't fire spurious
	// "ready" events before the new boot starts emitting them.
	bootDir := filepath.Join(c.CellHome, "boot")
	_ = os.RemoveAll(bootDir)
	bootWatcher := &runner.BootDirWatcher{}
	var bootDirEnv string
	var bootEvents <-chan runner.BootEvent
	if events, err := bootWatcher.Start(bootDir); err != nil {
		ux.Debugf("boot watcher: %v (continuing without in-container progress)", err)
	} else {
		bootDirEnv = bootDir
		bootEvents = events
		defer bootWatcher.Close()
	}

	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cellCfg,
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		Debug:        ux.Verbose,
		NixDaemon:    scanFlag("--nix-daemon"),
		Image:        imageID,
		ExtraEnv:     extraEnv,
		InheritEnv:   inheritEnv,
		ThinImage:    thin,
		BootDir:      bootDirEnv,
	}
	argv := runner.BuildArgv(spec, runner.OsFS, exec.LookPath)

	if scanFlag("--dry-run") {
		fmt.Println(shellJoin(argv))
		return nil
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sess, sessErr := session.Begin(c.BaseDir, binary, userArgs)
	if sessErr != nil {
		ux.Debugf("session begin: %v", sessErr)
	}

	if err := cmd.Start(); err != nil {
		if sess != nil {
			_ = sess.Finish(c.BaseDir, err)
		}
		return fmt.Errorf("start %q: %w", argv[0], err)
	}

	// CELL-264: consume in-container boot events. Each sentinel file
	// CREATE opens or seals a row. Entrypoint is mostly quiet in non-debug
	// mode, so host rows and container stdout rarely interleave during
	// boot; once the entrypoint emits boot.ready and exec's into the
	// binary (claude/zsh), the consumer returns and stops rendering.
	if bootEvents != nil {
		go runner.ConsumeBootEvents(bootEvents)
	}

	// Forward signals to the child process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			_ = cmd.Process.Signal(sig)
		}
	}()

	waitErr := cmd.Wait()
	if sess != nil {
		if err := sess.Finish(c.BaseDir, waitErr); err != nil {
			ux.Debugf("session finish: %v", err)
		}
	}
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return waitErr
	}
	return nil
}

// osArgs is the argument source for flag scanning. Overridable in tests.
var osArgs = os.Args

// scanFlag checks osArgs for a boolean flag.
// Needed because DisableFlagParsing prevents cobra from parsing persistent
// flags on agent subcommands.
func scanFlag(flag string) bool {
	for _, arg := range osArgs {
		if arg == flag {
			return true
		}
	}
	return false
}

// scanStringFlag scans osArgs for a string flag, handling both
// "--flag value" and "--flag=value" forms. Returns "" if not found.
func scanStringFlag(flag string) string {
	for i, arg := range osArgs {
		if arg == flag && i+1 < len(osArgs) {
			return osArgs[i+1]
		}
		if strings.HasPrefix(arg, flag+"=") {
			return arg[len(flag)+1:]
		}
	}
	return ""
}

// buildImageWithSpinner runs docker build with a spinner.
// In verbose mode (--debug), build output streams to stdout.
// In quiet mode, output is captured and replayed to stderr only on failure.
// If silent is true, the spinner is cleared on success (no lingering output).
func buildImageWithSpinner(configDir string, noCache bool, label string, silent bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var buf bytes.Buffer
	var out io.Writer = &buf
	if ux.Verbose {
		out = os.Stdout
	}
	sp := ux.NewProgressSpinner(label)
	if err := runner.BuildImage(ctx, configDir, noCache, ux.Verbose, out); err != nil {
		sp.Fail(label + " failed")
		if !ux.Verbose {
			if hint := ux.ClassifyBuildOutput(buf.String()); hint != nil {
				ux.PrintBuildErrorHint(hint)
			} else if buf.Len() > 0 {
				fmt.Fprint(os.Stderr, buf.String())
			}
		}
		return err
	}
	if silent {
		sp.Stop()
	} else {
		sp.Success(label)
	}
	return nil
}

// pullWithSpinner returns an AcquireDeps closure that calls pullFn with the
// active stack, wrapping it in a spinner for non-verbose mode. Used to build
// both the pure-pull and impure-pull dependencies from a single shape.
func pullWithSpinner(
	remoteTag string,
	pullFn func(context.Context, string, bool) error,
) func(context.Context) error {
	return func(ctx context.Context) error {
		label := fmt.Sprintf("Pulling %s", remoteTag)
		var sp *ux.ProgressSpinner
		if !ux.Verbose {
			sp = ux.NewProgressSpinner(label)
		} else {
			ux.Debugf("%s", label)
		}
		if err := pullFn(ctx, runner.Stack, ux.Verbose); err != nil {
			if sp != nil {
				sp.Stop()
			}
			ux.Debugf("pull %s failed: %v", remoteTag, err)
			return err
		}
		if sp != nil {
			sp.Success("Pulled " + remoteTag)
		}
		return nil
	}
}

// syncNixhomeWithConfirmation syncs the configured nixhome path into the
// build context, prompting the user before overwriting an existing sync that
// came from a different source. No-op when no nixhome path is configured.
//
// Only the impure (Dockerfile) build path needs this — runBuildPure resolves
// and consumes nixhome internally via runner.ResolvePureNixhomeRef.
func syncNixhomeWithConfirmation(c config.Config, cellCfg cfg.CellConfig) error {
	nixhomePath := cellCfg.Cell.NixhomePath
	if nixhomePath == "" {
		return nil
	}
	prevSource := scaffold.NixhomeSource(c.BuildDir)
	if prevSource != "" && prevSource != nixhomePath {
		ux.Debugf("nixhome source changed: %s → %s", prevSource, nixhomePath)
		fmt.Printf(" ⚠ nixhome source changed: %s → %s\n", prevSource, nixhomePath)
		overwrite, cErr := ux.GetConfirmation("Overwrite .devcell/nixhome with new source?")
		if cErr != nil || !overwrite {
			ux.Debugf("Skipping nixhome sync (user declined or error)")
			return nil
		}
	}
	ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
	if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
		return fmt.Errorf("sync nixhome: %w", err)
	}
	return nil
}

// runFallbackImpureBuild is the BuildImpure closure for the pure path's
// final fallback: docker-build the scaffolded Dockerfile and retag the
// result under the pure tag so a subsequent launch finds it locally without
// retrying the whole pull chain. Reached when both registry pulls failed
// and the host has no usable nix.
func runFallbackImpureBuild(ctx context.Context, c config.Config, cellCfg cfg.CellConfig) error {
	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}
	if err := syncNixhomeWithConfirmation(c, cellCfg); err != nil {
		return err
	}
	if err := scaffold.RegenerateBuildContext(c.BuildDir, cellCfg); err != nil {
		return fmt.Errorf("regenerate build context: %w", err)
	}
	if err := buildImageWithSpinner(
		c.BuildDir, false, "Building devcell image (impure fallback)", false); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "docker", "tag",
		runner.UserImageTag(), runner.UserImageTagPure()).Run(); err != nil {
		ux.Debugf("retag %s → %s failed: %v",
			runner.UserImageTag(), runner.UserImageTagPure(), err)
	}
	return nil
}

// updateFlakeLockWithSpinner runs nix flake lock/update with a spinner.
// Same pattern as buildImageWithSpinner.
func updateFlakeLockWithSpinner(configDir string, lockOnly bool, label string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var buf bytes.Buffer
	var out io.Writer = &buf
	if ux.Verbose {
		out = os.Stdout
	}
	sp := ux.NewProgressSpinner(label)
	if err := runner.UpdateFlakeLock(ctx, configDir, lockOnly, ux.Verbose, out); err != nil {
		sp.Fail(label + " failed")
		if !ux.Verbose && buf.Len() > 0 {
			fmt.Fprint(os.Stderr, buf.String())
		}
		return err
	}
	sp.Success(label)
	return nil
}

func shellJoin(argv []string) string {
	var parts []string
	for _, a := range argv {
		if strings.ContainsAny(a, " \t\"'\\") {
			parts = append(parts, "'"+a+"'")
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}
