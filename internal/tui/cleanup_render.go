package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
)

// renderCleanup draws the cleanup list: a fixed status line plus a scrolling
// group-and-candidate list. It is a screen, not a modal, so the dashboard
// header and footer frame stay in place.
func renderCleanup(s State, width, height int, t theme) string {
	view := s.cleanup
	if view == nil {
		return centerBlock([]string{"Cleanup unavailable."}, width, height)
	}
	if view.err != "" && len(view.items) == 0 {
		return renderCleanupError(view.err, width, height, t)
	}
	if len(view.items) == 0 {
		if view.loading {
			return renderCleanupLoading(width, height, t)
		}
		return renderCleanupEmpty(width, height)
	}

	status := cleanupStatusLine(view, width, t)
	rows := renderCleanupRows(view, width, t)
	bodyHeight := max(0, height-1)
	if bodyHeight == 0 {
		return fitWidth(status, width)
	}

	top := 0
	if view.cursor >= top+bodyHeight {
		top = view.cursor - bodyHeight + 1
	}
	if maxTop := max(0, len(rows)-bodyHeight); top > maxTop {
		top = maxTop
	}
	if top < 0 {
		top = 0
	}
	pane := viewport.New(width, bodyHeight)
	pane.SetContent(strings.Join(rows, "\n"))
	pane.SetYOffset(top)
	return strings.Join([]string{fitWidth(status, width), pane.View()}, "\n")
}

func renderCleanupLoading(width, height int, t theme) string {
	lines := []string{"Scanning cleanup" + t.glyphs.ellipsis}
	if width >= 80 {
		lines = append(lines, t.muted().Render("Reading worktrees, branches, Rex sessions, and orphans"))
	}
	return centerBlock(lines, width, height)
}

func renderCleanupError(text string, width, height int, t theme) string {
	prose := max(1, min(width-2, 72))
	lines := []string{t.error().Render("Cleanup unavailable"), ""}
	for _, line := range wrapText(render.Sanitize(text), prose) {
		lines = append(lines, line)
	}
	lines = append(lines, "", "[r] retry  [Esc] back")
	return centerBlock(lines, width, height)
}

func renderCleanupEmpty(width, height int) string {
	if width >= 80 {
		return centerBlock([]string{"No cleanup candidates. Press r to rescan, Esc to go back."}, width, height)
	}
	return centerBlock([]string{"No cleanup candidates.", "Press r to rescan, Esc to go back."}, width, height)
}

// cleanupStatusLine is the fixed summary above the list. At narrow widths the
// preview puts selection and force before the group counts so truncation cannot
// hide the safety controls; applied results put their counts first.
func cleanupStatusLine(view *cleanupView, width int, t theme) string {
	compact := width < 80
	var parts []string
	if view.loading {
		parts = append(parts, "scanning"+t.glyphs.ellipsis)
	}
	force := "force off"
	if view.force {
		force = "force on"
	}
	if view.applied {
		totals := view.report.Totals
		parts = append(parts,
			fmt.Sprintf("%d removed", totals.Removed),
			fmt.Sprintf("%d failed", totals.Failed),
			fmt.Sprintf("%d kept", totals.Kept))
		if retained := cleanupRetainedBranches(view.report); len(retained) > 0 {
			parts = append(parts, fmt.Sprintf("%d retained", len(retained)))
		}
		if !compact {
			parts = append(parts, force)
		}
	} else {
		safe, review, blocked := cleanupGroupCounts(view.items)
		selected := len(view.selectedCleanupItems())
		if compact {
			// Selection and force come before counts so a long count can never
			// truncate away the safety controls.
			parts = append(parts,
				fmt.Sprintf("sel %d", selected),
				force,
				fmt.Sprintf("%d safe", safe),
				fmt.Sprintf("%d review", review),
				fmt.Sprintf("%d blocked", blocked))
		} else {
			parts = append(parts,
				fmt.Sprintf("safe %d", safe),
				fmt.Sprintf("review %d", review),
				fmt.Sprintf("blocked %d", blocked),
				fmt.Sprintf("%d selected", selected),
				force)
		}
	}
	if orphans := len(view.report.Orphans.Items); orphans > 0 {
		parts = append(parts, fmt.Sprintf("%d orphans", orphans))
	}
	if view.err != "" {
		parts = append(parts, "error")
	}
	label := "Cleanup"
	if view.applied {
		label = "Cleanup applied"
	}
	line := " " + t.accent().Bold(true).Render(label) + "  " + t.muted().Render(strings.Join(parts, " · "))
	return fitWidth(line, width)
}

