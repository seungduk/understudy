package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// GeminiCLI is Google's Gemini CLI (gemini -p).
const GeminiCLI ID = "gemini-cli"

func init() {
	register(&Profile{
		ID:        GeminiCLI,
		Name:      "Gemini CLI",
		Protocols: []protocol.ID{protocol.Gemini},
		Detect:    Signature{Tools: []string{"run_shell_command", "read_file", "replace"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:     {Tool: "run_shell_command", Template: `{"command":"{{command}}","description":"Run the command"}`},
			core.ReadFile:  {Tool: "read_file", Template: `{"file_path":"{{path}}"}`},
			core.WriteFile: {Tool: "write_file", Template: `{"file_path":"{{path}}","content":"{{content}}"}`},
			core.EditFile:  {Tool: "replace", Template: `{"file_path":"{{path}}","old_string":"{{old}}","new_string":"{{new}}","instruction":"Replace the text"}`},
		},
		Answers: map[string]core.Action{"run_shell_command": core.Shell, "read_file": core.ReadFile, "write_file": core.WriteFile, "replace": core.EditFile},
		// Measured with 0.68.0: "Exit Code: N" for a command that fails (none for 0);
		// failed tools are functionResponse errors, which the wire marks.
		Results: map[string]ResultRule{"run_shell_command": {Exit: `(?m)^Exit Code: (\d+)`, Exit0: true, NonZeroIsError: true}},
		CWD:     []string{`(?m)\*\*Workspace Directories:\*\*\s*\n\s*-\s*(\S.*?)\s*$`},
		Aux:     []Aux{{Name: "routing", Contains: "Task Routing AI", Reply: `{"complexity_reasoning":"scripted","complexity_score":10}`}},
		Launch: Launch{
			Binary: "gemini", EnvVar: "UNDERSTUDY_GEMINI",
			Args:    []string{"-p", "{{prompt}}", "--output-format", "stream-json", "--yolo", "--model", "{{model}}"},
			Version: []string{"--version"},
			Env: []string{"HOME={{home}}", "GEMINI_API_KEY=understudy-dummy-key", "GOOGLE_GEMINI_BASE_URL={{url}}",
				"GEMINI_CLI_CUSTOM_HEADERS=X-Understudy-Session: {{session}}"},
			Files: map[string]string{"{{home}}/.gemini/settings.json": `{"security": {"auth": {"selectedType": "gemini-api-key"}, "folderTrust": {"enabled": false}},
 "privacy": {"usageStatisticsEnabled": false}, "telemetry": {"enabled": false}, "general": {"disableAutoUpdate": true}}
`},
			Model: "gemini-3.1-pro-preview",
			Notes: []string{"Gemini CLI first asks a routing model how complex the task is; understudy answers it without the script (log: aux routing)."},
		},
	})
}
