package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
)

// contentWidthCap is the widest frame rexws composes. Above it the block is
// centered and extra columns are margins (TUI-DESIGN.md §3.1).
const contentWidthCap = 120

// Render draws the whole screen.
func Render(s State) string {
	terminalWidth, terminalHeight := s.width, s.height
	if terminalWidth <= 0 {
		terminalWidth = 80
	}
	if terminalHeight <= 0 {
		terminalHeight = 24
	}
	t := currentTheme()
	if terminalWidth < 60 || terminalHeight < 18 {
		return renderTooSmall(t, terminalWidth, terminalHeight)
	}

	width := min(terminalWidth, contentWidthCap)
	if s.mode == ModeFatal {
		return frame(strings.Join(renderFatal(s, width, terminalHeight, t), "\n"), width, terminalWidth)
	}

	header := renderHeader(s, width, t)
	separator := strings.Repeat(t.glyphs.rule, width)
	// One clock read per frame keeps the notice banner and the cleanup error
	// line in agreement at the expiry boundary.
	now := time.Now()
	notice, hasNotice := renderNotice(s, width, t, now)
	bodyHeight := terminalHeight - 3
	if hasNotice {
		bodyHeight--
	}
	if bodyHeight < 0 {
		bodyHeight = 0
	}

	body := renderBody(s, width, bodyHeight, t, now)
	footer := renderFooter(s, width, t)

	lines := []string{header, separator, body}
	if hasNotice {
		lines = append(lines, notice)
	}
	lines = append(lines, footer)
	return frame(strings.Join(lines, "\n"), width, terminalWidth)
}

