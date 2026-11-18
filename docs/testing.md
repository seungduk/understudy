# Tests

```sh
go test -race ./...                  # unit and package tests, no network beyond loopback
golangci-lint run                    # lint (v2, .golangci.yml); CI runs it too
go test -tags e2e -v ./e2e           # drives the real CLIs (each skips if it is not installed)
UNDERSTUDY_HARNESS=codex,pi go test -tags e2e -run TestHarness -v ./e2e   # some harnesses only
(cd internal/ui && npm ci && npm run check && npm run build)   # the web UI: type-check and build into internal/ui/dist
```

The built UI (`internal/ui/dist`) is committed so `go install` needs no Node; CI rebuilds it and
fails when it differs from the sources (`internal/ui/web`).

The e2e suite covers text, a tool loop editing a real file, branching on a failing test, retry
after 529, an in-stream error, close and reset mid-stream, a gated stall released by the test, the
weekly and 5-hour limit messages, the system-prompt session marker, strict divergence, record
then replay (passing, then diverging), and the MCP loop (load a script, run the CLI with
`client_env`, find the divergence with `read_log`, fix, reset, run again).

Each codec runs the shared contract (`internal/protocol/codectest`): every reply shape, error
kind, retry hint and limit window round-trips through its encoder and its reader. The harness
profiles' result rules are tested on the exact texts each real CLI sent.

## In an agent factory

understudy ran a whole practice run of an agent factory on four machines on 2026-11-18:
the real Claude Code 2.1.325 in microVMs on three Macs, every seat (planner, writer,
reviewer, merger) scripted by task and seat (`X-Understudy-Session: F-1/writer`), with the server
on a fourth machine. Two tasks were planned, written (real edits through scripted Read/Edit calls),
checked, reviewed and landed; their writers were held at a gate until both had written, so the
second landing conflicted and the scripted merger resolved it; one planner request got the
weekly limit, which the factory read as that machine's limit and moved the seat to another
machine. 19 requests, all served, none diverged.

## Scripts written by agents

The guide is tested on agents. Fresh agents get [SCRIPTS.md](../SCRIPTS.md) and
[e2e/guide-tasks.md](../e2e/guide-tasks.md) and nothing else, write five scripts blind (text only, a
tool loop that edits a file, a branch on a check, the weekly limit, a stall released by a gate),
and `UNDERSTUDY_GUIDE_DIR=<their folder> go test -tags e2e -run TestGuide ./e2e` runs each with the
real CLI. Round 1 (2026-11-18): Claude Opus and Claude Haiku each passed 4 of 5; both missed that
Claude Code reports a lone `test -f` exiting 1 as a success. The guide now has a table of what
Claude Code sends back for each tool; round 2: Claude Opus 5/5, Claude Haiku 5/5 and Codex
(codex-cli 0.167.0) 5/5, each on the first try.
