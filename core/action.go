package core

// Action is the closed set of protocol- and harness-neutral tool actions a
// script can ask for. A harness profile turns each into its own tool call.
type Action int

const (
	NoAction  Action = iota
	Shell            // run a shell command
	ReadFile         // read a file
	WriteFile        // create or overwrite a file
	EditFile         // replace text in a file
)

// Actions lists every action, in a fixed order.
var Actions = []Action{Shell, ReadFile, WriteFile, EditFile}

var actionNames = map[Action]string{Shell: "shell", ReadFile: "read", WriteFile: "write", EditFile: "edit"}

// Name is the action's name in scripts; "" for NoAction.
func (a Action) Name() string { return actionNames[a] }

// ParseAction turns a script name into an action.
func ParseAction(name string) (Action, bool) {
	for _, a := range Actions {
		if a.Name() == name {
			return a, true
		}
	}
	return NoAction, false
}

// ActionNames lists the script names of every action, in order.
func ActionNames() []string {
	out := make([]string, len(Actions))
	for i, a := range Actions {
		out[i] = a.Name()
	}
	return out
}

// ActionArgs are the neutral arguments of an action. Which fields an action
// reads: Shell: Command; ReadFile: Path; WriteFile: Path, Content; EditFile: Path, Old, New.
type ActionArgs struct {
	Command string
	Path    string
	Content string
	Old     string
	New     string
}