// frame makes every line exactly the content width, then adds symmetric
// margins when the terminal is wider than the cap.
func frame(out string, width, terminalWidth int) string {
	left, right := 0, 0
	if terminalWidth > width {
		left = (terminalWidth - width) / 2
		right = terminalWidth - width - left
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		line = pad(fitWidth(line, width), width)
		if terminalWidth > width {
			line = strings.Repeat(" ", left) + line + strings.Repeat(" ", right)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func renderTooSmall(t theme, width, height int) string {
	block := "terminal too small\n" +
		t.muted().Render(fmt.Sprintf("need 60x18, have %dx%d", width, height))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, block)
}

func renderFatal(s State, width, height int, t theme) []string {
	message := "unknown error"
	if s.fatalErr != nil {
		message = render.Sanitize(s.fatalErr.Error())
	}
	file := "config.json"
	if s.fatalKind == "state" {
		file = "state.json"
	}
	prose := max(1, min(width, 100)-1)
	ellipsis := t.glyphs.ellipsis

	head := []string{
		" " + t.accent().Bold(true).Render("REXWS"),
		"",
		" " + t.danger().Render("Cannot load "+file),
		"",
	}
	remedy := fmt.Sprintf("Fix ~/.config/rexws/%s, then press r to retry.", file)
	tail := []string{" "}
	for _, line := range wrapText(remedy, prose) {
		tail = append(tail, " "+line)
	}
	tail = append(tail,
		" "+t.muted().Render("rexws list shows the same error outside the TUI."),
		"",
		" [r] retry  [q] quit",
	)

	// Reserve the remedy, hint, and recovery keys before giving rows to the
	// error. A long error is truncated with an ellipsis, never the key line.
	maxError := height - len(head) - len(tail)
	errorLines := fitLines(wrapText(message, prose), maxError, prose, ellipsis)
	lines := make([]string, 0, height)
	lines = append(lines, head...)
	for _, line := range errorLines {
		lines = append(lines, " "+line)
	}
	lines = append(lines, tail...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// fitLines keeps at most max wrapped prose lines, marking the cut with an
// ellipsis on the last kept line.
func fitLines(lines []string, maxLines, width int, ellipsis string) []string {
	if maxLines <= 0 {
		return nil
	}
	if len(lines) <= maxLines {
		return lines
	}
	out := append([]string(nil), lines[:maxLines]...)
	last := strings.TrimSuffix(out[len(out)-1], ellipsis)
	out[len(out)-1] = headCells(last, max(0, width-lipgloss.Width(ellipsis))) + ellipsis
	return out
}

// renderHeader draws: brand, filter chip, health token, counts. The health
// token is global and never truncated; counts are a whole chip or absent.
func renderHeader(s State, width int, t theme) string {
	brand := t.accent().Bold(true).Render("REXWS")
	health := s.healthToken()
	healthPart := ""
	if health != "" {
		healthPart = "  " + t.health(health).Render(health)
	}

	query := render.Sanitize(s.query)
	if query != "" && (width < 80 || health != "") {
		query = truncate(query, 12)
	}
	counts := ""
	if s.haveConfig && s.build.Workstreams > 0 {
		counts = countsChip(s, width, t)
	}

	// Sacrifice order is counts, then query width; health is never truncated.
	// Build the query first so a long query keeps its room at 80 columns.
	chipFixed := lipgloss.Width("  filter: ") + lipgloss.Width(" "+t.glyphs.times)
	nonQuery := lipgloss.Width(" "+brand) + lipgloss.Width(healthPart)

	line := " " + brand
	if query != "" {
		line += "  filter: " + query + " " + t.glyphs.times
	}
	line += healthPart
	if lipgloss.Width(line) > width && query != "" {
		available := width - nonQuery - chipFixed
		if available < 1 {
			query = ""
			line = " " + brand + healthPart
		} else {
			query = truncate(query, available)
			line = " " + brand + "  filter: " + query + " " + t.glyphs.times + healthPart
		}
	}

	if counts != "" {
		if width >= 120 {
			if gap := width - lipgloss.Width(line) - lipgloss.Width(counts); gap >= 2 {
				line += strings.Repeat(" ", gap) + counts
			}
		} else if lipgloss.Width(line)+2+lipgloss.Width(counts) <= width {
			line += "  " + counts
		}
	}
	return fitWidth(line, width)
}

// countsChip renders the header counts. `live` stays uncolored; the rest is
// muted metadata. A zero total suppresses the chip entirely.
func countsChip(s State, width int, t theme) string {
	if s.build.Workstreams == 0 {
		return ""
	}
	dot := " " + t.glyphs.dot + " "
	muted := t.muted()
	if width < 80 {
		return muted.Render("[") +
			muted.Render(fmt.Sprintf("%d ws", s.build.Workstreams)) +
			muted.Render(dot) +
			t.text().Render(fmt.Sprintf("%d live", s.build.Live)) +
			muted.Render("]")
	}
	word := "workstreams"
	if s.build.Workstreams == 1 {
		word = "workstream"
	}
	return muted.Render("[") +
		muted.Render(fmt.Sprintf("%d %s", s.build.Workstreams, word)) +
		muted.Render(dot) +
		t.text().Render(fmt.Sprintf("%d live", s.build.Live)) +
		muted.Render(dot) +
		muted.Render(fmt.Sprintf("%d agent-blocked", s.build.Blocked)) +
		muted.Render("]")
}

func renderNotice(s State, width int, t theme, now time.Time) (string, bool) {
	shown, more, ok := s.bannerNotice(now)
	if !ok {
		return "", false
	}
	suffix := ""
	if more > 0 {
		suffix = fmt.Sprintf("  +%d more", more)
	}
	available := width - lipgloss.Width(suffix)
	if available < 1 {
		available = 1
	}
	text := truncate(" "+render.Sanitize(shown.text), available)
	style := t.Notice(shown.persistent)
	switch {
	case shown.info && !shown.persistent:
		style = t.info()
	case shown.warning && !shown.persistent:
		style = t.warning()
	}
	return style.Render(text) + t.muted().Render(suffix), true
}

func renderBody(s State, width, height int, t theme, now time.Time) string {
	switch s.mode {
	case ModeHelp:
		return renderHelp(s, width, height, t)
	case ModeStatus:
		return renderStatusModal(s, width, height, t)
	case ModeRemove:
		return renderRemoveModal(s, width, height, t)
	case ModeNew:
		return renderNewForm(s, width, height, t)
	case ModeCleanup:
		return renderCleanup(s, width, height, t, now)
	case ModeCleanupDetail:
		return renderCleanupDetail(s, width, height, t)
	case ModeCleanupConfirm:
		return renderCleanupConfirm(s, width, height, t)
	}
	if !s.haveConfig {
		return renderLoading(width, height, t)
	}
	if len(s.workstreams) == 0 {
		return renderEmpty(width, height)
	}
	if len(s.rows) == 0 {
		if s.query != "" {
			return renderNoMatches(s.query, width, height)
		}
		return renderEmpty(width, height)
	}
	lines := renderRows(s, width, t)
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

func renderLoading(width, height int, t theme) string {
	lines := []string{"Loading" + t.glyphs.ellipsis}
	if width >= 80 {
		lines = append(lines, t.muted().Render("Reading Git worktrees and Rex sessions"))
	}
	return centerBlock(lines, width, height)
}

func renderEmpty(width, height int) string {
	if width >= 80 {
		return centerBlock([]string{"No workstreams. Register one with: rexws project add <path>"}, width, height)
	}
	return centerBlock([]string{"No workstreams.", "Register one: rexws project add <path>"}, width, height)
}

func renderNoMatches(query string, width, height int) string {
	query = render.Sanitize(query)
	if width >= 80 {
		line := fmt.Sprintf("No matches for %q. Press Esc to clear.", query)
		return centerBlock([]string{truncate(line, width)}, width, height)
	}
	return centerBlock([]string{
		truncate(fmt.Sprintf("No matches for %q.", query), width),
		"Press Esc to clear.",
	}, width, height)
}

func centerBlock(lines []string, width, height int) string {
	for i, line := range lines {
		lines[i] = fitWidth(line, width)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// grid is the single column layout for every width tier (TUI-DESIGN.md §3.2).
type grid struct {
	project   int
	branch    int
	badge     int
	count     int
	label     int
	app       int
	showCount bool
}

func newGrid(rows []Row, width int) grid {
	g := grid{project: 10, badge: 18}
	switch {
	case width >= 120:
		g.project, g.badge, g.showCount, g.count = 16, 28, true, 8
	case width >= 80:
		g.project, g.badge, g.showCount, g.count = 12, 28, true, 8
	}

	parentFixed := 11 + g.project + g.badge
	if g.showCount {
		parentFixed += 2 + g.count
	}
	g.branch = max(1, width-parentFixed)

	maxLabel := 1
	for _, row := range rows {
		if row.Kind != RowWindow {
			continue
		}
		label := lipgloss.Width(row.WindowLabel)
		if row.WindowActive {
			label++
		}
		maxLabel = max(maxLabel, label)
	}
	g.label = max(12, min(maxLabel, 24))

	childFixed := 12 + g.label + g.badge
	if g.showCount {
		childFixed += 2 + g.count
	}
	g.app = max(0, width-childFixed)
	return g
}

func renderRows(s State, width int, t theme) []string {
	g := newGrid(s.rows, width)
	lines := make([]string, 0, len(s.rows))
	for _, row := range s.rows {
		selected := row.Key == s.cursorKey
		switch row.Kind {
		case RowGroup:
			lines = append(lines, renderGroupRow(row, width, t))
		case RowWorkstream:
			lines = append(lines, renderWorkstreamRow(row, selected, g, width, t))
		case RowWindow:
			lines = append(lines, renderWindowRow(row, selected, g, width, t))
		}
	}
	return lines
}

func renderGroupRow(row Row, width int, t theme) string {
	statusStyle := t.StatusDef(config.StatusDef{ID: row.GroupID, Color: row.GroupColor})
	segs := []seg{
		{text: " " + t.glyphs.group, style: statusStyle},
	}
	if row.GroupIcon != "" {
		segs = append(segs, seg{text: " " + row.GroupIcon, style: statusStyle})
	}
	segs = append(segs, seg{
		text:  " " + row.GroupLabel,
		spans: offsetSpans(row.GroupSpans, 1),
		style: lipgloss.NewStyle().Bold(true),
	})
	if row.GroupCount != "" {
		segs = append(segs, seg{text: " " + row.GroupCount, style: t.muted()})
	}
	return finishLine(segs, false, width, t)
}

func renderWorkstreamRow(row Row, selected bool, g grid, width int, t theme) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	tree := "  "
	if row.HasWindows {
		switch {
		case row.ContextMatch:
			tree = t.glyphs.context + " "
		case row.Expanded:
			tree = t.glyphs.expanded + " "
		default:
			tree = t.glyphs.collapsed + " "
		}
	}
	base := t.text()
	if row.ContextMatch && !selected {
		base = t.faint()
	}

	project := truncate(row.Project, g.project)
	branch := truncate(row.Branch, g.branch)
	badgeText := t.badgeText(row.Badge)
	if badgeText == "—" {
		badgeText = t.glyphs.empty
	}
	badge := truncate(badgeText, g.badge)
	projectSpans := clampSpans(row.ProjectSpans, utf8.RuneCountInString(project))
	branchSpans := clampSpans(row.BranchSpans, utf8.RuneCountInString(branch))

	markerText, markerStyle := markerCell(row, base, t)
	badgeStyle := t.Agent(row.AgentState)
	if row.Badge == "—" {
		badgeStyle = t.muted()
	}
	if row.ContextMatch && !selected {
		badgeStyle = t.faint()
	}

	segs := []seg{
		{text: " " + marker + tree, style: base},
		{text: pad(project, g.project) + "  ", spans: projectSpans, style: base},
		{text: pad(branch, g.branch) + " ", spans: branchSpans, style: base},
		{text: markerText, style: markerStyle},
		{text: " ", style: base},
		{text: pad(badge, g.badge), style: badgeStyle},
	}
	if g.showCount {
		countText := strings.Repeat(" ", g.count)
		if row.WindowCount != "" {
			countText = rightAlign(row.WindowCount, g.count)
		}
		segs = append(segs, seg{text: "  " + countText, style: t.muted()})
	}
	return finishLine(segs, selected, width, t)
}

// markerCell composes the fixed two-cell marker: ambiguous takes the warning
// role; paused is muted; both take warning.
func markerCell(row Row, base lipgloss.Style, t theme) (string, lipgloss.Style) {
	switch {
	case row.MarkerAmbiguous && row.MarkerPaused:
		return pad(t.glyphs.both, 2), t.warning()
	case row.MarkerAmbiguous:
		return pad(t.glyphs.ambiguous+" ", 2), t.warning()
	case row.MarkerPaused:
		return pad(t.glyphs.paused, 2), t.muted()
	default:
		return "  ", base
	}
}

func renderWindowRow(row Row, selected bool, g grid, width int, t theme) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	prefix := " " + marker + "  " + t.glyphs.child + " "

	labelText := row.WindowLabel
	labelSpans := row.LabelSpans
	if row.WindowActive {
		labelText = "*" + labelText
		labelSpans = offsetSpans(labelSpans, 1)
	}
	label := truncate(labelText, g.label)
	labelSpans = clampSpans(labelSpans, utf8.RuneCountInString(label))

	appText := row.WindowApp
	if appText == "—" {
		appText = t.glyphs.empty
	}
	app := truncate(appText, g.app)
	appSpans := clampSpans(row.AppSpans, utf8.RuneCountInString(app))
	appPad := max(0, g.app-lipgloss.Width(app))
	appSpans = offsetSpans(appSpans, appPad)

	badgeText := t.badgeText(row.Badge)
	if badgeText == "—" {
		badgeText = t.glyphs.empty
	}
	badge := truncate(badgeText, g.badge)

	labelStyle := t.text()
	if row.WindowActive {
		labelStyle = t.accent().Bold(true)
	}
	badgeStyle := t.Agent(row.AgentState)
	if row.Badge == "—" {
		badgeStyle = t.muted()
	}

	segs := []seg{
		{text: prefix, style: t.text()},
		{text: pad(label, g.label), spans: labelSpans, style: labelStyle},
		{text: " ", style: t.text()},
		{text: strings.Repeat(" ", appPad) + app, spans: appSpans, style: t.muted()},
		{text: " ", style: t.text()},
		{text: "  ", style: t.text()},
		{text: " ", style: t.text()},
		{text: pad(badge, g.badge), style: badgeStyle},
	}
	if g.showCount {
		segs = append(segs, seg{text: "  " + strings.Repeat(" ", g.count), style: t.muted()})
	}
	return finishLine(segs, selected, width, t)
}

type seg struct {
	text  string
	spans []Span
	style lipgloss.Style
}

func finishLine(segs []seg, selected bool, width int, t theme) string {
	segs = truncateSegs(segs, width)
	var b strings.Builder
	for _, segment := range segs {
		base := segment.style
		highlight := t.match().Inherit(base)
		if selected {
			base = t.selection()
			highlight = t.selection().Underline(true)
		}
		b.WriteString(renderSpans(segment.text, segment.spans, base, highlight))
	}
	used := lipgloss.Width(joinPlain(segs))
	if used < width {
		padStyle := t.text()
		if selected {
			padStyle = t.selection()
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
	if width <= 0 {
		return ""
	}
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

func rightAlign(text string, width int) string {
	text = truncate(text, width)
	if used := lipgloss.Width(text); used < width {
		return strings.Repeat(" ", width-used) + text
	}
	return text
}

// truncate cuts text to width cells, using the active glyph set's ellipsis. It
// returns text unchanged when it already fits.
func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	ellipsis := currentGlyphs().ellipsis
	if width <= lipgloss.Width(ellipsis) {
		return ellipsis
	}
	var b strings.Builder
	used := 0
	limit := width - lipgloss.Width(ellipsis)
	for _, r := range text {
		rw := lipgloss.Width(string(r))
		if used+rw > limit {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteString(ellipsis)
	return b.String()
}

// truncateMiddle shortens a path-like value in the middle, preserving the last
// path component (and at least the tail of it) wherever it fits.
func truncateMiddle(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	ellipsis := currentGlyphs().ellipsis
	ellipsisWidth := lipgloss.Width(ellipsis)
	if width <= ellipsisWidth {
		return ellipsis
	}
	keep := width - ellipsisWidth
	tail := keep / 2
	if slash := strings.LastIndex(text, "/"); slash >= 0 {
		tail = max(tail, min(keep, lipgloss.Width(text[slash:])))
	}
	head := keep - tail
	return headCells(text, head) + ellipsis + tailCells(text, tail)
}

func headCells(text string, cells int) string {
	if cells <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range text {
		width := lipgloss.Width(string(r))
		if used+width > cells {
			break
		}
		b.WriteRune(r)
		used += width
	}
	return b.String()
}

func tailCells(text string, cells int) string {
	if cells <= 0 {
		return ""
	}
	runes := []rune(text)
	used := 0
	start := len(runes)
	for start > 0 {
		width := lipgloss.Width(string(runes[start-1]))
		if used+width > cells {
			break
		}
		used += width
		start--
	}
	return string(runes[start:])
}

func renderFooter(s State, width int, t theme) string {
	if line, ok := modalFooter(s, width, t); ok {
		return fitWidth(line, width)
	}

	compact := width < 80
	sep := "  "
	if compact {
		sep = " "
	}
	parts := footerParts(s, compact, t)
	line := " " + strings.Join(parts, sep)
	suffix := t.pendingText(footerSuffix(s))
	optional := []string{"[s] status", "[n] new", "[d] remove", "[c] cleanup", "[r] refresh", "[q] quit"}

	if !compact && suffix != "" {
		// If the wide key bar cannot leave room for the right-aligned status,
		// fall back to single spaces before sacrificing a priority key.
		if lipgloss.Width(line)+lipgloss.Width(suffix)+1 > width {
			sep = " "
			line = " " + strings.Join(parts, sep)
		}
		limit := width - lipgloss.Width(suffix) - 2
		for _, item := range optional {
			candidate := line + sep + item
			if lipgloss.Width(candidate) > limit {
				break
			}
			line = candidate
		}
		line = fitWidth(line, max(0, width-lipgloss.Width(suffix)-1))
		gap := max(1, width-lipgloss.Width(line)-lipgloss.Width(suffix))
		return fitWidth(line+strings.Repeat(" ", gap)+t.info().Bold(true).Render(suffix), width)
	}

	if !compact {
		for _, item := range optional {
			candidate := line + sep + item
			if lipgloss.Width(candidate) > width {
				break
			}
			line = candidate
		}
	}
	if suffix != "" {
		styled := t.info().Bold(true).Render(suffix)
		candidate := line + sep + suffix
		if lipgloss.Width(candidate) <= width {
			line += sep + styled
		} else {
			// Keep the active action visible on the compact footer; the help
			// key is the least urgent priority key to give up.
			trimmed := strings.TrimSuffix(line, sep+"[?] help")
			if plain := trimmed + sep + suffix; lipgloss.Width(plain) <= width {
				line = trimmed + sep + styled
			}
		}
	}
	return fitWidth(line, width)
}

func footerParts(s State, compact bool, t theme) []string {
	// The 60-79 footer is the four priority keys from TUI-DESIGN.md §4.5:
	// switch, expand, filter, help. `back` is the first optional key there.
	parts := []string{"[Enter] switch", "[" + t.glyphs.arrowRight + "] expand"}
	if !compact {
		parts = append(parts, "["+t.glyphs.arrowLeft+"] back")
	}
	if s.query != "" {
		if compact {
			parts = append(parts, "[Esc] clear")
		} else {
			parts = append(parts, "[Esc] clear filter")
		}
	} else {
		parts = append(parts, "[/] filter")
	}
	return append(parts, "[?] help")
}

func modalFooter(s State, width int, t theme) (string, bool) {
	compact := width < 80
	sep := "  "
	if compact {
		sep = " "
	}
	var parts []string
	switch s.mode {
	case ModeStatus:
		parts = []string{"[" + t.glyphs.arrowUp + t.glyphs.arrowDown + "] move", "[1-9] shortcut", "[Enter] apply", "[Esc] cancel"}
	case ModeRemove:
		parts = []string{"[y/Enter] confirm", "[n/Esc] cancel"}
	case ModeNew:
		parts = []string{"[Tab] field", "[" + t.glyphs.arrowLeft + "/" + t.glyphs.arrowRight + "] project", "[Enter] create", "[Esc] cancel"}
	case ModeHelp:
		parts = []string{"[" + t.glyphs.arrowUp + t.glyphs.arrowDown + "] scroll", "[PgUp/PgDn] page", "[?/Esc] close"}
	case ModeCleanup:
		if s.cleanup != nil && s.cleanup.applying {
			return " " + t.pendingText("applying…"), true
		}
		switch {
		case s.cleanup != nil && s.cleanup.loading:
			parts = []string{"[Esc] back"}
		case s.cleanup != nil && s.cleanup.err != "" && len(s.cleanup.items) == 0:
			parts = []string{"[r] retry", "[Esc] back"}
		case compact:
			parts = []string{"[sp] pick", "[f] force", "[r] rescan", "[Enter] review", "[Esc] back"}
		default:
			// The wide cleanup footer uses single spaces so rescan fits at 80.
			sep = " "
			parts = []string{"[Space] pick", "[a] safe", "[f] force", "[v] detail", "[r] rescan", "[Enter] review", "[Esc] back"}
		}
	case ModeCleanupDetail:
		parts = []string{"[" + t.glyphs.arrowUp + t.glyphs.arrowDown + "] move", "[Space] select", "[f] force", "[v/Esc] close"}
	case ModeCleanupConfirm:
		parts = []string{"[y] apply", "[n/Esc] cancel"}
	case ModeSearch:
		return renderSearchBar(s, width, t), true
	default:
		return "", false
	}
	return " " + strings.Join(parts, sep), true
}

func footerSuffix(s State) string {
	switch {
	case s.pending:
		if s.pendingLabel != "" {
			return s.pendingLabel
		}
		return "working" + currentGlyphs().ellipsis
	case s.loading:
		return "refreshing" + currentGlyphs().ellipsis
	}
	return ""
}

func renderSearchBar(s State, width int, t theme) string {
	arrows := t.glyphs.arrowUp + t.glyphs.arrowDown
	keys := "   [" + arrows + "] move  [Enter] accept  [Esc] clear"
	if width < 80 {
		keys = " [" + arrows + "] move [Enter] accept [Esc] clear"
	}
	input := s.input
	if asciiMode() {
		input.Cursor.SetChar("|")
	}
	keysWidth := lipgloss.Width(keys)
	promptWidth := lipgloss.Width(input.Prompt)
	// Reserve one cell for the cursor and one for the overflow slack that
	// handleOverflow allows; SetValue below recomputes the cached offsets.
	input.Width = max(1, width-keysWidth-promptWidth-2)
	input.SetValue(input.Value())
	view := fitWidth(input.View(), max(1, width-keysWidth))
	return fitWidth(view+keys, width)
}

func renderStatusModal(s State, width, height int, t theme) string {
	modal := s.status
	if modal == nil {
		return strings.Repeat("\n", max(0, height-1))
	}
	_, contentWidth := modalBoxMetrics(width)
	path := truncateMiddle(
		render.Sanitize(modal.project)+"/"+render.Sanitize(modal.branch),
		contentWidth,
	)
	lines := []string{path, ""}
	for i, def := range modal.statuses {
		prefix := "  "
		if i == modal.index {
			prefix = "> "
		}
		number := "  "
		if i < 9 {
			number = fmt.Sprintf("%d ", i+1)
		}
		// Only the icon carries the configured color; the label stays base
		// text so a low-contrast fixed color cannot make it unreadable. The
		// icon occupies a fixed two-cell slot so labels align.
		icon := def.ResolvedIcon()
		iconCell := pad(icon, 2) + " "
		iconStyle := t.StatusDef(def)
		if i == modal.index {
			iconStyle = iconStyle.Bold(true)
		}
		label := render.Sanitize(def.Label)
		if def.ID == modal.current {
			label += " (current)"
		}
		labelStyle := lipgloss.NewStyle()
		if i == modal.index {
			labelStyle = labelStyle.Bold(true)
		}
		line := prefix + number + iconCell + labelStyle.Render(label)
		if icon != "" {
			line = prefix + number + iconStyle.Render(iconCell) + labelStyle.Render(label)
		}
		lines = append(lines, line)
	}
	return renderModalBox("Status", lipgloss.NewStyle().Bold(true), lines, width, height, t)
}

func renderRemoveModal(s State, width, height int, t theme) string {
	modal := s.remove
	if modal == nil {
		return strings.Repeat("\n", max(0, height-1))
	}
	_, contentWidth := modalBoxMetrics(width)
	maxBody := max(0, height-2)
	path := truncateMiddle(render.Sanitize(modal.plan.Path), max(1, contentWidth-lipgloss.Width("Worktree: ")))
	lines := []string{
		t.danger().Render("Remove " + render.Sanitize(modal.project) + "/" + render.Sanitize(modal.branch) + "?"),
		"",
		"Worktree: " + path,
		"",
	}
	for _, line := range wrapText(
		"This closes its Rex session and windows, runs the provider remove command, and deletes its workflow state.",
		contentWidth,
	) {
		lines = append(lines, line)
	}

	// The exact loss, retained state, and action list come from the plan, the
	// same source the CLI prints, so the confirmation cannot disagree.
	block := []string{"", "Loss:"}
	for _, line := range modal.plan.LossLines() {
		block = append(block, modalWrappedLine(render.Sanitize(line), contentWidth)...)
	}
	if retained := modal.plan.RetainedLines(); len(retained) > 0 {
		block = append(block, "", "Retained:")
		for _, line := range retained {
			block = append(block, modalWrappedLine(render.Sanitize(line), contentWidth)...)
		}
	}
	block = append(block, "", "Actions:")
	for _, line := range modal.plan.ActionLines() {
		block = append(block, modalWrappedLine(render.Sanitize(line), contentWidth)...)
	}

	wrappedWarnings := make([][]string, 0, len(modal.plan.Warnings))
	for _, warning := range modal.plan.Warnings {
		if warning == nil {
			continue
		}
		wrappedWarnings = append(wrappedWarnings, wrapText("Warning: "+render.Sanitize(warning.Error()), contentWidth))
	}

	// Keep at least the first warning on screen; truncate the plan block
	// instead of hiding every warning behind a count.
	reserve := 0
	if len(wrappedWarnings) > 0 {
		reserve = 1 + len(wrappedWarnings[0])
		if reserve > maxBody {
			reserve = maxBody
		}
	}
	maxBlock := max(0, maxBody-len(lines)-reserve)
	if len(block) > maxBlock {
		if maxBlock == 0 {
			block = nil
		} else {
			hidden := len(block) - maxBlock + 1
			block = append(block[:maxBlock-1], fmt.Sprintf("%s %d more line(s) not shown", t.glyphs.ellipsis, hidden))
		}
	}
	lines = append(lines, block...)

	skipped := 0
	for _, wrapped := range wrappedWarnings {
		if len(lines)+1+len(wrapped) <= maxBody {
			lines = append(lines, "")
			lines = append(lines, wrapped...)
			continue
		}
		skipped++
	}
	if skipped > 0 {
		indicator := fmt.Sprintf("+%d more warnings", skipped)
		if len(lines) >= maxBody && maxBody > 0 {
			lines = append(lines[:maxBody-1], indicator)
		} else {
			lines = append(lines, indicator)
		}
	}
	return renderModalBox("Remove workstream", t.danger(), lines, width, height, t)
}

// modalWrappedLine wraps one plan line, preserving its leading indentation.
func modalWrappedLine(line string, width int) []string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	text := strings.TrimSpace(line)
	if text == "" {
		return []string{""}
	}
	inner := max(1, width-lipgloss.Width(indent))
	var out []string
	for _, wrapped := range wrapText(text, inner) {
		out = append(out, indent+wrapped)
	}
	return out
}

func renderNewForm(s State, width, height int, t theme) string {
	form := s.newForm
	if form == nil {
		return strings.Repeat("\n", max(0, height-1))
	}
	project, ok := s.newFormProject()
	if !ok {
		return strings.Repeat("\n", max(0, height-1))
	}
	marker := func(field int) string {
		if form.field == field {
			return "> "
		}
		return "  "
	}
	target := ops.TargetPath(project, form.branch.Value())
	_, contentWidth := modalBoxMetrics(width)
	branchWidth := max(1, contentWidth-lipgloss.Width(marker(1))-lipgloss.Width("Branch:   "))
	branchInput := form.branch
	branchInput.Prompt = ""
	if asciiMode() {
		branchInput.Cursor.SetChar("|")
	}
	// Leave two cells for the cursor and the overflow slack that
	// handleOverflow allows; SetValue recomputes the cached offsets.
	branchInput.Width = max(1, branchWidth-2)
	branchInput.SetValue(branchInput.Value())
	branchValue := fitWidth(branchInput.View(), branchWidth)
	if form.field != 1 {
		branchValue = truncate(render.Sanitize(form.branch.Value()), branchWidth)
	}
	targetWidth := max(1, contentWidth-lipgloss.Width("  Target:   "))
	targetText := truncateMiddle(render.Sanitize(target), targetWidth)
	lines := []string{
		fmt.Sprintf("%sProject:  %s  (%s/%s)", marker(0), render.Sanitize(project.Name), t.glyphs.arrowLeft, t.glyphs.arrowRight),
		fmt.Sprintf("%sBranch:   %s", marker(1), branchValue),
		t.muted().Render(fmt.Sprintf("  Provider: %s", render.Sanitize(s.cfg.ProviderFor(project)))),
		t.muted().Render("  Target:   " + targetText),
	}
	return renderModalBox("New worktree", lipgloss.NewStyle().Bold(true), lines, width, height, t)
}

func modalBoxMetrics(width int) (boxWidth, contentWidth int) {
	boxWidth = min(width-6, 64)
	if boxWidth < 20 {
		boxWidth = max(1, width-2)
	}
	return boxWidth, max(1, boxWidth-4)
}

// renderModalBox draws a bordered panel centered in the body.
func renderModalBox(title string, titleStyle lipgloss.Style, lines []string, width, height int, t theme) string {
	boxWidth, contentWidth := modalBoxMetrics(width)
	border := t.muted()

	var top string
	if title != "" {
		label := fitWidth(title, max(1, boxWidth-5))
		rest := max(0, boxWidth-5-lipgloss.Width(label))
		top = border.Render(t.glyphs.boxTopLeft+t.glyphs.boxHorizontal+" ") +
			titleStyle.Render(label) +
			border.Render(" "+strings.Repeat(t.glyphs.boxHorizontal, rest)+t.glyphs.boxTopRight)
	} else {
		top = border.Render(t.glyphs.boxTopLeft + strings.Repeat(t.glyphs.boxHorizontal, boxWidth-2) + t.glyphs.boxTopRight)
	}
	bottom := border.Render(t.glyphs.boxBottomLeft + strings.Repeat(t.glyphs.boxHorizontal, boxWidth-2) + t.glyphs.boxBottomRight)

	body := make([]string, 0, len(lines))
	maxBody := max(0, height-2)
	for _, line := range lines {
		if len(body) >= maxBody {
			break
		}
		line = fitWidth(line, contentWidth)
		body = append(body,
			border.Render(t.glyphs.boxVertical)+" "+pad(line, contentWidth)+" "+border.Render(t.glyphs.boxVertical))
	}

	boxRows := make([]string, 0, len(body)+2)
	boxRows = append(boxRows, top)
	boxRows = append(boxRows, body...)
	boxRows = append(boxRows, bottom)

	left := max(0, (width-boxWidth)/2)
	topPad := max(0, (height-len(boxRows))/2)
	out := make([]string, height)
	for i, row := range boxRows {
		if topPad+i >= height {
			break
		}
		out[topPad+i] = pad(strings.Repeat(" ", left)+row, width)
	}
	return strings.Join(out, "\n")
}

func renderHelp(s State, width, height int, t theme) string {
	boxWidth, contentWidth := helpBoxMetrics(width)

	lines := make([]string, 0, 24)
	persistent := s.persistentNotices(time.Now())
	if len(persistent) > 0 {
		lines = append(lines, " Warnings")
		for _, n := range persistent {
			for _, line := range wrapText(render.Sanitize(n.text), contentWidth-1) {
				lines = append(lines, " "+line)
			}
			if n.detail != "" {
				for _, line := range wrapText(render.Sanitize(n.detail), contentWidth-3) {
					lines = append(lines, "   "+line)
				}
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines,
		" Markers",
		" > selected    * active window",
		" "+t.glyphs.collapsed+t.glyphs.expanded+" collapsed/expanded  "+t.glyphs.context+" context  ? unknown  "+t.glyphs.empty+" none",
		" "+t.glyphs.ambiguous+"   ambiguous session binding",
		" "+t.glyphs.paused+"   live updates paused for this session",
		" "+t.glyphs.both+"  both    x dismiss the newest action error",
		" Keys",
		" j/k, "+t.glyphs.arrowDown+"/"+t.glyphs.arrowUp+"     move selection",
		" "+t.glyphs.arrowRight+"/l          expand / first child",
		" "+t.glyphs.arrowLeft+"/h          back / collapse",
		" space enter  toggle / switch",
		" s n d r c    status/new/remove/refresh/cleanup",
		" / ? q        search / help / quit",
		" PgUp/PgDn    move one page",
		" ctrl+u       clear query",
		" ctrl+n/p     move selection",
		" ctrl+c       quit",
		" esc          clear filter / close modal",
	)
	return renderHelpBox("Help", lines, boxWidth, width, height, t, s.helpOffset)
}

func helpBoxMetrics(width int) (boxWidth, contentWidth int) {
	switch {
	case width < 80:
		boxWidth = 56
	case width < 120:
		boxWidth = 64
	default:
		boxWidth = 88
	}
	boxWidth = min(boxWidth, max(1, width-2))
	return boxWidth, max(1, boxWidth-4)
}

func renderHelpBox(title string, content []string, boxWidth, width, height int, t theme, offset int) string {
	contentWidth := max(1, boxWidth-4)
	visible := max(0, height-2)
	top := max(0, offset)
	if maxTop := max(0, len(content)-visible); top > maxTop {
		top = maxTop
	}

	border := t.muted()
	titleWidth := lipgloss.Width(title)
	rest := max(0, boxWidth-5-titleWidth)
	topLine := border.Render(t.glyphs.boxTopLeft+t.glyphs.boxHorizontal+" ") +
		lipgloss.NewStyle().Bold(true).Render(title) +
		border.Render(" "+strings.Repeat(t.glyphs.boxHorizontal, rest)+t.glyphs.boxTopRight)
	bottomLine := border.Render(t.glyphs.boxBottomLeft + strings.Repeat(t.glyphs.boxHorizontal, boxWidth-2) + t.glyphs.boxBottomRight)

	rows := make([]string, 0, height)
	rows = append(rows, topLine)
	for i := 0; i < visible; i++ {
		line := ""
		if index := top + i; index < len(content) {
			line = content[index]
		}
		line = pad(fitWidth(line, contentWidth), contentWidth)
		rows = append(rows, border.Render(t.glyphs.boxVertical)+" "+line+" "+border.Render(t.glyphs.boxVertical))
	}
	rows = append(rows, bottomLine)

	left := max(0, (width-boxWidth)/2)
	out := make([]string, height)
	for i, row := range rows {
		if i >= height {
			break
		}
		out[i] = pad(strings.Repeat(" ", left)+row, width)
	}
	return strings.Join(out, "\n")
}

// wrapText breaks text into lines of at most width cells without splitting a
// word when it can be avoided. Prose only: rows never wrap.
func wrapText(text string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	for _, word := range words {
		switch {
		case current == "":
			current = word
		case lipgloss.Width(current)+1+lipgloss.Width(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
		if lipgloss.Width(current) > width {
			lines = append(lines, truncate(current, width))
			current = ""
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}
