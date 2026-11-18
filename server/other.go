package server

import (
	"net/http"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/registry"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/protocol"
)

// DefaultModel is listed by the model endpoints before any request has named a model.
const DefaultModel = "claude-opus-5-5"

// notFound answers a path no protocol serves, in the Anthropic error shape.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	e.Outcome, e.Status = reqlog.Other, 404
	seq := s.log.Add(e)
	write(w, registry.For(protocol.Anthropic).Encode(protocol.Meta{RequestID: requestID(seq)},
		protocol.Out{Kind: protocol.OutError, Error: protocol.ErrorSpec{Kind: core.NotFound, Message: "understudy does not serve " + r.Method + " " + r.URL.Path}}))
}
