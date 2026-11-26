package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// State is the pure dashboard state. The Bubbletea model owns one State and
// replaces it with the reducer's result.
type State struct {
	cfg        config.Config
	haveConfig bool

	workstreams     []core.Workstream
	discoveryErrors []ops.ProjectError
	rexAvailable    bool
	rexEverLoaded   bool
	streamsPaused   bool

	fatalKind string
	fatalErr  error

	width   int
	height  int
	loading bool

	expanded map[string]bool
	snapshot map[string]bool
	forced   map[string]bool
	query    string
	mode     Mode

	cursorKey RowKey
	cursorRow int
	viewTop   int
	build     BuildResult
	rows      []Row

	input      textinput.Model
	status     *statusModal
	remove     *removeModal
	newForm    *newForm
	helpOffset int
	notices    []notice

	pending      bool
	pendingLabel string

	removePlan *ops.RemovePlan
	createPlan *ops.CreatePlan

	refreshPending map[string]bool
	refreshSeq     map[string]int
	refreshToken   map[string]int
}

type notice struct {
	text       string
	persistent bool
	expires    time.Time
}

type statusModal struct {
	path     string
	project  string
	branch   string
	current  config.Status
	statuses []config.StatusDef
	index    int
}

type removeModal struct {
	plan    ops.RemovePlan
	project string
	branch  string
}

type newForm struct {
	projects     []config.Project
	projectIndex int
	branch       textinput.Model
	field        int // 0 project, 1 branch
}

func newState() State {
	input := textinput.New()
	input.Prompt = " Search: "
	input.Width = 40
	return State{
		width:          80,
		height:         24,
		mode:           ModeNormal,
		cursorRow:      -1,
		expanded:       map[string]bool{},
		forced:         map[string]bool{},
		input:          input,
		refreshPending: map[string]bool{},
		refreshSeq:     map[string]int{},
		refreshToken:   map[string]int{},
	}
}

// badgeUnknown reports whether Rex has never supplied data, so badges are
// rendered as "?" instead of pretending the workstreams are stopped.
func (s State) badgeUnknown() bool {
	return !s.rexAvailable && !s.rexEverLoaded
}

// rebuild recomputes the tree and clamps the cursor.
func (s State) rebuild() State {
	s.build = BuildRows(BuildOptions{
		Workstreams:  s.workstreams,
		Statuses:     s.cfg.Statuses,
		Expanded:     s.expanded,
		Forced:       s.forced,
		Query:        s.query,
		BadgeUnknown: s.badgeUnknown(),
	})
	s.rows = s.build.Rows
	s.forced = s.build.Forced
	s.cursorKey, s.cursorRow = ClampCursor(s.rows, s.cursorKey, s.cursorRow)
	return s.ensureCursorVisible()
}

func (s State) bodyHeight() int {
	height := s.height - 3
	if len(s.notices) > 0 {
		height--
	}
	if height < 0 {
		height = 0
	}
	return height
}

// ensureCursorVisible scrolls the viewport by the minimum amount that reveals
// the cursor.
func (s State) ensureCursorVisible() State {
	body := s.bodyHeight()
	if body <= 0 || s.cursorRow < 0 {
		s.viewTop = 0
		return s
	}
	if s.cursorRow < s.viewTop {
		s.viewTop = s.cursorRow
	}
	if s.cursorRow >= s.viewTop+body {
		s.viewTop = s.cursorRow - body + 1
	}
	if maxTop := max(0, len(s.rows)-body); s.viewTop > maxTop {
		s.viewTop = maxTop
	}
	if s.viewTop < 0 {
		s.viewTop = 0
	}
	return s
}

// selectedRow returns the row under the cursor and its index, or false when
// nothing is selectable.
func (s State) selectedRow() (Row, int, bool) {
	if s.cursorKey.ID == "" {
		return Row{}, -1, false
	}
	for i, row := range s.rows {
		if row.Selectable() && row.Key == s.cursorKey {
			return row, i, true
		}
	}
	return Row{}, -1, false
}

func (s State) workstreamIndex(path string) (int, bool) {
	for i, ws := range s.workstreams {
		if ws.Path == path {
			return i, true
		}
	}
	return -1, false
}

func (s State) workstream(path string) (core.Workstream, bool) {
	index, ok := s.workstreamIndex(path)
	if !ok {
		return core.Workstream{}, false
	}
	return s.workstreams[index], true
}

