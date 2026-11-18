package script

// The schema versions this package reads. v1 is the first format (Claude Code
// tool names only); v2 adds the harness field, neutral actions (shell, read,
// write, edit) and the daily and per_minute limit windows. A v1 script means
// exactly what it meant before.
const (
	SchemaV1 = "understudy/v1"
	SchemaV2 = "understudy/v2"
)

// File is one script file, exactly as written.
type File struct {
	Schema     string      `yaml:"schema" json:"schema"`
	Divergence Divergence  `yaml:"divergence,omitempty" json:"divergence,omitempty"`
	Provenance *Provenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`
	// Harness (v2) names the harness every session runs in, such as codex; a
	// session's own harness overrides it. "" lets the server tell from the request.
	Harness  string             `yaml:"harness,omitempty" json:"harness,omitempty"`
	Sessions map[string]Session `yaml:"sessions" json:"sessions"`
}

// Divergence says what happens when a request does not fit the script: a step's
// expect fails, no case matches, a tool is not offered, or the script has ended.
type Divergence string

const (
	// Strict answers the diverging request with a 400 error that names the
	// divergence and tells the client not to retry, so the run stops there.
	Strict Divergence = "strict"
	// Lenient logs the divergence and goes on: a failed expect is ignored, no
	// matching case takes the step's first case, a script that has ended repeats
	// its last step's reply.
	Lenient Divergence = "lenient"
)

// Provenance says where a script came from.
type Provenance struct {
	Source     Source `yaml:"source" json:"source"`
	Run        string `yaml:"run,omitempty" json:"run,omitempty"`
	Task       string `yaml:"task,omitempty" json:"task,omitempty"`
	Seat       string `yaml:"seat,omitempty" json:"seat,omitempty"`
	Session    string `yaml:"session,omitempty" json:"session,omitempty"`
	RecordedAt string `yaml:"recorded_at,omitempty" json:"recorded_at,omitempty"`
	CLIVersion string `yaml:"cli_version,omitempty" json:"cli_version,omitempty"`
	Note       string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Source is how a script was made.
type Source string

const (
	Written  Source = "written"
	Recorded Source = "recorded"
)

// Session is the ordered steps of one session. Step i answers the request whose
// history holds i assistant turns, unless a next jump moved the position.
type Session struct {
	// Harness (v2) names the harness this session runs in.
	Harness string `yaml:"harness,omitempty" json:"harness,omitempty"`
	Steps   []Step `yaml:"steps" json:"steps"`
}

// Step answers one turn. It has exactly one of Reply, Attempts or Cases.
type Step struct {
	Name     string   `yaml:"name" json:"name"`
	Expect   *Matcher `yaml:"expect,omitempty" json:"expect,omitempty"`
	Reply    *Reply   `yaml:"reply,omitempty" json:"reply,omitempty"`
	Attempts []Reply  `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	Next     string   `yaml:"next,omitempty" json:"next,omitempty"`
	Cases    []Case   `yaml:"cases,omitempty" json:"cases,omitempty"`
	// Otherwise answers when no case matches. Without it, no match is a divergence.
	Otherwise *Branch `yaml:"otherwise,omitempty" json:"otherwise,omitempty"`
}

// Case is one branch of a step: the first case whose When matches answers.
type Case struct {
	When     *Matcher `yaml:"when" json:"when"`
	Reply    *Reply   `yaml:"reply,omitempty" json:"reply,omitempty"`
	Attempts []Reply  `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	Next     string   `yaml:"next,omitempty" json:"next,omitempty"`
}

// Branch is what a step answers: exactly one of Reply or Attempts, then Next.
type Branch struct {
	Reply    *Reply  `yaml:"reply,omitempty" json:"reply,omitempty"`
	Attempts []Reply `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	Next     string  `yaml:"next,omitempty" json:"next,omitempty"`
}

// Matcher tests the tool results that the request carries (the results of the
// previous turn's tool calls). Every given field must hold for the same result;
// the matcher matches when any result passes. At least one field is required.
type Matcher struct {
	Tool string `yaml:"tool,omitempty" json:"tool,omitempty"`
	// Action (v2) is the neutral action the result answers: shell, read, write or edit.
	Action   string  `yaml:"action,omitempty" json:"action,omitempty"`
	Result   Outcome `yaml:"result,omitempty" json:"result,omitempty"`
	ExitCode *int    `yaml:"exit_code,omitempty" json:"exit_code,omitempty"`
	Contains string  `yaml:"contains,omitempty" json:"contains,omitempty"`
	Regex    string  `yaml:"regex,omitempty" json:"regex,omitempty"`
}

// Outcome is whether a tool result is an error.
type Outcome string

const (
	OK    Outcome = "ok"
	Error Outcome = "error"
)

// Reply is one answer of the fake model. It has exactly one kind (Message,
// ToolUse, HTTPError, StreamError, Limit) and optional delivery faults.
type Reply struct {
	Message     *Message     `yaml:"message,omitempty" json:"message,omitempty"`
	ToolUse     *ToolUse     `yaml:"tool_use,omitempty" json:"tool_use,omitempty"`
	HTTPError   *HTTPError   `yaml:"http_error,omitempty" json:"http_error,omitempty"`
	StreamError *StreamError `yaml:"stream_error,omitempty" json:"stream_error,omitempty"`
	Limit       *Limit       `yaml:"limit,omitempty" json:"limit,omitempty"`

	Stall *Stall `yaml:"stall,omitempty" json:"stall,omitempty"`
	Cut   *Cut   `yaml:"cut,omitempty" json:"cut,omitempty"`
	Pace  *Pace  `yaml:"pace,omitempty" json:"pace,omitempty"`
}

