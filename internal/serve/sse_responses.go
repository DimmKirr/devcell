package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Responses SSE shape (per OpenAI Responses API):
//
//   event: <name>
//   data: <json>
//
// Events emitted by devcell, in order, on a successful turn:
//
//   response.created                — initial response object
//   response.in_progress            — status update
//   response.output_item.added      — assistant message item starts
//   response.content_part.added     — output_text part starts
//   response.output_text.delta      — incremental text (one per claude delta)
//   response.output_text.done       — text part assembled
//   response.content_part.done      — content part finalized
//   response.output_item.done       — message item finalized
//   response.completed              — terminal, with usage
//
// On error: response.failed with response.error: {code, message}.
// No `[DONE]` sentinel — response.completed is the terminator.

type responseSSEPayload struct {
	Type     string           `json:"type"`
	Response *ResponsesObject `json:"response,omitempty"`
	// item / content_part / delta envelopes
	OutputIndex  *int            `json:"output_index,omitempty"`
	ContentIndex *int            `json:"content_index,omitempty"`
	ItemID       string          `json:"item_id,omitempty"`
	Item         json.RawMessage `json:"item,omitempty"`
	Part         json.RawMessage `json:"part,omitempty"`
	Delta        string          `json:"delta,omitempty"`
	Text         string          `json:"text,omitempty"`
}

// streamResponses runs claude in stream mode and writes Responses-API
// SSE to w.
func streamResponses(
	w http.ResponseWriter,
	r *http.Request,
	opts ExecOpts,
	respID, model string,
	instructions *string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming requires an http.Flusher", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events, err := streamClaude(ctx, opts)
	if err != nil {
		writeResponsesError(w, flusher, respID, model, err)
		return
	}
	pumpResponsesSSE(ctx, w, flusher, events, respID, model, instructions, 15*time.Second)
}

