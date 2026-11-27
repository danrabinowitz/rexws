package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func cleanupTestReport() ops.CleanupReport {
	return ops.CleanupReport{
		SchemaVersion: 1,
		Projects: []ops.CleanupProject{{
			Project:       "rexws",
			Path:          "/repo/rexws",
			DefaultBranch: "main",
			Items: []ops.CleanupItem{
				{
					Project: "rexws", Kind: ops.CleanupKindWorktree, Path: "/wt/safe", Branch: "feat/safe", Head: "aaa",
					Classification: ops.CleanupMerged, BranchState: ops.CleanupBranchMerged,
					Safe: true, Removable: true, Action: ops.CleanupActionRemoveWorktreeAndBranch,
					Detail: "branch is an ancestor of main",
				},
				{
					Project: "rexws", Kind: ops.CleanupKindBranch, Branch: "feat/merged", Head: "bbb",
					Classification: ops.CleanupMerged, BranchState: ops.CleanupBranchMerged,
					Safe: true, Removable: true, Action: ops.CleanupActionDeleteBranch,
					Detail: "branch is an ancestor of main",
				},
				{
					Project: "rexws", Kind: ops.CleanupKindWorktree, Path: "/wt/dirty", Branch: "feat/dirty", Head: "ccc",
					Classification: ops.CleanupDirty, BranchState: ops.CleanupBranchMerged,
					RequiresForce: true, Removable: true, Action: ops.CleanupActionRemoveWorktreeAndBranch,
					Detail: "uncommitted, untracked, or ignored changes: 2 changed paths",
					Loss:   &ops.CleanupLoss{DirtyFiles: []string{" M a.txt", "?? b.txt"}, Commits: []git.Commit{}},
				},
				{
					Project: "rexws", Kind: ops.CleanupKindBranch, Branch: "feat/unmerged", Head: "ddd",
					Classification: ops.CleanupUnmerged, BranchState: ops.CleanupBranchUnmerged,
					RequiresForce: true, Removable: true, Action: ops.CleanupActionDeleteBranch,
					Detail: "2 commits not in main",
					Loss:   &ops.CleanupLoss{DirtyFiles: []string{}, Commits: []git.Commit{{SHA: "ddd111", Subject: "unmerged work"}}},
				},
				{
					Project: "rexws", Kind: ops.CleanupKindBranch, Branch: "main", Head: "eee",
					Classification: ops.CleanupProtected, Action: ops.CleanupActionNone,
					Detail: "protected branch main",
				},
				{
					Project: "rexws", Kind: ops.CleanupKindWorktree, Path: "/wt/active", Branch: "feat/active", Head: "fff",
					Classification: ops.CleanupActive, ActiveSession: true, Action: ops.CleanupActionNone,
					Detail: "live Rex session session:x",
				},
			},
		}},
		Warnings: []string{"orphan scan incomplete"},
	}
}

func cleanupTestState(t *testing.T) State {
	t.Helper()
	s := testState(t)
	s.cleanup = &cleanupView{
		token:    1,
		selected: map[cleanupItemKey]bool{},
		report:   cleanupTestReport(),
	}
	s.mode = ModeCleanup
	return s.rebuildCleanup()
}

func cleanupCursorTo(s State, item int) State {
	for index, row := range s.cleanup.rows {
		if row.Kind == cleanupRowItem && row.Item == item {
			s.cleanup.cursor = index
			return s
		}
	}
	return s
}

func cleanupItemByBranch(s State, branch string) (cleanupItem, bool) {
	for _, item := range s.cleanup.items {
		if item.Item.Branch == branch {
			return item, true
		}
	}
	return cleanupItem{}, false
}

func TestCleanupGrouping(t *testing.T) {
	s := cleanupTestState(t)
	safe, review, blocked := cleanupGroupCounts(s.cleanup.items)
	if safe != 2 || review != 2 || blocked != 2 {
		t.Fatalf("counts = %d/%d/%d, items = %#v", safe, review, blocked, s.cleanup.items)
	}
	if len(s.cleanup.rows) != 9 {
		t.Fatalf("rows = %d: %#v", len(s.cleanup.rows), s.cleanup.rows)
	}
	wantGroups := []cleanupGroup{cleanupGroupSafe, cleanupGroupReview, cleanupGroupBlocked}
	var got []cleanupGroup
	for _, row := range s.cleanup.rows {
		if row.Kind == cleanupRowGroup {
			got = append(got, row.Group)
		}
	}
	if len(got) != 3 || got[0] != wantGroups[0] || got[1] != wantGroups[1] || got[2] != wantGroups[2] {
		t.Fatalf("group order = %v", got)
	}
	if s.cleanup.items[0].Item.Branch != "feat/safe" || s.cleanup.items[2].Item.Branch != "feat/dirty" || s.cleanup.items[4].Item.Branch != "main" {
		t.Errorf("grouped order = %s, %s, %s", s.cleanup.items[0].Item.Branch, s.cleanup.items[2].Item.Branch, s.cleanup.items[4].Item.Branch)
	}
	if s.cleanup.cursor != 1 {
		t.Errorf("initial cursor = %d, want the first candidate", s.cleanup.cursor)
	}
	if s.cleanup.items[2].Item.Detail == "" {
		t.Error("classification reason was lost")
	}
}

