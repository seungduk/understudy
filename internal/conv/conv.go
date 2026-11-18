// Package conv reads, from one neutral request, what the script engine needs:
// the session key, the tool results before every assistant turn (read by the
// harness profile) and the client's working directory. It keeps no state and
// knows no wire protocol.
package conv

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/script"
)

// SessionHeader is the request header that names the session.
const SessionHeader = "X-Understudy-Session"

// HarnessHeader is the request header that names the harness, when the script does not.
const HarnessHeader = "X-Understudy-Harness"

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

// Key is the session a request names.
type Key struct {
	Key    string
	Source KeySource
	// Bad is set when the request names a session in a form a key cannot have.
	Bad string
}

var markerRe = regexp.MustCompile(`\[understudy-session:\s*([^\]\s]*)\s*\]`)

// SessionKey reads the session key: the header, else the marker in the system
// prompt (or a system turn), else the marker in the user text before the first
// assistant turn, else "default".
func SessionKey(h http.Header, r core.Request) Key {
	check := func(k string, src KeySource) Key {
		if !script.ValidSessionKey(k) {
			return Key{Source: src, Bad: k}
		}
		return Key{Key: k, Source: src}
	}
	if hv := strings.TrimSpace(h.Get(SessionHeader)); hv != "" {
		return check(hv, FromHeader)
	}
	if m := markerRe.FindStringSubmatch(r.System); m != nil {
		return check(m[1], FromSystemPrompt)
	}
	for _, t := range r.Turns {
		if t.Role != core.System {
			continue
		}
		for _, p := range t.Parts {
			if m := markerRe.FindStringSubmatch(p.Text); p.Kind == core.Text && m != nil {
				return check(m[1], FromSystemPrompt)
			}
		}
	}
	for _, t := range r.Turns {
		if t.Role == core.Assistant {
			break
		}
		if t.Role != core.User {
			continue
		}
		for _, p := range t.Parts {
			if m := markerRe.FindStringSubmatch(p.Text); p.Kind == core.Text && m != nil {
				return check(m[1], FromUserPrompt)
			}
		}
	}
	return Key{Key: DefaultSession, Source: FromDefault}
}

// History is the tool results before every assistant turn: History[k] before
// assistant turn k, and len-1 is the turn now asked for. Each result is read by
// the profile (its tool's action, error and exit code).
func History(r core.Request, pr *harness.Profile) [][]script.Result {
	calls := map[string]core.Part{}
	for _, t := range r.Turns {
		if t.Role != core.Assistant {
			continue
		}
		for _, p := range t.Parts {
			if p.Kind == core.ToolCall {
				calls[p.CallID] = p
			}
		}
	}
	out := [][]script.Result{nil}
	for _, t := range r.Turns {
		if t.Role == core.Assistant {
			out = append(out, nil)
			continue
		}
		for _, p := range t.Parts {
			if p.Kind != core.ToolResult {
				continue
			}
			call, known := calls[p.CallID]
			tool := p.Tool
			if tool == "" {
				tool = call.Tool
			}
			action := pr.ActionOf(tool)
			if known && call.Tool == tool {
				action = pr.ActionOfCall(call)
			}
			rd := pr.Read(tool, p.Text, p.Mark)
			i := len(out) - 1
			out[i] = append(out[i], script.Result{Tool: tool, Action: action, IsError: rd.IsError, Text: p.Text, Exit: rd.Exit, HasExit: rd.HasExit})
		}
	}
	return out
}

// WorkingDir is the client's working directory as the prompt states it, "" when it does not.
func WorkingDir(r core.Request, pr *harness.Profile) string {
	texts := []string{r.System}
	for _, t := range r.Turns {
		if t.Role == core.System || t.Role == core.User {
			for _, p := range t.Parts {
				if p.Kind == core.Text {
					texts = append(texts, p.Text)
				}
			}
		}
	}
	return pr.WorkingDir(texts)
}

// Prompt is the text parts of the newest user turn, when no assistant turn
// follows it, joined by a blank line: the task at turn 0, and text sent along
// with tool results later. System turns are skipped.
func Prompt(r core.Request) string {
	for i := len(r.Turns) - 1; i >= 0; i-- {
		switch r.Turns[i].Role {
		case core.Assistant:
			return ""
		case core.User:
			var parts []string
			for _, p := range r.Turns[i].Parts {
				if p.Kind == core.Text && strings.TrimSpace(p.Text) != "" {
					parts = append(parts, p.Text)
				}
			}
			return strings.Join(parts, "\n\n")
		}
	}
	return ""
}
