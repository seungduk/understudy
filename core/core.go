// Package core is the protocol-neutral model understudy runs on. A Request is
// a conversation of turns made of parts (text, thinking, tool calls, tool
// results); a Reply is what the fake model answers. Errors and usage limits
// are closed enums. core knows no wire protocol and no harness: protocol
// packages translate to and from it, harness profiles map tools onto it.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Role is who speaks a turn.
type Role int

const (
	User Role = iota + 1
	Assistant
	// System is an instruction turn inside the conversation (Claude Code puts
	// its environment there, Codex sends developer messages).
	System
)

// PartKind is the closed set of turn parts.
type PartKind int

const (
	Text PartKind = iota + 1
	Thinking
	ToolCall
	ToolResult
	// Other is a part understudy does not read (images, documents, server tools).
	Other
)

// Mark is what the wire says about a tool result's success. Some protocols mark
// every result (Anthropic is_error), some only errors (Gemini's error key), some
// none (OpenAI); a harness profile decides the rest from the result's text.
type Mark int

const (
	Unmarked Mark = iota
	MarkedOK
	MarkedError
)

// ToolKind is how a tool takes its input.
type ToolKind int

const (
	// Function tools take a JSON object.
	Function ToolKind = iota
	// Freeform tools take raw text (OpenAI custom tools, such as Codex's apply_patch).
	Freeform
)

// Part is one piece of a turn.
type Part struct {
	Kind PartKind
	// Text is the text or thinking; for a ToolResult the result's text; for a
	// Freeform ToolCall its raw input.
	Text string
	// CallID links a ToolResult to its ToolCall.
	CallID string
	// Tool is the tool a ToolCall calls (and, where the wire names it, the tool a result answers).
	Tool string
	// Input is a Function ToolCall's JSON object.
	Input json.RawMessage
	// ToolKind is the kind of a ToolCall.
	ToolKind ToolKind
	// Mark is a ToolResult's success as the wire states it.
	Mark Mark
}

// Turn is one message of the conversation.
type Turn struct {
	Role  Role
	Parts []Part
}

// Tool is one tool the client offers.
type Tool struct {
	Name string
	Kind ToolKind
}

// Request is one model request, decoded from any protocol.
type Request struct {
	Model  string
	Stream bool
	// System is the system or developer instructions outside the turns, joined by "\n".
	System string
	Turns  []Turn
	Tools  []Tool
}

// ToolNames lists the offered tools' names, in order.
func (r Request) ToolNames() []string {
	out := make([]string, len(r.Tools))
	for i, t := range r.Tools {
		out[i] = t.Name
	}
	return out
}

// Stop is why a reply ended.
type Stop int

const (
	EndTurn Stop = iota + 1
	ToolUse
	MaxTokens
)

// Call is one tool call of a reply.
type Call struct {
	// ID is a bare identifier (hex digits); each protocol adds its own prefix.
	ID   string
	Tool string
	Kind ToolKind
	// Input is a Function call's JSON object; Raw is a Freeform call's text.
	Input json.RawMessage
	Raw   string
}

// Reply is a successful answer of the fake model.
type Reply struct {
	Thinking string
	// ThinkingSignature is an opaque token some protocols attach to thinking.
	ThinkingSignature string
	Text              string
	Calls             []Call
	Stop              Stop
}

// Usage is the token accounting of one reply.
type Usage struct {
	Input  int
	Output int
}

// EstimateTokens is a deterministic token estimate: one token per four bytes, at least one.
func EstimateTokens(n int) int {
	t := (n + 3) / 4
	if t < 1 {
		return 1
	}
	return t
}

// DeriveID is a deterministic identifier: prefix and 24 hex digits of a hash
// of the parts. The same parts give the same id, so retries see the same ids.
func DeriveID(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(h[:])[:24]
}
