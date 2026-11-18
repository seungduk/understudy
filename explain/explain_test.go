package explain

import (
	"bytes"
	"strings"
	"testing"

	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

const sc = `schema: understudy/v1
sessions:
  s:
    steps:
      - name: test
        reply: {tool_use: {calls: [{tool: Bash, input: {command: t}}]}}
      - name: judge
        cases:
          - when: {exit_code: 0}
            reply: {message: {text: ok}}
`

func TestEntries(t *testing.T) {
	s, err := script.Parse([]byte(sc))
	if err != nil {
		t.Fatal(err)
	}
	entries := []server.Entry{
		{Seq: 1, Path: "/api/hello", Outcome: server.Other},
		{Seq: 2, Path: "/v1/messages", Session: "s", SessionSource: "header", Turn: 1, Step: "judge", Branch: "none", Outcome: server.Diverged,
			Divergence: "no case", Error: "understudy: the request does not fit the script: no case", Trace: []string{"case 0: no"},
			History: [][]reqlog.Result{nil, {{Tool: "Bash", IsError: true, Text: "Exit code 1"}}}},
		{Seq: 3, Path: "/v1/messages", Session: "zz", Outcome: server.NoScript, Error: "no session"},
		{Seq: 4, Path: "/v1/messages", Session: "s", Outcome: server.Served, Step: "test", Branch: "reply", Attempt: 1, Reply: "tool_use"},
	}
	xs := Entries(entries, s)
	if len(xs) != 3 || xs[0].Reevaluated.Divergence == "" || xs[1].Reevaluated.Divergence != `the script has no session "zz"` || xs[2].Reevaluated.Step != "test" {
		t.Fatalf("%+v", xs)
	}
	var b bytes.Buffer
	Write(&b, xs)
	out := b.String()
	for _, want := range []string{"#2 /v1/messages session=\"s\" (header) turn=1", "DIVERGED: no case", "with the given script", "DIVERGES", "sent: no session", "outcome=served"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "sent: understudy: the request") {
		t.Error("repeated divergence")
	}
	if got := Summary(xs); got != "3 request(s): served=1 diverged=1 no_script=1" {
		t.Error(got)
	}
	b.Reset()
	Write(&b, nil)
	if !strings.Contains(b.String(), "no /v1/messages") {
		t.Error(b.String())
	}
	if xs := Entries(entries, nil); xs[0].Reevaluated != nil {
		t.Error("reevaluated without script")
	}
}
