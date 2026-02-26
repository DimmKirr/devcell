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
	rootCmd.PersistentFlags().Bool("build", false, "rebuild image before running")
	rootCmd.PersistentFlags().Bool("dry-run", false, "print docker run argv and exit without running")
	rootCmd.PersistentFlags().Bool("plain-text", false, "disable spinners, use plain log output (for CI/non-TTY)")
	rootCmd.PersistentFlags().Bool("debug", false, "plain-text mode plus stream full build log to stdout")
	rootCmd.AddCommand(
		claudeCmd,
		codexCmd,
		opencodeCmd,
		shellCmd,
		buildCmd,
		initCmd,
		vncCmd,
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
	for _, arg := range os.Args {
		switch arg {
		case "--plain-text":
			ux.LogPlainText = true
		case "--debug":
			ux.LogPlainText = true
			ux.Verbose = true
		}
	}
}

// cellFlags are flags consumed by devcell itself and must not be forwarded
// to the inner binary. DisableFlagParsing on subcommands causes cobra to
// leak persistent flags into args.
var cellFlags = map[string]bool{
	"--debug":      true,
	"--plain-text": true,
	"--dry-run":    true,
	"--build":      true,
}

// stripCellFlags removes devcell-specific flags from args so they are not
// forwarded to the inner binary.
func stripCellFlags(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if !cellFlags[a] {
			filtered = append(filtered, a)
		}
	}
	return filtered
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

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)

	// Build image if --build flag passed or image doesn't exist
	forceBuild := scanFlag("--build")
	if forceBuild {
		if err := buildImageWithSpinner(c.ConfigDir, false, "Building devcell image", false); err != nil {
			return err
		}
	} else if !runner.ImageExists(context.Background(), "devcell-local") {
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
		fmt.Printf(" APP_NAME: %s | VNC: localhost:%s | HOME: %s\n",
			c.AppName, c.VNCPort, c.CellHome)
	}

	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cellCfg,
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		Debug:        ux.Verbose,
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

// scanFlag checks os.Args for a flag (needed because DisableFlagParsing
// prevents cobra from parsing persistent flags on agent subcommands).
func scanFlag(flag string) bool {
	for _, arg := range os.Args {
		if arg == flag {
			return true
		}
	}
	return false
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
