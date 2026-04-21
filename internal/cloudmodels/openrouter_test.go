package cloudmodels_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DimmKirr/devcell/internal/cloudmodels"
	"github.com/DimmKirr/devcell/internal/ollama"
)

func TestFetchProviderModels_ParsesPricingAndName(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	resp := map[string]any{
		"data": []any{
			map[string]any{
				"id":   "anthropic/claude-opus-4-5",
				"name": "Claude Opus 4.5",
				"pricing": map[string]any{
					"prompt":     "0.000015",
					"completion": "0.000075",
				},
			},
			map[string]any{
				"id":   "openai/gpt-4o",
				"name": "GPT-4o",
				"pricing": map[string]any{
					"prompt":     "0.000005",
					"completion": "0.000015",
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	models, err := cloudmodels.FetchProviderModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchProviderModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	m := models[0]
	if m.Name != "anthropic/claude-opus-4-5" {
		t.Errorf("expected name 'anthropic/claude-opus-4-5', got %q", m.Name)
	}
	if m.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", m.Provider)
	}
	if m.CompletionPricePerToken != 0.000075 {
		t.Errorf("expected completion price 0.000075, got %v", m.CompletionPricePerToken)
	}
}

func TestFetchProviderModels_ReturnsError_WhenUnreachable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, err := cloudmodels.FetchProviderModels(context.Background(), "http://127.0.0.1:0")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestFilterLatestGen_KeepsHighestSemver(t *testing.T) {
	models := []ollama.Model{
		{Name: "anthropic/claude-opus-4", Provider: "anthropic"},
		{Name: "anthropic/claude-opus-4-5", Provider: "anthropic"},
		{Name: "anthropic/claude-sonnet-4-5", Provider: "anthropic"},
	}
	filtered := cloudmodels.FilterLatestGen(models)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 models after filtering, got %d (names: %v)", len(filtered), modelNames(filtered))
	}
	foundOpus45 := false
	for _, m := range filtered {
		if m.Name == "anthropic/claude-opus-4-5" {
			foundOpus45 = true
		}
		if m.Name == "anthropic/claude-opus-4" {
			t.Errorf("expected older claude-opus-4 to be filtered out")
		}
	}
	if !foundOpus45 {
		t.Error("expected claude-opus-4-5 to be kept")
	}
}

func TestFilterLatestGen_DifferentFamiliesKeptSeparately(t *testing.T) {
	models := []ollama.Model{
		{Name: "google/gemini-2-5-pro", Provider: "google"},
		{Name: "google/gemini-2-5-flash", Provider: "google"},
	}
	filtered := cloudmodels.FilterLatestGen(models)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 models (pro+flash are different families), got %d", len(filtered), )
	}
}

func TestFilterLatestGen_DotVersionDeduplicatesWithDash(t *testing.T) {
	// claude-opus-4.6 (dot-version) should supersede claude-opus-4 (dash-only).
	models := []ollama.Model{
		{Name: "anthropic/claude-opus-4", Provider: "anthropic"},
		{Name: "anthropic/claude-opus-4.6", Provider: "anthropic"},
	}
	filtered := cloudmodels.FilterLatestGen(models)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 model after dedup, got %d (names: %v)", len(filtered), modelNames(filtered))
	}
	if filtered[0].Name != "anthropic/claude-opus-4.6" {
		t.Errorf("expected claude-opus-4.6 to win, got %q", filtered[0].Name)
	}
}

func TestFilterLatestGen_Empty(t *testing.T) {
	filtered := cloudmodels.FilterLatestGen(nil)
	if len(filtered) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(filtered))
	}
}

func modelNames(models []ollama.Model) []string {
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	return names
}
