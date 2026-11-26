package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func testState(t *testing.T) State {
	t.Helper()
	s := newState()
	s.cfg = config.Default()
	s.cfg.Projects = []config.Project{
		{Name: "rexws", Path: "/code/rexws"},
		{Name: "do-si-do", Path: "/code/do-si-do"},
	}
	s.haveConfig = true
	s.rexAvailable = true
	s.rexEverLoaded = true
	s.workstreams = sampleWorkstreams()
	s.width = 80
	s.height = 24
	s = s.rebuild()
	if s.cursorKey.ID == "" {
		t.Fatal("test state has no initial selection")
	}
	return s
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func typeString(s State, text string) State {
	for _, r := range text {
		var effects Effects
		s, effects = Reduce(s, runeKey(r))
		if effects.Quit {
			panic("typing quit the app")
		}
	}
	return s
}

func TestNormalNavigationSkipsGroupsAndWraps(t *testing.T) {
	s := testState(t)
	if s.cursorKey.ID != "/wt/rexws" {
		t.Fatalf("initial cursor = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, runeKey('j'))
	if s.cursorKey.ID != "/wt/dosido" {
		t.Errorf("down = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, runeKey('j'))
	if s.cursorKey.ID != "/wt/mono" {
		t.Errorf("down skips the group header, got %#v", s.cursorKey)
	}
	s, _ = Reduce(s, runeKey('j'))
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("down = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, runeKey('j'))
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("down did not wrap to the first row: %#v", s.cursorKey)
	}
	s, _ = Reduce(s, runeKey('k'))
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("up did not wrap to the last row: %#v", s.cursorKey)
	}
}

func TestArrowKeysWrapAtEdges(t *testing.T) {
	s := testState(t)
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyDown})
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyDown})
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("down arrow did not wrap: %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyUp})
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("up arrow did not wrap: %#v", s.cursorKey)
	}
}

func TestPageKeysStopAtEdges(t *testing.T) {
	s := testState(t)
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, runeKey('j'))
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("PgDn wrapped past the end: %#v", s.cursorKey)
	}
	for s.cursorKey.ID != "/wt/rexws" {
		s, _ = Reduce(s, runeKey('k'))
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("PgUp wrapped past the start: %#v", s.cursorKey)
	}
}

func TestPageKeysStopAtEdgesWithOneRowPage(t *testing.T) {
	// A height of 4 leaves one row of body, so a page jump is also a single
	// step. It must clamp instead of taking the wrapping j/k path.
	s := testState(t)
	s.height = 4
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, runeKey('j'))
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("single-row PgDn wrapped past the end: %#v", s.cursorKey)
	}
	for s.cursorKey.ID != "/wt/rexws" {
		s, _ = Reduce(s, runeKey('k'))
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("single-row PgUp wrapped past the start: %#v", s.cursorKey)
	}
}

func TestSearchNavigationStopsAtEdges(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	if s.mode != ModeSearch || s.query != "" {
		t.Fatalf("search mode = %v, query = %q", s.mode, s.query)
	}
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyDown})
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyCtrlN}} {
		s, _ = Reduce(s, key)
		if s.cursorKey.ID != "/wt/ai" {
			t.Errorf("%v moved past the last row: %#v", key, s.cursorKey)
		}
	}
	for s.cursorKey.ID != "/wt/rexws" {
		s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyUp})
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyCtrlP}} {
		s, _ = Reduce(s, key)
		if s.cursorKey.ID != "/wt/rexws" {
			t.Errorf("%v moved past the first row: %#v", key, s.cursorKey)
		}
	}
}

