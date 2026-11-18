# Record and replay

```sh
# a real session's transcript -> a script (tool inputs rebased on {{cwd}})
claude -p --output-format stream-json --verbose "fix the bug" > run.jsonl
understudy record --transcript run.jsonl --session t1/writer --run r42 --task t1 --seat writer \
  --recorded-at 2026-11-18T09:30:00Z --out t1-writer.yaml

# or record live traffic: forward to the real API, write the script on Ctrl-C
understudy record-proxy --upstream https://api.anthropic.com --addr 127.0.0.1:8766 --out rec.yaml
```

`record-proxy` speaks every protocol: point any harness's base URL at it and it records the
session through the request's codec, with the harness's native tool calls (a script of a harness
other than Claude Code names its `harness:`, schema `understudy/v2`). The e2e suite records each
harness through it against a scripted upstream and replays the result.

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
