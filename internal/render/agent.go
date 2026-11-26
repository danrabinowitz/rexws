// Package render turns core models into display strings. AgentFormatter is
// the seam for alternative agent-column designs: Counts is option A (every
// state with its count). A frontend can add an aggregate+count or compact
// implementation without touching the CLI or TUI.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/core"
)

// AgentFormatter renders a workstream's agent cell.
type AgentFormatter interface {
	Format(ws core.Workstream) string
}

// Counts is option A: list every agent state with its count, most urgent
// first. A single agent omits the count.
type Counts struct{}

// Default is the formatter used by the CLI and the TUI unless a frontend
// chooses another implementation.
var Default AgentFormatter = Counts{}

type agentKey struct {
	state  core.AgentState
	detail string
}

type agentGroup struct {
	key   agentKey
	count int
}

// Format implements AgentFormatter.
func (Counts) Format(ws core.Workstream) string {
	switch ws.AgentState {
	case core.AgentStopped:
		return "stopped"
	case core.AgentUnknown:
		if len(ws.Agents) == 0 {
			return "unknown"
		}
	}
	if len(ws.Agents) == 0 {
		return "idle"
	}
	return formatAgents(ws.Agents)
}

// FormatWindowAgents renders one window's agents with the same option-A rules
// as the parent badge, considering only agents whose WindowID is windowID. A
// window with no reporting agents renders as an em dash.
func FormatWindowAgents(ws core.Workstream, windowID string) string {
	agents := windowAgents(ws, windowID)
	if len(agents) == 0 {
		return "—"
	}
	return formatAgents(agents)
}

// DisplayApp is the app name shown for an agent: App when Rex reported one,
// otherwise EffectiveApp. Callers that need a hidden EffectiveApp (search)
// should inspect App directly.
func DisplayApp(agent core.Agent) string {
	if agent.App != "" {
		return agent.App
	}
	return agent.EffectiveApp
}

// WindowApp returns the app name shown for a window: the focused block's app
// when that block is an agent, otherwise the app of the agent that determines
// the window's highest severity. It returns an em dash when neither exists.
func WindowApp(ws core.Workstream, window core.Window) string {
	for _, agent := range ws.Agents {
		if agent.WindowID == window.ID && agent.BlockID == window.FocusedBlockID {
			if app := DisplayApp(agent); app != "" {
				return app
			}
			break
		}
	}
	var best *core.Agent
	for i := range ws.Agents {
		agent := &ws.Agents[i]
		if agent.WindowID != window.ID {
			continue
		}
		if best == nil || agentSeverity(*agent) > agentSeverity(*best) {
			best = agent
		}
	}
	if best != nil {
		if app := DisplayApp(*best); app != "" {
			return app
		}
	}
	return "—"
}

func windowAgents(ws core.Workstream, windowID string) []core.Agent {
	agents := make([]core.Agent, 0, len(ws.Agents))
	for _, agent := range ws.Agents {
		if agent.WindowID == windowID {
			agents = append(agents, agent)
		}
	}
	return agents
}

func agentSeverity(agent core.Agent) int {
	state := agent.State
	if state == "" {
		state = core.AgentUnknown
	}
	return state.Severity()
}

func formatAgents(agents []core.Agent) string {
	groups := make([]agentGroup, 0, len(agents))
	index := make(map[agentKey]int, len(agents))
	for _, agent := range agents {
		state := agent.State
		if state == "" {
			state = core.AgentUnknown
		}
		key := agentKey{state: state}
		if state == core.AgentBlocked {
			key.detail = agent.Detail
		}
		if i, ok := index[key]; ok {
			groups[i].count++
			continue
		}
		index[key] = len(groups)
		groups = append(groups, agentGroup{key: key, count: 1})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].key.state.Severity() > groups[j].key.state.Severity()
	})

	multiple := len(agents) > 1
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		label := stateLabel(group.key)
		if multiple {
			label = fmt.Sprintf("%s ×%d", label, group.count)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

func stateLabel(key agentKey) string {
	if key.state == core.AgentBlocked && key.detail != "" {
		return fmt.Sprintf("blocked (%s)", key.detail)
	}
	return string(key.state)
}
