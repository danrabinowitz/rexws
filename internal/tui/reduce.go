package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// Effects are side effects the reducer asks the Bubbletea model to run.
type Effects struct {
	Quit            bool
	Refresh         bool
	Reconnect       bool
	Switch          *SwitchRequest
	SetStatus       *StatusRequest
	PreflightRemove *RemoveRequest
	ExecuteRemove   *ops.RemovePlan
	Create          *CreateRequest
}

// SwitchRequest selects a workstream's session or one of its windows.
type SwitchRequest struct {
	SessionName string
	Path        string
	WindowID    string
	// ExpandPath is set when Enter created a session for a stopped
	// workstream; the model expands that parent once after success.
	ExpandPath string
}

// StatusRequest applies a workflow status.
type StatusRequest struct {
	Path   string
	Status config.Status
}

// RemoveRequest starts the remove pre-checks.
type RemoveRequest struct {
	Path string
}

// CreateRequest asks the model to prepare and run a worktree provider.
type CreateRequest struct {
	Project string
	Branch  string
}

// Reduce applies one key press. It is pure: it returns the next state and the
// effects the model should execute. Modals take priority over search, and
// search over normal mode.
func Reduce(state State, msg tea.KeyMsg) (State, Effects) {
	if msg.Type == tea.KeyCtrlC {
		return state, Effects{Quit: true}
	}
	switch state.mode {
	case ModeFatal:
		return reduceFatal(state, msg)
	case ModeStatus:
		return reduceStatus(state, msg)
	case ModeRemove:
		return reduceRemove(state, msg)
	case ModeHelp:
		return reduceHelp(state, msg)
	case ModeNew:
		return reduceNewForm(state, msg)
	case ModeSearch:
		return reduceSearch(state, msg)
	default:
		return reduceNormal(state, msg)
	}
}

func reduceFatal(s State, msg tea.KeyMsg) (State, Effects) {
	switch {
	case isRune(msg, 'r'):
		return s, Effects{Refresh: true, Reconnect: true}
	case isRune(msg, 'q'):
		return s, Effects{Quit: true}
	}
	return s, Effects{}
}

func reduceNormal(s State, msg tea.KeyMsg) (State, Effects) {
	switch {
	case isRune(msg, 'q'):
		return s, Effects{Quit: true}
	case isRune(msg, '?'):
		s.mode = ModeHelp
		s.helpOffset = 0
		return s, Effects{}
	case isRune(msg, '/'):
		return s.enterSearch(), Effects{}
	case msg.Type == tea.KeyEsc:
		if s.query != "" {
			s = s.clearFilter()
		}
		return s, Effects{}
	case isRune(msg, 'x'):
		return s.dismissNotice(), Effects{}
	case isRune(msg, 'j'), msg.Type == tea.KeyDown:
		return moveSelection(s, 1), Effects{}
	case isRune(msg, 'k'), msg.Type == tea.KeyUp:
		return moveSelection(s, -1), Effects{}
	case isRune(msg, 'l'), msg.Type == tea.KeyRight:
		return expandRight(s), Effects{}
	case isRune(msg, 'h'), msg.Type == tea.KeyLeft:
		return collapseLeft(s), Effects{}
	case isSpace(msg):
		return toggleExpansion(s), Effects{}
	case msg.Type == tea.KeyEnter:
		return enterSelected(s)
	case isRune(msg, 's'):
		return statusKey(s), Effects{}
	case isRune(msg, 'n'):
		return newKey(s), Effects{}
	case isRune(msg, 'd'):
		return removeKey(s)
	case isRune(msg, 'r'):
		if s.pending || s.loading {
			return s, Effects{}
		}
		return s, Effects{Refresh: true, Reconnect: true}
	case msg.Type == tea.KeyPgDown:
		return moveSelection(s, pageSize(s)), Effects{}
	case msg.Type == tea.KeyPgUp:
		return moveSelection(s, -pageSize(s)), Effects{}
	}
	return s, Effects{}
}