// setQuery updates the filter and manages the one-shot expansion snapshot.
// Clearing the query restores the snapshot and drops sticky forcing.
func (s State) setQuery(query string) State {
	if query != "" && s.query == "" && s.snapshot == nil {
		s.snapshot = cloneSet(s.expanded)
	}
	s.query = query
	if query == "" {
		if s.snapshot != nil {
			s.expanded = cloneSet(s.snapshot)
		}
		s.snapshot = nil
		s.forced = nil
	}
	return s.rebuild()
}

func (s State) clearFilter() State {
	if s.query == "" {
		return s
	}
	s = s.setQuery("")
	s.input.SetValue("")
	s.input.CursorEnd()
	return s
}

func (s State) enterSearch() State {
	s.mode = ModeSearch
	s.input.SetValue(s.query)
	s.input.CursorEnd()
	s.input.Width = max(10, s.width-40)
	s.input.Focus()
	return s
}

func (s State) acceptSearch() State {
	s.mode = ModeNormal
	s.input.Blur()
	return s
}

func (s State) cancelSearch() State {
	s = s.clearFilter()
	s.mode = ModeNormal
	s.input.Blur()
	return s
}

func (s State) withExpanded(path string, expanded bool) State {
	s.expanded = cloneSet(s.expanded)
	s.expanded[path] = expanded
	return s.rebuild()
}

func (s State) openStatus(row Row) State {
	ws, ok := s.workstream(row.Path)
	if !ok {
		return s
	}
	index := 0
	for i, def := range s.cfg.Statuses {
		if def.ID == ws.WorkflowStatus {
			index = i
		}
	}
	s.status = &statusModal{
		path:     ws.Path,
		project:  ws.Project,
		branch:   ws.Branch,
		current:  ws.WorkflowStatus,
		statuses: s.cfg.Statuses,
		index:    index,
	}
	s.mode = ModeStatus
	return s
}

func (s State) openNewForm() State {
	projects := s.cfg.SortedProjects()
	if len(projects) == 0 {
		return s.addError("No projects registered. Register one with: rexws project add <path>")
	}
	branch := textinput.New()
	branch.Width = 40
	branch.Focus()
	s.newForm = &newForm{projects: projects, branch: branch, field: 1}
	s.mode = ModeNew
	return s
}

func (s State) newFormProject() (config.Project, bool) {
	if s.newForm == nil || len(s.newForm.projects) == 0 {
		return config.Project{}, false
	}
	return s.newForm.projects[s.newForm.projectIndex], true
}

func (s State) addError(text string) State {
	if text == "" {
		return s
	}
	s.notices = append(s.notices, notice{text: text, expires: time.Now().Add(8 * time.Second)})
	return s
}

// setPersistentWarnings replaces load-time warnings, keeping transient errors.
func (s State) setPersistentWarnings(warnings []string) State {
	kept := make([]notice, 0, len(s.notices)+len(warnings))
	for _, n := range s.notices {
		if !n.persistent {
			kept = append(kept, n)
		}
	}
	for _, warning := range warnings {
		kept = append(kept, notice{text: warning, persistent: true})
	}
	s.notices = kept
	return s
}

func (s State) pruneNotices(now time.Time) State {
	kept := s.notices[:0]
	for _, n := range s.notices {
		if n.persistent || n.expires.After(now) {
			kept = append(kept, n)
		}
	}
	s.notices = kept
	return s
}

func (s State) hasExpiringNotices(now time.Time) bool {
	for _, n := range s.notices {
		if !n.persistent && n.expires.After(now) {
			return true
		}
	}
	return false
}

func (s State) dismissNotice() State {
	if len(s.notices) == 0 {
		return s
	}
	s.notices = s.notices[:len(s.notices)-1]
	return s
}

// replaceWorkstream swaps one workstream in place (live agent or window
// updates) and rebuilds the tree.
func (s State) replaceWorkstream(index int, ws core.Workstream) State {
	s.workstreams = append([]core.Workstream{}, s.workstreams...)
	s.workstreams[index] = ws
	return s.rebuild()
}

// isSpace handles both the dedicated KeySpace and a rune space.
func isSpace(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeySpace || (msg.Type == tea.KeyRunes && string(msg.Runes) == " ")
}

func isRune(msg tea.KeyMsg, r rune) bool {
	return msg.Type == tea.KeyRunes && string(msg.Runes) == string(r)
}

// insertSearchRune appends r at the text input's cursor.
func insertSearchRune(input textinput.Model, r rune) textinput.Model {
	value := []rune(input.Value())
	position := input.Position()
	if position < 0 {
		position = 0
	}
	if position > len(value) {
		position = len(value)
	}
	value = append(value[:position], append([]rune{r}, value[position:]...)...)
	input.SetValue(string(value))
	input.SetCursor(position + 1)
	return input
}
