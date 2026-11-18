// Command understudy runs the fake model API (Anthropic Messages, OpenAI Chat
// Completions and Responses, Gemini) and its tools.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func usage() string {
	return `understudy: a scriptable fake of the model APIs agent harnesses use (Anthropic Messages,
OpenAI Chat Completions and Responses, Gemini), for testing agents.

Usage:
  understudy serve [--addr 127.0.0.1:8765] [--script FILE] [--clock real|virtual] [--ui on|off] [--mcp on|off]
  understudy mcp [--url http://127.0.0.1:8765] [--server auto|attach|start] [--clock real|virtual] [--script FILE]
  understudy validate FILE [--json]
  understudy explain (--log FILE | --url URL) [--script FILE] [--session KEY] [--json]
  understudy record --transcript FILE [--session KEY] [--run ID] [--task ID] [--seat NAME] [--cwd DIR] [--divergence strict|lenient] [--out FILE]
  understudy record-proxy --upstream URL [--addr ADDR] --out FILE
  understudy env [--url URL] [--session KEY] [--auth api-key|gateway]       (Claude Code)
  understudy harnesses [--json]
  understudy setup --harness NAME --home DIR [--url URL] [--session KEY] [--dir DIR] [--protocol P] [--prompt TEXT] [--json]
  understudy schema
  understudy version

serve also answers the web UI at /_understudy/ui/ and MCP (streamable HTTP) at /_understudy/mcp.
mcp is the MCP server over stdio for agents; it uses the server at --url, or starts one there.

Run 'understudy <command> --help' for a command's flags.
`
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return 2
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:], stdout, stderr)
	case "mcp":
		return cmdMCP(args[1:], os.Stdin, stdout, stderr)
	case "validate":
		return cmdValidate(args[1:], stdout, stderr)
	case "explain":
		return cmdExplain(args[1:], stdout, stderr)
	case "record":
		return cmdRecord(args[1:], stdout, stderr)
	case "record-proxy":
		return cmdRecordProxy(args[1:], stdout, stderr)
	case "env":
		return cmdEnv(args[1:], stdout, stderr)
	case "harnesses":
		return cmdHarnesses(args[1:], stdout, stderr)
	case "setup":
		return cmdSetup(args[1:], stdout, stderr)
	case "schema":
		_, _ = stdout.Write(script.SchemaJSON)
		return 0
	case "version":
		fmt.Fprintln(stdout, "understudy", server.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage())
		return 0
	}
	fmt.Fprintf(stderr, "understudy: unknown command %q\n\n%s", args[0], usage())
	return 2
}
