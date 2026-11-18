package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/seungduk/understudy/core"
)

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
}

// parseRequest decodes a Messages request. It refuses what the real API
// refuses for every client: no model, no messages, unknown roles. Claude Code
// also puts a "system" entry into messages (environment details), which the
// real API tolerates from it; it becomes a core.System turn.
func parseRequest(body []byte) (core.Request, error) {
	var raw rawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return core.Request{}, fmt.Errorf("the body is not a JSON Messages request: %v", err)
	}
	if raw.Model == "" {
		return core.Request{}, errors.New("model: field required")
	}
	if len(raw.Messages) == 0 {
		return core.Request{}, errors.New("messages: at least one message is required")
	}
	req := core.Request{Model: raw.Model, Stream: raw.Stream}
	sys, err := joinText(raw.System)
	if err != nil {
		return core.Request{}, fmt.Errorf("system: %v", err)
	}
	req.System = sys
	for _, t := range raw.Tools {
		req.Tools = append(req.Tools, core.Tool{Name: t.Name})
	}
	for i, m := range raw.Messages {
		var role core.Role
		switch m.Role {
		case "user":
			role = core.User
		case "assistant":
			role = core.Assistant
		case "system":
			role = core.System
		default:
			return core.Request{}, fmt.Errorf("messages.%d.role: unexpected value %q, expected user or assistant", i, m.Role)
		}
		parts, err := parseContent(m.Content)
		if err != nil {
			return core.Request{}, fmt.Errorf("messages.%d.content: %v", i, err)
		}
		req.Turns = append(req.Turns, core.Turn{Role: role, Parts: parts})
	}
	return req, nil
}

func parseContent(c json.RawMessage) ([]core.Part, error) {
	if len(c) == 0 || string(c) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(c, &s); err == nil {
		return []core.Part{{Kind: core.Text, Text: s}}, nil
	}
	var raws []rawBlock
	if err := json.Unmarshal(c, &raws); err != nil {
		return nil, errors.New("expected a string or a list of content blocks")
	}
	out := make([]core.Part, 0, len(raws))
	for _, r := range raws {
		switch r.Type {
		case "text":
			out = append(out, core.Part{Kind: core.Text, Text: r.Text})
		case "thinking":
			out = append(out, core.Part{Kind: core.Thinking, Text: r.Thinking})
		case "tool_use":
			out = append(out, core.Part{Kind: core.ToolCall, CallID: r.ID, Tool: r.Name, Input: r.Input})
		case "tool_result":
			text, err := joinText(r.Content)
			if err != nil {
				return nil, fmt.Errorf("tool_result content: %v", err)
			}
			mark := core.MarkedOK
			if r.IsError {
				mark = core.MarkedError
			}
			out = append(out, core.Part{Kind: core.ToolResult, CallID: r.ToolUseID, Mark: mark, Text: text})
		default:
			out = append(out, core.Part{Kind: core.Other})
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
