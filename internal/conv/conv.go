// Package conv reads, from one Messages request, what the script engine needs:
// the session key, the tool results before every assistant turn, and the
// client's working directory. It keeps no state.
package conv

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/seungduk/understudy/internal/wire"
	"github.com/seungduk/understudy/script"
)

// SessionHeader is the request header that names the session.
const SessionHeader = "X-Understudy-Session"

// MarkerPrefix starts the session marker that can stand in the system prompt or
// the first user message instead of the header: [understudy-session: KEY].
const MarkerPrefix = "[understudy-session:"

// DefaultSession is the key of a request that names no session.
const DefaultSession = "default"

// KeySource is where a request's session key came from.
type KeySource int

const (
	FromHeader KeySource = iota + 1
	FromSystemPrompt
	FromUserPrompt
	FromDefault
)

func (s KeySource) String() string {
	switch s {
	case FromHeader:
		return "header"
	case FromSystemPrompt:
		return "system_prompt"
	case FromUserPrompt:
		return "user_prompt"
	case FromDefault:
		return "default"
	}
	return "unknown"
}

// View is what one request says.
type View struct {
	Key       string
	KeySource KeySource
	// BadKey is set when the request names a session in a form a key cannot have.
	BadKey string
	// History[k] is the tool results before assistant turn k; len(History)-1 is the turn now asked for.
	History [][]script.Result
	CWD     string // "" when the request does not say
}

var (
	markerRe = regexp.MustCompile(`\[understudy-session:\s*([^\]\s]*)\s*\]`)
	cwdRe    = regexp.MustCompile(`(?m)^\s*-?\s*(?:Primary working directory|Working directory):\s*(\S.*?)\s*$`)
)

// Read reads a request.
func Read(h http.Header, r wire.Request) View {
	v := View{}
	v.Key, v.KeySource, v.BadKey = key(h, r)
	v.History = history(r)
	v.CWD = cwd(r)
	return v
}

func key(h http.Header, r wire.Request) (string, KeySource, string) {
	check := func(k string, src KeySource) (string, KeySource, string) {
		if !script.ValidSessionKey(k) {
			return "", src, k
		}
		return k, src, ""
	}
	if hv := strings.TrimSpace(h.Get(SessionHeader)); hv != "" {
		return check(hv, FromHeader)
	}
	if m := markerRe.FindStringSubmatch(r.System); m != nil {
		return check(m[1], FromSystemPrompt)
	}
	for _, msg := range r.Messages {
		if msg.Role != wire.User {
			continue
		}
		for _, b := range msg.Blocks {
			if b.Kind == wire.TextBlock {
				if m := markerRe.FindStringSubmatch(b.Text); m != nil {
					return check(m[1], FromUserPrompt)
				}
			}
		}
		break // only the first user message
	}
	return DefaultSession, FromDefault, ""
}

func history(r wire.Request) [][]script.Result {
	names := map[string]string{}
	for _, m := range r.Messages {
		if m.Role != wire.Assistant {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == wire.ToolUseBlock {
				names[b.ToolUseID] = b.ToolName
			}
		}
	}
	out := [][]script.Result{nil}
	for _, m := range r.Messages {
		if m.Role == wire.Assistant {
			out = append(out, nil)
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == wire.ToolResultBlock {
				i := len(out) - 1
				out[i] = append(out[i], script.Result{Tool: names[b.ToolUseID], IsError: b.IsError, Text: b.Text})
			}
		}
	}
	return out
}

func cwd(r wire.Request) string {
	texts := []string{r.System}
	for _, m := range r.Messages {
		if m.Role == wire.SystemRole || m.Role == wire.User {
			for _, b := range m.Blocks {
				if b.Kind == wire.TextBlock {
					texts = append(texts, b.Text)
				}
			}
		}
	}
	for _, t := range texts {
		if m := cwdRe.FindStringSubmatch(t); m != nil {
			return m[1]
		}
	}
	return ""
}
