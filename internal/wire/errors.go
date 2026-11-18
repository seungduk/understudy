// Package wire holds the subset of the Anthropic Messages API wire format that
// Claude Code uses: request decoding, SSE events, message JSON and error bodies.
// It knows nothing about scripts or sessions.
package wire

import (
	"encoding/json"
	"fmt"
)

// ErrorKind is the closed set of API error classes. Each kind has exactly one
// HTTP status and one error.type, as the real API sends them.
type ErrorKind int

const (
	InvalidRequest ErrorKind = iota + 1
	Authentication
	Billing
	Permission
	NotFound
	RequestTooLarge
	RateLimit
	APIError
	Timeout
	Overloaded
)

// ErrorKinds lists every kind, in a fixed order.
var ErrorKinds = []ErrorKind{InvalidRequest, Authentication, Billing, Permission, NotFound, RequestTooLarge, RateLimit, APIError, Timeout, Overloaded}

type errorInfo struct {
	name    string // script name
	status  int
	typ     string // error.type on the wire
	message string // default message
}

var errorTable = map[ErrorKind]errorInfo{
	InvalidRequest:  {"invalid_request", 400, "invalid_request_error", "Invalid request."},
	Authentication:  {"authentication", 401, "authentication_error", "invalid x-api-key"},
	Billing:         {"billing", 402, "billing_error", "Your credit balance is too low to access the Anthropic API."},
	Permission:      {"permission", 403, "permission_error", "Your API key does not have permission to use the specified resource."},
	NotFound:        {"not_found", 404, "not_found_error", "Not found."},
	RequestTooLarge: {"request_too_large", 413, "request_too_large", "Request exceeds the maximum allowed number of bytes."},
	RateLimit:       {"rate_limit", 429, "rate_limit_error", "Number of request tokens has exceeded your per-minute rate limit."},
	APIError:        {"api_error", 500, "api_error", "Internal server error"},
	Timeout:         {"timeout", 504, "timeout_error", "Request timed out."},
	Overloaded:      {"overloaded", 529, "overloaded_error", "Overloaded"},
}

func (k ErrorKind) info() errorInfo {
	i, ok := errorTable[k]
	if !ok {
		panic(fmt.Sprintf("wire: unknown ErrorKind %d", int(k)))
	}
	return i
}

// Name is the kind's name in scripts, such as "overloaded".
func (k ErrorKind) Name() string { return k.info().name }

// Status is the HTTP status the real API uses for the kind.
func (k ErrorKind) Status() int { return k.info().status }

// Type is the error.type on the wire, such as "overloaded_error".
func (k ErrorKind) Type() string { return k.info().typ }

// DefaultMessage is the error.message used when a script gives none.
func (k ErrorKind) DefaultMessage() string { return k.info().message }

// ParseErrorKind turns a script name into a kind.
func ParseErrorKind(name string) (ErrorKind, bool) {
	for _, k := range ErrorKinds {
		if k.Name() == name {
			return k, true
		}
	}
	return 0, false
}

// ErrorKindNames lists the script names of every kind, in order.
func ErrorKindNames() []string {
	out := make([]string, len(ErrorKinds))
	for i, k := range ErrorKinds {
		out[i] = k.Name()
	}
	return out
}

type errorPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Type      string       `json:"type"`
	Error     errorPayload `json:"error"`
	RequestID string       `json:"request_id,omitempty"`
}

// ErrorBody is the JSON body of an error response:
// {"type":"error","error":{"type":...,"message":...},"request_id":...}.
// An empty message uses the kind's default.
func ErrorBody(k ErrorKind, message, requestID string) []byte {
	if message == "" {
		message = k.DefaultMessage()
	}
	b, _ := json.Marshal(errorEnvelope{Type: "error", Error: errorPayload{Type: k.Type(), Message: message}, RequestID: requestID})
	return b
}

// ErrorEventData is the data of an in-stream "event: error" frame, which has no request_id.
func ErrorEventData(k ErrorKind, message string) []byte {
	if message == "" {
		message = k.DefaultMessage()
	}
	b, _ := json.Marshal(errorEnvelope{Type: "error", Error: errorPayload{Type: k.Type(), Message: message}})
	return b
}
