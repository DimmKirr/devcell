package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/logger"
)

// ResponsesRequest is the OpenAI Responses-API request body.
//
// All fields are accepted, but only Model, Input, Instructions, and Stream
// affect behavior. Tools, ResponseFormat, Reasoning, sampling params and
// other fields are decoded for compatibility and silently ignored — devcell
// shells out to a CLI agent and cannot honor them.
type ResponsesRequest struct {
	// Model selects the agent. Use "claude", "anthropic", or "opencode" as a prefix.
	// Append a sub-model with a slash: "anthropic/sonnet" or "anthropic/claude-sonnet-4-5".
	Model string `json:"model" example:"anthropic/sonnet"`

	// Input is either a string OR an array of input items.
	// String form: a single user message.
	// Array form: a multi-turn conversation; each item has role + content.
	Input json.RawMessage `json:"input" swaggertype:"string" example:"hello"`

	// Instructions is an optional system prompt prepended to the conversation.
	Instructions string `json:"instructions,omitempty" example:"be brief"`

	// Stream, if true, returns 400 — streaming is not supported.
	Stream bool `json:"stream,omitempty"`

	// Reasoning carries reasoning-model controls. Only `reasoning.effort`
	// (low|medium|high) is honored — it maps to `claude --effort`. Other
	// fields (summary, generate_summary) are accepted and ignored.
	Reasoning *ResponsesReasoningConfig `json:"reasoning,omitempty"`

	// Accepted, ignored — kept for client compatibility.
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty" swaggerignore:"true"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty" swaggerignore:"true"`
	ResponseFormat     json.RawMessage `json:"response_format,omitempty" swaggerignore:"true"`
	Text               json.RawMessage `json:"text,omitempty" swaggerignore:"true"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty" swaggerignore:"true"`
	Store              *bool           `json:"store,omitempty"`
	ParallelToolCalls  *bool           `json:"parallel_tool_calls,omitempty"`
	Truncation         string          `json:"truncation,omitempty"`
	User               string          `json:"user,omitempty"`
	Background         *bool           `json:"background,omitempty"`
	Include            []string        `json:"include,omitempty"`
}

// ResponsesReasoningConfig is the OpenAI-spec reasoning object.
//
// The Responses API allows clients to control thinking budget on reasoning
// models. devcell honors `effort` (mapping it to `claude --effort`) and
// ignores other fields.
type ResponsesReasoningConfig struct {
	// Effort: "low", "medium", or "high". Other values are silently dropped.
	Effort string `json:"effort,omitempty" example:"high"`
	// Summary controls reasoning summary verbosity. Accepted, ignored.
	Summary string `json:"summary,omitempty"`
	// GenerateSummary is a deprecated alias of Summary. Accepted, ignored.
	GenerateSummary string `json:"generate_summary,omitempty"`
}

// ResponsesOutputContentPart is one part of an output message's content array.
type ResponsesOutputContentPart struct {
	// Always "output_text" for text responses.
	Type string `json:"type" example:"output_text"`
	// The actual text.
	Text string `json:"text" example:"Hello, world!"`
	// Annotations on the text (always empty — reserved for future use).
	Annotations []any `json:"annotations"`
}

// ResponsesOutputItem is a single item in the output array.
//
// devcell only emits "message" items (assistant messages). Reasoning,
// tool_call, and other variants are not produced.
type ResponsesOutputItem struct {
	// Item type — always "message".
	Type string `json:"type" example:"message"`
	// Unique item ID (format: msg_<hex>).
	ID string `json:"id" example:"msg_a1b2c3d4e5f6"`
	// Status of this item — "completed" on success.
	Status string `json:"status" example:"completed"`
	// Role of the message author — always "assistant".
	Role string `json:"role" example:"assistant"`
	// Content parts of the message.
	Content []ResponsesOutputContentPart `json:"content"`
}

// ResponsesUsage tracks token usage (stubbed at zero — same as chat completions).
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens" example:"0"`
	OutputTokens int `json:"output_tokens" example:"0"`
	TotalTokens  int `json:"total_tokens" example:"0"`
}

