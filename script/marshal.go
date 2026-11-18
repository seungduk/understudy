package script

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Marshal writes a File as YAML that Parse reads back, fields in the order of
// the format, with a first comment line naming the schema guide.
func Marshal(f File) ([]byte, error) {
	var b bytes.Buffer
	schema := f.Schema
	if schema == "" {
		schema = SchemaV2
	}
	b.WriteString("# understudy script; format guide: SCRIPTS.md (schema " + schema + ")\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
