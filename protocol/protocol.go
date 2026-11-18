// Package protocol is the one narrow interface every wire protocol implements:
// decode an HTTP request into a core.Request, encode a core outcome into an
// HTTP response (a JSON body or SSE frames), and, for recording, read a
// response back. Implementations live in internal/protocol/<name>; the server
// and the recorder use them only through Codec.
package protocol

import (
	"net/http"
	"time"

	"github.com/seungduk/understudy/core"
)

// ID is the closed set of wire protocols.
type ID int

const (
	Anthropic       ID = iota + 1 // Anthropic Messages: POST /v1/messages
	OpenAIChat                    // OpenAI Chat Completions: POST /v1/chat/completions
	OpenAIResponses               // OpenAI Responses: POST /v1/responses
	Gemini                        // Gemini API: POST /v1beta/models/M:generateContent and :streamGenerateContent
)

// IDs lists every protocol, in a fixed order.
var IDs = []ID{Anthropic, OpenAIChat, OpenAIResponses, Gemini}

var names = map[ID]string{Anthropic: "anthropic", OpenAIChat: "openai-chat", OpenAIResponses: "openai-responses", Gemini: "gemini"}

// Name is the protocol's name in scripts, logs and flags.
func (p ID) Name() string {
	n, ok := names[p]
	if !ok {
		panic("protocol: unknown ID")
	}
	return n
}

// Parse turns a name into an ID.
func Parse(name string) (ID, bool) {
	for _, p := range IDs {
		if p.Name() == name {
			return p, true
		}
	}
	return 0, false
}

// Op is the closed set of operations a request asks for.
type Op int

const (
	Generate    Op = iota + 1 // a model turn: answered from the script
	CountTokens               // a token count
	ListModels
	GetModel
	Probe // a start-up probe that wants an empty 200 (Claude Code's HEAD /api/hello)
)

// Decoded is a decoded HTTP request.
type Decoded struct {
	Op      Op
	Request core.Request // Generate and CountTokens
	Model   string       // GetModel
}

// OutKind is the closed set of outcomes a codec encodes.
type OutKind int

const (
	OutReply       OutKind = iota + 1 // Out.Reply, streamed or not as Meta.Stream says
	OutError                          // Out.Error as an HTTP error
	OutStreamError                    // a 200 stream that breaks with Out.Error after Out.Partial text (an HTTP error when not streamed)
	OutLimit                          // Out.Limit: a usage limit
	OutTokens                         // Out.Tokens: a token count
	OutModels                         // Out.Models: a model list (or one model for GetModel)
	OutProbe                          // an empty 200
)

// ErrorSpec is an error to encode.
type ErrorSpec struct {
	Kind    core.ErrorKind
	Message string // "" uses the protocol's default message for the kind
	Retry   core.Retry
	// RetryAfter is sent as the protocol's retry delay when HasRetryAfter.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// LimitSpec is a usage limit to encode.
type LimitSpec struct {
	Window core.Window
	Now    time.Time
	Resets time.Time
}

// Out is one outcome to encode; Kind says which field is read.
type Out struct {
	Kind    OutKind
	Reply   core.Reply
	Error   ErrorSpec
	Partial string
	Limit   LimitSpec
	Tokens  int
	Models  []string
}

// Meta names one response.
type Meta struct {
	// Seed makes every id of the response; the same seed gives the same ids.
	Seed      string
	RequestID string
	Model     string
	Stream    bool
	Usage     core.Usage
	Now       time.Time
	Op        Op
}

// Encoded is a response ready to send. Frames non-nil means a 200
// text/event-stream whose frames go out in order; otherwise Status and Body.
type Encoded struct {
	Status int
	Header http.Header
	Body   []byte
	Frames [][]byte
}

// ObservedKind is the closed set of responses a recorder reads back.
type ObservedKind int

const (
	SawReply ObservedKind = iota + 1
	SawError
	SawStreamError
	SawLimit
)

// Observed is a response read back by a recorder.
type Observed struct {
	Kind    ObservedKind
	Reply   core.Reply
	Error   core.ErrorKind
	Message string
	Window  core.Window
	// ResetsIn is how long until a limit resets, when the response says.
	ResetsIn time.Duration
}

// Codec is one wire protocol.
type Codec interface {
	ID() ID
	// Routes are the http.ServeMux patterns the protocol answers, such as "POST /v1/messages".
	Routes() []string
	// Accepts says whether a request on a route that several protocols share
	// (GET /v1/models) is this protocol's.
	Accepts(r *http.Request) bool
	// Decode reads a request on one of Routes.
	Decode(r *http.Request, body []byte) (Decoded, error)
	// Encode makes the response of one outcome.
	Encode(m Meta, o Out) Encoded
	// Observe reads back a response this protocol sent (or a real API sent), for recording.
	Observe(op Op, status int, h http.Header, body []byte) (Observed, bool)
}
