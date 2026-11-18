package deliver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungduk/understudy/internal/clock"
	"github.com/seungduk/understudy/internal/gate"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/internal/wire"
)

func events() []wire.Event {
	return wire.StreamEvents(wire.Meta{ID: "msg_1", Model: "m"}, []wire.OutBlock{{Kind: wire.OutText, Text: "hello world"}}, wire.EndTurn, wire.Usage{Input: 1, Output: 2})
}

type rig struct {
	srv     *httptest.Server
	outcome chan reqlog.Outcome
	clk     *clock.Virtual
	gates   *gate.Set
	stalled chan struct{}
	resumed chan struct{}
}

func newRig(t *testing.T, p Plan) *rig {
	r := &rig{outcome: make(chan reqlog.Outcome, 1), clk: clock.NewVirtual(time.Unix(0, 0)), gates: gate.New(), stalled: make(chan struct{}, 1), resumed: make(chan struct{}, 1)}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.outcome <- Send(req.Context(), w, p, Deps{Clock: r.clk, Gates: r.gates, OnStall: func() { r.stalled <- struct{}{} }, OnResume: func() { r.resumed <- struct{}{} }})
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// raw sends one GET over a plain TCP connection and reads until the end.
func (r *rig) raw(t *testing.T) (string, error) {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(r.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(bufio.NewReader(c))
	return string(b), err
}

func TestStreamServed(t *testing.T) {
	r := newRig(t, Plan{Events: events(), Headers: http.Header{"Request-Id": {"req_1"}}})
	resp, err := http.Get(r.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || resp.Header.Get("Request-Id") != "req_1" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	var want strings.Builder
	for _, e := range events() {
		want.Write(e.Encode())
	}
	if string(b) != want.String() {
		t.Fatalf("body %q", b)
	}
	if o := <-r.outcome; o != reqlog.Served {
		t.Fatal(o)
	}
}

func TestBody(t *testing.T) {
	r := newRig(t, Plan{Status: 529, Body: wire.ErrorBody(wire.Overloaded, "", "req_1"), Headers: http.Header{"X-Should-Retry": {"true"}}})
	resp, err := http.Get(r.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 529 || !strings.Contains(string(b), "overloaded_error") || resp.Header.Get("X-Should-Retry") != "true" || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if <-r.outcome != reqlog.Served {
		t.Fatal("outcome")
	}
}

func TestCuts(t *testing.T) {
	for _, c := range []struct {
		name  string
		p     Plan
		reset bool
		saw   string // text the client must have got before the cut ("" = nothing)
		not   string
	}{
		{"close before response", Plan{Events: events(), Cut: true}, false, "", "HTTP/1.1"},
		{"reset before response", Plan{Events: events(), Cut: true, CutReset: true}, true, "", "HTTP/1.1"},
		{"close mid stream", Plan{Events: events(), Cut: true, CutAfter: 3}, false, "event: ping", "text_delta"},
		{"reset mid stream", Plan{Events: events(), Cut: true, CutAfter: 3, CutReset: true}, true, "", "message_stop"},
		{"cut past the end keeps the last event back", Plan{Events: events(), Cut: true, CutAfter: 999}, false, "message_delta", "message_stop"},
		{"close mid body", Plan{Status: 200, Body: []byte(`{"aaaaaaaaaa":"bbbbbbbbbb"}`), Cut: true, CutAfter: 1}, false, `{"aaaaaaaaaa`, "bbbbbbbbbb"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, c.p)
			got, err := r.raw(t)
			if c.reset {
				if !errors.Is(err, syscall.ECONNRESET) {
					t.Fatalf("want reset, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("want clean close, got %v", err)
			}
			if c.saw != "" && !strings.Contains(got, c.saw) {
				t.Errorf("missing %q in %q", c.saw, got)
			}
			if strings.Contains(got, c.not) {
				t.Errorf("unexpected %q in %q", c.not, got)
			}
			want := reqlog.CutClose
			if c.reset {
				want = reqlog.CutReset
			}
			if o := <-r.outcome; o != want {
				t.Errorf("outcome %s", o)
			}
		})
	}
}

func TestStalls(t *testing.T) {
	t.Run("gate before response", func(t *testing.T) {
		r := newRig(t, Plan{Events: events(), StallGate: "g"})
		done := make(chan string)
		go func() { s, _ := r.raw(t); done <- s }()
		<-r.stalled
		select {
		case <-done:
			t.Fatal("answered before release")
		case <-time.After(30 * time.Millisecond):
		}
		select {
		case <-r.resumed:
			t.Fatal("resumed before release")
		default:
		}
		r.gates.Release("g")
		if s := <-done; !strings.Contains(s, "message_stop") {
			t.Fatal(s)
		}
		<-r.resumed
		<-r.outcome
	})
	t.Run("timed mid stream on the virtual clock", func(t *testing.T) {
		r := newRig(t, Plan{Events: events(), StallFor: time.Hour, StallMid: true})
		done := make(chan string)
		go func() { s, _ := r.raw(t); done <- s }()
		<-r.stalled
		for r.clk.Sleepers() == 0 {
			time.Sleep(time.Millisecond)
		}
		r.clk.Advance(time.Hour)
		s := <-done
		if !strings.Contains(s, "message_stop") || !strings.Contains(s, "content_block_start") {
			t.Fatal(s)
		}
		<-r.outcome
	})
	t.Run("pace", func(t *testing.T) {
		r := newRig(t, Plan{Events: events(), FirstDelay: time.Second, Between: time.Second})
		done := make(chan string)
		go func() { s, _ := r.raw(t); done <- s }()
		for i := 0; i < len(events()); i++ {
			for r.clk.Sleepers() == 0 {
				time.Sleep(time.Millisecond)
			}
			r.clk.Advance(time.Second)
		}
		if s := <-done; !strings.Contains(s, "message_stop") {
			t.Fatal(s)
		}
		if !r.clk.Now().Equal(time.Unix(int64(len(events())), 0)) {
			t.Fatal(r.clk.Now())
		}
		<-r.outcome
	})
	t.Run("client gone while stalled", func(t *testing.T) {
		for _, p := range []Plan{{Events: events(), StallGate: "g2"}, {Events: events(), StallGate: "g3", StallMid: true},
			{Events: events(), FirstDelay: time.Hour}, {Events: events(), Between: time.Hour}} {
			r := newRig(t, p)
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, "GET", r.srv.URL, nil)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}()
			for r.clk.Sleepers() == 0 && len(r.gates.Statuses()) == 0 {
				time.Sleep(time.Millisecond)
			}
			cancel()
			if o := <-r.outcome; o != reqlog.ClientGone {
				t.Fatalf("%+v: %s", p, o)
			}
			if p.StallGate != "" {
				<-r.stalled
				<-r.resumed // a stall the client left still ends
			}
		}
	})
}
