// Package core merges Git worktrees, Rex sessions, and persisted workflow
// metadata into the Workstream model shared by every frontend.
package core

import (
	"fmt"
	"sort"

	"github.com/danrabinowitz/rexws/internal/agentstate"
	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// AgentState is what the coding agent in a workstream's Rex session is doing.
type AgentState = agentstate.State

const (
	AgentBlocked = agentstate.Blocked
	AgentError   = agentstate.Error
	AgentWorking = agentstate.Working
	AgentDone    = agentstate.Done
	AgentIdle    = agentstate.Idle
	AgentStopped = agentstate.Stopped
	// AgentUnknown means a Rex session exists but its status could not be read.
	AgentUnknown = agentstate.Unknown
)

// Agent is one terminal block that reported program status. A plain shell pane
// with no reports is not an agent, so a session may have fewer agents than
// blocks.
type Agent struct {
	BlockID      string     `json:"block_id"`
	BlockLabel   string     `json:"block_label,omitempty"`
	WindowID     string     `json:"window_id"`
	WindowLabel  string     `json:"window_label,omitempty"`
	App          string     `json:"app,omitempty"`
	EffectiveApp string     `json:"effective_app,omitempty"`
	State        AgentState `json:"state"`
	Detail       string     `json:"detail,omitempty"`
	Msg          string     `json:"msg,omitempty"`
	Title        string     `json:"title,omitempty"`
	UpdatedAt    string     `json:"updated_at,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// Window is one Rex window (tab) in a session.
type Window struct {
	ID             string   `json:"window_id"`
	Label          string   `json:"label"`
	Active         bool     `json:"active"`
	FocusedBlockID string   `json:"focused_block_id,omitempty"`
	BlockIDs       []string `json:"block_ids,omitempty"`
}

// Workstream is one worktree enriched with workflow and Rex state.
type Workstream struct {
	Project             string        `json:"project"`
	Branch              string        `json:"branch"`
	Path                string        `json:"path"`
	SessionName         string        `json:"session_name"`
	WorkflowStatus      config.Status `json:"workflow_status"`
	WorkflowStatusLabel string        `json:"workflow_status_label"`
	Note                string        `json:"note,omitempty"`
	RexSessionID        string        `json:"rex_session_id"`
	AgentState          AgentState    `json:"agent_state"`
	AgentDetail         string        `json:"agent_detail,omitempty"`
	Agents              []Agent       `json:"agents"`
	Windows             []Window      `json:"windows"`
	Detached            bool          `json:"detached,omitempty"`
}

// Input is the discovered worktrees for one project.
type Input struct {
	Project   config.Project
	Worktrees []git.Worktree
}

// SkippedWorktree is a discovered worktree whose path could not be
// canonicalized. Both the workstream model and the selector leave it out of
// their candidate set so one stale, unreadable, or symlink-broken worktree
// cannot break unrelated results.
type SkippedWorktree struct {
	Project     config.Project
	Path        string
	Branch      string
	SessionName string
	Err         error
}

// Warning names the project and worktree and explains why it was skipped.
func (s SkippedWorktree) Warning() string {
	name := s.Path
	if s.Branch != "" {
		name = fmt.Sprintf("%s (%s)", s.Path, s.Branch)
	}
	return fmt.Sprintf("project %s: worktree %s is unreadable and was skipped: %v", s.Project.Name, name, s.Err)
}

// Detail explains a selector that names a skipped worktree.
func (s SkippedWorktree) Detail() string {
	return fmt.Sprintf("%s is a worktree of project %s but its path could not be read: %v; it was skipped",
		s.Path, s.Project.Name, s.Err)
}

// WorktreeCandidate resolves one discovered worktree into the values the
// workstream model and the selector both use. ok is false for a bare or
// prunable worktree, which are intentionally ignored. skipped is non-nil when
// the path cannot be canonicalized; the caller leaves the worktree out and may
// report skipped.Warning(). Sharing this helper keeps the two candidate sets
// from drifting.
func WorktreeCandidate(project config.Project, wt git.Worktree) (path, branch string, skipped *SkippedWorktree, ok bool) {
	if wt.Bare || wt.Prunable {
		return "", "", nil, false
	}
	branch = wt.Branch
	if branch == "" {
		branch = wt.ShortHead()
	}
	path, err := pathutil.Canonical(wt.Path)
	if err != nil {
		return "", branch, &SkippedWorktree{
			Project:     project,
			Path:        wt.Path,
			Branch:      branch,
			SessionName: project.Name + "/" + branch,
			Err:         err,
		}, false
	}
	return path, branch, nil, true
}

// Session is a Rex session enriched with its agents and windows.
type Session struct {
	ID    string
	Label string
	// Agents are the session's reporting terminal blocks.
	Agents []Agent
	// Windows are the session's live windows in Rex order.
	Windows []Window
	// Unreadable means the session's blocks or windows could not be listed;
	// Agents and Windows are then empty and the aggregate is unknown.
	Unreadable bool
}

// Build joins worktree discovery with state.json and Rex sessions. cfg
// supplies the default status and the display labels, so every frontend gets
// the same labels without resolving them again. A worktree whose path cannot
// be canonicalized is skipped. Build drops that warning and is kept for
// callers that cannot display it; BuildWithWarnings returns it for callers
// that can.
func Build(inputs []Input, state config.State, sessions []Session, cfg config.Config) ([]Workstream, error) {
	workstreams, _ := BuildWithWarnings(inputs, state, sessions, cfg)
	return workstreams, nil
}

// BuildWithWarnings is Build plus warnings for worktrees that could not be
// canonicalized and were skipped. The readable worktrees stay usable.
func BuildWithWarnings(inputs []Input, state config.State, sessions []Session, cfg config.Config) ([]Workstream, []string) {
	byLabel := make(map[string]Session, len(sessions))
	for _, s := range sessions {
		// Rex does not enforce unique labels. The first session in server
		// order wins; rexws-created labels are unique by construction.
		if _, ok := byLabel[s.Label]; !ok {
			byLabel[s.Label] = s
		}
	}

	warnings := make([]string, 0)
	workstreams := make([]Workstream, 0)
	for _, input := range inputs {
		for _, wt := range input.Worktrees {
			path, branch, skipped, ok := WorktreeCandidate(input.Project, wt)
			if skipped != nil {
				warnings = append(warnings, skipped.Warning())
				continue
			}
			if !ok {
				continue
			}
			stored := state.For(path, cfg.DefaultStatusValue())
			ws := Workstream{
				Project:             input.Project.Name,
				Branch:              branch,
				Path:                path,
				SessionName:         input.Project.Name + "/" + branch,
				WorkflowStatus:      stored.Status,
				WorkflowStatusLabel: cfg.StatusLabel(stored.Status),
				Note:                stored.Note,
				AgentState:          AgentStopped,
				Agents:              []Agent{},
				Windows:             []Window{},
				Detached:            wt.Detached,
			}
			if session, ok := byLabel[ws.SessionName]; ok {
				ws.RexSessionID = session.ID
				switch {
				case session.Unreadable:
					ws.AgentState = AgentUnknown
				default:
					ws.Agents = append([]Agent{}, session.Agents...)
					ws.Windows = append([]Window{}, session.Windows...)
					ws.AgentState, ws.AgentDetail = summarize(ws.Agents)
				}
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
	return workstreams, warnings
}

// Summarize reduces per-block agents to the most urgent state. Unknown agents
// do not outrank a known state; if every agent is unknown, the result is
// unknown. No agents means the session is idle.
func Summarize(agents []Agent) (AgentState, string) { return summarize(agents) }

// summarize reduces per-block agents to the most urgent state. Unknown agents
// do not outrank a known state; if every agent is unknown, the result is
// unknown. No agents means the session is idle.
func summarize(agents []Agent) (AgentState, string) {
	if len(agents) == 0 {
		return AgentIdle, ""
	}

	best := AgentUnknown
	bestSeverity := -1
	detail := ""
	known := false
	for _, agent := range agents {
		state := agent.State
		if state == "" {
			state = AgentUnknown
		}
		if state == AgentUnknown {
			continue
		}
		known = true
		severity := state.Severity()
		if severity > bestSeverity {
			best = state
			bestSeverity = severity
			detail = ""
			if state == AgentBlocked {
				detail = agent.Detail
			}
		} else if severity == bestSeverity && best == AgentBlocked && detail == "" {
			detail = agent.Detail
		}
	}
	if !known {
		return AgentUnknown, ""
	}
	return best, detail
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
