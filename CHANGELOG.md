# Changelog

All notable changes to understudy are written here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and understudy uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until v1.0.0, a minor
release may change the Go API or the command line; each such change is listed here.

## [Unreleased]

## [0.1.0] - 2026-11-19

The first release.

### Added

- `understudy serve`: a scriptable fake of four model APIs, answered exactly as the real ones
  (event order, error bodies, status codes, limit headers): Anthropic Messages, OpenAI Chat
  Completions, OpenAI Responses and Gemini, plus DeepSeek's Chat Completions and Anthropic
  surfaces.
- Script format `understudy/v1` and `understudy/v2` (YAML, with a JSON Schema): sessions, steps,
  cases on tool results (`tool`, `result`, `exit_code`, `contains`, `regex`), retries with
  `attempts`, and the neutral actions `shell`, `read`, `write` and `edit`, which become each
  harness's own tools. `understudy validate` reports every problem with its line and a fix.
- Faults on any reply: HTTP errors, in-stream errors, stalls (timed, or held until a gate is
  released), mid-stream disconnects, TCP resets, slow streams, and usage limits in each API's
  form.
- Harness profiles for Claude Code, OpenAI Codex CLI, opencode, pi, Gemini CLI and Google
  Antigravity's CLI, and `understudy setup` / `understudy env` to point each one at the fake with
  dummy credentials only.
- Record and replay: `understudy record` turns a Claude Code stream-json transcript into a
  script; `understudy record-proxy` records any harness's live traffic. Replays diverge strictly
  or leniently.
- `understudy explain`: which step answered each request, and why the other cases did not.
- A web UI at `/_understudy/ui/` (sessions, conversations, divergences, gates, the script) and an
  MCP server (`understudy mcp`, and streamable HTTP at `/_understudy/mcp`) for agents.
- Go packages: `understudytest` (run the fake in a test and drive the real CLIs), `server`,
  `script`, `record`, `explain`, `control`, `clienv`, `harness`, `protocol` and `core`, with
  runnable examples.
- `understudy version` prints the release version and commit, also for `go install …@v0.1.0`.
- Prebuilt binaries for Linux, macOS and Windows on amd64 and arm64, with SHA-256 checksums and
  SBOMs, built by GoReleaser from each `v*` tag.

[Unreleased]: https://github.com/seungduk/understudy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/seungduk/understudy/releases/tag/v0.1.0
