package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

type fakeCollector struct {
	raw []byte
	err error
}

func (f fakeCollector) CollectSystemDF(context.Context) ([]byte, error) {
	return f.raw, f.err
}

// `docker system df -v --format json` returns a single object with four
// arrays — Images, Containers, Volumes, BuildCache. ParseSystemDF must
// round-trip the fixture so downstream ranking has all four populated and
// every image carries a non-zero SizeBytes after the size-string parse.
func TestParseSystemDF_LiveFixture_PopulatesAllFourCategories(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "df", "system_df_v.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.ParseSystemDF(raw)
	if err != nil {
		t.Fatalf("ParseSystemDF: %v", err)
	}
	if len(got.Images) == 0 {
		t.Error("want images, got none")
	}
	if len(got.Containers) == 0 {
		t.Error("want containers, got none")
	}
	if len(got.Volumes) == 0 {
		t.Error("want volumes, got none")
	}
	if len(got.BuildCache) == 0 {
		t.Error("want build cache, got none")
	}
	for i, img := range got.Images {
		if img.ID == "" {
			t.Errorf("image[%d]: missing ID", i)
		}
		if img.SizeBytes <= 0 {
			t.Errorf("image[%d] %s: SizeBytes=%d, want >0", i, img.ID, img.SizeBytes)
		}
	}
}

// Docker's CLI size format is inconsistent (GB not GiB, no space, lowercase
// kB, N/A for unknown). Pin behavior here so the rest of the parser doesn't
// have to rediscover it. Numbers come from real `docker system df` output.
func TestParseSize_DockerFormats(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"29.8GB", 29_800_000_000},
		{"4.063GB", 4_063_000_000},
		{"908.2MB", 908_200_000},
		{"813.6kB", 813_600},
		{"19.71kB", 19_710},
		{"6.9MB", 6_900_000},
		{"0B", 0},
		{"N/A", -1},
		{"", -1},
		{"1GB", 1_000_000_000},
		{"512B", 512},
	}
	for _, c := range cases {
		got, err := runner.ParseSize(c.in)
		if err != nil {
			t.Errorf("ParseSize(%q): err=%v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseSize_RejectsUnknownUnit(t *testing.T) {
	_, err := runner.ParseSize("12 zebibytes")
	if err == nil {
		t.Error("expected error for unknown unit")
	}
}

// RankEntries unifies images / volumes / cache into one list sorted by
// "bytes you'd actually reclaim if you removed this." For images that's
// UniqueBytes (shared layers don't free until every sibling is gone); for
// volumes and cache it's SizeBytes (no shared concept). Pinned items stay
// in the ranking but are flagged so the formatter can suppress the
// reclaim hint.
func TestRankEntries_SortsByReclaimableBytesDesc(t *testing.T) {
	snap := runner.DFSnapshot{
		Images: []runner.DFImage{
			{ID: "sha256:img-pinned", Containers: 3, SizeBytes: 29_800_000_000, UniqueBytes: 4_000_000_000, Repository: "devcell-user", Tag: "ultimate-pure"},
			{ID: "sha256:img-orphan", Containers: 0, SizeBytes: 29_800_000_000, UniqueBytes: 3_900_000_000, Repository: "<none>"},
			{ID: "sha256:img-fully-shared", Containers: 0, SizeBytes: 29_800_000_000, UniqueBytes: 0, Repository: "<none>"},
		},
		Volumes: []runner.DFVolume{
			{Name: "buildx_buildkit_multiarch00_state", Links: 1, SizeBytes: 4_400_000_000},
		},
		BuildCache: []runner.DFCache{
			{ID: "kjijd91atha3", SizeBytes: 908_200_000, Description: "nix profile install"},
		},
	}

	got := runner.RankEntries(snap, 10)
	if len(got) != 5 {
		t.Fatalf("want 5 entries, got %d", len(got))
	}

	// Expected order by reclaimable bytes desc:
	//   volume 4.4GB > img-pinned 4.0GB > img-orphan 3.9GB > cache 908MB > img-fully-shared 0
	wantOrder := []string{
		"buildx_buildkit_multiarch00_state",
		"sha256:img-pinned",
		"sha256:img-orphan",
		"kjijd91atha3",
		"sha256:img-fully-shared",
	}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("rank[%d]: got %q, want %q\nfull: %+v", i, got[i].ID, want, got)
		}
	}

	// Pinned image must carry PinCount > 0.
	for _, e := range got {
		if e.ID == "sha256:img-pinned" && e.PinCount != 3 {
			t.Errorf("img-pinned PinCount=%d, want 3", e.PinCount)
		}
		if e.ID == "sha256:img-orphan" && e.PinCount != 0 {
			t.Errorf("img-orphan PinCount=%d, want 0", e.PinCount)
		}
	}
}

