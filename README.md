<p align="center">
  <img src="docs/logo/understudy.svg" alt="understudy" width="120" height="120">
</p>

<h1 align="center">understudy</h1>

<p align="center"><b>Fake AI models for coding agents: save tokens and make agent tests deterministic.</b></p>

<p align="center">
  <b>English</b> | <a href="README.ko.md">한국어</a> | <a href="README.ja.md">日本語</a> | <a href="README.zh-CN.md">简体中文</a>
</p>

## What it is

Coding agents such as Claude Code, Codex CLI, opencode, pi, Gemini CLI, and Antigravity's CLI communicate with language models over HTTP APIs. **understudy** mocks these model APIs using concise YAML scripts. You define what the model returns at each turn, point your agent at understudy, and let the real agent execute: it reads and edits files, runs commands, handles retries, and streams responses just like it does in production.

Because tests run without real models or API credentials, agent test suites execute quickly and produce deterministic results every time. understudy can also simulate HTTP errors, stalls, dropped connections, and rate limits, matching the exact payloads and status codes of real provider APIs.

<p align="center">
  <a href="docs/screenshots/hero-divergence.png"><img src="docs/screenshots/hero-divergence.png" width="880" alt="The web UI showing a session divergence: the actual request did not match any script case, displayed side-by-side with expected values"></a>
</p>

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

understudy serve --script hello.yaml &         # web UI at http://127.0.0.1:8765/_understudy/ui/
eval "$(understudy env)"                       # points Claude Code to understudy with dummy credentials
claude -p "say hi" </dev/null                  # prints: Hello from the understudy.
understudy explain --url http://127.0.0.1:8765 # shows which step answered each request, and why
```

Scripts can also invoke tools, branch on command outputs, handle retries, and inject simulated errors: see [SCRIPTS.md](SCRIPTS.md) for the complete reference. Run `understudy validate FILE` to check your script and receive fix suggestions for any errors.

## Use with other harnesses

`understudy setup` generates configuration files in an isolated home directory and prints the command to run the harness against understudy with dummy credentials (pass `--session KEY` to specify a session name).

| Harness | Set up with |
|---|---|
| Claude Code | `eval "$(understudy env)"` |
| OpenAI Codex CLI | `understudy setup --harness codex --home /tmp/h` |
| opencode | `understudy setup --harness opencode --home /tmp/h` |
| pi | `understudy setup --harness pi --home /tmp/h` |
| Gemini CLI | `understudy setup --harness gemini-cli --home /tmp/h` |
| Antigravity CLI | `understudy setup --harness antigravity --home /tmp/h` |

For supported wire protocols, generated configuration files, and tested CLI versions, see [docs/harnesses.md](docs/harnesses.md).

## Web UI and MCP

`understudy serve` hosts an interactive web UI at `/_understudy/ui/`. You can monitor active sessions, trace turns to the script steps that answered them, inspect unexpected divergences, interactively release gated stalls, and view loaded scripts in real time ([learn more](docs/web-ui.md)).

`understudy mcp` provides these capabilities to AI agents via Model Context Protocol (MCP) tools: write and validate scripts, execute harnesses, inspect request logs, and release gates ([learn more](docs/mcp.md)).

<table>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/sessions.png"><img src="docs/screenshots/sessions.png" width="100%" alt="Sessions: twelve sessions of a planner, writer, reviewer, and tester workflow, including a session stalled at a gate, a diverged session, and an active stream"></a><br><sub><b>Sessions</b>: Inspect session state, turns, steps, and counts; release paused gates.</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/requests.png"><img src="docs/screenshots/requests.png" width="100%" alt="Requests: log of 41 requests with a diverged request open side by side"></a><br><sub><b>Requests</b>: Browse the full request log and inspect detailed payload diffs side by side.</sub></td>
  </tr>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/conversation.png"><img src="docs/screenshots/conversation.png" width="100%" alt="Session view: initial prompt, model thinking, scripted Read calls, and tool outputs from Claude Code"></a><br><sub><b>Session history</b>: Review prompts, scripted model responses, and client tool results.</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/script.png"><img src="docs/screenshots/script.png" width="100%" alt="Script view: syntax-highlighted YAML with request hit counts per step"></a><br><sub><b>Script view</b>: Browse the active YAML script with live hit counters for each step.</sub></td>
  </tr>
  <tr>
    <td colspan="2"><a href="docs/screenshots/dark-mobile.png"><img src="docs/screenshots/dark-mobile.png" width="100%" alt="Dark mode and responsive mobile view showing a writer's fix loop and a reviewer paused at a gate"></a><br><sub><b>Mobile & Dark Mode</b>: Fully responsive interface with dark mode support.</sub></td>
  </tr>
</table>

## Learn more

- [SCRIPTS.md](SCRIPTS.md): Script format specification, schema reference, and complete examples
- [Features](docs/features.md): Supported protocols, fault simulation, traffic recording, and Go test helpers
- [Harnesses](docs/harnesses.md): Setup instructions for each harness and tested version matrix
- [Faults and limits](docs/limits.md): Measured harness behavior under API errors, stalls, and rate limits
- [Record and replay](docs/recording.md) · [Go library](docs/go.md) · [Control API](docs/control-api.md)
- [Web UI](docs/web-ui.md) · [MCP server](docs/mcp.md) · [Testing](docs/testing.md) · [Architecture](docs/ARCHITECTURE.md)
- [Credits & references](docs/credits.md) · [Contributing](CONTRIBUTING.md)

## License

Apache-2.0. "Claude" and "Anthropic" are trademarks of Anthropic; "OpenAI" and "Codex" of OpenAI;
"Gemini" and "Antigravity" of Google; "DeepSeek" of DeepSeek. understudy is not affiliated with or
endorsed by any of them.