func renderCleanupRows(view *cleanupView, width int, t theme) []string {
	lines := make([]string, 0, len(view.rows))
	cursorItem := -1
	if view.cursor >= 0 && view.cursor < len(view.rows) && view.rows[view.cursor].Kind == cleanupRowItem {
		cursorItem = view.rows[view.cursor].Item
	}
	for _, row := range view.rows {
		switch row.Kind {
		case cleanupRowGroup:
			count := 0
			for _, item := range view.items {
				if item.Group == row.Group {
					count++
				}
			}
			lines = append(lines, renderCleanupGroupRow(row.Group, count, width, t))
		case cleanupRowItem:
			lines = append(lines, renderCleanupItemRow(view, row.Item, row.Item == cursorItem, width, t))
		}
	}
	return lines
}

func renderCleanupGroupRow(group cleanupGroup, count int, width int, t theme) string {
	style := cleanupGroupStyle(group, t)
	segs := []seg{
		{text: " " + t.glyphs.group + " " + cleanupGroupLabel(group) + fmt.Sprintf(" (%d)", count), style: style.Bold(true)},
	}
	if group == cleanupGroupReview {
		segs = append(segs, seg{text: "  force required (f)", style: t.muted()})
	}
	return finishLine(segs, false, width, t)
}

func cleanupGroupStyle(group cleanupGroup, t theme) lipgloss.Style {
	switch group {
	case cleanupGroupSafe:
		return t.Status(config.StatusReady)
	case cleanupGroupReview:
		return t.warning()
	default:
		return t.muted()
	}
}

func renderCleanupItemRow(view *cleanupView, index int, cursor bool, width int, t theme) string {
	item := view.items[index]
	marker := "  "
	if cursor {
		marker = "> "
	}
	checked := view.selected[item.Key]
	selectable := view.cleanupSelectable(item)
	check := " "
	switch {
	case checked:
		check = "x"
	case !selectable:
		check = "-"
	}

	targetWidth, classWidth, resultWidth := 24, 14, 9
	if width < 80 {
		targetWidth, classWidth, resultWidth = 18, 11, 7
	}
	target := truncateMiddle(render.Sanitize(cleanupTargetLabel(item.Item)), targetWidth)
	class := truncate(render.Sanitize(string(item.Item.Classification)), classWidth)
	result := truncate(render.Sanitize(string(item.Item.Result)), resultWidth)
	reasonText := item.Item.Detail
	if item.Item.Error != "" {
		// A failed apply's reason is more important than the classification
		// reason that was already visible during the preview.
		reasonText = item.Item.Error
	}
	reason := render.Sanitize(reasonText)

	base := t.text()
	targetStyle := base
	classStyle := cleanupGroupStyle(item.Group, t)
	reasonStyle := t.muted()
	checkStyle := t.muted()
	switch {
	case checked:
		checkStyle = t.accent().Bold(true)
	case !selectable:
		checkStyle = t.faint()
	}
	if item.Group == cleanupGroupBlocked {
		targetStyle = t.faint()
		reasonStyle = t.faint()
	}
	if item.Item.Error != "" || item.Item.Result == ops.CleanupResultFailed {
		reasonStyle = t.error()
	}

	segs := []seg{
		{text: " " + marker, style: base},
		{text: "[" + check + "] ", style: checkStyle},
		{text: pad(target, targetWidth) + "  ", style: targetStyle},
		{text: pad(class, classWidth) + "  ", style: classStyle},
	}
	if view.applied {
		resultStyle := t.muted()
		switch item.Item.Result {
		case ops.CleanupResultRemoved:
			resultStyle = t.info()
		case ops.CleanupResultFailed:
			resultStyle = t.error()
		}
		segs = append(segs, seg{text: pad(result, resultWidth) + "  ", style: resultStyle})
	}
	segs = append(segs, seg{text: reason, style: reasonStyle})
	return finishLine(segs, cursor, width, t)
}

