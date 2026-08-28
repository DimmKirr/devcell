package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestResponses_Background_Returns202 is the first red test: a request with
// `"background": true` must return HTTP 202 with a stub ResponsesObject that
// has a `resp_`-prefixed id and a non-terminal status (queued or in_progress).
// The synchronous code path is the regression baseline — without background,
// the same request returns 200 with the full output. With background, the
// agent runs in a goroutine and the handler responds immediately.
func TestResponses_Background_Returns202(t *testing.T) {
	fe := &fakeExec{stdout: "should not appear in immediate response"}
	store := NewJobStore()
	h := NewResponsesHandler(fe, store, false, "", "")

	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hello","background":true}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ResponsesObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Object != "response" {
		t.Errorf("object = %q, want response", resp.Object)
	}
	if !strings.HasPrefix(resp.ID, "resp_") {
		t.Errorf("id = %q, want resp_ prefix", resp.ID)
	}
	// Background returns before the agent finishes. Status must be one of
	// the non-terminal job states — never "completed".
	switch resp.Status {
	case "queued", "in_progress":
		// ok
	default:
		t.Errorf("status = %q, want queued or in_progress", resp.Status)
	}
	// The 202 body should not leak agent output (it hasn't run yet, or
	// even if it has finished racy-fast, the synchronous response builder
	// shouldn't have populated it).
	if resp.OutputText != "" {
		t.Errorf("output_text = %q, want empty on 202", resp.OutputText)
	}
	if resp.Model != "anthropic/sonnet" {
		t.Errorf("model = %q, want anthropic/sonnet", resp.Model)
	}
}

// TestResponseGet_UnknownID returns 404 with the OpenAI-shaped APIError
// envelope, mirroring how OpenAI's Responses API behaves for an id that
// doesn't exist (or has been evicted).
func TestResponseGet_UnknownID(t *testing.T) {
	store := NewJobStore()
	h := NewResponseGetHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_doesnotexist", nil)
	req.SetPathValue("id", "resp_doesnotexist")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var apiErr APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode APIError: %v\nbody: %s", err, rec.Body.String())
	}
	if apiErr.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", apiErr.Error.Type)
	}
	if apiErr.Error.Code != "response_not_found" {
		t.Errorf("error.code = %q, want response_not_found", apiErr.Error.Code)
	}
	if apiErr.Error.Message == "" {
		t.Error("error.message must not be empty")
	}
}

// blockExec lets the test pause the goroutine between job submission and
// completion so we can observe the in_progress state, then unblock and
// observe completion.
type blockExec struct {
	release chan struct{}
	stdout  string
}

func (b *blockExec) Run(opts ExecOpts) ExecResult {
	<-b.release
	return ExecResult{Stdout: b.stdout}
}

// pollGet helper: GET /v1/responses/{id} via a synthetic request with
// SetPathValue. Returns the decoded body and the HTTP status.
func pollGet(t *testing.T, h http.Handler, id string) (ResponsesObject, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp ResponsesObject
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v\nbody: %s", err, rec.Body.String())
		}
	}
	return resp, rec.Code
}

