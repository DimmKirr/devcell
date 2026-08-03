package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// `cell cleanup` — reap GC roots that no RUNNING container references.
// See CELL-334. Retention rule: "in use" = running (docker ps), not merely
// existing. Roots of stopped cells are reaped; those cells rebuild via the
// hydration gate (CELL-38) on next start.
var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Reap nix GC roots no running cell references (shared volume hygiene)",
	Long: `Reap GC root symlinks under /nix/var/nix/gcroots/devcell/ whose closure
no RUNNING devcell container references.

Only root symlinks (and their -meta files) are removed — the nix store itself
is untouched. Run ` + "`cell build prune --pure`" + ` afterwards to garbage-collect
the store paths the reaped roots were anchoring.

Roots of stopped cells are reaped too: a stopped cell rebuilds automatically
on its next start. A running cell can never lose its roots — its closure is
resolved live from inside the container before anything is removed.`,
	RunE: runCleanup,
}

func init() {
	cleanupCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	rootCmd.AddCommand(cleanupCmd)
}

func runCleanup(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	closures, err := runner.CollectLiveClosures(
		func() ([]string, error) { return runner.DockerRunningDevcellContainers(ctx) },
		func(container, link string) (string, error) {
			return runner.DockerResolveContainerLink(ctx, container, link)
		},
		ux.Debugf,
	)
	if err != nil {
		return err
	}

	return runner.RunCleanup(runner.RunCleanupArgs{
		Closures: closures,
		Exec:     func(step runner.PruneStep) error { return execStep(ctx, step) },
		Out:      os.Stdout,
		In:       os.Stdin,
		SkipYes:  yes,
		IsTTY:    isatty.IsTerminal(os.Stdin.Fd()),
	})
}