func renderCleanupDetail(s State, width, height int, t theme) string {
	item, ok := s.cleanupCurrent()
	if !ok {
		return renderModalBox("Cleanup detail", lipgloss.NewStyle().Bold(true), []string{"No candidate selected."}, width, height, t)
	}
	_, contentWidth := modalBoxMetrics(width)
	job := item.Item
	lines := []string{
		t.accent().Bold(true).Render(render.Sanitize(cleanupTargetLabel(job))),
		"",
		fmt.Sprintf("Kind: %s   Classification: %s", render.Sanitize(job.Kind), render.Sanitize(string(job.Classification))),
		fmt.Sprintf("Action: %s", render.Sanitize(string(cleanupActionOrNone(job)))),
		fmt.Sprintf("Safe: %s   Removable: %s   Requires force: %s",
			yesNo(job.Safe), yesNo(job.Removable), yesNo(job.RequiresForce)),
	}
	if job.BranchState != "" {
		lines = append(lines, "Branch state: "+render.Sanitize(string(job.BranchState)))
	}
	lines = append(lines, "Selection: "+cleanupSelectionHint(s.cleanup, item))
	if job.Result != "" {
		lines = append(lines, "Result: "+render.Sanitize(string(job.Result)))
	}
	if job.Error != "" {
		lines = append(lines, "", t.error().Render("Error:"))
		for _, line := range wrapText(render.Sanitize(job.Error), contentWidth) {
			lines = append(lines, " "+line)
		}
	}
	if job.Loss != nil {
		// The counts are the always-visible loss summary; the per-path and
		// per-commit detail may be clipped on a short terminal.
		lines = append(lines, wrapText("Loss: "+cleanupLossCounts(job.Loss), contentWidth)...)
	}
	lines = append(lines, "", "Reason:")
	for _, line := range wrapText(render.Sanitize(job.Detail), contentWidth) {
		lines = append(lines, " "+line)
	}
	if job.Loss != nil {
		lines = append(lines, cleanupLossLines(job.Loss, contentWidth)...)
	}
	lines = clipModalLines(lines, height, t)
	return renderModalBox("Cleanup detail", lipgloss.NewStyle().Bold(true), lines, width, height, t)
}

// clipModalLines reserves one visible line for an explicit truncation marker so
// a short terminal never hides the tail of a plan silently.
func clipModalLines(lines []string, height int, t theme) []string {
	maxBody := max(0, height-2)
	if len(lines) <= maxBody {
		return lines
	}
	if maxBody == 0 {
		return nil
	}
	hidden := len(lines) - maxBody + 1
	out := append([]string(nil), lines[:maxBody-1]...)
	return append(out, fmt.Sprintf("%s %d more line(s) not shown", t.glyphs.ellipsis, hidden))
}

func cleanupLossCounts(loss *ops.CleanupLoss) string {
	if loss == nil {
		return "none reported"
	}
	var parts []string
	if files := len(loss.DirtyFiles); files > 0 {
		parts = append(parts, fmt.Sprintf("%d changed/untracked/ignored path(s)", files))
	}
	if commits := len(loss.Commits); commits > 0 {
		parts = append(parts, fmt.Sprintf("%d unmerged commit(s)", commits))
	}
	if len(parts) == 0 {
		return "none reported"
	}
	return strings.Join(parts, ", ")
}

func cleanupActionOrNone(item ops.CleanupItem) ops.CleanupAction {
	if item.Action == "" {
		return ops.CleanupActionNone
	}
	return item.Action
}

func cleanupSelectionHint(view *cleanupView, item cleanupItem) string {
	if view == nil {
		return "unavailable"
	}
	switch {
	case view.selected[item.Key]:
		return "selected; Space deselects"
	case view.cleanupSelectable(item):
		return "Space selects"
	case item.Group == cleanupGroupReview:
		return "force required; press f to enable"
	case item.Group == cleanupGroupBlocked:
		return "blocked; cannot be selected"
	default:
		return "not selectable"
	}
}

func cleanupLossLines(loss *ops.CleanupLoss, width int) []string {
	if loss == nil {
		return nil
	}
	lines := []string{"", "Loss:"}
	if len(loss.DirtyFiles) > 0 {
		lines = append(lines, fmt.Sprintf("  %d changed, untracked, or ignored path(s):", len(loss.DirtyFiles)))
		lines = append(lines, limitedLines(loss.DirtyFiles, 5, "    ", width)...)
	}
	if len(loss.Commits) > 0 {
		lines = append(lines, fmt.Sprintf("  %d commit(s) not in the default branch:", len(loss.Commits)))
		commits := make([]string, 0, len(loss.Commits))
		for _, commit := range loss.Commits {
			commits = append(commits, commit.SHA+" "+commit.Subject)
		}
		lines = append(lines, limitedLines(commits, 5, "    ", width)...)
	}
	return lines
}

func limitedLines(values []string, limit int, indent string, width int) []string {
	lines := make([]string, 0, min(len(values), limit)+1)
	for i, value := range values {
		if i == limit {
			lines = append(lines, fmt.Sprintf("%s%s %d more", indent, currentGlyphs().ellipsis, len(values)-limit))
			break
		}
		lines = append(lines, indent+truncate(render.Sanitize(value), max(1, width-lipgloss.Width(indent))))
	}
	return lines
}

