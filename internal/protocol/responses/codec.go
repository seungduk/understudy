// Package responses is the OpenAI Responses protocol, as Codex (and pi's
// openai-responses provider) use it: input items (messages, reasoning,
// function and custom tool calls and their outputs), the typed SSE events
// (response.created ... response.completed) with sequence numbers, reasoning
// items with encrypted content, response.failed for in-stream errors, and
// OpenAI's error bodies. It translates to and from core only.
package responses

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

// Codec is the Responses protocol.
type Codec struct{}

// New is the codec.
func New() protocol.Codec { return Codec{} }

// ID is protocol.OpenAIResponses.
func (Codec) ID() protocol.ID { return protocol.OpenAIResponses }

// Routes are the endpoints under /v1 and at the root.
func (Codec) Routes() []string {
	return []string{"POST /v1/responses", "POST /responses", "GET /v1/models", "GET /v1/models/{id}", "GET /models", "GET /models/{id}"}
}

// Accepts takes a shared route only when the client is Codex (the chat codec takes the rest).
func (Codec) Accepts(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Originator"), "codex") || strings.HasPrefix(r.Header.Get("User-Agent"), "codex")
}

type rawItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
	Summary   []struct {
		Text string `json:"text"`
	} `json:"summary"`
	EncryptedContent string `json:"encrypted_content"`
}

type rawRequest struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	Instructions string          `json:"instructions"`
	Input        json.RawMessage `json:"input"`
	Tools        []struct {
		Type string `json:"type"`
		Name string `json:"name"`
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
		return core.Request{}, fmt.Errorf("the body is not a JSON responses request: %v", err)
	}
	if raw.Model == "" {
		return core.Request{}, errors.New("Missing required parameter: 'model'.")
	}
	req := core.Request{Model: raw.Model, Stream: raw.Stream}
	for _, t := range raw.Tools {
		switch t.Type {
		case "function":
			req.Tools = append(req.Tools, core.Tool{Name: t.Name})
		case "custom":
			req.Tools = append(req.Tools, core.Tool{Name: t.Name, Kind: core.Freeform})
		default:
			name := t.Name
			if name == "" {
				name = t.Type
			}
			req.Tools = append(req.Tools, core.Tool{Name: name})
		}
	}
	var items []rawItem
	var s string
	switch {
	case len(raw.Input) == 0 || string(raw.Input) == "null":
		return core.Request{}, errors.New("Missing required parameter: 'input'.")
	case json.Unmarshal(raw.Input, &s) == nil:
		items = []rawItem{{Type: "message", Role: "user", Content: raw.Input}}
	default:
		if err := json.Unmarshal(raw.Input, &items); err != nil {
			return core.Request{}, errors.New("input: expected a string or a list of items")
		}
	}
	sys := []string{}
	if raw.Instructions != "" {
		sys = append(sys, raw.Instructions)
	}
	leading := true
	add := func(role core.Role, p core.Part) {
		n := len(req.Turns)
		if n > 0 && req.Turns[n-1].Role == role && (role == core.Assistant || p.Kind == core.ToolResult && req.Turns[n-1].Parts[0].Kind == core.ToolResult) {
			req.Turns[n-1].Parts = append(req.Turns[n-1].Parts, p)
			return
		}
		req.Turns = append(req.Turns, core.Turn{Role: role, Parts: []core.Part{p}})
	}
	for i, it := range items {
		typ := it.Type
		if typ == "" && it.Role != "" {
			typ = "message"
		}
		switch typ {
		case "message":
			text, err := contentText(it.Content)
			if err != nil {
				return core.Request{}, fmt.Errorf("input[%d].content: %v", i, err)
			}
			switch it.Role {
			case "developer", "system":
				if leading {
					sys = append(sys, text)
				} else {
					add(core.System, core.Part{Kind: core.Text, Text: text})
				}
				continue
			case "user":
				req.Turns = append(req.Turns, core.Turn{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: text}}})
			case "assistant":
				add(core.Assistant, core.Part{Kind: core.Text, Text: text})
			default:
				return core.Request{}, fmt.Errorf("input[%d].role: %q is not one of developer, system, user, assistant", i, it.Role)
			}
		case "reasoning":
			var parts []string
			for _, p := range it.Summary {
				parts = append(parts, p.Text)
			}
			add(core.Assistant, core.Part{Kind: core.Thinking, Text: strings.Join(parts, "\n")})
		case "function_call":
			in := json.RawMessage(it.Arguments)
			if !json.Valid(in) {
				in, _ = json.Marshal(it.Arguments)
			}
			add(core.Assistant, core.Part{Kind: core.ToolCall, CallID: it.CallID, Tool: it.Name, Input: in})
		case "custom_tool_call":
			add(core.Assistant, core.Part{Kind: core.ToolCall, CallID: it.CallID, Tool: it.Name, ToolKind: core.Freeform, Text: it.Input})
		case "function_call_output", "custom_tool_call_output":
			text, err := contentText(it.Output)
			if err != nil {
				return core.Request{}, fmt.Errorf("input[%d].output: %v", i, err)
			}
			add(core.User, core.Part{Kind: core.ToolResult, CallID: it.CallID, Text: text})
		default:
			add(core.User, core.Part{Kind: core.Other})
		}
		leading = false
	}
	req.System = strings.Join(sys, "\n")
	return req, nil
}

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
		if p.Type == "input_text" || p.Type == "output_text" || p.Type == "text" || p.Type == "summary_text" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n"), nil
}

