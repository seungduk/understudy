package control_test

import (
	"context"
	"fmt"
	"net/http/httptest"

	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/server"
)

// A Client drives a running server (here one in-process; normally the one
// `understudy serve` started) over its control API: load a script, read the
// sessions and the request log, release gates.
func Example() {
	srv := httptest.NewServer(server.New(server.Options{}).Handler())
	defer srv.Close()

	c, err := control.New(srv.URL, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.Background()

	h, _ := c.Health(ctx)
	fmt.Println("ok:", h.OK, "script loaded:", h.ScriptLoaded)

	sessions, err := c.Load(ctx, []byte(`
schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: done
        reply: {message: {text: "DONE"}}
`))
	fmt.Println("loaded:", sessions, err)

	for _, s := range must(c.Sessions(ctx)) {
		fmt.Println(s.Key, s.State, s.Requests, "request(s)")
	}
	// Output:
	// ok: true script loaded: false
	// loaded: [t1/writer] <nil>
	// t1/writer idle 0 request(s)
}

// A script with problems is refused; the error lists each problem and its fix.
func ExampleClient_Load() {
	srv := httptest.NewServer(server.New(server.Options{}).Handler())
	defer srv.Close()
	c, _ := control.New(srv.URL, nil)

	_, err := c.Load(context.Background(), []byte("schema: understudy/v9\nsessions: {}\n"))
	fmt.Println(err)
	// Output:
	// the script was not loaded: the script has 2 problem(s):
	// - line 1: schema: the schema is "understudy/v9", this version reads "understudy/v1" and "understudy/v2" (schema). Fix: put 'schema: understudy/v2' as the first line
	// - line 2: sessions: the script has no sessions (required). Fix: add 'sessions:' with at least one session, such as 'default:' with 'steps:'. Fix: fix the problems listed and send the script again
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
