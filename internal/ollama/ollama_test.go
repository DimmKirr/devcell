package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ollamaapi "github.com/ollama/ollama/api"

	"github.com/DimmKirr/devcell/internal/ollama"
)

// --- RankModels tests ---

func TestRankModels_SortsBySWEScore(t *testing.T) {
	models := []ollama.Model{
		{Name: "qwen3:8b", ParameterSize: "8B"},
		{Name: "deepseek-r1:70b", ParameterSize: "70B"},
		{Name: "deepseek-r1:32b", ParameterSize: "32B"},
	}

	ranked := ollama.RankModels(models, 10, nil, nil)

	if len(ranked) != 3 {
		t.Fatalf("expected 3 models, got %d", len(ranked))
	}
	// deepseek-r1:70b should be first (highest SWE score)
	if ranked[0].Name != "deepseek-r1:70b" {
		t.Errorf("expected first model deepseek-r1:70b, got %s", ranked[0].Name)
	}
	if ranked[0].SWEScore <= ranked[1].SWEScore {
		t.Errorf("expected first score > second: %.1f vs %.1f", ranked[0].SWEScore, ranked[1].SWEScore)
	}
}

func TestRankModels_LimitsToTopN(t *testing.T) {
	models := []ollama.Model{
		{Name: "deepseek-r1:70b"},
		{Name: "deepseek-r1:32b"},
		{Name: "qwen3:32b"},
		{Name: "qwen3:8b"},
	}

	ranked := ollama.RankModels(models, 2, nil, nil)

	if len(ranked) != 2 {
		t.Fatalf("expected 2 models, got %d", len(ranked))
	}
}

func TestRankModels_UnknownModelsGetZeroScore(t *testing.T) {
	models := []ollama.Model{
		{Name: "unknown-model:latest"},
		{Name: "deepseek-r1:32b"},
	}

	ranked := ollama.RankModels(models, 10, nil, nil)

	if len(ranked) != 2 {
		t.Fatalf("expected 2 models, got %d", len(ranked))
	}
	// Known model should be first
	if ranked[0].Name != "deepseek-r1:32b" {
		t.Errorf("expected known model first, got %s", ranked[0].Name)
	}
	// Unknown model should have score 0
	if ranked[1].SWEScore != 0 {
		t.Errorf("expected unknown model score 0, got %.1f", ranked[1].SWEScore)
	}
}

func TestRankModels_Empty(t *testing.T) {
	ranked := ollama.RankModels(nil, 10, nil, nil)
	if len(ranked) != 0 {
		t.Errorf("expected empty result, got %d", len(ranked))
	}
}

func TestRankModels_RankNumbersAreSequential(t *testing.T) {
	models := []ollama.Model{
		{Name: "qwen3:8b"},
		{Name: "deepseek-r1:70b"},
		{Name: "deepseek-r1:32b"},
	}

	ranked := ollama.RankModels(models, 10, nil, nil)

	for i, r := range ranked {
		if r.Rank != i+1 {
			t.Errorf("expected rank %d, got %d for %s", i+1, r.Rank, r.Name)
		}
	}
}

func TestRankModels_UsesLiveSWEScores(t *testing.T) {
	models := []ollama.Model{
		{Name: "deepseek-r1:32b"},
		{Name: "qwen3:8b"},
		{Name: "unknown:latest"},
	}

	liveScores := map[string]float64{
		"deepseek-r1": 49.2,
		"qwen3":       28.0,
	}

	ranked := ollama.RankModels(models, 10, liveScores, nil)

	if ranked[0].Name != "deepseek-r1:32b" || ranked[0].SWEScore != 49.2 {
		t.Errorf("expected deepseek-r1:32b with 49.2, got %s with %.1f", ranked[0].Name, ranked[0].SWEScore)
	}
	if ranked[1].Name != "qwen3:8b" || ranked[1].SWEScore != 28.0 {
		t.Errorf("expected qwen3:8b with 28.0, got %s with %.1f", ranked[1].Name, ranked[1].SWEScore)
	}
	// unknown model falls back to hardcoded (0 if not in fallback)
	if ranked[2].SWEScore != 0 {
		t.Errorf("expected unknown model score 0, got %.1f", ranked[2].SWEScore)
	}
}

func TestRankModels_LiveScoresOverrideFallback(t *testing.T) {
	models := []ollama.Model{
		{Name: "deepseek-r1:32b"},
	}

	// Live score is different from hardcoded fallback
	liveScores := map[string]float64{
		"deepseek-r1": 99.9,
	}

	ranked := ollama.RankModels(models, 10, liveScores, nil)

	if ranked[0].SWEScore != 99.9 {
		t.Errorf("expected live score 99.9 to override fallback, got %.1f", ranked[0].SWEScore)
	}
}

func TestRankModels_ScoreSourceSWE(t *testing.T) {
	models := []ollama.Model{
		{Name: "deepseek-r1:32b"},
	}
	liveScores := map[string]float64{
		"deepseek-r1": 49.2,
	}

	ranked := ollama.RankModels(models, 10, liveScores, nil)

	if ranked[0].ScoreSource != "SWE" {
		t.Errorf("expected ScoreSource=SWE, got %q", ranked[0].ScoreSource)
	}
}

func TestRankModels_ScoreSourceEst(t *testing.T) {
	models := []ollama.Model{
		{Name: "deepseek-r1:32b"},
	}

	ranked := ollama.RankModels(models, 10, nil, nil)

	if ranked[0].ScoreSource != "est" {
		t.Errorf("expected ScoreSource=est, got %q", ranked[0].ScoreSource)
	}
}