// ResponsesError describes a model-side failure (exit != 0 from the agent CLI).
//
// Note: HTTP-level errors (400, 401, 405) use a different envelope at the
// top of the response — see APIError.
type ResponsesError struct {
	// Short error code, e.g. "server_error".
	Code string `json:"code,omitempty" example:"server_error"`
	// Human-readable message — typically the agent's stderr.
	Message string `json:"message" example:"agent failed"`
}

// ResponsesIncompleteDetails is reserved — always null in devcell.
type ResponsesIncompleteDetails struct {
	Reason string `json:"reason,omitempty"`
}

// ResponsesObject is the OpenAI Responses-API response body.
type ResponsesObject struct {
	// Unique response ID (format: resp_<hex>).
	ID string `json:"id" example:"resp_a1b2c3d4e5f6"`
	// Object type — always "response".
	Object string `json:"object" example:"response"`
	// Unix timestamp (seconds) when the response was created.
	CreatedAt int64 `json:"created_at" example:"1714000000"`
	// Status: "completed" on success, "failed" if the agent exited non-zero.
	Status string `json:"status" example:"completed"`
	// Model echo of the requested model string.
	Model string `json:"model" example:"anthropic/sonnet"`
	// Output items generated by the model.
	Output []ResponsesOutputItem `json:"output"`
	// Convenience field: concatenation of all output_text parts in Output.
	// Most clients (n8n, simple scripts) read this directly.
	OutputText string `json:"output_text" example:"Hello, world!"`
	// Token usage (stubbed at zero — reserved for future use).
	Usage ResponsesUsage `json:"usage"`
	// Error populated when Status == "failed", null otherwise.
	Error *ResponsesError `json:"error"`
	// IncompleteDetails is reserved — always null.
	IncompleteDetails *ResponsesIncompleteDetails `json:"incomplete_details"`
	// Echo of the input instructions, or null if not set.
	Instructions *string `json:"instructions"`
	// Echo of input metadata, or null.
	Metadata map[string]string `json:"metadata"`
	// ParallelToolCalls — echo of input or default true.
	ParallelToolCalls bool `json:"parallel_tool_calls" example:"true"`
	// PreviousResponseID — always null (stateless).
	PreviousResponseID *string `json:"previous_response_id"`
	// Reasoning config — echoes the input reasoning object (with normalized
	// effort if it was applied), or null if no reasoning was sent.
	Reasoning *ResponsesReasoningConfig `json:"reasoning"`
	// Store flag — echo of input or default true.
	Store bool `json:"store" example:"true"`
	// Sampling temperature — echo of input or default 1.0 (devcell ignores it).
	Temperature float64 `json:"temperature" example:"1.0"`
	// ToolChoice — always "auto".
	ToolChoice string `json:"tool_choice" example:"auto"`
	// Tools — always empty array (no tools bridged).
	Tools []any `json:"tools"`
	// Top-p — echo of input or default 1.0 (devcell ignores it).
	TopP float64 `json:"top_p" example:"1.0"`
	// Truncation — "disabled" by default.
	Truncation string `json:"truncation" example:"disabled"`
	// User — echo of input or empty.
	User string `json:"user,omitempty"`
}

// APIError is the OpenAI-shaped error envelope returned by /v1/responses
// for HTTP-level errors (4xx / 5xx).
type APIError struct {
	Error APIErrorBody `json:"error"`
}

// APIErrorBody is the inner error object.
type APIErrorBody struct {
	Message string `json:"message" example:"streaming is not supported"`
	Type    string `json:"type" example:"invalid_request_error"`
	Code    string `json:"code,omitempty" example:"streaming_unsupported"`
}

func writeAPIError(w http.ResponseWriter, status int, errType, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{Error: APIErrorBody{
		Message: message,
		Type:    errType,
		Code:    code,
	}})
}

func responseID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "resp_" + hex.EncodeToString(b)
}

func messageID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "msg_" + hex.EncodeToString(b)
}

// inputItem is a single message in the array form of `input`.
//
// Matches the SDK's EasyInputMessageParam: role + content (string or part array).
type inputItem struct {
	Type    string          `json:"type"` // optional — "message" if present
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// inputContentPart matches both `input_text` and `output_text` parts.
type inputContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// renderContent extracts text from a content field that may be a string
// or an array of typed parts.
func renderContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Else try array of parts.
	var parts []inputContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("content must be a string or an array of content parts")
	}
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, " "), nil
}

