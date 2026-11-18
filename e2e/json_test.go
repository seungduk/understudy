//go:build e2e

package e2e

import "encoding/json"

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
