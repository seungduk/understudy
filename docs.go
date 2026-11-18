// Package understudy holds the documents the tools serve: the script guide and
// the README, embedded so a built binary carries them.
package understudy

import _ "embed"

// ScriptsGuide is SCRIPTS.md, the one-page guide to the script format.
//
//go:embed SCRIPTS.md
var ScriptsGuide string

// Readme is README.md.
//
//go:embed README.md
var Readme string
