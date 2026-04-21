package cloudmodels_test

// Integration tests for the full model-ranking pipeline:
//   OpenRouter models → FetchProviderModels → FilterLatestGen
//   + SWE-bench scores → FetchSWEBenchScores
//   + RankModels → top-N list
//
// These tests use real-format payloads from test/testdata/ served by in-process
// HTTP servers to catch parsing regressions without network access.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DimmKirr/devcell/internal/cloudmodels"
	"github.com/DimmKirr/devcell/internal/ollama"
)

// testdataDir returns the absolute path to test/testdata/ relative to this file.
func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	// internal/cloudmodels/ → two levels up → test/testdata/
	return filepath.Join(filepath.Dir(file), "..", "..", "test", "testdata")
}

// serveFile serves the contents of a testdata file over HTTP.
func serveFile(t *testing.T, name string) *httptest.Server {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data, err := os.ReadFile(filepath.Join(testdataDir(), name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

// TestPipeline_ProviderParsing validates that FetchProviderModels correctly
// extracts provider, name, and pricing from a real-shape OpenRouter response.
func TestPipeline_ProviderParsing(t *testing.T) {
	srv := serveFile(t, "openrouter_models.json")
	defer srv.Close()

	models, err := cloudmodels.FetchProviderModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchProviderModels failed: %v", err)
	}

	byName := make(map[string]ollama.Model)
	for _, m := range models {
		byName[m.Name] = m
	}

	// Verify provider extraction
	cases := []struct{ id, wantProvider string }{
		{"anthropic/claude-opus-4-5", "anthropic"},
		{"openai/gpt-4o", "openai"},
		{"google/gemini-2-5-pro", "google"},
		{"deepseek/deepseek-r1", "deepseek"},
	}
	for _, c := range cases {
		m, ok := byName[c.id]
		if !ok {
			t.Errorf("model %q not found in results", c.id)
			continue
		}
		if m.Provider != c.wantProvider {
			t.Errorf("%q: provider want %q got %q", c.id, c.wantProvider, m.Provider)
		}
	}

	// Verify completion price is parsed (non-zero for paid models)
	claude := byName["anthropic/claude-opus-4-5"]
	if claude.CompletionPricePerToken != 0.000075 {
		t.Errorf("claude price: want 0.000075, got %v", claude.CompletionPricePerToken)
	}
	cheap := byName["openai/gpt-4o-mini"]
	if cheap.CompletionPricePerToken != 0.0000006 {
		t.Errorf("gpt-4o-mini price: want 0.0000006, got %v", cheap.CompletionPricePerToken)
	}
}

// TestPipeline_FilterLatestGen validates that version deduplication keeps
// the correct (highest-version) model per family.
func TestPipeline_FilterLatestGen(t *testing.T) {
	srv := serveFile(t, "openrouter_models.json")
	defer srv.Close()

	raw, err := cloudmodels.FetchProviderModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchProviderModels failed: %v", err)
	}
	filtered := cloudmodels.FilterLatestGen(raw)

	byName := make(map[string]bool)
	for _, m := range filtered {
		byName[m.Name] = true
	}

	// claude-opus-4-5 should survive, claude-opus-4 should be filtered
	if !byName["anthropic/claude-opus-4-5"] {
		t.Error("expected claude-opus-4-5 to survive (higher version)")
	}
	if byName["anthropic/claude-opus-4"] {
		t.Error("expected claude-opus-4 to be filtered (older version)")
	}

	// gemini-2-5-pro should survive; gemini-2-0-flash and gemini-2-5-flash have
	// non-numeric suffixes so FilterLatestGen treats them as separate families — both survive.
	if !byName["google/gemini-2-5-pro"] {
		t.Error("expected gemini-2-5-pro to survive")
	}

	// gpt-4o and gpt-4o-mini are different families (mini suffix changes family)
	if !byName["openai/gpt-4o"] {
		t.Error("expected gpt-4o to survive")
	}
}

// TestPipeline_SWEBenchScoreParsing validates FetchSWEBenchScores parses
// the real leaderboards.json format correctly.
func TestPipeline_SWEBenchScoreParsing(t *testing.T) {
	srv := serveFile(t, "swebench_leaderboards.json")
	defer srv.Close()

	scores, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchSWEBenchScores failed: %v", err)
	}

	// Direct tag match (e.g. "Model: claude-opus-4-5")
	if s, ok := scores["claude-opus-4-5"]; !ok || s != 72.5 {
		t.Errorf("claude-opus-4-5: want 72.5, got %v (ok=%v)", s, ok)
	}
	if s, ok := scores["gpt-4o"]; !ok || s != 57.4 {
		t.Errorf("gpt-4o: want 57.4, got %v (ok=%v)", s, ok)
	}

	// HuggingFace URL tag — should be stored under repo path AND model name
	if s, ok := scores["deepseek-ai/deepseek-r1"]; !ok || s != 49.2 {
		t.Errorf("deepseek-ai/deepseek-r1: want 49.2, got %v (ok=%v)", s, ok)
	}
	if s, ok := scores["deepseek-r1"]; !ok || s != 49.2 {
		t.Errorf("deepseek-r1 (short name): want 49.2, got %v (ok=%v)", s, ok)
	}
	if s, ok := scores["qwen/qwen2.5-coder-32b-instruct"]; !ok || s != 35.0 {
		t.Errorf("qwen repo path: want 35.0, got %v (ok=%v)", s, ok)
	}
}

