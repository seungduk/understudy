// Package anthropic is the Anthropic Messages API wire protocol, as Claude
// Code (and every Anthropic-compatible client) uses it: request decoding, the
// SSE event sequence, message JSON, error bodies and the subscription
// usage-limit headers. It translates to and from core only.
package anthropic

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

// Codec is the Anthropic Messages protocol.
type Codec struct{}

// New is the codec.
func New() protocol.Codec { return Codec{} }

// ID is protocol.Anthropic.
func (Codec) ID() protocol.ID { return protocol.Anthropic }

// Routes are the Messages endpoints, under / and under /anthropic/ (the base
// path of Anthropic-compatible gateways such as DeepSeek's), and Claude Code's
// start-up probe.
func (Codec) Routes() []string {
	var out []string
	for _, base := range []string{"", "/anthropic"} {
		out = append(out, "POST "+base+"/v1/messages", "POST "+base+"/v1/messages/count_tokens",
			"GET "+base+"/v1/models", "GET "+base+"/v1/models/{id}")
	}
	return append(out, "/api/hello")
}

// Accepts claims a shared route when the request carries an Anthropic header.
func (Codec) Accepts(r *http.Request) bool {
	return r.Header.Get("Anthropic-Version") != "" || r.Header.Get("X-Api-Key") != "" || strings.HasPrefix(r.URL.Path, "/anthropic/")
}

// Decode reads a request on one of Routes.
func (Codec) Decode(r *http.Request, body []byte) (protocol.Decoded, error) {
	p := strings.TrimPrefix(r.URL.Path, "/anthropic")
	switch {
	case p == "/api/hello":
		return protocol.Decoded{Op: protocol.Probe}, nil
	case p == "/v1/models":
		return protocol.Decoded{Op: protocol.ListModels}, nil
	case strings.HasPrefix(p, "/v1/models/"):
		return protocol.Decoded{Op: protocol.GetModel, Model: strings.TrimPrefix(p, "/v1/models/")}, nil
	}
	req, err := parseRequest(body)
	if err != nil {
		return protocol.Decoded{}, err
	}
	op := protocol.Generate
	if strings.HasSuffix(p, "/count_tokens") {
		op = protocol.CountTokens
	}
	return protocol.Decoded{Op: op, Request: req}, nil
}

func jsonOut(status int, h http.Header, body []byte) protocol.Encoded {
	h.Set("Content-Type", "application/json")
	return protocol.Encoded{Status: status, Header: h, Body: body}
}

// Encode makes the response of one outcome.
func (Codec) Encode(m protocol.Meta, o protocol.Out) protocol.Encoded {
	h := http.Header{}
	if m.RequestID != "" {
		h.Set("Request-Id", m.RequestID)
	}
	id := core.DeriveID("msg_", m.Seed)
	switch o.Kind {
	case protocol.OutReply:
		blocks := blocksOf(o.Reply)
		if m.Stream {
			return protocol.Encoded{Status: 200, Header: h, Frames: frames(streamEvents(id, m.Model, blocks, o.Reply.Stop, m.Usage))}
		}
		return jsonOut(200, h, messageJSON(id, m.Model, blocks, o.Reply.Stop, m.Usage))
	case protocol.OutError:
		return errorOut(h, o.Error, m.RequestID)
	case protocol.OutStreamError:
		if !m.Stream {
			// A non-streamed request cannot carry an in-stream error; send the same error as HTTP.
			return errorOut(h, protocol.ErrorSpec{Kind: o.Error.Kind, Message: o.Error.Message}, m.RequestID)
		}
		return protocol.Encoded{Status: 200, Header: h, Frames: frames(errorEvents(id, m.Model, m.Usage, o.Partial, o.Error.Kind, o.Error.Message))}
	case protocol.OutLimit:
		l := o.Limit
		for k, v := range LimitHeaders(l.Window, l.Now, l.Resets) {
			h.Set(k, v)
		}
		return jsonOut(429, h, ErrorBody(core.RateLimit, limitMessage(l.Window), m.RequestID))
	case protocol.OutTokens:
		return jsonOut(200, h, sse.JSON(map[string]int{"input_tokens": o.Tokens}))
	case protocol.OutModels:
		data := make([]model, len(o.Models))
		for i, id := range o.Models {
			data[i] = modelOf(id)
		}
		if m.Op == protocol.GetModel && len(data) == 1 {
			return jsonOut(200, h, sse.JSON(data[0]))
		}
		first, last := "", ""
		if len(data) > 0 {
			first, last = data[0].ID, data[len(data)-1].ID
		}
		return jsonOut(200, h, sse.JSON(map[string]any{"data": data, "has_more": false, "first_id": first, "last_id": last}))
	case protocol.OutProbe:
		h.Set("Content-Length", "0")
		return protocol.Encoded{Status: 200, Header: h, Body: []byte{}}
	}
	panic("anthropic: unknown OutKind")
}

