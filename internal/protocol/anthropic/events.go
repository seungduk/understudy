package anthropic

import (
	"encoding/json"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/sse"
)

// event is one SSE frame before encoding.
type event struct {
	name string
	data []byte
}

func (e event) encode() []byte { return sse.Named(e.name, e.data) }

func frames(evs []event) [][]byte {
	out := make([][]byte, len(evs))
	for i, e := range evs {
		out[i] = e.encode()
	}
	return out
}

type blockKind int

const (
	outText blockKind = iota + 1
	outThinking
	outToolUse
)

// outBlock is one content block of a reply.
type outBlock struct {
	kind      blockKind
	text      string          // text or thinking
	signature string          // thinking only
	id, name  string          // tool_use only
	input     json.RawMessage // tool_use only: a JSON object
}

// blocksOf lays a reply out as the API does: thinking, text, then tool uses.
func blocksOf(r core.Reply) []outBlock {
	var out []outBlock
	if r.Thinking != "" {
		out = append(out, outBlock{kind: outThinking, text: r.Thinking, signature: r.ThinkingSignature})
	}
	if r.Text != "" || len(r.Calls) == 0 {
		out = append(out, outBlock{kind: outText, text: r.Text})
	}
	for _, c := range r.Calls {
		in := c.Input
		if c.Kind == core.Freeform {
			// The Messages API has no freeform tools; a freeform call carries its text as {"input": ...}.
			in, _ = json.Marshal(map[string]string{"input": c.Raw})
		}
		if len(in) == 0 {
			in = json.RawMessage(`{}`)
		}
		out = append(out, outBlock{kind: outToolUse, id: "toolu_" + c.ID, name: c.Tool, input: in})
	}
	return out
}

func stopName(s core.Stop) string {
	switch s {
	case core.ToolUse:
		return "tool_use"
	case core.MaxTokens:
		return "max_tokens"
	}
	return "end_turn"
}

func ev(name string, v any) event { return event{name: name, data: sse.JSON(v)} }

type usageStart struct {
	InputTokens              int    `json:"input_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	ServiceTier              string `json:"service_tier"`
}

type messageObj struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Role         string     `json:"role"`
	Model        string     `json:"model"`
	Content      []any      `json:"content"`
	StopReason   *string    `json:"stop_reason"`
	StopSequence *string    `json:"stop_sequence"`
	Usage        usageStart `json:"usage"`
}

func messageStart(id, model string, u core.Usage) event {
	return ev("message_start", map[string]any{"type": "message_start", "message": messageObj{
		ID: id, Type: "message", Role: "assistant", Model: model, Content: []any{},
		Usage: usageStart{InputTokens: u.Input, OutputTokens: 1, ServiceTier: "standard"},
	}})
}

var ping = event{name: "ping", data: []byte(`{"type":"ping"}`)}

// streamEvents is the full event sequence of a streamed reply, as the real API
// sends it: message_start (full message object and usage), content_block_start,
// ping (after the first content_block_start), deltas, content_block_stop per block,
// message_delta (stop_reason and usage), message_stop.
// A tool_use block starts with input {} and streams its input as input_json_delta
// fragments, the first of them empty.
func streamEvents(id, model string, blocks []outBlock, stop core.Stop, u core.Usage) []event {
	out := []event{messageStart(id, model, u)}
	for i, b := range blocks {
		switch b.kind {
		case outText:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "text", "text": ""}}))
			for _, c := range sse.SplitText(b.text, sse.TextChunk) {
				out = append(out, delta(i, map[string]any{"type": "text_delta", "text": c}))
			}
		case outThinking:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}))
			for _, c := range sse.SplitText(b.text, sse.TextChunk) {
				out = append(out, delta(i, map[string]any{"type": "thinking_delta", "thinking": c}))
			}
			out = append(out, delta(i, map[string]any{"type": "signature_delta", "signature": b.signature}))
		case outToolUse:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": map[string]any{}}}))
			out = append(out, delta(i, map[string]any{"type": "input_json_delta", "partial_json": ""}))
			if s := string(b.input); s != "{}" {
				for _, c := range sse.SplitBytes(s, sse.JSONChunk) {
					out = append(out, delta(i, map[string]any{"type": "input_json_delta", "partial_json": c}))
				}
			}
		default:
			panic("anthropic: unknown block kind")
		}
		out = append(out, ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i}))
		if i == 0 {
			out = insertPing(out)
		}
	}
	if len(blocks) == 0 {
		out = append(out, ping)
	}
	out = append(out, ev("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stopName(stop), "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": u.Output}}))
	out = append(out, event{name: "message_stop", data: []byte(`{"type":"message_stop"}`)})
	return out
}

// insertPing puts ping right after the first content_block_start (index 1).
func insertPing(out []event) []event {
	out = append(out, event{})
	copy(out[3:], out[2:])
	out[2] = ping
	return out
}

func delta(i int, d map[string]any) event {
	return ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": d})
}

// errorEvents is an in-stream error: message_start, ping, then partial text (when
// given, as one text block that never stops), then "event: error". No message_stop follows.
func errorEvents(id, model string, u core.Usage, partial string, k core.ErrorKind, message string) []event {
	out := []event{messageStart(id, model, u)}
	if partial != "" {
		out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}}), ping)
		for _, c := range sse.SplitText(partial, sse.TextChunk) {
			out = append(out, delta(0, map[string]any{"type": "text_delta", "text": c}))
		}
	} else {
		out = append(out, ping)
	}
	return append(out, event{name: "error", data: errorEventData(k, message)})
}

// messageJSON is a non-streamed reply body.
func messageJSON(id, model string, blocks []outBlock, stop core.Stop, u core.Usage) []byte {
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.kind {
		case outText:
			content = append(content, map[string]any{"type": "text", "text": b.text})
		case outThinking:
			content = append(content, map[string]any{"type": "thinking", "thinking": b.text, "signature": b.signature})
		case outToolUse:
			content = append(content, map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": b.input})
		}
	}
	obj := map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": stopName(stop), "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": u.Input, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			"output_tokens": u.Output, "service_tier": "standard"}}
	return sse.JSON(obj)
}
