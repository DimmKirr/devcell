package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// agentForPrefix maps model prefix to the binary name.
var agentForPrefix = map[string]string{
	"claude":    "claude",
	"anthropic": "claude",
	"opencode":  "opencode",
}

// Executor runs an agent command and returns the result.
type Executor interface {
	Run(agent, prompt, model string) ExecResult
}

// ExecResult holds the output of an agent execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ChatMessage is an OpenAI-compatible message.
type ChatMessage struct {
	// Role of the message author: "system", "user", or "assistant".
	Role string `json:"role" example:"user"`
	// The message content (prompt text for user, response text for assistant).
	Content string `json:"content" example:"Explain the main function in this repo"`
}

// ChatRequest is the OpenAI-compatible chat completions request.
type ChatRequest struct {
	// Model selects the agent. Use "claude", "anthropic", or "opencode" as a prefix.
	// Append a sub-model with a slash: "claude/claude-sonnet-4-5".
	Model string `json:"model" example:"claude"`
	// Messages is the conversation history. The last user message is used as the prompt.
	Messages []ChatMessage `json:"messages"`
}

// ChatChoice is a single choice in the response.
type ChatChoice struct {
	// Index of this choice (always 0 — single-choice responses).
	Index int `json:"index" example:"0"`
	// The assistant's response message.
	Message ChatMessage `json:"message"`
	// Finish reason: "stop" on success, "error" if the agent exited non-zero.
	FinishReason string `json:"finish_reason" example:"stop"`
}

// ChatUsage tracks token usage (stubbed — always zero, reserved for future use).
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens" example:"0"`
	CompletionTokens int `json:"completion_tokens" example:"0"`
	TotalTokens      int `json:"total_tokens" example:"0"`
}

// ChatResponse is the OpenAI-compatible chat completions response.
type ChatResponse struct {
	// Unique completion ID (format: chatcmpl-<hex>).
	ID string `json:"id" example:"chatcmpl-a1b2c3d4e5f6"`
	// Object type (always "chat.completion").
	Object string `json:"object" example:"chat.completion"`
	// Unix timestamp of when the response was created.
	Created int64 `json:"created" example:"1714000000"`
	// The model that was requested.
	Model string `json:"model" example:"claude"`
	// Response choices (always a single element).
	Choices []ChatChoice `json:"choices"`
	// Token usage (stubbed, reserved for future use).
	Usage ChatUsage `json:"usage"`
}

// parseModel extracts agent and submodel from the model string.
// Formats: "claude", "opencode", "claude/claude-sonnet-4-5"
func parseModel(model string) (agent, submodel string) {
	if i := strings.IndexByte(model, '/'); i >= 0 {
		return model[:i], model[i+1:]
	}
	return model, ""
}

func chatcmplID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

// NewChatHandler returns an http.Handler for POST /v1/chat/completions.
//
// @Summary Send a chat completion request
// @Description Accepts an OpenAI-compatible chat completion request and routes it to the appropriate
// @Description LLM agent binary (Claude Code or OpenCode) running inside the DevCell container.
// @Description
// @Description The `model` field determines which agent handles the request:
// @Description - `"claude"` or `"anthropic"` — routes to Claude Code CLI
// @Description - `"opencode"` — routes to OpenCode CLI
// @Description - `"claude/claude-sonnet-4-5"` — routes to Claude Code with a specific sub-model
// @Description
// @Description Only the **last user message** in the `messages` array is sent as the prompt to the agent.
// @Description The response is a single-choice completion with finish_reason "stop" on success or "error" on failure.
// @Description
// @Description **Example request:**
// @Description ```json
// @Description {"model": "claude", "messages": [{"role": "user", "content": "explain this repo"}]}
// @Description ```
// @Tags chat
// @Accept json
// @Produce json
// @Param request body ChatRequest true "Chat completion request"
// @Success 200 {object} ChatResponse "Successful completion"
// @Failure 400 {string} string "Invalid JSON, missing model, empty messages, or unknown model prefix"
// @Failure 401 {string} string "Missing or invalid Bearer token"
// @Failure 405 {string} string "Only POST is allowed"
// @Security BearerAuth
// @Router /v1/chat/completions [post]
func NewChatHandler(exec Executor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		if req.Model == "" {
			http.Error(w, `"model" is required`, http.StatusBadRequest)
			return
		}

		prefix, submodel := parseModel(req.Model)
		agent, ok := agentForPrefix[prefix]
		if !ok {
			http.Error(w, fmt.Sprintf("unknown model %q; valid prefixes: anthropic, claude, opencode", prefix), http.StatusBadRequest)
			return
		}

		if len(req.Messages) == 0 {
			http.Error(w, `"messages" must be a non-empty array`, http.StatusBadRequest)
			return
		}

		// Use the last user message as the prompt.
		prompt := req.Messages[len(req.Messages)-1].Content

		result := exec.Run(agent, prompt, submodel)

		finishReason := "stop"
		content := result.Stdout
		if result.ExitCode != 0 {
			finishReason = "error"
			if content == "" {
				content = result.Stderr
			}
		}

		resp := ChatResponse{
			ID:      chatcmplID(),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []ChatChoice{
				{
					Index:        0,
					Message:      ChatMessage{Role: "assistant", Content: content},
					FinishReason: finishReason,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
}
