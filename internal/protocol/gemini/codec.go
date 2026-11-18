// Package gemini is the Gemini API protocol (generativelanguage v1beta), as
// Gemini CLI, Antigravity's CLI and pi's google provider use it: contents of
// user and model turns with text, thought, functionCall and functionResponse
// parts, generateContent and streamGenerateContent (?alt=sse), countTokens,
// Google's error bodies and RESOURCE_EXHAUSTED quota details. It translates
// to and from core only.
package gemini

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

// Codec is the Gemini protocol.
type Codec struct{}

// New is the codec.
func New() protocol.Codec { return Codec{} }

// ID is protocol.Gemini.
func (Codec) ID() protocol.ID { return protocol.Gemini }

// Routes are the v1beta model endpoints (every Gemini client here uses v1beta).
func (Codec) Routes() []string {
	return []string{"POST /v1beta/models/{action}", "GET /v1beta/models/{action}", "GET /v1beta/models"}
}

// Accepts takes every request on its routes (no other protocol shares them).
func (Codec) Accepts(*http.Request) bool { return true }

type rawPart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

type rawContent struct {
	Role  string    `json:"role"`
	Parts []rawPart `json:"parts"`
}

type rawRequest struct {
	Contents          []rawContent `json:"contents"`
	SystemInstruction *rawContent  `json:"systemInstruction"`
	Tools             []struct {
		FunctionDeclarations []struct {
			Name string `json:"name"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
}

// Decode reads a request on one of Routes: models/M:generateContent,
// :streamGenerateContent, :countTokens, or a model listing.
func (Codec) Decode(r *http.Request, body []byte) (protocol.Decoded, error) {
	// The action is read from the path itself, so a caller that did not route
	// the request through a ServeMux (the recording proxy) decodes it the same.
	action := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/v1beta/models"), "/")
	if r.Method == http.MethodGet {
		if action == "" {
			return protocol.Decoded{Op: protocol.ListModels}, nil
		}
		return protocol.Decoded{Op: protocol.GetModel, Model: action}, nil
	}
	model, method, ok := strings.Cut(action, ":")
	if !ok || model == "" {
		return protocol.Decoded{}, fmt.Errorf("the path %s names no model method; use models/MODEL:generateContent", r.URL.Path)
	}
	var op protocol.Op
	stream := false
	switch method {
	case "generateContent":
		op = protocol.Generate
	case "streamGenerateContent":
		op, stream = protocol.Generate, true
	case "countTokens":
		op = protocol.CountTokens
	default:
		return protocol.Decoded{}, fmt.Errorf("the method %q is not one of generateContent, streamGenerateContent, countTokens", method)
	}
	req, err := parse(body)
	if err != nil {
		return protocol.Decoded{}, err
	}
	req.Model, req.Stream = model, stream
	return protocol.Decoded{Op: op, Request: req}, nil
}

func parse(body []byte) (core.Request, error) {
	var raw rawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return core.Request{}, fmt.Errorf("Invalid JSON payload received. %v", err)
	}
	if len(raw.Contents) == 0 {
		return core.Request{}, errors.New("* GenerateContentRequest.contents: contents is not specified")
	}
	req := core.Request{}
	for _, t := range raw.Tools {
		for _, f := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, core.Tool{Name: f.Name})
		}
	}
	if raw.SystemInstruction != nil {
		var s []string
		for _, p := range raw.SystemInstruction.Parts {
			s = append(s, p.Text)
		}
		req.System = strings.Join(s, "\n")
	}
	for i, c := range raw.Contents {
		var role core.Role
		switch c.Role {
		case "user", "":
			role = core.User
		case "model":
			role = core.Assistant
		default:
			return core.Request{}, fmt.Errorf("* GenerateContentRequest.contents[%d].role: %q is not user or model", i, c.Role)
		}
		// A content of function responses is the client's side, whatever role it
		// names (Antigravity's CLI sends them as role "model").
		if len(c.Parts) > 0 {
			all := true
			for _, p := range c.Parts {
				all = all && p.FunctionResponse != nil
			}
			if all {
				role = core.User
			}
		}
		t := core.Turn{Role: role}
		for _, p := range c.Parts {
			switch {
			case p.FunctionCall != nil:
				in := p.FunctionCall.Args
				if len(in) == 0 || string(in) == "null" {
					in = json.RawMessage(`{}`)
				}
				t.Parts = append(t.Parts, core.Part{Kind: core.ToolCall, CallID: p.FunctionCall.ID, Tool: p.FunctionCall.Name, Input: in})
			case p.FunctionResponse != nil:
				text, mark := resultText(p.FunctionResponse.Response)
				t.Parts = append(t.Parts, core.Part{Kind: core.ToolResult, CallID: p.FunctionResponse.ID, Tool: p.FunctionResponse.Name, Text: text, Mark: mark})
			case p.Thought:
				t.Parts = append(t.Parts, core.Part{Kind: core.Thinking, Text: p.Text})
			case p.Text != "":
				t.Parts = append(t.Parts, core.Part{Kind: core.Text, Text: p.Text})
			default:
				t.Parts = append(t.Parts, core.Part{Kind: core.Other})
			}
		}
		req.Turns = append(req.Turns, t)
	}
	return req, nil
}

// resultText reads a functionResponse.response: its "output" (or "result")
// text, or its "error", which marks the result an error; any other object is
// its JSON.
func resultText(r json.RawMessage) (string, core.Mark) {
	var m map[string]json.RawMessage
	if json.Unmarshal(r, &m) != nil {
		return string(r), core.Unmarked
	}
	str := func(v json.RawMessage) string {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		return string(v)
	}
	if e, ok := m["error"]; ok && string(e) != "null" {
		return str(e), core.MarkedError
	}
	for _, k := range []string{"output", "result", "content"} {
		if v, ok := m[k]; ok {
			return str(v), core.Unmarked
		}
	}
	return string(r), core.Unmarked
}

type errorInfo struct {
	status  int
	code    string // error.status
	message string
}

var errorTable = map[core.ErrorKind]errorInfo{
	core.InvalidRequest:  {400, "INVALID_ARGUMENT", "Request contains an invalid argument."},
	core.Authentication:  {400, "INVALID_ARGUMENT", "API key not valid. Please pass a valid API key."},
	core.Billing:         {400, "FAILED_PRECONDITION", "Billing is not enabled for this project."},
	core.Permission:      {403, "PERMISSION_DENIED", "The caller does not have permission"},
	core.NotFound:        {404, "NOT_FOUND", "models/x is not found for API version v1beta, or is not supported for generateContent."},
	core.RequestTooLarge: {400, "INVALID_ARGUMENT", "Request payload size exceeds the limit."},
	core.RateLimit:       {429, "RESOURCE_EXHAUSTED", "Resource has been exhausted (e.g. check quota)."},
	core.APIError:        {500, "INTERNAL", "An internal error has occurred. Please retry or report in https://developers.generativeai.google/guide/troubleshooting"},
	core.Timeout:         {504, "DEADLINE_EXCEEDED", "Deadline expired before operation could complete."},
	core.Overloaded:      {503, "UNAVAILABLE", "The model is overloaded. Please try again later."},
}

func info(k core.ErrorKind) errorInfo {
	i, ok := errorTable[k]
	if !ok {
		panic(fmt.Sprintf("gemini: unknown ErrorKind %d", int(k)))
	}
	return i
}

// Status is the HTTP status of the kind.
func Status(k core.ErrorKind) int { return info(k).status }

// reason is the ErrorInfo reason some kinds carry, so a client can tell them apart.
func reason(k core.ErrorKind) string {
	switch k {
	case core.Authentication:
		return "API_KEY_INVALID"
	case core.Billing:
		return "BILLING_DISABLED"
	}
	return ""
}

func errorObject(k core.ErrorKind, message string, details []any) map[string]any {
	i := info(k)
	if message == "" {
		message = i.message
	}
	if r := reason(k); r != "" {
		details = append(details, map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": r, "domain": "googleapis.com"})
	}
	if details == nil {
		details = []any{}
	}
	return map[string]any{"code": i.status, "message": message, "status": i.code, "details": details}
}

func errorBody(k core.ErrorKind, message string, details []any) []byte {
	return sse.JSON(map[string]any{"error": errorObject(k, message, details)})
}

func retryInfo(d time.Duration) map[string]any {
	return map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": strconv.Itoa(int(d/time.Second)) + "s"}
}

// quotaID is the quota a window's limit names. Gemini CLI reads PerDay as its
// daily quota (no retry) and any other quota as retryable after RetryInfo.
func quotaID(w core.Window) string {
	switch w {
	case core.PerMinute:
		return "GenerateRequestsPerMinutePerProjectPerModel"
	case core.Daily:
		return "GenerateRequestsPerDayPerProjectPerModel"
	case core.FiveHour:
		return "GenerateRequestsPer5HoursPerProjectPerModel"
	}
	return "GenerateRequestsPerWeekPerProjectPerModel"
}

func windowOfQuota(q string) core.Window {
	for _, w := range core.Windows {
		if quotaID(w) == q {
			return w
		}
	}
	if strings.Contains(q, "PerDay") {
		return core.Daily
	}
	return core.PerMinute
}

func usage(u core.Usage) map[string]any {
	return map[string]any{"promptTokenCount": u.Input, "candidatesTokenCount": u.Output, "totalTokenCount": u.Input + u.Output}
}

func finish(s core.Stop) string {
	if s == core.MaxTokens {
		return "MAX_TOKENS"
	}
	return "STOP"
}

// parts lays a reply out: a thought part, text parts, then function calls
// (the first one carrying the thought signature, which clients send back).
func parts(r core.Reply, chunks bool) [][]any {
	var out [][]any
	sig := ""
	if r.ThinkingSignature != "" {
		// The API's thoughtSignature is bytes, so it is base64 on the wire.
		sig = base64.StdEncoding.EncodeToString([]byte(r.ThinkingSignature))
	}
	if r.Thinking != "" {
		if chunks {
			for _, c := range sse.SplitText(r.Thinking, sse.TextChunk) {
				out = append(out, []any{map[string]any{"text": c, "thought": true}})
			}
		} else {
			out = append(out, []any{map[string]any{"text": r.Thinking, "thought": true}})
		}
	}
	if r.Text != "" || len(r.Calls) == 0 {
		texts := []string{r.Text}
		if chunks {
			texts = sse.SplitText(r.Text, sse.TextChunk)
			if len(texts) == 0 {
				texts = []string{""}
			}
		}
		for i, c := range texts {
			p := map[string]any{"text": c}
			if i == 0 && len(r.Calls) == 0 && sig != "" {
				p["thoughtSignature"] = sig
			}
			out = append(out, []any{p})
		}
	}
	if len(r.Calls) > 0 {
		var calls []any
		for i, c := range r.Calls {
			args := c.Input
			if c.Kind == core.Freeform {
				args, _ = json.Marshal(map[string]string{"input": c.Raw})
			}
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			p := map[string]any{"functionCall": map[string]any{"name": c.Tool, "args": args}}
			if i == 0 && sig != "" {
				p["thoughtSignature"] = sig
			}
			calls = append(calls, p)
		}
		out = append(out, calls)
	}
	return out
}

func candidate(ps []any, finishReason string) map[string]any {
	c := map[string]any{"content": map[string]any{"parts": ps, "role": "model"}, "index": 0}
	if finishReason != "" {
		c["finishReason"] = finishReason
	}
	return c
}

// Encode makes the response of one outcome.
func (Codec) Encode(m protocol.Meta, o protocol.Out) protocol.Encoded {
	h := http.Header{}
	if m.RequestID != "" {
		h.Set("X-Request-Id", m.RequestID)
	}
	id := core.DeriveID("", m.Seed)
	switch o.Kind {
	case protocol.OutReply:
		if !m.Stream {
			var all []any
			for _, ps := range parts(o.Reply, false) {
				all = append(all, ps...)
			}
			return jsonOut(200, h, sse.JSON(map[string]any{"candidates": []any{candidate(all, finish(o.Reply.Stop))},
				"usageMetadata": usage(m.Usage), "modelVersion": m.Model, "responseId": id}))
		}
		chunks := parts(o.Reply, true)
		var frames [][]byte
		for i, ps := range chunks {
			fr := ""
			if i == len(chunks)-1 {
				fr = finish(o.Reply.Stop)
			}
			frames = append(frames, sse.Data(sse.JSON(map[string]any{"candidates": []any{candidate(ps, fr)},
				"usageMetadata": usage(m.Usage), "modelVersion": m.Model, "responseId": id})))
		}
		return protocol.Encoded{Status: 200, Header: h, Frames: frames}
	case protocol.OutError:
		var details []any
		if o.Error.HasRetryAfter {
			details = append(details, retryInfo(o.Error.RetryAfter))
		}
		return jsonOut(Status(o.Error.Kind), h, errorBody(o.Error.Kind, o.Error.Message, details))
	case protocol.OutStreamError:
		if !m.Stream {
			return jsonOut(Status(o.Error.Kind), h, errorBody(o.Error.Kind, o.Error.Message, nil))
		}
		var frames [][]byte
		for _, c := range sse.SplitText(o.Partial, sse.TextChunk) {
			frames = append(frames, sse.Data(sse.JSON(map[string]any{"candidates": []any{candidate([]any{map[string]any{"text": c}}, "")},
				"usageMetadata": usage(m.Usage), "modelVersion": m.Model, "responseId": id})))
		}
		frames = append(frames, sse.Data(errorBody(o.Error.Kind, o.Error.Message, nil)))
		return protocol.Encoded{Status: 200, Header: h, Frames: frames}
	case protocol.OutLimit:
		l := o.Limit
		in := l.Resets.Sub(l.Now)
		if in < 0 {
			in = 0
		}
		secs := int(in / time.Second)
		msg := fmt.Sprintf("You exceeded your current quota, please check your plan and billing details. Quota exceeded for metric: generativelanguage.googleapis.com/generate_content_requests, limit: 50, model: %s. Please retry in %ds.", m.Model, secs)
		details := []any{
			map[string]any{"@type": "type.googleapis.com/google.rpc.QuotaFailure", "violations": []any{map[string]any{
				"quotaMetric": "generativelanguage.googleapis.com/generate_content_requests", "quotaId": quotaID(l.Window),
				"quotaDimensions": map[string]any{"location": "global", "model": m.Model}, "quotaValue": "50"}}},
			map[string]any{"@type": "type.googleapis.com/google.rpc.Help", "links": []any{map[string]any{"description": "Learn more about Gemini API quotas", "url": "https://ai.google.dev/gemini-api/docs/rate-limits"}}},
			retryInfo(in),
		}
		return jsonOut(429, h, errorBody(core.RateLimit, msg, details))
	case protocol.OutTokens:
		return jsonOut(200, h, sse.JSON(map[string]int{"totalTokens": o.Tokens}))
	case protocol.OutModels:
		obj := func(id string) map[string]any {
			return map[string]any{"name": "models/" + strings.TrimPrefix(id, "models/"), "displayName": id, "version": "001",
				"inputTokenLimit": 1048576, "outputTokenLimit": 65536,
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent", "countTokens"}}
		}
		if m.Op == protocol.GetModel && len(o.Models) == 1 {
			return jsonOut(200, h, sse.JSON(obj(o.Models[0])))
		}
		data := make([]any, len(o.Models))
		for i, id := range o.Models {
			data[i] = obj(id)
		}
		return jsonOut(200, h, sse.JSON(map[string]any{"models": data}))
	case protocol.OutProbe:
		return protocol.Encoded{Status: 200, Header: h, Body: []byte{}}
	}
	panic("gemini: unknown OutKind")
}

func jsonOut(status int, h http.Header, body []byte) protocol.Encoded {
	h.Set("Content-Type", "application/json; charset=UTF-8")
	return protocol.Encoded{Status: status, Header: h, Body: body}
}

type respChunk struct {
	Candidates []struct {
		Content      rawContent `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type       string `json:"@type"`
			Reason     string `json:"reason"`
			RetryDelay string `json:"retryDelay"`
			Violations []struct {
				QuotaID string `json:"quotaId"`
			} `json:"violations"`
		} `json:"details"`
	} `json:"error"`
}