// TestPipeline_EndToEnd validates the full pipeline: fetch → filter → rank.
// Checks that cloud models get correct SWE scores matched from SWE-bench data,
// and that no model inherits a score from an unrelated model via loose substring matching.
func TestPipeline_EndToEnd(t *testing.T) {
	orSrv := serveFile(t, "openrouter_models.json")
	defer orSrv.Close()

	sweSrv := serveFile(t, "swebench_leaderboards.json")
	defer sweSrv.Close()

	raw, err := cloudmodels.FetchProviderModels(context.Background(), orSrv.URL)
	if err != nil {
		t.Fatalf("FetchProviderModels: %v", err)
	}
	models := cloudmodels.FilterLatestGen(raw)

	scores, err := ollama.FetchSWEBenchScores(context.Background(), sweSrv.URL)
	if err != nil {
		t.Fatalf("FetchSWEBenchScores: %v", err)
	}

	ranked := ollama.RankModels(models, len(models), scores, nil, 0, "swe")

	byName := make(map[string]ollama.RankedModel)
	for _, r := range ranked {
		byName[r.Name] = r
	}

	// claude-opus-4-5: SWE-bench tag is "claude-opus-4-5" — direct match expected
	claude := byName["anthropic/claude-opus-4-5"]
	if claude.SWEScore != 72.5 {
		t.Errorf("claude-opus-4-5 SWE score: want 72.5, got %v (source=%q)", claude.SWEScore, claude.ScoreSource)
	}
	if claude.ScoreSource != "SWE" {
		t.Errorf("claude-opus-4-5 score source: want SWE, got %q", claude.ScoreSource)
	}

	// gpt-4o: SWE-bench tag is "gpt-4o" — direct match expected
	gpt4o := byName["openai/gpt-4o"]
	if gpt4o.SWEScore != 57.4 {
		t.Errorf("gpt-4o SWE score: want 57.4, got %v", gpt4o.SWEScore)
	}

	// gpt-4o-mini: no SWE-bench entry — must NOT inherit gpt-4o's score via substring
	// "gpt-4o" is a substring of "gpt-4o-mini", loose matching would wrongly return 57.4
	mini := byName["openai/gpt-4o-mini"]
	if mini.SWEScore == 57.4 {
		t.Errorf("gpt-4o-mini wrongly inherited gpt-4o score (substring match too loose)")
	}

	// deepseek-r1-0528: FilterLatestGen keeps this over deepseek-r1 (same family, higher version).
	// Score matched via HF URL tag "deepseek-ai/DeepSeek-R1-0528" → NormalizeCloudID strategy.
	deepseek := byName["deepseek/deepseek-r1-0528"]
	if deepseek.SWEScore != 57.6 {
		t.Errorf("deepseek-r1-0528 SWE score: want 57.6, got %v (source=%q)", deepseek.SWEScore, deepseek.ScoreSource)
	}

	// Validate ranked list is sorted by SWE score descending (swe sort)
	for i := 1; i < len(ranked); i++ {
		if ranked[i].SWEScore > ranked[i-1].SWEScore {
			t.Errorf("rank %d (%s=%.1f) > rank %d (%s=%.1f): not sorted by SWE",
				i+1, ranked[i].Name, ranked[i].SWEScore,
				i, ranked[i-1].Name, ranked[i-1].SWEScore)
		}
	}
}
