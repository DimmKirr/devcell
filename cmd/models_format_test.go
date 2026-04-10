package main

// White-box tests for renderModels — package main for access to unexported symbols.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/ux"
)

func TestRenderModels_JSONOutputIsValidJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "deepseek-r1:32b", ParameterSize: "32B"}, SWEScore: 49.2, Rank: 1, ScoreSource: "SWE"},
		{Model: ollama.Model{Name: "qwen3:8b", ParameterSize: "8B"}, SWEScore: 28.0, Rank: 2, ScoreSource: "est"},
	}

	out := captureStdoutMain(func() {
		renderModels(ranked, map[string]ollama.HFModelInfo{}, 32.0)
	})

	var result []map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 2 {
		t.Fatalf("want 2 entries, got %d", len(result))
	}
}

func TestRenderModels_JSONContainsNameAndRank(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "deepseek-r1:32b", ParameterSize: "32B"}, SWEScore: 49.2, Rank: 1, ScoreSource: "SWE"},
	}

	out := captureStdoutMain(func() {
		renderModels(ranked, map[string]ollama.HFModelInfo{}, 0)
	})

	var result []map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if result[0]["name"] != "deepseek-r1:32b" {
		t.Errorf("want name=deepseek-r1:32b, got %v", result[0]["name"])
	}
	// rank is a number in JSON
	rank, ok := result[0]["rank"].(float64)
	if !ok || rank != 1 {
		t.Errorf("want rank=1, got %v", result[0]["rank"])
	}
}

func TestRenderModels_JSONSuppressesProseOutput(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "qwen3:8b", ParameterSize: "8B"}, SWEScore: 28.0, Rank: 1},
	}

	out := captureStdoutMain(func() {
		renderModels(ranked, map[string]ollama.HFModelInfo{}, 0)
	})

	// Prose like "Local Models", TOML snippet markers, "ollama" footer should NOT appear
	if strings.Contains(out, "Local Models") {
		t.Errorf("json mode should suppress prose header, got: %q", out)
	}
	if strings.Contains(out, "[ollama]") {
		t.Errorf("json mode should suppress TOML snippet, got: %q", out)
	}
}

func TestRenderModels_TextContainsModelName(t *testing.T) {
	ux.OutputFormat = "text"

	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "deepseek-r1:32b", ParameterSize: "32B"}, SWEScore: 49.2, Rank: 1, ScoreSource: "SWE"},
	}

	out := captureStdoutMain(func() {
		renderModels(ranked, map[string]ollama.HFModelInfo{}, 0)
	})

	if !strings.Contains(out, "deepseek-r1:32b") {
		t.Errorf("text output should contain model name, got: %q", out)
	}
}

func TestRenderModels_TextIncludesProseHeader(t *testing.T) {
	ux.OutputFormat = "text"

	ranked := []ollama.RankedModel{
		{Model: ollama.Model{Name: "qwen3:8b", ParameterSize: "8B"}, Rank: 1},
	}

	out := captureStdoutMain(func() {
		renderModels(ranked, map[string]ollama.HFModelInfo{}, 0)
	})

	if !strings.Contains(out, "Local Models") {
		t.Errorf("text mode should include prose header, got: %q", out)
	}
}