func TestRankModels_ScoreSourceEmpty_WhenNoScore(t *testing.T) {
	models := []ollama.Model{
		{Name: "totally-unknown:latest"},
	}

	ranked := ollama.RankModels(models, 10, nil, nil)

	if ranked[0].ScoreSource != "" {
		t.Errorf("expected empty ScoreSource for unknown model, got %q", ranked[0].ScoreSource)
	}
}

func TestRankModels_UsesHFRepoIDForSWEMatch(t *testing.T) {
	models := []ollama.Model{
		{Name: "qwen2.5-coder:32b"},
	}

	// SWE-bench scores keyed by HF repo path (as extracted from HF URL tags)
	sweScores := map[string]float64{
		"qwen/qwen2.5-coder-32b-instruct": 35.0,
	}

	// HF info maps family → repo ID
	hfInfoMap := map[string]ollama.HFModelInfo{
		"qwen2.5-coder": {ModelID: "Qwen/Qwen2.5-Coder-32B-Instruct"},
	}

	ranked := ollama.RankModels(models, 10, sweScores, hfInfoMap)

	if ranked[0].SWEScore != 35.0 {
		t.Errorf("expected SWE score 35.0 via HF repo ID, got %.1f", ranked[0].SWEScore)
	}
	if ranked[0].ScoreSource != "SWE" {
		t.Errorf("expected ScoreSource=SWE, got %q", ranked[0].ScoreSource)
	}
}

// --- Detect tests ---

func TestDetect_ReturnsTrue_WhenOllamaReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok := ollama.Detect(context.Background(), srv.URL)
	if !ok {
		t.Error("expected Detect to return true for reachable server")
	}
}

func TestDetect_ReturnsFalse_WhenUnreachable(t *testing.T) {
	ok := ollama.Detect(context.Background(), "http://127.0.0.1:0")
	if ok {
		t.Error("expected Detect to return false for unreachable server")
	}
}

// --- FetchModels tests ---

func TestFetchModels_ParsesOllamaResponse(t *testing.T) {
	resp := ollamaapi.ListResponse{
		Models: []ollamaapi.ListModelResponse{
			{
				Name: "deepseek-r1:32b",
				Size: 32_000_000_000,
				Details: ollamaapi.ModelDetails{
					ParameterSize: "32B",
					Family:        "deepseek",
				},
			},
			{
				Name: "qwen3:8b",
				Size: 8_000_000_000,
				Details: ollamaapi.ModelDetails{
					ParameterSize: "8B",
					Family:        "qwen3",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	models, err := ollama.FetchModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "deepseek-r1:32b" {
		t.Errorf("expected first model deepseek-r1:32b, got %s", models[0].Name)
	}
	if models[0].ParameterSize != "32B" {
		t.Errorf("expected parameter size 32B, got %s", models[0].ParameterSize)
	}
}

func TestFetchModels_ReturnsError_WhenUnreachable(t *testing.T) {
	_, err := ollama.FetchModels(context.Background(), "http://127.0.0.1:0")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// --- FormatTOMLSnippet tests ---

func TestFormatTOMLSnippet_ProducesCommentedConfig(t *testing.T) {
	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "deepseek-r1:70b"}, SWEScore: 43.8, Rank: 1},
		{Model: ollama.Model{Name: "qwen3:32b"}, SWEScore: 38.2, Rank: 2},
	}

	snippet := ollama.FormatTOMLSnippet(ranked)

	// Should contain commented-out TOML
	if len(snippet) == 0 {
		t.Fatal("expected non-empty snippet")
	}
	// Should have the default model
	if !contains(snippet, "ollama/deepseek-r1:70b") {
		t.Error("expected default model in snippet")
	}
	// Should list both models
	if !contains(snippet, "deepseek-r1:70b") || !contains(snippet, "qwen3:32b") {
		t.Error("expected both models in snippet")
	}
	// Should be commented out
	if snippet[0] != '#' {
		t.Error("expected snippet to start with comment")
	}
}

func TestFormatActiveTOMLSnippet_ProducesUncommentedConfig(t *testing.T) {
	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "deepseek-r1:70b"}, SWEScore: 43.8, Rank: 1},
		{Model: ollama.Model{Name: "qwen3:32b"}, SWEScore: 38.2, Rank: 2},
	}

	snippet := ollama.FormatActiveTOMLSnippet(ranked)

	if len(snippet) == 0 {
		t.Fatal("expected non-empty snippet")
	}
	// Should start with active TOML (no comment prefix)
	if snippet[0] == '#' {
		t.Error("expected snippet to NOT start with comment")
	}
	// Should have the default model set to #1 ranked
	if !contains(snippet, `default = "ollama/deepseek-r1:70b"`) {
		t.Error("expected default model to be #1 ranked")
	}
	// Should list both models
	if !contains(snippet, "deepseek-r1:70b") || !contains(snippet, "qwen3:32b") {
		t.Error("expected both models in snippet")
	}
	// Should have [models] header
	if !contains(snippet, "[models]") {
		t.Error("expected [models] section header")
	}
	// Should have [models.providers.ollama] header
	if !contains(snippet, "[models.providers.ollama]") {
		t.Error("expected [models.providers.ollama] section header")
	}
}

func TestFormatActiveTOMLSnippet_Empty(t *testing.T) {
	snippet := ollama.FormatActiveTOMLSnippet(nil)
	if snippet != "" {
		t.Errorf("expected empty string for nil ranked, got: %q", snippet)
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
