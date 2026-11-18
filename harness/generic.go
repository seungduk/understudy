package harness

import "github.com/seungduk/understudy/protocol"

func init() {
	register(&Profile{
		ID:        Generic,
		Name:      "an unknown client",
		Protocols: protocol.IDs,
		CWD: []string{
			`(?m)^\s*-?\s*(?:Primary working directory|Working directory|Current working directory|Command Working Directory):\s*(\S.*?)\s*$`,
			`<cwd>([^<]+)</cwd>`,
		},
	})
}
