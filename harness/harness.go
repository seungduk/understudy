// Package harness is the agent harnesses understudy plays the model for, as
// data: for each harness, the protocols it speaks, how a request shows it is
// that harness, how each neutral action (shell, read, write, edit) becomes its
// own tool call, how its tool results show errors and exit codes, where its
// prompt names the working directory, which side requests it makes (titles,
// routing) and how to point it at understudy. The code here only reads that
// data; adding a harness is adding one profile file.
package harness

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// ID names a harness. The set is closed: only the IDs of the profiles in this
// package exist, and Lookup refuses any other.
type ID string

// Generic is the profile of a client understudy does not know: native tool
// calls only, results read from the wire's own error marks.
const Generic ID = "generic"

// Profile is one harness, as data.
type Profile struct {
	ID   ID
	Name string // its product name, for people
	// Protocols are the wire protocols it can speak; the first is its usual one.
	Protocols []protocol.ID
	// Detect is how a request shows it came from this harness.
	Detect Signature
	// Tools turns each neutral action into this harness's tool call.
	Tools map[core.Action]ToolMap
	// Answers names the neutral action a result of each tool answers.
	Answers map[string]core.Action
	// Results says how each tool's results show errors and exit codes; a tool
	// not listed is an error only when the wire marks it so.
	Results map[string]ResultRule
	// CWD are regular expressions whose first group is the working directory,
	// tried in order on the system prompt and the user and system turns.
	CWD []string
	// Aux are side requests the harness makes outside the conversation
	// (titles, routing), answered without the script.
	Aux []Aux
	// Launch is how to run the harness against understudy.
	Launch Launch
	// Measured is what the real harness did with errors and limits, for the docs.
	Measured []string

	once     sync.Once
	cwdRes   []*regexp.Regexp
	exitRes  map[string]*regexp.Regexp
	errorRes map[string]*regexp.Regexp
}

// Signature is what a request of the harness carries.
type Signature struct {
	// Tools must all be offered.
	Tools []string
	// Without must none be offered.
	Without []string
}

// ToolMap is the tool call of one action. Template is, for a Function tool, a
// JSON object whose string values may hold placeholders, and for a Freeform
// tool the raw text with placeholders. Placeholders: {{command}}, {{path}},
// {{content}}, {{old}}, {{new}}, with an optional filter: |plus and |minus
// prefix every line with + or - (patch lines), |sh quotes for a POSIX shell.
type ToolMap struct {
	Tool     string
	Kind     core.ToolKind
	Template string
}

// ResultRule reads one tool's results.
type ResultRule struct {
	// Exit is a regular expression whose first group is the exit code.
	Exit string
	// Exit0 says a result that is not an error and shows no exit code exited 0.
	Exit0 bool
	// Error is a regular expression that, when it matches, makes the result an error.
	Error string
	// NonZeroIsError makes a result that shows an exit code other than 0 an error.
	NonZeroIsError bool
}

// Aux is one side request: when the system prompt (or, with InFirstUser, the
// first user text) contains Contains, the answer is Reply, as a plain message.
type Aux struct {
	Contains    string
	InFirstUser bool
	Reply       string
	Name        string // for the log, such as "title"
}

// Launch is how to run the harness. Every string may hold {{url}} (the
// server's base URL), {{session}}, {{home}} (an empty home folder made for the
// run), {{dir}} (the working directory), {{prompt}} and {{model}}.
type Launch struct {
	Binary string   // the command's usual name
	EnvVar string   // a variable that names another binary, such as UNDERSTUDY_CODEX
	Args   []string // the arguments of one scripted, non-interactive run
	// Version is the arguments that print the version.
	Version []string
	// Env is KEY=VALUE pairs; the run starts from an empty environment plus
	// PATH, LANG and TMPDIR, so no real credential can leak in.
	Env []string
	// Files are written before the run: path (under {{home}} or {{dir}}) -> content.
	Files map[string]string
	// Model is the model name the run asks for ({{model}}).
	Model string
	// Models are, for a harness that speaks several protocols, the model
	// name ({{model}}) that makes it use each one; Model is the default.
	Models map[protocol.ID]string
	// SessionInPrompt says the harness cannot send the session header, so a
	// run puts the marker [understudy-session: KEY] into the prompt instead.
	SessionInPrompt bool
	// Notes say what a person setting it up by hand must know.
	Notes []string
}

var (
	mu       sync.RWMutex
	profiles = map[ID]*Profile{}
)

// register adds a profile; each profile file calls it from init.
func register(p *Profile) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := profiles[p.ID]; dup {
		panic("harness: duplicate profile " + string(p.ID))
	}
	profiles[p.ID] = p
}

