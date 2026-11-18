package harness_test

import (
	"fmt"

	"github.com/seungduk/understudy/harness"
)

// Names lists every agent harness understudy has a profile for.
func ExampleNames() {
	fmt.Println(harness.Names())
	// Output:
	// [antigravity claude-code codex gemini-cli generic opencode pi]
}

// A profile is data: the APIs the harness speaks and how to launch it against
// understudy.
func ExampleLookup() {
	p, ok := harness.Lookup("codex")
	if !ok {
		return
	}
	fmt.Println(p.Name, "speaks", p.Protocols[0].Name(), "and runs as", p.Launch.Binary)
	// Output:
	// OpenAI Codex CLI speaks openai-responses and runs as codex
}
