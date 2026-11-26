package render

import (
	"testing"

	"github.com/danrabinowitz/rexws/internal/core"
)

func workstream(state core.AgentState, agents ...core.Agent) core.Workstream {
	return core.Workstream{AgentState: state, Agents: agents}
}

func TestCounts(t *testing.T) {
	tests := []struct {
		name string
		ws   core.Workstream
		want string
	}{
		{"stopped", workstream(core.AgentStopped), "stopped"},
		{"idle with no agents", workstream(core.AgentIdle), "idle"},
		{"unknown with no agents", workstream(core.AgentUnknown), "unknown"},
		{"one working", workstream(core.AgentWorking, core.Agent{State: core.AgentWorking}), "working"},
		{"one blocked with kind", workstream(core.AgentBlocked, core.Agent{State: core.AgentBlocked, Detail: "permission"}), "blocked (permission)"},
		{"one blocked without kind", workstream(core.AgentBlocked, core.Agent{State: core.AgentBlocked}), "blocked"},
		{"two working", workstream(core.AgentWorking,
			core.Agent{State: core.AgentWorking}, core.Agent{State: core.AgentWorking}), "working ×2"},
		{"blocked and working", workstream(core.AgentBlocked,
			core.Agent{State: core.AgentBlocked, Detail: "permission"}, core.Agent{State: core.AgentWorking}),
			"blocked (permission) ×1, working ×1"},
		{"two different blocked kinds", workstream(core.AgentBlocked,
			core.Agent{State: core.AgentBlocked, Detail: "permission"}, core.Agent{State: core.AgentBlocked, Detail: "question"}),
			"blocked (permission) ×1, blocked (question) ×1"},
		{"error, done, done", workstream(core.AgentError,
			core.Agent{State: core.AgentError}, core.Agent{State: core.AgentDone}, core.Agent{State: core.AgentDone}),
			"error ×1, done ×2"},
		{"working and unknown", workstream(core.AgentWorking,
			core.Agent{State: core.AgentWorking}, core.Agent{State: core.AgentUnknown}),
			"working ×1, unknown ×1"},
		{"all unknown", workstream(core.AgentUnknown,
			core.Agent{State: core.AgentUnknown}, core.Agent{State: core.AgentUnknown}), "unknown ×2"},
		{"single idle agent", workstream(core.AgentIdle, core.Agent{State: core.AgentIdle}), "idle"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Default.Format(test.ws); got != test.want {
				t.Errorf("Format() = %q, want %q", got, test.want)
			}
		})
	}
}
