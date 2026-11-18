// Package explain says, for each request of a log, which session, turn and
// step answered it and why the other cases did not, optionally re-evaluated
// against another script (to see what an edited script would do).
//
// Entries explains server.Entry values (from server.Server.Requests, or a
// control.Client), Write prints them, and Summary gives one line. The command
// `understudy explain` is this package over a saved log or a running server.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package explain
