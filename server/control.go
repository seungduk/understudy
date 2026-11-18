package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/seungduk/understudy/script"
)

func (s *Server) controlRoutes() {
	p := ControlPrefix
	s.mux.HandleFunc("GET "+p+"health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": Version, "script_loaded": s.script.Load() != nil})
	})
	s.mux.HandleFunc("PUT "+p+"script", s.putScript)
	s.mux.HandleFunc("GET "+p+"script", s.getScript)
	s.mux.HandleFunc("GET "+p+"script/info", s.scriptInfo)
	s.mux.HandleFunc("POST "+p+"validate", s.validate)
	s.mux.HandleFunc("GET "+p+"sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"sessions": s.Sessions()})
	})
	s.mux.HandleFunc("GET "+p+"events", s.events)
	s.mux.HandleFunc("POST "+p+"gates/{name}/release", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !script.ValidGateName(name) {
			controlError(w, 400, "the gate name "+name+" is not valid", "use 1-64 lower-case letters, digits, - and _, as in the script")
			return
		}
		writeJSON(w, 200, map[string]any{"released": name, "waiting": s.Release(name)})
	})
	s.mux.HandleFunc("GET "+p+"gates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"gates": s.Gates()})
	})
	s.mux.HandleFunc("GET "+p+"requests", func(w http.ResponseWriter, r *http.Request) {
		var es []Entry
		if k := r.URL.Query().Get("session"); k != "" {
			es = s.SessionRequests(k)
		} else {
			es = s.Requests()
		}
		writeJSON(w, 200, map[string]any{"requests": es})
	})
	s.mux.HandleFunc("POST "+p+"reset", func(w http.ResponseWriter, r *http.Request) {
		s.Reset()
		writeJSON(w, 200, map[string]any{"reset": true})
	})
	s.mux.HandleFunc("GET "+p+"clock", func(w http.ResponseWriter, r *http.Request) {
		mode := "real"
		if s.virtual != nil {
			mode = "virtual"
		}
		writeJSON(w, 200, map[string]any{"mode": mode, "now": s.Now().UTC().Format(time.RFC3339Nano)})
	})
	s.mux.HandleFunc("POST "+p+"clock/advance", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MS *int64 `json:"ms"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || body.MS == nil || *body.MS < 0 {
			controlError(w, 400, "the body must be {\"ms\": N} with N >= 0", "send how many milliseconds to move the clock, such as {\"ms\": 30000}")
			return
		}
		if err := s.Advance(time.Duration(*body.MS) * time.Millisecond); err != nil {
			controlError(w, 409, err.Error(), "start the server with --clock virtual")
			return
		}
		writeJSON(w, 200, map[string]any{"now": s.Now().UTC().Format(time.RFC3339Nano)})
	})
	s.mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
		controlError(w, 404, "no control endpoint "+r.Method+" "+r.URL.Path,
			"use GET health, PUT script, GET script, GET script/info, POST validate, GET sessions, GET requests, GET events, POST reset, GET gates, POST gates/NAME/release, GET clock, POST clock/advance under "+ControlPrefix)
	})
}

func controlError(w http.ResponseWriter, status int, msg, fix string) {
	writeJSON(w, status, map[string]string{"error": msg, "fix": fix})
}

func (s *Server) putScript(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBody))
	if err != nil {
		controlError(w, 400, "cannot read the body", "send the script file as the body")
		return
	}
	sc, err := Parse(data)
	if err != nil {
		var ps script.Problems
		if errors.As(err, &ps) {
			writeJSON(w, 400, map[string]any{"error": "the script was not loaded", "problems": ps})
			return
		}
		controlError(w, 400, err.Error(), "send a script file")
		return
	}
	s.Load(sc)
	writeJSON(w, 200, map[string]any{"loaded": true, "sessions": sc.Keys()})
}

func (s *Server) getScript(w http.ResponseWriter, r *http.Request) {
	sc := s.script.Load()
	if sc == nil {
		controlError(w, 404, "no script is loaded", "PUT one to "+ControlPrefix+"script")
		return
	}
	b := sc.Source()
	if b == nil {
		var err error
		if b, err = script.Marshal(sc.File()); err != nil {
			controlError(w, 500, err.Error(), "report this as a bug")
			return
		}
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(b)
}

func (s *Server) scriptInfo(w http.ResponseWriter, r *http.Request) {
	sc := s.script.Load()
	if sc == nil {
		writeJSON(w, 200, map[string]any{"loaded": false})
		return
	}
	f := sc.File()
	writeJSON(w, 200, map[string]any{"loaded": true, "divergence": sc.Divergence(), "provenance": f.Provenance, "sessions": sc.Outline()})
}

// Validation is the result of checking a script without loading it.
type Validation struct {
	OK       bool             `json:"ok"`
	Sessions []string         `json:"sessions"`
	Problems []script.Problem `json:"problems"`
}

// Validate checks a script's text as PUT script would, without loading it.
func Validate(data []byte) Validation {
	v := Validation{Sessions: []string{}, Problems: []script.Problem{}}
	sc, err := script.Parse(data)
	if err != nil {
		var ps script.Problems
		if !errors.As(err, &ps) {
			ps = script.Problems{{Rule: script.RuleSyntax, Message: err.Error(), Fix: "send a script file"}}
		}
		v.Problems = ps
		return v
	}
	v.OK, v.Sessions = true, sc.Keys()
	return v
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBody))
	if err != nil {
		controlError(w, 400, "cannot read the body", "send the script file as the body")
		return
	}
	writeJSON(w, 200, Validate(data))
}

// EventsHeartbeat is how often the events stream sends a comment when nothing changes.
const EventsHeartbeat = 15 * time.Second

// eventsCoalesce is how long the events stream gathers changes into one event.
const eventsCoalesce = 50 * time.Millisecond

// events streams "change" events (Server-Sent Events) carrying {"version": N}:
// one at once, then one after every burst of changes. Clients refetch what they show.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	send := func(v uint64) bool {
		if _, err := fmt.Fprintf(w, "event: change\ndata: {\"version\":%d}\n\n", v); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	ctx := r.Context()
	v := s.Version()
	if _, err := io.WriteString(w, "retry: 1000\n"); err != nil || !send(v) {
		return
	}
	for {
		wctx, cancel := context.WithTimeout(ctx, EventsHeartbeat)
		nv, err := s.WaitChange(wctx, v)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil { // heartbeat
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			continue
		}
		t := time.NewTimer(eventsCoalesce)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		v = max(nv, s.Version())
		if !send(v) {
			return
		}
	}
}
