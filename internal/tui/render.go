package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
)

var (
	accentColor = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#7AA2F7"}
	selectBG    = lipgloss.AdaptiveColor{Light: "#DCE6FF", Dark: "#2A3F5F"}

	matchStyle  = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accentColor)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	groupStyle  = lipgloss.NewStyle().Bold(true)
	titleStyle  = lipgloss.NewStyle().Bold(true)
	noticeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#9A3412", Dark: "#FDBA74"})
)

// Render draws the whole screen.
func Render(s State) string {
	width, height := s.width, s.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	if width < 60 || height < 18 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, "terminal too small")
	}
	if s.mode == ModeFatal {
		return renderFatal(s, width, height)
	}

	header := renderHeader(s, width)
	separator := strings.Repeat("─", width)
	notice, hasNotice := renderNotice(s, width)
	bodyHeight := height - 3
	if hasNotice {
		bodyHeight--
	}
	if bodyHeight < 0 {
		bodyHeight = 0
	}

	body := renderBody(s, width, bodyHeight)
	footer := renderFooter(s, width)

	lines := []string{header, separator, body}
	if hasNotice {
		lines = append(lines, notice)
	}
	lines = append(lines, footer)
	return strings.Join(lines, "\n")
}

func renderFatal(s State, width, height int) string {
	message := "unknown error"
	if s.fatalErr != nil {
		message = render.Sanitize(s.fatalErr.Error())
	}
	remedy := "Fix ~/.config/rexws/config.json or ~/.config/rexws/state.json, then retry. `rexws list` shows CLI errors."
	lines := []string{
		" REXWS",
		"",
		" " + truncate(message, width-2),
		"",
		" " + truncate(remedy, width-2),
		"",
		" [r] retry  [q] quit",
	}
	content := strings.Join(lines, "\n")
	if lipgloss.Height(content) < height {
		content += strings.Repeat("\n", height-lipgloss.Height(content))
	}
	return content
}

func renderHeader(s State, width int) string {
	parts := []string{titleStyle.Render("REXWS")}
	if s.query != "" {
		query := s.query
		if width < 80 {
			query = truncate(query, 12)
		}
		parts = append(parts, "filter: "+query+" ×")
	}
	if s.haveConfig {
		switch {
		case !s.rexAvailable && s.rexEverLoaded:
			parts = append(parts, "Rex unavailable; live updates paused")
		case !s.rexAvailable:
			parts = append(parts, "Rex unavailable")
		case s.streamsPaused:
			parts = append(parts, "live updates paused")
		}
	}
	if s.haveConfig {
		if width >= 80 {
			parts = append(parts, fmt.Sprintf("[%d workstreams · %d live · %d agent-blocked]",
				s.build.Workstreams, s.build.Live, s.build.Blocked))
		} else {
			parts = append(parts, fmt.Sprintf("[%d ws · %d live]", s.build.Workstreams, s.build.Live))
		}
	}
	return fitWidth(" "+strings.Join(parts, "  "), width)
}

func renderNotice(s State, width int) (string, bool) {
	visible := make([]notice, 0, len(s.notices))
	for _, n := range s.notices {
		if n.persistent || n.expires.After(time.Now()) {
			visible = append(visible, n)
		}
	}
	if len(visible) == 0 {
		return "", false
	}
	newest := visible[len(visible)-1]
	text := " " + render.Sanitize(newest.text)
	if len(visible) > 1 {
		text += fmt.Sprintf("  +%d more", len(visible)-1)
	}
	return noticeStyle.Render(truncate(text, width)), true
}

