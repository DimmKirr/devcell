package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// `cell build prune` — standalone cleanup subcommand. See CELL-101.
//
// Default mode: prune the Docker daemon (containers, images, volumes, BuildKit
// cache). `--pure` switches the target to the nix store (linux-builder VM on
// macOS, local /nix/store on Linux native). `--force` escalates to a nuclear
// reset (wipe daemon data dir on macOS / Linux; nuke linux-builder qcow on
// macOS dry-run only; aggressive GC on Linux). `-y` / `--yes` skips the
// confirmation prompt.
//
// NOTE: `--pure` here means "target the nix store" (CELL-101) and is
// UNRELATED to `--pure` on agent commands (`cell claude --pure`, etc.) which
// is a silent no-op after CELL-183. Both flags coexist in the cell CLI and
// the prune meaning is preserved for now. See CELL-189 chain for the agent
// flip context.
var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune Docker daemon or nix store. --pure for nix path. --force for nuclear.",
	Long: `Prune the local Docker daemon (default) or nix store (--pure).

By default, mirrors ` + "`docker system prune`" + ` semantics — removes stopped
containers, unused images, unused volumes, and BuildKit cache.

--force escalates:
  - Docker: stops the daemon, wipes its data directory, restarts it.
            macOS:    Docker Desktop VM at ~/Library/Containers/com.docker.docker/Data
            Linux:    /var/lib/docker (rootful) or ~/.local/share/docker (rootless).
  - Nix:    macOS:    DRY-RUN ONLY — prints the qcow nuke + launchctl restart plan.
                      (Will execute once linux-builder paths are verified live.)
            Linux:    Aggressive GC ceiling — delete old generations, collect-garbage,
                      optimise, wipe ~/.cache/nix. Does NOT rm -rf /nix/store.

A "This will delete ALL …" prompt confirms every destructive op. Pass -y to skip.
Non-interactive stdin without -y refuses (prevents accidental pipelined destruction).`,
	RunE: runBuildPrune,
}

func init() {
	pruneCmd.Flags().Bool("pure", false, "target the nix store (linux-builder VM on macOS, local on Linux) instead of Docker")
	pruneCmd.Flags().Bool("force", false, "nuclear cleanup (wipe daemon data dir / nuke linux-builder VM / aggressive nix GC)")
	pruneCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	buildCmd.AddCommand(pruneCmd)
}

func runBuildPrune(cmd *cobra.Command, _ []string) error {
	pure, _ := cmd.Flags().GetBool("pure")
	force, _ := cmd.Flags().GetBool("force")
	yes, _ := cmd.Flags().GetBool("yes")

	homeDir, _ := os.UserHomeDir()
	opts := runner.PruneOpts{
		GOOS:    runtime.GOOS,
		Force:   force,
		Pure:    pure,
		HomeDir: homeDir,
		// Auto-detect rootless Docker and NixOS at runtime.
		Rootless: detectRootlessDocker(),
		NixOS:    fileExists("/etc/NIXOS"),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runner.RunPrune(runner.RunPruneArgs{
		Opts:    opts,
		Exec:    func(step runner.PruneStep) error { return execStep(ctx, step) },
		Out:     os.Stdout,
		In:      os.Stdin,
		SkipYes: yes,
		IsTTY:   isatty.IsTerminal(os.Stdin.Fd()),
	})
}

func execStep(ctx context.Context, step runner.PruneStep) error {
	if len(step.Argv) == 0 {
		return nil
	}
	c := exec.CommandContext(ctx, step.Argv[0], step.Argv[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func detectRootlessDocker() bool {
	out, err := exec.Command("docker", "info", "--format", "{{.SecurityOptions}}").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "rootless")
}

