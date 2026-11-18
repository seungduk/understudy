# Writing understudy scripts

An understudy script is a YAML file that tells the fake model what to answer at each turn of an
agent harness's session: Claude Code, Codex, opencode, pi, Gemini CLI or Antigravity's CLI. The
harness runs for real (it reads and edits files, runs commands, retries); only the model is
scripted. This page is the whole format (schema `understudy/v2`; `understudy/v1` scripts read
the same as before). Check every script with `understudy validate FILE` (add `--json` for
machine-readable problems).

## Shape

```yaml
schema: understudy/v2          # required: understudy/v2 (or v1 for Claude-Code-only scripts)
divergence: strict             # optional: strict (default) or lenient; see "When the run does not fit"
harness: codex                 # optional: the harness every session runs in (see "Harnesses")
provenance:                    # optional: where the script came from
  source: written              # written or recorded
  task: t1                     # optional: run, task, seat, session, recorded_at (RFC 3339), cli_version, note
sessions:                      # required: one entry per session key
  t1/writer:                   # the session key
    harness: codex             # optional: this session's harness (overrides the file's)
    steps:                     # the turns, in order
      - name: read-file        # required, unique: lower-case letters, digits, - and _
        reply: { ... }         # what the model answers at this turn
```

**Session key.** Each request belongs to a session. The harness sends the key as the header
`X-Understudy-Session` (`understudy setup --session KEY` configures it; for Claude Code set
`ANTHROPIC_CUSTOM_HEADERS="X-Understudy-Session: t1/writer"`), or the system prompt or the user's
first message holds the marker `[understudy-session: t1/writer]` (Antigravity's CLI sends no
custom headers: put the marker in the prompt). A request with neither is in the session
`default`. Keys are 1-128 letters, digits and `. _ : / @ + -`.

**Harness.** Which harness sent a request decides how actions become tool calls and how tool
results are read. understudy tells it from the tools the request offers; write `harness:` (one
of `claude-code`, `codex`, `opencode`, `pi`, `gemini-cli`, `antigravity`) to say it outright.
`understudy harnesses` lists them.

**Turns.** Step 1 answers the first request of the session, step 2 the request after the first
reply, and so on: the position is read from the conversation the request carries, so nothing is
remembered between requests and parallel sessions never mix. When a step (or the case that
answered) has `next: other-step`, the turn after it is answered by `other-step` instead of the
following step, so a loop is a `next` back to an earlier step. A script only answers; the files
a session works on are made by whoever starts it (a test prepares the working directory).

## Replies

A reply has exactly one kind:

| Kind | Fields | What Claude Code sees |
|---|---|---|
| `message` | `text` (required), `thinking`, `stop` (`end_turn` default, or `max_tokens`) | A final answer; the session's turn ends with this text as the result. |
| `tool_use` | `calls` (required, 1-64 of `{tool, input}`), `text`, `thinking` | Tool calls. Claude Code runs them and sends the results in the next request. |
| `http_error` | `error` (required), `message`, `retry_after_seconds`, `retry` (`default`, `never`, `always`) | An HTTP error with the real API's body. `retry: never` sends `x-should-retry: false`. |
| `stream_error` | `error` (required), `message`, `partial_text` | A 200 stream that breaks with an `event: error` (after `partial_text`, if given). |
| `limit` | `window` (required: `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`, and in v2 `daily`, `per_minute`), `resets_in_seconds` (required) | A usage limit, in the protocol's own form (see "Limits"). |

`error` is one of: `invalid_request`, `authentication`, `billing`, `permission`, `not_found`,
`request_too_large`, `rate_limit`, `api_error`, `timeout`, `overloaded`. Each protocol sends
its own status and body for it:

