package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postResponses(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) ResponsesObject {
	t.Helper()
	var r ResponsesObject
	if err := json.NewDecoder(rec.Body).Decode(&r); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	return r
}

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) APIError {
	t.Helper()
	var e APIError
	if err := json.NewDecoder(rec.Body).Decode(&e); err != nil {
		t.Fatalf("decode error: %v\nbody: %s", err, rec.Body.String())
	}
	return e
}

func TestResponses_StringInput(t *testing.T) {
	fe := &fakeExec{stdout: "world"}
	h := NewResponsesHandler(fe)

	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hello"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	r := decodeResponse(t, rec)
	if r.Object != "response" {
		t.Errorf("object = %q, want response", r.Object)
	}
	if r.Status != "completed" {
		t.Errorf("status = %q, want completed", r.Status)
	}
	if r.OutputText != "world" {
		t.Errorf("output_text = %q, want world", r.OutputText)
	}
	if len(r.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(r.Output))
	}
	out := r.Output[0]
	if out.Type != "message" || out.Role != "assistant" || out.Status != "completed" {
		t.Errorf("output[0] = %+v", out)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "output_text" || out.Content[0].Text != "world" {
		t.Errorf("output[0].content = %+v", out.Content)
	}
	if r.Error != nil {
		t.Errorf("error = %+v, want nil", r.Error)
	}
	if !strings.HasPrefix(r.ID, "resp_") {
		t.Errorf("id = %q, want resp_ prefix", r.ID)
	}
	if !strings.HasPrefix(out.ID, "msg_") {
		t.Errorf("output[0].id = %q, want msg_ prefix", out.ID)
	}

	// Routing: "anthropic/sonnet" → agent "claude", submodel "sonnet"
	if fe.agent != "claude" || fe.model != "sonnet" {
		t.Errorf("routing: agent=%q model=%q, want claude/sonnet", fe.agent, fe.model)
	}
	if !strings.Contains(fe.prompt, "[user]: hello") {
		t.Errorf("prompt = %q, want [user]: hello", fe.prompt)
	}
}

func TestResponses_ArrayInput(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	body := `{
		"model": "anthropic/sonnet",
		"input": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"hello"},
			{"role":"user","content":"what did I say?"}
		]
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	want := "[user]: hi\n[assistant]: hello\n[user]: what did I say?\n"
	if fe.prompt != want {
		t.Errorf("prompt =\n%q\nwant\n%q", fe.prompt, want)
	}
}

func TestResponses_ContentParts(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	body := `{
		"model": "anthropic/sonnet",
		"input": [
			{"role":"user","content":[{"type":"input_text","text":"part one"},{"type":"input_text","text":"part two"}]}
		]
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fe.prompt, "[user]: part one part two") {
		t.Errorf("prompt = %q", fe.prompt)
	}
}