func errorOut(h http.Header, e protocol.ErrorSpec, requestID string) protocol.Encoded {
	if e.HasRetryAfter {
		h.Set("Retry-After", strconv.Itoa(int(e.RetryAfter/time.Second)))
	}
	switch e.Retry {
	case core.RetryNever:
		h.Set("X-Should-Retry", "false")
	case core.RetryAlways:
		h.Set("X-Should-Retry", "true")
	}
	return jsonOut(Status(e.Kind), h, ErrorBody(e.Kind, e.Message, requestID))
}

type model struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

func modelOf(id string) model {
	return model{Type: "model", ID: id, DisplayName: id, CreatedAt: "2026-01-01T00:00:00Z"}
}

func limitMessage(w core.Window) string {
	switch w {
	case core.FiveHour:
		return "You have reached your 5-hour usage limit."
	case core.SevenDayOpus:
		return "You have reached your weekly Opus usage limit."
	case core.SevenDaySonnet:
		return "You have reached your weekly Sonnet usage limit."
	case core.Daily:
		return "You have reached your daily usage limit."
	case core.PerMinute:
		return "Number of request tokens has exceeded your per-minute rate limit."
	}
	return "You have reached your weekly usage limit."
}

// LimitHeaders are the headers of a usage-limit 429 for window w, resetting at
// resets. Measured against Claude Code 2.1.325: with these (and a gateway
// token, see docs/limits.md) it prints "You've hit your weekly limit". The windows
// the unified headers do not have (per_minute, daily) are a plain rate limit
// with retry-after; daily also says not to retry.
func LimitHeaders(w core.Window, now, resets time.Time) map[string]string {
	secs := int(resets.Sub(now) / time.Second)
	if secs < 0 {
		secs = 0
	}
	reset := strconv.FormatInt(resets.Unix(), 10)
	switch w {
	case core.PerMinute:
		return map[string]string{"Retry-After": strconv.Itoa(secs)}
	case core.Daily:
		return map[string]string{"Retry-After": strconv.Itoa(secs), "X-Should-Retry": "false"}
	}
	h := map[string]string{
		"X-Should-Retry":                     "false",
		"Retry-After":                        strconv.Itoa(secs),
		"Anthropic-Ratelimit-Unified-Status": "rejected",
		"Anthropic-Ratelimit-Unified-Representative-Claim": w.Name(),
		"Anthropic-Ratelimit-Unified-Reset":                reset,
	}
	win := "7d"
	if w == core.FiveHour {
		win = "5h"
	}
	h["Anthropic-Ratelimit-Unified-"+win+"-Status"] = "rejected"
	h["Anthropic-Ratelimit-Unified-"+win+"-Reset"] = reset
	h["Anthropic-Ratelimit-Unified-"+win+"-Utilization"] = "1.0"
	return h
}