| `error` | Anthropic | OpenAI (Chat, Responses) | Gemini |
|---|---|---|---|
| `invalid_request` | 400 `invalid_request_error` | 400 `invalid_request_error` | 400 `INVALID_ARGUMENT` |
| `authentication` | 401 `authentication_error` | 401 `invalid_api_key` | 400 `INVALID_ARGUMENT` (`API_KEY_INVALID`) |
| `billing` | 402 `billing_error` | 429 `insufficient_quota` | 400 `FAILED_PRECONDITION` |
| `permission` | 403 `permission_error` | 403 `permission_denied` | 403 `PERMISSION_DENIED` |
| `not_found` | 404 `not_found_error` | 404 `model_not_found` | 404 `NOT_FOUND` |
| `request_too_large` | 413 `request_too_large` | 413 `request_too_large` | 400 `INVALID_ARGUMENT` |
| `rate_limit` | 429 `rate_limit_error` | 429 `rate_limit_exceeded` | 429 `RESOURCE_EXHAUSTED` |
| `api_error` | 500 `api_error` | 500 `server_error` | 500 `INTERNAL` |
| `timeout` | 504 `timeout_error` | 504 `timeout` | 504 `DEADLINE_EXCEEDED` |
| `overloaded` | 529 `overloaded_error` | 503 `server_is_overloaded` | 503 `UNAVAILABLE` |

## Tool calls: actions or native tools

A call in `calls` is one of two forms.

**An action (v2)**, the same in every harness; understudy turns it into the harness's own tool:

| Action | Fields | Claude Code | Codex | opencode | pi | Gemini CLI | Antigravity |
|---|---|---|---|---|---|---|---|
| `shell` | `command` | `Bash` | `exec_command` | `bash` | `bash` | `run_shell_command` | `run_command` |
| `read` | `path` | `Read` | `exec_command` (`cat`) | `read` | `read` | `read_file` | `read_file` |
| `write` | `path`, `content` | `Write` | `apply_patch` (Add File) | `write` | `write` | `write_file` | `write_file` |
| `edit` | `path`, `old`, `new` | `Edit` | `apply_patch` (Update File) | `edit` | `edit` | `replace` | `edit_file` |

```yaml
calls:
  - shell: {command: "go test ./..."}
  - read: {path: "{{cwd}}/main.go"}
  - write: {path: "{{cwd}}/notes.txt", content: "done\n"}
  - edit: {path: "{{cwd}}/main.go", old: "return 1", new: "return 0"}
```

`old` must occur once in the file. For Codex, whose only edit tool is a patch, `old` and `new`
are whole lines. Claude Code refuses to edit a file not read first in the session (the other
five edit it anyway), so read before you edit.

**A native call**, sent exactly as written: `{tool: Name, input: {...}}` with the harness's
own tool name and fields, for any tool the harness offers (`understudy harnesses --json` shows
each harness's tools). A freeform tool (Codex's `apply_patch`) takes its text as
`input: {input: "..."}`. For Claude Code:

| Tool | Input |
|---|---|
| `Read` | `file_path` (absolute) |
| `Write` | `file_path`, `content` |
| `Edit` | `file_path`, `old_string`, `new_string` (Claude Code refuses an Edit of a file not Read first in the session) |
| `Bash` | `command`, `description` |

Paths must be absolute: write `{{cwd}}` for the session's working directory, as in
`path: "{{cwd}}/src/main.go"`. `{{cwd}}` is the only placeholder.

Harnesses run tools only with permission; `understudy setup` and the Go helpers start each one
in its no-questions mode (`--dangerously-skip-permissions`, `--yolo`, ...), or edits come back
as errors.

## Faults

Any reply can also carry these, to test how the client copes:

| Field | Meaning |
|---|---|
| `stall: {seconds: 30}` | Wait 30 s before answering (the virtual clock moves only when told to). |
| `stall: {gate: review}` | Wait until the gate is released: `POST /_understudy/gates/review/release`, or `Release("review")` in Go. |
| `stall: {..., at: mid_stream}` | Stall after the stream has started (default `before_response`). |
| `cut: {after_events: 3, how: close}` | Send 3 SSE events, then close the connection. `after_events: 0` cuts before any response. |
| `cut: {after_events: 3, how: reset}` | The same, but a TCP reset. |
| `pace: {first_event_ms: 500, between_events_ms: 50}` | A slow stream. |

`cut` and `between_events_ms` apply only to streamed kinds (`message`, `tool_use`, `stream_error`).

## Retries: attempts

Claude Code retries failed requests at the same turn. `attempts` lists the answers for the 1st,
2nd, 3rd ... request at this turn; the last one repeats for any further request:

```yaml
      - name: first
        attempts:
          - http_error: {error: overloaded}       # the 1st request gets a 529
          - message: {text: "Hello."}             # the retry gets the answer
```

## Branches: cases