func renderBody(s State, width, height int) string {
	switch s.mode {
	case ModeHelp:
		return renderHelp(s, width, height)
	case ModeStatus:
		return renderStatusModal(s, width, height)
	case ModeRemove:
		return renderRemoveModal(s, width, height)
	case ModeNew:
		return renderNewForm(s, width, height)
	}
	if !s.haveConfig {
		return centerLine("Loading…", width, height)
	}
	if len(s.workstreams) == 0 {
		return centerLine("No workstreams. Register a repository with: rexws project add <path>", width, height)
	}
	if len(s.rows) == 0 {
		if s.query != "" {
			return centerLine(fmt.Sprintf("No matches for %q. Press Esc to clear.", s.query), width, height)
		}
		return centerLine("No workstreams.", width, height)
	}
	lines := renderRows(s, width)
	top := s.viewTop
	if s.cursorRow >= 0 {
		if s.cursorRow < top {
			top = s.cursorRow
		}
		if s.cursorRow >= top+height {
			top = s.cursorRow - height + 1
		}
	}
	if top < 0 {
		top = 0
	}
	if maxTop := max(0, len(lines)-height); top > maxTop {
		top = maxTop
	}
	view := viewport.New(width, height)
	view.SetContent(strings.Join(lines, "\n"))
	view.SetYOffset(top)
	return view.View()
}

func centerLine(text string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, truncate(text, width))
}

type widths struct {
	project   int
	branch    int
	badge     int
	count     int
	label     int
	app       int
	winBadge  int
	showCount bool
	showApp   bool
}

func computeWidths(rows []Row, width int) widths {
	wide := width >= 80
	projectCap, badgeCap := 12, 28
	if !wide {
		projectCap, badgeCap = 10, 18
	}
	maxProject, maxBadge, maxCount := 1, 1, 0
	maxLabel, maxApp, maxWinBadge := 1, 1, 1
	for _, row := range rows {
		switch row.Kind {
		case RowWorkstream:
			maxProject = max(maxProject, lipgloss.Width(row.Project))
			maxBadge = max(maxBadge, lipgloss.Width(row.Badge))
			maxCount = max(maxCount, lipgloss.Width(row.WindowCount))
		case RowWindow:
			maxLabel = max(maxLabel, lipgloss.Width(row.WindowLabel))
			maxApp = max(maxApp, lipgloss.Width(row.WindowApp))
			maxWinBadge = max(maxWinBadge, lipgloss.Width(row.Badge))
		}
	}
	out := widths{
		project:   min(maxProject, projectCap),
		badge:     min(maxBadge, badgeCap),
		count:     maxCount,
		label:     maxLabel,
		app:       min(maxApp, 12),
		winBadge:  min(maxWinBadge, badgeCap),
		showCount: wide,
		showApp:   true,
	}
	if out.project < 1 {
		out.project = 1
	}
	if out.badge < 1 {
		out.badge = 1
	}
	// Workstream branch gets the remaining width, with at least 10 cells.
	// Prefix (5) + project + gap (2) + branch + gap (2) + badge.
	fixed := 9 + out.project + out.badge
	if out.showCount && out.count > 0 {
		fixed += 3 + out.count
	}
	out.branch = max(10, width-fixed)
	if !wide {
		// Hide the count entirely; narrow layout uses the space for branch.
		out.showCount = false
		out.branch = max(10, width-(9+out.project+out.badge))
	}
	// Window columns: hide the app before truncating the label below 12.
	prefix := 7
	appWidth := out.app
	label := width - prefix - appWidth - 2 - out.winBadge - 2
	if label < 12 {
		out.showApp = false
		label = width - prefix - out.winBadge - 2
	}
	if label < 12 {
		label = 12
	}
	out.label = max(12, min(maxLabel, label))
	if out.showApp {
		out.app = max(1, min(maxApp, appWidth))
	}
	return out
}

func renderRows(s State, width int) []string {
	w := computeWidths(s.rows, width)
	lines := make([]string, 0, len(s.rows))
	for _, row := range s.rows {
		selected := row.Key == s.cursorKey
		switch row.Kind {
		case RowGroup:
			lines = append(lines, renderGroupRow(row, width))
		case RowWorkstream:
			lines = append(lines, renderWorkstreamRow(row, selected, w, width))
		case RowWindow:
			lines = append(lines, renderWindowRow(row, selected, w, width))
		}
	}
	return lines
}