// buildPrompt serializes instructions + input into a single prompt string.
//
// Returns (prompt, error). An empty input returns ("", error).
func buildPrompt(instructions string, input json.RawMessage) (string, error) {
	var b strings.Builder

	if instructions != "" {
		b.WriteString("[system]: ")
		b.WriteString(instructions)
		b.WriteString("\n")
	}

	if len(input) == 0 || string(input) == "null" {
		if b.Len() == 0 {
			return "", fmt.Errorf("input is required")
		}
		return "", fmt.Errorf("input is required")
	}

	// Try string form.
	var s string
	if err := json.Unmarshal(input, &s); err == nil {
		if s == "" {
			return "", fmt.Errorf("input is required")
		}
		b.WriteString("[user]: ")
		b.WriteString(s)
		b.WriteString("\n")
		return b.String(), nil
	}

	// Try array form.
	var items []inputItem
	if err := json.Unmarshal(input, &items); err != nil {
		return "", fmt.Errorf("input must be a string or an array of input items")
	}
	if len(items) == 0 {
		return "", fmt.Errorf("input is required")
	}

	any := false
	for _, it := range items {
		text, err := renderContent(it.Content)
		if err != nil {
			return "", err
		}
		if text == "" {
			continue
		}
		role := it.Role
		if role == "" {
			role = "user"
		}
		// "developer" is OpenAI's newer alias for system.
		if role == "developer" {
			role = "system"
		}
		fmt.Fprintf(&b, "[%s]: %s\n", role, text)
		any = true
	}
	if !any {
		return "", fmt.Errorf("input is required")
	}
	return b.String(), nil
}