func TestRankEntries_TopNCapsTheSlice(t *testing.T) {
	images := make([]runner.DFImage, 50)
	for i := range images {
		images[i] = runner.DFImage{ID: fmt.Sprintf("sha256:img-%02d", i), UniqueBytes: int64(50-i) * 1_000_000}
	}
	got := runner.RankEntries(runner.DFSnapshot{Images: images}, 10)
	if len(got) != 10 {
		t.Errorf("len(top10) = %d, want 10", len(got))
	}
	// First entry should be the biggest (img-00, 50MB).
	if got[0].ID != "sha256:img-00" {
		t.Errorf("first = %q, want sha256:img-00", got[0].ID)
	}
}

// `--kind cache --top 5` must return top 5 *cache* entries, not "filter
// the top 5 of everything down to cache" (which produces zero rows when
// the top 5 globally are all images, as on any healthy dev box).
// Regression test from smoke run on dimm-585 (2026-05-23).
func TestFormatTable_KindFilterAppliesBeforeTopN(t *testing.T) {
	snap := runner.DFSnapshot{
		Images: []runner.DFImage{
			{ID: "sha256:big1", SizeBytes: 30_000_000_000, UniqueBytes: 4_000_000_000, Repository: "<none>"},
			{ID: "sha256:big2", SizeBytes: 30_000_000_000, UniqueBytes: 4_000_000_000, Repository: "<none>"},
			{ID: "sha256:big3", SizeBytes: 30_000_000_000, UniqueBytes: 4_000_000_000, Repository: "<none>"},
			{ID: "sha256:big4", SizeBytes: 30_000_000_000, UniqueBytes: 4_000_000_000, Repository: "<none>"},
			{ID: "sha256:big5", SizeBytes: 30_000_000_000, UniqueBytes: 4_000_000_000, Repository: "<none>"},
		},
		BuildCache: []runner.DFCache{
			{ID: "smallcache", SizeBytes: 100_000_000, Description: "small"},
		},
	}
	var buf bytes.Buffer
	err := runner.FormatTable(snap, runner.FormatOpts{
		TopN:  5,
		Kinds: []runner.EntryKind{runner.EntryKindCache},
	}, &buf)
	if err != nil {
		t.Fatalf("FormatTable: %v", err)
	}
	if !strings.Contains(buf.String(), "smallcache") {
		t.Errorf("expected 'smallcache' row when filtering to cache; got:\n%s", buf.String())
	}
}