func renderCleanupConfirm(s State, width, height int, t theme) string {
	view := s.cleanup
	if view == nil || view.confirm == nil {
		return strings.Repeat("\n", max(0, height-1))
	}
	confirm := view.confirm
	_, contentWidth := modalBoxMetrics(width)
	lines := []string{
		t.danger().Render(fmt.Sprintf("Apply cleanup to %d item(s)?", confirm.items)),
	}
	// The force loss summary is part of the headline: at 60 columns the body
	// clips, and no accepted apply may hide what force discards.
	if len(confirm.forceItems) > 0 {
		files, commits := 0, 0
		for _, item := range confirm.forceItems {
			if item.Item.Loss == nil {
				continue
			}
			files += len(item.Item.Loss.DirtyFiles)
			commits += len(item.Item.Loss.Commits)
		}
		summary := fmt.Sprintf("Force: on - %d force item(s)", len(confirm.forceItems))
		var bits []string
		if files > 0 {
			bits = append(bits, fmt.Sprintf("%d changed path(s)", files))
		}
		if commits > 0 {
			bits = append(bits, fmt.Sprintf("%d unmerged commit(s)", commits))
		}
		if len(bits) > 0 {
			summary += ": " + strings.Join(bits, ", ")
		}
		lines = append(lines, wrapText(summary, contentWidth)...)
	}
	lines = append(lines, "")
	for _, line := range wrapText(
		"This removes the selected worktrees, deletes the selected branches, and prunes missing-worktree records. Unselected items are not changed.",
		contentWidth,
	) {
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, wrapText(fmt.Sprintf(
		"Will remove: %d worktree(s), delete %d branch(es), prune %d record(s).",
		confirm.worktrees, confirm.branches, confirm.prunes,
	), contentWidth)...)
	lines = append(lines, fmt.Sprintf("Kept: %d item(s); %d branch(es) retained.", confirm.retainedItems, len(confirm.retainedBranches)))
	if len(confirm.retainedBranches) > 0 {
		names := strings.Join(previewNames(confirm.retainedBranches, 3), ", ") + moreNames(confirm.retainedBranches, 3)
		lines = append(lines, truncate(render.Sanitize("Retained branches: "+names), contentWidth))
	}

	if len(confirm.forceItems) == 0 {
		force := "Force: off; dirty and unmerged items cannot be selected."
		if view.force {
			force = "Force: on."
		}
		lines = append(lines, "")
		lines = append(lines, wrapText(force, contentWidth)...)
	} else {
		lines = append(lines, "", "Force-required items (their loss is applied):")
		for i, item := range confirm.forceItems {
			if i == 5 {
				lines = append(lines, fmt.Sprintf("  %s %d more force-required item(s)", currentGlyphs().ellipsis, len(confirm.forceItems)-5))
				break
			}
			text := "  " + render.Sanitize(cleanupItemLabel(item.Item)) + ": " + render.Sanitize(cleanupLossSummary(item.Item))
			lines = append(lines, truncate(text, contentWidth))
		}
	}
	if len(confirm.warnings) > 0 {
		lines = append(lines, "", fmt.Sprintf("Warnings/errors: %d", len(confirm.warnings)))
		for _, line := range wrapText(render.Sanitize(confirm.warnings[0]), contentWidth) {
			lines = append(lines, " "+line)
		}
	}
	if orphans := len(view.report.Orphans.Items); orphans > 0 {
		lines = append(lines, "", fmt.Sprintf("Orphans: %d reported; rexws cleanup --fix handles them (not changed here).", orphans))
	}

	lines = append(lines, "", "Items:")
	shown := 0
	total := 0
	for _, project := range confirm.report.Projects {
		total += len(project.Items)
	}
	for _, project := range confirm.report.Projects {
		for _, item := range project.Items {
			if shown == 10 {
				lines = append(lines, fmt.Sprintf("  %s %d more item(s)", currentGlyphs().ellipsis, total-shown))
				break
			}
			lines = append(lines, truncate("  "+render.Sanitize(cleanupActionLine(item)), contentWidth))
			shown++
		}
		if shown == 10 {
			break
		}
	}
	lines = clipModalLines(lines, height, t)
	return renderModalBox("Apply cleanup", t.danger(), lines, width, height, t)
}

func previewNames(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func moreNames(values []string, limit int) string {
	if len(values) <= limit {
		return ""
	}
	return fmt.Sprintf(" (+%d more)", len(values)-limit)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
