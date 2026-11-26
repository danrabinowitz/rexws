package tui

import (
	"fmt"
	"sort"
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
	sessions        []core.Session

	// Per-stream health, keyed by session ID.
	streamPaused map[string]bool      // debounced: visible after 2s
	pausePending map[string]bool      // a tick is scheduled
	pauseDue     map[string]time.Time // absolute time the pause becomes visible
	pauseSeq     map[string]int
	pauseToken   map[string]int

	// Stream generation and the debounced Rex availability check. The
	// server-wide stream is an availability probe, not a per-session health
	// signal.
	streamGeneration int
	rexCheckPending  bool
	rexCheckSeq      int
	rexCheckToken    int
	lastRexCheck     time.Time
	lastLoadAt       time.Time
	lastLoadHealthy  bool

	// Ambiguity derived from live session labels vs workstream SessionNames.
	ambiguousSessions map[string]bool // session ID bound to >1 workstream
	ambiguousLabels   map[string]bool // label shared by >1 live session

	fatalKind string
	fatalErr  error

	width        int
	height       int
	loading      bool
	reloadWanted bool

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

	input          textinput.Model
	status         *statusModal
	remove         *removeModal
	newForm        *newForm
	cleanup        *cleanupView
	cleanupScanSeq int
	helpOffset     int
	notices        []notice
	noticeSeq      int

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
	detail     string // full context shown only in Help
	persistent bool
	// info marks a successful result rather than an error. Info notices lose
	// the banner to transient errors, warnings, and persistent warnings.
	info bool
	// warning marks a non-fatal problem such as a leftover directory.
	warning bool
	// priority orders persistent warnings: lower is more urgent.
	priority int
	// seq is insertion order; the newest transient error wins the banner.
	seq     int
	expires time.Time
}

// Persistent warning classes, in banner priority order.
const (
	warningAmbiguity = iota
	warningDiscovery
	warningSession
)

// persistentWarning is one load-time condition. Conditions persist until a
// later load stops reporting them.
type persistentWarning struct {
	text   string
	detail string
	class  int
}

// visibleNotices returns the notices that are currently displayed, in
// insertion order. An expired but unpruned notice is not visible.
func (s State) visibleNotices(now time.Time) []notice {
	visible := make([]notice, 0, len(s.notices))
	for _, n := range s.notices {
		if n.persistent || n.expires.After(now) {
			visible = append(visible, n)
		}
	}
	return visible
}

// persistentNotices returns active warnings, highest priority first and
// newest first within a class.
func (s State) persistentNotices(now time.Time) []notice {
	var out []notice
	for _, n := range s.visibleNotices(now) {
		if n.persistent {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].priority != out[j].priority {
			return out[i].priority < out[j].priority
		}
		return out[i].seq > out[j].seq
	})
	return out
}

// bannerNotice chooses the single notice the banner shows and the number of
// other active notices. Transient errors outrank transient warnings, which
// outrank persistent warnings, which outrank transient info.
func (s State) bannerNotice(now time.Time) (notice, int, bool) {
	visible := s.visibleNotices(now)
	if len(visible) == 0 {
		return notice{}, 0, false
	}
	best := -1
	bestWarning := -1
	bestInfo := -1
	for i, n := range visible {
		if n.persistent {
			continue
		}
		switch {
		case n.info:
			if bestInfo == -1 || n.seq > visible[bestInfo].seq {
				bestInfo = i
			}
		case n.warning:
			if bestWarning == -1 || n.seq > visible[bestWarning].seq {
				bestWarning = i
			}
		default:
			if best == -1 || n.seq > visible[best].seq {
				best = i
			}
		}
	}
	if best != -1 {
		return visible[best], len(visible) - 1, true
	}
	if bestWarning != -1 {
		return visible[bestWarning], len(visible) - 1, true
	}
	if persistent := s.persistentNotices(now); len(persistent) > 0 {
		return persistent[0], len(visible) - 1, true
	}
	if bestInfo != -1 {
		return visible[bestInfo], len(visible) - 1, true
	}
	return notice{}, 0, false
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

		streamPaused:      map[string]bool{},
		pausePending:      map[string]bool{},
		pauseDue:          map[string]time.Time{},
		pauseSeq:          map[string]int{},
		pauseToken:        map[string]int{},
		ambiguousSessions: map[string]bool{},
		ambiguousLabels:   map[string]bool{},
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
		Workstreams:         s.workstreams,
		Statuses:            s.cfg.Statuses,
		Expanded:            s.expanded,
		Forced:              s.forced,
		Query:               s.query,
		BadgeUnknown:        s.badgeUnknown(),
		PausedSessions:      s.pausedSessions(),
		SuppressPauseMarker: s.suppressPauseMarker(),
		AmbiguousSessions:   s.ambiguousSessions,
		AmbiguousLabels:     s.ambiguousLabels,
	})
	s.rows = s.build.Rows
	s.forced = s.build.Forced
	s.cursorKey, s.cursorRow = ClampCursor(s.rows, s.cursorKey, s.cursorRow)
	return s.ensureCursorVisible()
}

// liveSessionIDs returns the distinct Rex session IDs bound to workstreams.
func (s State) liveSessionIDs() []string {
	seen := make(map[string]bool, len(s.workstreams))
	ids := make([]string, 0, len(s.workstreams))
	for _, ws := range s.workstreams {
		if ws.RexSessionID == "" || seen[ws.RexSessionID] {
			continue
		}
		seen[ws.RexSessionID] = true
		ids = append(ids, ws.RexSessionID)
	}
	return ids
}

