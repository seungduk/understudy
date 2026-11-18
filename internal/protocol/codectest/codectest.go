// Package codectest is the contract every protocol.Codec must honour, as one
// test that each codec package runs on itself: every reply shape, streamed and
// not, reads back as itself; every error kind and limit window reads back as
// itself (or as the kind the protocol cannot tell it from); stream errors read
// back with their partial text; and every frame is a well-formed SSE frame.
package codectest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

// Replies are the reply shapes every codec must carry.
var Replies = []core.Reply{
	{Text: "hello world, a longer text than one chunk of the stream", Stop: core.EndTurn},
	{Text: "cut short", Stop: core.MaxTokens},
	{Thinking: "plan the answer first", ThinkingSignature: "sig_x", Text: "done", Stop: core.EndTurn},
	{Stop: core.ToolUse, Calls: []core.Call{{ID: "a1", Tool: "bash", Input: json.RawMessage(`{"command":"go test ./...","description":"Run"}`)}}},
	{Thinking: "t", ThinkingSignature: "s", Text: "two calls", Stop: core.ToolUse, Calls: []core.Call{
		{ID: "a1", Tool: "read", Input: json.RawMessage(`{"path":"/x/한국어.txt"}`)}, {ID: "b2", Tool: "noargs", Input: json.RawMessage(`{}`)}}},
}

// FreeformReply has a freeform call, which only some protocols carry as such.
var FreeformReply = core.Reply{Stop: core.ToolUse, Calls: []core.Call{{ID: "f1", Tool: "apply_patch", Kind: core.Freeform, Raw: "*** Begin Patch\n*** Add File: /a\n+x\n*** End Patch\n"}}}

// Options say what a protocol cannot tell apart, for the checks.
type Options struct {
	// SameAs maps an error kind to the kind it reads back as (the protocol
	// gives both the same status and body).
	SameAs map[core.ErrorKind]core.ErrorKind
	// CallID turns a reply's bare call id into the id on the wire ("" when the wire has none).
	CallID func(id string) string
	// Freeform says the protocol carries freeform calls as freeform.
	Freeform bool
	// LimitWindows are the windows that read back as themselves.
	LimitWindows []core.Window
	// NoSignature says the protocol has no thinking signature.
	NoSignature bool
}

func header(e protocol.Encoded) http.Header {
	if e.Frames != nil {
		return http.Header{"Content-Type": {"text/event-stream"}}
	}
	return e.Header
}

func body(e protocol.Encoded) []byte {
	if e.Frames == nil {
		return e.Body
	}
	var b []byte
	for _, f := range e.Frames {
		b = append(b, f...)
	}
	return b
}

func meta(stream bool) protocol.Meta {
	return protocol.Meta{Seed: "seed", RequestID: "req_1", Model: "m", Stream: stream, Usage: core.Usage{Input: 7, Output: 9}, Now: time.Unix(1000, 0), Op: protocol.Generate}
}