A step can choose its reply from the tool results the request carries (the results of the
previous turn's calls). The first case whose `when` matches answers; `otherwise` answers when
none does.

```yaml
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "Tests pass."}}
          - when: {tool: Bash, contains: FAIL}
            next: fix                               # go back to the step named fix
            reply: {tool_use: {calls: [{tool: Read, input: {file_path: "{{cwd}}/main.go"}}]}}
        otherwise:
          reply: {message: {text: "blocked: unexpected test output"}}
```

A test (`when`, or a step's `expect`) has one or more of these fields; all must hold for the same
tool result, and the test matches when any result passes:

| Field | Holds when the result ... |
|---|---|
| `action: shell` | (v2) answers a call of this action (`shell`, `read`, `write`, `edit`), in any harness |
| `tool: Bash` | answers a call to this tool (the harness's own name) |
| `result: ok` / `result: error` | is not an error / is an error |
| `exit_code: 1` | shows this exit code, as the harness reports it (table below) |
| `contains: "text"` | contains the text |
| `regex: "^ok\\s"` | matches the RE2 regular expression |

A step without cases can say what it `expect`s from the results before it answers:
`expect: {tool: Edit, result: ok}`.

What Claude Code sends back as tool results (measured with Claude Code 2.1.325):

| The call | The result | Matches |
|---|---|---|
| `Bash` that exits 0 | not an error; its output, or `(Bash completed with no output)` | `result: ok`, `exit_code: 0` |
| `Bash` that exits N (not 0) | an error; `Exit code N`, then its output | `result: error`, `exit_code: N`, `contains:` on the output |
| `Bash` of a lone `test`, `[ ... ]` or `grep` that exits 1 | **not an error**, `(Bash completed with no output)`: Claude Code reads exit 1 of these as "false" or "no match", not as a failure | `result: ok`, `exit_code: 0` |
| `Read` | not an error; the file with line numbers (`1\tfirst line`) | `result: ok`, `contains:` |
| `Edit`, `Write` | not an error; `The file ... has been updated successfully.`, or an error saying why not | `result: ok` / `result: error` |

So to branch on whether a file exists or text is found, make the failure an exit code Claude Code
reports: `test -f ready || exit 1`, `grep -q PASS out.txt || exit 1`.

How the other harnesses report a shell result (measured; `understudy` reads these for you):

| Harness | A command that exits N (not 0) | Exit 0 | Other failed tools |
|---|---|---|---|
| Codex 0.170.0 | `Process exited with code N` (an error) | `... code 0` | apply_patch: `Exit code: N` |
| pi 0.73.1 | output, then `Command exited with code N` (an error) | output only | `ENOENT: ...` text |
| Gemini CLI 0.68.0 | `Exit Code: N` (an error) | output only | marked errors (`File not found: ...`) |
| Antigravity 1.5.11 | `Command exited with code N.` (an error) | `... code 0.` | marked errors |
| opencode 1.19.6 | **the output only: no exit code, not an error** | output only | `File not found: ...` text |

opencode never shows the model an exit code, so `exit_code` cannot match there: print a marker
and test for it, as in `go test ./... && echo TESTS-PASS || echo TESTS-FAIL` with
`when: {action: shell, contains: TESTS-PASS}`. That form works in every harness.

## When the run does not fit

A request diverges when an `expect` fails, no case matches and there is no `otherwise`, a call
names a tool the client does not offer (or an action the harness has no tool for), or the session
asks for more turns than the script has.

- `divergence: strict` (default): the server answers a 400 in the protocol's form, with
  `x-should-retry: false` and a message that names the divergence; Claude Code stops with
  `API Error: 400 ... does not fit the script: ...` (Claude Code 2.1.325 retries a 400 five times
  first, so the log shows the diverging request six times; the other harnesses do not retry it).
- `divergence: lenient`: the server logs the divergence and answers anyway (a failed expect is
  ignored, no match takes case 0, past the end repeats the last step).

Every request is in the log (`GET /_understudy/requests`), with its session, turn, step, case,
attempt and a trace of every test. `understudy explain --url URL` prints it readably, and
`--script FILE` shows what another script would choose.

## Six complete scripts

**1. Text only.**

```yaml
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply:
          message:
            thinking: The user wants a short plan.
            text: "PLAN: 1. read main.go 2. fix the bug 3. run go test"
```

**2. A tool loop that edits a file.**

```yaml
schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: read
        reply:
          tool_use:
            text: Reading the file first.
            calls:
              - {tool: Read, input: {file_path: "{{cwd}}/greeting.txt"}}
      - name: edit
        expect: {tool: Read, result: ok}
        reply:
          tool_use:
            calls:
              - tool: Edit
                input: {file_path: "{{cwd}}/greeting.txt", old_string: "hello", new_string: "goodbye"}
      - name: done
        expect: {tool: Edit, result: ok}
        reply:
          message: {text: "DONE: greeting.txt now says goodbye"}
```

**3. Branch on the test result.**

```yaml
schema: understudy/v1
sessions:
  t2/writer:
    steps:
      - name: test
        reply:
          tool_use:
            calls: [{tool: Bash, input: {command: "go test ./...", description: Run the tests}}]
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "DONE: tests pass"}}
          - when: {tool: Bash, result: error, contains: FAIL}
            next: test
            reply:
              tool_use:
                calls:
                  - {tool: Write, input: {file_path: "{{cwd}}/fix.go", content: "package main\n"}}
        otherwise:
          reply: {message: {text: "blocked: go test did not run"}}
```

After the `Write`, `next: test` runs the tests again, and `judge` judges the new result.

## Limits

A `limit` reply is a usage limit in each protocol's own form. What each harness did with
`limit: {window: seven_day, resets_in_seconds: 7200}` (measured 2026-11-18):

| Harness | Shows | Retries |
|---|---|---|
| Claude Code (gateway token) | `You've hit your weekly limit · resets <time>`, exit 1 | none |
| Codex | `You’ve hit your usage limit. ... try again at <time>.`, exit 1 | none |
| Gemini CLI | `You exceeded your current quota ... Suggested retry after 7200s.` (`daily`: `You have exhausted your daily quota on this model.`) | none |
| Antigravity | `Error 429 ... RESOURCE_EXHAUSTED`, exit 3 | none |
| pi | `429 The usage limit has been reached` (exit 0) | 3 more times, then stops |
| opencode | nothing: it waits out `retry-after` and asks again | until the reset |

`per_minute` is a plain rate limit that says when to retry: every harness waited and retried.

**4. The weekly limit.**

```yaml
schema: understudy/v1
sessions:
  t3/writer:
    steps:
      - name: first
        reply:
          limit: {window: seven_day, resets_in_seconds: 7200}
```

Claude Code prints `You've hit your weekly limit · resets <time>` and exits 1 when it runs with
the gateway token (`understudy env --auth gateway`, or `Auth: clienv.Gateway` in Go); with an
API key it prints `API Error: Request rejected (429)`.

**5. Stall until released.**

```yaml
schema: understudy/v1
sessions:
  t4/reviewer:
    steps:
      - name: verdict
        reply:
          message: {text: "CAUSE: WORK"}
          stall: {gate: reviewer-gate}
```

The request waits until `curl -X POST http://127.0.0.1:8765/_understudy/gates/reviewer-gate/release`.

**6. The same task in any harness (v2 actions).**

```yaml
schema: understudy/v2
sessions:
  t5/writer:
    harness: codex               # optional: understudy can tell from the request
    steps:
      - name: read
        reply: {tool_use: {calls: [{read: {path: "{{cwd}}/greeting.txt"}}]}}
      - name: edit
        expect: {action: read, contains: hello old world}
        reply: {tool_use: {calls: [{edit: {path: "{{cwd}}/greeting.txt", old: "hello old world", new: "hello new world"}}]}}
      - name: check
        expect: {action: edit, result: ok}
        reply: {tool_use: {calls: [{shell: {command: "grep -q 'hello new world' greeting.txt && echo CHECK-PASS"}}]}}
      - name: judge
        cases:
          - when: {action: shell, contains: CHECK-PASS}
            reply: {message: {text: "DONE: greeting updated"}}
        otherwise:
          reply: {message: {text: "blocked: the check failed"}}
```

Remove the `harness:` line and the same script runs in all six harnesses.

## Mistakes the checker catches

Unknown fields (with "did you mean"), a step with both `reply` and `cases`, a reply with no kind or
two, a call with both a tool and an action (or two actions), `next` naming no step, an unknown
`error`, `window`, `action` or harness, a v2 field in a v1 script, a regex that does not compile,
`{{home}}` or any placeholder but `{{cwd}}`, and numbers out of range. Each problem says where (path and line),
which rule, and how to fix it. The JSON Schema is `understudy schema` (or
`script/understudy-v1.schema.json`) for editors.