// pausedSessions returns the live session IDs whose updates are visibly
// paused.
func (s State) pausedSessions() map[string]bool {
	out := map[string]bool{}
	for _, id := range s.liveSessionIDs() {
		if s.streamPaused[id] {
			out[id] = true
		}
	}
	return out
}

func (s State) allLiveStreamsPaused() bool {
	live := s.liveSessionIDs()
	if len(live) == 0 {
		return false
	}
	for _, id := range live {
		if !s.streamPaused[id] {
			return false
		}
	}
	return true
}

// suppressPauseMarker hides the per-row pause marker when the header already
// reports the outage; ambiguity markers stay.
func (s State) suppressPauseMarker() bool {
	if !s.haveConfig {
		return false
	}
	if !s.rexAvailable {
		return true
	}
	return s.allLiveStreamsPaused()
}

// healthToken is the single global health condition shown in the header. It is
// never filter-scoped.
func (s State) healthToken() string {
	if !s.haveConfig {
		return ""
	}
	switch {
	case !s.rexAvailable && s.rexEverLoaded:
		return "Rex unavailable; paused (r)"
	case !s.rexAvailable:
		return "Rex unavailable (r)"
	}
	live := s.liveSessionIDs()
	if len(live) == 0 {
		return ""
	}
	paused := 0
	for _, id := range live {
		if s.streamPaused[id] {
			paused++
		}
	}
	switch {
	case paused == 0:
		return ""
	case paused >= len(live):
		return "live paused (r)"
	case paused == 1:
		return "1 stream paused (r)"
	default:
		return fmt.Sprintf("%d streams paused (r)", paused)
	}
}

// markStreamConnecting records that a reconnect attempt is underway: a pending
// tick is canceled, but the absolute pause deadline and an already-visible
// pause are kept.
func (s State) markStreamConnecting(sessionID string) State {
	s.pauseToken[sessionID]++
	delete(s.pausePending, sessionID)
	return s
}

// markStreamConnected records a real connect and clears any pending or
// visible pause immediately.
func (s State) markStreamConnected(sessionID string) State {
	s.pauseToken[sessionID]++
	delete(s.pausePending, sessionID)
	delete(s.streamPaused, sessionID)
	delete(s.pauseDue, sessionID)
	return s
}

// pruneStreamState forgets health for sessions that are no longer live.
func (s State) pruneStreamState() State {
	keep := map[string]bool{}
	for _, id := range s.liveSessionIDs() {
		keep[id] = true
	}
	for _, set := range []map[string]bool{s.streamPaused, s.pausePending} {
		for id := range set {
			if !keep[id] {
				delete(set, id)
			}
		}
	}
	for _, set := range []map[string]int{s.pauseSeq, s.pauseToken} {
		for id := range set {
			if !keep[id] {
				delete(set, id)
			}
		}
	}
	for id := range s.pauseDue {
		if !keep[id] {
			delete(s.pauseDue, id)
		}
	}
	return s
}

// resetStreamState drops health and pending stream work from an earlier stream
// generation. A later generation re-reports health from scratch.
func (s State) resetStreamState() State {
	s.streamPaused = map[string]bool{}
	s.pausePending = map[string]bool{}
	s.pauseDue = map[string]time.Time{}
	s.pauseToken = map[string]int{}
	s.rexCheckPending = false
	s.rexCheckToken++
	return s
}

func (s State) bodyHeight() int {
	height := s.height - 3
	if len(s.visibleNotices(time.Now())) > 0 {
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
	s.noticeSeq++
	s.notices = append(s.notices, notice{
		text:    text,
		seq:     s.noticeSeq,
		expires: time.Now().Add(8 * time.Second),
	})
	return s
}

// addInfo records a transient successful result. Info notices do not outrank
// errors, warnings, or persistent warnings in the banner.
func (s State) addInfo(text string) State {
	if text == "" {
		return s
	}
	s.noticeSeq++
	s.notices = append(s.notices, notice{
		text:    text,
		info:    true,
		seq:     s.noticeSeq,
		expires: time.Now().Add(8 * time.Second),
	})
	return s
}

// addWarning records a transient non-fatal problem, such as a provider that
// deregistered a worktree but left its directory behind.
func (s State) addWarning(text string) State {
	if text == "" {
		return s
	}
	s.noticeSeq++
	s.notices = append(s.notices, notice{
		text:    text,
		warning: true,
		seq:     s.noticeSeq,
		expires: time.Now().Add(8 * time.Second),
	})
	return s
}

// setPersistentWarnings replaces load-time warnings, keeping transient errors.
// Persistent warnings do not expire; a later load that does not report one
// removes it.
func (s State) setPersistentWarnings(warnings []persistentWarning) State {
	kept := make([]notice, 0, len(s.notices)+len(warnings))
	for _, n := range s.notices {
		if !n.persistent {
			kept = append(kept, n)
		}
	}
	for _, warning := range warnings {
		if warning.text == "" {
			continue
		}
		s.noticeSeq++
		kept = append(kept, notice{
			text:       "! " + warning.text,
			detail:     warning.detail,
			persistent: true,
			priority:   warning.class,
			seq:        s.noticeSeq,
		})
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

// dismissNotice removes the newest unexpired transient error. Persistent
// warnings are not dismissable.
func (s State) dismissNotice() State {
	now := time.Now()
	newest := -1
	for i, n := range s.notices {
		if n.persistent || !n.expires.After(now) {
			continue
		}
		if newest == -1 || n.seq > s.notices[newest].seq {
			newest = i
		}
	}
	if newest == -1 {
		return s
	}
	s.notices = append(s.notices[:newest], s.notices[newest+1:]...)
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
