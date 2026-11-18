package record

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/script"
)

func TestFromRealTranscript(t *testing.T) {
	f, err := os.Open("testdata/toolloop-2.1.325.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	file, rep, err := FromTranscript(f, Meta{SessionKey: "t1/writer", Run: "r1", Task: "t1", Seat: "writer", RecordedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Steps != 5 || rep.CLIVersion != "2.1.325" || rep.Session == "" {
		t.Fatalf("%+v", rep)
	}
	p := file.Provenance
	if p.Source != script.Recorded || p.Run != "r1" || p.Task != "t1" || p.Seat != "writer" || p.RecordedAt != "2026-11-18T09:00:00Z" || p.CLIVersion != "2.1.325" || p.Session != rep.Session {
		t.Fatalf("%+v", p)
	}
	steps := file.Sessions["t1/writer"].Steps
	read := steps[0].Reply.ToolUse
	if read.Thinking != "I should read the file first." || read.Text != "Reading a.txt." || read.Calls[0].Tool != "Read" || read.Calls[0].Input["file_path"] != "{{cwd}}/a.txt" || steps[0].Expect != nil {
		t.Fatalf("%+v", read)
	}
	if e := steps[1].Expect; e == nil || e.Result != script.OK {
		t.Fatalf("expect %+v", e)
	}
	if steps[1].Reply.ToolUse.Calls[0].Input["old_string"] != "old" {
		t.Fatal(steps[1].Reply.ToolUse.Calls[0].Input)
	}
	// The failing Bash call: the next step expects the same error and exit code.
	if e := steps[3].Expect; e == nil || e.Result != script.Error || e.ExitCode == nil || *e.ExitCode != 3 {
		t.Fatalf("expect %+v", e)
	}
	if steps[4].Reply.Message.Text != "All done." {
		t.Fatal(steps[4].Reply.Message)
	}
	// It is an ordinary script: it marshals and parses back.
	b, err := script.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.Parse(b); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.HasPrefix(string(b), "# understudy script") || !strings.Contains(string(b), "file_path: '{{cwd}}/a.txt'") {
		t.Fatalf("%s", b)
	}
}

func TestFromLimitTranscript(t *testing.T) {
	f, _ := os.Open("testdata/limit-2.1.325.jsonl")
	defer f.Close()
	file, _, err := FromTranscript(f, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	st := file.Sessions["default"].Steps
	if len(st) != 1 || st[0].Reply.Limit == nil || st[0].Reply.Limit.Window != script.SevenDay || file.Provenance.RecordedAt != "" {
		t.Fatalf("%+v", st)
	}
}

func TestSyntheticTranscript(t *testing.T) {
	src := `{"type":"system","subtype":"init","cwd":"/w","claude_code_version":"9.9.9","session_id":"s1"}
{"type":"system","subtype":"api_retry","error_status":529}
{"type":"system","subtype":"api_retry","error_status":0}
{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"a"}]}}
{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls /w/x","args":["/w/y",1]}},{"type":"tool_use","name":"Read"}]}}
{"type":"assistant","parent_tool_use_id":"toolu_9","message":{"id":"sub","content":[{"type":"text","text":"sub"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"denied"}]},{"type":"tool_result","content":"ok"}]}}

{"type":"assistant","message":{"id":"m2","content":[{"type":"thinking","thinking":"t"}]}}
{"type":"user","message":"plain prompt"}
{"type":"assistant","error":"overloaded","message":{"id":"m3","content":[{"type":"text","text":"API Error: 500"}]}}
{"type":"assistant","error":"rate_limit","message":{"id":"m4","content":[{"type":"text","text":"You've hit your 5-hour limit"}]}}
{"type":"result","subtype":"success","is_error":true}
`
	file, rep, err := FromTranscript(strings.NewReader(src), Meta{Divergence: script.Lenient})
	if err != nil {
		t.Fatal(err)
	}
	st := file.Sessions["default"].Steps
	if len(st) != 4 || rep.Retries != 1 || len(rep.Notes) != 3 || file.Divergence != script.Lenient {
		t.Fatalf("%d %+v", len(st), rep)
	}
	if len(st[0].Attempts) != 2 || st[0].Attempts[0].HTTPError.Error != "overloaded" {
		t.Fatalf("%+v", st[0])
	}
	tu := st[0].Attempts[1].ToolUse
	if tu.Text != "a" || tu.Calls[0].Input["command"] != "ls {{cwd}}/x" || tu.Calls[0].Input["args"].([]any)[0] != "{{cwd}}/y" || len(tu.Calls[1].Input) != 0 {
		t.Fatalf("%+v", tu)
	}
	if e := st[1].Expect; e.Result != script.Error || e.ExitCode != nil {
		t.Fatalf("%+v", e)
	}
	if st[1].Reply.Message.Text != "(no text)" || st[1].Reply.Message.Thinking != "t" {
		t.Fatalf("%+v", st[1].Reply.Message)
	}
	if st[2].Reply.HTTPError.Error != "api_error" || st[3].Reply.Limit.Window != script.FiveHour {
		t.Fatalf("%+v %+v", st[2].Reply, st[3].Reply)
	}
	for kind, want := range map[string]string{"rate_limit": "rate_limit", "authentication_failed": "authentication", "billing_error": "billing", "invalid_request": "invalid_request", "server_error": "api_error", "unknown": "api_error"} {
		if got := errorReply(kind, "x").HTTPError.Error; got != want {
			t.Errorf("%s: %s", kind, got)
		}
	}
	for _, text := range []string{"You've hit your weekly limit", "You've hit your Opus limit", "You've hit your Sonnet limit", "You've hit your session limit"} {
		if errorReply("rate_limit", text).Limit == nil {
			t.Error(text)
		}
	}
	if contentText(5) != "" {
		t.Error("content")
	}
}

func TestTranscriptErrors(t *testing.T) {
	for _, src := range []string{"not json\n", `{"type":"system","subtype":"init"}` + "\n", `{"type":"assistant","message":"x"}` + "\n",
		`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","name":"bad name","input":{}}]}}` + "\n"} {
		if _, _, err := FromTranscript(strings.NewReader(src), Meta{}); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
	if _, _, err := FromTranscript(errReader{}, Meta{}); err == nil {
		t.Error("read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

// A Claude Code session file: every record names its cwd, version and session.
func TestFromSessionFile(t *testing.T) {
	src := `{"type":"user","sessionId":"s9","cwd":"/w","version":"2.1.326","message":{"role":"user","content":"go"}}
{"type":"assistant","sessionId":"s9","cwd":"/w","version":"2.1.326","message":{"id":"m1","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/a"}}]}}
{"type":"assistant","isSidechain":true,"sessionId":"s9","message":{"id":"sub","content":[{"type":"text","text":"x"}]}}
{"type":"user","sessionId":"s9","cwd":"/w","message":{"role":"user","content":[{"type":"tool_result","content":"1\ta"}]}}
{"type":"assistant","sessionId":"s9","cwd":"/w","message":{"id":"m2","content":[{"type":"text","text":"done"}]}}
`
	file, rep, err := FromTranscript(strings.NewReader(src), Meta{})
	if err != nil {
		t.Fatal(err)
	}
	st := file.Sessions["default"].Steps
	if rep.Session != "s9" || rep.CLIVersion != "2.1.326" || len(st) != 2 || st[0].Reply.ToolUse.Calls[0].Input["file_path"] != "{{cwd}}/a" || len(rep.Notes) != 1 {
		t.Fatalf("%+v %+v", rep, st)
	}
}
