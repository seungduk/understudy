package protocol_test

import (
	"fmt"

	"github.com/seungduk/understudy/protocol"
)

// Every wire protocol understudy answers, by the name scripts and flags use.
func ExampleParse() {
	for _, id := range protocol.IDs {
		fmt.Println(id.Name())
	}
	id, ok := protocol.Parse("gemini")
	fmt.Println(id == protocol.Gemini, ok)
	// Output:
	// anthropic
	// openai-chat
	// openai-responses
	// gemini
	// true true
}
