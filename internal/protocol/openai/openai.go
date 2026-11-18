// Package openai is what the two OpenAI protocols (Chat Completions and
// Responses) share: the error bodies and statuses, the retry headers the
// OpenAI SDKs read, usage limits in the form Codex reads, and the model list.
// It translates core values only; each protocol package uses it for its errors.
package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/seungduk/understudy/core"
)

type errorInfo struct {
	status  int
	typ     string // error.type
	code    string // error.code ("" sends null)
	message string // the default message
}

// errorTable is each core error kind as the OpenAI API sends it.
var errorTable = map[core.ErrorKind]errorInfo{
	core.InvalidRequest:  {400, "invalid_request_error", "", "Invalid request."},
	core.Authentication:  {401, "invalid_request_error", "invalid_api_key", "Incorrect API key provided."},
	core.Billing:         {429, "insufficient_quota", "insufficient_quota", "You exceeded your current quota, please check your plan and billing details."},
	core.Permission:      {403, "invalid_request_error", "permission_denied", "You are not allowed to access this resource."},
	core.NotFound:        {404, "invalid_request_error", "model_not_found", "The model does not exist or you do not have access to it."},
	core.RequestTooLarge: {413, "invalid_request_error", "request_too_large", "Request too large."},
	core.RateLimit:       {429, "requests", "rate_limit_exceeded", "Rate limit reached for requests. Please try again in 1s."},
	core.APIError:        {500, "server_error", "", "The server had an error while processing your request. Sorry about that!"},
	core.Timeout:         {504, "server_error", "timeout", "Request timed out."},
	core.Overloaded:      {503, "server_error", "server_is_overloaded", "The server is overloaded or not ready yet."},
}

func info(k core.ErrorKind) errorInfo {
	i, ok := errorTable[k]
	if !ok {
		panic(fmt.Sprintf("openai: unknown ErrorKind %d", int(k)))
	}
	return i
}

// Status is the HTTP status of the kind.
func Status(k core.ErrorKind) int { return info(k).status }

// Code is the error.code of the kind ("" when the API sends null).
func Code(k core.ErrorKind) string { return info(k).code }

// Type is the error.type of the kind.
func Type(k core.ErrorKind) string { return info(k).typ }

// Message is message, or the kind's default when it is "".
func Message(k core.ErrorKind, message string) string {
	if message == "" {
		return info(k).message
	}
	return message
}

// ErrorObject is the "error" object: {message, type, param, code}.
func ErrorObject(k core.ErrorKind, message string) map[string]any {
	i := info(k)
	var code any
	if i.code != "" {
		code = i.code
	}
	return map[string]any{"message": Message(k, message), "type": i.typ, "param": nil, "code": code}
}

// ErrorBody is {"error": ErrorObject}.
func ErrorBody(k core.ErrorKind, message string) []byte {
	b, _ := json.Marshal(map[string]any{"error": ErrorObject(k, message)})
	return b
}

// RetryHeaders sets the headers the OpenAI SDKs read: x-should-retry, and
// retry-after with retry-after-ms.
func RetryHeaders(h http.Header, r core.Retry, after time.Duration, hasAfter bool) {
	switch r {
	case core.RetryNever:
		h.Set("X-Should-Retry", "false")
	case core.RetryAlways:
		h.Set("X-Should-Retry", "true")
	}
	if hasAfter {
		h.Set("Retry-After", strconv.Itoa(int(after/time.Second)))
		h.Set("Retry-After-Ms", strconv.FormatInt(after.Milliseconds(), 10))
	}
}

// KindOf reads an error response back: by error.code, then error.type, then status.
func KindOf(status int, body []byte) (core.ErrorKind, string, bool) {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	code, _ := env.Error.Code.(string)
	for _, k := range core.ErrorKinds {
		if i := info(k); i.code != "" && i.code == code && i.status == status {
			return k, env.Error.Message, true
		}
	}
	for _, k := range core.ErrorKinds {
		if i := info(k); i.status == status && i.typ == env.Error.Type {
			return k, env.Error.Message, true
		}
	}
	for _, k := range core.ErrorKinds {
		if info(k).status == status {
			return k, env.Error.Message, true
		}
	}
	return 0, "", false
}

// WindowMinutes is the length of a usage window, as Codex's limit headers give it.
func WindowMinutes(w core.Window) int {
	switch w {
	case core.FiveHour:
		return 300
	case core.Daily:
		return 1440
	case core.PerMinute:
		return 1
	}
	return 10080
}

