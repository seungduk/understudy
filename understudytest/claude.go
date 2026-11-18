package understudytest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/clienv"
)

// Permissions is how the CLI asks before tools run.
type Permissions int

const (
	// SkipPermissions runs tools without asking (--dangerously-skip-permissions),
	// which a scripted run needs to edit files. It is the default.
	SkipPermissions Permissions = iota
	// AskPermissions leaves the CLI's default: in -p mode, edits are refused.
	AskPermissions
)

// Run is one CLI invocation.
type Run struct {
	Dir     string // working directory; required
	Session string // session key, sent as the X-Understudy-Session header; "" sends none
	Prompt  string
	Auth    clienv.Auth
	Perms   Permissions
	Args    []string      // more CLI arguments
	Timeout time.Duration // zero means two minutes
}

// Outcome is what the CLI did.
type Outcome struct {
	Exit     int
	Events   []map[string]any // every stream-json line
	Result   string           // the result line's result
	IsError  bool             // the result line's is_error
	Version  string           // claude_code_version from the init line
	Stderr   string
	TimedOut bool
}

// ClaudePath is the CLI the helper runs: $UNDERSTUDY_CLAUDE, or claude on PATH.
func ClaudePath() (string, error) {
	if p := os.Getenv("UNDERSTUDY_CLAUDE"); p != "" {
		return p, nil
	}
	return exec.LookPath("claude")
}

// Claude runs `claude -p --output-format stream-json --verbose` against the
// fake, with a fresh config folder and dummy credentials. The test is skipped,
// with a message saying so, when the CLI is not installed.
func Claude(t testing.TB, f *Fake, r Run) Outcome {
	t.Helper()
	bin, err := ClaudePath()
	if err != nil {
		t.Skip("understudytest: the claude CLI is not installed (not on PATH and UNDERSTUDY_CLAUDE is unset); skipping a test that drives the real CLI")
	}
	if r.Dir == "" {
		t.Fatalf("understudytest: Run.Dir is required")
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	cfg := t.TempDir()
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if r.Perms == SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(append(args, r.Args...), r.Prompt)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = r.Dir
	cmd.Env = clienv.Merge(os.Environ(), append(clienv.Env(f.URL, r.Session, r.Auth), "CLAUDE_CONFIG_DIR="+filepath.Clean(cfg)))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errb, nil
	runErr := cmd.Run()
	o := Outcome{Stderr: errb.String(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		o.Exit = ee.ExitCode()
	default:
		t.Fatalf("understudytest: cannot run %s: %v", bin, runErr)
	}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		o.Events = append(o.Events, ev)
		switch ev["type"] {
		case "system":
			if ev["subtype"] == "init" {
				o.Version, _ = ev["claude_code_version"].(string)
			}
		case "result":
			o.Result, _ = ev["result"].(string)
			o.IsError, _ = ev["is_error"].(bool)
		}
	}
	return o
}

// Count is how many stream-json events have this type and, when given, subtype.
func (o Outcome) Count(typ, subtype string) int {
	n := 0
	for _, e := range o.Events {
		if e["type"] == typ && (subtype == "" || e["subtype"] == subtype) {
			n++
		}
	}
	return n
}
