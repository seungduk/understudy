// Package registry is the list of protocol codecs the server and the recorder
// serve. Adding a protocol is adding its package and one line here.
package registry

import (
	"github.com/seungduk/understudy/internal/protocol/anthropic"
	"github.com/seungduk/understudy/internal/protocol/gemini"
	"github.com/seungduk/understudy/internal/protocol/openaichat"
	"github.com/seungduk/understudy/internal/protocol/responses"
	"github.com/seungduk/understudy/protocol"
)

// All is every codec, in the order shared routes try them.
func All() []protocol.Codec {
	return []protocol.Codec{anthropic.New(), openaichat.New(), responses.New(), gemini.New()}
}

// For is the codec of one protocol.
func For(id protocol.ID) protocol.Codec {
	for _, c := range All() {
		if c.ID() == id {
			return c
		}
	}
	panic("registry: no codec for " + id.Name())
}