// Lookup finds a profile by name.
func Lookup(name string) (*Profile, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := profiles[ID(name)]
	return p, ok
}

// All is every profile but Generic, sorted by ID.
func All() []*Profile {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Profile, 0, len(profiles))
	for _, p := range profiles {
		if p.ID != Generic {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Names is every profile's ID, sorted (Generic included).
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(profiles))
	for id := range profiles {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

// Detect finds the profile whose signature the request carries, preferring
// the most specific one (the most tools required); Generic when none fits.
func Detect(p protocol.ID, r core.Request) *Profile {
	offered := map[string]bool{}
	for _, t := range r.Tools {
		offered[t.Name] = true
	}
	var best *Profile
	for _, pr := range All() {
		if !speaks(pr, p) || len(pr.Detect.Tools) == 0 {
			continue
		}
		fits := true
		for _, t := range pr.Detect.Tools {
			fits = fits && offered[t]
		}
		for _, t := range pr.Detect.Without {
			fits = fits && !offered[t]
		}
		if fits && (best == nil || len(pr.Detect.Tools)+len(pr.Detect.Without) > len(best.Detect.Tools)+len(best.Detect.Without)) {
			best = pr
		}
	}
	if best == nil {
		best, _ = Lookup(string(Generic))
	}
	return best
}

func speaks(pr *Profile, p protocol.ID) bool {
	for _, x := range pr.Protocols {
		if x == p {
			return true
		}
	}
	return false
}

// Speaks says whether the harness can use protocol p.
func (pr *Profile) Speaks(p protocol.ID) bool { return speaks(pr, p) }

func (pr *Profile) compile() {
	pr.once.Do(func() {
		for _, s := range pr.CWD {
			pr.cwdRes = append(pr.cwdRes, regexp.MustCompile(s))
		}
		pr.exitRes, pr.errorRes = map[string]*regexp.Regexp{}, map[string]*regexp.Regexp{}
		for tool, r := range pr.Results {
			if r.Exit != "" {
				pr.exitRes[tool] = regexp.MustCompile(r.Exit)
			}
			if r.Error != "" {
				pr.errorRes[tool] = regexp.MustCompile(r.Error)
			}
		}
	})
}

// ActionOf is the neutral action a result of tool answers.
func (pr *Profile) ActionOf(tool string) core.Action { return pr.Answers[tool] }

// ActionOfCall is the neutral action of one call: when several actions share
// the tool (Codex reads with exec_command and writes with apply_patch), the
// action whose template the call's input fits; otherwise ActionOf(tool).
func (pr *Profile) ActionOfCall(c core.Part) core.Action {
	shared := 0
	for _, tm := range pr.Tools {
		if tm.Tool == c.Tool {
			shared++
		}
	}
	if shared > 1 {
		for _, a := range []core.Action{core.ReadFile, core.WriteFile, core.EditFile, core.Shell} {
			if tm, ok := pr.Tools[a]; ok && tm.Tool == c.Tool && fits(tm, c) {
				return a
			}
		}
	}
	return pr.ActionOf(c.Tool)
}

// fits says whether a call's input could have come from the template: its
// constant parts are there and its placeholders stand for any text.
func fits(tm ToolMap, c core.Part) bool {
	if tm.Kind == core.Freeform {
		return templateRe(tm.Template).MatchString(c.Text)
	}
	var tv map[string]any
	var cv map[string]any
	if json.Unmarshal([]byte(tm.Template), &tv) != nil || json.Unmarshal(c.Input, &cv) != nil {
		return false
	}
	for k, v := range tv {
		ts, isStr := v.(string)
		if !isStr {
			continue // numbers, booleans and nested values say nothing about the action
		}
		if cs, ok := cv[k].(string); !ok || !templateRe(ts).MatchString(cs) {
			return false
		}
	}
	return true
}

func templateRe(t string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?s)^`)
	last := 0
	for _, m := range placeholderRe.FindAllStringIndex(t, -1) {
		b.WriteString(regexp.QuoteMeta(t[last:m[0]]))
		b.WriteString(`.*`)
		last = m[1]
	}
	b.WriteString(regexp.QuoteMeta(t[last:]))
	b.WriteString(`$`)
	return regexp.MustCompile(b.String())
}

// Read reads one tool result: whether it is an error and the exit code it shows.
func (pr *Profile) Read(tool, text string, mark core.Mark) Reading {
	pr.compile()
	res := Reading{IsError: mark == core.MarkedError}
	rule, known := pr.Results[tool]
	if re := pr.errorRes[tool]; re != nil && re.MatchString(text) {
		res.IsError = true
	}
	if re := pr.exitRes[tool]; re != nil {
		if m := re.FindStringSubmatch(text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 0 && n <= 255 {
				res.Exit, res.HasExit = n, true
			}
		}
	}
	if known && rule.NonZeroIsError && res.HasExit && res.Exit != 0 {
		res.IsError = true
	}
	if known && rule.Exit0 && !res.HasExit && !res.IsError {
		res.HasExit = true
	}
	return res
}

// Reading is how a profile read one tool result.
type Reading struct {
	IsError bool
	Exit    int
	HasExit bool
}

// WorkingDir finds the working directory in the texts (system prompt first).
func (pr *Profile) WorkingDir(texts []string) string {
	pr.compile()
	for _, re := range pr.cwdRes {
		for _, t := range texts {
			if m := re.FindStringSubmatch(t); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
	}
	return ""
}

// AuxReply is the answer to a side request, when r is one. The Generic
// profile tries the side requests of every profile that speaks p, since a side
// request (a title, a routing call) offers no tools to detect its harness by.
func (pr *Profile) AuxReply(p protocol.ID, r core.Request) (name, reply string, ok bool) {
	if pr.ID == Generic {
		for _, x := range All() {
			if x.Speaks(p) {
				if n, rep, ok := x.auxReply(r); ok {
					return n, rep, true
				}
			}
		}
		return "", "", false
	}
	return pr.auxReply(r)
}

func (pr *Profile) auxReply(r core.Request) (name, reply string, ok bool) {
	first := ""
	for _, t := range r.Turns {
		if t.Role == core.User {
			for _, p := range t.Parts {
				if p.Kind == core.Text {
					first = p.Text
					break
				}
			}
			break
		}
	}
	sys := r.System
	for _, t := range r.Turns {
		if t.Role == core.System {
			for _, p := range t.Parts {
				sys += "\n" + p.Text
			}
		}
	}
	for _, a := range pr.Aux {
		hay := sys
		if a.InFirstUser {
			hay = first
		}
		if strings.Contains(hay, a.Contains) {
			return a.Name, a.Reply, true
		}
	}
	return "", "", false
}

// ErrNoAction is returned by Call when the harness has no tool for an action.
type ErrNoAction struct {
	Harness ID
	Action  core.Action
}

func (e ErrNoAction) Error() string {
	return fmt.Sprintf("the harness %s has no tool for the %s action; call its tool natively with {tool, input}", e.Harness, e.Action.Name())
}

// Call is the tool call of an action with its arguments.
func (pr *Profile) Call(a core.Action, args core.ActionArgs) (core.Call, error) {
	tm, ok := pr.Tools[a]
	if !ok {
		return core.Call{}, ErrNoAction{Harness: pr.ID, Action: a}
	}
	c := core.Call{Tool: tm.Tool, Kind: tm.Kind}
	if tm.Kind == core.Freeform {
		c.Raw = fill(tm.Template, args)
		return c, nil
	}
	var v any
	if err := json.Unmarshal([]byte(tm.Template), &v); err != nil {
		panic("harness: the template of " + string(pr.ID) + " " + a.Name() + " is not JSON: " + err.Error())
	}
	b, err := json.Marshal(fillJSON(v, args))
	if err != nil {
		return core.Call{}, err
	}
	c.Input = b
	return c, nil
}

var placeholderRe = regexp.MustCompile(`\{\{(command|path|content|old|new)(?:\|(plus|minus|sh))?\}\}`)

func fill(t string, a core.ActionArgs) string {
	return placeholderRe.ReplaceAllStringFunc(t, func(m string) string {
		g := placeholderRe.FindStringSubmatch(m)
		var v string
		switch g[1] {
		case "command":
			v = a.Command
		case "path":
			v = a.Path
		case "content":
			v = a.Content
		case "old":
			v = a.Old
		case "new":
			v = a.New
		}
		switch g[2] {
		case "plus":
			return prefixLines(v, "+")
		case "minus":
			return prefixLines(v, "-")
		case "sh":
			return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
		}
		return v
	})
}

// prefixLines puts p before every line of v; a final newline ends the last
// line and adds no empty line; an empty v is one empty line.
func prefixLines(v, p string) string {
	v = strings.TrimSuffix(v, "\n")
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		lines[i] = p + l
	}
	return strings.Join(lines, "\n")
}

func fillJSON(v any, a core.ActionArgs) any {
	switch x := v.(type) {
	case string:
		return fill(x, a)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fillJSON(e, a)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fillJSON(e, a)
		}
		return out
	}
	return v
}
