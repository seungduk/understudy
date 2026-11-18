// Package openaichat is the OpenAI Chat Completions protocol, as opencode, pi
// and every OpenAI-compatible client (DeepSeek's API among them) use it:
// messages with tool_calls and tool results, the chat.completion.chunk stream
// with tool_calls deltas, reasoning_content for thinking (DeepSeek's field),
// and OpenAI's error bodies. It translates to and from core only.
package openaichat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/openai"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

// Codec is the Chat Completions protocol.
type Codec struct{}

// New is the codec.
func New() protocol.Codec { return Codec{} }

// ID is protocol.OpenAIChat.
func (Codec) ID() protocol.ID { return protocol.OpenAIChat }

// Routes are the endpoints under /v1 and at the root (DeepSeek's base URL has no /v1).
func (Codec) Routes() []string {
	return []string{"POST /v1/chat/completions", "POST /chat/completions", "GET /v1/models", "GET /v1/models/{id}", "GET /models", "GET /models/{id}"}
}

// Accepts takes any request on a shared route (the Anthropic codec, tried first, claims its own).
func (Codec) Accepts(*http.Request) bool { return true }

type rawMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ToolCallID       string          `json:"tool_call_id"`
	ToolCalls        []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
		Custom struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		} `json:"custom"`
	} `json:"tool_calls"`
}

type rawRequest struct {
	Model    string       `json:"model"`
	Stream   bool         `json:"stream"`
	Messages []rawMessage `json:"messages"`
	Tools    []struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Custom struct {
			Name string `json:"name"`
		} `json:"custom"`
	} `json:"tools"`
}

// Decode reads a request on one of Routes.
func (Codec) Decode(r *http.Request, body []byte) (protocol.Decoded, error) {
	p := strings.TrimPrefix(r.URL.Path, "/v1")
	switch {
	case p == "/models":
		return protocol.Decoded{Op: protocol.ListModels}, nil
	case strings.HasPrefix(p, "/models/"):
		return protocol.Decoded{Op: protocol.GetModel, Model: strings.TrimPrefix(p, "/models/")}, nil
	}
	req, err := parse(body)
	return protocol.Decoded{Op: protocol.Generate, Request: req}, err
}

func parse(body []byte) (core.Request, error) {
	var raw rawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return core.Request{}, fmt.Errorf("the body is not a JSON chat completions request: %v", err)
	}
	if raw.Model == "" {
		return core.Request{}, errors.New("you must provide a model parameter")
	}
	if len(raw.Messages) == 0 {
		return core.Request{}, errors.New("messages: at least one message is required")
	}
	req := core.Request{Model: raw.Model, Stream: raw.Stream}
	for _, t := range raw.Tools {
		if t.Type == "custom" {
			req.Tools = append(req.Tools, core.Tool{Name: t.Custom.Name, Kind: core.Freeform})
		} else {
			req.Tools = append(req.Tools, core.Tool{Name: t.Function.Name})
		}
	}
	var sys []string
	leading := true
	for i, m := range raw.Messages {
		text, err := contentText(m.Content)
		if err != nil {
			return core.Request{}, fmt.Errorf("messages[%d].content: %v", i, err)
		}
		switch m.Role {
		case "system", "developer":
			if leading {
				sys = append(sys, text)
			} else {
				req.Turns = append(req.Turns, core.Turn{Role: core.System, Parts: []core.Part{{Kind: core.Text, Text: text}}})
			}
			continue
		case "user":
			req.Turns = append(req.Turns, core.Turn{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: text}}})
		case "assistant":
			t := core.Turn{Role: core.Assistant}
			if th := m.ReasoningContent + m.Reasoning; th != "" {
				t.Parts = append(t.Parts, core.Part{Kind: core.Thinking, Text: th})
			}
			if text != "" {
				t.Parts = append(t.Parts, core.Part{Kind: core.Text, Text: text})
			}
			for _, tc := range m.ToolCalls {
				if tc.Type == "custom" {
					t.Parts = append(t.Parts, core.Part{Kind: core.ToolCall, CallID: tc.ID, Tool: tc.Custom.Name, ToolKind: core.Freeform, Text: tc.Custom.Input})
					continue
				}
				in := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(in) {
					in, _ = json.Marshal(tc.Function.Arguments)
				}
				t.Parts = append(t.Parts, core.Part{Kind: core.ToolCall, CallID: tc.ID, Tool: tc.Function.Name, Input: in})
			}
			req.Turns = append(req.Turns, t)
		case "tool":
			part := core.Part{Kind: core.ToolResult, CallID: m.ToolCallID, Text: text}
			if n := len(req.Turns); n > 0 && req.Turns[n-1].Role == core.User && len(req.Turns[n-1].Parts) > 0 && req.Turns[n-1].Parts[0].Kind == core.ToolResult {
				req.Turns[n-1].Parts = append(req.Turns[n-1].Parts, part)
			} else {
				req.Turns = append(req.Turns, core.Turn{Role: core.User, Parts: []core.Part{part}})
			}
		default:
			return core.Request{}, fmt.Errorf("messages[%d].role: %q is not one of system, developer, user, assistant, tool", i, m.Role)
		}
		leading = false
	}
	req.System = strings.Join(sys, "\n")
	return req, nil
}

