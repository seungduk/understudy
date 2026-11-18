// Package layers checks that the packages depend only downwards, on the
// packages the table allows. It runs with go test ./..., so CI enforces it.
package layers

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const mod = "github.com/seungduk/understudy/"

// rel is a module package's path in the table: "." for the module root.
func rel(imp string) (string, bool) {
	if imp+"/" == mod {
		return ".", true
	}
	if !strings.HasPrefix(imp, mod) {
		return "", false
	}
	return strings.TrimPrefix(imp, mod), true
}

// allowed lists, per package, the module packages it may import (tests excluded).
// Lower layers first; nothing may import a package listed below it.
var allowed = map[string][]string{
	// The module root (the embedded documents) and leaf utilities.
	".":                     {},
	"internal/notify":       {},
	"ui":                    {},
	"internal/clock":        {},
	"internal/gate":         {},
	"internal/reqlog":       {},
	"internal/protocol/sse": {},
	// The neutral model and the interfaces: no wire protocol, no harness.
	"core":     {},
	"protocol": {"core"},
	// The script engine knows only core.
	"script": {"core"},
	// Harness profiles are data over core and the protocol IDs.
	"harness": {"core", "protocol"},
	// One package per wire protocol, each only over core and the interface.
	"internal/protocol/anthropic":  {"core", "internal/protocol/sse", "protocol"},
	"internal/protocol/openai":     {"core"},
	"internal/protocol/openaichat": {"core", "internal/protocol/openai", "internal/protocol/sse", "protocol"},
	"internal/protocol/responses":  {"core", "internal/protocol/openai", "internal/protocol/sse", "protocol"},
	"internal/protocol/gemini":     {"core", "internal/protocol/sse", "protocol"},
	"internal/protocol/codectest":  {"core", "internal/protocol/sse", "protocol"},
	"internal/protocol/registry":   {"internal/protocol/anthropic", "internal/protocol/gemini", "internal/protocol/openaichat", "internal/protocol/responses", "protocol"},
	// Reading a request for the engine: neutral.
	"internal/conv":    {"core", "harness", "script"},
	"internal/deliver": {"internal/clock", "internal/gate", "internal/reqlog"},
	// The top: protocol-agnostic server, recorder, control and tools.
	"server":          {"core", "harness", "internal/clock", "internal/conv", "internal/deliver", "internal/gate", "internal/notify", "internal/protocol/registry", "internal/reqlog", "protocol", "script"},
	"record":          {"core", "harness", "internal/conv", "internal/protocol/anthropic", "internal/protocol/registry", "protocol", "script"},
	"explain":         {"script", "server"},
	"control":         {"script", "server"},
	"clienv":          {},
	"understudytest":  {"clienv", "harness", "protocol", "script", "server"},
	"mcpserver":       {".", "clienv", "control", "explain", "harness", "protocol", "record", "script", "server"},
	"cmd/understudy":  {"clienv", "control", "core", "explain", "harness", "mcpserver", "protocol", "record", "script", "server", "ui"},
	"internal/layers": {},
	"e2e":             {},
	"examples/go":     {},
}

func TestLayers(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, strings.TrimSuffix(mod, "/")+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		pkg, _ := rel(f[0])
		seen[pkg] = true
		ok, known := allowed[pkg]
		if !known {
			t.Errorf("package %s is not in the layer table; add it to allowed in internal/layers", pkg)
			continue
		}
		set := map[string]bool{}
		for _, a := range ok {
			set[a] = true
		}
		for _, imp := range f[1:] {
			dep, ok := rel(imp)
			if !ok {
				continue
			}
			if !set[dep] {
				t.Errorf("%s imports %s, which its layer does not allow", pkg, dep)
			}
		}
	}
	var missing []string
	for p := range allowed {
		if !seen[p] && p != "e2e" && p != "examples/go" {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the layer table names packages that do not exist: %v", missing)
	}
}
