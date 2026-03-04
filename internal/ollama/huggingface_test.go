package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DimmKirr/devcell/internal/ollama"
)

func TestFetchHFModelInfo_ReturnsTaskType(t *testing.T) {
	results := []map[string]interface{}{
		{
			"modelId":      "deepseek-ai/DeepSeek-R1",
			"pipeline_tag": "text-generation",
			"tags":         []string{"text-generation", "pytorch", "reasoning"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	info, err := ollama.FetchHFModelInfo(context.Background(), srv.URL, "deepseek-r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.PipelineTag != "text-generation" {
		t.Errorf("expected pipeline_tag=text-generation, got %s", info.PipelineTag)
	}
	if info.ModelID != "deepseek-ai/DeepSeek-R1" {
		t.Errorf("expected modelId=deepseek-ai/DeepSeek-R1, got %s", info.ModelID)
	}
}

func TestFetchHFModelInfo_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer srv.Close()

	_, err := ollama.FetchHFModelInfo(context.Background(), srv.URL, "unknown-model")
	if err == nil {
		t.Error("expected error for no results")
	}
}

func TestFetchHFModelInfo_HTTPError(t *testing.T) {
	_, err := ollama.FetchHFModelInfo(context.Background(), "http://127.0.0.1:0", "test")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestFetchHFModelInfo_ExtractsTags(t *testing.T) {
	results := []map[string]interface{}{
		{
			"modelId":      "Qwen/Qwen2.5-Coder-32B",
			"pipeline_tag": "text-generation",
			"tags":         []string{"text-generation", "code", "pytorch"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	info, err := ollama.FetchHFModelInfo(context.Background(), srv.URL, "qwen2.5-coder")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, tag := range info.Tags {
		if tag == "code" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'code' tag in %v", info.Tags)
	}
}

func TestModelFamily_StripsSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"deepseek-r1:32b", "deepseek-r1"},
		{"qwen3:8b", "qwen3"},
		{"codellama:latest", "codellama"},
		{"model-no-tag", "model-no-tag"},
	}
	for _, tt := range tests {
		got := ollama.ModelFamily(tt.input)
		if got != tt.expected {
			t.Errorf("ModelFamily(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
