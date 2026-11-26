// Package tui is the rexws Bubbletea dashboard. The row model, filtering,
// cursor rules, and key reducer are pure so they can be tested without a
// terminal; the Bubbletea model only moves messages and runs effects.
package tui

import (
	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
)

// Mode is the current input mode. Modals capture keys before search, and
// search before normal mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModeSearch
	ModeStatus
	ModeRemove
	ModeHelp
	ModeNew
	ModeFatal
)

// RowKind is the level of a row. Group headers are not selectable.
type RowKind int

const (
	RowGroup RowKind = iota
	RowWorkstream
	RowWindow
)

// RowKey identifies a selectable row by its stable key: a workstream's
// canonical path or a window's window_id.
type RowKey struct {
	Kind RowKind
	ID   string
}

// Selectable reports whether navigation may land on the row.
func (r Row) Selectable() bool {
	return r.Kind == RowWorkstream || r.Kind == RowWindow
}

// Span is a rune range in a displayed string, used for match highlighting.
type Span struct {
	Start int
	End   int
}

// Row is one rendered line in the tree. Display strings are already
// sanitized; spans refer to those sanitized strings.
type Row struct {
	Kind  RowKind
	Key   RowKey
	Depth int

	// Group header.
	GroupID    config.Status
	GroupLabel string
	GroupCount string
	GroupSpans []Span

	// Workstream row.
	Path         string
	SessionName  string
	Project      string
	Branch       string
	Badge        string
	WindowCount  string
	HasWindows   bool
	Expanded     bool
	Matched      bool
	ContextMatch bool
	ProjectSpans []Span
	BranchSpans  []Span

	// Window row.
	WindowID      string
	WindowLabel   string
	WindowApp     string
	WindowActive  bool
	WindowMatched bool
	LabelSpans    []Span
	AppSpans      []Span
}

// BuildOptions are the inputs to BuildRows. Forced is the sticky set of
// parents a filter has forced open; BuildRows returns the updated set.
type BuildOptions struct {
	Workstreams  []core.Workstream
	Statuses     []config.StatusDef
	Expanded     map[string]bool
	Forced       map[string]bool
	Query        string
	BadgeUnknown bool
}

// BuildResult is the derived tree plus counts over the visible workstreams.
type BuildResult struct {
	Rows        []Row
	Forced      map[string]bool
	Workstreams int
	Live        int
	Blocked     int
}
