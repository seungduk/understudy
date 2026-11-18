package explain_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/seungduk/understudy/explain"
	"github.com/seungduk/understudy/server"
)

// Entries says, for each logged request, which step answered it and why; Write
// prints that for people and agents.
func ExampleEntries() {
	sc, _ := server.Parse([]byte(`
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply: {message: {text: "Hello from the understudy."}}
`))
	s := server.New(server.Options{Script: sc})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"say hi"}]}`))
	if err != nil {
		panic(err)
	}
	resp.Body.Close()

	explain.Write(os.Stdout, explain.Entries(s.Requests(), sc))
	// Output:
	// #1 /v1/messages session="default" (default) turn=0 step="answer" branch="reply" attempt=1 reply=message outcome=served
	//     with the given script: step="answer" branch="reply"
}
