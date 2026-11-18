# understudy

A small, exact, scriptable fake of the Anthropic Messages API, so the **real** Claude Code CLI
can run end to end with only the model faked. Claude Code reads and edits real files, runs real
commands, retries and streams its `stream-json` output as it always does; understudy plays the
model from a script. Use it for deterministic tests of agent factories, harnesses and anything
else that drives `claude -p`.

*An understudy learns the lead's part and plays it exactly when asked.*

- **Exact wire**: the SSE event sequence of the real API (`message_start` with the full message
  and usage, `content_block_start/delta/stop` for text, thinking with `signature_delta`, tool use
  with `input_json_delta` fragments, `ping`, `message_delta` with `stop_reason` and usage,
  `message_stop`), in-stream `error` events, the real error bodies and statuses,
  `POST /v1/messages` (streamed and not, with `?beta=true`), `/v1/messages/count_tokens`,
  `/v1/models`, and Claude Code's start-up `HEAD /api/hello`.
- **Scripts agents can write**: YAML with a JSON Schema, closed sets of reply kinds and tests,
  unknown fields refused, plain fix-it errors with path, line and rule
  ([SCRIPTS.md](SCRIPTS.md) is the one-page guide).
- **Stateless and concurrent**: each request's turn is read from the conversation it carries, so
  many sessions run at once and client retries are safe. Sessions are keyed explicitly by a
  header or a prompt marker.
- **Faults**: HTTP errors, in-stream errors, stalls (timed, or held until a gate is released),
  mid-stream disconnects, TCP resets, slow streams, and subscription usage limits that Claude Code
  reports as `You've hit your weekly limit`.
- **Record and replay**: turn a real session's transcript (or live traffic through a recording
  proxy) into a script, then replay it with a strict or lenient divergence policy.
- **Go library and test helper**: start the server inside `go test`, load scripts, drive the real
  CLI, assert on the request log, release gates, move a virtual clock.
- One dependency (`gopkg.in/yaml.v3`), Apache-2.0.

## Quick start

```sh
go install github.com/seungduk/understudy/cmd/understudy@latest

cat > hello.yaml <<'YAML'
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply: {message: {text: "Hello from the understudy."}}
YAML

understudy validate hello.yaml
understudy serve --script hello.yaml &          # http://127.0.0.1:8765
eval "$(understudy env)"                        # ANTHROPIC_BASE_URL + a dummy key, nothing real
claude -p --output-format stream-json --verbose "say hi" </dev/null
understudy explain --url http://127.0.0.1:8765  # which step answered each request, and why
```

`understudy env` prints `export`/`unset` lines: it points `ANTHROPIC_BASE_URL` at the server,
sets a dummy `ANTHROPIC_API_KEY`, turns off telemetry and non-essential traffic, and removes any
variable that could carry a real credential. Run the CLI with its own empty `CLAUDE_CONFIG_DIR`
so it never reads your login. understudy never needs or forwards a real credential, and the
request log never keeps `Authorization`, `x-api-key` or any header that looks like a secret.

## Scripts

```yaml
schema: understudy/v1
sessions:
  t2/writer:                       # session key: header X-Understudy-Session, or [understudy-session: KEY] in the prompt
    steps:
      - name: test                 # step 1 answers turn 1
        reply:
          tool_use:
            calls: [{tool: Bash, input: {command: "go test ./...", description: Run the tests}}]
      - name: judge                # step 2 answers the request that carries the test result
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "DONE: tests pass"}}
          - when: {tool: Bash, result: error, contains: FAIL}
            next: test             # fix, then test again
            reply:
              tool_use:
                calls: [{tool: Write, input: {file_path: "{{cwd}}/fix.go", content: "package main\n"}}]
```

Replies: `message`, `tool_use`, `http_error`, `stream_error`, `limit`. Tests on tool results:
`tool`, `result` (ok/error), `exit_code`, `contains`, `regex`. Retries at one turn: `attempts`.
Faults on any reply: `stall`, `cut`, `pace`. The full format, every field, and five complete
examples are in [SCRIPTS.md](SCRIPTS.md); the JSON Schema is
[script/understudy-v1.schema.json](script/understudy-v1.schema.json) (`understudy schema`).

`understudy validate FILE --json` prints `{ok, file, sessions, problems: [{path, line, rule,
message, fix}]}`; the exit code is 0 when the script checks.

## Faults

| Script | Wire | Claude Code 2.1.325 did |
|---|---|---|
| `http_error: {error: overloaded}` | 529 `overloaded_error` | retried (exit 0 when a later attempt answers) |
| `http_error: {error: api_error}` | 500 `api_error` | retried |
| `stream_error: {error: overloaded}` | 200, `message_start`, `ping`, `event: error` | retried the whole request |
| `cut: {after_events: 4, how: close}` | stream closed mid-way | retried |
| `cut: {after_events: 0, how: reset}` / `after_events: 3` | TCP RST before / during the stream | retried |
| `stall: {gate: g}` | the reply waits until `POST /_understudy/gates/g/release` | waited, then went on |
| `limit: {window: seven_day, ...}` | 429 with the unified headers (below) | `You've hit your weekly limit · resets <time>`, exit 1, no retry (gateway token) |
| strict divergence | 400 `invalid_request_error`, `x-should-retry: false` | retried 5 times, then `API Error: 400 understudy: the request does not fit the script: ...`, exit 1 |