// Message is a final answer: the model's turn ends.
type Message struct {
	Thinking string `yaml:"thinking,omitempty" json:"thinking,omitempty"`
	Text     string `yaml:"text" json:"text"`
	Stop     Stop   `yaml:"stop,omitempty" json:"stop,omitempty"`
}

// Stop is why a final message ended.
type Stop string

const (
	EndTurn   Stop = "end_turn"
	MaxTokens Stop = "max_tokens"
)

// ToolUse is a turn that calls tools; the client runs them and sends the results.
type ToolUse struct {
	Thinking string `yaml:"thinking,omitempty" json:"thinking,omitempty"`
	Text     string `yaml:"text,omitempty" json:"text,omitempty"`
	Calls    []Call `yaml:"calls" json:"calls"`
}

// Call is one tool call: either a native call (Tool and Input, sent as
// written) or (v2) exactly one neutral action, which the harness profile turns
// into its own tool call. The text {{cwd}} in any string becomes the client's
// working directory.
type Call struct {
	Tool  string         `yaml:"tool,omitempty" json:"tool,omitempty"`
	Input map[string]any `yaml:"input,omitempty" json:"input,omitempty"`

	Shell *ShellAction `yaml:"shell,omitempty" json:"shell,omitempty"`
	Read  *ReadAction  `yaml:"read,omitempty" json:"read,omitempty"`
	Write *WriteAction `yaml:"write,omitempty" json:"write,omitempty"`
	Edit  *EditAction  `yaml:"edit,omitempty" json:"edit,omitempty"`
}

// ShellAction runs a shell command in the session's working directory.
type ShellAction struct {
	Command string `yaml:"command" json:"command"`
}

// ReadAction reads a file.
type ReadAction struct {
	Path string `yaml:"path" json:"path"`
}

// WriteAction creates or overwrites a file.
type WriteAction struct {
	Path    string `yaml:"path" json:"path"`
	Content string `yaml:"content" json:"content"`
}

// EditAction replaces the text Old with New in a file. Old must occur once.
// For harnesses whose only edit tool is a patch (Codex), Old and New are whole lines.
type EditAction struct {
	Path string `yaml:"path" json:"path"`
	Old  string `yaml:"old" json:"old"`
	New  string `yaml:"new" json:"new"`
}

// HTTPError answers with an HTTP error status and the real API's error body.
type HTTPError struct {
	Error             string `yaml:"error" json:"error"`
	Message           string `yaml:"message,omitempty" json:"message,omitempty"`
	RetryAfterSeconds *int   `yaml:"retry_after_seconds,omitempty" json:"retry_after_seconds,omitempty"`
	Retry             Retry  `yaml:"retry,omitempty" json:"retry,omitempty"`
}

// Retry is the x-should-retry header the error carries.
type Retry string

const (
	RetryDefault Retry = "default" // no header: the client decides by status
	RetryNever   Retry = "never"   // x-should-retry: false
	RetryAlways  Retry = "always"  // x-should-retry: true
)

// StreamError starts a 200 stream, optionally streams partial text, then sends
// an in-stream "event: error" and ends without message_stop.
type StreamError struct {
	Error       string `yaml:"error" json:"error"`
	Message     string `yaml:"message,omitempty" json:"message,omitempty"`
	PartialText string `yaml:"partial_text,omitempty" json:"partial_text,omitempty"`
}

// Limit answers as an account's usage limit does, in each protocol's own form
// (Anthropic: a 429 with the unified rate-limit headers; OpenAI: a 429
// usage_limit_reached; Gemini: a 429 RESOURCE_EXHAUSTED with quota details).
// See docs/limits.md for what each harness shows.
type Limit struct {
	Window          Window `yaml:"window" json:"window"`
	ResetsInSeconds int    `yaml:"resets_in_seconds" json:"resets_in_seconds"`
}

// Window is the usage window that ran out.
type Window string

const (
	FiveHour       Window = "five_hour"
	SevenDay       Window = "seven_day"
	SevenDayOpus   Window = "seven_day_opus"
	SevenDaySonnet Window = "seven_day_sonnet"
	Daily          Window = "daily"      // v2: a per-day quota (Gemini requests per day)
	PerMinute      Window = "per_minute" // v2: a per-minute rate limit that says when to retry
)

// Stall holds the reply: for Seconds, or until the named Gate is released
// through the control API. Exactly one of Seconds or Gate.
type Stall struct {
	Seconds int     `yaml:"seconds,omitempty" json:"seconds,omitempty"`
	Gate    string  `yaml:"gate,omitempty" json:"gate,omitempty"`
	At      StallAt `yaml:"at,omitempty" json:"at,omitempty"`
}

// StallAt is where the stall happens.
type StallAt string

const (
	BeforeResponse StallAt = "before_response" // before any byte of the response
	MidStream      StallAt = "mid_stream"      // after message_start and the first content event
)

// Cut ends the connection after AfterEvents SSE events (0: before the response
// headers). Close ends it cleanly (FIN); Reset aborts it (TCP RST).
type Cut struct {
	AfterEvents int    `yaml:"after_events" json:"after_events"`
	How         CutHow `yaml:"how" json:"how"`
}

// CutHow is how a cut ends the connection.
type CutHow string

const (
	Close CutHow = "close"
	Reset CutHow = "reset"
)

// Pace slows a reply: FirstEventMS before the first byte, BetweenEventsMS between events.
type Pace struct {
	FirstEventMS    int `yaml:"first_event_ms,omitempty" json:"first_event_ms,omitempty"`
	BetweenEventsMS int `yaml:"between_events_ms,omitempty" json:"between_events_ms,omitempty"`
}
