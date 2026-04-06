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
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the OpenAI-compatible chat completions request.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

// ChatChoice is a single choice in the response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage tracks token usage (stubbed for now).
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is the OpenAI-compatible chat completions response.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
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
