package harness

import (
	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// Codex is OpenAI's Codex CLI (codex exec).
const Codex ID = "codex"

func init() {
	register(&Profile{
		ID:        Codex,
		Name:      "OpenAI Codex CLI",
		Protocols: []protocol.ID{protocol.OpenAIResponses},
		Detect:    Signature{Tools: []string{"exec_command", "apply_patch"}},
		Tools: map[core.Action]ToolMap{
			core.Shell:    {Tool: "exec_command", Template: `{"cmd":"{{command}}","yield_time_ms":30000}`},
			core.ReadFile: {Tool: "exec_command", Template: `{"cmd":"cat {{path|sh}}","yield_time_ms":30000}`},
			// apply_patch is a freeform tool: its input is the patch text.
			core.WriteFile: {Tool: "apply_patch", Kind: core.Freeform, Template: "*** Begin Patch\n*** Add File: {{path}}\n{{content|plus}}\n*** End Patch\n"},
			core.EditFile:  {Tool: "apply_patch", Kind: core.Freeform, Template: "*** Begin Patch\n*** Update File: {{path}}\n@@\n{{old|minus}}\n{{new|plus}}\n*** End Patch\n"},
		},
		Answers: map[string]core.Action{"exec_command": core.Shell, "write_stdin": core.Shell, "apply_patch": core.EditFile},
		Results: map[string]ResultRule{
			"exec_command": {Exit: `(?m)^Process exited with code (\d+)`, NonZeroIsError: true},
			"apply_patch":  {Exit: `(?m)^Exit code: (\d+)`, NonZeroIsError: true, Error: `(?m)^(?:apply_patch verification failed|Invalid patch)`},
		},
		CWD: []string{`<cwd>([^<]+)</cwd>`},
		Launch: Launch{
			Binary: "codex", EnvVar: "UNDERSTUDY_CODEX",
			Args:    []string{"exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "-C", "{{dir}}", "--", "{{prompt}}"},
			Version: []string{"--version"},
			Env:     []string{"HOME={{home}}", "CODEX_HOME={{home}}/.codex", "UNDERSTUDY_DUMMY_KEY=understudy-dummy-key"},
			Files: map[string]string{"{{home}}/.codex/config.toml": `model = "{{model}}"
model_provider = "understudy"
check_for_update_on_startup = false

[model_providers.understudy]
name = "understudy"
base_url = "{{url}}/v1"
env_key = "UNDERSTUDY_DUMMY_KEY"
wire_api = "responses"
http_headers = { "X-Understudy-Session" = "{{session}}" }
request_max_retries = 4
stream_max_retries = 5
`},
			Model: "gpt-5.5",
			Notes: []string{"codex exec reads more prompt from stdin when it is not a terminal: give it an empty stdin (</dev/null).",
				"Only the Responses API is supported since Codex dropped wire_api = \"chat\".",
				"apply_patch matches whole lines: an edit action's old and new are whole lines."},
		},
	})
}
