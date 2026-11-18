package wire

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// OutKind is the closed set of content blocks this server sends.
type OutKind int

const (
	OutText OutKind = iota + 1
	OutThinking
	OutToolUse
)

// OutBlock is one content block of a reply.
type OutBlock struct {
	Kind      OutKind
	Text      string          // text or thinking
	Signature string          // thinking only
	ID        string          // tool_use only
	Name      string          // tool_use only
	Input     json.RawMessage // tool_use only: a JSON object
}

// StopReason is the closed set of stop reasons this server sends.
type StopReason int

const (
	EndTurn StopReason = iota + 1
	ToolUse
	MaxTokens
)

func (s StopReason) wire() string {
	switch s {
	case EndTurn:
		return "end_turn"
	case ToolUse:
		return "tool_use"
	case MaxTokens:
		return "max_tokens"
	}
	panic("wire: unknown StopReason")
}

// Usage is the token accounting of one reply.
type Usage struct {
	Input  int
	Output int
}

// Meta names one reply.
type Meta struct {
	ID    string // msg_...
	Model string
}

// Event is one SSE frame.
type Event struct {
	Name string
	Data []byte
}

// Encode is the frame as it goes on the wire: "event: <name>\ndata: <json>\n\n".
func (e Event) Encode() []byte {
	b := make([]byte, 0, len(e.Name)+len(e.Data)+16)
	b = append(b, "event: "...)
	b = append(b, e.Name...)
	b = append(b, "\ndata: "...)
	b = append(b, e.Data...)
	b = append(b, "\n\n"...)
	return b
}

// Chunk sizes: text and thinking deltas carry up to textChunk bytes, ending on a
// word boundary where one exists; tool input fragments carry up to jsonChunk bytes.
const (
	textChunk = 24
	jsonChunk = 16
)

func ev(name string, v any) Event {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return Event{Name: name, Data: b}
}

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

func messageStart(m Meta, u Usage) Event {
	return ev("message_start", map[string]any{"type": "message_start", "message": messageObj{
		ID: m.ID, Type: "message", Role: "assistant", Model: m.Model, Content: []any{},
		Usage: usageStart{InputTokens: u.Input, OutputTokens: 1, ServiceTier: "standard"},
	}})
}

var ping = Event{Name: "ping", Data: []byte(`{"type":"ping"}`)}

// StreamEvents is the full event sequence of a streamed reply, as the real API
// sends it: message_start (full message object and usage), content_block_start,
// ping (after the first content_block_start), deltas, content_block_stop per block,
// message_delta (stop_reason and usage), message_stop.
// A tool_use block starts with input {} and streams its input as input_json_delta
// fragments, the first of them empty.
func StreamEvents(m Meta, blocks []OutBlock, stop StopReason, u Usage) []Event {
	out := []Event{messageStart(m, u)}
	for i, b := range blocks {
		switch b.Kind {
		case OutText:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "text", "text": ""}}))
			for _, c := range SplitText(b.Text, textChunk) {
				out = append(out, delta(i, map[string]any{"type": "text_delta", "text": c}))
			}
		case OutThinking:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}))
			for _, c := range SplitText(b.Text, textChunk) {
				out = append(out, delta(i, map[string]any{"type": "thinking_delta", "thinking": c}))
			}
			out = append(out, delta(i, map[string]any{"type": "signature_delta", "signature": b.Signature}))
		case OutToolUse:
			out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": map[string]any{}}}))
			out = append(out, delta(i, map[string]any{"type": "input_json_delta", "partial_json": ""}))
			if s := string(b.Input); s != "{}" {
				for _, c := range SplitBytes(s, jsonChunk) {
					out = append(out, delta(i, map[string]any{"type": "input_json_delta", "partial_json": c}))
				}
			}
		default:
			panic("wire: unknown OutKind")
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
		"delta": map[string]any{"stop_reason": stop.wire(), "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": u.Output}}))
	out = append(out, Event{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)})
	return out
}

// insertPing puts ping right after the first content_block_start (index 1).
func insertPing(out []Event) []Event {
	out = append(out, Event{})
	copy(out[3:], out[2:])
	out[2] = ping
	return out
}

func delta(i int, d map[string]any) Event {
	return ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": d})
}

// ErrorEvents is an in-stream error: message_start, ping, then partial text (when
// given, as one text block that never stops), then "event: error". No message_stop follows.
func ErrorEvents(m Meta, u Usage, partial string, k ErrorKind, message string) []Event {
	out := []Event{messageStart(m, u)}
	if partial != "" {
		out = append(out, ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}}), ping)
		for _, c := range SplitText(partial, textChunk) {
			out = append(out, delta(0, map[string]any{"type": "text_delta", "text": c}))
		}
	} else {
		out = append(out, ping)
	}
	return append(out, Event{Name: "error", Data: ErrorEventData(k, message)})
}

// MessageJSON is a non-streamed reply body.
func MessageJSON(m Meta, blocks []OutBlock, stop StopReason, u Usage) []byte {
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Kind {
		case OutText:
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		case OutThinking:
			content = append(content, map[string]any{"type": "thinking", "thinking": b.Text, "signature": b.Signature})
		case OutToolUse:
			content = append(content, map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": b.Input})
		}
	}
	sr := stop.wire()
	obj := map[string]any{"id": m.ID, "type": "message", "role": "assistant", "model": m.Model, "content": content,
		"stop_reason": sr, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": u.Input, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			"output_tokens": u.Output, "service_tier": "standard"}}
	b, _ := json.Marshal(obj)
	return b
}

// SplitText cuts s into pieces of at most max bytes (a longer word is cut on a
// rune boundary), preferring to end a piece after a space. Joining the pieces gives s.
func SplitText(s string, max int) []string {
	var out []string
	for len(s) > 0 {
		if len(s) <= max {
			out = append(out, s)
			break
		}
		cut := strings.LastIndexByte(s[:max], ' ') + 1
		if cut <= 0 {
			cut = runeCut(s, max)
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return out
}

// SplitBytes cuts s into pieces of at most max bytes on rune boundaries
// (a piece is longer only when one rune is longer than max).
func SplitBytes(s string, max int) []string {
	var out []string
	for len(s) > 0 {
		cut := runeCut(s, max)
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return out
}

func runeCut(s string, max int) int {
	if len(s) <= max {
		return len(s)
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		_, n := utf8.DecodeRuneInString(s)
		return n
	}
	return cut
}