// Observe reads back a Messages response, for recording.
func (Codec) Observe(op protocol.Op, status int, h http.Header, body []byte) (protocol.Observed, bool) {
	if op != protocol.Generate {
		return protocol.Observed{}, false
	}
	if status != http.StatusOK {
		var env struct {
			Error struct{ Type, Message string } `json:"error"`
		}
		_ = json.Unmarshal(body, &env)
		if status == 429 && h.Get("Anthropic-Ratelimit-Unified-Status") == "rejected" {
			w, ok := core.ParseWindow(h.Get("Anthropic-Ratelimit-Unified-Representative-Claim"))
			if !ok {
				w = core.SevenDay
			}
			o := protocol.Observed{Kind: protocol.SawLimit, Window: w}
			if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
				o.ResetsIn = time.Duration(s) * time.Second
			}
			return o, true
		}
		k, ok := kindOfStatus(status)
		if !ok {
			return protocol.Observed{}, false
		}
		return protocol.Observed{Kind: protocol.SawError, Error: k, Message: env.Error.Message}, true
	}
	if strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") {
		return observeStream(body)
	}
	return observeJSON(body)
}

type collected struct {
	kind  string
	name  string
	id    string
	text  strings.Builder
	sig   strings.Builder
	input strings.Builder
}

func observeStream(raw []byte) (protocol.Observed, bool) {
	var blocks []*collected
	byIndex := map[int]*collected{}
	stop := core.EndTurn
	for _, line := range sse.DataLines(raw) {
		var e struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type, Name, ID string
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Error struct{ Type, Message string } `json:"error"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Type {
		case "content_block_start":
			b := &collected{kind: e.ContentBlock.Type, name: e.ContentBlock.Name, id: e.ContentBlock.ID}
			blocks = append(blocks, b)
			byIndex[e.Index] = b
		case "content_block_delta":
			b := byIndex[e.Index]
			if b == nil {
				continue
			}
			b.text.WriteString(e.Delta.Text + e.Delta.Thinking)
			b.sig.WriteString(e.Delta.Signature)
			b.input.WriteString(e.Delta.PartialJSON)
		case "message_delta":
			stop = stopOf(e.Delta.StopReason)
		case "error":
			k, ok := kindOfType(e.Error.Type)
			if !ok {
				k = core.APIError
			}
			r, _ := assemble(blocks, stop)
			return protocol.Observed{Kind: protocol.SawStreamError, Error: k, Message: e.Error.Message, Reply: core.Reply{Text: r.Text}}, true
		}
	}
	r, err := assemble(blocks, stop)
	if err != nil {
		return protocol.Observed{}, false
	}
	return protocol.Observed{Kind: protocol.SawReply, Reply: r}, true
}

func stopOf(s string) core.Stop {
	switch s {
	case "tool_use":
		return core.ToolUse
	case "max_tokens":
		return core.MaxTokens
	}
	return core.EndTurn
}

func observeJSON(raw []byte) (protocol.Observed, bool) {
	var m struct {
		Content []struct {
			Type, Text, Thinking, Signature, Name, ID string
			Input                                     json.RawMessage
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return protocol.Observed{}, false
	}
	var blocks []*collected
	for _, c := range m.Content {
		b := &collected{kind: c.Type, name: c.Name, id: c.ID}
		b.text.WriteString(c.Text + c.Thinking)
		b.sig.WriteString(c.Signature)
		b.input.Write(c.Input)
		blocks = append(blocks, b)
	}
	r, err := assemble(blocks, stopOf(m.StopReason))
	if err != nil {
		return protocol.Observed{}, false
	}
	return protocol.Observed{Kind: protocol.SawReply, Reply: r}, true
}

func assemble(blocks []*collected, stop core.Stop) (core.Reply, error) {
	var thinking, sig, text []string
	r := core.Reply{Stop: stop}
	for _, b := range blocks {
		switch b.kind {
		case "thinking":
			thinking = append(thinking, b.text.String())
			sig = append(sig, b.sig.String())
		case "text":
			text = append(text, b.text.String())
		case "tool_use":
			in := b.input.String()
			if in == "" {
				in = "{}"
			}
			if !json.Valid([]byte(in)) {
				return core.Reply{}, errors.New("tool input is not JSON")
			}
			r.Calls = append(r.Calls, core.Call{ID: b.id, Tool: b.name, Input: json.RawMessage(in)})
		}
	}
	r.Thinking, r.ThinkingSignature, r.Text = strings.Join(thinking, "\n"), strings.Join(sig, ""), strings.Join(text, "\n")
	return r, nil
}
