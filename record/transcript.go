package record

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/internal/protocol/anthropic"
	"github.com/seungduk/understudy/script"
)

// Meta is what the caller knows about the recorded session.
type Meta struct {
	// SessionKey is the key the script answers under; "" means "default".
	SessionKey      string
	Run, Task, Seat string
	// RecordedAt is when the session ran; zero means unknown.
	RecordedAt time.Time
	// CWD is the session's working directory; "" takes it from the transcript's init line.
	// Every occurrence in tool inputs becomes {{cwd}}, so the script replays in another folder.
	CWD string
	// Divergence is the replay policy written into the script; "" means strict.
	Divergence script.Divergence
}

// Report says what the conversion did besides the steps.
type Report struct {
	Steps      int      `json:"steps"`
	Retries    int      `json:"retries"` // failed API attempts recorded as attempts before a reply
	CLIVersion string   `json:"cli_version,omitempty"`
	Session    string   `json:"session,omitempty"` // Claude Code's session id
	Notes      []string `json:"notes,omitempty"`
}

type tEvent struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	CWD             string          `json:"cwd"`
	CLIVersion      string          `json:"claude_code_version"` // stream-json init line
	Version         string          `json:"version"`             // a session file record
	SessionID       string          `json:"session_id"`          // stream-json
	SessionFileID   string          `json:"sessionId"`           // a session file record
	IsSidechain     bool            `json:"isSidechain"`         // a session file record of a subagent
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	Error           string          `json:"error"`
	ErrorStatus     int             `json:"error_status"`
	APIErrorStatus  int             `json:"api_error_status"`
	Result          json.RawMessage `json:"result"`
	IsError         bool            `json:"is_error"`
}

type tMessage struct {
	ID      string           `json:"id"`
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

// turn is one assistant reply being collected.
type turn struct {
	id       string
	thinking []string
	text     []string
	calls    []script.Call
	errReply *script.Reply // a synthetic assistant message for an API error
	retries  []script.Reply
	expect   *script.Matcher
}

// FromTranscript reads a Claude Code `--output-format stream-json --verbose`
// transcript and gives a script whose steps are the model's real replies.
func FromTranscript(r io.Reader, m Meta) (script.File, Report, error) {
	var rep Report
	var turns []*turn
	var cur *turn
	var pendingRetries []script.Reply
	var lastResults []result
	cwd := m.CWD
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return script.File{}, rep, fmt.Errorf("cannot read the transcript: %v", err)
	}
	line := 0
	flush := func() {
		if cur != nil {
			turns = append(turns, cur)
			cur = nil
		}
	}
	for _, text := range lines {
		line++
		raw := strings.TrimSpace(text)
		if raw == "" {
			continue
		}
		var ev tEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return script.File{}, rep, fmt.Errorf("line %d is not JSON: %v. Fix: give the stream-json output of 'claude -p --output-format stream-json --verbose' or a Claude Code session file (~/.claude/projects/*/*.jsonl)", line, err)
		}
		// A session file names the working directory, the CLI's version and the session on every record.
		if ev.SessionFileID != "" {
			if cwd == "" {
				cwd = ev.CWD
			}
			if rep.CLIVersion == "" {
				rep.CLIVersion = ev.Version
			}
			if rep.Session == "" {
				rep.Session = ev.SessionFileID
			}
		}
		if ev.IsSidechain {
			rep.Notes = append(rep.Notes, fmt.Sprintf("line %d: skipped a subagent record (isSidechain); record subagents as their own sessions", line))
			continue
		}
		if ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
			rep.Notes = append(rep.Notes, fmt.Sprintf("line %d: skipped a subagent event (parent tool use %s); record subagents as their own sessions", line, *ev.ParentToolUseID))
			continue
		}
		switch ev.Type {
		case "system":
			switch ev.Subtype {
			case "init":
				if cwd == "" {
					cwd = ev.CWD
				}
				rep.CLIVersion, rep.Session = ev.CLIVersion, ev.SessionID
			case "api_retry":
				if k, ok := kindOfStatus(ev.ErrorStatus); ok {
					pendingRetries = append(pendingRetries, script.Reply{HTTPError: &script.HTTPError{Error: k.Name()}})
				} else {
					rep.Notes = append(rep.Notes, fmt.Sprintf("line %d: a retry after a connection error (status %d) is not recorded", line, ev.ErrorStatus))
				}
			}
		case "assistant":
			var msg tMessage
			if err := json.Unmarshal(ev.Message, &msg); err != nil {
				return script.File{}, rep, fmt.Errorf("line %d: the assistant message is not readable: %v", line, err)
			}
			if cur != nil && msg.ID != cur.id {
				flush()
			}
			if cur == nil {
				cur = &turn{id: msg.ID, retries: pendingRetries, expect: expectFor(claudeResults(lastResults))}
				pendingRetries = nil
			}
			if ev.Error != "" {
				cur.errReply = errorReply(ev.Error, textOf(msg))
				continue
			}
			for _, b := range msg.Content {
				switch b["type"] {
				case "thinking":
					if s, _ := b["thinking"].(string); s != "" {
						cur.thinking = append(cur.thinking, s)
					}
				case "text":
					if s, _ := b["text"].(string); s != "" {
						cur.text = append(cur.text, s)
					}
				case "tool_use":
					name, _ := b["name"].(string)
					input, _ := b["input"].(map[string]any)
					if input == nil {
						input = map[string]any{}
					}
					cur.calls = append(cur.calls, script.Call{Tool: name, Input: input})
				}
			}
		case "user":
			flush()
			var msg tMessage
			if err := json.Unmarshal(ev.Message, &msg); err != nil {
				continue // a plain prompt string
			}
			lastResults = nil
			for _, b := range msg.Content {
				if b["type"] != "tool_result" {
					continue
				}
				isErr, _ := b["is_error"].(bool)
				lastResults = append(lastResults, result{text: contentText(b["content"]), isError: isErr})
			}
		}
	}
	flush()
	if len(turns) == 0 {
		return script.File{}, rep, fmt.Errorf("the transcript has no assistant replies. Fix: give the stream-json output of a session that ran at least one turn (with --verbose)")
	}
	steps := make([]script.Step, 0, len(turns))
	for i, t := range turns {
		st := script.Step{Name: fmt.Sprintf("turn-%d", i+1), Expect: t.expect}
		var reply script.Reply
		switch {
		case t.errReply != nil:
			reply = *t.errReply
		case len(t.calls) > 0:
			for j := range t.calls {
				if cwd != "" {
					t.calls[j].Input = rebase(t.calls[j].Input, cwd).(map[string]any)
				}
			}
			reply = script.Reply{ToolUse: &script.ToolUse{Thinking: strings.Join(t.thinking, "\n"), Text: strings.Join(t.text, "\n"), Calls: t.calls}}
		default:
			text := strings.Join(t.text, "\n")
			if text == "" {
				text = "(no text)"
				rep.Notes = append(rep.Notes, fmt.Sprintf("turn %d: the reply had no text; wrote (no text)", i+1))
			}
			reply = script.Reply{Message: &script.Message{Thinking: strings.Join(t.thinking, "\n"), Text: text}}
		}
		if len(t.retries) > 0 {
			rep.Retries += len(t.retries)
			st.Attempts = append(append([]script.Reply{}, t.retries...), reply)
		} else {
			st.Reply = &reply
		}
		steps = append(steps, st)
	}
	rep.Steps = len(steps)
	key := m.SessionKey
	if key == "" {
		key = "default"
	}
	prov := &script.Provenance{Source: script.Recorded, Run: m.Run, Task: m.Task, Seat: m.Seat, Session: rep.Session, CLIVersion: rep.CLIVersion}
	if !m.RecordedAt.IsZero() {
		prov.RecordedAt = m.RecordedAt.UTC().Format(time.RFC3339)
	}
	f := script.File{Schema: script.SchemaV1, Divergence: m.Divergence, Provenance: prov, Sessions: map[string]script.Session{key: {Steps: steps}}}
	if _, err := script.Compile(f); err != nil {
		return f, rep, fmt.Errorf("the recorded script does not check: %v", err)
	}
	return f, rep, nil
}