func reduceSearch(s State, msg tea.KeyMsg) (State, Effects) {
	switch {
	case msg.Type == tea.KeyEsc:
		return s.cancelSearch(), Effects{}
	case msg.Type == tea.KeyEnter:
		return s.acceptSearch(), Effects{}
	case msg.Type == tea.KeyDown, msg.Type == tea.KeyCtrlN:
		return moveSelection(s, 1), Effects{}
	case msg.Type == tea.KeyUp, msg.Type == tea.KeyCtrlP:
		return moveSelection(s, -1), Effects{}
	case isSpace(msg):
		s.input = insertSearchRune(s.input, ' ')
		return s.setQuery(s.input.Value()), Effects{}
	case msg.Type == tea.KeyRunes,
		msg.Type == tea.KeyBackspace,
		msg.Type == tea.KeyDelete,
		msg.Type == tea.KeyLeft,
		msg.Type == tea.KeyRight,
		msg.Type == tea.KeyHome,
		msg.Type == tea.KeyEnd,
		msg.Type == tea.KeyCtrlU:
		s.input, _ = s.input.Update(msg)
		return s.setQuery(s.input.Value()), Effects{}
	}
	return s, Effects{}
}

func reduceStatus(s State, msg tea.KeyMsg) (State, Effects) {
	modal := s.status
	if modal == nil {
		s.mode = ModeNormal
		return s, Effects{}
	}
	switch {
	case msg.Type == tea.KeyEsc:
		s.status = nil
		s.mode = ModeNormal
	case msg.Type == tea.KeyUp:
		if modal.index > 0 {
			modal.index--
		}
	case msg.Type == tea.KeyDown:
		if modal.index < len(modal.statuses)-1 {
			modal.index++
		}
	case msg.Type == tea.KeyEnter:
		return applyStatus(s, modal, modal.index)
	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1:
		r := msg.Runes[0]
		if r >= '1' && r <= '9' {
			return applyStatus(s, modal, int(r-'1'))
		}
	}
	return s, Effects{}
}

func applyStatus(s State, modal *statusModal, index int) (State, Effects) {
	if index < 0 || index >= len(modal.statuses) {
		s.status = nil
		s.mode = ModeNormal
		return s, Effects{}
	}
	def := modal.statuses[index]
	s.status = nil
	s.mode = ModeNormal
	if def.ID == modal.current {
		return s, Effects{}
	}
	return s, Effects{SetStatus: &StatusRequest{Path: modal.path, Status: def.ID}}
}

func reduceRemove(s State, msg tea.KeyMsg) (State, Effects) {
	modal := s.remove
	if modal == nil {
		s.mode = ModeNormal
		return s, Effects{}
	}
	switch {
	case msg.Type == tea.KeyEsc, isRune(msg, 'n'):
		s.remove = nil
		s.mode = ModeNormal
	case msg.Type == tea.KeyEnter, isRune(msg, 'y'):
		plan := modal.plan
		s.remove = nil
		s.mode = ModeNormal
		return s, Effects{ExecuteRemove: &plan}
	}
	return s, Effects{}
}

func reduceHelp(s State, msg tea.KeyMsg) (State, Effects) {
	switch {
	case isRune(msg, '?'), msg.Type == tea.KeyEsc:
		s.mode = ModeNormal
	case msg.Type == tea.KeyUp:
		if s.helpOffset > 0 {
			s.helpOffset--
		}
	case msg.Type == tea.KeyDown:
		s.helpOffset++
	case msg.Type == tea.KeyPgUp:
		s.helpOffset = max(0, s.helpOffset-10)
	case msg.Type == tea.KeyPgDown:
		s.helpOffset += 10
	}
	return s, Effects{}
}

func reduceNewForm(s State, msg tea.KeyMsg) (State, Effects) {
	form := s.newForm
	if form == nil {
		s.mode = ModeNormal
		return s, Effects{}
	}
	if s.pending {
		return s, Effects{}
	}
	switch {
	case msg.Type == tea.KeyEsc:
		s.newForm = nil
		s.mode = ModeNormal
		return s, Effects{}
	case msg.Type == tea.KeyTab, msg.Type == tea.KeyUp, msg.Type == tea.KeyDown:
		form.field = 1 - form.field
		if form.field == 1 {
			form.branch.Focus()
		} else {
			form.branch.Blur()
		}
		return s, Effects{}
	}

	if form.field == 0 {
		switch {
		case msg.Type == tea.KeyLeft:
			form.projectIndex = (form.projectIndex - 1 + len(form.projects)) % len(form.projects)
		case msg.Type == tea.KeyRight:
			form.projectIndex = (form.projectIndex + 1) % len(form.projects)
		case msg.Type == tea.KeyEnter:
			form.field = 1
			form.branch.Focus()
		}
		return s, Effects{}
	}

	switch {
	case msg.Type == tea.KeyEnter:
		branch := strings.TrimSpace(form.branch.Value())
		if branch == "" {
			return s, Effects{}
		}
		project, ok := s.newFormProject()
		if !ok {
			return s, Effects{}
		}
		return s, Effects{Create: &CreateRequest{Project: project.Name, Branch: branch}}
	case isSpace(msg):
		form.branch = insertSearchRune(form.branch, ' ')
	case msg.Type == tea.KeyRunes,
		msg.Type == tea.KeyBackspace,
		msg.Type == tea.KeyDelete,
		msg.Type == tea.KeyLeft,
		msg.Type == tea.KeyRight,
		msg.Type == tea.KeyHome,
		msg.Type == tea.KeyEnd,
		msg.Type == tea.KeyCtrlU:
		form.branch, _ = form.branch.Update(msg)
	}
	return s, Effects{}
}

