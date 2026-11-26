package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/render"
)

// BuildRows derives the visible tree from the workstreams, expansion state,
// sticky forced set, and query. It is pure: the input maps are not mutated.
//
// Visibility rules (TUI-UX §6.3):
//  1. parent matches, no child matches: show the parent; show all children
//     only when expanded;
//  2. parent and at least one child match: show the parent, force it open, and
//     show all children;
//  3. only children match: show the parent as a dimmed context row, force it
//     open, and show only matching children;
//  4. neither: hide the workstream.
//
// A status-group match makes every workstream in that group a parent match.
func BuildRows(opts BuildOptions) BuildResult {
	query := opts.Query
	active := query != ""
	forced := cloneSet(opts.Forced)
	result := BuildResult{Forced: forced}

	byStatus := make(map[config.Status][]core.Workstream, len(opts.Statuses))
	for _, ws := range opts.Workstreams {
		byStatus[ws.WorkflowStatus] = append(byStatus[ws.WorkflowStatus], ws)
	}

	for _, def := range opts.Statuses {
		group := byStatus[def.ID]
		if len(group) == 0 {
			continue
		}
		groupLabel := strings.ToUpper(render.Sanitize(def.Label))
		groupID := strings.ToUpper(render.Sanitize(string(def.ID)))
		groupMatch := active && (matchesFold(groupLabel, query) || matchesFold(groupID, query))

		type visibleWorkstream struct {
			ws          core.Workstream
			parentMatch bool
			context     bool
			expanded    bool
			windowIdxs  []int
		}
		visible := make([]visibleWorkstream, 0, len(group))
		for _, ws := range group {
			parentMatch := !active || groupMatch || workstreamMatches(ws, query)
			childIdxs := matchingWindowIndexes(ws, query)
			childMatch := len(childIdxs) > 0
			if active && !parentMatch && !childMatch {
				continue
			}
			if active && childMatch {
				forced[ws.Path] = true
			}
			expanded := opts.Expanded[ws.Path]
			if active && (childMatch || forced[ws.Path]) {
				expanded = true
			}
			idxs := make([]int, 0, len(ws.Windows))
			if active && !parentMatch && childMatch {
				idxs = childIdxs
			} else {
				for i := range ws.Windows {
					idxs = append(idxs, i)
				}
			}
			visible = append(visible, visibleWorkstream{
				ws:          ws,
				parentMatch: parentMatch,
				context:     active && !parentMatch && childMatch,
				expanded:    expanded,
				windowIdxs:  idxs,
			})
		}
		if len(visible) == 0 {
			continue
		}

		count := fmt.Sprintf("(%d)", len(group))
		if active {
			count = fmt.Sprintf("(%d/%d)", len(visible), len(group))
		}
		result.Rows = append(result.Rows, Row{
			Kind:       RowGroup,
			Key:        RowKey{Kind: RowGroup, ID: string(def.ID)},
			Depth:      0,
			GroupID:    def.ID,
			GroupLabel: groupLabel,
			GroupCount: count,
			GroupSpans: matchSpans(groupLabel, query),
		})

		for _, item := range visible {
			result.Workstreams++
			if item.ws.RexSessionID != "" {
				result.Live++
			}
			if item.ws.AgentState == core.AgentBlocked {
				result.Blocked++
			}
			row := workstreamRow(item.ws, item.parentMatch, item.context, item.expanded, len(item.windowIdxs), active, opts, query)
			result.Rows = append(result.Rows, row)
			if !item.expanded || len(item.ws.Windows) == 0 {
				continue
			}
			labelCounts := windowLabelCounts(item.ws)
			for _, idx := range item.windowIdxs {
				window := item.ws.Windows[idx]
				matched := active && windowMatches(item.ws, window, query)
				result.Rows = append(result.Rows, windowRow(item.ws, window, labelCounts, matched, active, opts.BadgeUnknown, query))
			}
		}
	}
	return result
}

