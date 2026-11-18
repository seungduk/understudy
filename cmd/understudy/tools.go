package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seungduk/understudy/clienv"
	"github.com/seungduk/understudy/explain"
	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

func writeJSONOut(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// validate: exit 0 when the script checks, 1 with problems, 2 for usage errors.
func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print a JSON report: {ok, file, sessions, problems:[{path, line, rule, message, fix}]}")
	file, rest := splitFile(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if file == "" && fs.NArg() == 1 {
		file = fs.Arg(0)
	}
	if file == "" {
		fmt.Fprintln(stderr, "understudy validate: give the script file, such as: understudy validate fix.yaml")
		return 2
	}
	sc, err := script.Load(file)
	var ps script.Problems
	if err != nil && !errors.As(err, &ps) {
		ps = script.Problems{{Rule: script.RuleSyntax, Message: err.Error(), Fix: "give a script file"}}
	}
	if *asJSON {
		rep := map[string]any{"ok": err == nil, "file": file, "problems": ps}
		if ps == nil {
			rep["problems"] = []script.Problem{}
		}
		if sc != nil {
			rep["sessions"] = sc.Keys()
		}
		writeJSONOut(stdout, rep)
	} else if err == nil {
		fmt.Fprintf(stdout, "%s: ok (%d session(s): %s)\n", file, len(sc.Keys()), strings.Join(sc.Keys(), ", "))
	} else {
		fmt.Fprintf(stdout, "%s: %d problem(s)\n", file, len(ps))
		for _, p := range ps {
			fmt.Fprintf(stdout, "- %s\n", p)
		}
	}
	if err != nil {
		return 1
	}
	return 0
}

// splitFile takes a leading non-flag argument as the file, so flags may follow it.
func splitFile(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func cmdExplain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	logFile := fs.String("log", "", "a saved request log (the JSON of GET /_understudy/requests)")
	url := fs.String("url", "", "a running server's base URL, such as http://127.0.0.1:8765")
	scriptFile := fs.String("script", "", "also choose each request again with this script")
	session := fs.String("session", "", "only this session")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*logFile == "") == (*url == "") {
		fmt.Fprintln(stderr, "understudy explain: give exactly one of --log FILE or --url URL")
		return 2
	}
	var data []byte
	var err error
	if *logFile != "" {
		data, err = os.ReadFile(*logFile)
	} else {
		var resp *http.Response
		resp, err = http.Get(strings.TrimSuffix(*url, "/") + server.ControlPrefix + "requests")
		if err == nil {
			data, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "understudy explain: cannot read the log: %v\n", err)
		return 1
	}
	var log struct {
		Requests []server.Entry `json:"requests"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		fmt.Fprintf(stderr, "understudy explain: the log is not the JSON of GET %srequests: %v\n", server.ControlPrefix, err)
		return 1
	}
	var sc *script.Script
	if *scriptFile != "" {
		if sc, err = script.Load(*scriptFile); err != nil {
			fmt.Fprintf(stderr, "understudy explain: %s: %v\n", *scriptFile, err)
			return 1
		}
	}
	entries := log.Requests
	if *session != "" {
		var keep []server.Entry
		for _, e := range entries {
			if e.Session == *session {
				keep = append(keep, e)
			}
		}
		entries = keep
	}
	xs := explain.Entries(entries, sc)
	if *asJSON {
		writeJSONOut(stdout, map[string]any{"summary": explain.Summary(xs), "requests": xs})
		return 0
	}
	explain.Write(stdout, xs)
	fmt.Fprintln(stdout, explain.Summary(xs))
	return 0
}

func cmdRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	transcript := fs.String("transcript", "", "a stream-json transcript or a Claude Code session file ('-' for stdin)")
	session := fs.String("session", "default", "the session key the script answers under")
	run := fs.String("run", "", "provenance: the run id")
	task := fs.String("task", "", "provenance: the task id")
	seat := fs.String("seat", "", "provenance: the seat (planner, writer, reviewer, ...)")
	cwd := fs.String("cwd", "", "the recorded working directory (default: from the transcript)")
	at := fs.String("recorded-at", "", "when the session ran, RFC 3339 (default: unknown)")
	div := fs.String("divergence", "strict", "replay policy written into the script: strict or lenient")
	out := fs.String("out", "", "write the script here (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *transcript == "" {
		fmt.Fprintln(stderr, "understudy record: give --transcript FILE (the stream-json output of claude -p --output-format stream-json --verbose)")
		return 2
	}
	m := record.Meta{SessionKey: *session, Run: *run, Task: *task, Seat: *seat, CWD: *cwd}
	switch script.Divergence(*div) {
	case script.Strict, script.Lenient:
		m.Divergence = script.Divergence(*div)
	default:
		fmt.Fprintf(stderr, "understudy record: unknown --divergence %q. Fix: use strict or lenient\n", *div)
		return 2
	}
	if *at != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fmt.Fprintf(stderr, "understudy record: --recorded-at %q is not RFC 3339. Fix: write it like 2026-11-18T09:30:00Z\n", *at)
			return 2
		}
		m.RecordedAt = t
	}
	var r io.Reader = os.Stdin
	if *transcript != "-" {
		f, err := os.Open(*transcript)
		if err != nil {
			fmt.Fprintf(stderr, "understudy record: %v\n", err)
			return 1
		}
		defer f.Close()
		r = f
	}
	file, rep, err := record.FromTranscript(r, m)
	if err != nil {
		fmt.Fprintf(stderr, "understudy record: %v\n", err)
		return 1
	}
	b, err := script.Marshal(file)
	if err != nil {
		fmt.Fprintf(stderr, "understudy record: %v\n", err)
		return 1
	}
	if *out == "" {
		_, _ = stdout.Write(b)
	} else if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintf(stderr, "understudy record: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "understudy record: %d step(s), %d retried attempt(s), Claude Code %s\n", rep.Steps, rep.Retries, rep.CLIVersion)
	for _, n := range rep.Notes {
		fmt.Fprintf(stderr, "  note: %s\n", n)
	}
	return 0
}

func cmdEnv(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8765", "the server's base URL")
	session := fs.String("session", "", "session key to send as the X-Understudy-Session header")
	auth := fs.String("auth", "api-key", "api-key, or gateway to get subscription-style limit messages")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var a clienv.Auth
	switch *auth {
	case "api-key":
		a = clienv.APIKey
	case "gateway":
		a = clienv.Gateway
	default:
		fmt.Fprintf(stderr, "understudy env: unknown --auth %q. Fix: use api-key or gateway\n", *auth)
		return 2
	}
	if *session != "" && !script.ValidSessionKey(*session) {
		fmt.Fprintf(stderr, "understudy env: %q is not a session key. Fix: use letters, digits and . _ : / @ + -\n", *session)
		return 2
	}
	fmt.Fprintln(stdout, "# eval \"$(understudy env ...)\"; dummy credentials only")
	for _, k := range clienv.Cleared {
		fmt.Fprintf(stdout, "unset %s\n", k)
	}
	for _, kv := range clienv.Env(*url, *session, a) {
		k, v, _ := strings.Cut(kv, "=")
		fmt.Fprintf(stdout, "export %s='%s'\n", k, strings.ReplaceAll(v, "'", `'\''`))
	}
	return 0
}
