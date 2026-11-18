// Package control is a client of a running understudy server's control API
// (the endpoints under /_understudy/). Every error says what to do next.
//
// A Client loads and reads scripts, lists sessions and gates, reads the request
// log and releases gates. It is what `understudy explain --url`, the MCP server
// and the web UI use; use it to drive a server started with `understudy serve`
// from another process. In the same process, call the server.Server methods
// directly instead.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package control
