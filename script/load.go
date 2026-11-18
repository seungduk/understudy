package script

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// valueKind is the closed set of value shapes in the format.
type valueKind int

const (
	vString valueKind = iota + 1
	vInt
	vObject   // a known object: its fields are in shapes[obj]
	vList     // a list of elem
	vMap      // a map from keys to elem (sessions)
	vFreeJSON // any YAML value (tool input)
)

type field struct {
	kind valueKind
	obj  string // vObject: shape name; vList/vMap: element shape name ("" with elemKind)
	elem valueKind
}

func str() field            { return field{kind: vString} }
func num() field            { return field{kind: vInt} }
func obj(n string) field    { return field{kind: vObject, obj: n} }
func listOf(n string) field { return field{kind: vList, obj: n, elem: vObject} }
func mapOf(n string) field  { return field{kind: vMap, obj: n, elem: vObject} }
func freeObject() field     { return field{kind: vFreeJSON} }

// shapes is every object of the format and its fields. schema_test.go checks
// that the published JSON Schema says the same.
var shapes = map[string]map[string]field{
	"file":       {"schema": str(), "divergence": str(), "provenance": obj("provenance"), "harness": str(), "sessions": mapOf("session")},
	"provenance": {"source": str(), "run": str(), "task": str(), "seat": str(), "session": str(), "recorded_at": str(), "cli_version": str(), "note": str()},
	"session":    {"harness": str(), "steps": listOf("step")},
	"step": {"name": str(), "expect": obj("matcher"), "reply": obj("reply"), "attempts": listOf("reply"), "next": str(),
		"cases": listOf("case"), "otherwise": obj("branch")},
	"case":         {"when": obj("matcher"), "reply": obj("reply"), "attempts": listOf("reply"), "next": str()},
	"branch":       {"reply": obj("reply"), "attempts": listOf("reply"), "next": str()},
	"matcher":      {"tool": str(), "action": str(), "result": str(), "exit_code": num(), "contains": str(), "regex": str()},
	"reply":        {"message": obj("message"), "tool_use": obj("tool_use"), "http_error": obj("http_error"), "stream_error": obj("stream_error"), "limit": obj("limit"), "stall": obj("stall"), "cut": obj("cut"), "pace": obj("pace")},
	"message":      {"thinking": str(), "text": str(), "stop": str()},
	"tool_use":     {"thinking": str(), "text": str(), "calls": listOf("call")},
	"call":         {"tool": str(), "input": freeObject(), "shell": obj("shell"), "read": obj("read"), "write": obj("write"), "edit": obj("edit")},
	"shell":        {"command": str()},
	"read":         {"path": str()},
	"write":        {"path": str(), "content": str()},
	"edit":         {"path": str(), "old": str(), "new": str()},
	"http_error":   {"error": str(), "message": str(), "retry_after_seconds": num(), "retry": str()},
	"stream_error": {"error": str(), "message": str(), "partial_text": str()},
	"limit":        {"window": str(), "resets_in_seconds": num()},
	"stall":        {"seconds": num(), "gate": str(), "at": str()},
	"cut":          {"after_events": num(), "how": str()},
	"pace":         {"first_event_ms": num(), "between_events_ms": num()},
}

// lines maps a problem path to its line, filled while walking the YAML.
type lines map[string]int

// Parse reads a script from YAML (or JSON) and compiles it. On failure the
// error is Problems, every problem with its line, rule and fix.
func Parse(data []byte) (*Script, error) {
	f, ls, err := parseFile(data)
	if err != nil {
		return nil, err
	}
	s, ps := compile(f, ls)
	if len(ps) > 0 {
		return nil, ps.sorted()
	}
	s.source = append([]byte(nil), data...)
	return s, nil
}

// ParseFile reads a script into a File without compiling it (for tools that
// edit scripts). The error is Problems for format problems.
func ParseFile(data []byte) (File, error) {
	f, _, err := parseFile(data)
	return f, err
}

// Load reads and compiles the script at path.
func Load(path string) (*Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Problems{{Rule: RuleSyntax, Message: fmt.Sprintf("cannot read %s: %v", path, err), Fix: "give the path of a readable script file"}}
	}
	return Parse(data)
}

