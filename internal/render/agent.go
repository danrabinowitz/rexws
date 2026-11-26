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

	groups := make([]agentGroup, 0, len(ws.Agents))
	index := make(map[agentKey]int, len(ws.Agents))
	for _, agent := range ws.Agents {
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

	multiple := len(ws.Agents) > 1
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
