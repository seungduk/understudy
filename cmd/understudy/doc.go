// Command understudy runs the fake model API (Anthropic Messages, OpenAI Chat
// Completions and Responses, Gemini) and its tools.
//
// Install a prebuilt binary from the GitHub releases, or build it:
//
//	go install github.com/seungduk/understudy/cmd/understudy@latest
//
// Then:
//
//	understudy serve --script hello.yaml   # the fake API, the web UI and MCP on 127.0.0.1:8765
//	eval "$(understudy env)"               # point Claude Code in this shell at it
//	understudy validate hello.yaml         # check a script; each problem says how to fix it
//	understudy explain --url http://127.0.0.1:8765
//	understudy setup --harness codex --home ./agent-home
//	understudy mcp                         # the MCP server over stdio, for agents
//	understudy version
//
// Run `understudy help` for every command and `understudy <command> --help` for
// a command's flags.
package main