// stream builds the SSE frames with their sequence numbers.
type stream struct {
	frames [][]byte
	seq    int
}

func (s *stream) emit(typ string, fields map[string]any) {
	fields["type"] = typ
	fields["sequence_number"] = s.seq
	s.seq++
	s.frames = append(s.frames, sse.Named(typ, sse.JSON(fields)))
}

func usage(u core.Usage) map[string]any {
	return map[string]any{"input_tokens": u.Input, "input_tokens_details": map[string]any{"cached_tokens": 0},
		"output_tokens": u.Output, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": u.Input + u.Output}
}

func responseObj(id, model string, created int64, status string, output []any, u *core.Usage) map[string]any {
	obj := map[string]any{"id": id, "object": "response", "created_at": created, "status": status, "model": model,
		"output": output, "error": nil, "incomplete_details": nil, "usage": nil}
	if u != nil {
		obj["usage"] = usage(*u)
	}
	return obj
}

// items are the output items of a reply, in order: reasoning, message, calls.
func items(seed string, r core.Reply) []map[string]any {
	var out []map[string]any
	if r.Thinking != "" {
		out = append(out, map[string]any{"id": core.DeriveID("rs_", seed), "type": "reasoning",
			"summary": []any{map[string]any{"type": "summary_text", "text": r.Thinking}}, "encrypted_content": r.ThinkingSignature})
	}
	if r.Text != "" || len(r.Calls) == 0 {
		out = append(out, map[string]any{"id": core.DeriveID("msg_", seed), "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": r.Text, "annotations": []any{}}}})
	}
	for _, c := range r.Calls {
		if c.Kind == core.Freeform {
			out = append(out, map[string]any{"id": "ctc_" + c.ID, "type": "custom_tool_call", "status": "completed", "name": c.Tool, "call_id": "call_" + c.ID, "input": c.Raw})
			continue
		}
		args := string(c.Input)
		if args == "" {
			args = "{}"
		}
		out = append(out, map[string]any{"id": "fc_" + c.ID, "type": "function_call", "status": "completed", "name": c.Tool, "call_id": "call_" + c.ID, "arguments": args})
	}
	return out
}