func renderGroupRow(row Row, width int) string {
	text := " ▌ " + row.GroupLabel
	if row.GroupCount != "" {
		text += " " + row.GroupCount
	}
	segs := []seg{{text: pad(text, width), spans: offsetSpans(row.GroupSpans, 3), style: groupStyle}}
	return finishLine(segs, false, width)
}

func renderWorkstreamRow(row Row, selected bool, w widths, width int) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	prefix := " " + marker
	if row.HasWindows {
		switch {
		case row.ContextMatch:
			prefix += "↳ "
		case row.Expanded:
			prefix += "▾ "
		default:
			prefix += "▸ "
		}
	}
	base := lipgloss.NewStyle()
	if row.ContextMatch && !selected {
		base = dimStyle
	}
	project := truncate(row.Project, w.project)
	branch := truncate(row.Branch, w.branch)
	badge := truncate(row.Badge, w.badge)
	projectSpans := clampSpans(row.ProjectSpans, utf8.RuneCountInString(project))
	branchSpans := clampSpans(row.BranchSpans, utf8.RuneCountInString(branch))

	segs := []seg{
		{text: prefix, style: base},
		{text: pad(project, w.project) + "  ", spans: projectSpans, style: base},
		{text: pad(branch, w.branch) + "  ", spans: branchSpans, style: base},
		{text: pad(badge, w.badge), style: base},
	}
	if w.showCount && row.WindowCount != "" {
		segs = append(segs, seg{text: " · " + row.WindowCount, style: base})
	}
	return finishLine(segs, selected, width)
}

func renderWindowRow(row Row, selected bool, w widths, width int) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	prefix := " " + marker + "  • "
	labelText := row.WindowLabel
	labelSpans := row.LabelSpans
	if row.WindowActive {
		labelText = "*" + labelText
		labelSpans = offsetSpans(labelSpans, 1)
	}
	label := truncate(labelText, w.label)
	app := truncate(row.WindowApp, w.app)
	badge := truncate(row.Badge, w.winBadge)
	labelSpans = clampSpans(labelSpans, utf8.RuneCountInString(label))
	appSpans := clampSpans(row.AppSpans, utf8.RuneCountInString(app))

	labelStyle := lipgloss.NewStyle()
	if row.WindowActive {
		labelStyle = labelStyle.Bold(true)
	}
	segs := []seg{
		{text: prefix, style: lipgloss.NewStyle()},
		{text: pad(label, w.label) + "  ", spans: labelSpans, style: labelStyle},
	}
	if w.showApp {
		segs = append(segs, seg{text: pad(app, w.app) + "  ", spans: appSpans, style: lipgloss.NewStyle()})
	}
	segs = append(segs, seg{text: pad(badge, w.winBadge), style: lipgloss.NewStyle()})
	return finishLine(segs, selected, width)
}

type seg struct {
	text  string
	spans []Span
	style lipgloss.Style
}

