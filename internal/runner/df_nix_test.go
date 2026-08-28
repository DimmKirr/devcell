package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// `cell build df` nix section: default output shows nix-store state —
// counts, root NAMES, stale markers, and per-root metadata. Same safety
// contract as the CELL-390 probe: read-only, volume-mounted container,
// no nix invocation, no auto/ roots.

func TestNixDFReportScript_IsReadOnly(t *testing.T) {
	forbidden := []string{
		"rm ", "rm\t", "ln -s", "mkdir", "mv ", "touch",
		"nix-store", "nix-collect-garbage", "nix ",
		"> /", ">> /",
	}
	for _, tok := range forbidden {
		if strings.Contains(runner.NixDFReportScript, tok) {
			t.Errorf("df report script must be read-only, found %q", tok)
		}
	}
	if strings.Contains(runner.NixDFReportScript, "gcroots/auto") {
		t.Error("df report script must not evaluate auto/ roots")
	}
}

func TestParseNixStoreReport_ParsesHealthRootsAndMetas(t *testing.T) {
	out := `total=3 stale=1 hashes=2 generations=4 orphaned=2 revs=2 newest_rev=9f8e7d6 newest_projects=1
root name=dikb1y8-profile stale=0
root name=dikb1y8-generation stale=0
root name=old4321-profile stale=1
meta hash=dikb1y8 project=devcell stack=ultimate nixpkgs=9f8e7d6
`
	r, err := runner.ParseNixStoreReport("devcell-nix-store", out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Volume != "devcell-nix-store" {
		t.Errorf("volume not carried: %+v", r)
	}
	if r.Health.TotalRoots != 3 || r.Health.OrphanedGenerations != 2 {
		t.Errorf("health not parsed: %+v", r.Health)
	}
	if len(r.Roots) != 3 {
		t.Fatalf("want 3 roots, got %d: %+v", len(r.Roots), r.Roots)
	}
	if r.Roots[0].Name != "dikb1y8-profile" || r.Roots[0].Stale {
		t.Errorf("root[0] wrong: %+v", r.Roots[0])
	}
	if r.Roots[2].Name != "old4321-profile" || !r.Roots[2].Stale {
		t.Errorf("stale root not flagged: %+v", r.Roots[2])
	}
	if len(r.Metas) != 1 || r.Metas[0].Project != "devcell" ||
		r.Metas[0].Stack != "ultimate" || r.Metas[0].Nixpkgs != "9f8e7d6" {
		t.Errorf("meta wrong: %+v", r.Metas)
	}
}

func TestFormatNixStoreSection_ShowsCountsNamesMetaAndHints(t *testing.T) {
	r := runner.NixStoreReport{
		Volume: "devcell-nix-store",
		Health: runner.NixStoreHealth{
			TotalRoots: 3, StaleRoots: 1, ProfileHashes: 2,
			Generations: 4, OrphanedGenerations: 2,
		},
		Roots: []runner.NixRootEntry{
			{Name: "dikb1y8-profile"},
			{Name: "dikb1y8-generation"},
			{Name: "old4321-profile", Stale: true},
		},
		Metas: []runner.NixRootMeta{
			{Hash: "dikb1y8", Project: "devcell", Stack: "ultimate", Nixpkgs: "9f8e7d6abcdef"},
		},
	}
	var b strings.Builder
	runner.FormatNixStoreSection(r, &b)
	out := b.String()

	for _, want := range []string{
		"Nix store (devcell-nix-store)",
		"3 roots", "2 profile hashes", "4 generations", "2 orphaned",
		"dikb1y8-profile", "dikb1y8-generation",
		"old4321-profile", "(stale)",
		"project=devcell", "stack=ultimate", "nixpkgs=9f8e7d6",
		"cell cleanup", "cell build prune --pure",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nix section missing %q\noutput:\n%s", want, out)
		}
	}
}

// A root whose hash has a -meta file gets the metadata inline on its
// -profile row; unmetad roots render bare.
func TestFormatNixStoreSection_MetaJoinedByHash(t *testing.T) {
	r := runner.NixStoreReport{
		Volume: "v",
		Health: runner.NixStoreHealth{TotalRoots: 2, ProfileHashes: 2},
		Roots: []runner.NixRootEntry{
			{Name: "aaa1111-profile"},
			{Name: "bbb2222-profile"},
		},
		Metas: []runner.NixRootMeta{{Hash: "aaa1111", Project: "trips"}},
	}
	var b strings.Builder
	runner.FormatNixStoreSection(r, &b)
	out := b.String()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "aaa1111-profile") && !strings.Contains(line, "project=trips") {
			t.Errorf("meta must be joined onto its root's row: %q", line)
		}
		if strings.Contains(line, "bbb2222-profile") && strings.Contains(line, "project=") {
			t.Errorf("unmetad root must render bare: %q", line)
		}
	}
}

// Probe failure → empty section, df must not break.
func TestFormatNixStoreSection_EmptyReportRendersNothing(t *testing.T) {
	var b strings.Builder
	runner.FormatNixStoreSection(runner.NixStoreReport{}, &b)
	if b.Len() != 0 {
		t.Errorf("empty report must render nothing, got %q", b.String())
	}
}
