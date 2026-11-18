package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// ClaudeCode is Anthropic's Claude Code CLI (claude -p).
const ClaudeCode ID = "claude-code"

func init() {
	register(&Profile{
		ID:        ClaudeCode,
		Name:      "Claude Code",
		Protocols: []protocol.ID{protocol.Anthropic},
		Detect:    Signature{Tools: []string{"Bash", "Read", "Edit"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:     {Tool: "Bash", Template: `{"command":"{{command}}","description":"Run the command"}`},
			core.ReadFile:  {Tool: "Read", Template: `{"file_path":"{{path}}"}`},
			core.WriteFile: {Tool: "Write", Template: `{"file_path":"{{path}}","content":"{{content}}"}`},
			core.EditFile:  {Tool: "Edit", Template: `{"file_path":"{{path}}","old_string":"{{old}}","new_string":"{{new}}"}`},
		},
		Answers: map[string]core.Action{"Bash": core.Shell, "Read": core.ReadFile, "Write": core.WriteFile, "Edit": core.EditFile},
		// Measured with 2.1.325: a failed Bash call is is_error with "Exit code N" first;
		// a lone test/[ ]/grep exiting 1 is not an error and shows no code.
		Results: map[string]ResultRule{"Bash": {Exit: `(?m)^(?:Error: )?Exit code (\d+)`, Exit0: true}},
		CWD:     []string{`(?m)^\s*-?\s*(?:Primary working directory|Working directory):\s*(\S.*?)\s*$`},
		Launch: Launch{
			Binary: "claude", EnvVar: "UNDERSTUDY_CLAUDE",
			Args:    []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions", "{{prompt}}"},
			Version: []string{"--version"},
			Env: []string{"ANTHROPIC_BASE_URL={{url}}", "ANTHROPIC_API_KEY=understudy-dummy-key", "CLAUDE_CONFIG_DIR={{home}}/.claude",
				"ANTHROPIC_CUSTOM_HEADERS=X-Understudy-Session: {{session}}", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1",
				"DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"},
			Model: "claude-opus-5-5",
			Notes: []string{"Edit refuses a file not Read first in the session.",
				"The weekly-limit message needs the gateway token (understudy env --auth gateway); with an API key a limit is a plain 429."},
		},
		Measured: []string{
			"529/500, in-stream error, cut close/reset: retried (exit 0 when a later attempt answers)",
			"400 with x-should-retry false: retried 5 times, then API Error: 400 ..., exit 1",
			"limit (gateway token): You've hit your weekly limit · resets <time>, exit 1, no retry; with an API key: API Error: Request rejected (429)",
		},
	})
}
