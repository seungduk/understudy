# Architecture

understudy plays the model for real agent harnesses. One script format runs
over several wire protocols and several harnesses, so the code is cut into
layers that know as little as possible about each other:

```
       cmd/understudy   mcpserver   control   ui   understudytest   explain
                        \           |         |            |          /
                 server (HTTP, control API)      record (proxy, transcripts)
                        |  protocol-agnostic: uses only the interfaces below
          +-------------+--------------+-----------------------------+
          |             |              |                             |
   internal/conv   internal/deliver  internal/protocol/registry   harness
   (session key,   (stalls, gates,    |  (the list of codecs)      (profiles: data)
    history,        cuts, pacing)     |                              |
    cwd)                              +---------+---------+-------+  |
          |                           |         |         |       |  |
          |                     anthropic  openaichat  responses gemini
          |                           \         \  openai /       /  |
          |                            \     (shared OpenAI errors)  |
          |                             \_________|_________/        |
          |                                       |                  |
          |                                   protocol  <------------+
          |                               (Codec interface, IDs)
          |                                       |
        script (engine: steps, cases, attempts, gates, divergence)
          |                                       |
          +------------------- core --------------+
              (turns, parts, replies, error kinds, windows, actions)
```

Every arrow points down. `internal/layers` checks the table on every
`go test ./...` (and so in CI): a package may import only the packages its
row lists.

## The layers

**core** is the neutral model. A `Request` is a conversation of `Turn`s
(user, assistant, system) made of `Part`s: text, thinking, tool call, tool
result, other. A tool result carries the wire's own `Mark` (unmarked, ok,
error) because protocols differ: Anthropic marks every result, Gemini marks
errors, OpenAI marks none. A `Reply` is thinking, text, tool `Call`s (JSON or
freeform input) and a `Stop`. Errors are the closed enum `ErrorKind`, usage
windows the closed enum `Window`, and the neutral tool actions the closed enum
`Action` (shell, read, write, edit). core knows no protocol and no harness.

**script** is the engine: steps, attempts, cases, `next`, expectations, gates
and the divergence policy, over core values only. It reads tool results as
`script.Result` (tool, action, error, text, exit code), which the harness
profile fills in; it never parses a harness's output itself.

**protocol** is the one narrow interface every wire protocol implements:

```go
type Codec interface {
	ID() ID                                   // closed: anthropic, openai-chat, openai-responses, gemini
	Routes() []string                         // ServeMux patterns
	Accepts(r *http.Request) bool             // for a route two protocols share (GET /v1/models)
	Decode(r *http.Request, body []byte) (Decoded, error)       // -> core.Request and an Op
	Encode(m Meta, o Out) Encoded                               // core outcome -> body or SSE frames
	Observe(op Op, status int, h http.Header, body []byte) (Observed, bool) // a response read back, for recording
}
```

`Out` is a closed sum (reply, error, stream error, limit, tokens, models,
probe), so a codec answers every outcome the engine can produce, and nothing
else. **internal/protocol/{anthropic,openaichat,responses,gemini}** each
implement it over core alone (the two OpenAI ones share their error and limit
bodies in `internal/protocol/openai`). **internal/protocol/codectest** is the
contract each codec runs on itself: every reply shape round-trips through
`Encode` and `Observe`, streamed and not; every error kind, retry hint and
limit window reads back as itself; every frame is a well-formed SSE frame.

**harness** is the harnesses as data. A `Profile` says which protocols the
harness speaks, which offered tools identify it, how each neutral action
becomes its tool call (a JSON or freeform template), which action each of its
tools answers, how its results show errors and exit codes (regular
expressions measured on the real CLI), where its prompt states the working
directory, which side requests it makes (titles, routing) and how to launch
it against understudy (environment, config files, arguments, session key
carrier). The code in the package only reads that data. The set of harnesses
is closed: `Lookup` knows only the registered profiles, and the server refuses
a script that names another.

**internal/conv** reads a `core.Request` for the engine: the session key (the
`X-Understudy-Session` header, or the `[understudy-session: KEY]` marker in
the system prompt or the user text before the first assistant turn), the tool
results before each assistant turn (read by the profile) and the working
directory (found by the profile).

**server**, **record**, **control**, **mcpserver** and **cmd/understudy** sit
on top and are protocol-agnostic: the server routes each codec's patterns, decodes, picks
the profile (the script's `harness:`, else the `X-Understudy-Harness` header,
else detection by offered tools), answers side requests, runs the engine,
turns neutral actions into the profile's tool calls, and has the codec encode
the outcome; `internal/deliver` then sends it with its faults. The recording
proxy decodes requests and `Observe`s responses through the same codecs, so it
records every protocol.

## Adding a harness

A harness that speaks a protocol understudy has is one profile file in
`harness/`: an `ID` constant and a `register(&Profile{...})` in `init`. No
other code changes: the server detects it by its tools, maps actions with its
templates, reads its results with its rules, and `understudytest.Harness`
and `understudy setup` launch it from its launch data. Antigravity's CLI was
added last this way; its commit touches only `harness/antigravity.go`, its
test, a line of the e2e table and the docs (see the PR).

A harness that speaks a new protocol also needs one package under
`internal/protocol/` that implements `Codec` and passes `codectest`, an `ID`
constant in `protocol`, and its line in `internal/protocol/registry`.
