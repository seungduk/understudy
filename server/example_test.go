package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/seungduk/understudy/server"
)

// A two-step script: the fake model asks the agent to run the tests, then
// answers depending on how they went.
const testsScript = `
schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: test
        reply:
          tool_use:
            calls: [{tool: Bash, input: {command: "go test ./..."}}]
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "DONE: tests pass"}}
        otherwise:
          reply: {message: {text: "blocked: the tests fail"}}
`

// claudeTools are the tools Claude Code offers that tell the server which
// agent is calling (see the harness package).
const claudeTools = `[{"name":"Bash","input_schema":{"type":"object"}},{"name":"Read","input_schema":{"type":"object"}},{"name":"Edit","input_schema":{"type":"object"}}]`

// post sends one Anthropic Messages request for session t1/writer and gives
// the HTTP status and the reply's content block types.
func post(url, body string) (int, []string) {
	req, _ := http.NewRequest("POST", url+"/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Understudy-Session", "t1/writer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var reply struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	}
	_ = json.Unmarshal(data, &reply)
	var parts []string
	for _, c := range reply.Content {
		switch c.Type {
		case "text":
			parts = append(parts, "text: "+c.Text)
		case "tool_use":
			parts = append(parts, "tool_use: "+c.Name)
		}
	}
	return resp.StatusCode, parts
}

// Run the fake model in-process, as a test would: load a script, send the
// requests an agent would send, and assert on the request log.
func Example() {
	sc, err := server.Parse([]byte(testsScript))
	if err != nil {
		fmt.Println(err) // each problem says where it is and how to fix it
		return
	}
	s := server.New(server.Options{Script: sc})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Turn 0: the agent sends the task; the script asks it to run the tests.
	fmt.Println(post(srv.URL, `{"model":"claude-opus-5-5","max_tokens":100,"tools":`+claudeTools+`,
		"messages":[{"role":"user","content":"fix the bug"}]}`))

	// Turn 1: the agent sends the command's result; it passed, so the script says DONE.
	fmt.Println(post(srv.URL, `{"model":"claude-opus-5-5","max_tokens":100,"tools":`+claudeTools+`,
		"messages":[{"role":"user","content":"fix the bug"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./..."}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}]}`))

	// The log says which step answered each request, and how it ended.
	for _, e := range s.SessionRequests("t1/writer") {
		fmt.Printf("turn %d: step %s, %s\n", e.Turn, e.Step, e.Outcome)
	}
	// Output:
	// 200 [tool_use: Bash]
	// 200 [text: DONE: tests pass]
	// turn 0: step test, served
	// turn 1: step judge, served
}

// A request the script did not expect is a divergence: by default the server
// refuses it and the log shows what the script wanted instead.
func Example_divergence() {
	s := server.New(server.Options{Script: must(server.Parse([]byte(testsScript)))})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// The agent offers no Bash tool, so the first step cannot be played.
	status, _ := post(srv.URL, `{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"fix the bug"}]}`)
	e := s.SessionRequests("t1/writer")[0]
	fmt.Println(status, e.Outcome)
	fmt.Println(len(e.Expected) > 0)
	// Output:
	// 400 diverged
	// true
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
