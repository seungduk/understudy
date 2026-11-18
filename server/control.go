package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/seungduk/understudy/script"
	"gopkg.in/yaml.v3"
)

func (s *Server) controlRoutes() {
	p := ControlPrefix
	s.mux.HandleFunc("GET "+p+"health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": Version, "script_loaded": s.script.Load() != nil})
	})
	s.mux.HandleFunc("PUT "+p+"script", s.putScript)
	s.mux.HandleFunc("GET "+p+"script", s.getScript)
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
			"use GET health, PUT script, GET script, GET requests, POST reset, GET gates, POST gates/NAME/release, GET clock, POST clock/advance under "+ControlPrefix)
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
	sc, err := script.Parse(data)
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
	b, err := yaml.Marshal(sc.File())
	if err != nil {
		controlError(w, 500, err.Error(), "report this as a bug")
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(b)
}