func workstreamRow(ws core.Workstream, parentMatch, context, expanded bool, visibleWindows int, active bool, opts BuildOptions, query string) Row {
	project := render.Sanitize(ws.Project)
	branch := render.Sanitize(ws.Branch)
	badge := render.Sanitize(render.Default.Format(ws))
	if opts.BadgeUnknown {
		badge = "?"
	}
	// Markers occupy their own two-cell cell on the parent row; windows inherit
	// the pause without a marker (TUI-DESIGN.md §1.3, §3.2).
	ambiguous := ws.RexSessionID != "" &&
		(opts.AmbiguousSessions[ws.RexSessionID] || opts.AmbiguousLabels[ws.SessionName])
	paused := !opts.SuppressPauseMarker && ws.RexSessionID != "" && opts.PausedSessions[ws.RexSessionID]
	state := workstreamHighestState(ws)
	if opts.BadgeUnknown {
		state = core.AgentUnknown
	}
	row := Row{
		Kind:            RowWorkstream,
		Key:             RowKey{Kind: RowWorkstream, ID: ws.Path},
		Depth:           1,
		Path:            ws.Path,
		SessionName:     render.Sanitize(ws.SessionName),
		Project:         project,
		Branch:          branch,
		Badge:           badge,
		AgentState:      state,
		MarkerAmbiguous: ambiguous,
		MarkerPaused:    paused,
		HasWindows:      len(ws.Windows) > 0,
		Expanded:        expanded,
		Matched:         active && parentMatch,
		ContextMatch:    context,
	}
	if active {
		row.ProjectSpans = matchSpans(project, query)
		row.BranchSpans = matchSpans(branch, query)
	}
	row.WindowCount = windowCountText(len(ws.Windows), visibleWindows, active, context)
	return row
}

func windowRow(ws core.Workstream, window core.Window, labelCounts map[string]int, matched, active, badgeUnknown bool, query string) Row {
	label := displayWindowLabel(window, labelCounts)
	app := "—"
	badge := "—"
	if !badgeUnknown {
		app = render.Sanitize(render.WindowApp(ws, window))
		badge = render.Sanitize(render.FormatWindowAgents(ws, window.ID))
	}
	if app == "" {
		app = "—"
	}
	if badge == "" {
		badge = "—"
	}
	state := windowHighestState(ws, window.ID)
	if badgeUnknown {
		state = core.AgentUnknown
	}
	if badge == "—" {
		state = core.AgentStopped
	}
	row := Row{
		Kind:          RowWindow,
		Key:           RowKey{Kind: RowWindow, ID: window.ID},
		Depth:         2,
		Path:          ws.Path,
		SessionName:   render.Sanitize(ws.SessionName),
		Project:       render.Sanitize(ws.Project),
		Branch:        render.Sanitize(ws.Branch),
		WindowID:      window.ID,
		WindowLabel:   label,
		WindowApp:     app,
		Badge:         badge,
		AgentState:    state,
		WindowActive:  window.Active,
		WindowMatched: matched,
	}
	if active {
		row.LabelSpans = matchSpans(label, query)
		if app != "—" {
			row.AppSpans = matchSpans(app, query)
		}
	}
	return row
}

// workstreamHighestState mirrors render.Default.Format's ordering, including
// unknown agents, so the whole badge takes the color of its first label.
func workstreamHighestState(ws core.Workstream) core.AgentState {
	switch ws.AgentState {
	case core.AgentStopped:
		return core.AgentStopped
	case core.AgentUnknown:
		if len(ws.Agents) == 0 {
			return core.AgentUnknown
		}
	}
	if len(ws.Agents) == 0 {
		return core.AgentIdle
	}
	return highestAgentState(ws.Agents, "")
}

// windowHighestState returns the window's highest-severity agent state, so the
// whole badge can take that role's color.
func windowHighestState(ws core.Workstream, windowID string) core.AgentState {
	return highestAgentState(ws.Agents, windowID)
}

