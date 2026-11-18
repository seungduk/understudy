package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/seungduk/understudy/core"
)

type errorInfo struct {
	status  int
	typ     string // error.type on the wire
	message string // default message
}

// errorTable is each core error kind as the Anthropic API sends it.
var errorTable = map[core.ErrorKind]errorInfo{
	core.InvalidRequest:  {400, "invalid_request_error", "Invalid request."},
	core.Authentication:  {401, "authentication_error", "invalid x-api-key"},
	core.Billing:         {402, "billing_error", "Your credit balance is too low to access the Anthropic API."},
	core.Permission:      {403, "permission_error", "Your API key does not have permission to use the specified resource."},
	core.NotFound:        {404, "not_found_error", "Not found."},
	core.RequestTooLarge: {413, "request_too_large", "Request exceeds the maximum allowed number of bytes."},
	core.RateLimit:       {429, "rate_limit_error", "Number of request tokens has exceeded your per-minute rate limit."},
	core.APIError:        {500, "api_error", "Internal server error"},
	core.Timeout:         {504, "timeout_error", "Request timed out."},
	core.Overloaded:      {529, "overloaded_error", "Overloaded"},
}

func info(k core.ErrorKind) errorInfo {
	i, ok := errorTable[k]
	if !ok {
		panic(fmt.Sprintf("anthropic: unknown ErrorKind %d", int(k)))
	}
	return i
}

// Status is the HTTP status the real API uses for the kind.
func Status(k core.ErrorKind) int { return info(k).status }

// Type is the error.type on the wire, such as "overloaded_error".
func Type(k core.ErrorKind) string { return info(k).typ }

func kindOfType(typ string) (core.ErrorKind, bool) {
	for _, k := range core.ErrorKinds {
		if Type(k) == typ {
			return k, true
		}
	}
	return 0, false
}

func kindOfStatus(status int) (core.ErrorKind, bool) {
	for _, k := range core.ErrorKinds {
		if Status(k) == status {
			return k, true
		}
	}
	return 0, false
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
func ErrorBody(k core.ErrorKind, message, requestID string) []byte {
	if message == "" {
		message = info(k).message
	}
	b, _ := json.Marshal(errorEnvelope{Type: "error", Error: errorPayload{Type: Type(k), Message: message}, RequestID: requestID})
	return b
}

// errorEventData is the data of an in-stream "event: error" frame, which has no request_id.
func errorEventData(k core.ErrorKind, message string) []byte {
	if message == "" {
		message = info(k).message
	}
	b, _ := json.Marshal(errorEnvelope{Type: "error", Error: errorPayload{Type: Type(k), Message: message}})
	return b
}