func with(m map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(m)+len(kv)/2)
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// streamReply is the event sequence of a reply, item by item, as the API sends it.
func streamReply(st *stream, id, model string, created int64, its []map[string]any, stop core.Stop, u core.Usage) {
	st.emit("response.created", map[string]any{"response": responseObj(id, model, created, "in_progress", []any{}, nil)})
	st.emit("response.in_progress", map[string]any{"response": responseObj(id, model, created, "in_progress", []any{}, nil)})
	var done []any
	for i, it := range its {
		iid := it["id"]
		switch it["type"] {
		case "reasoning":
			text := it["summary"].([]any)[0].(map[string]any)["text"].(string)
			st.emit("response.output_item.added", map[string]any{"output_index": i, "item": with(it, "summary", []any{})})
			st.emit("response.reasoning_summary_part.added", map[string]any{"item_id": iid, "output_index": i, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}})
			for _, c := range sse.SplitText(text, sse.TextChunk) {
				st.emit("response.reasoning_summary_text.delta", map[string]any{"item_id": iid, "output_index": i, "summary_index": 0, "delta": c})
			}
			st.emit("response.reasoning_summary_text.done", map[string]any{"item_id": iid, "output_index": i, "summary_index": 0, "text": text})
			st.emit("response.reasoning_summary_part.done", map[string]any{"item_id": iid, "output_index": i, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": text}})
		case "message":
			text := it["content"].([]any)[0].(map[string]any)["text"].(string)
			st.emit("response.output_item.added", map[string]any{"output_index": i, "item": with(it, "status", "in_progress", "content", []any{})})
			st.emit("response.content_part.added", map[string]any{"item_id": iid, "output_index": i, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			for _, c := range sse.SplitText(text, sse.TextChunk) {
				st.emit("response.output_text.delta", map[string]any{"item_id": iid, "output_index": i, "content_index": 0, "delta": c, "logprobs": []any{}})
			}
			st.emit("response.output_text.done", map[string]any{"item_id": iid, "output_index": i, "content_index": 0, "text": text, "logprobs": []any{}})
			st.emit("response.content_part.done", map[string]any{"item_id": iid, "output_index": i, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}}})
		case "function_call":
			args := it["arguments"].(string)
			st.emit("response.output_item.added", map[string]any{"output_index": i, "item": with(it, "status", "in_progress", "arguments", "")})
			for _, c := range sse.SplitBytes(args, sse.JSONChunk) {
				st.emit("response.function_call_arguments.delta", map[string]any{"item_id": iid, "output_index": i, "delta": c})
			}
			st.emit("response.function_call_arguments.done", map[string]any{"item_id": iid, "output_index": i, "arguments": args})
		case "custom_tool_call":
			in := it["input"].(string)
			st.emit("response.output_item.added", map[string]any{"output_index": i, "item": with(it, "status", "in_progress", "input", "")})
			for _, c := range sse.SplitBytes(in, sse.JSONChunk) {
				st.emit("response.custom_tool_call_input.delta", map[string]any{"item_id": iid, "call_id": it["call_id"], "output_index": i, "delta": c})
			}
			st.emit("response.custom_tool_call_input.done", map[string]any{"item_id": iid, "call_id": it["call_id"], "output_index": i, "input": in})
		}
		st.emit("response.output_item.done", map[string]any{"output_index": i, "item": it})
		done = append(done, it)
	}
	if stop == core.MaxTokens {
		obj := responseObj(id, model, created, "incomplete", done, &u)
		obj["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		st.emit("response.incomplete", map[string]any{"response": obj})
		return
	}
	st.emit("response.completed", map[string]any{"response": responseObj(id, model, created, "completed", done, &u)})
}

// failCode is the error.code a response.failed carries for a kind.
func failCode(k core.ErrorKind) string {
	if c := openai.Code(k); c != "" {
		return c
	}
	return openai.Type(k)
}

// Encode makes the response of one outcome.
func (Codec) Encode(m protocol.Meta, o protocol.Out) protocol.Encoded {
	h := http.Header{}
	if m.RequestID != "" {
		h.Set("X-Request-Id", m.RequestID)
	}
	id := core.DeriveID("resp_", m.Seed)
	created := m.Now.Unix()
	switch o.Kind {
	case protocol.OutReply:
		its := items(m.Seed, o.Reply)
		if m.Stream {
			st := &stream{}
			streamReply(st, id, m.Model, created, its, o.Reply.Stop, m.Usage)
			return protocol.Encoded{Status: 200, Header: h, Frames: st.frames}
		}
		out := make([]any, len(its))
		for i, it := range its {
			out[i] = it
		}
		status := "completed"
		if o.Reply.Stop == core.MaxTokens {
			status = "incomplete"
		}
		obj := responseObj(id, m.Model, created, status, out, &m.Usage)
		if status == "incomplete" {
			obj["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		}
		return jsonOut(200, h, sse.JSON(obj))
	case protocol.OutError:
		openai.RetryHeaders(h, o.Error.Retry, o.Error.RetryAfter, o.Error.HasRetryAfter)
		return jsonOut(openai.Status(o.Error.Kind), h, openai.ErrorBody(o.Error.Kind, o.Error.Message))
	case protocol.OutStreamError:
		if !m.Stream {
			return jsonOut(openai.Status(o.Error.Kind), h, openai.ErrorBody(o.Error.Kind, o.Error.Message))
		}
		st := &stream{}
		st.emit("response.created", map[string]any{"response": responseObj(id, m.Model, created, "in_progress", []any{}, nil)})
		st.emit("response.in_progress", map[string]any{"response": responseObj(id, m.Model, created, "in_progress", []any{}, nil)})
		if o.Partial != "" {
			iid := core.DeriveID("msg_", m.Seed)
			st.emit("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": iid, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}})
			st.emit("response.content_part.added", map[string]any{"item_id": iid, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			for _, c := range sse.SplitText(o.Partial, sse.TextChunk) {
				st.emit("response.output_text.delta", map[string]any{"item_id": iid, "output_index": 0, "content_index": 0, "delta": c, "logprobs": []any{}})
			}
		}
		obj := responseObj(id, m.Model, created, "failed", []any{}, nil)
		obj["error"] = map[string]any{"code": failCode(o.Error.Kind), "message": openai.Message(o.Error.Kind, o.Error.Message)}
		st.emit("response.failed", map[string]any{"response": obj})
		return protocol.Encoded{Status: 200, Header: h, Frames: st.frames}
	case protocol.OutLimit:
		stc, lh, body := openai.Limit(o.Limit.Window, o.Limit.Now, o.Limit.Resets)
		for k, v := range lh {
			h[k] = v
		}
		return jsonOut(stc, h, body)
	case protocol.OutModels:
		return jsonOut(200, h, openai.Models(o.Models, m.Op == protocol.GetModel))
	case protocol.OutTokens:
		return jsonOut(200, h, sse.JSON(map[string]int{"input_tokens": o.Tokens}))
	case protocol.OutProbe:
		return protocol.Encoded{Status: 200, Header: h, Body: []byte{}}
	}
	panic("responses: unknown OutKind")
}

func jsonOut(status int, h http.Header, body []byte) protocol.Encoded {
	h.Set("Content-Type", "application/json")
	return protocol.Encoded{Status: status, Header: h, Body: body}
}

type outItem struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
	Content   []struct {
		Type, Text string
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
	EncryptedContent string `json:"encrypted_content"`
}

type outResponse struct {
	Status            string    `json:"status"`
	Output            []outItem `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code, Message string
	} `json:"error"`
}

func replyOf(r outResponse) (core.Reply, bool) {
	rep := core.Reply{Stop: core.EndTurn}
	var thinking, sig, text []string
	for _, it := range r.Output {
		switch it.Type {
		case "reasoning":
			for _, s := range it.Summary {
				thinking = append(thinking, s.Text)
			}
			sig = append(sig, it.EncryptedContent)
		case "message":
			for _, c := range it.Content {
				text = append(text, c.Text)
			}
		case "function_call":
			if !json.Valid([]byte(it.Arguments)) {
				return core.Reply{}, false
			}
			rep.Calls = append(rep.Calls, core.Call{ID: it.CallID, Tool: it.Name, Input: json.RawMessage(it.Arguments)})
		case "custom_tool_call":
			rep.Calls = append(rep.Calls, core.Call{ID: it.CallID, Tool: it.Name, Kind: core.Freeform, Raw: it.Input})
		}
	}
	rep.Thinking, rep.ThinkingSignature, rep.Text = strings.Join(thinking, "\n"), strings.Join(sig, ""), strings.Join(text, "\n")
	if len(rep.Calls) > 0 {
		rep.Stop = core.ToolUse
	}
	if r.Status == "incomplete" {
		rep.Stop = core.MaxTokens
	}
	return rep, true
}

func kindOfCode(code string) core.ErrorKind {
	for _, k := range core.ErrorKinds {
		if failCode(k) == code {
			return k
		}
	}
	return core.APIError
}

// Observe reads back a Responses response, for recording.
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
	if !strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") {
		var r outResponse
		if json.Unmarshal(body, &r) != nil {
			return protocol.Observed{}, false
		}
		rep, ok := replyOf(r)
		return protocol.Observed{Kind: protocol.SawReply, Reply: rep}, ok
	}
	var partial strings.Builder
	for _, line := range sse.DataLines(body) {
		var e struct {
			Type     string      `json:"type"`
			Delta    string      `json:"delta"`
			Response outResponse `json:"response"`
			Code     string      `json:"code"`
			Message  string      `json:"message"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Type {
		case "response.output_text.delta":
			partial.WriteString(e.Delta)
		case "response.completed", "response.incomplete":
			rep, ok := replyOf(e.Response)
			return protocol.Observed{Kind: protocol.SawReply, Reply: rep}, ok
		case "response.failed":
			o := protocol.Observed{Kind: protocol.SawStreamError, Error: core.APIError, Reply: core.Reply{Text: partial.String()}}
			if e.Response.Error != nil {
				o.Error, o.Message = kindOfCode(e.Response.Error.Code), e.Response.Error.Message
			}
			return o, true
		case "error":
			return protocol.Observed{Kind: protocol.SawStreamError, Error: kindOfCode(e.Code), Message: e.Message, Reply: core.Reply{Text: partial.String()}}, true
		}
	}
	return protocol.Observed{}, false
}
