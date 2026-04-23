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
	// Model identifier — use this value in the chat completions "model" field.
	ID string `json:"id" example:"anthropic/claude-sonnet-4-5-20250514"`
	// Object type (always "model").
	Object string `json:"object" example:"model"`
	// Unix timestamp when the model was discovered.
	Created int64 `json:"created" example:"1714000000"`
	// Owner of the model: "anthropic" for API-discovered models, "devcell" for local agents.
	OwnedBy string `json:"owned_by" example:"anthropic"`
}

// ModelsResponse is the OpenAI-compatible /v1/models response.
type ModelsResponse struct {
	// Object type (always "list").
	Object string `json:"object" example:"list"`
	// Available models discovered from installed agents and the Anthropic API.
	Data []ModelInfo `json:"data"`
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
//
// @Summary List available models
// @Description Returns all models that can be used in chat completion requests.
// @Description
// @Description Models are discovered dynamically at request time:
// @Description 1. If the `claude` binary is found, the server tries the Anthropic API to list real model IDs
// @Description    (e.g. `anthropic/claude-sonnet-4-5-20250514`). If the API is unreachable, it falls back to
// @Description    aliases: `anthropic/opus`, `anthropic/sonnet`, `anthropic/haiku`.
// @Description 2. If the `opencode` binary is found, `opencode` is added as an available model.
// @Description
// @Description Use any returned `id` value as the `model` field in `/v1/chat/completions`.
// @Tags models
// @Produce json
// @Success 200 {object} ModelsResponse "List of available models"
// @Failure 401 {string} string "Missing or invalid Bearer token"
// @Failure 405 {string} string "Only GET is allowed"
// @Security BearerAuth
// @Router /v1/models [get]
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
