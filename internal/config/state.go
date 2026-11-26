package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

// Status is the human workflow stage of a workstream.
type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusInReview   Status = "in_review"
	StatusReady      Status = "ready"
	StatusDone       Status = "done"
)

// Statuses lists every workflow stage in dashboard order.
var Statuses = []Status{StatusInProgress, StatusBlocked, StatusInReview, StatusReady, StatusDone}

// ParseStatus validates a stage name.
func ParseStatus(s string) (Status, error) {
	status := Status(s)
	if !status.Valid() {
		return "", fmt.Errorf("unknown status %q (want one of %s)", s, StatusList())
	}
	return status, nil
}

// Valid reports whether s is a known workflow stage.
func (s Status) Valid() bool {
	return slices.Contains(Statuses, s)
}

// StatusList returns the stage names comma-separated, for error messages.
func StatusList() string {
	names := make([]string, len(Statuses))
	for i, s := range Statuses {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// WorkstreamState is the persisted metadata for one worktree.
type WorkstreamState struct {
	Status    Status    `json:"status"`
	Note      string    `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// State is the content of state.json.
type State struct {
	Workstreams map[string]WorkstreamState `json:"workstreams"`
}

// NewState returns an empty state.
func NewState() State {
	return State{Workstreams: make(map[string]WorkstreamState)}
}

// LoadState reads state.json. A missing file yields empty state.
func LoadState() (State, error) {
	path, err := StatePath()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if st.Workstreams == nil {
		st.Workstreams = make(map[string]WorkstreamState)
	}
	for worktree, ws := range st.Workstreams {
		if !ws.Status.Valid() {
			return State{}, fmt.Errorf("%s: workstream %q has unknown status %q", path, worktree, ws.Status)
		}
	}
	return st, nil
}

// Save writes state.json atomically.
func (s State) Save() error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	return writeJSON(path, s)
}

// For returns the stored metadata for a worktree path. Unknown paths default
// to ready, as the spec requires.
func (s State) For(path string) WorkstreamState {
	if ws, ok := s.Workstreams[path]; ok {
		return ws
	}
	return WorkstreamState{Status: StatusReady}
}

// SetStatus records a new workflow stage for a worktree, preserving its note.
func (s *State) SetStatus(path string, status Status, now time.Time) WorkstreamState {
	if s.Workstreams == nil {
		s.Workstreams = make(map[string]WorkstreamState)
	}
	ws := s.Workstreams[path]
	ws.Status = status
	ws.UpdatedAt = now.UTC()
	s.Workstreams[path] = ws
	return ws
}