func TestCleanupEntryKey(t *testing.T) {
	s := testState(t)
	s, effects := Reduce(s, runeKey('c'))
	if !effects.BeginCleanup || s.mode != ModeCleanup || s.cleanup == nil || !s.cleanup.loading {
		t.Fatalf("c = mode %v effects %#v cleanup %#v", s.mode, effects, s.cleanup)
	}

	s = testState(t)
	s.rexAvailable = false
	s, effects = Reduce(s, runeKey('c'))
	if effects.BeginCleanup || s.mode != ModeNormal || s.cleanup != nil {
		t.Errorf("cleanup opened while Rex was unavailable: mode %v effects %#v", s.mode, effects)
	}
	if _, ok := tuiNotice(s, "Rex unavailable"); !ok {
		t.Errorf("notices = %#v", s.notices)
	}

	s = testState(t)
	s.pending = true
	s, effects = Reduce(s, runeKey('c'))
	if effects.BeginCleanup || s.mode != ModeNormal {
		t.Errorf("cleanup opened during a pending action: %#v", effects)
	}
}

func TestCleanupSelectionGating(t *testing.T) {
	s := cleanupTestState(t)

	// The first safe item is selectable.
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(s.cleanup.selected); got != 1 {
		t.Fatalf("selected = %d", got)
	}

	// A reviewed item needs force.
	s = cleanupCursorTo(s, 2)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(s.cleanup.selected); got != 1 {
		t.Errorf("review item selected without force: %d", got)
	}
	s, _ = Reduce(s, runeKey('f'))
	if !s.cleanup.force {
		t.Fatal("f did not enable force")
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(s.cleanup.selected); got != 2 {
		t.Errorf("review item not selected with force: %d", got)
	}
	// Turning force off deselects every force-required item.
	s, _ = Reduce(s, runeKey('f'))
	if s.cleanup.force {
		t.Fatal("f did not disable force")
	}
	if item, _ := cleanupItemByBranch(s, "feat/dirty"); s.cleanup.selected[item.Key] {
		t.Error("force-required item stayed selected with force off")
	}

	// A blocked row can never be selected, even with force.
	s, _ = Reduce(s, runeKey('f'))
	s = cleanupCursorTo(s, 4)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if item, _ := cleanupItemByBranch(s, "main"); s.cleanup.selected[item.Key] {
		t.Error("blocked item was selected")
	}
}

func TestCleanupSelectAllSafe(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	if got := len(s.cleanup.selected); got != 2 {
		t.Fatalf("select-all-safe selected %d", got)
	}
	for _, item := range s.cleanup.items {
		if item.Group != cleanupGroupSafe && s.cleanup.selected[item.Key] {
			t.Errorf("non-safe item %s was selected", item.Item.Branch)
		}
	}
	s, _ = Reduce(s, runeKey('a'))
	if got := len(s.cleanup.selected); got != 0 {
		t.Errorf("second select-all-safe left %d selected", got)
	}
}

func TestCleanupConfirmSnapshot(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects != (Effects{}) {
		t.Errorf("opening the confirm applied something: %#v", effects)
	}
	if s.mode != ModeCleanupConfirm || s.cleanup.confirm == nil {
		t.Fatalf("mode = %v confirm = %#v", s.mode, s.cleanup.confirm)
	}
	confirm := s.cleanup.confirm
	if confirm.items != 2 || confirm.worktrees != 1 || confirm.branches != 2 || confirm.prunes != 0 {
		t.Errorf("confirm counts = %#v", confirm)
	}
	if len(confirm.forceItems) != 0 {
		t.Errorf("force items = %#v", confirm.forceItems)
	}
	if confirm.retainedItems != 4 {
		t.Errorf("retained items = %d", confirm.retainedItems)
	}
	if len(confirm.retainedBranches) != 4 {
		t.Errorf("retained branches = %v", confirm.retainedBranches)
	}
	if len(confirm.report.Projects) != 1 || len(confirm.report.Projects[0].Items) != 2 {
		t.Fatalf("confirm report = %#v", confirm.report)
	}
	if confirm.report.Force {
		t.Error("confirm report claimed force")
	}

	// Cancel changes nothing and emits no effect.
	s, effects = Reduce(s, runeKey('n'))
	if effects != (Effects{}) || s.mode != ModeCleanup || s.cleanup.confirm != nil {
		t.Errorf("cancel = mode %v effects %#v", s.mode, effects)
	}

	// Confirm applies the exact snapshotted report. The cancel kept the
	// selection, so Enter reopens the same confirm.
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	s, effects = Reduce(s, runeKey('y'))
	if effects.ApplyCleanup == nil {
		t.Fatalf("y did not emit ApplyCleanup: %#v", effects)
	}
	if s.mode != ModeCleanup || !s.cleanup.applying || s.cleanup.loading {
		t.Errorf("after confirm mode %v applying %v loading %v", s.mode, s.cleanup.applying, s.cleanup.loading)
	}
	if len(effects.ApplyCleanup.Projects) != 1 || len(effects.ApplyCleanup.Projects[0].Items) != 2 {
		t.Errorf("applied report = %#v", effects.ApplyCleanup)
	}
}

func TestCleanupForceReportAndBlockedOmission(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, runeKey('f'))
	s = cleanupCursorTo(s, 2) // dirty worktree
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	s = cleanupCursorTo(s, 3) // unmerged branch
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})

	report := s.cleanup.selectedCleanupReport()
	if !report.Force {
		t.Error("force report did not carry force")
	}
	if got := len(report.Projects[0].Items); got != 4 {
		t.Errorf("selected items = %d, want 4", got)
	}

	// Even if a blocked key were forced into the selection set, the apply
	// report must omit it.
	blocked, ok := cleanupItemByBranch(s, "main")
	if !ok {
		t.Fatal("missing blocked fixture")
	}
	s.cleanup.selected[blocked.Key] = true
	report = s.cleanup.selectedCleanupReport()
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.Branch == "main" {
				t.Error("blocked item reached the apply report")
			}
		}
	}

	// With force off, force-required items are omitted even if selected.
	s, _ = Reduce(s, runeKey('f'))
	report = s.cleanup.selectedCleanupReport()
	if report.Force {
		t.Error("force off report carried force")
	}
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.RequiresForce {
				t.Errorf("force-required item %s reached the report with force off", item.Branch)
			}
		}
	}
}

