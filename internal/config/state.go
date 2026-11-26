package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

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

// LoadState reads state.json. A missing file yields empty state. Every stored
// status must appear in allowed, which comes from the configured status set.
func LoadState(allowed []Status) (State, error) {
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

	allowedSet := make(map[Status]bool, len(allowed))
	for _, status := range allowed {
		allowedSet[status] = true
	}
	var invalid []string
	for worktree, ws := range st.Workstreams {
		if !allowedSet[ws.Status] {
			invalid = append(invalid, fmt.Sprintf("%s (%s)", worktree, ws.Status))
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return State{}, fmt.Errorf("%s: entries use statuses not in config.json: %s; update them or add the statuses", path, strings.Join(invalid, ", "))
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

// For returns the stored metadata for a worktree path. Unknown paths use
// fallback, which is config.json's default_status.
func (s State) For(path string, fallback Status) WorkstreamState {
	if ws, ok := s.Workstreams[path]; ok {
		return ws
	}
	return WorkstreamState{Status: fallback}
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