func TestResponses_Instructions(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	body := `{"model":"anthropic/sonnet","instructions":"be brief","input":"hi"}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	want := "[system]: be brief\n[user]: hi\n"
	if fe.prompt != want {
		t.Errorf("prompt = %q, want %q", fe.prompt, want)
	}

	// Echoed in response.
	r := decodeResponse(t, rec)
	if r.Instructions == nil || *r.Instructions != "be brief" {
		t.Errorf("instructions echo = %v, want \"be brief\"", r.Instructions)
	}
}

func TestResponses_SystemRoleInArray(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	// system role inside input[] flattens to [system]: line.
	body := `{
		"model": "anthropic/sonnet",
		"input": [
			{"role":"system","content":"you are helpful"},
			{"role":"user","content":"hi"}
		]
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	want := "[system]: you are helpful\n[user]: hi\n"
	if fe.prompt != want {
		t.Errorf("prompt = %q, want %q", fe.prompt, want)
	}
}

func TestResponses_DeveloperRoleAliasesToSystem(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	body := `{
		"model": "anthropic/sonnet",
		"input": [
			{"role":"developer","content":"dev instructions"},
			{"role":"user","content":"hi"}
		]
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fe.prompt, "[system]: dev instructions") {
		t.Errorf("prompt = %q", fe.prompt)
	}
}

func TestResponses_StreamRejected(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hi","stream":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "streaming_unsupported" {
		t.Errorf("code = %q, want streaming_unsupported", e.Error.Code)
	}
	if e.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", e.Error.Type)
	}
	if fe.called {
		t.Error("executor should not have been called")
	}
}

func TestResponses_BadJSON(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	rec := postResponses(t, h, `{not json}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "invalid_json" {
		t.Errorf("code = %q, want invalid_json", e.Error.Code)
	}
}

func TestResponses_EmptyModel(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	rec := postResponses(t, h, `{"input":"hi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "model_required" {
		t.Errorf("code = %q, want model_required", e.Error.Code)
	}
}

func TestResponses_UnknownModel(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	rec := postResponses(t, h, `{"model":"gpt-4","input":"hi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "unknown_model" {
		t.Errorf("code = %q, want unknown_model", e.Error.Code)
	}
}

func TestResponses_EmptyInputString(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "input_required" {
		t.Errorf("code = %q, want input_required", e.Error.Code)
	}
}

func TestResponses_EmptyInputArray(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "input_required" {
		t.Errorf("code = %q, want input_required", e.Error.Code)
	}
}

func TestResponses_MissingInput(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	rec := postResponses(t, h, `{"model":"anthropic/sonnet"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "input_required" {
		t.Errorf("code = %q, want input_required", e.Error.Code)
	}
}

func TestResponses_NonPOST(t *testing.T) {
	h := NewResponsesHandler(&fakeExec{})
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
	e := decodeAPIError(t, rec)
	if e.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", e.Error.Code)
	}
}

func TestResponses_ExitCodeFailure(t *testing.T) {
	fe := &fakeExec{stderr: "boom", exitCode: 1}
	h := NewResponsesHandler(fe)

	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hi"}`)
	// Failure is a 200 with status: "failed" and error populated — matches OpenAI.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (failure mode), got %d", rec.Code)
	}
	r := decodeResponse(t, rec)
	if r.Status != "failed" {
		t.Errorf("status = %q, want failed", r.Status)
	}
	if r.Error == nil || r.Error.Message != "boom" {
		t.Errorf("error = %+v, want {message: boom}", r.Error)
	}
	if r.OutputText != "" {
		t.Errorf("output_text = %q, want empty on failure", r.OutputText)
	}
	if len(r.Output) != 0 {
		t.Errorf("output len = %d, want 0 on failure", len(r.Output))
	}
}

func TestResponses_IgnoredFieldsTolerated(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	// All these fields should decode cleanly and have no effect.
	body := `{
		"model": "anthropic/sonnet",
		"input": "hi",
		"previous_response_id": "resp_old123",
		"tools": [{"type":"function","function":{"name":"foo"}}],
		"tool_choice": "auto",
		"response_format": {"type":"json_object"},
		"reasoning": {"effort":"high"},
		"temperature": 0.5,
		"top_p": 0.9,
		"max_output_tokens": 100,
		"metadata": {"a":"b"},
		"store": false,
		"parallel_tool_calls": false,
		"truncation": "auto",
		"user": "u1",
		"include": ["message.output_text.logprobs"]
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	r := decodeResponse(t, rec)
	if r.Status != "completed" {
		t.Errorf("status = %q, want completed", r.Status)
	}
	// Echoes
	if r.Temperature != 0.5 {
		t.Errorf("temperature echo = %v, want 0.5", r.Temperature)
	}
	if r.TopP != 0.9 {
		t.Errorf("top_p echo = %v, want 0.9", r.TopP)
	}
	if r.Store {
		t.Errorf("store echo = true, want false")
	}
	if r.ParallelToolCalls {
		t.Errorf("parallel_tool_calls echo = true, want false")
	}
	if r.Truncation != "auto" {
		t.Errorf("truncation echo = %q, want auto", r.Truncation)
	}
	if r.User != "u1" {
		t.Errorf("user echo = %q, want u1", r.User)
	}
}

// --- reasoning.effort → claude --effort mapping ---
//
// OpenAI documents `low`, `medium`, `high` as the valid values for
// `reasoning.effort`. Claude CLI accepts those plus `xhigh` and `max`.
// We deliberately accept only the OpenAI-spec values and silently drop
// non-spec values (including Claude's extensions) — same permissive
// pattern we use for other partially-supported fields.

func TestResponses_Effort_OpenAISpecValuesPassThrough(t *testing.T) {
	for _, v := range []string{"low", "medium", "high"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewResponsesHandler(fe)
			body := `{"model":"anthropic/sonnet","input":"hi","reasoning":{"effort":"` + v + `"}}`
			rec := postResponses(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if fe.effort != v {
				t.Errorf("effort reaching executor = %q, want %q", fe.effort, v)
			}
		})
	}
}

func TestResponses_Effort_ClaudeOnlyValuesDropped(t *testing.T) {
	// "xhigh" and "max" are Claude CLI extensions, NOT in the OpenAI spec.
	// Clients sending them are misusing the OpenAI surface — we drop the
	// value rather than silently passing a non-spec string to the CLI.
	for _, v := range []string{"xhigh", "max"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewResponsesHandler(fe)
			body := `{"model":"anthropic/sonnet","input":"hi","reasoning":{"effort":"` + v + `"}}`
			rec := postResponses(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if fe.effort != "" {
				t.Errorf("non-OpenAI-spec effort %q leaked to executor (got %q), should be dropped",
					v, fe.effort)
			}
		})
	}
}

func TestResponses_Effort_UnknownValuesDropped(t *testing.T) {
	for _, v := range []string{"extreme", "minimal", "LOW", "High", "auto", "none"} {
		t.Run(v, func(t *testing.T) {
			fe := &fakeExec{stdout: "ok"}
			h := NewResponsesHandler(fe)
			body := `{"model":"anthropic/sonnet","input":"hi","reasoning":{"effort":"` + v + `"}}`
			rec := postResponses(t, h, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if fe.effort != "" {
				t.Errorf("unknown effort %q leaked to executor (got %q)", v, fe.effort)
			}
		})
	}
}

func TestResponses_Effort_AbsentNoFlag(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)
	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fe.effort != "" {
		t.Errorf("absent reasoning.effort still produced executor effort=%q", fe.effort)
	}
}

func TestResponses_Effort_OtherReasoningFieldsIgnored(t *testing.T) {
	// `summary` and `generate_summary` decode but have no effect.
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)
	body := `{
		"model":"anthropic/sonnet","input":"hi",
		"reasoning":{"effort":"medium","summary":"detailed","generate_summary":"auto"}
	}`
	rec := postResponses(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fe.effort != "medium" {
		t.Errorf("effort = %q, want medium", fe.effort)
	}
}

func TestResponses_Effort_EchoedInResponse(t *testing.T) {
	// The reasoning object should be echoed back verbatim (with whatever
	// fields the client sent) so SDK round-trips don't drop information.
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)
	rec := postResponses(t, h,
		`{"model":"anthropic/sonnet","input":"hi","reasoning":{"effort":"high","summary":"auto"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	r := decodeResponse(t, rec)
	if r.Reasoning == nil {
		t.Fatalf("expected reasoning to be echoed, got nil")
	}
	if r.Reasoning.Effort != "high" {
		t.Errorf("echo effort = %q, want high", r.Reasoning.Effort)
	}
	if r.Reasoning.Summary != "auto" {
		t.Errorf("echo summary = %q, want auto", r.Reasoning.Summary)
	}
}

func TestResponses_OpenCodeRouting(t *testing.T) {
	fe := &fakeExec{stdout: "ok"}
	h := NewResponsesHandler(fe)

	rec := postResponses(t, h, `{"model":"opencode","input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fe.agent != "opencode" || fe.model != "" {
		t.Errorf("routing: agent=%q model=%q, want opencode/(empty)", fe.agent, fe.model)
	}
}
