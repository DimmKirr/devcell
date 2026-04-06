package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAnthropicModels_Success(t *testing.T) {
	// Fake Anthropic API server
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("expected x-api-key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("expected anthropic-version header")
		}
		json.NewEncoder(w).Encode(anthropicModelsResponse{
			Data: []anthropicModel{
				{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
				{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
				{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5"},
			},
		})
	}))
	defer fake.Close()

	models, err := FetchAnthropicModels(fake.URL, "test-token")
	if err != nil {
		t.Fatalf("FetchAnthropicModels: %v", err)
	}

	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}

	// Should be prefixed with "anthropic/"
	if models[0].ID != "anthropic/claude-sonnet-4-6" {
		t.Errorf("models[0].ID = %q, want %q", models[0].ID, "anthropic/claude-sonnet-4-6")
	}
	if models[0].OwnedBy != "anthropic" {
		t.Errorf("owned_by = %q, want %q", models[0].OwnedBy, "anthropic")
	}
}

func TestFetchAnthropicModels_NoToken(t *testing.T) {
	models, err := FetchAnthropicModels("http://localhost", "")
	if err != nil {
		t.Fatalf("expected no error with empty token, got: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models with empty token, got %d", len(models))
	}
}

func TestFetchAnthropicModels_ServerError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer fake.Close()

	models, err := FetchAnthropicModels(fake.URL, "bad-token")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if models != nil {
		t.Errorf("expected nil models on error, got %d", len(models))
	}
}

func TestReadClaudeCredentials_ValidFile(t *testing.T) {
	dir := t.TempDir()
	data := `{"claudeAiOauth":{"accessToken":"sk-ant-test-123"}}`
	writeTestFile(t, dir+"/.credentials.json", data)

	token := ReadClaudeCredentials(dir + "/.credentials.json")
	if token != "sk-ant-test-123" {
		t.Errorf("token = %q, want %q", token, "sk-ant-test-123")
	}
}

func TestReadClaudeCredentials_MissingFile(t *testing.T) {
	token := ReadClaudeCredentials("/nonexistent/.credentials.json")
	if token != "" {
		t.Errorf("expected empty token for missing file, got %q", token)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := writeFile(path, []byte(content)); err != nil {
		t.Fatal(err)
	}
}
