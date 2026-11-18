package script_test

import (
	"errors"
	"fmt"

	"github.com/seungduk/understudy/script"
)

// Parse reads a script's YAML and checks it.
func ExampleParse() {
	sc, err := script.Parse([]byte(`
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply: {message: {text: "Hello from the understudy."}}
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, s := range sc.Outline() {
		fmt.Println(s.Key, len(s.Steps), "step(s)")
	}
	// Output:
	// default 1 step(s)
}

// A script with mistakes is refused with every problem, each with where it is
// and how to fix it, so an agent can repair the script without guessing.
func ExampleParse_problems() {
	_, err := script.Parse([]byte(`
schema: understudy/v1
sessions:
  default:
    steps:
      - name: Answer
        reply: {mesage: {text: "hi"}}
`))
	var ps script.Problems
	if errors.As(err, &ps) {
		for _, p := range ps {
			fmt.Printf("line %d: %s\n  fix: %s\n", p.Line, p.Message, p.Fix)
		}
	}
	// Output:
	// line 6: the step name "Answer" has characters a name cannot have
	//   fix: use 1-64 lower-case letters, digits, - and _, such as run-tests
	// line 7: unknown field mesage in reply
	//   fix: did you mean message? The fields of reply are: cut, http_error, limit, message, pace, stall, stream_error, tool_use
	// line 7: the reply has no kind
	//   fix: give the reply exactly one of: message, tool_use, http_error, stream_error, limit
}

// Compile checks a script built in Go, the same way Parse checks YAML.
func ExampleCompile() {
	sc, err := script.Compile(script.File{
		Schema: script.SchemaV1,
		Sessions: map[string]script.Session{
			"t1/writer": {Steps: []script.Step{
				{Name: "done", Reply: &script.Reply{Message: &script.Message{Text: "DONE"}}},
			}},
		},
	})
	fmt.Println(sc.Keys(), err)
	// Output:
	// [t1/writer] <nil>
}
