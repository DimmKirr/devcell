package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// ModelInfo represents a single model in the OpenAI /v1/models response.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsResponse is the OpenAI-compatible /v1/models response.
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// LookPathFunc matches exec.LookPath signature.
type LookPathFunc func(name string) (string, error)

// AnthropicClient abstracts Anthropic API calls for testability.
type AnthropicClient interface {
	FetchModels() ([]ModelInfo, error)
}

// fallbackClaudeModels are the known Claude model aliases used when API is unavailable.
var fallbackClaudeModels = []string{
	"opus",
	"sonnet",
	"haiku",
}

// DiscoverModels probes for installed agent binaries and returns available models.
// When claude is found, tries the Anthropic API first (via credentials),
// falls back to hardcoded aliases.
func DiscoverModels(lookPath LookPathFunc, ac AnthropicClient) []ModelInfo {
	now := time.Now().Unix()
	var models []ModelInfo

	// Claude: if binary exists, discover anthropic models
	if _, err := lookPath("claude"); err == nil {
		if ac != nil {
			if apiModels, err := ac.FetchModels(); err == nil && len(apiModels) > 0 {
				models = append(models, apiModels...)
			} else {
				models = append(models, fallbackModels(now)...)
			}
		} else {
			models = append(models, fallbackModels(now)...)
		}
	}

	// OpenCode: if binary exists, add as single agent model
	if _, err := lookPath("opencode"); err == nil {
		models = append(models, ModelInfo{
			ID: "opencode", Object: "model", Created: now, OwnedBy: "devcell",
		})
	}

	return models
}

func fallbackModels(now int64) []ModelInfo {
	models := make([]ModelInfo, 0, len(fallbackClaudeModels))
	for _, m := range fallbackClaudeModels {
		models = append(models, ModelInfo{
			ID: "anthropic/" + m, Object: "model", Created: now, OwnedBy: "devcell",
		})
	}
	return models
}

// RealAnthropicClient reads credentials and hits the Anthropic API.
type RealAnthropicClient struct {
	CredentialsPath string // path to .credentials.json
	APIURL          string // override for testing; defaults to AnthropicAPIURL
}

// FetchModels reads the Claude OAuth token and fetches models from the Anthropic API.
func (c *RealAnthropicClient) FetchModels() ([]ModelInfo, error) {
	credPath := c.CredentialsPath
	if credPath == "" {
		credPath = DefaultCredentialsPath()
	}

	token := ReadClaudeCredentials(credPath)
	if token == "" {
		// Also try ANTHROPIC_API_KEY env var
		token = os.Getenv("ANTHROPIC_API_KEY")
	}
	if token == "" {
		return nil, fmt.Errorf("no anthropic credentials found")
	}

	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = AnthropicAPIURL
	}

	return FetchAnthropicModels(apiURL, token)
}

// NewModelsHandler returns an http.Handler for GET /v1/models.
func NewModelsHandler(lookPath LookPathFunc, ac AnthropicClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		models := DiscoverModels(lookPath, ac)
		resp := ModelsResponse{
			Object: "list",
			Data:   models,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
}