func TestRightExpandsThenEntersChildAndLeftRoundTrips(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	parent, ok := rowByKey(s.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !parent.Expanded {
		t.Fatalf("first right did not expand: %#v", parent)
	}
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("first right moved the cursor to %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if s.cursorKey.ID != "window:agent" {
		t.Errorf("second right = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyLeft})
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("left from child = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyLeft})
	parent, _ = rowByKey(s.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if parent.Expanded {
		t.Error("left did not collapse the parent")
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyLeft})
	if s.cursorKey.ID != "/wt/rexws" {
		t.Errorf("left on a collapsed parent changed the cursor: %#v", s.cursorKey)
	}
}

func TestRightOnWindowAndWindowlessParentIsNoop(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if s.cursorKey.ID != "window:agent" {
		t.Fatalf("setup cursor = %#v", s.cursorKey)
	}
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if effects != (Effects{}) || s.cursorKey.ID != "window:agent" {
		t.Errorf("right on a window changed state: %#v %#v", effects, s.cursorKey)
	}

	s = testState(t)
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, runeKey('j'))
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if s.cursorKey.ID != "/wt/ai" {
		t.Errorf("right on a windowless parent changed the cursor: %#v", s.cursorKey)
	}
}

func TestSpaceTogglesWithoutMoving(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	parent, _ := rowByKey(s.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !parent.Expanded || s.cursorKey.ID != "/wt/rexws" {
		t.Fatalf("space = %#v, cursor %#v", parent, s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if s.cursorKey.ID != "window:agent" {
		t.Fatalf("child cursor = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if s.cursorKey.ID != "window:agent" {
		t.Errorf("space on a child moved the cursor: %#v", s.cursorKey)
	}
}

func TestSearchTypesSpacesAndJK(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	if s.mode != ModeSearch {
		t.Fatalf("mode = %v", s.mode)
	}
	s = typeString(s, "a b")
	if s.query != "a b" {
		t.Errorf("query = %q", s.query)
	}
	if s.mode != ModeSearch {
		t.Errorf("mode = %v", s.mode)
	}

	s = testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "jk")
	if s.query != "jk" || s.mode != ModeSearch {
		t.Errorf("j/k typed query = %q mode %v", s.query, s.mode)
	}
}

func TestSearchLeftRightEditsQuery(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "ab")
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyLeft})
	s = typeString(s, "X")
	if s.query != "aXb" {
		t.Errorf("query = %q", s.query)
	}
}

func TestSearchCtrlUClearsAndStaysFocused(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "tests")
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyCtrlU})
	if s.query != "" || s.mode != ModeSearch {
		t.Errorf("query = %q mode = %v", s.query, s.mode)
	}
}

func TestSearchEnterAcceptsThenSecondEnterSwitches(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "tests")
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects != (Effects{}) {
		t.Errorf("accept produced effects: %#v", effects)
	}
	if s.mode != ModeNormal || s.query != "tests" {
		t.Fatalf("after accept mode = %v query = %q", s.mode, s.query)
	}
	s, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Switch == nil {
		t.Fatalf("second enter produced %#v", effects)
	}
	if effects.Switch.SessionName != "rexws/m1-m2-core-rex" {
		t.Errorf("switch = %#v", effects.Switch)
	}
	if effects.Switch.WindowID != "window:agent" {
		t.Errorf("active window was not passed: %#v", effects.Switch)
	}
}

func TestSearchEnterOnAWindowSwitchesByID(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "tests")
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyDown})
	if s.cursorKey.ID != "window:tests" {
		t.Fatalf("search down = %#v", s.cursorKey)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if s.mode != ModeNormal {
		t.Fatalf("mode = %v", s.mode)
	}
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Switch == nil || effects.Switch.WindowID != "window:tests" {
		t.Fatalf("switch = %#v", effects.Switch)
	}
}

func TestSearchEscClearsAndRestoresExpansion(t *testing.T) {
	s := testState(t)
	if s.expanded["/wt/rexws"] {
		t.Fatal("fixture unexpectedly expanded")
	}
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "tests")
	parent, _ := rowByKey(s.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !parent.Expanded {
		t.Fatalf("filter did not force the parent open: %#v", parent)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != ModeNormal || s.query != "" {
		t.Fatalf("after esc mode = %v query = %q", s.mode, s.query)
	}
	if s.expanded["/wt/rexws"] {
		t.Error("expansion snapshot was not restored")
	}
	parent, _ = rowByKey(s.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if parent.Expanded {
		t.Error("parent is still expanded after clearing the filter")
	}
}

func TestSearchBackspaceToEmptyClearsFilterAndStaysFocused(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "a")
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyBackspace})
	if s.query != "" || s.mode != ModeSearch {
		t.Errorf("query = %q mode = %v", s.query, s.mode)
	}
	if !s.input.Focused() {
		t.Error("search input lost focus")
	}
}

