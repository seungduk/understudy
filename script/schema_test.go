package script

// The JSON Schema is written by hand (script/understudy-v1.schema.json) so it can
// carry descriptions; this test keeps it in step with the loader's field table.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestSchemaMatchesFormat(t *testing.T) {
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
	if err := json.Unmarshal(SchemaJSON, &s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Schema, "2020-12") || s.Ref != "#/$defs/file" {
		t.Fatal(s.Schema, s.Ref)
	}
	for name, fields := range shapes {
		d, ok := s.Defs[name]
		if !ok {
			t.Errorf("schema has no $defs/%s", name)
			continue
		}
		if d.AdditionalProperties == nil || *d.AdditionalProperties {
			t.Errorf("%s: additionalProperties must be false", name)
		}
		var want, got []string
		for f := range fields {
			want = append(want, f)
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
		if _, ok := shapes[name]; !ok && name != "errorKind" && name != "gateName" && name != "toolName" {
			t.Errorf("schema has $defs/%s, which the format does not", name)
		}
	}
}