func TestCleanupApplyResultsMerge(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	confirmed := s.cleanup.confirm.report
	confirmed.Applied = true
	for i := range confirmed.Projects[0].Items {
		switch i {
		case 0:
			confirmed.Projects[0].Items[i].Result = ops.CleanupResultRemoved
		case 1:
			confirmed.Projects[0].Items[i].Result = ops.CleanupResultFailed
			confirmed.Projects[0].Items[i].Error = "branch moved after planning; kept"
		}
	}
	confirmed.Totals = ops.CleanupTotals{Removed: 1, Failed: 1, Kept: 0}

	m := NewModel()
	defer m.Close()
	m.state = s
	m.state.cleanup.confirm = nil
	m.state.cleanup.applying = true
	m.state.cleanup.loading = true
	m.state.mode = ModeCleanup

	m.handleCleanupApplied(cleanupAppliedMsg{report: confirmed, err: errors.New("state save failed")})

	if m.state.cleanup.applied == false || m.state.cleanup.loading || m.state.cleanup.applying {
		t.Fatalf("cleanup state = %#v", m.state.cleanup)
	}
	if len(m.state.cleanup.selected) != 0 {
		t.Errorf("selection survived apply: %#v", m.state.cleanup.selected)
	}
	if m.state.cleanup.err == "" {
		t.Error("apply error was not recorded")
	}
	if _, ok := tuiNotice(m.state, "state save failed"); !ok {
		t.Errorf("notices = %#v", m.state.notices)
	}

	safe, _ := cleanupItemByBranch(m.state, "feat/safe")
	if safe.Item.Result != ops.CleanupResultRemoved {
		t.Errorf("safe result = %q", safe.Item.Result)
	}
	branch, _ := cleanupItemByBranch(m.state, "feat/merged")
	if branch.Item.Result != ops.CleanupResultFailed || !strings.Contains(branch.Item.Error, "branch moved") {
		t.Errorf("failed item = %#v", branch.Item)
	}
	// Unselected items keep their preview state and remain in the list.
	blocked, _ := cleanupItemByBranch(m.state, "main")
	if blocked.Item.Result != "" {
		t.Errorf("unselected item gained result %q", blocked.Item.Result)
	}

	info, ok := tuiNotice(m.state, "1 removed, 1 failed, 0 kept")
	if !ok {
		t.Fatalf("notices = %#v", m.state.notices)
	}
	if !info.info || !strings.Contains(info.text, "1 removed, 1 failed, 0 kept") || !strings.Contains(info.text, "retained") {
		t.Errorf("apply notice = %#v", info)
	}
}

func TestCleanupKeptResultAndRetainedBranch(t *testing.T) {
	preview := cleanupTestReport()
	applied := ops.CleanupReport{
		Applied: true,
		Projects: []ops.CleanupProject{{
			Project: "rexws",
			Items: []ops.CleanupItem{{
				Project: "rexws", Kind: ops.CleanupKindWorktree, Path: "/wt/safe", Branch: "feat/safe", Head: "aaa",
				Action: ops.CleanupActionRemoveWorktreeAndBranch,
				Result: ops.CleanupResultKept,
				Error:  "worktree changed after planning; kept",
			}},
		}},
	}
	merged := mergeCleanupResults(preview, applied)
	var safe ops.CleanupItem
	for _, project := range merged.Projects {
		for _, item := range project.Items {
			if item.Branch == "feat/safe" {
				safe = item
			}
		}
	}
	if safe.Result != ops.CleanupResultKept || !strings.Contains(safe.Error, "changed after planning") {
		t.Errorf("kept item = %#v", safe)
	}
	// A kept item did not delete its branch.
	retained := cleanupRetainedBranches(merged)
	found := false
	for _, branch := range retained {
		if branch == "rexws/feat/safe" {
			found = true
		}
	}
	if !found {
		t.Errorf("retained branches = %v", retained)
	}
}

func TestCleanupApplyWithoutConfirmDoesNotMutate(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	// Enter opens the confirm and emits no mutation; only y inside the modal
	// does.
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.ApplyCleanup != nil {
		t.Fatal("Enter on the list applied without confirmation")
	}
	if s.mode != ModeCleanupConfirm {
		t.Fatalf("mode = %v", s.mode)
	}
	// Esc closes the confirm and still emits nothing.
	s, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if effects.ApplyCleanup != nil || s.mode != ModeCleanup {
		t.Errorf("esc emitted %#v mode %v", effects, s.mode)
	}
}