// TestResponses_Background_InProgressThenCompleted submits a background
// job, polls GET while the executor is still running (status=in_progress,
// empty output), then unblocks the executor and polls again until the
// terminal state appears (status=completed, populated output_text and
// usage). This is the core happy path for the async-job pattern.
func TestResponses_Background_InProgressThenCompleted(t *testing.T) {
	be := &blockExec{release: make(chan struct{}), stdout: "ASYNC OK"}
	store := NewJobStore()
	postH := NewResponsesHandler(be, store, false, "", "")
	getH := NewResponseGetHandler(store)

	rec := postResponses(t, postH, `{"model":"anthropic/sonnet","input":"go","background":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var submitted ResponsesObject
	if err := json.NewDecoder(rec.Body).Decode(&submitted); err != nil {
		t.Fatalf("decode 202: %v", err)
	}
	id := submitted.ID

	// 1) GET while executor is blocked → status=in_progress.
	resp, code := pollGet(t, getH, id)
	if code != http.StatusOK {
		t.Fatalf("GET while blocked: code = %d, want 200", code)
	}
	if resp.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress (executor still blocked)", resp.Status)
	}
	if resp.OutputText != "" {
		t.Errorf("output_text = %q, want empty while in-progress", resp.OutputText)
	}

	// 2) Unblock; poll until terminal.
	close(be.release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, code = pollGet(t, getH, id)
		if code == http.StatusOK && resp.Status == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if resp.Status != "completed" {
		t.Fatalf("status = %q after unblock, want completed", resp.Status)
	}
	if resp.OutputText != "ASYNC OK" {
		t.Errorf("output_text = %q, want ASYNC OK", resp.OutputText)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "message" {
		t.Errorf("output structure malformed: %+v", resp.Output)
	}
}

// TestResponses_Background_Failed verifies that a non-zero exit from the
// agent inside the background goroutine transitions the job to status=failed
// with the stderr surfaced on error.message — same shape as the synchronous
// failure path.
func TestResponses_Background_Failed(t *testing.T) {
	fe := &fakeExec{stderr: "agent blew up", exitCode: 1}
	store := NewJobStore()
	postH := NewResponsesHandler(fe, store, false, "", "")
	getH := NewResponseGetHandler(store)

	rec := postResponses(t, postH, `{"model":"anthropic/sonnet","input":"x","background":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: expected 202, got %d", rec.Code)
	}
	var submitted ResponsesObject
	json.NewDecoder(rec.Body).Decode(&submitted)

	var resp ResponsesObject
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var code int
		resp, code = pollGet(t, getH, submitted.ID)
		if code == http.StatusOK && resp.Status != "in_progress" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if resp.Status != "failed" {
		t.Fatalf("status = %q, want failed", resp.Status)
	}
	if resp.Error == nil {
		t.Fatal("error must be populated on failure")
	}
	if resp.Error.Message != "agent blew up" {
		t.Errorf("error.message = %q, want stderr passthrough", resp.Error.Message)
	}
	if resp.OutputText != "" {
		t.Errorf("output_text = %q, want empty on failure", resp.OutputText)
	}
}

// TestResponseCancel_InProgress submits a background job whose executor is
// blocked, then issues POST /v1/responses/{id}/cancel. The job status must
// flip to "cancelled" immediately (whether or not the goroutine has yet
// observed the ctx cancellation — the API contract is that the cancel HTTP
// call is the source of truth for visible status).
func TestResponseCancel_InProgress(t *testing.T) {
	be := &blockExec{release: make(chan struct{}), stdout: "never delivered"}
	store := NewJobStore()
	postH := NewResponsesHandler(be, store, false, "", "")
	cancelH := NewResponseCancelHandler(store)
	getH := NewResponseGetHandler(store)

	rec := postResponses(t, postH, `{"model":"anthropic/sonnet","input":"go","background":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: expected 202, got %d", rec.Code)
	}
	var submitted ResponsesObject
	json.NewDecoder(rec.Body).Decode(&submitted)
	id := submitted.ID

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/responses/"+id+"/cancel", nil)
	cancelReq.SetPathValue("id", id)
	cancelRec := httptest.NewRecorder()
	cancelH.ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelResp ResponsesObject
	if err := json.NewDecoder(cancelRec.Body).Decode(&cancelResp); err != nil {
		t.Fatalf("decode cancel body: %v", err)
	}
	if cancelResp.Status != "cancelled" {
		t.Errorf("cancel response status = %q, want cancelled", cancelResp.Status)
	}

	resp, code := pollGet(t, getH, id)
	if code != http.StatusOK {
		t.Fatalf("GET after cancel: code = %d", code)
	}
	if resp.Status != "cancelled" {
		t.Errorf("GET status = %q, want cancelled", resp.Status)
	}

	// Unblock the executor so the goroutine can exit (otherwise the test
	// process leaks a goroutine). Result must NOT overwrite the cancelled
	// status — JobStore.Complete guards against that.
	close(be.release)
	time.Sleep(20 * time.Millisecond)
	resp, _ = pollGet(t, getH, id)
	if resp.Status != "cancelled" {
		t.Errorf("status after late completion = %q, want cancelled (cancel wins)", resp.Status)
	}
}

// TestResponses_Background_WithStream_Returns400 enforces the
// stream+background mutual exclusion. Streamed-background polling is a future
// enhancement (OpenAI supports retrieving a stored response with stream=true);
// for the first pass we reject the combination with 400 so clients fail loud
// rather than silently getting one mode or the other.
func TestResponses_Background_WithStream_Returns400(t *testing.T) {
	fe := &fakeExec{stdout: "x"}
	store := NewJobStore()
	h := NewResponsesHandler(fe, store, false, "", "")

	rec := postResponses(t, h, `{"model":"anthropic/sonnet","input":"hi","background":true,"stream":true}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var apiErr APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode APIError: %v", err)
	}
	if apiErr.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", apiErr.Error.Type)
	}
	if apiErr.Error.Code != "unsupported_combination" {
		t.Errorf("error.code = %q, want unsupported_combination", apiErr.Error.Code)
	}
}

