// Package server is the fake model API: an http.Handler that answers the
// Anthropic Messages, OpenAI Chat Completions, OpenAI Responses and Gemini
// endpoints from a script, plus the side endpoints the harnesses also call
// (model lists, token counts) and a control API under ControlPrefix. Use it in-process
// from Go tests, or through `understudy serve`.
//
// New makes a Server from Options (the script, and a real or virtual clock);
// Handler is its whole HTTP surface. Each request is matched to a session (the
// X-Understudy-Session header, or a prompt marker) and a turn (read from the
// conversation the request carries, so the server keeps no per-connection
// state and client retries are safe), answered by that step of the script, and
// logged. Requests, SessionRequests and Sessions read the log; Release opens
// gates; Advance moves the virtual clock; Load swaps the script.
//
// A request the script does not expect is a divergence: by default the server
// refuses it (a lenient script answers anyway), and the log entry says what
// the script expected beside what came.
//
// The understudytest package wraps a Server for tests that run the real agent
// CLIs.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package server