func TestCleanupKeysWhileLoading(t *testing.T) {
	s := cleanupTestState(t)
	s.cleanup.loading = true
	for _, msg := range []tea.KeyMsg{runeKey('a'), runeKey('f'), runeKey('v'), tea.KeyMsg{Type: tea.KeySpace}, tea.KeyMsg{Type: tea.KeyEnter}, runeKey('r')} {
		next, effects := Reduce(s, msg)
		if effects != (Effects{}) || next.mode != ModeCleanup {
			t.Errorf("key %v while loading = mode %v effects %#v", msg, next.mode, effects)
		}
	}
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != ModeNormal || s.cleanup != nil {
		t.Errorf("esc while loading = mode %v cleanup %#v", s.mode, s.cleanup)
	}
}

func TestCleanupStaleScanIgnored(t *testing.T) {
	state := cleanupTestState(t)
	state.cleanup = &cleanupView{loading: true, token: 2, selected: map[cleanupItemKey]bool{}}
	state.mode = ModeCleanup
	state = state.rebuildCleanup()

	m := NewModel()
	defer m.Close()
	m.state = state
	m.handleCleanupPlanned(cleanupPlannedMsg{token: 1, report: cleanupTestReport()})
	if len(m.state.cleanup.items) != 0 || !m.state.cleanup.loading {
		t.Errorf("stale scan was applied: %#v", m.state.cleanup)
	}
	m.handleCleanupPlanned(cleanupPlannedMsg{token: 2, report: cleanupTestReport()})
	if m.state.cleanup.loading || len(m.state.cleanup.items) != 6 {
		t.Errorf("current scan was dropped: loading %v items %d", m.state.cleanup.loading, len(m.state.cleanup.items))
	}
}

func TestCleanupDetailNavigation(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('v'))
	if s.mode != ModeCleanupDetail {
		t.Fatalf("mode = %v", s.mode)
	}
	before := s.cleanup.cursor
	s, _ = Reduce(s, runeKey('j'))
	if s.cleanup.cursor == before {
		t.Error("j did not move the detail selection")
	}
	s, _ = Reduce(s, runeKey('v'))
	if s.mode != ModeCleanup {
		t.Errorf("v did not close detail: %v", s.mode)
	}
}

func cleanupRenderState(t *testing.T, width, height int) State {
	t.Helper()
	s := cleanupTestState(t)
	s.width, s.height = width, height
	return s
}

func TestRenderCleanupList(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 18}} {
		s := cleanupRenderState(t, size.width, size.height)
		out := Render(s)
		assertScreen(t, out, size.width, size.height)
		for _, want := range []string{"Cleanup", "SAFE (2)", "NEEDS REVIEW (2)", "BLOCKED (2)", "force off", "uncommitted"} {
			if !strings.Contains(out, want) {
				t.Errorf("%dx%d cleanup missing %q:\n%s", size.width, size.height, want, out)
			}
		}
		if !strings.Contains(out, "[Enter] review") || !strings.Contains(out, "[Esc] back") {
			t.Errorf("%dx%d footer missing: %q", size.width, size.height, strings.Split(out, "\n")[size.height-1])
		}
	}
}

func TestRenderCleanupDetailAndLoss(t *testing.T) {
	s := cleanupRenderState(t, 80, 24)
	s = cleanupCursorTo(s, 2) // dirty review item
	s.mode = ModeCleanupDetail
	out := Render(s)
	assertScreen(t, out, 80, 24)
	for _, want := range []string{"Cleanup detail", "rexws/feat/dirty", "Classification: dirty", "Action: remove-worktree-and-branch", "Requires force: yes", "Reason:", "Loss:", "a.txt", "b.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCleanupConfirm(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 18}} {
		s := cleanupRenderState(t, size.width, size.height)
		s, _ = Reduce(s, runeKey('a'))
		s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
		out := Render(s)
		assertScreen(t, out, size.width, size.height)
		for _, want := range []string{"Apply cleanup to 2 item(s)?", "Will remove: 1 worktree(s), delete 2 branch(es)", "Kept: 4 item(s); 4 branch(es) retained", "Retained branches:", "[y] apply"} {
			if !strings.Contains(out, want) {
				t.Errorf("%dx%d confirm missing %q:\n%s", size.width, size.height, want, out)
			}
		}
		if size.width == 60 {
			// The body clips at the minimum size; the marker must say so instead
			// of dropping the tail silently.
			marker := currentGlyphs().ellipsis + " "
			if !strings.Contains(out, marker) || !strings.Contains(out, "more line(s) not shown") {
				t.Errorf("%dx%d confirm missing truncation marker:\n%s", size.width, size.height, out)
			}
		}
	}
}

