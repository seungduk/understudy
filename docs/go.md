# Go library and test helpers

```go
o := us.Harness(t, fake, harness.Codex, us.HarnessRun{Dir: dir, Session: "t1/writer", Prompt: "fix it"})
// o.Exit, o.Stdout, o.Stderr, o.Version, o.Home; Protocol: protocol.Gemini picks pi's Gemini provider
```

`us.Harness` runs any profiled harness from its launch data (a fresh home folder, an empty
environment but `PATH`, dummy keys) and skips the test when the binary is missing
(`UNDERSTUDY_CODEX`, `UNDERSTUDY_OPENCODE`, `UNDERSTUDY_PI`, `UNDERSTUDY_GEMINI`, `UNDERSTUDY_AGY`
name other binaries). For Claude Code:

```go
import us "github.com/seungduk/understudy/understudytest"

func TestWriterSeat(t *testing.T) {
	fake := us.Start(t, us.ScriptFile("testdata/writer.yaml"))  // or us.YAML(...), us.Script(s), us.VirtualClock(t0)
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})
	run := us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/writer", Prompt: "change the greeting"})
	// run.Exit, run.Result, run.IsError, run.Events (stream-json), run.Version
	// fake.Messages() is the request log: session, turn, step, branch, attempt, outcome, trace
	// fake.Release("gate"), fake.WaitGate("gate", 1, time.Minute), fake.Advance(d), fake.Reset()
}
```

`us.Claude` runs `claude -p --output-format stream-json --verbose` with a fresh
`CLAUDE_CONFIG_DIR`, dummy credentials (`Auth: clienv.APIKey` or `clienv.Gateway`) and
`--dangerously-skip-permissions`; it skips the test, saying why, when `claude` is not installed
(`UNDERSTUDY_CLAUDE` names another binary). The server alone is `server.New(server.Options{...})`,
an `http.Handler`. See [examples/go/writer_test.go](../examples/go/writer_test.go).
