package script

import _ "embed"

// SchemaJSON is the JSON Schema (draft 2020-12) of the current format
// (understudy/v2), for editors and agents.
//
//go:embed understudy-v2.schema.json
var SchemaJSON []byte

// SchemaV1JSON is the JSON Schema of understudy/v1, unchanged.
//
//go:embed understudy-v1.schema.json
var SchemaV1JSON []byte