## Account-limit simulation (measured)

Measured on 2026-11-18 with **Claude Code 2.1.325** (macOS arm64), against a probe server and then
the e2e suite:

- A 429 carrying these headers:

  ```
  x-should-retry: false
  retry-after: <seconds>
  anthropic-ratelimit-unified-status: rejected
  anthropic-ratelimit-unified-representative-claim: seven_day     (five_hour, seven_day_opus, seven_day_sonnet)
  anthropic-ratelimit-unified-reset: <unix seconds>
  anthropic-ratelimit-unified-7d-status: rejected                 (5h-* for five_hour)
  anthropic-ratelimit-unified-7d-reset: <unix seconds>
  anthropic-ratelimit-unified-7d-utilization: 1.0
  ```

  makes Claude Code print `You've hit your weekly limit · resets 10:37pm (Asia/Seoul)` (or
  `5-hour limit`, `Opus limit`), set `api_error: "usage_limit_reached"` on its result line, exit 1,
  and send no retry, **when it authenticates the way subscription and gateway accounts do**. It
  reads the unified headers only for claude.ai subscribers, or when `ANTHROPIC_BASE_URL` points
  somewhere other than `api.anthropic.com` and `ANTHROPIC_AUTH_TOKEN` is a JWT whose audience is
  `claude-gateway`. understudy's `--auth gateway` (`clienv.Gateway`) sends such a token: an
  unsigned JWT (`alg: none`) that nothing can verify, so it is not a credential.
- With a dummy API key, the same 429 is a plain rate limit:
  `API Error: Request rejected (429) · <message>`.
- Without `x-should-retry: false`, Claude Code retries the 429 (up to 10 times) in either mode.

Other measured facts (2.1.325): `claude -p` sends `HEAD /api/hello` at start even with
`ANTHROPIC_BASE_URL` set; each turn is one `POST /v1/messages?beta=true` with `stream: true`; the
conversation carries a `role: "system"` message holding `Primary working directory: <cwd>` (which
`{{cwd}}` uses); a failed Bash call comes back as `is_error: true` with `Exit code N` first; an Edit
without permission comes back as an error, so scripted runs pass `--dangerously-skip-permissions`;
`ANTHROPIC_CUSTOM_HEADERS` reaches every request; `--append-system-prompt` reaches the `system`
field.

## Record and replay

```sh
# a real session's transcript -> a script (tool inputs rebased on {{cwd}})
claude -p --output-format stream-json --verbose "fix the bug" > run.jsonl
understudy record --transcript run.jsonl --session t1/writer --run r42 --task t1 --seat writer \
  --recorded-at 2026-11-18T09:30:00Z --out t1-writer.yaml

# or record live traffic: forward to the real API, write the script on Ctrl-C
understudy record-proxy --upstream https://api.anthropic.com --addr 127.0.0.1:8766 --out rec.yaml
```

`record --transcript` reads two forms: the `stream-json` output of `claude -p --output-format
stream-json --verbose`, and a Claude Code session file (`~/.claude/projects/*/*.jsonl`). A tool
that keeps sessions in its own log extracts the session records into one of these first.
Subagent records are left out with a note.

A recorded script is an ordinary script, with `provenance` (source, run, task, seat, Claude Code
session id, time, CLI version). Each step `expect`s the outcome the recording saw (ok, or the
error and its exit code), so a replay whose tool results differ stops at the first divergence
(`divergence: strict`) or logs it and goes on (`lenient`). Retries the client made are recorded
as `attempts`; the usage-limit message becomes a `limit` reply. In Go: `record.FromTranscript`
and `record.NewProxy`. The proxy forwards credentials to the upstream and never stores them.

## Go

```go
import us "github.com/seungduk/understudy/understudytest"

func TestWriterSeat(t *testing.T) {
	fake := us.Start(t, us.ScriptFile("testdata/writer.yaml"))  // or us.YAML(...), us.Script(s), us.VirtualClock(t0)
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})
	run := us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/writer", Prompt: "change the greeting"})
	// run.Exit, run.Result, run.IsError, run.Events (stream-json), run.Version
	// fake.Messages() is the request log: session, turn, step, branch, attempt, outcome, trace
	// fake.Release("gate"), fake.WaitGate("gate", 1, time.Minute), fake.Advance(d), fake.Reset()
}
```

