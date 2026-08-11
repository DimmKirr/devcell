package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/mattn/go-isatty"
)

// CELL-390: "Nix store" preflight phase — read-only health report on the
// shared nix volume at every cell start. Non-fatal by design: any probe
// failure degrades to a "skipped" detail, never blocks boot. Mutation
// happens only behind the explicit --auto-cleanup opt-in, which runs the
// CELL-334 reaper (running-only retention).
//
// Every datapoint and decision is mirrored to ux.Debugf so `--debug` shows
// the full check: volume, argv, raw probe output, parsed counts, cleanup
// closures, and reap results.
func nixStorePhase(ctx context.Context, pr *ux.PhaseRunner, thin bool, baseDir string, staleWarn bool) error {
	if !thin {
		ux.Debugf("nix health: skipped (not thin mode — no store volume)")
		return nil
	}
	if v := os.Getenv("DEVCELL_NIX_HEALTH_CHECK"); v == "false" || v == "0" {
		ux.Debugf("nix health: skipped (DEVCELL_NIX_HEALTH_CHECK=%s)", v)
		return nil
	}

	var health runner.NixStoreHealth
	probed := false
	_ = pr.PhaseDetailed("Nix store", func() (string, error) {
		volume := runner.ThinStoreVolume()
		argv := runner.NixHealthProbeArgv(volume)
		ux.Debugf("nix health: probing volume=%s", volume)
		ux.Debugf("nix health: argv=%s", runner.DebugArgv(argv))

		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		ux.Debugf("nix health: raw output=%q err=%v", strings.TrimSpace(string(out)), err)
		if err != nil {
			// Docker hiccup, missing volume, etc. — report, don't block.
			return "skipped (probe unavailable)", nil
		}
		h, perr := runner.ParseNixStoreHealth(string(out))
		if perr != nil {
			ux.Debugf("nix health: parse failed: %v", perr)
			return "skipped (unreadable probe output)", nil
		}
		ux.Debugf("nix health: parsed total=%d stale=%d hashes=%d generations=%d orphaned=%d revs=%d newest_rev=%s newest_projects=%d",
			h.TotalRoots, h.StaleRoots, h.ProfileHashes, h.Generations, h.OrphanedGenerations,
			h.DistinctRevs, h.NewestRev, h.NewestProjects)
		health, probed = h, true

		detail := h.Summary()
		if scanFlag("--auto-cleanup") {
			detail += "; " + autoCleanupDetail(ctx)
		}
		return detail, nil
	})

	return staleCellNudge(baseDir, health, probed, staleWarn)
}

// staleCellNudge implements CELL-391: if this project's nixpkgs rev is
// behind the newest rev live on the volume, warn that starting keeps a
// parallel closure alive and offer to abort (default: proceed). Inform,
// not enforce — every failure path is silence, and non-TTY never blocks.
func staleCellNudge(baseDir string, h runner.NixStoreHealth, probed, staleWarn bool) error {
	if !probed {
		return nil
	}
	if !staleWarn {
		ux.Debugf("stale check: skipped ([cell] stale_warning = false)")
		return nil
	}
	if v := os.Getenv("DEVCELL_STALE_WARN"); v == "false" || v == "0" {
		ux.Debugf("stale check: skipped (DEVCELL_STALE_WARN=%s)", v)
		return nil
	}
	projectRev, _ := runner.ProjectNixpkgsRev(baseDir)
	ux.Debugf("stale check: project rev=%q volume newest=%q distinct revs=%d",
		projectRev, h.NewestRev, h.DistinctRevs)
	warning, stale := runner.StaleCellWarning(projectRev, h)
	if !stale {
		ux.Debugf("stale check: cell is current (or revs unknown) — no nudge")
		return nil
	}
	isTTY := isatty.IsTerminal(os.Stdin.Fd())
	ux.Debugf("stale check: nudging user (tty=%v, default=proceed)", isTTY)
	if runner.ConfirmProceed(os.Stdout, os.Stdin, isTTY, warning) {
		ux.Debugf("stale check: user chose to proceed with the stale closure")
		return nil
	}
	ux.Debugf("stale check: user aborted launch")
	return fmt.Errorf("launch aborted — update this cell with: cell build --update")
}

// CELL-418: detect whether the thin image's baked-in nix closure still
// exists on the shared nix-store volume. A dead closure means the image
// will boot silently broken — no nix-daemon, no shell setup, no tools.
// Prompts for rebuild on TTY; auto-rebuilds on non-TTY.
func closureCheckPhase(ctx context.Context, pr *ux.PhaseRunner, thin bool, imageTag string, rebuild func() error) error {
	if !thin {
		ux.Debugf("closure check: skipped (not thin mode)")
		return nil
	}
	volume := runner.ThinStoreVolume()
	ux.Debugf("closure check: volume=%s image=%s", volume, imageTag)

	argv := runner.ClosureAliveArgv(volume, imageTag)
	ux.Debugf("closure check: argv=%s", runner.DebugArgv(argv))

	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	resolved, alive := runner.ParseClosureAliveResult(string(out), err)
	ux.Debugf("closure check: alive=%v resolved=%q err=%v", alive, resolved, err)

	warning, dead := runner.ClosureDeadWarning(alive)
	if !dead {
		ux.Debugf("closure check: closure alive at %s", resolved)
		return nil
	}

	isTTY := isatty.IsTerminal(os.Stdin.Fd())
	ux.Debugf("closure check: dead closure detected (tty=%v)", isTTY)

	if !runner.ConfirmProceed(os.Stdout, os.Stdin, isTTY, warning) {
		return fmt.Errorf("launch aborted — rebuild with: cell build")
	}

	return rebuild()
}

// autoCleanupDetail runs the CELL-334 reaper without prompting — the
// --auto-cleanup flag IS the consent. Failures degrade to a detail string;
// a cell start must never break because hygiene did.
func autoCleanupDetail(ctx context.Context) string {
	closures, err := runner.CollectLiveClosures(
		func() ([]string, error) { return runner.DockerRunningDevcellContainers(ctx) },
		func(container, link string) (string, error) {
			return runner.DockerResolveContainerLink(ctx, container, link)
		},
		ux.Debugf,
	)
	if err != nil {
		ux.Debugf("auto-cleanup: refused: %v", err)
		return "auto-cleanup skipped (a running cell's closure is unresolvable)"
	}
	for _, step := range runner.BuildCleanupSteps(closures) {
		ux.Debugf("auto-cleanup: exec %s", strings.Join(step.Argv[:6], " ")+" …")
		out, runErr := exec.CommandContext(ctx, step.Argv[0], step.Argv[1:]...).CombinedOutput()
		ux.Debugf("auto-cleanup: output=%q err=%v", strings.TrimSpace(string(out)), runErr)
		if runErr != nil {
			return "auto-cleanup failed (see --debug)"
		}
		// Surface the reaper's own one-line summary as the non-debug UX.
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Cleanup:") {
				return strings.ToLower(strings.TrimPrefix(line, "Cleanup: "))
			}
		}
	}
	return "auto-cleanup ran"
}