func finishLine(segs []seg, selected bool, width int) string {
	segs = truncateSegs(segs, width)
	var b strings.Builder
	for _, segment := range segs {
		base := segment.style
		if selected {
			base = base.Bold(true).Background(selectBG)
		}
		highlight := matchStyle.Inherit(base)
		b.WriteString(renderSpans(segment.text, segment.spans, base, highlight))
	}
	used := lipgloss.Width(joinPlain(segs))
	if used < width {
		padStyle := lipgloss.NewStyle()
		if selected {
			padStyle = padStyle.Background(selectBG)
		}
		b.WriteString(padStyle.Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

func joinPlain(segs []seg) string {
	var b strings.Builder
	for _, segment := range segs {
		b.WriteString(segment.text)
	}
	return b.String()
}

func renderSpans(text string, spans []Span, base, highlight lipgloss.Style) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	marked := make([]bool, len(runes))
	for _, span := range spans {
		start := max(0, span.Start)
		end := min(len(runes), span.End)
		for i := start; i < end; i++ {
			marked[i] = true
		}
	}
	var b strings.Builder
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && marked[j] == marked[i] {
			j++
		}
		part := string(runes[i:j])
		if marked[i] {
			b.WriteString(highlight.Render(part))
		} else {
			b.WriteString(base.Render(part))
		}
		i = j
	}
	return b.String()
}

func truncateSegs(segs []seg, width int) []seg {
	remaining := width
	out := make([]seg, 0, len(segs))
	for _, segment := range segs {
		used := lipgloss.Width(segment.text)
		if used <= remaining {
			out = append(out, segment)
			remaining -= used
			continue
		}
		cut := truncate(segment.text, remaining)
		out = append(out, seg{
			text:  cut,
			spans: clampSpans(segment.spans, utf8.RuneCountInString(cut)),
			style: segment.style,
		})
		break
	}
	return out
}

func clampSpans(spans []Span, limit int) []Span {
	out := make([]Span, 0, len(spans))
	for _, span := range spans {
		if span.Start >= limit {
			continue
		}
		end := min(span.End, limit)
		out = append(out, Span{Start: span.Start, End: end})
	}
	return out
}

func offsetSpans(spans []Span, offset int) []Span {
	out := make([]Span, 0, len(spans))
	for _, span := range spans {
		out = append(out, Span{Start: span.Start + offset, End: span.End + offset})
	}
	return out
}

// fitWidth truncates ANSI-styled text to width cells without corrupting
// escape sequences.
func fitWidth(text string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

func pad(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if used := lipgloss.Width(text); used < width {
		return text + strings.Repeat(" ", width-used)
	}
	return text
}

// truncate cuts text to width cells, using an ellipsis. It returns text
// unchanged when it already fits.
func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range text {
		rw := lipgloss.Width(string(r))
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteString("…")
	return b.String()
}

func renderFooter(s State, width int) string {
	switch s.mode {
	case ModeStatus:
		return truncate(" [↑↓] move  [1-9] shortcut  [Enter] apply  [Esc] cancel", width)
	case ModeRemove:
		return truncate(" [y/Enter] confirm  [n/Esc] cancel", width)
	case ModeNew:
		return truncate(" [Tab] field  [←/→] project  [Enter] create  [Esc] cancel", width)
	case ModeHelp:
		return truncate(" [↑↓] scroll  [PgUp/PgDn] page  [?/Esc] close", width)
	case ModeSearch:
		return renderSearchBar(s, width)
	}

	parts := []string{"[Enter] switch", "[→] expand", "[←] back"}
	if s.query != "" {
		parts = append(parts, "[Esc] clear filter")
	} else {
		parts = append(parts, "[/] filter")
	}
	parts = append(parts, "[?] help")
	line := " " + strings.Join(parts, "  ")
	if suffix := footerSuffix(s); suffix != "" {
		line += "  " + suffix
	}
	optional := []string{"[s] status", "[n] new", "[d] remove", "[r] refresh", "[q] quit"}
	for _, item := range optional {
		candidate := line + "  " + item
		if lipgloss.Width(candidate) > width {
			break
		}
		line = candidate
	}
	return truncate(line, width)
}

func footerSuffix(s State) string {
	switch {
	case s.pending:
		if s.pendingLabel != "" {
			return s.pendingLabel
		}
		return "working…"
	case s.loading:
		return "refreshing…"
	}
	return ""
}

func renderSearchBar(s State, width int) string {
	keys := "   [↑↓] move  [Enter] accept  [Esc] clear"
	available := width - lipgloss.Width(keys) - 1
	if available < 12 {
		available = 12
	}
	s.input.Width = available
	line := s.input.View() + keys
	return fitWidth(line, width)
}

func renderStatusModal(s State, width, height int) string {
	modal := s.status
	if modal == nil {
		return ""
	}
	lines := []string{
		" " + titleStyle.Render("Status for "+render.Sanitize(modal.project)+"/"+render.Sanitize(modal.branch)),
		"",
	}
	for i, def := range modal.statuses {
		number := "  "
		if i < 9 {
			number = fmt.Sprintf("%d ", i+1)
		}
		label := number + render.Sanitize(def.Label)
		if def.ID == modal.current {
			label += " (current)"
		}
		if i == modal.index {
			lines = append(lines, " > "+label)
		} else {
			lines = append(lines, "   "+label)
		}
	}
	return fitBody(lines, width, height)
}

func renderRemoveModal(s State, width, height int) string {
	modal := s.remove
	if modal == nil {
		return ""
	}
	lines := []string{
		" " + titleStyle.Render("Remove "+render.Sanitize(modal.project)+"/"+render.Sanitize(modal.branch)+"?"),
		"",
		" Worktree: " + render.Sanitize(modal.plan.Path),
		"",
		" This closes its Rex session and windows, runs the provider remove command,",
		" and deletes its workflow state.",
	}
	return fitBody(lines, width, height)
}

func renderNewForm(s State, width, height int) string {
	form := s.newForm
	if form == nil {
		return ""
	}
	project, ok := s.newFormProject()
	if !ok {
		return ""
	}
	marker := func(field int) string {
		if form.field == field {
			return "> "
		}
		return "  "
	}
	target := ops.TargetPath(project, form.branch.Value())
	lines := []string{
		" " + titleStyle.Render("New worktree"),
		"",
		fmt.Sprintf(" %sProject:  %s  (←/→)", marker(0), render.Sanitize(project.Name)),
	}
	branchValue := form.branch.View()
	if form.field != 1 {
		branchValue = render.Sanitize(form.branch.Value())
	}
	lines = append(lines,
		fmt.Sprintf(" %sBranch:   %s", marker(1), branchValue),
		fmt.Sprintf("   Provider: %s", render.Sanitize(s.cfg.ProviderFor(project))),
		fmt.Sprintf("   Target:   %s", render.Sanitize(target)),
	)
	return fitBody(lines, width, height)
}

func fitBody(lines []string, width, height int) string {
	for i := range lines {
		lines[i] = fitWidth(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func renderHelp(s State, width, height int) string {
	h := help.New()
	h.Width = width
	h.ShowAll = true
	content := h.View(helpKeys{})
	lines := strings.Split(content, "\n")
	top := s.helpOffset
	if maxTop := max(0, len(lines)-height); top > maxTop {
		top = maxTop
	}
	if top < 0 {
		top = 0
	}
	view := viewport.New(width, height)
	view.SetContent(content)
	view.SetYOffset(top)
	return view.View()
}

type helpKeys struct{}

func (helpKeys) ShortHelp() []key.Binding { return nil }

func (helpKeys) FullHelp() [][]key.Binding {
	bind := func(keys, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, description))
	}
	return [][]key.Binding{
		{
			bind("j/k, ↓/↑", "move selection"),
			bind("→/l", "expand / first child"),
			bind("←/h", "back / collapse"),
			bind("space", "toggle expansion"),
			bind("PgUp/PgDn", "move one page"),
		},
		{
			bind("enter", "switch session/window"),
			bind("s", "set workflow status"),
			bind("n", "new worktree"),
			bind("d", "remove workstream"),
			bind("r", "refresh and retry Rex"),
		},
		{
			bind("/", "search"),
			bind("enter", "accept filter"),
			bind("esc", "clear filter"),
			bind("ctrl+u", "clear query"),
			bind("ctrl+n/p", "move selection"),
		},
		{
			bind("?", "help"),
			bind("q", "quit"),
			bind("ctrl+c", "quit"),
		},
	}
}
