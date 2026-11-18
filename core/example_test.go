package core_test

import (
	"fmt"

	"github.com/seungduk/understudy/core"
)

// The neutral actions a script can ask any harness to take; each harness
// profile maps them onto its own tools.
func ExampleParseAction() {
	fmt.Println(core.ActionNames())
	a, ok := core.ParseAction("edit")
	fmt.Println(a == core.EditFile, ok)
	// Output:
	// [shell read write edit]
	// true true
}