func TestEscPriorityAndFilterClear(t *testing.T) {
	// Help closes first.
	s := testState(t)
	s, _ = Reduce(s, runeKey('?'))
	if s.mode != ModeHelp {
		t.Fatalf("help mode = %v", s.mode)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != ModeNormal {
		t.Errorf("esc did not close help: %v", s.mode)
	}

	// A normal-mode filter is cleared by Esc.
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "tests")
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if s.query != "tests" {
		t.Fatalf("query = %q", s.query)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.query != "" {
		t.Errorf("esc did not clear the filter: %q", s.query)
	}

	// Esc with no modal, search, or filter is a no-op and never moves level.
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != ModeNormal || s.cursorKey.ID == "" {
		t.Errorf("esc changed state: mode %v cursor %#v", s.mode, s.cursorKey)
	}
}

func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	for _, mode := range []Mode{ModeNormal, ModeSearch, ModeStatus, ModeHelp, ModeNew, ModeRemove, ModeFatal, ModeCleanup, ModeCleanupDetail, ModeCleanupConfirm} {
		s := testState(t)
		s.mode = mode
		_, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyCtrlC})
		if !effects.Quit {
			t.Errorf("mode %v: ctrl+c did not quit", mode)
		}
	}
}

func TestQOnlyQuitsInNormalMode(t *testing.T) {
	s := testState(t)
	if _, effects := Reduce(s, runeKey('q')); !effects.Quit {
		t.Error("q did not quit in normal mode")
	}
	s, _ = Reduce(s, runeKey('/'))
	s = typeString(s, "q")
	if s.query != "q" {
		t.Errorf("q in search typed %q", s.query)
	}
}

func TestNoSelectionMakesRowActionsNoops(t *testing.T) {
	s := newState()
	s.cfg = config.Default()
	s.cfg.Projects = []config.Project{{Name: "rexws", Path: "/code/rexws"}}
	s.haveConfig = true
	s.rexAvailable = true
	s.width, s.height = 80, 24
	s = s.rebuild()
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		runeKey('s'),
		runeKey('d'),
		{Type: tea.KeyRight},
		{Type: tea.KeySpace},
	} {
		_, effects := Reduce(s, msg)
		if effects != (Effects{}) {
			t.Errorf("key %v produced %#v with no selection", msg, effects)
		}
	}
	// n still works.
	s, _ = Reduce(s, runeKey('n'))
	if s.mode != ModeNew {
		t.Errorf("n did not open the form with no selection: %v", s.mode)
	}
}

func TestSingleFlightIgnoresMutatingKeys(t *testing.T) {
	s := testState(t)
	s.pending = true
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		runeKey('s'),
		runeKey('d'),
		runeKey('n'),
		runeKey('r'),
	} {
		_, effects := Reduce(s, msg)
		if effects != (Effects{}) {
			t.Errorf("key %v produced %#v while pending", msg, effects)
		}
	}
	s, _ = Reduce(s, runeKey('j'))
	if s.cursorKey.ID != "/wt/dosido" {
		t.Errorf("navigation was blocked while pending: %#v", s.cursorKey)
	}
}

func TestRexUnavailableDisablesCreateAndRemove(t *testing.T) {
	s := testState(t)
	s.rexAvailable = false
	s.rexEverLoaded = false
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Switch != nil {
		t.Errorf("enter switched while Rex was unavailable: %#v", effects)
	}
	if len(s.notices) == 0 {
		t.Error("enter did not add a Rex-unavailable notice")
	}
	s, effects = Reduce(s, runeKey('d'))
	if effects.PreflightRemove != nil {
		t.Errorf("d preflighted while Rex was unavailable: %#v", effects)
	}
	// s remains available.
	s, _ = Reduce(s, runeKey('s'))
	if s.mode != ModeStatus {
		t.Errorf("s did not open the status selector: %v", s.mode)
	}
}

func TestStatusSelectorAppliesAndNoopsCurrent(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('s'))
	if s.mode != ModeStatus || s.status == nil {
		t.Fatalf("status mode = %v", s.mode)
	}
	if s.status.index != 0 {
		t.Errorf("current status index = %d", s.status.index)
	}
	s, effects := Reduce(s, runeKey('2'))
	if effects.SetStatus == nil || effects.SetStatus.Path != "/wt/rexws" || effects.SetStatus.Status != config.StatusBlocked {
		t.Fatalf("apply = %#v", effects)
	}
	if s.mode != ModeNormal {
		t.Errorf("mode after apply = %v", s.mode)
	}

	s = testState(t)
	s, _ = Reduce(s, runeKey('s'))
	s, effects = Reduce(s, runeKey('1'))
	if effects.SetStatus != nil {
		t.Errorf("applying the current status produced %#v", effects)
	}
	if s.mode != ModeNormal {
		t.Errorf("mode = %v", s.mode)
	}
}

