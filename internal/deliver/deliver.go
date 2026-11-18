// Package deliver writes one planned reply to an HTTP response, with its
// faults: stalls (timed or gated), pacing, and cuts (clean close or TCP reset).
package deliver

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/seungduk/understudy/internal/clock"
	"github.com/seungduk/understudy/internal/gate"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/internal/wire"
)

// Plan is one reply, ready to send. Exactly one of Events (a 200 SSE stream)
// or Body (a JSON response with Status) is set.
type Plan struct {
	Status  int
	Headers http.Header
	Body    []byte
	Events  []wire.Event

	StallFor  time.Duration // a timed stall, or
	StallGate string        // a gated stall
	StallMid  bool          // stall after the first two events instead of before the response

	Cut      bool
	CutAfter int  // events (or, for a body, 0 = before the response, more = after half of it)
	CutReset bool // reset (RST) instead of close (FIN)

	FirstDelay time.Duration
	Between    time.Duration
}

// Deps are what delivery waits on.
type Deps struct {
	Clock clock.Clock
	Gates *gate.Set
	// OnStall is called when the reply starts to wait (for the log), may be nil.
	OnStall func()
	// OnResume is called when that wait ends, however it ends, may be nil.
	OnResume func()
}

// Send writes p to w and says how the request ended.
func Send(ctx context.Context, w http.ResponseWriter, p Plan, d Deps) reqlog.Outcome {
	stall := func() bool {
		if p.StallFor == 0 && p.StallGate == "" {
			return true
		}
		if d.OnStall != nil {
			d.OnStall()
		}
		if d.OnResume != nil {
			defer d.OnResume()
		}
		if p.StallGate != "" {
			return d.Gates.Wait(ctx, p.StallGate) == nil
		}
		return d.Clock.Sleep(ctx, p.StallFor) == nil
	}
	if !p.StallMid && !stall() {
		return reqlog.ClientGone
	}
	if d.Clock.Sleep(ctx, p.FirstDelay) != nil && p.FirstDelay > 0 {
		return reqlog.ClientGone
	}
	if p.Cut && p.CutAfter == 0 {
		return cut(w, p.CutReset)
	}
	if p.Events == nil {
		return sendBody(w, p)
	}
	h := w.Header()
	for k, v := range p.Headers {
		h[k] = v
	}
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	cutAt := -1
	if p.Cut {
		cutAt = min(p.CutAfter, len(p.Events)-1)
	}
	midAt := -1
	if p.StallMid {
		midAt = min(2, len(p.Events)-1)
	}
	for i, e := range p.Events {
		if i == cutAt {
			return cut(w, p.CutReset)
		}
		if i == midAt && !stall() {
			return reqlog.ClientGone
		}
		if i > 0 && p.Between > 0 && d.Clock.Sleep(ctx, p.Between) != nil {
			return reqlog.ClientGone
		}
		if _, err := w.Write(e.Encode()); err != nil {
			return reqlog.ClientGone
		}
		if err := rc.Flush(); err != nil {
			return reqlog.ClientGone
		}
	}
	return reqlog.Served
}

func sendBody(w http.ResponseWriter, p Plan) reqlog.Outcome {
	h := w.Header()
	for k, v := range p.Headers {
		h[k] = v
	}
	h.Set("Content-Type", "application/json")
	body := p.Body
	if p.Cut {
		// After the headers and half of the body.
		body = body[:len(body)/2]
	}
	w.WriteHeader(p.Status)
	if _, err := w.Write(body); err != nil {
		return reqlog.ClientGone
	}
	if p.Cut {
		_ = http.NewResponseController(w).Flush()
		return cut(w, p.CutReset)
	}
	return reqlog.Served
}

// cut ends the connection now: a clean close, or a reset (SO_LINGER 0).
func cut(w http.ResponseWriter, reset bool) reqlog.Outcome {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		// Not hijackable (HTTP/2): abort the handler, which drops the stream.
		panic(http.ErrAbortHandler)
	}
	if reset {
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = conn.Close()
		return reqlog.CutReset
	}
	_ = conn.Close()
	return reqlog.CutClose
}
