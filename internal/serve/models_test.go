package serve

import (
	"fmt"
	"testing"
)

// fakeAnthropicClient returns canned models.
type fakeAnthropicClient struct {
	models []ModelInfo
	err    error
}

func (f *fakeAnthropicClient) FetchModels() ([]ModelInfo, error) {
	return f.models, f.err
}

func TestDiscoverModels_ClaudeWithAPI(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", &lookPathError{name}
	}
	ac := &fakeAnthropicClient{
		models: []ModelInfo{
			{ID: "anthropic/claude-sonnet-4-6", Object: "model", OwnedBy: "anthropic"},
			{ID: "anthropic/claude-opus-4-6", Object: "model", OwnedBy: "anthropic"},
		},
	}

	models := DiscoverModels(lookup, ac)

	if len(models) != 2 {
		t.Fatalf("expected 2 models from API, got %d", len(models))
	}
	if models[0].ID != "anthropic/claude-sonnet-4-6" {
		t.Errorf("models[0].ID = %q, want %q", models[0].ID, "anthropic/claude-sonnet-4-6")
	}
}

func TestDiscoverModels_ClaudeFallback(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", &lookPathError{name}
	}

	// nil client → fallback to hardcoded
	models := DiscoverModels(lookup, nil)

	if len(models) == 0 {
		t.Fatal("expected fallback models when no API client")
	}

	var foundOpus, foundSonnet, foundHaiku bool
	for _, m := range models {
		switch m.ID {
		case "anthropic/opus":
			foundOpus = true
		case "anthropic/sonnet":
			foundSonnet = true
		case "anthropic/haiku":
			foundHaiku = true
		}
	}
	if !foundOpus || !foundSonnet || !foundHaiku {
		t.Errorf("expected opus/sonnet/haiku fallbacks, got: %v", models)
	}
}

func TestDiscoverModels_APIErrorFallsBack(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", &lookPathError{name}
	}
	ac := &fakeAnthropicClient{err: fmt.Errorf("network error")}

	models := DiscoverModels(lookup, ac)

	// Should fall back to hardcoded
	var foundSonnet bool
	for _, m := range models {
		if m.ID == "anthropic/sonnet" {
			foundSonnet = true
		}
	}
	if !foundSonnet {
		t.Error("expected fallback to hardcoded models on API error")
	}
}

func TestDiscoverModels_NoBinaries(t *testing.T) {
	lookup := func(name string) (string, error) {
		return "", &lookPathError{name}
	}

	models := DiscoverModels(lookup, nil)

	if len(models) != 0 {
		t.Errorf("expected 0 models when no binaries found, got %d", len(models))
	}
}

func TestDiscoverModels_OpencodeFound(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "opencode" {
			return "/usr/bin/opencode", nil
		}
		return "", &lookPathError{name}
	}

	models := DiscoverModels(lookup, nil)

	var foundOpencode bool
	for _, m := range models {
		if m.ID == "opencode" {
			foundOpencode = true
		}
	}
	if !foundOpencode {
		t.Error("expected a model with id 'opencode'")
	}
}

func TestDiscoverModels_BothFound(t *testing.T) {
	lookup := func(name string) (string, error) {
		switch name {
		case "claude", "opencode":
			return "/usr/bin/" + name, nil
		}
		return "", &lookPathError{name}
	}

	models := DiscoverModels(lookup, nil)

	var foundAnthropic, foundOpencode bool
	for _, m := range models {
		if m.ID == "anthropic/sonnet" {
			foundAnthropic = true
		}
		if m.ID == "opencode" {
			foundOpencode = true
		}
	}
	if !foundAnthropic {
		t.Error("expected anthropic/* models")
	}
	if !foundOpencode {
		t.Error("expected opencode model")
	}
}

// lookPathError mimics exec.ErrNotFound for testing.
type lookPathError struct{ name string }

func (e *lookPathError) Error() string { return e.name + ": not found" }
