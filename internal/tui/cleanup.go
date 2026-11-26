package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/ops"
)

// cleanupView is the cleanup screen's state. It is nil outside the screen.
// The report is the last preview; after an apply it carries per-item results
// until the next scan.
type cleanupView struct {
	loading  bool
	applying bool
	applied  bool
	err      string
	token    int
	report   ops.CleanupReport
	items    []cleanupItem
	rows     []cleanupRow
	cursor   int
	selected map[cleanupItemKey]bool
	force    bool
	confirm  *cleanupConfirm
}

// cleanupGroup is the risk bucket a candidate row belongs to.
type cleanupGroup int

const (
	cleanupGroupSafe cleanupGroup = iota
	cleanupGroupReview
	cleanupGroupBlocked
)

// cleanupRowKind distinguishes a non-selectable group header from a candidate.
type cleanupRowKind int

const (
	cleanupRowGroup cleanupRowKind = iota
	cleanupRowItem
)

// cleanupItemKey is the stable identity of one candidate across rescans and an
// apply result. Project-scoped branches and worktree paths are unique in a
// cleanup report.
type cleanupItemKey struct {
	Project string
	Kind    string
	Path    string
	Branch  string
	Head    string
}

// cleanupItem is one report item plus its derived group.
type cleanupItem struct {
	Key   cleanupItemKey
	Group cleanupGroup
	Item  ops.CleanupItem
}

// cleanupRow is one rendered line: a group header or a candidate.
type cleanupRow struct {
	Kind  cleanupRowKind
	Group cleanupGroup
	Item  int // index into cleanupView.items for cleanupRowItem
}

// cleanupConfirm is a snapshot of the selection shown by the confirm modal.
// The apply uses this exact report, so the confirmed actions cannot change
// between opening the modal and pressing y.
type cleanupConfirm struct {
	report           ops.CleanupReport
	items            int
	worktrees        int
	branches         int
	prunes           int
	forceItems       []cleanupItem
	retainedItems    int
	retainedBranches []string
	warnings         []string
}

func cleanupKeyFor(item ops.CleanupItem) cleanupItemKey {
	return cleanupItemKey{
		Project: item.Project,
		Kind:    item.Kind,
		Path:    item.Path,
		Branch:  item.Branch,
		Head:    item.Head,
	}
}

// cleanupGroupFor maps CleanupItem fields onto the three review buckets. Safe is
// the no-force set; a dirty or unmerged item needs review and force; everything
// else is blocked because cleanup cannot act on it.
func cleanupGroupFor(item ops.CleanupItem) cleanupGroup {
	switch {
	case item.Safe:
		return cleanupGroupSafe
	case item.RequiresForce:
		return cleanupGroupReview
	default:
		return cleanupGroupBlocked
	}
}

func cleanupGroupLabel(group cleanupGroup) string {
	switch group {
	case cleanupGroupSafe:
		return "SAFE"
	case cleanupGroupReview:
		return "NEEDS REVIEW"
	default:
		return "BLOCKED"
	}
}

// cleanupItemsFromReport flattens a report into grouped order while preserving
// each project's stable item order inside a group.
func cleanupItemsFromReport(report ops.CleanupReport) []cleanupItem {
	var groups [3][]cleanupItem
	for _, project := range report.Projects {
		for _, item := range project.Items {
			group := cleanupGroupFor(item)
			groups[group] = append(groups[group], cleanupItem{
				Key:   cleanupKeyFor(item),
				Group: group,
				Item:  item,
			})
		}
	}
	items := make([]cleanupItem, 0, len(groups[0])+len(groups[1])+len(groups[2]))
	for _, group := range groups {
		items = append(items, group...)
	}
	return items
}

// buildCleanupRows inserts a group header before each non-empty group.
func buildCleanupRows(items []cleanupItem) []cleanupRow {
	rows := make([]cleanupRow, 0, len(items)+3)
	current := cleanupGroup(-1)
	for index, item := range items {
		if item.Group != current {
			current = item.Group
			rows = append(rows, cleanupRow{Kind: cleanupRowGroup, Group: item.Group})
		}
		rows = append(rows, cleanupRow{Kind: cleanupRowItem, Group: item.Group, Item: index})
	}
	return rows
}

func cleanupGroupCounts(items []cleanupItem) (safe, review, blocked int) {
	for _, item := range items {
		switch item.Group {
		case cleanupGroupSafe:
			safe++
		case cleanupGroupReview:
			review++
		default:
			blocked++
		}
	}
	return safe, review, blocked
}