func kindOf(status int, code, reason string) core.ErrorKind {
	for _, k := range core.ErrorKinds {
		if i := info(k); i.status == status && i.code == code && reason == reasonOrEmpty(k) {
			return k
		}
	}
	for _, k := range core.ErrorKinds {
		if info(k).status == status {
			return k
		}
	}
	return core.APIError
}

func reasonOrEmpty(k core.ErrorKind) string { return reason(k) }

func observeError(status int, c respChunk) protocol.Observed {
	e := c.Error
	why := ""
	quota := ""
	var delay time.Duration
	for _, d := range e.Details {
		if d.Reason != "" {
			why = d.Reason
		}
		for _, v := range d.Violations {
			quota = v.QuotaID
		}
		if d.RetryDelay != "" {
			if n, err := strconv.ParseFloat(strings.TrimSuffix(d.RetryDelay, "s"), 64); err == nil {
				delay = time.Duration(n * float64(time.Second))
			}
		}
	}
	if status == 0 {
		status = e.Code
	}
	if status == 429 && quota != "" {
		return protocol.Observed{Kind: protocol.SawLimit, Window: windowOfQuota(quota), ResetsIn: delay}
	}
	return protocol.Observed{Kind: protocol.SawError, Error: kindOf(status, e.Status, why), Message: e.Message}
}