func windowOfMinutes(m int) core.Window {
	for _, w := range core.Windows {
		if WindowMinutes(w) == m {
			return w
		}
	}
	return core.SevenDay
}

// Limit is a usage limit, as the OpenAI-protocol servers Codex knows send it.
// The account windows (five_hour, seven_day..., and daily) are a 429
// usage_limit_reached with resets_at and the x-codex-primary-* headers Codex
// reads, and x-should-retry: false for the OpenAI SDKs (without it they sleep
// for retry-after); per_minute is a 429 rate_limit_exceeded that says when to
// try again.
func Limit(w core.Window, now, resets time.Time) (int, http.Header, []byte) {
	h := http.Header{}
	secs := int(resets.Sub(now) / time.Second)
	if secs < 0 {
		secs = 0
	}
	switch w {
	case core.PerMinute:
		RetryHeaders(h, core.RetryDefault, time.Duration(secs)*time.Second, true)
		return 429, h, ErrorBody(core.RateLimit, fmt.Sprintf("Rate limit reached for requests per min (RPM). Please try again in %ds.", secs))
	}
	mins := strconv.Itoa(WindowMinutes(w))
	reset := strconv.FormatInt(resets.Unix(), 10)
	h.Set("X-Codex-Primary-Used-Percent", "100")
	h.Set("X-Codex-Primary-Window-Minutes", mins)
	h.Set("X-Codex-Primary-Reset-At", reset)
	h.Set("Retry-After", strconv.Itoa(secs))
	h.Set("X-Should-Retry", "false")
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"type": "usage_limit_reached", "code": "usage_limit_reached", "param": nil,
		"message":   "The usage limit has been reached",
		"plan_type": "plus", "resets_at": resets.Unix(), "resets_in_seconds": secs, "limit_window_minutes": WindowMinutes(w),
	}})
	return 429, h, b
}

// KindOfError reads an in-stream error object back by its code, then its type.
func KindOfError(typ, code string) (core.ErrorKind, bool) {
	for _, k := range core.ErrorKinds {
		if i := info(k); i.code != "" && i.code == code {
			return k, true
		}
	}
	for _, k := range core.ErrorKinds {
		if i := info(k); i.code == "" && i.typ == typ {
			return k, true
		}
	}
	return 0, false
}

var limitRe = regexp.MustCompile(`requests per min \(RPM\)\. Please try again in (\d+)s`)

// ObserveLimit reads a usage limit back, when a 429 is one.
func ObserveLimit(status int, h http.Header, body []byte) (core.Window, time.Duration, bool) {
	if status != 429 {
		return 0, 0, false
	}
	var env struct {
		Error struct {
			Type      string `json:"type"`
			Message   string `json:"message"`
			ResetsIn  *int   `json:"resets_in_seconds"`
			WindowMin *int   `json:"limit_window_minutes"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	switch {
	case env.Error.Type == "usage_limit_reached":
		w := core.SevenDay
		if env.Error.WindowMin != nil {
			w = windowOfMinutes(*env.Error.WindowMin)
		} else if m, err := strconv.Atoi(h.Get("X-Codex-Primary-Window-Minutes")); err == nil {
			w = windowOfMinutes(m)
		}
		var in time.Duration
		if env.Error.ResetsIn != nil {
			in = time.Duration(*env.Error.ResetsIn) * time.Second
		}
		return w, in, true
	case env.Error.Type == "requests" && limitRe.MatchString(env.Error.Message):
		m := limitRe.FindStringSubmatch(env.Error.Message)
		n, _ := strconv.Atoi(m[1])
		return core.PerMinute, time.Duration(n) * time.Second, true
	}
	return 0, 0, false
}

// Models is the body of GET /v1/models (or of one model, when one is true).
func Models(ids []string, one bool) []byte {
	obj := func(id string) map[string]any {
		return map[string]any{"id": id, "object": "model", "created": 1767225600, "owned_by": "understudy"}
	}
	if one && len(ids) == 1 {
		b, _ := json.Marshal(obj(ids[0]))
		return b
	}
	data := make([]any, len(ids))
	for i, id := range ids {
		data[i] = obj(id)
	}
	b, _ := json.Marshal(map[string]any{"object": "list", "data": data})
	return b
}
