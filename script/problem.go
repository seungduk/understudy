package script

import (
	"fmt"
	"sort"
	"strings"
)

// Rule is the closed set of checks a script can fail.
type Rule string

const (
	RuleSyntax       Rule = "syntax"         // not YAML
	RuleSchema       Rule = "schema"         // schema missing or another version
	RuleUnknownField Rule = "unknown_field"  // a field this version does not have
	RuleType         Rule = "type"           // a value of the wrong type
	RuleRequired     Rule = "required"       // a required field is missing or empty
	RuleOneOf        Rule = "one_of"         // not exactly one of a set of fields
	RuleEnum         Rule = "enum"           // a value outside a closed set
	RuleRange        Rule = "range"          // a number out of bounds
	RulePattern      Rule = "pattern"        // a name or key of the wrong form
	RuleUnique       Rule = "unique"         // a duplicate name
	RuleReference    Rule = "reference"      // next names no step
	RuleRegex        Rule = "regex"          // a regex that does not compile
	RuleTemplate     Rule = "template"       // a {{...}} other than {{cwd}}
	RuleNotApplied   Rule = "not_applicable" // a field that has no effect where it is
)

// Problem is one thing wrong with a script, with the fix.
type Problem struct {
	Path    string `json:"path"`           // where, such as sessions["t1/writer"].steps[2].reply
	Line    int    `json:"line,omitempty"` // 1-based line in the file, when known
	Rule    Rule   `json:"rule"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

func (p Problem) String() string {
	loc := p.Path
	if p.Line > 0 {
		loc = fmt.Sprintf("line %d: %s", p.Line, p.Path)
	}
	if loc == "" {
		loc = "file"
	}
	return fmt.Sprintf("%s: %s (%s). Fix: %s", loc, p.Message, p.Rule, p.Fix)
}

// Problems is every problem of a script, in file order. It is an error.
type Problems []Problem

func (ps Problems) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the script has %d problem(s):", len(ps))
	for _, p := range ps {
		b.WriteString("\n- ")
		b.WriteString(p.String())
	}
	return b.String()
}

func (ps Problems) sorted() Problems {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Line != ps[j].Line {
			if ps[i].Line == 0 || ps[j].Line == 0 {
				return ps[j].Line == 0 && ps[i].Line != 0
			}
			return ps[i].Line < ps[j].Line
		}
		return false
	})
	return ps
}

// closest is the candidate nearest to s by edit distance, if near enough.
func closest(s string, candidates []string) string {
	best, bestD := "", 1<<30
	for _, c := range candidates {
		if d := editDistance(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	if bestD <= 2 || bestD*3 <= len(s) {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := prev[j-1]
			if a[i-1] != b[j-1] {
				c++
			}
			cur[j] = min(c, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
