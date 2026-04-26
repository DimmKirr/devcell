package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeExec records what was called and returns canned output.
type fakeExec struct {
	called bool
	agent  string
	prompt string
	model  string
	effort string

	stdout   string
	stderr   string
	exitCode int
}

func (f *fakeExec) Run(opts ExecOpts) ExecResult {
	f.called = true
	f.agent = opts.Agent
	f.prompt = opts.Prompt
	f.model = opts.Model
	f.effort = opts.Effort
	return ExecResult{
		Stdout:   f.stdout,
		Stderr:   f.stderr,
		ExitCode: f.exitCode,
	}
}

func postChat(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHandler_ValidClaude(t *testing.T) {
	fe := &fakeExec{stdout: "hello back", exitCode: 0}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want %q", resp.Object, "chat.completion")
	}
	if resp.Model != "anthropic/sonnet" {
		t.Errorf("model = %q, want %q", resp.Model, "anthropic/sonnet")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "hello back" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "hello back")
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("role = %q, want %q", resp.Choices[0].Message.Role, "assistant")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
	if fe.agent != "claude" {
		t.Errorf("agent = %q, want %q", fe.agent, "claude")
	}
	if fe.prompt != "hello" {
		t.Errorf("prompt = %q, want %q", fe.prompt, "hello")
	}
}

func TestHandler_ValidOpencode(t *testing.T) {
	fe := &fakeExec{stdout: "opencode result"}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"opencode","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fe.agent != "opencode" {
		t.Errorf("agent = %q, want %q", fe.agent, "opencode")
	}
}

func TestHandler_ModelWithSubmodel(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/opus","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fe.agent != "claude" {
		t.Errorf("agent = %q, want %q", fe.agent, "claude")
	}
	if fe.model != "opus" {
		t.Errorf("model = %q, want %q", fe.model, "opus")
	}
}

func TestHandler_MissingModel(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model") {
		t.Errorf("error should mention 'model', got: %s", rec.Body.String())
	}
	if fe.called {
		t.Error("exec should not be called on validation error")
	}
}

func TestHandler_MissingMessages(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "messages") {
		t.Errorf("error should mention 'messages', got: %s", rec.Body.String())
	}
}

func TestHandler_EmptyMessages(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "messages") {
		t.Errorf("error should mention 'messages', got: %s", rec.Body.String())
	}
}

func TestHandler_UnknownAgent(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"foo","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "anthropic") || !strings.Contains(body, "opencode") {
		t.Errorf("error should list valid prefixes, got: %s", body)
	}
}

func TestHandler_EmptyBody(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", &bytes.Buffer{})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{broken`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	fe := &fakeExec{}
	h := NewChatHandler(fe)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandler_MultipleMessages_UsesLast(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[{"role":"user","content":"first"},{"role":"user","content":"second"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fe.prompt != "second" {
		t.Errorf("prompt = %q, want last message %q", fe.prompt, "second")
	}
}

func TestHandler_ExecFailure(t *testing.T) {
	fe := &fakeExec{stderr: "something broke", exitCode: 1}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even on exec failure, got %d", rec.Code)
	}
	var resp ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(resp.Choices))
	}
	// On failure, stderr goes into content so callers can see the error
	if resp.Choices[0].FinishReason != "error" {
		t.Errorf("finish_reason = %q, want %q", resp.Choices[0].FinishReason, "error")
	}
}

func TestHandler_ResponseHasID(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewChatHandler(fe)

	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[{"role":"user","content":"hello"}]}`)

	var resp ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.ID == "" {
		t.Error("response should have a non-empty id")
	}
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Errorf("id = %q, want prefix 'chatcmpl-'", resp.ID)
	}
}

// --- reasoning_effort → claude --effort mapping (Chat Completions) ---
//
// Mirrors the Responses-API tests. Chat Completions sends `reasoning_effort`
// at the request root (flat), not nested under `reasoning`. Both endpoints
// must produce identical executor behavior for the same effort value.

func TestHandler_Effort_OpenAISpecValuesPassThrough(t *testing.T) {
	for _, v := range []string{"low", "medium", "high"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewChatHandler(fe)
			body := `{"model":"anthropic/sonnet","reasoning_effort":"` + v +
				`","messages":[{"role":"user","content":"hi"}]}`
			rec := postChat(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if fe.effort != v {
				t.Errorf("effort reaching executor = %q, want %q", fe.effort, v)
			}
		})
	}
}

func TestHandler_Effort_ClaudeOnlyValuesDropped(t *testing.T) {
	// Same rule as Responses: Claude's "xhigh"/"max" are not OpenAI spec, drop them.
	for _, v := range []string{"xhigh", "max"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewChatHandler(fe)
			body := `{"model":"anthropic/sonnet","reasoning_effort":"` + v +
				`","messages":[{"role":"user","content":"hi"}]}`
			rec := postChat(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if fe.effort != "" {
				t.Errorf("non-OpenAI-spec effort %q leaked to executor (got %q)", v, fe.effort)
			}
		})
	}
}

func TestHandler_Effort_UnknownValuesDropped(t *testing.T) {
	for _, v := range []string{"extreme", "minimal", "LOW", "High", "auto"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewChatHandler(fe)
			body := `{"model":"anthropic/sonnet","reasoning_effort":"` + v +
				`","messages":[{"role":"user","content":"hi"}]}`
			rec := postChat(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if fe.effort != "" {
				t.Errorf("unknown effort %q leaked to executor (got %q)", v, fe.effort)
			}
		})
	}
}

func TestHandler_Effort_AbsentNoFlag(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewChatHandler(fe)
	rec := postChat(t, h, `{"model":"anthropic/sonnet","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fe.effort != "" {
		t.Errorf("absent reasoning_effort produced executor effort=%q", fe.effort)
	}
}
