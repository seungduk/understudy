package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/protocol"
	"github.com/seungduk/understudy/script"
)

func protocolNames(ps []protocol.ID) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

// cmdHarnesses lists the harness profiles: protocols, tools per action, launch.
func cmdHarnesses(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("harnesses", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the profiles as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	type tool struct {
		Action, Tool, Kind, Template string
	}
	type row struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Protocols []string `json:"protocols"`
		Binary    string   `json:"binary"`
		EnvVar    string   `json:"binary_env"`
		Tools     []tool   `json:"tools"`
		Session   string   `json:"session"`
		Notes     []string `json:"notes,omitempty"`
	}
	var rows []row
	for _, pr := range harness.All() {
		r := row{ID: string(pr.ID), Name: pr.Name, Protocols: protocolNames(pr.Protocols), Binary: pr.Launch.Binary, EnvVar: pr.Launch.EnvVar,
			Session: "header X-Understudy-Session", Notes: pr.Launch.Notes}
		if pr.Launch.SessionInPrompt {
			r.Session = "marker [understudy-session: KEY] in the prompt"
		}
		for _, a := range core.Actions {
			if tm, ok := pr.Tools[a]; ok {
				kind := "function"
				if tm.Kind == core.Freeform {
					kind = "freeform"
				}
				r.Tools = append(r.Tools, tool{Action: a.Name(), Tool: tm.Tool, Kind: kind, Template: tm.Template})
			}
		}
		rows = append(rows, r)
	}
	if *asJSON {
		writeJSONOut(stdout, map[string]any{"harnesses": rows})
		return 0
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "%s  %s  (%s; binary %s or $%s; session by %s)\n", r.ID, r.Name, strings.Join(r.Protocols, ", "), r.Binary, r.EnvVar, r.Session)
		for _, t := range r.Tools {
			fmt.Fprintf(stdout, "    %-6s -> %s\n", t.Action, t.Tool)
		}
	}
	return 0
}

// cmdSetup writes a harness's config files into a home folder and prints the
// environment and the command that run it against the server.
func cmdSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("harness", "", "the harness: "+strings.Join(harnessIDs(), ", "))
	url := fs.String("url", "http://127.0.0.1:8765", "the server's base URL")
	session := fs.String("session", "", "the session key")
	home := fs.String("home", "", "an empty folder to be the harness's home (config and sessions go there)")
	dir := fs.String("dir", ".", "the working directory of the run")
	proto := fs.String("protocol", "", "the protocol, for a harness that speaks several")
	prompt := fs.String("prompt", "do the task", "the prompt")
	asJSON := fs.Bool("json", false, "print {env, files, command} as JSON instead of shell lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pr, ok := harness.Lookup(*name)
	if !ok || pr.ID == harness.Generic {
		fmt.Fprintf(stderr, "understudy setup: unknown --harness %q. Fix: use one of %s\n", *name, strings.Join(harnessIDs(), ", "))
		return 2
	}
	if *home == "" {
		fmt.Fprintln(stderr, "understudy setup: --home is required. Fix: give an empty folder, such as --home /tmp/codex-home, so the harness never reads your own config")
		return 2
	}
	if *session != "" && !script.ValidSessionKey(*session) {
		fmt.Fprintf(stderr, "understudy setup: %q is not a session key. Fix: use letters, digits and . _ : / @ + -\n", *session)
		return 2
	}
	var p protocol.ID
	if *proto != "" {
		if p, ok = protocol.Parse(*proto); !ok || !pr.Speaks(p) {
			fmt.Fprintf(stderr, "understudy setup: %s does not speak %q. Fix: use one of %s\n", pr.ID, *proto, strings.Join(protocolNames(pr.Protocols), ", "))
			return 2
		}
	}
	h, _ := filepath.Abs(*home)
	d, _ := filepath.Abs(*dir)
	s, err := pr.Prepare(harness.Target{URL: *url, Session: *session, Home: h, Dir: d, Prompt: *prompt, Protocol: p})
	if err != nil && strings.Contains(err.Error(), "not installed") {
		// Setting up does not need the binary; name it as the profile does.
		os.Setenv(pr.Launch.EnvVar, pr.Launch.Binary)
		s, err = pr.Prepare(harness.Target{URL: *url, Session: *session, Home: h, Dir: d, Prompt: *prompt, Protocol: p})
	}
	if err != nil {
		fmt.Fprintf(stderr, "understudy setup: %v\n", err)
		return 1
	}
	paths := make([]string, 0, len(s.Files))
	for path, content := range s.Files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(stderr, "understudy setup: %v\n", err)
			return 1
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintf(stderr, "understudy setup: %v\n", err)
			return 1
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if *asJSON {
		writeJSONOut(stdout, map[string]any{"harness": pr.ID, "env": s.Env, "files": paths, "command": append([]string{s.Binary}, s.Args...)})
		return 0
	}
	fmt.Fprintf(stdout, "# %s against %s; files written: %s\n# run from %s with an empty stdin, in a clean environment:\n", pr.Name, *url, strings.Join(paths, ", "), d)
	var env []string
	for _, kv := range s.Env {
		k, v, _ := strings.Cut(kv, "=")
		env = append(env, k+"="+shq(v))
	}
	cmd := []string{shq(s.Binary)}
	for _, a := range s.Args {
		cmd = append(cmd, shq(a))
	}
	fmt.Fprintf(stdout, "env -i %s %s </dev/null\n", strings.Join(env, " "), strings.Join(cmd, " "))
	return 0
}

func shq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@,+") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func harnessIDs() []string {
	var out []string
	for _, pr := range harness.All() {
		out = append(out, string(pr.ID))
	}
	return out
}