// Observe reads back a Gemini response, for recording.
func (Codec) Observe(op protocol.Op, status int, h http.Header, body []byte) (protocol.Observed, bool) {
	if op != protocol.Generate {
		return protocol.Observed{}, false
	}
	var chunks []respChunk
	if status != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") {
		var c respChunk
		if json.Unmarshal(body, &c) != nil {
			return protocol.Observed{}, false
		}
		if status != http.StatusOK {
			if c.Error == nil {
				return protocol.Observed{}, false
			}
			return observeError(status, c), true
		}
		chunks = []respChunk{c}
	} else {
		for _, line := range sse.DataLines(body) {
			var c respChunk
			if json.Unmarshal([]byte(line), &c) == nil {
				chunks = append(chunks, c)
			}
		}
	}
	r := core.Reply{Stop: core.EndTurn}
	var thinking, text strings.Builder
	n := 0
	for _, c := range chunks {
		if c.Error != nil {
			o := observeError(0, c)
			if o.Kind == protocol.SawError {
				o.Kind = protocol.SawStreamError
			}
			o.Reply.Text = text.String()
			return o, true
		}
		for _, cand := range c.Candidates {
			for _, p := range cand.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					args := p.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					n++
					r.Calls = append(r.Calls, core.Call{ID: p.FunctionCall.ID, Tool: p.FunctionCall.Name, Input: args})
				case p.Thought:
					thinking.WriteString(p.Text)
				default:
					text.WriteString(p.Text)
				}
				if p.ThoughtSignature != "" && r.ThinkingSignature == "" {
					r.ThinkingSignature = p.ThoughtSignature
					if b, err := base64.StdEncoding.DecodeString(p.ThoughtSignature); err == nil {
						r.ThinkingSignature = string(b)
					}
				}
			}
			if cand.FinishReason == "MAX_TOKENS" {
				r.Stop = core.MaxTokens
			}
		}
	}
	r.Thinking, r.Text = thinking.String(), text.String()
	if len(r.Calls) > 0 && r.Stop != core.MaxTokens {
		r.Stop = core.ToolUse
	}
	return protocol.Observed{Kind: protocol.SawReply, Reply: r}, true
}
