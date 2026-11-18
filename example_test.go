package understudy_test

import (
	"fmt"
	"strings"

	"github.com/seungduk/understudy"
)

// The script guide ships inside the module, so tools built on understudy can
// hand it to an agent without a network fetch.
func ExampleScriptsGuide() {
	first, _, _ := strings.Cut(understudy.ScriptsGuide, "\n")
	fmt.Println(first)
	// Output:
	// # Writing understudy scripts
}
