package script

import _ "embed"

// SchemaJSON is the JSON Schema (draft 2020-12) of the format, for editors and agents.
//
//go:embed understudy-v1.schema.json
var SchemaJSON []byte