// Totals must reflect actual on-disk bytes, not the inflated "sum of
// each image's full Size" which double-counts shared layers. Docker
// itself reports the deduped value in its summary; we must match it.
// Smoke-test on a real dev box showed 341 GB instead of the truthful
// ~66 GB before this regression test landed.
func TestComputeTotals_ImagesUsesUniqueBytesNotSize(t *testing.T) {
	snap := runner.DFSnapshot{
		Images: []runner.DFImage{
			// Two siblings sharing 25 GB of layers; only the unique 4 GB
			// each represents actual on-disk bytes for that image.
			{ID: "sha256:a", SizeBytes: 29_000_000_000, UniqueBytes: 4_000_000_000, SharedBytes: 25_000_000_000},
			{ID: "sha256:b", SizeBytes: 29_000_000_000, UniqueBytes: 4_000_000_000, SharedBytes: 25_000_000_000},
		},
	}
	var buf bytes.Buffer
	if err := runner.FormatJSON(snap, runner.FormatOpts{}, &buf); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Totals struct {
			ImagesBytes int64 `json:"imagesBytes"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// True on-disk total = sum of UniqueBytes (4 + 4 = 8 GB).
	// The naive sum-of-Size would be 58 GB and is wrong.
	want := int64(8_000_000_000)
	if got.Totals.ImagesBytes != want {
		t.Errorf("totals.imagesBytes = %d (%.1f GB), want %d (%.1f GB)",
			got.Totals.ImagesBytes, float64(got.Totals.ImagesBytes)/1e9,
			want, float64(want)/1e9)
	}
}

// RunDF orchestrates collector → parser → ranker → formatter. The fake
// collector returns the captured live fixture, and the orchestrator must
// produce a formatted table containing the totals line.
func TestRunDF_PipesCollectorThroughTableFormatter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "df", "system_df_v.json"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = runner.RunDF(runner.RunDFArgs{
		Ctx:       context.Background(),
		Collector: fakeCollector{raw: raw},
		Opts:      runner.DFOpts{TopN: 5},
		Out:       &buf,
	})
	if err != nil {
		t.Fatalf("RunDF: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Totals:") {
		t.Errorf("expected Totals: line in output:\n%s", out)
	}
	if !strings.Contains(out, "TYPE") {
		t.Errorf("expected table header in output:\n%s", out)
	}
}

func TestRunDF_JSONMode_EmitsValidJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "df", "system_df_v.json"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = runner.RunDF(runner.RunDFArgs{
		Ctx:       context.Background(),
		Collector: fakeCollector{raw: raw},
		Opts:      runner.DFOpts{TopN: 5, JSON: true},
		Out:       &buf,
	})
	if err != nil {
		t.Fatalf("RunDF: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(buf.Bytes(), &probe); err != nil {
		t.Fatalf("RunDF --json produced invalid JSON: %v\n%s", err, buf.String())
	}
	for _, key := range []string{"entries", "totals", "hints"} {
		if _, ok := probe[key]; !ok {
			t.Errorf("output missing key %q", key)
		}
	}
}

// FormatJSON must emit a stable schema (entries, totals, hints) with
// camelCase keys, so downstream `jq` scripts can pin against it.
func TestFormatJSON_StableSchema(t *testing.T) {
	snap := runner.DFSnapshot{
		Images: []runner.DFImage{
			{ID: "sha256:img1", Containers: 0, SizeBytes: 1_000_000, UniqueBytes: 1_000_000, Repository: "<none>"},
		},
		BuildCache: []runner.DFCache{
			{ID: "cache1", SizeBytes: 500_000, Description: "go mod download"},
		},
	}
	var buf bytes.Buffer
	if err := runner.FormatJSON(snap, runner.FormatOpts{TopN: 10}, &buf); err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	var got struct {
		Entries []struct {
			Kind         string `json:"kind"`
			ID           string `json:"id"`
			SizeBytes    int64  `json:"sizeBytes"`
			ReclaimBytes int64  `json:"reclaimBytes"`
			PinCount     int    `json:"pinCount"`
		} `json:"entries"`
		Totals struct {
			ImagesBytes     int64 `json:"imagesBytes"`
			CacheBytes      int64 `json:"cacheBytes"`
			VolumesBytes    int64 `json:"volumesBytes"`
			ContainersBytes int64 `json:"containersBytes"`
		} `json:"totals"`
		Hints []string `json:"hints"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("FormatJSON output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got.Entries) != 2 {
		t.Errorf("entries: got %d, want 2", len(got.Entries))
	}
	if got.Entries[0].ID != "sha256:img1" {
		t.Errorf("entries[0].id = %q, want sha256:img1", got.Entries[0].ID)
	}
	if got.Totals.ImagesBytes != 1_000_000 {
		t.Errorf("totals.imagesBytes = %d, want 1000000", got.Totals.ImagesBytes)
	}
	if got.Totals.CacheBytes != 500_000 {
		t.Errorf("totals.cacheBytes = %d, want 500000", got.Totals.CacheBytes)
	}
}

// FormatTable must (a) print the header, (b) mark pinned items with a
// pin marker that exposes the container count, (c) print the exact
// reclaim command next to orphans, (d) print a Totals: footer.
func TestFormatTable_HighlightsPinnedAndPrintsHints(t *testing.T) {
	snap := runner.DFSnapshot{
		Images: []runner.DFImage{
			{ID: "sha256:2569df2ab030deadbeef", Containers: 3, SizeBytes: 29_800_000_000, UniqueBytes: 4_063_000_000, Repository: "devcell-user", Tag: "ultimate-pure"},
			{ID: "sha256:aded93bf10dcdeadbeef", Containers: 0, SizeBytes: 29_800_000_000, UniqueBytes: 3_869_000_000, Repository: "<none>", CreatedSince: "43 hours ago"},
		},
		Volumes: []runner.DFVolume{
			{Name: "buildx_buildkit_multiarch00_state", Links: 1, SizeBytes: 4_398_000_000},
		},
		BuildCache: []runner.DFCache{
			{ID: "kjijd91atha3", SizeBytes: 908_200_000, Description: "nix profile install"},
		},
	}
	var buf bytes.Buffer
	if err := runner.FormatTable(snap, runner.FormatOpts{TopN: 5}, &buf); err != nil {
		t.Fatalf("FormatTable: %v", err)
	}
	out := buf.String()

	wantSubstrings := []string{
		"TYPE", "SIZE", "RECLAIM", "PINNED",
		"devcell-user:ultimate-pure",
		"✓ (3)",                        // pinned marker with count
		"docker image rm aded93bf10dc", // orphan reclaim hint, short id form
		"docker buildx prune",          // cache/volume hint
		"Totals:",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}
