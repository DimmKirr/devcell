package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-390: startup nix-store health check. Read-only by construction —
// the probe reports, only `cell build prune` / `cell cleanup` act.

// The probe must be pure filesystem inspection. nix must never be invoked
// (`nix-store --gc --print-dead` mutates: its root-finding pass deletes
// indirect roots — measured live, CELL-333). Nothing may be created,
// deleted, or retargeted.
func TestNixHealthProbeScript_ContainsNoMutatingTokens(t *testing.T) {
	forbidden := []string{
		"rm ", "rm\t", "ln -s", "mkdir", "mv ", "touch",
		"nix-store", "nix-collect-garbage", "nix ",
		"> /", ">> /",
	}
	for _, tok := range forbidden {
		if strings.Contains(runner.NixHealthProbeScript, tok) {
			t.Errorf("probe script must be read-only, found %q:\n%s", tok, runner.NixHealthProbeScript)
		}
	}
}

// auto/ roots are namespace-local (CELL-330) — the probe must not even
// evaluate them, from any namespace their targets are unanswerable.
func TestNixHealthProbeScript_IgnoresAutoRoots(t *testing.T) {
	if strings.Contains(runner.NixHealthProbeScript, "gcroots/auto") {
		t.Error("probe must not evaluate auto/ roots — targets are container-private")
	}
	if !strings.Contains(runner.NixHealthProbeScript, "gcroots/devcell") {
		t.Error("probe must inspect gcroots/devcell/ (volume-resident targets)")
	}
}

// The probe runs in the nixos/nix image whose sh has NO sed, and whose ls
// follows symlinks (a root symlink to a store dir lists the dir contents).
// Verified live 2026-08-01: `sed: command not found`, hashes silently 0.
// Every container-side script must stick to basename/cut/grep/readlink.
func TestVolumeScripts_NoSedNoLs(t *testing.T) {
	scripts := map[string]string{
		"NixHealthProbeScript":  runner.NixHealthProbeScript,
		"NixDFReportScript":     runner.NixDFReportScript,
		"SafeNixGCScript":       runner.SafeNixGCScript,
		"NixGCRootReportScript": runner.NixGCRootReportScript,
	}
	for name, s := range scripts {
		if strings.Contains(s, "sed ") || strings.Contains(s, "| sed") {
			t.Errorf("%s uses sed — not present in the nixos/nix probe image", name)
		}
		// ls as a command ($(ls …), piped, or line-start) — plain-word
		// matches like "cells" are fine.
		if strings.Contains(s, "$(ls ") || strings.Contains(s, "| ls ") ||
			strings.Contains(s, "\nls ") || strings.HasPrefix(s, "ls ") {
			t.Errorf("%s uses ls — it follows root symlinks into store dirs", name)
		}
	}
}

func TestParseNixStoreHealth_ParsesProbeOutput(t *testing.T) {
	h, err := runner.ParseNixStoreHealth("total=8 stale=3 hashes=2 generations=5 orphaned=4\n")
	if err != nil {
		t.Fatal(err)
	}
	if h.TotalRoots != 8 || h.StaleRoots != 3 || h.ProfileHashes != 2 ||
		h.Generations != 5 || h.OrphanedGenerations != 4 {
		t.Errorf("wrong parse: %+v", h)
	}
}

// Docker may prepend pull noise when the probe image isn't local — the
// parser must find the datapoint line anywhere in the output.
func TestParseNixStoreHealth_SkipsLeadingNoise(t *testing.T) {
	out := "Unable to find image locally\nlatest: Pulling...\ntotal=1 stale=0 hashes=1 generations=1 orphaned=0\n"
	h, err := runner.ParseNixStoreHealth(out)
	if err != nil {
		t.Fatal(err)
	}
	if h.TotalRoots != 1 || h.ProfileHashes != 1 {
		t.Errorf("wrong parse: %+v", h)
	}
}

func TestParseNixStoreHealth_NoDatapointLineIsError(t *testing.T) {
	if _, err := runner.ParseNixStoreHealth("garbage\n"); err == nil {
		t.Error("output without a datapoint line must error (probe degraded)")
	}
}

// Summary is the non-debug UX: one line for the "Nix store" phase row.
func TestNixStoreHealth_SummaryClean(t *testing.T) {
	h := runner.NixStoreHealth{TotalRoots: 4, ProfileHashes: 1, Generations: 2}
	s := h.Summary()
	for _, want := range []string{"clean", "4 roots", "1 profile hash"} {
		if !strings.Contains(s, want) {
			t.Errorf("clean summary missing %q, got %q", want, s)
		}
	}
}

func TestNixStoreHealth_SummaryFindingsIncludePruneHint(t *testing.T) {
	h := runner.NixStoreHealth{TotalRoots: 6, StaleRoots: 2, ProfileHashes: 3, Generations: 8, OrphanedGenerations: 5}
	s := h.Summary()
	for _, want := range []string{"2 stale root", "5 orphaned generation", "cell build prune --pure"} {
		if !strings.Contains(s, want) {
			t.Errorf("findings summary missing %q, got %q", want, s)
		}
	}
}

func TestNixStoreHealth_SummaryReportsDrift(t *testing.T) {
	h := runner.NixStoreHealth{TotalRoots: 4, ProfileHashes: 3, Generations: 2}
	if s := h.Summary(); !strings.Contains(s, "3 profile hashes") {
		t.Errorf("drift (multiple hashes) must be visible in summary, got %q", s)
	}
}

func TestNixHealthProbeArgv_RunsInContainerOnNixVolume(t *testing.T) {
	argv := runner.NixHealthProbeArgv("devcell-nix-store")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"docker", "run", "--rm", "devcell-nix-store:/nix"} {
		if !strings.Contains(joined, want) {
			t.Errorf("probe argv missing %q: %v", want, argv)
		}
	}
}

// The --debug rendering of a docker-run argv must not dump embedded shell
// scripts to the console — a multi-line `sh -c` payload made the health
// check look like it printed the script instead of running it.
func TestDebugArgv_ElidesMultilineScripts(t *testing.T) {
	argv := runner.NixHealthProbeArgv("devcell-nix-store")
	got := runner.DebugArgv(argv)
	if strings.Contains(got, "\n") {
		t.Errorf("DebugArgv must be a single line, got:\n%s", got)
	}
	if strings.Contains(got, "gcroots") {
		t.Errorf("DebugArgv must not include the script body, got:\n%s", got)
	}
	for _, want := range []string{"docker run --rm", "devcell-nix-store:/nix", "sh -c"} {
		if !strings.Contains(got, want) {
			t.Errorf("DebugArgv missing %q, got: %s", want, got)
		}
	}
	if !strings.Contains(got, "script:") {
		t.Errorf("DebugArgv should mark the elided script, got: %s", got)
	}
}

// Single-line argvs pass through untouched.
func TestDebugArgv_KeepsSingleLineArgs(t *testing.T) {
	got := runner.DebugArgv([]string{"docker", "volume", "inspect", "devcell-nix-store"})
	if got != "docker volume inspect devcell-nix-store" {
		t.Errorf("unexpected rendering: %s", got)
	}
}
