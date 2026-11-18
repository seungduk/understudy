package harness

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/seungduk/understudy/protocol"
)

// Target is one run to set up: where the server is and what the run is.
type Target struct {
	URL      string      // the server's base URL, such as http://127.0.0.1:8765
	Session  string      // the session key; "" sends "default"
	Home     string      // an empty folder that becomes the run's home
	Dir      string      // the working directory
	Prompt   string      // the task
	Protocol protocol.ID // the protocol to use; 0 is the harness's usual one
	Model    string      // the model name to ask for; "" is the profile's for Protocol
}

// Setup is a run, ready to start: the binary, its arguments and environment,
// and the files to write first.
type Setup struct {
	Binary string
	Args   []string
	Env    []string // complete: PATH, LANG and TMPDIR from this process, then the profile's
	Files  map[string]string
}

// Binary is the harness command: the profile's EnvVar when set, else its
// usual name on PATH.
func (pr *Profile) Binary() (string, error) {
	if pr.Launch.EnvVar != "" {
		if p := os.Getenv(pr.Launch.EnvVar); p != "" {
			return p, nil
		}
	}
	if pr.Launch.Binary == "" {
		return "", fmt.Errorf("the %s profile has no command", pr.ID)
	}
	p, err := exec.LookPath(pr.Launch.Binary)
	if err != nil {
		return "", fmt.Errorf("%s is not installed (not on PATH and %s is unset)", pr.Launch.Binary, pr.Launch.EnvVar)
	}
	return p, nil
}

// Model is the model name a run asks for, to use protocol p.
func (pr *Profile) Model(p protocol.ID) string {
	if m, ok := pr.Launch.Models[p]; ok {
		return m
	}
	return pr.Launch.Model
}

// Prepare expands the profile's launch data for one run.
func (pr *Profile) Prepare(t Target) (Setup, error) {
	bin, err := pr.Binary()
	if err != nil {
		return Setup{}, err
	}
	p := t.Protocol
	if p == 0 {
		p = pr.Protocols[0]
	}
	if !pr.Speaks(p) {
		return Setup{}, fmt.Errorf("%s does not speak %s; it speaks %s", pr.ID, p.Name(), protocolNames(pr.Protocols))
	}
	session := t.Session
	if session == "" {
		session = "default"
	}
	prompt := t.Prompt
	if pr.Launch.SessionInPrompt && !strings.Contains(prompt, "[understudy-session:") {
		prompt += " [understudy-session: " + session + "]"
	}
	model := pr.Model(p)
	if t.Model != "" {
		model = t.Model
	}
	r := strings.NewReplacer("{{url}}", strings.TrimSuffix(t.URL, "/"), "{{session}}", session, "{{home}}", t.Home,
		"{{dir}}", t.Dir, "{{prompt}}", prompt, "{{model}}", model)
	s := Setup{Binary: bin, Files: map[string]string{}}
	for _, a := range pr.Launch.Args {
		s.Args = append(s.Args, r.Replace(a))
	}
	for _, k := range []string{"PATH", "LANG", "TERM"} {
		if v := os.Getenv(k); v != "" {
			s.Env = append(s.Env, k+"="+v)
		}
	}
	s.Env = append(s.Env, "TMPDIR="+t.Home+"/tmp", "NO_COLOR=1")
	for _, e := range pr.Launch.Env {
		s.Env = append(s.Env, r.Replace(e))
	}
	for path, content := range pr.Launch.Files {
		s.Files[r.Replace(path)] = r.Replace(content)
	}
	s.Files[t.Home+"/tmp/.keep"] = ""
	return s, nil
}

func protocolNames(ps []protocol.ID) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