// contentText reads a string, null, or a list of parts, joining the text parts.
func contentText(c json.RawMessage) (string, error) {
	if len(c) == 0 || string(c) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(c, &s); err == nil {
		return s, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c, &parts); err != nil {
		return "", errors.New("expected a string or a list of content parts")
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" || p.Type == "input_text" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n"), nil
}

func finish(s core.Stop) string {
	switch s {
	case core.ToolUse:
		return "tool_calls"
	case core.MaxTokens:
		return "length"
	}
	return "stop"
}

func usage(u core.Usage) map[string]any {
	return map[string]any{"prompt_tokens": u.Input, "completion_tokens": u.Output, "total_tokens": u.Input + u.Output}
}

func toolCall(c core.Call) map[string]any {
	if c.Kind == core.Freeform {
		return map[string]any{"id": "call_" + c.ID, "type": "custom", "custom": map[string]any{"name": c.Tool, "input": c.Raw}}
	}
	args := string(c.Input)
	if args == "" {
		args = "{}"
	}
	return map[string]any{"id": "call_" + c.ID, "type": "function", "function": map[string]any{"name": c.Tool, "arguments": args}}
}

// Encode makes the response of one outcome.
func (Codec) Encode(m protocol.Meta, o protocol.Out) protocol.Encoded {
	h := http.Header{}
	if m.RequestID != "" {
		h.Set("X-Request-Id", m.RequestID)
	}
	id := core.DeriveID("chatcmpl-", m.Seed)
	created := m.Now.Unix()
	switch o.Kind {
	case protocol.OutReply:
		if m.Stream {
			return protocol.Encoded{Status: 200, Header: h, Frames: streamFrames(id, m.Model, created, o.Reply, m.Usage)}
		}
		msg := map[string]any{"role": "assistant", "content": nil}
		if o.Reply.Text != "" || len(o.Reply.Calls) == 0 {
			msg["content"] = o.Reply.Text
		}
		if o.Reply.Thinking != "" {
			msg["reasoning_content"] = o.Reply.Thinking
		}
		if len(o.Reply.Calls) > 0 {
			calls := make([]any, len(o.Reply.Calls))
			for i, c := range o.Reply.Calls {
				calls[i] = toolCall(c)
			}
			msg["tool_calls"] = calls
		}
		return jsonOut(200, h, sse.JSON(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": m.Model,
			"choices": []any{map[string]any{"index": 0, "message": msg, "logprobs": nil, "finish_reason": finish(o.Reply.Stop)}},
			"usage":   usage(m.Usage)}))
	case protocol.OutError:
		openai.RetryHeaders(h, o.Error.Retry, o.Error.RetryAfter, o.Error.HasRetryAfter)
		return jsonOut(openai.Status(o.Error.Kind), h, openai.ErrorBody(o.Error.Kind, o.Error.Message))
	case protocol.OutStreamError:
		if !m.Stream {
			return jsonOut(openai.Status(o.Error.Kind), h, openai.ErrorBody(o.Error.Kind, o.Error.Message))
		}
		frames := [][]byte{chunk(id, m.Model, created, map[string]any{"role": "assistant", "content": ""}, nil, nil)}
		for _, c := range sse.SplitText(o.Partial, sse.TextChunk) {
			frames = append(frames, chunk(id, m.Model, created, map[string]any{"content": c}, nil, nil))
		}
		frames = append(frames, sse.Data(sse.JSON(map[string]any{"error": openai.ErrorObject(o.Error.Kind, o.Error.Message)})))
		return protocol.Encoded{Status: 200, Header: h, Frames: frames}
	case protocol.OutLimit:
		st, lh, body := openai.Limit(o.Limit.Window, o.Limit.Now, o.Limit.Resets)
		for k, v := range lh {
			h[k] = v
		}
		return jsonOut(st, h, body)
	case protocol.OutModels:
		return jsonOut(200, h, openai.Models(o.Models, m.Op == protocol.GetModel))
	case protocol.OutTokens:
		return jsonOut(200, h, sse.JSON(map[string]int{"input_tokens": o.Tokens}))
	case protocol.OutProbe:
		return protocol.Encoded{Status: 200, Header: h, Body: []byte{}}
	}
	panic("openaichat: unknown OutKind")
}