// TestResponses_Background_SurvivesRequestCtxCancel proves the critical
// correctness property: the goroutine that runs the agent must NOT be bound
// to r.Context(). The request context is cancelled the moment we write the
// 202 response. If runBackgroundJob inherited that ctx (or if a future
// ContextExecutor passed it through to exec.CommandContext), the claude
// subprocess would die immediately and the job would never reach completed.
//
// We simulate this by submitting via an http.Request whose context we
// explicitly cancel right after the POST returns, then verify the goroutine
// still completes and stores its result.
func TestResponses_Background_SurvivesRequestCtxCancel(t *testing.T) {
	be := &blockExec{release: make(chan struct{}), stdout: "SURVIVED"}
	store := NewJobStore()
	postH := NewResponsesHandler(be, store, false, "", "")
	getH := NewResponseGetHandler(store)

	ctx, cancel := context.WithCancel(context.Background())
	body := strings.NewReader(`{"model":"anthropic/sonnet","input":"x","background":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	postH.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: expected 202, got %d", rec.Code)
	}
	var submitted ResponsesObject
	json.NewDecoder(rec.Body).Decode(&submitted)

	// Cancel the request context. If runBackgroundJob inherited it, the
	// goroutine would now treat it as a cancellation signal and would
	// either bail or get its subprocess killed via CommandContext.
	cancel()

	// Now unblock the executor; goroutine should still complete normally.
	close(be.release)

	var resp ResponsesObject
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var code int
		resp, code = pollGet(t, getH, submitted.ID)
		if code == http.StatusOK && resp.Status == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if resp.Status != "completed" {
		t.Fatalf("status = %q, want completed (goroutine should outlive request ctx cancel)", resp.Status)
	}
	if resp.OutputText != "SURVIVED" {
		t.Errorf("output_text = %q, want SURVIVED", resp.OutputText)
	}
}

// TestJobStore_Sweep_EvictsTerminalJobs verifies the TTL eviction path.
// Completed/failed/cancelled jobs older than the TTL should be removed;
// in-progress jobs (no FinishedAt yet) must NEVER be evicted regardless of
// how long they've been running. Without this protection the store would
// grow unboundedly in long-lived cell serve processes.
func TestJobStore_Sweep_EvictsTerminalJobs(t *testing.T) {
	store := NewJobStore()

	// Fresh in-progress job — must survive.
	store.Create("resp_inflight", func() {})

	// Old terminal job — must be evicted.
	store.Create("resp_old_done", func() {})
	store.Complete("resp_old_done", "completed", &ResponsesObject{ID: "resp_old_done"})
	// Manually backdate FinishedAt so we can sweep without sleeping.
	store.setFinishedAt("resp_old_done", time.Now().Add(-2*time.Hour))

	// Fresh terminal job — must NOT be evicted yet (younger than TTL).
	store.Create("resp_fresh_done", func() {})
	store.Complete("resp_fresh_done", "completed", &ResponsesObject{ID: "resp_fresh_done"})

	evicted := store.Sweep(time.Now(), time.Hour)
	if evicted != 1 {
		t.Errorf("evicted = %d, want 1 (only resp_old_done)", evicted)
	}

	if _, ok := store.Get("resp_inflight"); !ok {
		t.Error("in-progress job was evicted (should NEVER happen)")
	}
	if _, ok := store.Get("resp_old_done"); ok {
		t.Error("old terminal job was not evicted")
	}
	if _, ok := store.Get("resp_fresh_done"); !ok {
		t.Error("fresh terminal job was evicted prematurely")
	}
}

// TestResponseCancel_UnknownID returns 404 with the same envelope as GET.
func TestResponseCancel_UnknownID(t *testing.T) {
	store := NewJobStore()
	h := NewResponseCancelHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/resp_nope/cancel", nil)
	req.SetPathValue("id", "resp_nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