type result struct {
	text    string
	isError bool
}

// claudeResults reads transcript results as Claude Code's profile does.
func claudeResults(rs []result) []script.Result {
	pr, _ := harness.Lookup(string(harness.ClaudeCode))
	out := make([]script.Result, len(rs))
	for i, r := range rs {
		mark := core.MarkedOK
		if r.isError {
			mark = core.MarkedError
		}
		rd := pr.Read("Bash", r.text, mark)
		out[i] = script.Result{IsError: rd.IsError, Text: r.text, Exit: rd.Exit, HasExit: rd.HasExit && r.isError}
	}
	return out
}

// expectFor is the expectation a replay checks before the next reply: the same
// outcome as in the recording (an error, with its exit code when it shows one, or ok).
func expectFor(rs []script.Result) *script.Matcher {
	if len(rs) == 0 {
		return nil
	}
	for _, r := range rs {
		if r.IsError {
			m := &script.Matcher{Result: script.Error}
			if r.HasExit {
				code := r.Exit
				m.ExitCode = &code
			}
			return m
		}
	}
	return &script.Matcher{Result: script.OK}
}

var limitTexts = []struct {
	re *regexp.Regexp
	w  script.Window
}{
	{regexp.MustCompile(`hit your (5-hour|session) limit`), script.FiveHour},
	{regexp.MustCompile(`hit your weekly limit`), script.SevenDay},
	{regexp.MustCompile(`hit your Opus limit`), script.SevenDayOpus},
	{regexp.MustCompile(`hit your Sonnet limit`), script.SevenDaySonnet},
}

// errorReply turns Claude Code's synthetic assistant message for an API error
// back into the error the server sent.
func errorReply(kind, text string) *script.Reply {
	for _, l := range limitTexts {
		if l.re.MatchString(text) {
			return &script.Reply{Limit: &script.Limit{Window: l.w, ResetsInSeconds: 3600}}
		}
	}
	name := "api_error"
	switch kind {
	case "rate_limit":
		name = "rate_limit"
	case "authentication_failed":
		name = "authentication"
	case "billing_error":
		name = "billing"
	case "invalid_request":
		name = "invalid_request"
	case "server_error":
		name = "api_error"
	}
	return &script.Reply{HTTPError: &script.HTTPError{Error: name, Retry: script.RetryNever}}
}

func kindOfStatus(status int) (core.ErrorKind, bool) {
	for _, k := range core.ErrorKinds {
		if anthropic.Status(k) == status {
			return k, true
		}
	}
	return 0, false
}

func textOf(m tMessage) string {
	var parts []string
	for _, b := range m.Content {
		if s, ok := b["text"].(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func contentText(c any) string {
	switch x := c.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, e := range x {
			if m, ok := e.(map[string]any); ok && m["type"] == "text" {
				s, _ := m["text"].(string)
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// rebase replaces the recorded working directory with {{cwd}} in every string.
func rebase(v any, cwd string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, cwd, "{{cwd}}")
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = rebase(e, cwd)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = rebase(e, cwd)
		}
		return out
	}
	return v
}
