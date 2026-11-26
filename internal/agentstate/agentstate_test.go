package agentstate

import "testing"

func TestSeverityOrder(t *testing.T) {
	order := []State{Blocked, Error, Working, Unknown, Done, Idle, Stopped}
	for i := 1; i < len(order); i++ {
		if order[i-1].Severity() <= order[i].Severity() {
			t.Errorf("%q severity %d is not above %q severity %d",
				order[i-1], order[i-1].Severity(), order[i], order[i].Severity())
		}
	}
}

func TestNormalize(t *testing.T) {
	for _, state := range []State{Blocked, Error, Working, Done, Idle} {
		if got := Normalize(string(state)); got != state {
			t.Errorf("Normalize(%q) = %q", state, got)
		}
	}
	for _, value := range []string{"", "teleporting", "unknown", "stopped"} {
		if got := Normalize(value); got != Idle {
			t.Errorf("Normalize(%q) = %q, want idle", value, got)
		}
	}
}
