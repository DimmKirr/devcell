package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HuggingFaceAPIURL is the default HuggingFace Hub API base URL.
const HuggingFaceAPIURL = "https://huggingface.co/api/models"

// HFModelInfo holds model metadata from HuggingFace.
type HFModelInfo struct {
	ModelID     string   `json:"modelId"`
	PipelineTag string   `json:"pipeline_tag"`
	Tags        []string `json:"tags"`
}

// FetchHFModelInfo searches the HuggingFace Hub API for a model by name
// and returns its metadata. The baseURL parameter allows test injection.
func FetchHFModelInfo(ctx context.Context, baseURL string, modelFamily string) (HFModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	u, err := url.Parse(baseURL)
	if err != nil {
		return HFModelInfo{}, fmt.Errorf("parse URL: %w", err)
	}

	q := u.Query()
	q.Set("search", modelFamily)
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	q.Set("limit", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return HFModelInfo{}, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return HFModelInfo{}, fmt.Errorf("fetch HuggingFace: %w", err)
	}
	defer resp.Body.Close()

	var results []HFModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return HFModelInfo{}, fmt.Errorf("decode response: %w", err)
	}

	if len(results) == 0 {
		return HFModelInfo{}, fmt.Errorf("no HuggingFace model found for %q", modelFamily)
	}

	return results[0], nil
}

// ModelFamily strips the :tag suffix from an ollama model name (exported).
// "deepseek-r1:32b" → "deepseek-r1", "codellama:latest" → "codellama".
func ModelFamily(name string) string {
	return modelFamily(name)
}

// InferTaskLabel returns a short human-readable label for what a model is good at,
// based on HuggingFace tags and model name.
func InferTaskLabel(info HFModelInfo, ollamaName string) string {
	name := strings.ToLower(ollamaName)

	// Check HuggingFace tags first.
	tagSet := make(map[string]bool)
	for _, tag := range info.Tags {
		tagSet[strings.ToLower(tag)] = true
	}

	if tagSet["code"] || strings.Contains(name, "coder") || strings.Contains(name, "code") {
		return "Code"
	}
	if strings.Contains(name, "-r1") || strings.Contains(name, "reasoning") || tagSet["reasoning"] {
		return "Reasoning"
	}
	if tagSet["chat"] || strings.Contains(name, "chat") {
		return "Chat"
	}

	if info.PipelineTag != "" {
		return info.PipelineTag
	}
	return "General"
}
