package ops

import (
	"context"
	"fmt"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// RexState is the Rex view at one moment. Err means Rex itself was
// unavailable; Warnings describe sessions or blocks that could not be read
// while Rex was reachable.
type RexState struct {
	Sessions []core.Session
	Warnings []string
	Err      error
}

// ReadRex snapshots live Rex sessions and converts them to the core model.
func ReadRex(ctx context.Context, cfg config.Config) RexState {
	snapshot, err := rex.New(cfg.RexBinary()).Snapshot(ctx)
	if err != nil {
		return RexState{Err: err}
	}
	sessions, warnings := SessionsFromSnapshot(snapshot)
	return RexState{Sessions: sessions, Warnings: warnings}
}

// SessionsFromSnapshot converts Rex's per-block view into the core model. A
// session whose blocks could not be listed is unreadable; a block whose status
// could not be read becomes an unknown agent with its error preserved.
func SessionsFromSnapshot(snapshot []rex.SessionStatus) ([]core.Session, []string) {
	var warnings []string
	sessions := make([]core.Session, 0, len(snapshot))
	for _, item := range snapshot {
		session := core.Session{ID: item.ID, Label: item.Label}
		if item.Err != nil {
			session.Unreadable = true
			warnings = append(warnings, fmt.Sprintf("session %q: %v", item.Label, item.Err))
			sessions = append(sessions, session)
			continue
		}
		agents, blockWarnings := AgentsFromBlocks(item.Label, item.Blocks)
		session.Agents = agents
		warnings = append(warnings, blockWarnings...)
		session.Windows = WindowsFromRex(item.Windows)
		sessions = append(sessions, session)
	}
	return sessions, warnings
}

// AgentsFromBlocks converts Rex's per-block view into core agents. A block
// whose status could not be read becomes an unknown agent with its error
// preserved.
func AgentsFromBlocks(label string, blocks []rex.BlockStatus) ([]core.Agent, []string) {
	var warnings []string
	agents := make([]core.Agent, 0, len(blocks))
	for _, block := range blocks {
		agent := core.Agent{
			BlockID:      block.BlockID,
			BlockLabel:   block.BlockLabel,
			WindowID:     block.WindowID,
			WindowLabel:  block.WindowLabel,
			App:          block.App,
			EffectiveApp: block.EffectiveApp,
			State:        block.State,
			Detail:       block.Detail,
			Msg:          block.Msg,
			Title:        block.Title,
			UpdatedAt:    block.UpdatedAt,
		}
		if block.Err != nil {
			agent.State = core.AgentUnknown
			agent.Error = block.Err.Error()
			warnings = append(warnings, fmt.Sprintf("session %q %s: %v", label, agentLocation(block), block.Err))
		}
		agents = append(agents, agent)
	}
	return agents, warnings
}

// WindowsFromRex converts Rex windows into the core window model.
func WindowsFromRex(windows []rex.Window) []core.Window {
	out := make([]core.Window, 0, len(windows))
	for _, window := range windows {
		out = append(out, core.Window{
			ID:             window.ID,
			Label:          window.Label,
			Active:         window.Active,
			FocusedBlockID: window.FocusedBlockID,
			BlockIDs:       append([]string{}, window.BlockIDs...),
		})
	}
	return out
}

// agentLocation names the pane a status warning is about.
func agentLocation(block rex.BlockStatus) string {
	window := block.WindowLabel
	if window == "" {
		window = shortID(block.WindowID)
	}
	label := block.BlockLabel
	if label == "" {
		label = shortID(block.BlockID)
	}
	return fmt.Sprintf("window %q block %q", window, label)
}

func shortID(id string) string {
	if id == "" {
		return "-"
	}
	const width = 6
	if len(id) > width {
		return id[len(id)-width:]
	}
	return id
}