// Run checks the contract on c.
func Run(t *testing.T, c protocol.Codec, o Options) {
	t.Helper()
	want := func(r core.Reply) core.Reply {
		w := r
		if o.NoSignature {
			w.ThinkingSignature = ""
		}
		w.Calls = nil
		for _, cl := range r.Calls {
			if o.CallID != nil {
				cl.ID = o.CallID(cl.ID)
			}
			w.Calls = append(w.Calls, cl)
		}
		return w
	}
	replies := append([]core.Reply(nil), Replies...)
	if o.Freeform {
		replies = append(replies, FreeformReply)
	}
	for i, r := range replies {
		for _, stream := range []bool{true, false} {
			e := c.Encode(meta(stream), protocol.Out{Kind: protocol.OutReply, Reply: r})
			if e.Status != 200 || (e.Frames != nil) != stream {
				t.Errorf("reply %d stream=%v: status %d frames %v", i, stream, e.Status, e.Frames != nil)
			}
			for _, f := range e.Frames {
				if !strings.HasSuffix(string(f), "\n\n") || len(sse.Parse(f)) != 1 {
					t.Errorf("reply %d: bad frame %q", i, f)
				}
			}
			got, ok := c.Observe(protocol.Generate, e.Status, header(e), body(e))
			if w := want(r); !ok || got.Kind != protocol.SawReply || !equal(got.Reply, w) {
				t.Errorf("reply %d stream=%v:\n got %+v\nwant %+v", i, stream, got.Reply, w)
			}
		}
	}
	for _, k := range core.ErrorKinds {
		wantK := k
		if s, ok := o.SameAs[k]; ok {
			wantK = s
		}
		for _, rt := range []core.Retry{core.RetryDefault, core.RetryNever, core.RetryAlways} {
			e := c.Encode(meta(true), protocol.Out{Kind: protocol.OutError, Error: protocol.ErrorSpec{Kind: k, Retry: rt, RetryAfter: 3 * time.Second, HasRetryAfter: true}})
			if e.Status < 400 || e.Frames != nil || !json.Valid(e.Body) {
				t.Errorf("%s: %d %s", k.Name(), e.Status, e.Body)
			}
			got, ok := c.Observe(protocol.Generate, e.Status, e.Header, e.Body)
			if !ok || got.Kind != protocol.SawError || got.Error != wantK || got.Message == "" {
				t.Errorf("error %s retry %d: %+v", k.Name(), rt, got)
			}
		}
		e := c.Encode(meta(true), protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: k, Message: "boom"}, Partial: "half a reply"})
		got, ok := c.Observe(protocol.Generate, e.Status, header(e), body(e))
		if !ok || got.Kind != protocol.SawStreamError || got.Error != wantK || got.Message != "boom" || got.Reply.Text != "half a reply" {
			t.Errorf("stream error %s: %+v", k.Name(), got)
		}
		e = c.Encode(meta(false), protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: k, Message: "boom"}})
		if got, ok := c.Observe(protocol.Generate, e.Status, e.Header, e.Body); !ok || e.Frames != nil || got.Kind != protocol.SawError || got.Error != wantK {
			t.Errorf("non-streamed stream error %s: %+v", k.Name(), got)
		}
	}
	isLimit := map[core.Window]bool{}
	for _, w := range o.LimitWindows {
		isLimit[w] = true
	}
	for _, w := range core.Windows {
		e := c.Encode(meta(true), protocol.Out{Kind: protocol.OutLimit, Limit: protocol.LimitSpec{Window: w, Now: time.Unix(1000, 0), Resets: time.Unix(1000+7200, 0)}})
		got, ok := c.Observe(protocol.Generate, e.Status, e.Header, e.Body)
		if e.Status != 429 || !ok {
			t.Errorf("limit %s: %d %+v", w.Name(), e.Status, got)
			continue
		}
		if isLimit[w] && (got.Kind != protocol.SawLimit || got.Window != w || got.ResetsIn != 2*time.Hour) {
			t.Errorf("limit %s read back as %+v", w.Name(), got)
		}
	}
	if _, ok := c.Observe(protocol.CountTokens, 200, http.Header{}, []byte(`{}`)); ok {
		t.Error("a count_tokens response was observed")
	}
	if _, ok := c.Observe(protocol.Generate, 200, http.Header{}, []byte(`nope`)); ok {
		t.Error("a body that is not JSON was observed")
	}
	for _, k := range []protocol.OutKind{protocol.OutTokens, protocol.OutModels, protocol.OutProbe} {
		if e := c.Encode(meta(false), protocol.Out{Kind: k, Tokens: 3, Models: []string{"a", "b"}}); e.Status != 200 {
			t.Errorf("out %d: %d", k, e.Status)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("no panic on an unknown OutKind")
			}
		}()
		c.Encode(meta(false), protocol.Out{})
	}()
}

func equal(a, b core.Reply) bool {
	if a.Thinking != b.Thinking || a.ThinkingSignature != b.ThinkingSignature || a.Text != b.Text || a.Stop != b.Stop || len(a.Calls) != len(b.Calls) {
		return false
	}
	for i := range a.Calls {
		x, y := a.Calls[i], b.Calls[i]
		if x.ID != y.ID || x.Tool != y.Tool || x.Kind != y.Kind || x.Raw != y.Raw || !sameJSON(x.Input, y.Input) {
			return false
		}
	}
	return true
}

func sameJSON(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