func jsonOut(status int, h http.Header, body []byte) protocol.Encoded {
	h.Set("Content-Type", "application/json")
	return protocol.Encoded{Status: status, Header: h, Body: body}
}

func chunk(id, model string, created int64, delta map[string]any, finishReason any, u map[string]any) []byte {
	obj := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"system_fingerprint": "fp_understudy",
		"choices":            []any{map[string]any{"index": 0, "delta": delta, "logprobs": nil, "finish_reason": finishReason}}}
	if u != nil {
		obj["usage"] = u
	}
	return sse.Data(sse.JSON(obj))
}

// streamFrames is a streamed reply: a role chunk, reasoning_content deltas,
// content deltas, each tool call as a first delta (id, type, name, empty
// arguments) and argument fragments, a last chunk with finish_reason and usage,
// then data: [DONE].
func streamFrames(id, model string, created int64, r core.Reply, u core.Usage) [][]byte {
	out := [][]byte{chunk(id, model, created, map[string]any{"role": "assistant", "content": ""}, nil, nil)}
	for _, c := range sse.SplitText(r.Thinking, sse.TextChunk) {
		out = append(out, chunk(id, model, created, map[string]any{"reasoning_content": c}, nil, nil))
	}
	for _, c := range sse.SplitText(r.Text, sse.TextChunk) {
		out = append(out, chunk(id, model, created, map[string]any{"content": c}, nil, nil))
	}
	for i, c := range r.Calls {
		if c.Kind == core.Freeform {
			first := map[string]any{"index": i, "id": "call_" + c.ID, "type": "custom", "custom": map[string]any{"name": c.Tool, "input": ""}}
			out = append(out, chunk(id, model, created, map[string]any{"tool_calls": []any{first}}, nil, nil))
			for _, f := range sse.SplitBytes(c.Raw, sse.JSONChunk) {
				out = append(out, chunk(id, model, created, map[string]any{"tool_calls": []any{map[string]any{"index": i, "custom": map[string]any{"input": f}}}}, nil, nil))
			}
			continue
		}
		first := map[string]any{"index": i, "id": "call_" + c.ID, "type": "function", "function": map[string]any{"name": c.Tool, "arguments": ""}}
		out = append(out, chunk(id, model, created, map[string]any{"tool_calls": []any{first}}, nil, nil))
		args := string(c.Input)
		if args == "" {
			args = "{}"
		}
		for _, f := range sse.SplitBytes(args, sse.JSONChunk) {
			out = append(out, chunk(id, model, created, map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": f}}}}, nil, nil))
		}
	}
	out = append(out, chunk(id, model, created, map[string]any{}, finish(r.Stop), usage(u)))
	return append(out, sse.Data([]byte("[DONE]")))
}

// Observe reads back a chat completions response, for recording.
func (Codec) Observe(op protocol.Op, status int, h http.Header, body []byte) (protocol.Observed, bool) {
	if op != protocol.Generate {
		return protocol.Observed{}, false
	}
	if status != http.StatusOK {
		if w, in, ok := openai.ObserveLimit(status, h, body); ok {
			return protocol.Observed{Kind: protocol.SawLimit, Window: w, ResetsIn: in}, true
		}
		k, msg, ok := openai.KindOf(status, body)
		if !ok {
			return protocol.Observed{}, false
		}
		return protocol.Observed{Kind: protocol.SawError, Error: k, Message: msg}, true
	}
	if strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") {
		return observeStream(body)
	}
	var m struct {
		Choices []struct {
			Message      rawMessage `json:"message"`
			FinishReason string     `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &m) != nil || len(m.Choices) == 0 {
		return protocol.Observed{}, false
	}
	c := m.Choices[0]
	text, _ := contentText(c.Message.Content)
	r := core.Reply{Thinking: c.Message.ReasoningContent + c.Message.Reasoning, Text: text, Stop: stopOf(c.FinishReason)}
	for _, tc := range c.Message.ToolCalls {
		if tc.Type == "custom" {
			r.Calls = append(r.Calls, core.Call{ID: tc.ID, Tool: tc.Custom.Name, Kind: core.Freeform, Raw: tc.Custom.Input})
			continue
		}
		if !json.Valid([]byte(tc.Function.Arguments)) {
			return protocol.Observed{}, false
		}
		r.Calls = append(r.Calls, core.Call{ID: tc.ID, Tool: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
	}
	return protocol.Observed{Kind: protocol.SawReply, Reply: r}, true
}

func stopOf(s string) core.Stop {
	switch s {
	case "tool_calls":
		return core.ToolUse
	case "length":
		return core.MaxTokens
	}
	return core.EndTurn
}

func observeStream(raw []byte) (protocol.Observed, bool) {
	type acc struct {
		id, name string
		custom   bool
		args     strings.Builder
	}
	var calls []*acc
	byIndex := map[int]*acc{}
	var thinking, text strings.Builder
	stop := core.EndTurn
	for _, line := range sse.DataLines(raw) {
		if line == "[DONE]" {
			break
		}
		var c struct {
			Error *struct {
				Message, Type string
				Code          any
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name, Arguments string
						} `json:"function"`
						Custom struct {
							Name, Input string
						} `json:"custom"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		if c.Error != nil {
			code, _ := c.Error.Code.(string)
			k, ok := openai.KindOfError(c.Error.Type, code)
			if !ok {
				k = core.APIError
			}
			return protocol.Observed{Kind: protocol.SawStreamError, Error: k, Message: c.Error.Message, Reply: core.Reply{Text: text.String()}}, true
		}
		for _, ch := range c.Choices {
			thinking.WriteString(ch.Delta.ReasoningContent + ch.Delta.Reasoning)
			text.WriteString(ch.Delta.Content)
			for _, tc := range ch.Delta.ToolCalls {
				a := byIndex[tc.Index]
				if a == nil {
					a = &acc{}
					byIndex[tc.Index] = a
					calls = append(calls, a)
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Type == "custom" {
					a.custom = true
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				if tc.Custom.Name != "" {
					a.name = tc.Custom.Name
				}
				a.args.WriteString(tc.Function.Arguments + tc.Custom.Input)
			}
			if ch.FinishReason != nil {
				stop = stopOf(*ch.FinishReason)
			}
		}
	}
	r := core.Reply{Thinking: thinking.String(), Text: text.String(), Stop: stop}
	for _, a := range calls {
		if a.custom {
			r.Calls = append(r.Calls, core.Call{ID: a.id, Tool: a.name, Kind: core.Freeform, Raw: a.args.String()})
			continue
		}
		if !json.Valid([]byte(a.args.String())) {
			return protocol.Observed{}, false
		}
		r.Calls = append(r.Calls, core.Call{ID: a.id, Tool: a.name, Input: json.RawMessage(a.args.String())})
	}
	return protocol.Observed{Kind: protocol.SawReply, Reply: r}, true
}
