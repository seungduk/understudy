// Package core is the protocol-neutral model understudy runs on. A Request is
// a conversation of turns made of parts (text, thinking, tool calls, tool
// results); a Reply is what the fake model answers. Errors and usage limits
// are closed enums. core knows no wire protocol and no harness: protocol
// packages translate to and from it, harness profiles map tools onto it.
//
// Most users never import core: scripts name its values by their names (an
// Action is shell, read, write or edit; an ErrorKind is overloaded, rate_limit,
// and so on), and the Parse functions turn those names into values.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package core
