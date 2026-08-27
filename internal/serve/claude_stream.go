package serve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// claude `--output-format=stream-json --include-partial-messages` emits
// JSONL on stdout: one JSON object per line. The outer envelope has a
// `type` discriminator. Most lines are `stream_event` wrappers around a
// raw Anthropic Messages-API event (message_start / content_block_delta
// / message_stop / etc.). The terminal line is `type=result` with the
// same shape claude_json.go decodes for non-streamed runs.
//
// We model only the events devcell consumes:
//
//   {"type":"stream_event","event":{"type":"message_start", "message":{…}}}
//   {"type":"stream_event","event":{"type":"content_block_start","index":N,"content_block":{"type":"text"|"tool_use"|…}}}
//   {"type":"stream_event","event":{"type":"content_block_delta","index":N,"delta":{"type":"text_delta","text":"…"}}}
//   {"type":"stream_event","event":{"type":"content_block_stop","index":N}}
//   {"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"…"},"usage":{…}}}
//   {"type":"stream_event","event":{"type":"message_stop"}}
//   {"type":"result", …}
//
// Other top-level types (system/init, system/hook_*, assistant full-turn,
// user/tool_result) are intentionally skipped for v1.

// streamEnvelope is the outer wrapper for every JSONL line.
type streamEnvelope struct {
	Type    string          `json:"type"`
	Event   json.RawMessage `json:"event,omitempty"`   // populated for type="stream_event"
	Subtype string          `json:"subtype,omitempty"` // populated for type="result" (and others we ignore)
}

// streamInnerEvent is the Anthropic Messages-API event nested inside a
// stream_event wrapper.
type streamInnerEvent struct {
	Type         string              `json:"type"`
	Index        int                 `json:"index,omitempty"`
	Message      *streamMessage      `json:"message,omitempty"`       // message_start
	ContentBlock *streamContentBlock `json:"content_block,omitempty"` // content_block_start
	Delta        *streamDelta        `json:"delta,omitempty"`         // content_block_delta + message_delta share this field name with different shapes
}

type streamMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Role  string `json:"role"`
}

type streamContentBlock struct {
	Type string `json:"type"` // "text" | "tool_use" | …
	Text string `json:"text"` // populated for type="text" (typically empty at start)
}

// streamDelta carries either a content-block delta (text_delta) or a
// message-stop delta (stop_reason) — distinguished by the `type` field
// when present (text_delta) or by the surrounding event type.
type streamDelta struct {
	Type       string `json:"type,omitempty"`        // "text_delta" for content_block_delta
	Text       string `json:"text,omitempty"`        // text_delta payload
	StopReason string `json:"stop_reason,omitempty"` // populated on message_delta
}

// StreamEventKind discriminates the canonical event sent on the channel.
type StreamEventKind int

const (
	streamEventInvalid StreamEventKind = iota
	StreamEventMessageStart
	StreamEventTextDelta
	StreamEventMessageStop
	StreamEventResult
	StreamEventError
)

// StreamEvent is the canonical, OpenAI-agnostic event the SSE formatters
// consume. One source (claude scanner), two sinks (Chat Completions and
// Responses).
type StreamEvent struct {
	Kind StreamEventKind
	// MessageID populated on MessageStart (the upstream message id).
	MessageID string
	// Model populated on MessageStart.
	Model string
	// Delta populated on TextDelta — incremental text since the previous
	// delta, not cumulative.
	Delta string
	// StopReason populated on MessageStop — "end_turn" / "max_tokens" / …
	StopReason string
	// Final populated on Result — reuses claude_json.go's typed envelope
	// for the terminal usage + cost payload.
	Final *claudeJSONResult
	// Err populated on Error — terminal; the caller should stop reading.
	Err error
}

// scanClaudeStream reads JSONL lines from r and emits canonical events on
// the returned channel. The channel closes when the scanner reaches EOF
// or a fatal decode error. Non-fatal lines (unknown envelopes, skipped
// event types) are silently dropped.
//
// The caller is responsible for cancelling its source (typically via
// killing the claude subprocess) — this function only reads.
func scanClaudeStream(r io.Reader) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		// claude can emit single lines >64KB (e.g. when an assistant
		// turn carries a large final content block). Bump the buffer.
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			ev, ok := decodeStreamLine(line)
			if !ok {
				continue
			}
			out <- ev
		}
		if err := sc.Err(); err != nil {
			out <- StreamEvent{Kind: StreamEventError, Err: fmt.Errorf("claude stream scan: %w", err)}
		}
	}()
	return out
}

// decodeStreamLine returns (event, true) when the line carries a
// canonical event we want to forward; (zero, false) otherwise. Decode
// failures are logged via the returned Error event only when they look
// like real claude output (i.e. valid JSON with a type field) — random
// non-JSON lines are silently dropped to be tolerant of stderr noise
// occasionally landing on stdout.
func decodeStreamLine(line []byte) (StreamEvent, bool) {
	var env streamEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return StreamEvent{}, false
	}
	switch env.Type {
	case "stream_event":
		return decodeInnerEvent(env.Event)
	case "result":
		// Reuse the same parser as the buffered (non-stream) path so
		// the terminal usage shape is identical.
		text, usage, err := parseClaudeJSON(line)
		if err != nil {
			return StreamEvent{Kind: StreamEventError, Err: err}, true
		}
		return StreamEvent{
			Kind: StreamEventResult,
			Final: &claudeJSONResult{
				Type:         "result",
				Subtype:      env.Subtype,
				Result:       text,
				TotalCostUSD: usage.TotalCostUSD,
				Usage: claudeJSONUsageObj{
					InputTokens:              usage.InputTokens,
					OutputTokens:             usage.OutputTokens,
					CacheCreationInputTokens: usage.CacheCreationInputTokens,
					CacheReadInputTokens:     usage.CacheReadInputTokens,
				},
			},
		}, true
	default:
		// system/init, system/hook_*, assistant (full turn), user (tool
		// result). Out of scope for v1.
		return StreamEvent{}, false
	}
}

func decodeInnerEvent(raw json.RawMessage) (StreamEvent, bool) {
	var ev streamInnerEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return StreamEvent{}, false
	}
	switch ev.Type {
	case "message_start":
		if ev.Message == nil {
			return StreamEvent{}, false
		}
		return StreamEvent{
			Kind:      StreamEventMessageStart,
			MessageID: ev.Message.ID,
			Model:     ev.Message.Model,
		}, true

	case "content_block_delta":
		// Only forward text_delta — tool_use deltas are out of scope.
		if ev.Delta == nil || ev.Delta.Type != "text_delta" || ev.Delta.Text == "" {
			return StreamEvent{}, false
		}
		return StreamEvent{Kind: StreamEventTextDelta, Delta: ev.Delta.Text}, true

	case "message_delta":
		stop := ""
		if ev.Delta != nil {
			stop = ev.Delta.StopReason
		}
		return StreamEvent{Kind: StreamEventMessageStop, StopReason: stop}, true

	case "content_block_start", "content_block_stop", "message_stop":
		// Boundary markers — not needed for the OpenAI mappings we
		// emit. message_stop arrives after message_delta with no extra
		// payload; the formatter has already handled the terminator
		// once it sees Result.
		return StreamEvent{}, false

	default:
		return StreamEvent{}, false
	}
}