// pumpResponsesSSE consumes canonical events and writes Responses SSE
// frames. Pure formatter for tests.
func pumpResponsesSSE(
	ctx context.Context,
	w io.Writer,
	flusher http.Flusher,
	events <-chan StreamEvent,
	respID, model string,
	instructions *string,
	heartbeatInterval time.Duration,
) {
	created := time.Now().Unix()
	itemID := messageID()
	zero := 0

	// Track whether we've started the message item — emitted lazily on
	// the first delta so an instant-error path can skip it.
	itemStarted := false

	// Accumulate text so output_text.done / output_item.done carry the
	// final assembled text (the OpenAI spec requires it).
	var accumulated string

	emitCreated := func() {
		obj := &ResponsesObject{
			ID:                respID,
			Object:            "response",
			CreatedAt:         created,
			Status:            "in_progress",
			Model:             model,
			Output:            []ResponsesOutputItem{},
			OutputText:        "",
			Usage:             ResponsesUsage{},
			Error:             nil,
			IncompleteDetails: nil,
			Instructions:      instructions,
			Metadata:          map[string]string{},
			ParallelToolCalls: true,
			Reasoning:         nil,
			Store:             true,
			Temperature:       1.0,
			ToolChoice:        "auto",
			Tools:             []any{},
			TopP:              1.0,
			Truncation:        "disabled",
		}
		writeResponseEvent(w, flusher, "response.created", responseSSEPayload{
			Type: "response.created", Response: obj,
		})
		writeResponseEvent(w, flusher, "response.in_progress", responseSSEPayload{
			Type: "response.in_progress", Response: obj,
		})
	}

	startItem := func() {
		if itemStarted {
			return
		}
		itemStarted = true
		item, _ := json.Marshal(map[string]any{
			"id":      itemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		})
		part, _ := json.Marshal(map[string]any{
			"type":        "output_text",
			"text":        "",
			"annotations": []any{},
		})
		writeResponseEvent(w, flusher, "response.output_item.added", responseSSEPayload{
			Type:        "response.output_item.added",
			OutputIndex: &zero,
			Item:        item,
		})
		writeResponseEvent(w, flusher, "response.content_part.added", responseSSEPayload{
			Type:         "response.content_part.added",
			ItemID:       itemID,
			OutputIndex:  &zero,
			ContentIndex: &zero,
			Part:         part,
		})
	}

	finalize := func(final *claudeJSONResult) {
		if !itemStarted {
			startItem()
		}
		writeResponseEvent(w, flusher, "response.output_text.done", responseSSEPayload{
			Type: "response.output_text.done", ItemID: itemID,
			OutputIndex: &zero, ContentIndex: &zero, Text: accumulated,
		})
		part, _ := json.Marshal(map[string]any{
			"type": "output_text", "text": accumulated, "annotations": []any{},
		})
		writeResponseEvent(w, flusher, "response.content_part.done", responseSSEPayload{
			Type: "response.content_part.done", ItemID: itemID,
			OutputIndex: &zero, ContentIndex: &zero, Part: part,
		})
		item, _ := json.Marshal(map[string]any{
			"id": itemID, "type": "message", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": accumulated, "annotations": []any{}}},
		})
		writeResponseEvent(w, flusher, "response.output_item.done", responseSSEPayload{
			Type: "response.output_item.done", OutputIndex: &zero, Item: item,
		})

		var usage ResponsesUsage
		if final != nil {
			usage = responsesUsageFromExec(Usage{
				InputTokens:              final.Usage.InputTokens,
				OutputTokens:             final.Usage.OutputTokens,
				CacheCreationInputTokens: final.Usage.CacheCreationInputTokens,
				CacheReadInputTokens:     final.Usage.CacheReadInputTokens,
			})
		}
		obj := &ResponsesObject{
			ID:        respID,
			Object:    "response",
			CreatedAt: created,
			Status:    "completed",
			Model:     model,
			Output: []ResponsesOutputItem{{
				Type:   "message",
				ID:     itemID,
				Status: "completed",
				Role:   "assistant",
				Content: []ResponsesOutputContentPart{
					{Type: "output_text", Text: accumulated, Annotations: []any{}},
				},
			}},
			OutputText:   accumulated,
			Usage:        usage,
			Instructions: instructions,
			Metadata:     map[string]string{},
			Tools:        []any{},
			ToolChoice:   "auto",
			Truncation:   "disabled",
			Temperature:  1.0,
			TopP:         1.0,
			Store:        true,
		}
		writeResponseEvent(w, flusher, "response.completed", responseSSEPayload{
			Type: "response.completed", Response: obj,
		})
	}

	failed := func(message string) {
		errObj := &ResponsesError{Code: "server_error", Message: message}
		obj := &ResponsesObject{
			ID:        respID,
			Object:    "response",
			CreatedAt: created,
			Status:    "failed",
			Model:     model,
			Error:     errObj,
		}
		writeResponseEvent(w, flusher, "response.failed", responseSSEPayload{
			Type: "response.failed", Response: obj,
		})
	}

	emitCreated()

	var heartbeatC <-chan time.Time
	if heartbeatInterval > 0 {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		heartbeatC = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeatC:
			fmt.Fprint(w, ":keepalive\n\n")
			flusher.Flush()

		case ev, open := <-events:
			if !open {
				finalize(nil)
				return
			}
			switch ev.Kind {
			case StreamEventMessageStart:
				startItem()

			case StreamEventTextDelta:
				startItem()
				accumulated += ev.Delta
				writeResponseEvent(w, flusher, "response.output_text.delta", responseSSEPayload{
					Type:         "response.output_text.delta",
					ItemID:       itemID,
					OutputIndex:  &zero,
					ContentIndex: &zero,
					Delta:        ev.Delta,
				})

			case StreamEventResult:
				if ev.Final != nil && ev.Final.IsError {
					failed(ev.Final.Subtype)
					return
				}
				finalize(ev.Final)
				return

			case StreamEventError:
				failed(ev.Err.Error())
				return

			default:
				// MessageStop is implicit in Result for our mapping.
			}
		}
	}
}

func writeResponseEvent(w io.Writer, flusher http.Flusher, name string, payload responseSSEPayload) {
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	flusher.Flush()
}

func writeResponsesError(w io.Writer, flusher http.Flusher, respID, model string, err error) {
	obj := &ResponsesObject{
		ID:        respID,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    "failed",
		Model:     model,
		Error:     &ResponsesError{Code: "server_error", Message: err.Error()},
	}
	writeResponseEvent(w, flusher, "response.failed", responseSSEPayload{
		Type: "response.failed", Response: obj,
	})
}