// clone copies the mutable parts of a cleanup view. State is a value type and
// the reducer is pure, so every mutating helper works on a copy instead of
// changing a view another State value may still reference.
func (v *cleanupView) clone() *cleanupView {
	if v == nil {
		return nil
	}
	copied := *v
	copied.items = append([]cleanupItem(nil), v.items...)
	copied.rows = append([]cleanupRow(nil), v.rows...)
	copied.selected = make(map[cleanupItemKey]bool, len(v.selected))
	for key, value := range v.selected {
		copied.selected[key] = value
	}
	return &copied
}

// rebuildCleanup recomputes rows and clamps the cursor after a report or
// selection change.
func (s State) rebuildCleanup() State {
	if s.cleanup == nil {
		return s
	}
	s.cleanup = s.cleanup.clone()
	s.cleanup.items = cleanupItemsFromReport(s.cleanup.report)
	s.cleanup.rows = buildCleanupRows(s.cleanup.items)
	s.cleanup.cursor = clampCleanupCursor(s.cleanup.rows, s.cleanup.cursor)
	return s
}

// clampCleanupCursor keeps the cursor on a candidate, scanning forward and then
// backward. Group headers are never selectable.
func clampCleanupCursor(rows []cleanupRow, cursor int) int {
	if len(rows) == 0 {
		return -1
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	for i := cursor; i < len(rows); i++ {
		if rows[i].Kind == cleanupRowItem {
			return i
		}
	}
	for i := cursor; i >= 0; i-- {
		if rows[i].Kind == cleanupRowItem {
			return i
		}
	}
	return -1
}

func (s State) cleanupCurrent() (cleanupItem, bool) {
	if s.cleanup == nil || s.cleanup.cursor < 0 || s.cleanup.cursor >= len(s.cleanup.rows) {
		return cleanupItem{}, false
	}
	row := s.cleanup.rows[s.cleanup.cursor]
	if row.Kind != cleanupRowItem || row.Item < 0 || row.Item >= len(s.cleanup.items) {
		return cleanupItem{}, false
	}
	return s.cleanup.items[row.Item], true
}

// moveCleanupSelection moves by delta candidates, skipping group headers and
// never wrapping. A page move that runs past an edge clamps to that edge, so
// PgDn/PgUp can always reach the first and last candidate.
func moveCleanupSelection(s State, delta int) State {
	if s.cleanup == nil || len(s.cleanup.rows) == 0 {
		return s
	}
	direction := sign(delta)
	if direction == 0 {
		return s
	}
	cursor := s.cleanup.cursor
	if cursor < 0 {
		if direction > 0 {
			cursor = 0
		} else {
			cursor = len(s.cleanup.rows) - 1
		}
	} else {
		cursor += delta
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(s.cleanup.rows) {
		cursor = len(s.cleanup.rows) - 1
	}
	for i := cursor; i >= 0 && i < len(s.cleanup.rows); i += direction {
		if s.cleanup.rows[i].Kind == cleanupRowItem {
			s.cleanup = s.cleanup.clone()
			s.cleanup.cursor = i
			return s
		}
	}
	// A clamped edge can land on a group header (for example a trailing empty
	// group). Scan the other way so the edge candidate is still reachable.
	for i := cursor; i >= 0 && i < len(s.cleanup.rows); i -= direction {
		if s.cleanup.rows[i].Kind == cleanupRowItem {
			s.cleanup = s.cleanup.clone()
			s.cleanup.cursor = i
			return s
		}
	}
	return s
}

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

// cleanupSelectable reports whether Space may select the item. Safe items are
// always selectable; review items require force; blocked items never are.
func (v *cleanupView) cleanupSelectable(item cleanupItem) bool {
	if v == nil || v.applied || v.applying || v.loading {
		return false
	}
	if item.Item.Action == ops.CleanupActionNone {
		return false
	}
	switch item.Group {
	case cleanupGroupSafe:
		return true
	case cleanupGroupReview:
		return v.force
	default:
		return false
	}
}

func (s State) toggleCleanupSelection() State {
	item, ok := s.cleanupCurrent()
	if !ok || !s.cleanup.cleanupSelectable(item) {
		return s
	}
	s.cleanup = s.cleanup.clone()
	if s.cleanup.selected[item.Key] {
		delete(s.cleanup.selected, item.Key)
	} else {
		s.cleanup.selected[item.Key] = true
	}
	return s
}

// toggleCleanupSelectAllSafe selects every safe candidate; when all safe
// candidates are already selected it clears them. It never changes review or
// blocked rows.
func (s State) toggleCleanupSelectAllSafe() State {
	if s.cleanup == nil || s.cleanup.applied || s.cleanup.applying || s.cleanup.loading {
		return s
	}
	var safe []cleanupItem
	allSelected := true
	for _, item := range s.cleanup.items {
		if item.Group != cleanupGroupSafe || !s.cleanup.cleanupSelectable(item) {
			continue
		}
		safe = append(safe, item)
		if !s.cleanup.selected[item.Key] {
			allSelected = false
		}
	}
	if len(safe) == 0 {
		return s
	}
	s.cleanup = s.cleanup.clone()
	for _, item := range safe {
		if allSelected {
			delete(s.cleanup.selected, item.Key)
		} else {
			s.cleanup.selected[item.Key] = true
		}
	}
	return s
}

// toggleCleanupForce enables or disables review-item selection. Turning force
// off deselects every item that needs it, so an accidental apply cannot mutate
// a dirty worktree or unmerged branch.
func (s State) toggleCleanupForce() State {
	if s.cleanup == nil || s.cleanup.applied || s.cleanup.applying || s.cleanup.loading {
		return s
	}
	s.cleanup = s.cleanup.clone()
	s.cleanup.force = !s.cleanup.force
	if !s.cleanup.force {
		for _, item := range s.cleanup.items {
			if item.Item.RequiresForce {
				delete(s.cleanup.selected, item.Key)
			}
		}
	}
	return s
}

// selectedCleanupItems returns the selected candidates that may be applied with
// the current force setting.
func (v *cleanupView) selectedCleanupItems() []cleanupItem {
	if v == nil {
		return nil
	}
	var out []cleanupItem
	for _, item := range v.items {
		if !v.selected[item.Key] {
			continue
		}
		if item.Group == cleanupGroupBlocked || item.Item.Action == ops.CleanupActionNone {
			continue
		}
		if item.Item.RequiresForce && !v.force {
			continue
		}
		out = append(out, item)
	}
	return out
}

// selectedCleanupReport builds the report ApplyCleanup receives. It contains
// only the selected, actionable items and carries the current force flag.
func (v *cleanupView) selectedCleanupReport() ops.CleanupReport {
	report := v.report
	report.Projects = make([]ops.CleanupProject, 0, len(v.report.Projects))
	report.Applied = false
	report.Force = v.force
	for _, project := range v.report.Projects {
		items := make([]ops.CleanupItem, 0, len(project.Items))
		for _, item := range project.Items {
			key := cleanupKeyFor(item)
			if !v.selected[key] {
				continue
			}
			if cleanupGroupFor(item) == cleanupGroupBlocked || item.Action == ops.CleanupActionNone {
				continue
			}
			if item.RequiresForce && !v.force {
				continue
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			continue
		}
		copied := project
		copied.Items = items
		report.Projects = append(report.Projects, copied)
	}
	return report
}

// openCleanupConfirm snapshots the selection. With no applicable selection it
// is a no-op.
func (s State) openCleanupConfirm() State {
	if s.cleanup == nil || s.cleanup.applied || s.cleanup.applying || s.cleanup.loading {
		return s
	}
	selected := s.cleanup.selectedCleanupItems()
	if len(selected) == 0 {
		return s
	}
	confirm := s.cleanup.buildConfirm(selected)
	s.cleanup = s.cleanup.clone()
	s.cleanup.confirm = confirm
	s.mode = ModeCleanupConfirm
	return s
}

// buildConfirm summarizes exactly what the modal and the apply will do.
func (v *cleanupView) buildConfirm(selected []cleanupItem) *cleanupConfirm {
	confirm := &cleanupConfirm{
		report:     v.selectedCleanupReport(),
		items:      len(selected),
		warnings:   append(append([]string(nil), v.report.Warnings...), v.report.Errors...),
		forceItems: nil,
	}
	chosen := make(map[cleanupItemKey]bool, len(selected))
	deletedBranches := make(map[string]bool)
	for _, item := range selected {
		chosen[item.Key] = true
		switch item.Item.Action {
		case ops.CleanupActionPrune:
			confirm.prunes++
		case ops.CleanupActionRemoveWorktree:
			confirm.worktrees++
		case ops.CleanupActionDeleteBranch:
			confirm.branches++
		case ops.CleanupActionRemoveWorktreeAndBranch:
			confirm.worktrees++
			confirm.branches++
		}
		if item.Item.RequiresForce {
			confirm.forceItems = append(confirm.forceItems, item)
		}
		if item.Item.Branch != "" && cleanupActionDeletesBranch(item.Item.Action) {
			deletedBranches[item.Item.Project+"/"+item.Item.Branch] = true
		}
	}
	retained := make(map[string]bool)
	for _, item := range v.items {
		confirm.retainedItems++ // provisional: every item not chosen is kept
		if chosen[item.Key] {
			confirm.retainedItems--
		}
		if item.Item.Branch == "" {
			continue
		}
		label := item.Item.Project + "/" + item.Item.Branch
		if deletedBranches[label] {
			continue
		}
		retained[label] = true
	}
	confirm.retainedBranches = sortedKeys(retained)
	return confirm
}

func cleanupActionDeletesBranch(action ops.CleanupAction) bool {
	return action == ops.CleanupActionDeleteBranch || action == ops.CleanupActionRemoveWorktreeAndBranch
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// mergeCleanupResults keeps the full preview and stamps each selected item's
// result, so the screen can report removed, failed, and kept in place.
func mergeCleanupResults(preview, applied ops.CleanupReport) ops.CleanupReport {
	results := make(map[cleanupItemKey]ops.CleanupResult)
	failures := make(map[cleanupItemKey]string)
	for _, project := range applied.Projects {
		for _, item := range project.Items {
			key := cleanupKeyFor(item)
			results[key] = item.Result
			failures[key] = item.Error
		}
	}
	merged := preview
	merged.Projects = make([]ops.CleanupProject, 0, len(preview.Projects))
	for _, project := range preview.Projects {
		copied := project
		copied.Items = append([]ops.CleanupItem(nil), project.Items...)
		for i := range copied.Items {
			key := cleanupKeyFor(copied.Items[i])
			if result, ok := results[key]; ok {
				copied.Items[i].Result = result
				copied.Items[i].Error = failures[key]
			}
		}
		merged.Projects = append(merged.Projects, copied)
	}
	merged.Applied = applied.Applied
	merged.Force = applied.Force
	merged.Totals = applied.Totals
	merged.Warnings = mergeStrings(preview.Warnings, applied.Warnings)
	merged.Errors = mergeStrings(preview.Errors, applied.Errors)
	return merged
}

func mergeStrings(left, right []string) []string {
	seen := make(map[string]bool, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// cleanupRetainedBranches counts the branches left after applying the merged
// report. A branch is retained unless its selected item removed it.
func cleanupRetainedBranches(report ops.CleanupReport) []string {
	retained := make(map[string]bool)
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.Branch == "" {
				continue
			}
			if item.Result == ops.CleanupResultRemoved && cleanupActionDeletesBranch(item.Action) {
				continue
			}
			retained[item.Project+"/"+item.Branch] = true
		}
	}
	return sortedKeys(retained)
}

// cleanupActionLine is the confirm modal's exact per-item action wording.
func cleanupActionLine(item ops.CleanupItem) string {
	switch item.Action {
	case ops.CleanupActionPrune:
		return "prune missing worktree " + cleanupItemLabel(item)
	case ops.CleanupActionRemoveWorktree:
		return "remove worktree " + cleanupItemLabel(item)
	case ops.CleanupActionDeleteBranch:
		return "delete branch " + cleanupTargetLabel(item)
	case ops.CleanupActionRemoveWorktreeAndBranch:
		line := "remove worktree " + cleanupItemLabel(item)
		if item.Branch != "" {
			line += " and delete branch " + item.Project + "/" + item.Branch
		}
		return line
	default:
		return "no action " + cleanupTargetLabel(item)
	}
}

// cleanupItemLabel names the filesystem object for worktree actions and the
// project-scoped branch for branch actions.
func cleanupItemLabel(item ops.CleanupItem) string {
	if item.Kind == ops.CleanupKindWorktree && item.Path != "" {
		return item.Path
	}
	return cleanupTargetLabel(item)
}

// cleanupTargetLabel names an item the way the TUI and CLI do: project/branch
// when a branch exists, otherwise the worktree path.
func cleanupTargetLabel(item ops.CleanupItem) string {
	if item.Branch != "" {
		return item.Project + "/" + item.Branch
	}
	if item.Path != "" {
		return item.Path
	}
	if item.Head != "" {
		return item.Project + "/" + item.Head
	}
	return item.Project
}

// cleanupLossSummary describes a forced item's loss without listing every
// path, for a one-line confirm entry.
func cleanupLossSummary(item ops.CleanupItem) string {
	if item.Loss == nil {
		return item.Detail
	}
	var parts []string
	if count := len(item.Loss.DirtyFiles); count > 0 {
		word := "paths"
		if count == 1 {
			word = "path"
		}
		parts = append(parts, fmt.Sprintf("%d changed/untracked/ignored %s", count, word))
	}
	if count := len(item.Loss.Commits); count > 0 {
		word := "commits"
		if count == 1 {
			word = "commit"
		}
		parts = append(parts, fmt.Sprintf("%d unmerged %s", count, word))
	}
	if len(parts) == 0 {
		return item.Detail
	}
	return strings.Join(parts, "; ")
}
