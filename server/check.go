package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/script"
)

// Check finds what a checked script names that this server does not know: a
// harness without a profile. The script package checks the form of a harness
// name; only the server knows which harnesses exist.
func Check(sc *script.Script) script.Problems {
	f := sc.File()
	var ps script.Problems
	add := func(path, name string) {
		if name == "" {
			return
		}
		if _, ok := harness.Lookup(name); ok {
			return
		}
		ps = append(ps, script.Problem{Path: path, Rule: script.RuleEnum, Message: fmt.Sprintf("there is no harness %q", name),
			Fix: "name one of: " + strings.Join(harness.Names(), ", ") + "; or leave harness out to let the server tell from the request"})
	}
	add("harness", f.Harness)
	keys := make([]string, 0, len(f.Sessions))
	for k := range f.Sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(fmt.Sprintf("sessions[%q].harness", k), f.Sessions[k].Harness)
	}
	return ps
}

// Parse is script.Parse followed by Check: a script this server can run.
func Parse(data []byte) (*script.Script, error) {
	sc, err := script.Parse(data)
	if err != nil {
		return nil, err
	}
	if ps := Check(sc); len(ps) > 0 {
		return nil, ps
	}
	return sc, nil
}
