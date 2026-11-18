// Package understudy is a fake AI model for coding agents: a local server
// that answers the model APIs agent harnesses use (Anthropic Messages, OpenAI
// Chat Completions and Responses, Gemini) from a short YAML script, so the
// real agent (Claude Code, Codex, opencode, pi, Gemini CLI, Antigravity's CLI)
// runs end to end with no API key, no tokens spent, and the same result on
// every run. Scripts can also answer with HTTP errors, stalls, dropped
// connections and usage limits, in each API's real form.
//
// This package holds the documents the tools serve, embedded so a built
// binary carries them: ScriptsGuide (the script format) and Readme.
//
// # Packages
//
// For Go tests, start with understudytest:
//
//   - [github.com/seungduk/understudy/understudytest]: run the fake inside a
//     test and drive the real agent CLIs against it.
//   - [github.com/seungduk/understudy/script]: the script format; parse,
//     check and build scripts.
//   - [github.com/seungduk/understudy/server]: the fake API as an
//     http.Handler, with the request log, gates and a virtual clock.
//   - [github.com/seungduk/understudy/record]: turn real sessions into
//     scripts, from a transcript or through a recording proxy.
//   - [github.com/seungduk/understudy/explain]: say which step answered each
//     logged request, and why.
//   - [github.com/seungduk/understudy/control]: a client of a running
//     server's control API.
//   - [github.com/seungduk/understudy/clienv]: the environment that points
//     Claude Code at the fake with dummy credentials.
//   - [github.com/seungduk/understudy/harness],
//     [github.com/seungduk/understudy/protocol] and
//     [github.com/seungduk/understudy/core]: the harness profiles, the wire
//     protocol IDs and the neutral model the others share.
//
// The command is [github.com/seungduk/understudy/cmd/understudy]: install a
// prebuilt binary from https://github.com/seungduk/understudy/releases, or run
// go install github.com/seungduk/understudy/cmd/understudy@latest.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change the API, and
// CHANGELOG.md says how. Patch releases do not.
package understudy
