//go:build probe

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/protocol"
	us "github.com/seungduk/understudy/understudytest"
)

// TestMeasure runs each fault and limit once through the harness named by
// UNDERSTUDY_PROBE and reports how many requests it sent, its exit code and
// what it printed, to measure its retry and limit behaviour.
func TestMeasure(t *testing.T) {
	id := harness.ID(os.Getenv("UNDERSTUDY_PROBE"))
	proto, _ := protocol.Parse(os.Getenv("UNDERSTUDY_PROBE_PROTOCOL"))
	only := os.Getenv("UNDERSTUDY_MEASURE")
	cases := []struct{ name, step string }{
		{"api_error", "attempts: [{http_error: {error: api_error}}, {message: {text: RECOVERED}}]"},
		{"overloaded", "attempts: [{http_error: {error: overloaded}}, {message: {text: RECOVERED}}]"},
		{"rate_limit", "attempts: [{http_error: {error: rate_limit, retry_after_seconds: 1}}, {message: {text: RECOVERED}}]"},
		{"stream_error", "attempts: [{stream_error: {error: overloaded, partial_text: half}}, {message: {text: RECOVERED}}]"},
		{"cut_reset", "attempts: [{message: {text: never seen}, cut: {after_events: 3, how: reset}}, {message: {text: RECOVERED}}]"},
		{"invalid_never", "attempts: [{http_error: {error: invalid_request, message: scripted refusal, retry: never}}, {message: {text: RECOVERED}}]"},
		{"limit_per_minute", "attempts: [{limit: {window: per_minute, resets_in_seconds: 2}}, {message: {text: RECOVERED}}]"},
		{"limit_daily", "reply: {limit: {window: daily, resets_in_seconds: 7200}}"},
		{"limit_five_hour", "reply: {limit: {window: five_hour, resets_in_seconds: 7200}}"},
		{"limit_seven_day", "reply: {limit: {window: seven_day, resets_in_seconds: 7200}}"},
	}
	for _, c := range cases {
		if only != "" && !strings.Contains(","+only+",", ","+c.name+",") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(fmt.Sprintf("schema: understudy/v2\nsessions:\n  m:\n    steps:\n      - name: one\n        %s\n", c.step)))
			start := time.Now()
			o := us.Harness(t, f, id, us.HarnessRun{Dir: t.TempDir(), Session: "m", Protocol: proto, Prompt: "say hi [understudy-session: m]", Timeout: 150 * time.Second})
			n := 0
			var at []string
			for _, e := range f.Messages() {
				if e.Session == "m" && e.Aux == "" {
					n++
					at = append(at, fmt.Sprintf("%.1fs", 0.0))
				}
			}
			_ = at
			t.Logf("RESULT %s: requests=%d exit=%d timedout=%v took=%s recovered=%v\nstdout: %s\nstderr: %s", c.name, n, o.Exit, o.TimedOut,
				time.Since(start).Round(time.Second), strings.Contains(o.Stdout, "RECOVERED"), tail(o.Stdout, 1500), tail(o.Stderr, 1500))
		})
	}
}
