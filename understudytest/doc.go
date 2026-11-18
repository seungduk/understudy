// Package understudytest runs an understudy server inside a Go test and
// drives the real agent CLIs against it: Claude Code with Claude, and any
// harness with a profile (Codex, opencode, pi, Gemini CLI, Antigravity's CLI)
// with Harness.
//
//	fake := understudytest.Start(t, understudytest.ScriptFile("testdata/fix.yaml"))
//	dir := understudytest.TempDir(t, map[string]string{"main.go": src})
//	run := understudytest.Claude(t, fake, understudytest.Run{Dir: dir, Session: "t1/writer", Prompt: "fix it"})
//	if run.Result != "Tests pass." { ... }
//	fake.Messages() // the request log: session, turn, step, outcome
//
// Start serves on a free loopback port until the test ends. Claude and Harness
// give each run a fresh home folder, an environment holding only PATH and dummy
// credentials, and the fake as the model endpoint, and skip the test (saying
// why) when the CLI is not installed. Gates (Fake.Release, Fake.WaitGate) and
// the virtual clock (VirtualClock, Fake.Advance) let a test hold a reply and
// look at the agent while it waits.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package understudytest
