package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// AnthropicAPIURL is the default Anthropic models endpoint.
	AnthropicAPIURL  = "https://api.anthropic.com/v1/models"
	anthropicVersion = "2023-06-01"
)

// anthropicModel is a single model from the Anthropic API.
type anthropicModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// anthropicModelsResponse is the Anthropic /v1/models response.
type anthropicModelsResponse struct {
	Data []anthropicModel `json:"data"`
}

// FetchAnthropicModels hits the Anthropic API to get available models.
// Returns nil, nil if token is empty (no-op).
func FetchAnthropicModels(baseURL, token string) ([]ModelInfo, error) {
	if token == "" {
		return nil, nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", token)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API returned %d", resp.StatusCode)
	}

	var body anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	models := make([]ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, ModelInfo{
			ID:      "anthropic/" + m.ID,
			Object:  "model",
			Created: now,
			OwnedBy: "anthropic",
		})
	}
	return models, nil
}

// ReadClaudeCredentials reads the OAuth access token from Claude's credentials file.
func ReadClaudeCredentials(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var creds struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds.OAuth.AccessToken
}

// DefaultCredentialsPath returns the default path to Claude's credentials.
func DefaultCredentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", ".credentials.json")
}

// writeFile is a helper for tests and internal use.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
