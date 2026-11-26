package render

import (
	"testing"

	"github.com/danrabinowitz/rexws/internal/core"
)

func workstream(state core.AgentState, agents ...core.Agent) core.Workstream {
	return core.Workstream{AgentState: state, Agents: agents}
}

func TestFormatWindowAgents(t *testing.T) {
	ws := core.Workstream{
		AgentState: core.AgentBlocked,
		Agents: []core.Agent{
			{BlockID: "block:1", WindowID: "window:1", App: "claude-code", State: core.AgentBlocked, Detail: "permission"},
			{BlockID: "block:2", WindowID: "window:1", App: "claude-code", State: core.AgentWorking},
			{BlockID: "block:3", WindowID: "window:2", App: "cargo", State: core.AgentIdle},
		},
	}
	tests := []struct {
		name     string
		windowID string
		want     string
	}{
		{"two agents", "window:1", "blocked (permission) ×1, working ×1"},
		{"one agent", "window:2", "idle"},
		{"silent window", "window:3", "—"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatWindowAgents(ws, test.windowID); got != test.want {
				t.Errorf("FormatWindowAgents() = %q, want %q", got, test.want)
			}
		})
	}

	// The parent badge still aggregates every window.
	if got := Default.Format(ws); got != "blocked (permission) ×1, working ×1, idle ×1" {
		t.Errorf("parent badge = %q", got)
	}
}

func TestWindowApp(t *testing.T) {
	ws := core.Workstream{Agents: []core.Agent{
		{BlockID: "block:1", WindowID: "window:1", App: "", EffectiveApp: "hidden-app", State: core.AgentIdle},
		{BlockID: "block:2", WindowID: "window:1", App: "claude-code", State: core.AgentWorking},
		{BlockID: "block:3", WindowID: "window:2", App: "vim", EffectiveApp: "nvim", State: core.AgentIdle},
		{BlockID: "block:4", WindowID: "window:2", App: "cargo", State: core.AgentBlocked},
	}}
	tests := []struct {
		name   string
		window core.Window
		want   string
	}{
		{"focused block falls back to effective app", core.Window{ID: "window:1", FocusedBlockID: "block:1"}, "hidden-app"},
		{"focused block wins over severity", core.Window{ID: "window:1", FocusedBlockID: "block:2"}, "claude-code"},
		{"highest severity agent", core.Window{ID: "window:2"}, "cargo"},
		{"no agents", core.Window{ID: "window:3"}, "—"},
		{"focused block is silent", core.Window{ID: "window:2", FocusedBlockID: "block:silent"}, "cargo"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := WindowApp(ws, test.window); got != test.want {
				t.Errorf("WindowApp() = %q, want %q", got, test.want)
			}
		})
	}
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
