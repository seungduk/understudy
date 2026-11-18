package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Role is the closed set of message roles. Claude Code also puts a "system"
// entry into messages (environment details), which the real API tolerates from it.
type Role int

const (
	User Role = iota + 1
	Assistant
	SystemRole
)

// BlockKind is the closed set of content block kinds this package reads.
// Kinds it does not know (images, documents, server tools) are read as Other.
type BlockKind int

const (
	TextBlock BlockKind = iota + 1
	ThinkingBlock
	ToolUseBlock
	ToolResultBlock
	OtherBlock
)

// Block is one content block of a request message.
type Block struct {
	Kind      BlockKind
	Text      string // text, thinking, or the joined text of a tool_result
	ToolUseID string // tool_use id, or the tool_use_id a tool_result answers
	ToolName  string // tool_use only
	IsError   bool   // tool_result only
	Input     json.RawMessage
}

// Message is one entry of the request's messages.
type Message struct {
	Role   Role
	Blocks []Block
}

// Request is what this server reads from POST /v1/messages.
type Request struct {
	Model     string
	Stream    bool
	System    string // the system prompt, text blocks joined by "\n"
	Messages  []Message
	ToolNames []string
	MaxTokens int
}

type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	System   json.RawMessage `json:"system"`
	Messages []rawMessage    `json:"messages"`
	Tools    []struct {
		Name string `json:"name"`
	} `json:"tools"`
	MaxTokens int `json:"max_tokens"`
}

// ParseRequest decodes a Messages request. It refuses what the real API
// refuses for every client: no model, no messages, unknown roles.
func ParseRequest(body []byte) (Request, error) {
	var raw rawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return Request{}, fmt.Errorf("the body is not a JSON Messages request: %v", err)
	}
	if raw.Model == "" {
		return Request{}, errors.New("model: field required")
	}
	if len(raw.Messages) == 0 {
		return Request{}, errors.New("messages: at least one message is required")
	}
	req := Request{Model: raw.Model, Stream: raw.Stream, MaxTokens: raw.MaxTokens}
	sys, err := joinText(raw.System)
	if err != nil {
		return Request{}, fmt.Errorf("system: %v", err)
	}
	req.System = sys
	for _, t := range raw.Tools {
		req.ToolNames = append(req.ToolNames, t.Name)
	}
	for i, m := range raw.Messages {
		var role Role
		switch m.Role {
		case "user":
			role = User
		case "assistant":
			role = Assistant
		case "system":
			role = SystemRole
		default:
			return Request{}, fmt.Errorf("messages.%d.role: unexpected value %q, expected user or assistant", i, m.Role)
		}
		blocks, err := parseContent(m.Content)
		if err != nil {
			return Request{}, fmt.Errorf("messages.%d.content: %v", i, err)
		}
		req.Messages = append(req.Messages, Message{Role: role, Blocks: blocks})
	}
	return req, nil
}

func parseContent(c json.RawMessage) ([]Block, error) {
	if len(c) == 0 || string(c) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(c, &s); err == nil {
		return []Block{{Kind: TextBlock, Text: s}}, nil
	}
	var raws []rawBlock
	if err := json.Unmarshal(c, &raws); err != nil {
		return nil, errors.New("expected a string or a list of content blocks")
	}
	out := make([]Block, 0, len(raws))
	for _, r := range raws {
		switch r.Type {
		case "text":
			out = append(out, Block{Kind: TextBlock, Text: r.Text})
		case "thinking":
			out = append(out, Block{Kind: ThinkingBlock, Text: r.Thinking})
		case "tool_use":
			out = append(out, Block{Kind: ToolUseBlock, ToolUseID: r.ID, ToolName: r.Name, Input: r.Input})
		case "tool_result":
			text, err := joinText(r.Content)
			if err != nil {
				return nil, fmt.Errorf("tool_result content: %v", err)
			}
			out = append(out, Block{Kind: ToolResultBlock, ToolUseID: r.ToolUseID, IsError: r.IsError, Text: text})
		default:
			out = append(out, Block{Kind: OtherBlock})
		}
	}
	return out, nil
}

// joinText reads a string or a list of blocks and joins the text blocks with "\n".
func joinText(c json.RawMessage) (string, error) {
	if len(c) == 0 || string(c) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(c, &s); err == nil {
		return s, nil
	}
	var raws []rawBlock
	if err := json.Unmarshal(c, &raws); err != nil {
		return "", errors.New("expected a string or a list of blocks")
	}
	var parts []string
	for _, r := range raws {
		if r.Type == "text" {
			parts = append(parts, r.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// EstimateTokens is a deterministic token estimate: one token per four bytes, at least one.
func EstimateTokens(n int) int {
	t := (n + 3) / 4
	if t < 1 {
		return 1
	}
	return t
}
