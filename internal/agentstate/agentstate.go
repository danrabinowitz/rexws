// Package agentstate holds the agent/program states rexws understands and
// their ordering. Rex reports these with OSC 7501; a state rexws does not know
// is treated as idle, as Rex documents.
package agentstate

// State is what a program in a terminal reports it is doing.
type State string

const (
	Blocked State = "blocked"
	Error   State = "error"
	Working State = "working"
	Unknown State = "unknown" // rexws could not read the status
	Done    State = "done"
	Idle    State = "idle"
	Stopped State = "stopped" // no Rex session
)

// Values is the canonical value set for the JSON contract. The schema manifest
// and its tests consume it.
func Values() []State {
	return []State{Blocked, Error, Working, Unknown, Done, Idle, Stopped}
}

// Severity orders states for aggregation and display, most urgent first.
func (s State) Severity() int {
	switch s {
	case Blocked:
		return 5
	case Error:
		return 4
	case Working:
		return 3
	case Unknown:
		return 2
	case Done:
		return 1
	case Idle:
		return 0
	default:
		return -1
	}
}

// Normalize maps a state reported by Rex to a rexws state. Values rexws does
// not know are idle, per the program-status schema.
func Normalize(value string) State {
	switch State(value) {
	case Blocked, Error, Working, Done, Idle:
		return State(value)
	default:
		return Idle
	}
}