// NewResponsesHandler returns an http.Handler for POST /v1/responses.
//
// @Summary Create a model response (Responses API)
// @Description OpenAI-compatible Responses API endpoint. Accepts a request shaped like
// @Description `client.responses.create` from the official SDKs and returns a Response object.
// @Description
// @Description The `model` field selects the agent (same routing as `/v1/chat/completions`):
// @Description - `"anthropic/sonnet"`, `"claude/<id>"` — routes to the Claude Code CLI
// @Description - `"opencode"` — routes to the OpenCode CLI
// @Description
// @Description The `input` field is either a string or an array of input items
// @Description (`{"role": "user|assistant|system", "content": "..."}` or with typed content parts).
// @Description The `instructions` field is prepended as a system message.
// @Description
// @Description **Statelessness:** devcell does not persist responses. `previous_response_id`
// @Description is accepted for compatibility but ignored — clients must send full conversation
// @Description history every request.
// @Description
// @Description **Streaming is not supported.** Requests with `"stream": true` return 400.
// @Description
// @Description **Honored fields beyond core:**
// @Description - `reasoning.effort` (`low` / `medium` / `high`) → maps to the `claude --effort` CLI flag
// @Description   to control thinking budget. Other values (including Claude's `xhigh`/`max`) are silently dropped.
// @Description
// @Description **Unsupported fields** (`tools`, `response_format`, `temperature`, `top_p`,
// @Description `max_output_tokens`, `metadata`, `store`, `service_tier`, etc.) are accepted to keep
// @Description SDK clients happy but have no effect — devcell shells out to a CLI agent and
// @Description cannot honor them.
// @Description
// @Description **Example request:**
// @Description ```json
// @Description {"model": "anthropic/sonnet", "input": "What is 2+2?"}
// @Description ```
// @Description
// @Description **Reading the response:** most clients use the top-level `output_text` field.
// @Description SDK clients use the `output[].content[].text` structure.
// @Tags responses
// @Accept json
// @Produce json
// @Param request body ResponsesRequest true "Responses API request"
// @Success 200 {object} ResponsesObject "Successful response"
// @Failure 400 {object} APIError "Invalid JSON, missing model/input, unknown model, or streaming requested"
// @Failure 401 {string} string "Missing or invalid Bearer token"
// @Failure 405 {object} APIError "Only POST is allowed"
// @Security BearerAuth
// @Router /v1/responses [post]
func NewResponsesHandler(exec Executor, logPrompts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed,
				"invalid_request_error", "method_not_allowed",
				"only POST is allowed")
			return
		}

		var req ResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest,
				"invalid_request_error", "invalid_json",
				fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		if req.Stream {
			writeAPIError(w, http.StatusBadRequest,
				"invalid_request_error", "streaming_unsupported",
				"streaming is not supported by devcell serve")
			return
		}

		if req.Model == "" {
			writeAPIError(w, http.StatusBadRequest,
				"invalid_request_error", "model_required",
				`"model" is required`)
			return
		}

		prefix, submodel := parseModel(req.Model)
		agent, ok := agentForPrefix[prefix]
		if !ok {
			writeAPIError(w, http.StatusBadRequest,
				"invalid_request_error", "unknown_model",
				fmt.Sprintf("unknown model %q; valid prefixes: anthropic, claude, opencode", prefix))
			return
		}

		prompt, err := buildPrompt(req.Instructions, req.Input)
		if err != nil {
			code := "input_required"
			if !strings.Contains(err.Error(), "required") {
				code = "invalid_input"
			}
			writeAPIError(w, http.StatusBadRequest,
				"invalid_request_error", code,
				err.Error())
			return
		}

		// reasoning.effort is OpenAI's per-request thinking-budget hint
		// (low|medium|high). Maps to claude's --effort flag. Silently drop
		// unknown values rather than 400 — matches our permissive parsing
		// of other not-fully-supported fields.
		var effort string
		if req.Reasoning != nil && validEffort(req.Reasoning.Effort) {
			effort = req.Reasoning.Effort
		}

		id := responseID()

		if logPrompts {
			logger.Info("responses prompt",
				"id", id, "model", req.Model, "agent", agent,
				"effort", effort, "prompt", prompt)
		}

		result := exec.Run(ExecOpts{
			Agent:  agent,
			Prompt: prompt,
			Model:  submodel,
			Effort: effort,
		})

		status := "completed"
		text := result.Stdout
		var apiErr *ResponsesError
		var output []ResponsesOutputItem
		if result.ExitCode != 0 {
			status = "failed"
			msg := result.Stderr
			if msg == "" {
				msg = "agent failed"
			}
			apiErr = &ResponsesError{Code: "server_error", Message: msg}
			text = ""
			output = []ResponsesOutputItem{}
		} else {
			output = []ResponsesOutputItem{
				{
					Type:   "message",
					ID:     messageID(),
					Status: "completed",
					Role:   "assistant",
					Content: []ResponsesOutputContentPart{
						{Type: "output_text", Text: text, Annotations: []any{}},
					},
				},
			}
		}

		var instructionsEcho *string
		if req.Instructions != "" {
			s := req.Instructions
			instructionsEcho = &s
		}

		temperature := 1.0
		if req.Temperature != nil {
			temperature = *req.Temperature
		}
		topP := 1.0
		if req.TopP != nil {
			topP = *req.TopP
		}
		store := true
		if req.Store != nil {
			store = *req.Store
		}
		parallel := true
		if req.ParallelToolCalls != nil {
			parallel = *req.ParallelToolCalls
		}
		truncation := "disabled"
		if req.Truncation != "" {
			truncation = req.Truncation
		}

		if logPrompts {
			logger.Info("responses response",
				"id", id, "status", status, "output_text", text)
		}

		resp := ResponsesObject{
			ID:                 id,
			Object:             "response",
			CreatedAt:          time.Now().Unix(),
			Status:             status,
			Model:              req.Model,
			Output:             output,
			OutputText:         text,
			Usage:              ResponsesUsage{},
			Error:              apiErr,
			IncompleteDetails:  nil,
			Instructions:       instructionsEcho,
			Metadata:           map[string]string{},
			ParallelToolCalls:  parallel,
			PreviousResponseID: nil,
			Reasoning:          req.Reasoning,
			Store:              store,
			Temperature:        temperature,
			ToolChoice:         "auto",
			Tools:              []any{},
			TopP:               topP,
			Truncation:         truncation,
			User:               req.User,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
}