func highestAgentState(agents []core.Agent, windowID string) core.AgentState {
	best := core.AgentState("")
	bestSeverity := -2
	for _, agent := range agents {
		if windowID != "" && agent.WindowID != windowID {
			continue
		}
		state := agent.State
		if state == "" {
			state = core.AgentUnknown
		}
		if severity := state.Severity(); severity > bestSeverity {
			bestSeverity = severity
			best = state
		}
	}
	return best
}

// windowCountText follows the three cases in TUI-UX §3.2.
func windowCountText(total, visible int, active, context bool) string {
	if total == 0 {
		return ""
	}
	if active && context {
		return fmt.Sprintf("%d/%d tabs", visible, total)
	}
	if total == 1 {
		return "1 tab"
	}
	return fmt.Sprintf("%d tabs", total)
}

// displayWindowLabel disambiguates duplicate labels with the last six
// characters of the window ID.
func displayWindowLabel(window core.Window, labelCounts map[string]int) string {
	label := strings.TrimSpace(render.Sanitize(window.Label))
	short := shortID(window.ID)
	if label == "" {
		return short
	}
	if labelCounts[label] > 1 {
		return fmt.Sprintf("%s (%s)", label, short)
	}
	return label
}

func windowLabelCounts(ws core.Workstream) map[string]int {
	counts := make(map[string]int, len(ws.Windows))
	for _, window := range ws.Windows {
		counts[strings.TrimSpace(render.Sanitize(window.Label))]++
	}
	return counts
}

func shortID(id string) string {
	const width = 6
	if len(id) > width {
		return id[len(id)-width:]
	}
	return id
}

func workstreamMatches(ws core.Workstream, query string) bool {
	if matchesFold(render.Sanitize(ws.Project), query) || matchesFold(render.Sanitize(ws.Branch), query) {
		return true
	}
	for _, agent := range ws.Agents {
		if agentMatches(agent, query) {
			return true
		}
	}
	return false
}

func matchingWindowIndexes(ws core.Workstream, query string) []int {
	var indexes []int
	for i, window := range ws.Windows {
		if windowMatches(ws, window, query) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func windowMatches(ws core.Workstream, window core.Window, query string) bool {
	labelCounts := windowLabelCounts(ws)
	if matchesFold(displayWindowLabel(window, labelCounts), query) {
		return true
	}
	if matchesFold(render.Sanitize(render.WindowApp(ws, window)), query) {
		return true
	}
	for _, agent := range ws.Agents {
		if agent.WindowID == window.ID && agentMatches(agent, query) {
			return true
		}
	}
	return false
}

// agentMatches matches an agent's state and detail, not its message or title.
func agentMatches(agent core.Agent, query string) bool {
	state := string(agent.State)
	if state == "" {
		state = string(core.AgentUnknown)
	}
	return matchesFold(state, query) || matchesFold(render.Sanitize(agent.Detail), query)
}

// matchesFold reports whether query occurs in s, ignoring case.
func matchesFold(s, query string) bool {
	return len(matchSpans(s, query)) > 0
}

// matchSpans returns non-overlapping rune spans of case-insensitive query
// occurrences in s.
func matchSpans(s, query string) []Span {
	if query == "" || s == "" {
		return nil
	}
	text := []rune(s)
	needle := []rune(query)
	if len(needle) > len(text) {
		return nil
	}
	folded := make([]rune, len(needle))
	for i, r := range needle {
		folded[i] = unicode.ToLower(r)
	}
	var spans []Span
	for i := 0; i+len(folded) <= len(text); {
		matched := true
		for j, want := range folded {
			if unicode.ToLower(text[i+j]) != want {
				matched = false
				break
			}
		}
		if matched {
			spans = append(spans, Span{Start: i, End: i + len(folded)})
			i += len(folded)
			continue
		}
		i++
	}
	return spans
}

func cloneSet(set map[string]bool) map[string]bool {
	out := make(map[string]bool, len(set))
	for key, value := range set {
		out[key] = value
	}
	return out
}