`us.Claude` runs `claude -p --output-format stream-json --verbose` with a fresh
`CLAUDE_CONFIG_DIR`, dummy credentials (`Auth: clienv.APIKey` or `clienv.Gateway`) and
`--dangerously-skip-permissions`; it skips the test, saying why, when `claude` is not installed
(`UNDERSTUDY_CLAUDE` names another binary). The server alone is `server.New(server.Options{...})`,
an `http.Handler`. See [examples/go/writer_test.go](examples/go/writer_test.go).

## Control API

On the same address, under `/_understudy/`:

| Call | Does |
|---|---|
| `GET health` | `{ok, version, script_loaded}` |
| `PUT script` (body: the YAML) | load a script; `400 {problems: [...]}` when it does not check |
| `GET script` | the loaded script as YAML |
| `GET requests[?session=KEY]` | the request log |
| `POST reset` | forget the log, attempt counts and gates (waiting requests are released) |
| `GET gates`, `POST gates/NAME/release` | gates and who waits on them |
| `GET clock`, `POST clock/advance {"ms": N}` | the virtual clock (`serve --clock virtual`) |

## Tests

```sh
go test -race ./...                  # unit and package tests, no network beyond loopback
go test -tags e2e -v ./e2e           # drives the real claude CLI (skips if it is not installed)
```

The e2e suite covers text, a tool loop editing a real file, branching on a failing test, retry
after 529, an in-stream error, close and reset mid-stream, a gated stall released by the test, the
weekly and 5-hour limit messages, the system-prompt session marker, strict divergence, and record
then replay (passing, then diverging).

### In an agent factory

understudy ran a whole practice run of an agent factory on four machines on 2026-11-18:
the real Claude Code 2.1.325 in microVMs on three Macs, every seat (planner, writer,
reviewer, merger) scripted by task and seat (`X-Understudy-Session: F-1/writer`), with the server
on a fourth machine. Two tasks were planned, written (real edits through scripted Read/Edit calls),
checked, reviewed and landed; their writers were held at a gate until both had written, so the
second landing conflicted and the scripted merger resolved it; one planner request got the
weekly limit, which the factory read as that machine's limit and moved the seat to another
machine. 19 requests, all served, none diverged.

### Claude Code versions tested

| Version | Platform | Result | Date |
|---|---|---|---|
| 2.1.325 | macOS 27 arm64 | e2e suite passes (11 tests) | 2026-11-18 |
| 2.1.326 | Linux x86_64 | e2e suite passes (11 tests) | 2026-11-18 |
| 2.1.325 | Linux arm64 microVMs on macOS | a full agent-factory practice run, 19 requests | 2026-11-18 |

### Scripts written by agents

The guide is tested on agents. Fresh agents get [SCRIPTS.md](SCRIPTS.md) and
[e2e/guide-tasks.md](e2e/guide-tasks.md) and nothing else, write five scripts blind (text only, a
tool loop that edits a file, a branch on a check, the weekly limit, a stall released by a gate),
and `UNDERSTUDY_GUIDE_DIR=<their folder> go test -tags e2e -run TestGuide ./e2e` runs each with the
real CLI. Round 1 (2026-11-18): Claude Opus and Claude Haiku each passed 4 of 5; both missed that
Claude Code reports a lone `test -f` exiting 1 as a success. The guide now has a table of what
Claude Code sends back for each tool; round 2: Claude Opus 5/5, Claude Haiku 5/5 and Codex
(codex-cli 0.167.0) 5/5, each on the first try.

## Learned from

understudy takes ideas, not code, from these projects (all MIT or Apache-2.0); see [NOTICE](NOTICE).

- [AkaraChen/fakellm](https://github.com/AkaraChen/fakellm): the event sequence and where `ping`
  goes, measured Claude Code retry behaviour (`docs/wire.md`), reading the conversation position
  from the request history so retries and concurrency are safe, close versus reset cuts, the
  request-log record.
- [majorcontext/harness](https://github.com/majorcontext/harness) (`harnesstest`): named steps and
  block/release gates for deterministic stalls.
- [acus9345/mock-llm-server](https://github.com/acus9345/mock-llm-server): the small reply model
  (text, reasoning, tool calls, error, stream error).
- [piyook/llm-mock](https://github.com/piyook/llm-mock), [theblixguy/llm-mock-server](https://github.com/theblixguy/llm-mock-server),
  [kagent-dev/mockllm](https://github.com/kagent-dev/mockllm): fault and matching vocabularies.
- [sloprail/harness-mocks](https://github.com/sloprail/harness-mocks): a corpus of recorded real
  Claude Code runs (a fake CLI, the opposite approach: it replaces the CLI, understudy keeps it).

What understudy adds: an explicit session key, closed reply kinds and tests instead of free-form
regex rules and embedded scripting, event-gated stalls, the measured subscription-limit
simulation, and record/replay with a divergence policy.

## License

Apache-2.0. "Claude" and "Anthropic" are trademarks of Anthropic; understudy is not affiliated
with or endorsed by Anthropic.
