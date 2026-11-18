package server

import (
	"io"
	"net/http"
	"sort"

	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/internal/wire"
)

// DefaultModel is listed by /v1/models before any request has named a model.
const DefaultModel = "claude-opus-5-5"

func (s *Server) other(r *http.Request, status int) int {
	e := s.entry(r)
	e.Outcome, e.Status = reqlog.Other, status
	return s.log.Add(e)
}

func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if _, err := wire.ParseRequest(body); err != nil {
		seq := s.other(r, 400)
		writeAPIError(w, seq, wire.InvalidRequest, err.Error())
		return
	}
	s.other(r, 200)
	writeJSON(w, 200, map[string]int{"input_tokens": wire.EstimateTokens(len(body))})
}

func writeAPIError(w http.ResponseWriter, seq int, k wire.ErrorKind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Request-Id", requestID(seq))
	w.WriteHeader(k.Status())
	_, _ = w.Write(wire.ErrorBody(k, msg, requestID(seq)))
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

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	s.other(r, 200)
	s.mu.Lock()
	ids := make([]string, 0, len(s.models)+1)
	for m := range s.models {
		ids = append(ids, m)
	}
	s.mu.Unlock()
	if len(ids) == 0 {
		ids = append(ids, DefaultModel)
	}
	sort.Strings(ids)
	data := make([]model, len(ids))
	for i, id := range ids {
		data[i] = modelOf(id)
	}
	writeJSON(w, 200, map[string]any{"data": data, "has_more": false, "first_id": ids[0], "last_id": ids[len(ids)-1]})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	s.other(r, 200)
	writeJSON(w, 200, modelOf(r.PathValue("id")))
}

// hello answers Claude Code's start-up probe (HEAD /api/hello, measured on 2.1.325).
func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	s.other(r, 200)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(200)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	seq := s.other(r, 404)
	writeAPIError(w, seq, wire.NotFound, "understudy does not serve "+r.Method+" "+r.URL.Path)
}
