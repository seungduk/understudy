package script

// The JSON Schema is written by hand (script/understudy-v1.schema.json) so it can
// carry descriptions; this test keeps it in step with the loader's field table.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// v2Only are the fields understudy/v1 does not have, per shape.
var v2Only = map[string][]string{"file": {"harness"}, "session": {"harness"}, "matcher": {"action"}, "call": {"shell", "read", "write", "edit"},
	"shell": nil, "read": nil, "write": nil, "edit": nil}

func TestSchemaMatchesFormat(t *testing.T) {
	checkSchema(t, SchemaJSON, func(string) []string { return nil }, false)
}

func TestSchemaV1MatchesFormat(t *testing.T) {
	checkSchema(t, SchemaV1JSON, func(shape string) []string { return v2Only[shape] }, true)
}

func checkSchema(t *testing.T, data []byte, without func(shape string) []string, v1 bool) {
	t.Helper()
	var s struct {
		Schema string `json:"$schema"`
		ID     string `json:"$id"`
		Ref    string `json:"$ref"`
		Defs   map[string]struct {
			Type                 string                     `json:"type"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Schema, "2020-12") || s.Ref != "#/$defs/file" {
		t.Fatal(s.Schema, s.Ref)
	}
	for name, fields := range shapes {
		if only, ok := v2Only[name]; v1 && ok && only == nil {
			continue // a shape v1 does not have
		}
		d, ok := s.Defs[name]
		if !ok {
			t.Errorf("schema has no $defs/%s", name)
			continue
		}
		if d.AdditionalProperties == nil || *d.AdditionalProperties {
			t.Errorf("%s: additionalProperties must be false", name)
		}
		var want, got []string
		skip := map[string]bool{}
		for _, f := range without(name) {
			skip[f] = true
		}
		for f := range fields {
			if !skip[f] {
				want = append(want, f)
			}
		}
		for f := range d.Properties {
			got = append(got, f)
		}
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("%s: schema fields %v, format fields %v", name, got, want)
		}
	}
	for name := range s.Defs {
		if _, ok := shapes[name]; !ok && name != "errorKind" && name != "gateName" && name != "toolName" && name != "harnessName" {
			t.Errorf("schema has $defs/%s, which the format does not", name)
		}
	}
}
