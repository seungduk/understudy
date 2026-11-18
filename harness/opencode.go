package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// OpenCode is sst's opencode (opencode run).
const OpenCode ID = "opencode"

func init() {
	register(&Profile{
		ID:        OpenCode,
		Name:      "opencode",
		Protocols: []protocol.ID{protocol.OpenAIChat, protocol.Anthropic},
		Detect:    Signature{Tools: []string{"bash", "read", "edit", "write", "todowrite"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:     {Tool: "bash", Template: `{"command":"{{command}}","description":"Run the command"}`},
			core.ReadFile:  {Tool: "read", Template: `{"filePath":"{{path}}"}`},
			core.WriteFile: {Tool: "write", Template: `{"filePath":"{{path}}","content":"{{content}}"}`},
			core.EditFile:  {Tool: "edit", Template: `{"filePath":"{{path}}","oldString":"{{old}}","newString":"{{new}}"}`},
		},
		Answers: map[string]core.Action{"bash": core.Shell, "read": core.ReadFile, "write": core.WriteFile, "edit": core.EditFile},
		// Measured with 1.19.6: bash results carry only the output, never the exit
		// code; failed tools come back as plain text such as "File not found: ...".
		Results: map[string]ResultRule{
			"read":  {Error: `^(?:File not found|Error)`},
			"write": {Error: `^Error`},
			"edit":  {Error: `^(?:Error|File not found|oldString not found)`},
		},
		CWD: []string{`(?m)^\s*Working directory:\s*(\S.*?)\s*$`},
		Aux: []Aux{{Name: "title", Contains: "You are a title generator", Reply: "Scripted session"}},
		Launch: Launch{
			Binary: "opencode", EnvVar: "UNDERSTUDY_OPENCODE",
			Args:    []string{"run", "--format", "json", "--model", "{{model}}", "{{prompt}}"},
			Version: []string{"--version"},
			Env: []string{"HOME={{home}}", "XDG_CONFIG_HOME={{home}}/.config", "XDG_DATA_HOME={{home}}/.local/share", "XDG_CACHE_HOME={{home}}/.cache",
				"XDG_STATE_HOME={{home}}/.local/state", "OPENCODE_CONFIG={{home}}/opencode.json", "OPENCODE_DISABLE_MODELS_FETCH=1",
				"OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_LSP_DOWNLOAD=1"},
			Files: map[string]string{"{{home}}/opencode.json": `{
  "$schema": "https://opencode.ai/config.json",
  "autoupdate": false,
  "share": "disabled",
  "permission": {"edit": "allow", "bash": "allow", "webfetch": "deny"},
  "provider": {
    "understudy": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "understudy",
      "options": {"baseURL": "{{url}}/v1", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}},
      "models": {"fake-chat": {"name": "fake-chat"}}
    },
    "deepseek": {
      "options": {"baseURL": "{{url}}", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}},
      "models": {"deepseek-chat": {"name": "deepseek-chat"}}
    },
    "anthropic": {
      "options": {"baseURL": "{{url}}/v1", "apiKey": "understudy-dummy-key", "headers": {"X-Understudy-Session": "{{session}}"}},
      "models": {"fake-claude": {"name": "fake-claude"}}
    }
  },
  "model": "{{model}}",
  "small_model": "{{model}}"
}
`},
			Model:  "understudy/fake-chat",
			Models: map[protocol.ID]string{protocol.OpenAIChat: "understudy/fake-chat", protocol.Anthropic: "anthropic/fake-claude"},
			Notes: []string{"The first run installs the provider package (@ai-sdk/openai-compatible) from npm into the run's cache.",
				"opencode asks a small model for a session title; understudy answers it without the script (log: aux title)."},
		},
	})
}
