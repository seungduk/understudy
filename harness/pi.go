package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// Pi is badlogic's pi coding agent (pi -p).
const Pi ID = "pi"

func init() {
	register(&Profile{
		ID:        Pi,
		Name:      "pi coding agent",
		Protocols: []protocol.ID{protocol.OpenAIChat, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini},
		Detect:    Signature{Tools: []string{"read", "bash", "edit", "write"}, Without: []string{"todowrite"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:     {Tool: "bash", Template: `{"command":"{{command}}"}`},
			core.ReadFile:  {Tool: "read", Template: `{"path":"{{path}}"}`},
			core.WriteFile: {Tool: "write", Template: `{"path":"{{path}}","content":"{{content}}"}`},
			core.EditFile:  {Tool: "edit", Template: `{"path":"{{path}}","edits":[{"oldText":"{{old}}","newText":"{{new}}"}]}`},
		},
		Answers: map[string]core.Action{"bash": core.Shell, "read": core.ReadFile, "write": core.WriteFile, "edit": core.EditFile},
		// Measured with 0.73.1: a failed command ends with "Command exited with code N";
		// failed tools come back as their error text (unmarked over chat completions).
		Results: map[string]ResultRule{
			"bash":  {Exit: `(?m)^Command exited with code (\d+)`, Exit0: true, NonZeroIsError: true},
			"read":  {Error: `^(?:ENOENT|EISDIR|EACCES|Error)`},
			"write": {Error: `^(?:ENOENT|EISDIR|EACCES|Error)`},
			"edit":  {Error: `^(?:ENOENT|Error|Could not find|Found \d+ occurrences)`},
		},
		CWD: []string{`(?m)^\s*Current working directory:\s*(\S.*?)\s*$`},
		Launch: Launch{
			Binary: "pi", EnvVar: "UNDERSTUDY_PI",
			Args:    []string{"-p", "--mode", "json", "--model", "{{model}}", "{{prompt}}"},
			Version: []string{"--version"},
			Env:     []string{"HOME={{home}}", "PI_CODING_AGENT_DIR={{home}}/agent", "PI_OFFLINE=1"},
			Files: map[string]string{"{{home}}/agent/models.json": `{"providers": {
  "us-chat": {"baseUrl": "{{url}}/v1", "api": "openai-completions", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}, "models": [{"id": "fake-chat"}]},
  "us-responses": {"baseUrl": "{{url}}/v1", "api": "openai-responses", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}, "models": [{"id": "fake-responses"}]},
  "us-anthropic": {"baseUrl": "{{url}}", "api": "anthropic-messages", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}, "models": [{"id": "fake-claude"}]},
  "us-gemini": {"baseUrl": "{{url}}/v1beta", "api": "google-generative-ai", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}, "models": [{"id": "fake-gemini"}]}
}}
`},
			Model: "us-chat/fake-chat",
			Models: map[protocol.ID]string{protocol.OpenAIChat: "us-chat/fake-chat", protocol.OpenAIResponses: "us-responses/fake-responses",
				protocol.Anthropic: "us-anthropic/fake-claude", protocol.Gemini: "us-gemini/fake-gemini"},
		},
	})
}
