package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// Antigravity is Google Antigravity's CLI (agy -p).
const Antigravity ID = "antigravity"

func init() {
	register(&Profile{
		ID:        Antigravity,
		Name:      "Google Antigravity CLI (agy)",
		Protocols: []protocol.ID{protocol.Gemini},
		Detect:    Signature{Tools: []string{"run_command", "edit_file", "read_file"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:     {Tool: "run_command", Template: `{"CommandLine":"{{command}}","toolSummary":"Run the command","WaitMsBeforeAsync":30000}`},
			core.ReadFile:  {Tool: "read_file", Template: `{"Path":"{{path}}"}`},
			core.WriteFile: {Tool: "write_file", Template: `{"Path":"{{path}}","Content":"{{content}}","Overwrite":true}`},
			core.EditFile:  {Tool: "edit_file", Template: `{"Path":"{{path}}","Target":"{{old}}","Replacement":"{{new}}"}`},
		},
		Answers: map[string]core.Action{"run_command": core.Shell, "read_file": core.ReadFile, "write_file": core.WriteFile, "edit_file": core.EditFile},
		Results: map[string]ResultRule{"run_command": {Exit: `(?m)^Command exited with code (\d+)`, Exit0: true, NonZeroIsError: true}},
		CWD:     []string{`(?m)^\s*Command Working Directory:\s*(\S.*?)\s*$`},
		Launch: Launch{
			Binary: "agy", EnvVar: "UNDERSTUDY_AGY",
			Args:            []string{"-p", "{{prompt}}", "--output-format", "stream-json", "--dangerously-skip-permissions"},
			Version:         []string{"--version"},
			Env:             []string{"HOME={{home}}", "GEMINI_API_KEY=understudy-dummy-key", "GOOGLE_GEMINI_BASE_URL={{url}}", "AGY_CLI_DISABLE_AUTO_UPDATE=true"},
			Files:           map[string]string{"{{home}}/.gemini/antigravity-cli/settings.json": `{"modelProvider": "gemini"}` + "\n"},
			Model:           "gemini-3.1-pro-preview",
			SessionInPrompt: true,
			Notes:           []string{"agy reads GEMINI_API_KEY only with modelProvider gemini in its settings; it sends no custom headers, so put [understudy-session: KEY] in the prompt."},
		},
	})
}
