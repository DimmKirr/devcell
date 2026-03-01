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
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cell",
	Short: "Run AI coding agents in a devcell container",
	Long: `cell launches AI coding agents (claude, codex, opencode) and utility
tools inside a consistent Docker dev environment.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown command %q — run 'cell --help' for usage", args[0])
		}
		return cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Version = version.Version
	rootCmd.PersistentFlags().Bool("build", false, "rebuild image before running (forces --no-cache)")
	rootCmd.PersistentFlags().Bool("dry-run", false, "print docker run argv and exit without running")
	rootCmd.PersistentFlags().Bool("plain-text", false, "disable spinners, use plain log output (for CI/non-TTY)")
	rootCmd.PersistentFlags().Bool("debug", false, "plain-text mode plus stream full build log to stdout")
	rootCmd.PersistentFlags().String("engine", "docker", "execution engine: docker or vagrant")
	rootCmd.PersistentFlags().Bool("macos", false, "use macOS VM via Vagrant (alias for --engine=vagrant)")
	rootCmd.PersistentFlags().String("vagrant-provider", "utm", "Vagrant provider (e.g. utm)")
	rootCmd.PersistentFlags().String("vagrant-box", "", "Vagrant box name override")
	rootCmd.AddCommand(
		claudeCmd,
		codexCmd,
		opencodeCmd,
		shellCmd,
		buildCmd,
		initCmd,
		vncCmd,
		rdpCmd,
		chromeCmd,
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
}

// cellBoolFlags are boolean flags consumed by devcell: strip the flag token only.
var cellBoolFlags = map[string]bool{
	"--build":      true,
	"--dry-run":    true,
	"--plain-text": true,
	"--debug":      true,
	"--macos":      true,
}

// cellStringFlags are string flags consumed by devcell: strip the flag token
// AND its value (handles both "--flag value" and "--flag=value" forms).
var cellStringFlags = map[string]bool{
	"--engine":           true,
	"--vagrant-provider": true,
	"--vagrant-box":      true,
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
func runAgent(binary string, defaultFlags, userArgs []string) error {
	userArgs = stripCellFlags(userArgs)
	applyOutputFlags()
	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// First-run: scaffold if devcell.toml absent
	if !scaffold.IsInitialized(c.ConfigDir) {
		fmt.Printf(" First run — scaffolding %s\n", c.ConfigDir)
		if err := scaffold.Scaffold(c.ConfigDir); err != nil {
			return fmt.Errorf("scaffold: %w", err)
		}
		ok, promptErr := ux.GetConfirmation("Build image now? (~5 min first time)")
		if promptErr == nil && ok {
			if buildErr := buildImageWithSpinner(c.ConfigDir, false, "Building devcell image", false); buildErr != nil {
				return buildErr
			}
		}
	}

	// Vagrant engine branch — stub, not yet implemented
	engine := scanStringFlag("--engine")
	if scanFlag("--macos") {
		engine = "vagrant"
	}
	if engine == "vagrant" {
		vagrantBox := scanStringFlag("--vagrant-box")
		if err := scaffold.ScaffoldVagrantfile(c.ConfigDir, vagrantBox, ""); err != nil {
			fmt.Fprintf(os.Stderr, "warning: vagrantfile scaffold failed: %v\n", err)
		}
		fmt.Fprintln(os.Stderr, "Vagrant engine is not yet implemented.")
		fmt.Fprintf(os.Stderr, "Vagrantfile scaffolded at: %s/Vagrantfile\n", c.ConfigDir)
		return nil
	}

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)

	if scanFlag("--build") && !scanFlag("--dry-run") {
		if err := buildImageWithSpinner(c.ConfigDir, true, "Building devcell image", false); err != nil {
			return err
		}
	} else if !scanFlag("--dry-run") && !runner.ImageExists(context.Background(), runner.UserImageTag()) {
		fmt.Printf(" No %s image found — building automatically\n", runner.UserImageTag())
		if err := buildImageWithSpinner(c.ConfigDir, false, "Building devcell image", false); err != nil {
			return err
		}
	}

	// Ensure network
	if err := runner.EnsureNetwork(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: network setup failed: %v\n", err)
	}

	// Remove orphaned stopped container from a previous crashed run
	if err := runner.RemoveOrphanedContainer(context.Background(), c.ContainerName); err != nil {
		return err
	}

	// Backup .claude.json (non-fatal)
	if err := backup.Backup(c.CellHome, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: backup failed: %v\n", err)
	}

	if ux.Verbose {
		fmt.Printf(" APP_NAME: %s | VNC: localhost:%s | RDP: localhost:%s | HOME: %s\n",
			c.AppName, c.VNCPort, c.RDPPort, c.CellHome)
	}

	// Pin the container to the exact image ID just built so a concurrent
	// cell build on another terminal can't swap the tag under us mid-launch.
	imageID, err := runner.LocalImageID(context.Background())
	if err != nil {
		// Non-fatal: fall back to the mutable tag.
		imageID = ""
	}

	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cellCfg,
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		Debug:        ux.Verbose,
		Image:        imageID,
	}
	argv := runner.BuildArgv(spec, runner.OsFS, exec.LookPath)

	if scanFlag("--dry-run") {
		fmt.Println(shellJoin(argv))
		return nil
	}

	// Replace process with docker (or op if prefix present)
	execBin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("binary not found %q: %w", argv[0], err)
	}
	return syscall.Exec(execBin, argv, os.Environ())
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
		if !ux.Verbose && buf.Len() > 0 {
			fmt.Fprint(os.Stderr, buf.String())
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
