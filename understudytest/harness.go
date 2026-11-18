package understudytest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/protocol"
)

// HarnessRun is one run of any harness.
type HarnessRun struct {
	Dir      string // working directory; required
	Session  string // session key, sent the way the profile sends it ("" sends "default")
	Prompt   string
	Protocol protocol.ID   // 0: the harness's usual protocol
	Model    string        // "": the profile's model for Protocol
	Timeout  time.Duration // zero means three minutes
}

// HarnessOutcome is what the harness did.
type HarnessOutcome struct {
	Exit     int
	Stdout   string
	Stderr   string
	Version  string
	TimedOut bool
	Home     string // the run's home folder (transcripts and session files are under it)
}

// Harness runs a harness once against the fake with the profile's launch data:
// an empty environment but PATH, a fresh home folder, dummy credentials, and
// the server as its model endpoint. The test is skipped when the harness is
// not installed.
func Harness(t testing.TB, f *Fake, id harness.ID, r HarnessRun) HarnessOutcome {
	t.Helper()
	pr, ok := harness.Lookup(string(id))
	if !ok {
		t.Fatalf("understudytest: no harness profile %q", id)
	}
	if r.Dir == "" {
		t.Fatalf("understudytest: HarnessRun.Dir is required")
	}
	// Not t.TempDir: a harness may leave a background child writing in its home
	// (Codex syncs plugins) after it exits, and the test should not fail on that.
	home, err := os.MkdirTemp("", "understudy-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 5 && os.RemoveAll(home) != nil; i++ {
			time.Sleep(200 * time.Millisecond)
		}
	})
	s, err := pr.Prepare(harness.Target{URL: f.URL, Session: r.Session, Home: home, Dir: r.Dir, Prompt: r.Prompt, Protocol: r.Protocol, Model: r.Model})
	if err != nil {
		t.Skipf("understudytest: %v; skipping a test that drives the real %s", err, pr.Name)
	}
	for path, content := range s.Files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	o := HarnessOutcome{Home: home}
	if len(pr.Launch.Version) > 0 {
		cmd := exec.Command(s.Binary, pr.Launch.Version...)
		cmd.Env = s.Env
		out, _ := cmd.CombinedOutput()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		o.Version = strings.TrimSpace(lines[len(lines)-1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Binary, s.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = r.Dir, s.Env, nil
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	o.Stdout, o.Stderr, o.TimedOut = out.String(), errb.String(), errors.Is(ctx.Err(), context.DeadlineExceeded)
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		o.Exit = ee.ExitCode()
	default:
		t.Fatalf("understudytest: cannot run %s: %v", s.Binary, runErr)
	}
	return o
}
