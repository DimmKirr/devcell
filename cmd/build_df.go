package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/spf13/cobra"
)

// `cell build df` — read-only sibling of `cell build prune` (CELL-98).
//
// Shows the top-N largest images, containers, volumes, and build-cache
// entries on the local Docker daemon, marking which are pinned by running
// containers and which are reclaimable. Prints reclaim commands as hints
// — never executes them. The destructive trigger stays in `cell build
// prune`.
var dfCmd = &cobra.Command{
	Use:   "df",
	Short: "Show Docker disk usage ranked by reclaimable size",
	Long: `Read-only sibling of ` + "`cell build prune`" + `. Surfaces the top-N largest
images, containers, volumes, and build-cache entries on the local
Docker daemon, marking which are pinned by running containers and
which are reclaimable. Prints reclaim commands as hints — never
executes them.

Default mode prints a human table. --json emits a stable schema for
scripting (entries / totals / hints). --kind narrows to one or more
of: images, containers, volumes, cache.`,
	RunE: runBuildDf,
}

func init() {
	dfCmd.Flags().Int("top", 10, "number of rows to print (table mode)")
	dfCmd.Flags().Bool("json", false, "emit JSON for scripting")
	dfCmd.Flags().Bool("all", false, "print every entry, no top-N cap")
	dfCmd.Flags().StringSlice("kind", nil, "filter to one or more kinds: images, containers, volumes, cache")
	buildCmd.AddCommand(dfCmd)
}

func runBuildDf(cmd *cobra.Command, _ []string) error {
	topN, _ := cmd.Flags().GetInt("top")
	jsonOut, _ := cmd.Flags().GetBool("json")
	all, _ := cmd.Flags().GetBool("all")
	kinds, _ := cmd.Flags().GetStringSlice("kind")
	if all {
		topN = 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runner.RunDF(runner.RunDFArgs{
		Ctx:       ctx,
		Collector: runner.ExecCollector{},
		Opts: runner.DFOpts{
			TopN:  topN,
			Kinds: toEntryKinds(kinds),
			JSON:  jsonOut,
		},
		Out: os.Stdout,
	})
}

// toEntryKinds normalizes the user's --kind values. CLI help text uses the
// plural ("images, containers, volumes, cache") but internal EntryKind
// constants are singular. Strip a trailing 's' so both forms work and
// nothing silently produces zero rows.
func toEntryKinds(in []string) []runner.EntryKind {
	if len(in) == 0 {
		return nil
	}
	out := make([]runner.EntryKind, 0, len(in))
	for _, s := range in {
		s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "s")
		out = append(out, runner.EntryKind(s))
	}
	return out
}
