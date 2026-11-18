// Package protocol is the one narrow interface every wire protocol implements:
// decode an HTTP request into a core.Request, encode a core outcome into an
// HTTP response (a JSON body or SSE frames), and, for recording, read a
// response back. Implementations live in internal/protocol/<name>; the server
// and the recorder use them only through Codec.
//
// The wire protocols are the closed set ID: Anthropic Messages, OpenAI Chat
// Completions, OpenAI Responses and Gemini. Users name them in scripts and
// flags; Parse turns a name into an ID.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package protocol
