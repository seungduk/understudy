package conv

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/seungduk/understudy/internal/wire"
	"github.com/seungduk/understudy/script"
)

func req(t *testing.T, body string) wire.Request {
	t.Helper()
	r, err := wire.ParseRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKey(t *testing.T) {
	plain := req(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	sys := req(t, `{"model":"m","system":"x [understudy-session: t1/planner] y","messages":[{"role":"user","content":"[understudy-session: other]"}]}`)
	user := req(t, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"do it [understudy-session:t2/writer]"}]}]}`)
	later := req(t, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"[understudy-session: late]"}]}`)
	bad := req(t, `{"model":"m","system":"[understudy-session: ]","messages":[{"role":"user","content":"a"}]}`)
	hdr := http.Header{}
	hdr.Set(SessionHeader, " t9/reviewer ")
	badHdr := http.Header{}
	badHdr.Set(SessionHeader, "has space")
	cases := []struct {
		h      http.Header
		r      wire.Request
		key    string
		src    KeySource
		badKey string
	}{
		{http.Header{}, plain, "default", FromDefault, ""},
		{hdr, sys, "t9/reviewer", FromHeader, ""},
		{http.Header{}, sys, "t1/planner", FromSystemPrompt, ""},
		{http.Header{}, user, "t2/writer", FromUserPrompt, ""},
		{http.Header{}, later, "default", FromDefault, ""},
		{http.Header{}, bad, "", FromSystemPrompt, ""},
		{badHdr, plain, "", FromHeader, "has space"},
	}
	for i, c := range cases {
		v := Read(c.h, c.r)
		if v.Key != c.key || v.KeySource != c.src || v.BadKey != c.badKey {
			t.Errorf("%d: %+v", i, v)
		}
	}
	for s, want := range map[KeySource]string{FromHeader: "header", FromSystemPrompt: "system_prompt", FromUserPrompt: "user_prompt", FromDefault: "default", 0: "unknown"} {
		if s.String() != want {
			t.Errorf("%v", s)
		}
	}
}

func TestHistoryAndCWD(t *testing.T) {
	r := req(t, `{"model":"m","messages":[
	{"role":"user","content":"go"},
	{"role":"system","content":[{"type":"text","text":"# Environment\n - Primary working directory: /tmp/w x\n - Platform: darwin"}]},
	{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}},{"type":"tool_use","id":"t2","name":"Bash","input":{}}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file"},{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"Exit code 1"}]},
	{"role":"assistant","content":[{"type":"text","text":"hm"}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"zz","content":"orphan"}]}]}`)
	v := Read(http.Header{}, r)
	want := [][]script.Result{nil, {{Tool: "Read", Text: "file"}, {Tool: "Bash", IsError: true, Text: "Exit code 1"}}, {{Tool: "", Text: "orphan"}}}
	if !reflect.DeepEqual(v.History, want) {
		t.Errorf("history %+v", v.History)
	}
	if v.CWD != "/tmp/w x" {
		t.Errorf("cwd %q", v.CWD)
	}
	if v := Read(http.Header{}, req(t, `{"model":"m","system":"Working directory: /a","messages":[{"role":"user","content":"x"}]}`)); v.CWD != "/a" {
		t.Errorf("system cwd %q", v.CWD)
	}
	if v := Read(http.Header{}, req(t, `{"model":"m","messages":[{"role":"user","content":"x"}]}`)); v.CWD != "" || len(v.History) != 1 {
		t.Errorf("none %+v", v)
	}
}
