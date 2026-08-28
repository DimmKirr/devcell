package main

import (
	"fmt"
	"os"
)

// OpenRouter exposes two API surfaces: the Anthropic-compat endpoint used by
// Claude Code (ANTHROPIC_BASE_URL, no /v1 — Claude Code appends it) and the
// OpenAI-compat endpoint used by Codex and OpenCode SDKs.
const (
	openRouterAnthropicBaseURL = "https://openrouter.ai/api"
	openRouterOpenAIBaseURL    = "https://openrouter.ai/api/v1"
)

// FillOpenRouterKey fills OPENROUTER_API_KEY from the environment. Called
// after 1Password resolution so the key is available. Env builders that need
// the key set OPENROUTER_API_KEY to "" as a placeholder; runAgent fills it.
func FillOpenRouterKey(env map[string]string) error {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("--openrouter requires OPENROUTER_API_KEY env var (set it or add to [op] documents)")
	}
	env["OPENROUTER_API_KEY"] = apiKey
	return nil
}
