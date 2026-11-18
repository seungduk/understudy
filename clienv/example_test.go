package clienv_test

import (
	"fmt"

	"github.com/seungduk/understudy/clienv"
)

// Env gives the variables that point Claude Code at a fake server, with a
// dummy key only.
func ExampleEnv() {
	for _, kv := range clienv.Env("http://127.0.0.1:8765", "t1/writer", clienv.APIKey) {
		fmt.Println(kv)
	}
	// Output:
	// ANTHROPIC_BASE_URL=http://127.0.0.1:8765
	// DISABLE_AUTOUPDATER=1
	// DISABLE_TELEMETRY=1
	// DISABLE_ERROR_REPORTING=1
	// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
	// ANTHROPIC_API_KEY=understudy-dummy-key
	// ANTHROPIC_CUSTOM_HEADERS=X-Understudy-Session: t1/writer
}

// Merge builds a child process's environment: the inherited one (here a
// stand-in for os.Environ()) without any variable that could reach a real
// account, then the fake's. Use it as exec.Cmd.Env.
func ExampleMerge() {
	inherited := []string{"PATH=/usr/bin:/bin", "ANTHROPIC_AUTH_TOKEN=a-real-token", "HOME=/home/dev"}
	for _, kv := range clienv.Merge(inherited, []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8765"}) {
		fmt.Println(kv)
	}
	// Output:
	// PATH=/usr/bin:/bin
	// HOME=/home/dev
	// ANTHROPIC_BASE_URL=http://127.0.0.1:8765
}
