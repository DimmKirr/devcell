package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/backup"
	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/op"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
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
	rootCmd.PersistentFlags().String("base-image", "", "core image for scaffold Dockerfile (default: ghcr.io/dimmkirr/devcell:core-local)")
	rootCmd.PersistentFlags().String("session-name", "", "session name for persistent home (~/.devcell/<name>)")
	rootCmd.AddCommand(
		claudeCmd,
		codexCmd,
		opencodeCmd,
		shellCmd,
		chromeCmd,
		loginCmd,
		buildCmd,
		initCmd,
		vncCmd,
		rdpCmd,
		modelsCmd,
		serveCmd,
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
}

// cellStringFlags are string flags consumed by devcell: strip the flag token
// AND its value (handles both "--flag value" and "--flag=value" forms).
var cellStringFlags = map[string]bool{
	"--engine":           true,
	"--vagrant-provider": true,
	"--vagrant-box":      true,
	"--base-image":       true,
	"--session-name":     true,
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

	// Override session name via --session-name flag.
	if sn := scanStringFlag("--session-name"); sn != "" {
		os.Setenv("DEVCELL_SESSION_NAME", sn)
	}

	// First-run: scaffold if .devcell.toml absent in project dir
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

		if err := buildImageWithSpinner(c.BuildDir, false, "Building devcell image", false); err != nil {
			return err
		}
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

	// Resolve available GUI ports — probe and bump if already bound
	if cellCfg.Cell.ResolvedGUI() {
		c.ResolveAvailablePorts()
	}

	needsBuild := scanFlag("--build") && !scanFlag("--dry-run")
	autoDetect := !scanFlag("--dry-run") && !scanFlag("--build") &&
		!runner.ImageExists(context.Background(), runner.UserImageTag())
	// DIMM-124: also rebuild when build context is newer than the existing image
	// (catches stale images left after a failed build or config change)
	var changedFiles []string
	staleImage := false
	if !scanFlag("--dry-run") && !scanFlag("--build") && !autoDetect {
		changedFiles, staleImage = runner.ChangedBuildFiles(c.BuildDir)
	}

	if needsBuild || autoDetect || staleImage {
		if autoDetect {
			fmt.Printf(" No %s image found — building automatically\n", runner.UserImageTag())
		} else if staleImage {
			fmt.Printf(" Build context changed (%s in %s) — rebuilding %s\n",
				strings.Join(changedFiles, ", "), c.BuildDir, runner.UserImageTag())
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
		if nixhomePath := cellCfg.Cell.NixhomePath; nixhomePath != "" {
			// Check if nixhome source changed since last sync.
			prevSource := scaffold.NixhomeSource(c.BuildDir)
			if prevSource != "" && prevSource != nixhomePath {
				ux.Debugf("nixhome source changed: %s → %s", prevSource, nixhomePath)
				fmt.Printf(" ⚠ nixhome source changed: %s → %s\n", prevSource, nixhomePath)
				overwrite, cErr := ux.GetConfirmation("Overwrite .devcell/nixhome with new source?")
				if cErr != nil || !overwrite {
					ux.Debugf("Skipping nixhome sync (user declined or error)")
				} else {
					ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
					if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
						return fmt.Errorf("sync nixhome: %w", err)
					}
				}
			} else {
				ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
				if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
					return fmt.Errorf("sync nixhome: %w", err)
				}
			}
		}
		if err := scaffold.RegenerateBuildContext(c.BuildDir, cellCfg); err != nil {
			return fmt.Errorf("regenerate build context: %w", err)
		}
		if err := buildImageWithSpinner(c.BuildDir, needsBuild, "Building devcell image", false); err != nil {
			return err
		}
	}

	if ux.Verbose {
		fmt.Printf(" APP_NAME: %s | VNC: localhost:%s | RDP: localhost:%s | HOME: %s\n",
			c.AppName, c.VNCPort, c.RDPPort, c.CellHome)
		baseVer, userVer := runner.ImageVersions(context.Background())
		if baseVer != "" {
			fmt.Printf(" Base image: %s\n", baseVer)
		}
		if userVer != "" {
			fmt.Printf(" User image: %s\n", userVer)
		}
		if baseVer == "" && userVer == "" {
			fmt.Printf(" Image versions: not available (missing /etc/devcell/*-image-version)\n")
		}
	}

	// Show a spinner during pre-launch setup (network, cleanup, backup, etc.).
	// In verbose mode, just print the header — debug output follows.
	var openSp *ux.ProgressSpinner
	if !ux.Verbose {
		openSp = ux.NewProgressSpinner(fmt.Sprintf("Opening Cell %s", c.AppName))
	} else {
		ux.Println(fmt.Sprintf("Opening Cell %s ...", c.AppName))
	}

	// Ensure network
	if err := runner.EnsureNetwork(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: network setup failed: %v\n", err)
	}

	// Remove orphaned stopped container from a previous crashed run
	if err := runner.RemoveOrphanedContainer(context.Background(), c.ContainerName); err != nil {
		if openSp != nil {
			openSp.Fail("setup failed")
		}
		return err
	}

	// Backup .claude.json (non-fatal)
	if err := backup.Backup(c.CellHome, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: backup failed: %v\n", err)
	}

	// Pin the container to the exact image ID just built so a concurrent
	// cell build on another terminal can't swap the tag under us mid-launch.
	imageID, err := runner.LocalImageID(context.Background())
	if err != nil {
		// Non-fatal: fall back to the mutable tag.
		imageID = ""
	}

	// Inject system prompt for Claude Code — describes container environment,
	// bind mounts, and host path mappings so Claude understands its runtime context.
	if binary == "claude" {
		prompt := runner.BuildSystemPrompt(c, cellCfg)
		defaultFlags = append(defaultFlags, "--append-system-prompt", prompt)
	}

	// Resolve git identity from host config (follows symlinks, includes, XDG paths).
	// Only if no explicit git env or [git] toml section — those take priority in BuildArgv.
	if os.Getenv("GIT_AUTHOR_NAME") == "" && !cellCfg.Git.HasIdentity() {
		if extraEnv == nil {
			extraEnv = make(map[string]string)
		}
		if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				extraEnv["GIT_AUTHOR_NAME"] = name
				extraEnv["GIT_COMMITTER_NAME"] = name
			}
		}
		if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
			if email := strings.TrimSpace(string(out)); email != "" {
				extraEnv["GIT_AUTHOR_EMAIL"] = email
				extraEnv["GIT_COMMITTER_EMAIL"] = email
			}
		}
	}

	// Resolve 1Password items → set in process env so docker inherits via -e KEY
	var inheritEnv []string
	opDocs := cellCfg.Op.ResolvedDocuments()
	if len(opDocs) > 0 {
		if openSp != nil {
			openSp.UpdateText(fmt.Sprintf("Opening Cell %s (resolving secrets)", c.AppName))
		}
		ux.Debugf("1Password: resolving %d document(s): %v", len(opDocs), opDocs)
		if _, err := exec.LookPath("op"); err == nil {
			resolved, err := op.ResolveItems(opDocs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: 1Password: %v\n", err)
			} else {
				keys := make([]string, 0, len(resolved))
				for k, v := range resolved {
					os.Setenv(k, v)
					inheritEnv = append(inheritEnv, k)
					keys = append(keys, k)
				}
				ux.Debugf("1Password: resolved %d secret(s): %v", len(keys), keys)
			}
		} else {
			ux.Debugf("1Password: op CLI not found, skipping secret resolution")
		}
	}

	// Stop spinner before handing terminal to child process.
	if openSp != nil {
		openSp.Stop()
	}

	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cellCfg,
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		Debug:        ux.Verbose,
		Image:        imageID,
		ExtraEnv:     extraEnv,
		InheritEnv:   inheritEnv,
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

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", argv[0], err)
	}

	// Forward signals to the child process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			_ = cmd.Process.Signal(sig)
		}
	}()

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
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