func TestStatusSelectorEscCancels(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('s'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != ModeNormal || s.status != nil {
		t.Errorf("esc did not close the selector: mode %v", s.mode)
	}
}

func TestRemoveKeyEmitsPreflight(t *testing.T) {
	s := testState(t)
	s, effects := Reduce(s, runeKey('d'))
	if effects.PreflightRemove == nil || effects.PreflightRemove.Path != "/wt/rexws" {
		t.Fatalf("effects = %#v", effects)
	}
	// A window targets its workstream.
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if s.cursorKey.ID != "window:agent" {
		t.Fatalf("cursor = %#v", s.cursorKey)
	}
	s, effects = Reduce(s, runeKey('d'))
	if effects.PreflightRemove == nil || effects.PreflightRemove.Path != "/wt/rexws" {
		t.Errorf("window remove = %#v", effects)
	}
}

func TestRemoveConfirmation(t *testing.T) {
	s := testState(t)
	plan := ops.RemovePlan{
		Project: config.Project{Name: "rexws"},
		Path:    "/wt/rexws",
		Branch:  "m1-m2-core-rex",
	}
	s.remove = &removeModal{plan: plan, project: "rexws", branch: "m1-m2-core-rex"}
	s.mode = ModeRemove
	s, _ = Reduce(s, runeKey('n'))
	if s.mode != ModeNormal || s.remove != nil {
		t.Errorf("n did not cancel: mode %v", s.mode)
	}

	s.remove = &removeModal{plan: plan}
	s.mode = ModeRemove
	s, effects := Reduce(s, runeKey('y'))
	if effects.ExecuteRemove == nil || s.mode != ModeNormal {
		t.Errorf("y did not confirm: %#v mode %v", effects, s.mode)
	}

	s.remove = &removeModal{plan: plan}
	s.mode = ModeRemove
	_, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.ExecuteRemove == nil {
		t.Errorf("enter did not confirm: %#v", effects)
	}
}

func TestNewWorktreeForm(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('n'))
	if s.mode != ModeNew || s.newForm == nil {
		t.Fatalf("mode = %v", s.mode)
	}
	// First sorted project is do-si-do.
	if got, ok := s.newFormProject(); !ok || got.Name != "do-si-do" {
		t.Fatalf("project = %#v", got)
	}
	s = typeString(s, "feat/x")
	if s.newForm.branch.Value() != "feat/x" {
		t.Errorf("branch = %q", s.newForm.branch.Value())
	}
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Create == nil || effects.Create.Project != "do-si-do" || effects.Create.Branch != "feat/x" {
		t.Fatalf("create = %#v", effects.Create)
	}

	// Tab moves to the project field; Right cycles.
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyTab})
	if s.newForm.field != 0 {
		t.Fatalf("field = %d", s.newForm.field)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyRight})
	if got, _ := s.newFormProject(); got.Name != "rexws" {
		t.Errorf("project after right = %q", got.Name)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if s.newForm.field != 1 {
		t.Errorf("enter did not return to the branch field: %d", s.newForm.field)
	}

	// Esc cancels without an effect.
	_, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if effects != (Effects{}) {
		t.Errorf("esc produced %#v", effects)
	}
}

func TestNewWorktreeFormRequiresProjects(t *testing.T) {
	s := testState(t)
	s.cfg.Projects = nil
	s, effects := Reduce(s, runeKey('n'))
	if effects != (Effects{}) || s.mode != ModeNormal {
		t.Errorf("no-project n = mode %v effects %#v", s.mode, effects)
	}
	if len(s.notices) == 0 {
		t.Error("no-project n did not add a notice")
	}
}

func TestHelpScrollsAndCloses(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('?'))
	if s.mode != ModeHelp {
		t.Fatal(s.mode)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyDown})
	if s.helpOffset != 1 {
		t.Errorf("offset = %d", s.helpOffset)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.helpOffset != 11 {
		t.Errorf("offset = %d", s.helpOffset)
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.helpOffset != 1 {
		t.Errorf("offset = %d", s.helpOffset)
	}
	s, _ = Reduce(s, runeKey('?'))
	if s.mode != ModeNormal {
		t.Errorf("? did not close help: %v", s.mode)
	}
}

func TestFatalModeRetriesAndQuits(t *testing.T) {
	s := testState(t)
	s.mode = ModeFatal
	_, effects := Reduce(s, runeKey('r'))
	if !effects.Refresh || !effects.Reconnect {
		t.Errorf("r = %#v", effects)
	}
	_, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if effects != (Effects{}) {
		t.Errorf("esc = %#v", effects)
	}
	_, effects = Reduce(s, runeKey('q'))
	if !effects.Quit {
		t.Errorf("q = %#v", effects)
	}
}