func parseFile(data []byte) (File, lines, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return File{}, nil, Problems{{Rule: RuleSyntax, Message: "the file is not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: "),
			Fix: "fix the YAML syntax at the line named; indent with spaces, quote strings that hold ': ' or start with special characters"}}
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return File{}, nil, Problems{{Rule: RuleRequired, Message: "the file is empty", Fix: "start the file with 'schema: " + SchemaV2 + "' and a 'sessions:' map"}}
	}
	doc := root.Content[0]
	ls := lines{}
	var ps Problems
	walk(doc, field{kind: vObject, obj: "file"}, "", ls, &ps)
	for _, p := range ps {
		if p.Rule != RuleUnknownField {
			return File{}, nil, ps.sorted()
		}
	}
	var f File
	if err := doc.Decode(&f); err != nil {
		return File{}, nil, Problems{{Rule: RuleType, Message: err.Error(), Fix: "give each field the type the guide shows"}}
	}
	if len(ps) > 0 {
		// Unknown fields do not stop the other checks: report everything at once.
		_, more := compile(f, ls)
		return File{}, nil, append(ps, more...).sorted()
	}
	return f, ls, nil
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.MappingNode:
		return "a map"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a single value"
	case yaml.AliasNode:
		return "an alias"
	}
	return "nothing"
}

func walk(n *yaml.Node, f field, path string, ls lines, ps *Problems) {
	if n.Kind == yaml.AliasNode {
		*ps = append(*ps, Problem{Path: path, Line: n.Line, Rule: RuleType, Message: "YAML aliases (*name) are not supported", Fix: "write the value out in full"})
		return
	}
	ls[path] = n.Line
	add := func(rule Rule, msg, fix string) {
		*ps = append(*ps, Problem{Path: path, Line: n.Line, Rule: rule, Message: msg, Fix: fix})
	}
	switch f.kind {
	case vString:
		if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
			add(RuleType, "expected text, found "+kindName(n.Kind), "write a single text value; use a YAML block (|) for several lines")
		}
	case vInt:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
			add(RuleType, fmt.Sprintf("expected a whole number, found %q", n.Value), "write a whole number without quotes, such as 3")
			return
		}
		if _, err := strconv.Atoi(n.Value); err != nil {
			add(RuleRange, fmt.Sprintf("%q is not a whole number this format can hold", n.Value), "write a smaller whole number")
		}
	case vFreeJSON:
		if n.Kind != yaml.MappingNode {
			add(RuleType, "expected a map of the tool's input fields, found "+kindName(n.Kind), "write input as a map, such as {file_path: /work/a.txt}")
		}
	case vList:
		if n.Kind != yaml.SequenceNode {
			add(RuleType, "expected a list, found "+kindName(n.Kind), "write each item on its own line starting with '- '")
			return
		}
		for i, c := range n.Content {
			walk(c, field{kind: f.elem, obj: f.obj}, fmt.Sprintf("%s[%d]", path, i), ls, ps)
		}
	case vMap:
		if n.Kind != yaml.MappingNode {
			add(RuleType, "expected a map, found "+kindName(n.Kind), "write 'key:' lines under it")
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			walk(n.Content[i+1], field{kind: f.elem, obj: f.obj}, fmt.Sprintf("%s[%q]", path, k.Value), ls, ps)
		}
	case vObject:
		if n.Kind != yaml.MappingNode {
			add(RuleType, "expected a map, found "+kindName(n.Kind), "write the fields of "+f.obj+" as 'field: value' lines")
			return
		}
		fields := shapes[f.obj]
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			sub := k.Value
			if path != "" {
				sub = path + "." + k.Value
			}
			if seen[k.Value] {
				*ps = append(*ps, Problem{Path: sub, Line: k.Line, Rule: RuleUnique, Message: "the field " + k.Value + " is given twice", Fix: "keep one of them"})
				continue
			}
			seen[k.Value] = true
			ff, ok := fields[k.Value]
			if !ok {
				names := make([]string, 0, len(fields))
				for name := range fields {
					names = append(names, name)
				}
				sort.Strings(names)
				fix := "remove it; the fields of " + f.obj + " are: " + strings.Join(names, ", ")
				if c := closest(k.Value, names); c != "" {
					fix = "did you mean " + c + "? The fields of " + f.obj + " are: " + strings.Join(names, ", ")
				}
				*ps = append(*ps, Problem{Path: sub, Line: k.Line, Rule: RuleUnknownField, Message: "unknown field " + k.Value + " in " + f.obj, Fix: fix})
				continue
			}
			walk(v, ff, sub, ls, ps)
		}
	}
}