func moveSelection(s State, delta int) State {
	s.cursorKey, s.cursorRow = MoveCursor(s.rows, s.cursorRow, delta)
	return s.ensureCursorVisible()
}

func expandRight(s State) State {
	row, index, ok := s.selectedRow()
	if !ok || row.Kind != RowWorkstream || !row.HasWindows {
		return s
	}
	if !row.Expanded {
		return s.withExpanded(row.Path, true)
	}
	if key, childIndex, ok := FirstChild(s.rows, index); ok {
		s.cursorKey, s.cursorRow = key, childIndex
		return s.ensureCursorVisible()
	}
	return s
}

func collapseLeft(s State) State {
	row, index, ok := s.selectedRow()
	if !ok {
		return s
	}
	if row.Kind == RowWindow {
		if key, parentIndex, ok := ParentIndex(s.rows, index); ok {
			s.cursorKey, s.cursorRow = key, parentIndex
			return s.ensureCursorVisible()
		}
		return s
	}
	if !row.HasWindows || !row.Expanded {
		return s
	}
	// Sticky forced expansion wins while a filter is active.
	if s.query != "" && s.forced[row.Path] {
		return s
	}
	return s.withExpanded(row.Path, false)
}

func toggleExpansion(s State) State {
	row, _, ok := s.selectedRow()
	if !ok || row.Kind != RowWorkstream || !row.HasWindows {
		return s
	}
	if s.query != "" && s.forced[row.Path] {
		return s
	}
	return s.withExpanded(row.Path, !row.Expanded)
}

func enterSelected(s State) (State, Effects) {
	if s.pending {
		return s, Effects{}
	}
	row, _, ok := s.selectedRow()
	if !ok {
		return s, Effects{}
	}
	if !s.rexAvailable {
		return s.addError("Rex unavailable; press r to retry."), Effects{}
	}
	switch row.Kind {
	case RowWorkstream:
		ws, ok := s.workstream(row.Path)
		if !ok {
			return s, Effects{}
		}
		request := &SwitchRequest{SessionName: ws.SessionName, Path: ws.Path}
		if ws.RexSessionID != "" {
			request.WindowID = activeWindowID(ws)
		} else {
			request.ExpandPath = ws.Path
		}
		s.cursorKey = row.Key
		return s, Effects{Switch: request}
	case RowWindow:
		ws, ok := s.workstream(row.Path)
		if !ok || ws.RexSessionID == "" {
			return s.addError("That Rex session is no longer live; refreshing."), Effects{Refresh: true}
		}
		return s, Effects{Switch: &SwitchRequest{
			SessionName: ws.SessionName,
			Path:        ws.Path,
			WindowID:    row.WindowID,
		}}
	}
	return s, Effects{}
}

func statusKey(s State) State {
	if s.pending {
		return s
	}
	row, _, ok := s.selectedRow()
	if !ok {
		return s
	}
	return s.openStatus(row)
}

func newKey(s State) State {
	if s.pending {
		return s
	}
	return s.openNewForm()
}

func removeKey(s State) (State, Effects) {
	if s.pending {
		return s, Effects{}
	}
	row, _, ok := s.selectedRow()
	if !ok {
		return s, Effects{}
	}
	if !s.rexAvailable {
		return s.addError("Rex unavailable; press r to retry."), Effects{}
	}
	return s, Effects{PreflightRemove: &RemoveRequest{Path: row.Path}}
}

func activeWindowID(ws core.Workstream) string {
	for _, window := range ws.Windows {
		if window.Active {
			return window.ID
		}
	}
	return ""
}

func pageSize(s State) int {
	size := s.bodyHeight()
	if size < 1 {
		size = 1
	}
	return size
}
