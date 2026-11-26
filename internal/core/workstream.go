// Package core merges Git worktrees, Rex sessions, and persisted workflow
// metadata into the Workstream model shared by every frontend.
package core

import (
	"fmt"
	"sort"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// AgentState is what the coding agent in a workstream's Rex session is doing.
type AgentState string

const (
	AgentBlocked AgentState = "blocked"
	AgentError   AgentState = "error"
	AgentWorking AgentState = "working"
	AgentDone    AgentState = "done"
	AgentIdle    AgentState = "idle"
	AgentStopped AgentState = "stopped"
	// AgentUnknown means a Rex session exists but its status could not be read.
	AgentUnknown AgentState = "unknown"
)

// Workstream is one worktree enriched with workflow and Rex state.
type Workstream struct {
	Project        string        `json:"project"`
	Branch         string        `json:"branch"`
	Path           string        `json:"path"`
	SessionName    string        `json:"session_name"`
	WorkflowStatus config.Status `json:"workflow_status"`
	Note           string        `json:"note,omitempty"`
	RexSessionID   string        `json:"rex_session_id"`
	AgentState     AgentState    `json:"agent_state"`
	AgentDetail    string        `json:"agent_detail,omitempty"`
	Detached       bool          `json:"detached,omitempty"`
}

// Input is the discovered worktrees for one project.
type Input struct {
	Project   config.Project
	Worktrees []git.Worktree
}

// Session is a Rex session enriched with its agent state.
type Session struct {
	ID          string
	Label       string
	AgentState  AgentState
	AgentDetail string
}

// Build joins worktree discovery with state.json and Rex sessions.
func Build(inputs []Input, state config.State, sessions []Session) ([]Workstream, error) {
	byLabel := make(map[string]Session, len(sessions))
	for _, s := range sessions {
		// Rex does not enforce unique labels. The first session in server
		// order wins; rexws-created labels are unique by construction.
		if _, ok := byLabel[s.Label]; !ok {
			byLabel[s.Label] = s
		}
	}

	workstreams := make([]Workstream, 0)
	for _, input := range inputs {
		for _, wt := range input.Worktrees {
			if wt.Bare || wt.Prunable {
				continue
			}
			path, err := pathutil.Canonical(wt.Path)
			if err != nil {
				return nil, fmt.Errorf("project %s: %w", input.Project.Name, err)
			}
			branch := wt.Branch
			if branch == "" {
				branch = wt.ShortHead()
			}
			stored := state.For(path)
			ws := Workstream{
				Project:        input.Project.Name,
				Branch:         branch,
				Path:           path,
				SessionName:    input.Project.Name + "/" + branch,
				WorkflowStatus: stored.Status,
				Note:           stored.Note,
				AgentState:     AgentStopped,
				Detached:       wt.Detached,
			}
			if session, ok := byLabel[ws.SessionName]; ok {
				ws.RexSessionID = session.ID
				ws.AgentState = session.AgentState
				ws.AgentDetail = session.AgentDetail
			}
			workstreams = append(workstreams, ws)
		}
	}

	sort.SliceStable(workstreams, func(i, j int) bool {
		a, b := workstreams[i], workstreams[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Detached != b.Detached {
			return !a.Detached
		}
		return a.Branch < b.Branch
	})
	return workstreams, nil
}

// FilterStatuses keeps only workstreams at one of the given stages. An empty
// filter keeps everything.
func FilterStatuses(workstreams []Workstream, statuses []config.Status) []Workstream {
	if len(statuses) == 0 {
		return workstreams
	}
	keep := make(map[config.Status]bool, len(statuses))
	for _, s := range statuses {
		keep[s] = true
	}
	out := make([]Workstream, 0, len(workstreams))
	for _, ws := range workstreams {
		if keep[ws.WorkflowStatus] {
			out = append(out, ws)
		}
	}
	return out
}
