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
	".":                {}, // the module root: the embedded documents
	"internal/notify":  {},
	"ui":               {},
	"internal/wire":    {},
	"internal/clock":   {},
	"internal/gate":    {},
	"internal/reqlog":  {},
	"clienv":           {},
	"script":           {"internal/wire"},
	"internal/conv":    {"internal/wire", "script"},
	"internal/deliver": {"internal/clock", "internal/gate", "internal/reqlog", "internal/wire"},
	"server":           {"internal/clock", "internal/conv", "internal/deliver", "internal/gate", "internal/notify", "internal/reqlog", "internal/wire", "script"},
	"record":           {"internal/conv", "internal/wire", "script"},
	"explain":          {"script", "server"},
	"control":          {"script", "server"},
	"understudytest":   {"clienv", "script", "server"},
	"mcpserver":        {".", "clienv", "control", "explain", "record", "script", "server"},
	"cmd/understudy":   {"clienv", "control", "explain", "mcpserver", "record", "script", "server", "ui"},
	"internal/layers":  {},
	"e2e":              {},
	"examples/go":      {},
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
