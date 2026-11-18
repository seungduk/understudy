package record_test

import (
	"fmt"
	"os"

	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
)

// FromTranscript turns a real Claude Code run, saved with
// `claude -p --output-format stream-json --verbose`, into a script that
// replays the model's replies.
func ExampleFromTranscript() {
	f, err := os.Open("testdata/toolloop-2.1.325.jsonl")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	file, rep, err := record.FromTranscript(f, record.Meta{SessionKey: "t1/writer"})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%d steps, %d retries, Claude Code %s\n", rep.Steps, rep.Retries, rep.CLIVersion)

	// The result is an ordinary script: check it, then run it or save it.
	sc, err := script.Compile(file)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, s := range sc.Outline() {
		var names []string
		for _, st := range s.Steps {
			names = append(names, st.Name)
		}
		fmt.Println(s.Key, names)
	}
	// Output:
	// 5 steps, 0 retries, Claude Code 2.1.325
	// t1/writer [turn-1 turn-2 turn-3 turn-4 turn-5]
}
