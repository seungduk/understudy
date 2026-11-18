# What understudy does

A small, exact, scriptable fake of the model APIs that agent harnesses use, so the **real**
harness runs end to end with only the model faked. Claude Code, OpenAI's Codex CLI, opencode,
pi, Gemini CLI and Google Antigravity's CLI read and edit real files, run real commands, retry
and stream as they always do; understudy plays the model from a script. Use it for
deterministic tests of agent factories, harnesses and anything else that drives a coding agent.

- **Four wire protocols, exactly**: Anthropic Messages (the SSE event sequence, thinking with
  signatures, `input_json_delta`, in-stream errors, the subscription limit headers), OpenAI Chat
  Completions (`chat.completion.chunk` with `tool_calls` deltas, `reasoning_content`), OpenAI
  Responses (the typed `response.*` events Codex consumes, reasoning items, function and custom
  tool calls, `response.failed`) and Gemini (`generateContent`, `streamGenerateContent?alt=sse`,
  `functionCall`/`functionResponse` parts, `RESOURCE_EXHAUSTED` quota details), with each one's
  real error bodies and statuses.
- **One script for every harness**: neutral actions (`shell`, `read`, `write`, `edit`) become
  each harness's own tools (`Bash`, `exec_command`, `apply_patch`, `run_shell_command`, ...);
  native calls are sent as written. Each harness's results are read by its profile (how it
  reports exit codes and errors, measured on the real CLI).
- **Scripts agents can write**: YAML with a JSON Schema, closed sets of reply kinds and tests,
  unknown fields refused, plain fix-it errors with path, line and rule
  ([SCRIPTS.md](../SCRIPTS.md) is the one-page guide).
- **Stateless and concurrent**: each request's turn is read from the conversation it carries, so
  many sessions run at once and client retries are safe. Sessions are keyed explicitly by a
  header or a prompt marker.
- **Faults**: HTTP errors, in-stream errors, stalls (timed, or held until a gate is released),
  mid-stream disconnects, TCP resets, slow streams, and usage limits in each protocol's form
  (Claude Code's `You've hit your weekly limit`, Codex's `You’ve hit your usage limit`, Gemini's
  quota errors).
- **Record and replay**: turn a real session (a Claude Code transcript, or live traffic of any
  harness through the recording proxy) into a script, then replay it with a strict or lenient
  divergence policy.
- **Go library and test helpers**: start the server inside `go test`, load scripts, drive any of
  the real CLIs, assert on the request log, release gates, move a virtual clock.
- **Web UI**: sessions and their state, each conversation with the step that answered every
  request, divergences with what was expected beside what came, gates to release, the script
  with the steps that ran, live.
- **MCP server** (`understudy mcp`): agents write, check and load scripts, run the real CLI,
  read the log, release gates and record transcripts through tools.
- Two dependencies (`gopkg.in/yaml.v3` and the official MCP Go SDK), Apache-2.0. How the code
  is layered: [docs/ARCHITECTURE.md](ARCHITECTURE.md).

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
examples are in [SCRIPTS.md](../SCRIPTS.md); the JSON Schema is
[script/understudy-v1.schema.json](../script/understudy-v1.schema.json) (`understudy schema`).

`understudy validate FILE --json` prints `{ok, file, sessions, problems: [{path, line, rule,
message, fix}]}`; the exit code is 0 when the script checks.
