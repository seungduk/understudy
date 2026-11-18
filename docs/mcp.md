# MCP

`understudy mcp` is an MCP server over stdio for agents (built on the official
[Go SDK](https://github.com/modelcontextprotocol/go-sdk)). It acts on the understudy server at
`--url` (default `http://127.0.0.1:8765`) and starts one there when none answers
(`--server auto`, the default; `attach` only uses a running one; `start` always starts one). A
server it starts also has the web UI, and stops with it. `understudy serve` also answers MCP
over streamable HTTP at `/_understudy/mcp` (`--mcp off` turns it off).

| Tool | Does |
|---|---|
| `validate_script` | check a script (`yaml` or `file`) without loading it: `{ok, sessions, problems: [{path, line, rule, message, fix}]}` |
| `load_script` | load a script into the server; one that does not check is refused with its problems |
| `client_env` | the environment and command that run the real `claude -p` against the server, dummy credentials only (`session`, `auth: api_key \| gateway`); with `harness` (`codex`, `opencode`, `pi`, `gemini-cli`, `antigravity`, and `protocol`, `dir`) that harness's command, its config written into a fresh home |
| `list_sessions` | every session with its state, turn, step and counts, and the gates |
| `read_log` | the request log, filtered by `session`, `outcome` (or `divergence`), `step`, `since_seq`, `limit`; `detail: summary \| full` |
| `explain` | why each request got its answer, what a divergence expected and got; with `script_yaml` or `script_file`, what that script would choose instead |
| `release_gate` | let requests held by `stall: {gate: NAME}` go on |
| `reset` | forget the log, retry counts and gates; the script stays |
| `record` | a real session's transcript into a script (`then: load` also loads it) |

Resources: `understudy://docs/SCRIPTS.md` (the script guide) and
`understudy://schema/understudy-v1.schema.json`. Every answer is JSON; a failed call is an error
result `{ok: false, error, fix}`, with `problems` for a script that does not check and the shape
of the arguments for arguments that do not fit.

Claude Code (or `.mcp.json` in a project, with the same `command` and `args`):

```sh
claude mcp add understudy -- understudy mcp
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.understudy]
command = "understudy"
args = ["mcp"]
```

Cursor (`.cursor/mcp.json`), or any client that speaks streamable HTTP to a running
`understudy serve`:

```json
{ "mcpServers": { "understudy": { "command": "understudy", "args": ["mcp"] } } }
```

```json
{ "mcpServers": { "understudy": { "url": "http://127.0.0.1:8765/_understudy/mcp" } } }
```

The loop an agent runs: read the guide resource, write a script, `validate_script`,
`load_script`, `client_env` for the session, run the command it gives in the working folder,
then `list_sessions` and `read_log` with `outcome: divergence` (or `explain`) to see where the
run left the script; fix it, `load_script`, `reset`, run again.

## The MCP server, used by agents

Fresh agents got the README (which then held this page) and the MCP config (`understudy mcp --url ... --server attach`
against their own `understudy serve`) and nothing else, on 2026-11-18, with Claude Code 2.1.325
as the CLI under test:

- **Find and fix a divergence**: script a check (`test -f done.txt || exit 1`) that the next step
  expects to pass, run the real `claude -p` in a folder without the file, find the divergence
  with the tools, fix the script to answer both outcomes, reset and run again until the result is
  `NOT DONE` and the session is `done`. Claude Code (Opus) and Codex (codex-cli 0.167.0) each
  passed on the first try (Claude used `read_log` with `outcome: divergence`, then `explain` with
  the edited script before loading it; Codex used `explain`).
- **Stall and release, then record**: hold a final answer at a gate, run the CLI in the
  background, show the session `stalled` on the gate with `list_sessions`, `release_gate`, see
  `APPROVED`, and `record` the run's transcript. Both passed on the first try.

The server's own log after each run was checked, not only the agents' reports. Every run used
the dummy credentials `client_env` gives.