func TestRenderCleanupConfirmHeadlineWarningsAndOrphansAt60(t *testing.T) {
	s := cleanupRenderState(t, 60, 18)
	s.cleanup.report.Orphans.Items = make([]ops.CleanupOrphan, 3)
	s = s.rebuildCleanup()
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	out := Render(s)
	assertScreen(t, out, 60, 18)
	// Warnings, errors, and the orphan count join the headline so the 60-column
	// body cut cannot hide them. The exact counts and retained branches stay
	// visible ahead of the explanatory prose.
	for _, want := range []string{"Warnings/errors: 1", "orphan scan incomplete", "Orphans: 3", "rexws cleanup --fix", "Kept:", "Retained branches:"} {
		if !strings.Contains(out, want) {
			t.Errorf("60x18 confirm headline missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCleanupConfirmLongWarningAt60(t *testing.T) {
	s := cleanupRenderState(t, 60, 18)
	s.cleanup.report.Warnings[0] = "orphan scan incomplete: " + strings.Repeat("a long warning ", 12)
	s = s.rebuildCleanup()
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	out := Render(s)
	assertScreen(t, out, 60, 18)
	// A long warning is clamped to one line so it cannot push the exact counts
	// past the cut.
	for _, want := range []string{"Warnings/errors: 1", "Kept:", "Retained branches:", currentGlyphs().ellipsis, "more line(s) not shown"} {
		if !strings.Contains(out, want) {
			t.Errorf("60x18 long-warning confirm missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCleanupConfirmForceItems(t *testing.T) {
	s := cleanupRenderState(t, 100, 30)
	s, _ = Reduce(s, runeKey('f'))
	s = cleanupCursorTo(s, 2)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	s = cleanupCursorTo(s, 3)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	out := Render(s)
	assertScreen(t, out, 100, 30)
	for _, want := range []string{"Apply cleanup to 2 item(s)?", "Force: on - 2 force item(s)", "2 changed path(s)", "1 unmerged", "commit(s)", "Force-required items", "2 changed/untracked/ignored paths"} {
		if !strings.Contains(out, want) {
			t.Errorf("force confirm missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCleanupAppliedResults(t *testing.T) {
	s := cleanupRenderState(t, 80, 24)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	report := s.cleanup.confirm.report
	report.Applied = true
	report.Projects[0].Items[0].Result = ops.CleanupResultRemoved
	report.Projects[0].Items[1].Result = ops.CleanupResultFailed
	report.Projects[0].Items[1].Error = "branch moved; kept"
	report.Totals = ops.CleanupTotals{Removed: 1, Failed: 1, Kept: 0}
	s.cleanup.applied = true
	s.cleanup.report = mergeCleanupResults(s.cleanup.report, report)
	s.cleanup.selected = map[cleanupItemKey]bool{}
	s.cleanup.confirm = nil
	s.mode = ModeCleanup
	s = s.rebuildCleanup()

	out := Render(s)
	assertScreen(t, out, 80, 24)
	for _, want := range []string{"Cleanup applied", "1 removed", "1 failed", "0 kept", "branch moved; kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("applied cleanup missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCleanupStates(t *testing.T) {
	s := renderState(t, 80, 24)
	s.cleanup = &cleanupView{loading: true}
	s.mode = ModeCleanup
	if out := Render(s); !strings.Contains(out, "Scanning cleanup") {
		t.Errorf("loading = %q", out)
	}

	s.cleanup = &cleanupView{err: "cannot verify live Rex sessions: rex: not found"}
	if out := Render(s); !strings.Contains(out, "Cleanup unavailable") || !strings.Contains(out, "[r] retry") {
		t.Errorf("error = %q", out)
	}

	s.cleanup = &cleanupView{}
	if out := Render(s); !strings.Contains(out, "No cleanup candidates") {
		t.Errorf("empty = %q", out)
	}
}

func TestRenderCleanupHelpAndFooterEntry(t *testing.T) {
	s := renderState(t, 120, 24)
	out := Render(s)
	footer := strings.Split(out, "\n")[len(strings.Split(out, "\n"))-1]
	if !strings.Contains(footer, "[c] cleanup") {
		t.Errorf("footer missing cleanup key: %q", footer)
	}
	s.mode = ModeHelp
	if out := Render(s); !strings.Contains(out, "status/new/remove/refresh/cleanup") {
		t.Errorf("help missing cleanup key:\n%s", out)
	}
}

// A held or double-tapped Enter opens the review and must not apply it before
// the modal paints. y is the only apply key.
func TestCleanupDoubleEnterNeverApplies(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.ApplyCleanup != nil || s.mode != ModeCleanupConfirm {
		t.Fatalf("first Enter = mode %v effects %#v", s.mode, effects)
	}
	s, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.ApplyCleanup != nil || s.mode != ModeCleanupConfirm {
		t.Errorf("second Enter applied the review: mode %v effects %#v", s.mode, effects)
	}
	s, effects = Reduce(s, runeKey('y'))
	if effects.ApplyCleanup == nil || s.mode != ModeCleanup {
		t.Errorf("y did not apply: mode %v effects %#v", s.mode, effects)
	}

	// The detail screen's Enter opens the same review with the same rule.
	s = cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s.mode = ModeCleanupDetail
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if s.mode != ModeCleanupConfirm {
		t.Fatalf("detail Enter = mode %v", s.mode)
	}
	s, effects = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.ApplyCleanup != nil {
		t.Error("second Enter from detail applied the review")
	}
}

func TestRenderCleanupConfirmForceSummaryAt60(t *testing.T) {
	s := cleanupRenderState(t, 60, 18)
	s, _ = Reduce(s, runeKey('f'))
	s = cleanupCursorTo(s, 2)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	s = cleanupCursorTo(s, 3)
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	out := Render(s)
	assertScreen(t, out, 60, 18)
	for _, want := range []string{"Force: on - 2 force item(s)", "2 changed path(s)", "unmerged commit(s)", "Kept:", "Retained branches:"} {
		if !strings.Contains(out, want) {
			t.Errorf("60x18 force summary missing %q:\n%s", want, out)
		}
	}
}

func TestCleanupRefusedApplyKeepsPreview(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	selected := len(s.cleanup.selected)
	if selected != 2 {
		t.Fatalf("selected = %d", selected)
	}
	s.cleanup.applying = true
	s.mode = ModeCleanup

	m := NewModel()
	defer m.Close()
	m.state = s
	m.handleCleanupApplied(cleanupAppliedMsg{
		report: cleanupTestReport(), // Applied=false, Projects populated
		err:    errors.New("cannot verify live Rex sessions before applying"),
	})

	if m.state.cleanup.applied {
		t.Error("refused apply was reported as applied")
	}
	if len(m.state.cleanup.selected) != selected {
		t.Errorf("selection was cleared by a refused apply: %#v", m.state.cleanup.selected)
	}
	if len(m.state.cleanup.items) != 6 {
		t.Errorf("preview items = %d", len(m.state.cleanup.items))
	}
	if m.state.cleanup.err == "" {
		t.Error("refused apply did not record the error")
	}
	if _, ok := tuiNotice(m.state, "cannot verify live Rex sessions"); !ok {
		t.Errorf("notices = %#v", m.state.notices)
	}
	for _, n := range m.state.notices {
		if n.info && strings.Contains(n.text, "removed") {
			t.Errorf("refused apply added a success notice: %#v", n)
		}
	}
}

func TestCleanupScanTokenNotReusedAfterClose(t *testing.T) {
	s := testState(t)
	s, _ = Reduce(s, runeKey('c'))
	first := s.cleanup.token
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEsc})
	if s.cleanup != nil || s.mode != ModeNormal {
		t.Fatalf("esc during scan = mode %v cleanup %#v", s.mode, s.cleanup)
	}
	s, _ = Reduce(s, runeKey('c'))
	second := s.cleanup.token
	if first == second {
		t.Fatalf("scan token reused after close: %d", first)
	}

	m := NewModel()
	defer m.Close()
	m.state = s
	m.handleCleanupPlanned(cleanupPlannedMsg{token: first, report: cleanupTestReport()})
	if len(m.state.cleanup.items) != 0 || !m.state.cleanup.loading {
		t.Error("a stale scan was accepted after reopen")
	}
	m.handleCleanupPlanned(cleanupPlannedMsg{token: second, report: cleanupTestReport()})
	if len(m.state.cleanup.items) != 6 || m.state.cleanup.loading {
		t.Errorf("current scan = loading %v items %d", m.state.cleanup.loading, len(m.state.cleanup.items))
	}
}

func TestCleanupStatusKeepsForceBeforeCountsAt60(t *testing.T) {
	s := cleanupTestState(t)
	for i := 0; i < 20; i++ {
		item := s.cleanup.items[0]
		item.Key.Branch = "extra/" + string(rune('a'+i%26))
		s.cleanup.items = append(s.cleanup.items, item)
	}
	line := cleanupStatusLine(s.cleanup, 60, currentTheme())
	if !strings.Contains(line, "force off") {
		t.Fatalf("force state was truncated away: %q", line)
	}
	if force, safe := strings.Index(line, "force off"), strings.Index(line, "safe"); force < 0 || force > safe {
		t.Errorf("force is not before group counts: %q", line)
	}
	if !strings.Contains(line, "sel 0") {
		t.Errorf("selection was truncated away: %q", line)
	}
}

func TestCleanupPageReachesFirstAndLastItem(t *testing.T) {
	s := cleanupTestState(t)
	s = moveCleanupSelection(s, 100)
	last, ok := s.cleanupCurrent()
	if !ok || last.Item.Branch != "feat/active" {
		t.Fatalf("PgDn edge = %#v", last.Item)
	}
	s = moveCleanupSelection(s, -100)
	first, ok := s.cleanupCurrent()
	if !ok || first.Item.Branch != "feat/safe" {
		t.Fatalf("PgUp edge = %#v", first.Item)
	}
}

func TestCleanupConfirmSanitizesRetainedBranchNames(t *testing.T) {
	s := cleanupTestState(t)
	for i := range s.cleanup.report.Projects[0].Items {
		if s.cleanup.report.Projects[0].Items[i].Branch == "feat/unmerged" {
			s.cleanup.report.Projects[0].Items[i].Branch = "feat/\x1b[31munmerged"
		}
	}
	s = s.rebuildCleanup()
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape reached the confirm:\n%q", out)
	}
	if !strings.Contains(out, "Retained branches:") || !strings.Contains(out, "?") {
		t.Errorf("sanitized retained branch line missing:\n%s", out)
	}
}

func TestCleanupReducersCopyOnWrite(t *testing.T) {
	s := cleanupTestState(t)
	before := s
	after, _ := Reduce(s, tea.KeyMsg{Type: tea.KeySpace})
	if len(before.cleanup.selected) != 0 {
		t.Errorf("selection mutated a shared view: %#v", before.cleanup.selected)
	}
	if len(after.cleanup.selected) != 1 {
		t.Errorf("selection was not applied: %#v", after.cleanup.selected)
	}

	cursorBefore := s.cleanup.cursor
	moved := moveCleanupSelection(s, 1)
	if s.cleanup.cursor != cursorBefore {
		t.Errorf("cursor mutated a shared view: %d -> %d", cursorBefore, s.cleanup.cursor)
	}
	if moved.cleanup.cursor == cursorBefore {
		t.Error("move did not advance the copy")
	}

	selectable, _ := Reduce(cleanupTestState(t), tea.KeyMsg{Type: tea.KeySpace})
	confirmedBefore := selectable
	opened, _ := Reduce(selectable, tea.KeyMsg{Type: tea.KeyEnter})
	if confirmedBefore.cleanup.confirm != nil {
		t.Error("opening the confirm mutated a shared view")
	}
	if opened.cleanup.confirm == nil || opened.mode != ModeCleanupConfirm {
		t.Errorf("confirm = %#v mode %v", opened.cleanup.confirm, opened.mode)
	}
}

func TestRenderCleanupDetailShowsLossSummaryAndTruncation(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 18}} {
		s := cleanupRenderState(t, size.width, size.height)
		loss := &ops.CleanupLoss{
			DirtyFiles: []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"},
			Commits: []git.Commit{
				{SHA: "aaa1111", Subject: "one"},
				{SHA: "bbb2222", Subject: "two"},
				{SHA: "ccc3333", Subject: "three"},
				{SHA: "ddd4444", Subject: "four"},
				{SHA: "eee5555", Subject: "five"},
			},
		}
		s.cleanup.report.Projects[0].Items[2].Loss = loss
		s = s.rebuildCleanup()
		s = cleanupCursorTo(s, 2)
		s.mode = ModeCleanupDetail
		out := Render(s)
		assertScreen(t, out, size.width, size.height)
		for _, want := range []string{"Loss:", "5 changed/untracked/ignored path(s)", "unmerged", "commit(s)", "more line(s) not shown"} {
			if !strings.Contains(out, want) {
				t.Errorf("%dx%d detail missing %q:\n%s", size.width, size.height, want, out)
			}
		}
	}
}

// cleanupAppliedStatusState builds the applied-results state used by the
// status-line tests: one removed item, one failed item, and unselected items
// that stay retained.
func cleanupAppliedStatusState(t *testing.T) State {
	t.Helper()
	s := cleanupRenderState(t, 60, 18)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	report := s.cleanup.confirm.report
	report.Applied = true
	report.Projects[0].Items[0].Result = ops.CleanupResultRemoved
	report.Projects[0].Items[1].Result = ops.CleanupResultFailed
	report.Projects[0].Items[1].Error = "branch moved; kept"
	report.Totals = ops.CleanupTotals{Removed: 1, Failed: 1, Kept: 0}
	s.cleanup.applied = true
	s.cleanup.report = mergeCleanupResults(s.cleanup.report, report)
	s.cleanup.selected = map[cleanupItemKey]bool{}
	s.cleanup.confirm = nil
	s.mode = ModeCleanup
	return s.rebuildCleanup()
}

func TestCleanupAppliedStatusOrderAt60(t *testing.T) {
	s := cleanupAppliedStatusState(t)
	line := cleanupStatusLine(s.cleanup, 60, currentTheme())
	prev := -1
	for _, want := range []string{"1 removed", "1 failed", "0 kept", "retained"} {
		at := strings.Index(line, want)
		if at < 0 {
			t.Fatalf("60-column applied status missing %q: %q", want, line)
		}
		if at <= prev {
			t.Errorf("applied status out of order at %q: %q", want, line)
		}
		prev = at
	}
}

func TestCleanupAppliedStatusMarksHardCutAt60(t *testing.T) {
	s := cleanupAppliedStatusState(t)
	// Push the applied line past 60 cells so the status must truncate.
	s.cleanup.report.Orphans.Items = make([]ops.CleanupOrphan, 12)
	line := cleanupStatusLine(s.cleanup, 60, currentTheme())
	if !strings.Contains(line, currentGlyphs().ellipsis) {
		t.Errorf("hard-cut applied status has no ellipsis: %q", line)
	}
	if got := lipgloss.Width(line); got > 60 {
		t.Errorf("applied status = %d cells, want <= 60: %q", got, line)
	}
}

func TestCleanupConfirmIgnoresPastedY(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	pasted := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}, Paste: true}
	s, effects := Reduce(s, pasted)
	if effects.ApplyCleanup != nil || s.mode != ModeCleanupConfirm {
		t.Fatalf("pasted y applied: mode %v effects %#v", s.mode, effects)
	}
	s, effects = Reduce(s, runeKey('y'))
	if effects.ApplyCleanup == nil {
		t.Fatalf("typed y did not apply: %#v", effects)
	}
}

func TestRenderCleanupRefusedApplyKeepsPersistentError(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s.cleanup.applying = true
	s.mode = ModeCleanup

	m := NewModel()
	defer m.Close()
	m.state = s
	m.handleCleanupApplied(cleanupAppliedMsg{
		report: cleanupTestReport(), // Applied=false
		err:    errors.New("cannot verify live Rex sessions before applying"),
	})

	// While the transient banner is visible it owns the message.
	out := Render(m.state)
	assertScreen(t, out, 80, 24)
	if got := strings.Count(out, "cannot verify live Rex sessions before applying"); got != 1 {
		t.Errorf("error appears %d times while the banner is visible:\n%s", got, out)
	}

	// Once the notice expires, the screen keeps a persistent error line.
	m.state = m.state.pruneNotices(time.Now().Add(10 * time.Second))
	out = Render(m.state)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(out, "cannot verify live Rex sessions before applying") {
		t.Errorf("refused apply error disappeared after the banner expired:\n%s", out)
	}
}

func TestCleanupPageSizeReservesPersistentError(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s.cleanup.applying = true
	s.mode = ModeCleanup

	m := NewModel()
	defer m.Close()
	m.state = s
	m.handleCleanupApplied(cleanupAppliedMsg{
		report: cleanupTestReport(), // Applied=false
		err:    errors.New("cannot verify live Rex sessions before applying"),
	})
	m.state = m.state.pruneNotices(time.Now().Add(10 * time.Second))
	m.state.width, m.state.height = 60, 18

	_, hasErrLine := cleanupRefusedError(m.state, time.Now())
	fixed := 1
	if hasErrLine {
		fixed++
	}
	visible := m.state.bodyHeight() - fixed
	page := cleanupPageSize(m.state)
	if page > visible {
		t.Errorf("page size %d exceeds visible list height %d", page, visible)
	}
	if page < 1 {
		t.Errorf("page size = %d", page)
	}
}

func TestCleanupMoveThenToggleKeepsPriorSelection(t *testing.T) {
	s := cleanupTestState(t)
	moved := moveCleanupSelection(s, 1)
	if moved.cleanup.cursor == s.cleanup.cursor {
		t.Fatal("move did not advance the cursor")
	}
	// The shallow copy shares the selection map; a toggle must clone before
	// writing so neither earlier state sees the change.
	toggled, _ := Reduce(moved, tea.KeyMsg{Type: tea.KeySpace})
	if len(s.cleanup.selected) != 0 || len(moved.cleanup.selected) != 0 {
		t.Errorf("toggle mutated a shared selection map: before=%d moved=%d", len(s.cleanup.selected), len(moved.cleanup.selected))
	}
	if len(toggled.cleanup.selected) != 1 {
		t.Errorf("selection was not applied to the copy: %#v", toggled.cleanup.selected)
	}
}

func TestCleanupFooterStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupView)
		want   []string
		absent []string
	}{
		{
			name:   "wide",
			mutate: func(*cleanupView) {},
			want:   []string{"[r] rescan", "[v] detail", "[Space] pick"},
		},
		{
			name:   "loading",
			mutate: func(v *cleanupView) { v.loading = true },
			want:   []string{"[Esc] back"},
			absent: []string{"[r] rescan", "[v] detail"},
		},
		{
			name:   "error",
			mutate: func(v *cleanupView) { v.err = "boom"; v.items = nil; v.rows = nil },
			want:   []string{"[r] retry", "[Esc] back"},
		},
		{
			name:   "applying",
			mutate: func(v *cleanupView) { v.applying = true },
			want:   []string{"applying"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := cleanupRenderState(t, 80, 24)
			tc.mutate(s.cleanup)
			line, ok := modalFooter(s, 80, currentTheme())
			if !ok {
				t.Fatalf("no footer for %s", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(line, want) {
					t.Errorf("footer missing %q: %q", want, line)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(line, absent) {
					t.Errorf("footer has %q: %q", absent, line)
				}
			}
		})
	}
}

func TestCleanupApplyDeduplicatesWarnings(t *testing.T) {
	s := cleanupTestState(t)
	s, _ = Reduce(s, runeKey('a'))
	s, _ = Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	confirmed := s.cleanup.confirm.report
	confirmed.Applied = true
	// The preview warning is re-reported and a new warning is added. The merge
	// must keep one copy of the shared warning.
	confirmed.Warnings = []string{"orphan scan incomplete", "branch moved during apply"}
	confirmed.Errors = []string{"same error"}
	s.cleanup.report.Errors = []string{"same error"}
	confirmed.Totals = ops.CleanupTotals{Removed: 2}

	m := NewModel()
	defer m.Close()
	m.state = s
	m.handleCleanupApplied(cleanupAppliedMsg{report: confirmed})

	warnings := map[string]int{}
	for _, warning := range m.state.cleanup.report.Warnings {
		warnings[warning]++
	}
	if warnings["orphan scan incomplete"] != 1 {
		t.Errorf("preview warning was duplicated: %v", m.state.cleanup.report.Warnings)
	}
	if warnings["branch moved during apply"] != 1 {
		t.Errorf("applied warning was lost or duplicated: %v", m.state.cleanup.report.Warnings)
	}
	errs := map[string]int{}
	for _, text := range m.state.cleanup.report.Errors {
		errs[text]++
	}
	if errs["same error"] != 1 {
		t.Errorf("shared error was duplicated: %v", m.state.cleanup.report.Errors)
	}
	if _, ok := tuiNotice(m.state, "2 removed"); !ok {
		t.Errorf("success notice missing: %#v", m.state.notices)
	}
}

func TestRenderCleanupDetailLossBeforeError(t *testing.T) {
	s := cleanupRenderState(t, 60, 18)
	s.cleanup.report.Projects[0].Items[2].Result = ops.CleanupResultFailed
	s.cleanup.report.Projects[0].Items[2].Error = "worktree changed after planning; kept"
	s = s.rebuildCleanup()
	s = cleanupCursorTo(s, 2)
	s.mode = ModeCleanupDetail
	out := Render(s)
	assertScreen(t, out, 60, 18)
	lossAt := strings.Index(out, "Loss:")
	errAt := strings.Index(out, "Error:")
	if lossAt < 0 || errAt < 0 || lossAt > errAt {
		t.Errorf("Loss summary is not before Error (loss=%d error=%d):\n%s", lossAt, errAt, out)
	}
	if !strings.Contains(out, "2 changed/untracked/ignored path(s)") {
		t.Errorf("applied-failure loss summary was clipped:\n%s", out)
	}
}
