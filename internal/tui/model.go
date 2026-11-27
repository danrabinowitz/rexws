package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/agentstate"
	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// tickFunc schedules a delayed message. It is a seam so timing tests do not
// have to sleep.
type tickFunc func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

// Model is the Bubbletea model. It owns the pure State plus the event hub.
type Model struct {
	state         State
	hub           *streamHub
	ctx           context.Context
	cancel        context.CancelFunc
	noticeTicking bool
	tick          tickFunc
	now           func() time.Time
}

// NewModel returns a dashboard that loads its data on Init.
func NewModel() *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{state: newState(), ctx: ctx, cancel: cancel, tick: tea.Tick, now: time.Now}
}

// Run starts the dashboard in the alternate screen.
func Run() error {
	model := NewModel()
	defer model.Close()
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

// Close stops background streams.
func (m *Model) Close() {
	m.cancel()
	if m.hub != nil {
		m.hub.Close()
	}
}

// Init starts the initial data load.
func (m *Model) Init() tea.Cmd { return loadCmd(m.ctx) }

// View renders the current state.
func (m *Model) View() string { return Render(m.state) }

// Update handles messages and keeps the notice-expiry tick running whenever a
// transient error is visible, no matter which handler added it.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	if m.state.hasExpiringNotices(time.Now()) {
		if !m.noticeTicking {
			m.noticeTicking = true
			cmd = batch([]tea.Cmd{cmd, noticeTickCmd()})
		}
	} else {
		m.noticeTicking = false
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.state.width = msg.Width
		m.state.height = msg.Height
		m.state.input.Width = max(10, msg.Width-40)
		m.state = m.state.ensureCursorVisible()
		return m, nil

	case tea.KeyMsg:
		state, effects := Reduce(m.state, msg)
		m.state = state
		return m.applyEffects(effects)

	case loadedMsg:
		return m.handleLoaded(msg)

	case switchDoneMsg:
		return m.handleSwitchDone(msg)
	case statusDoneMsg:
		return m.handleStatusDone(msg)
	case removePreparedMsg:
		return m.handleRemovePrepared(msg)
	case cleanupPlannedMsg:
		return m.handleCleanupPlanned(msg)
	case cleanupAppliedMsg:
		return m.handleCleanupApplied(msg)
	case removeSessionClosedMsg:
		return m.handleRemoveSessionClosed(msg)
	case removeExecDoneMsg:
		return m.handleRemoveExecDone(msg)
	case createPreparedMsg:
		return m.handleCreatePrepared(msg)
	case createExecDoneMsg:
		return m.handleCreateExecDone(msg)
	case createVerifiedMsg:
		return m.handleCreateVerified(msg)

	case streamEventMsg:
		if m.hub == nil {
			return m, nil
		}
		if m.staleStream(msg.generation) {
			return m, waitForStream(m.hub)
		}
		cmd := m.handleStreamEvent(msg.event)
		return m, batch([]tea.Cmd{cmd, waitForStream(m.hub)})
	case streamStateMsg:
		if m.hub == nil {
			return m, nil
		}
		if m.staleStream(msg.generation) {
			return m, waitForStream(m.hub)
		}
		cmd := m.handleStreamState(msg)
		return m, batch([]tea.Cmd{cmd, waitForStream(m.hub)})
	case sessionAgentsMsg:
		return m.handleSessionAgents(msg)
	case sessionWindowsMsg:
		return m.handleSessionWindows(msg)
	case windowRefreshTickMsg:
		return m.handleWindowRefreshTick(msg)
	case pauseTickMsg:
		return m.handlePauseTick(msg)
	case rexCheckTickMsg:
		return m.handleRexCheckTick(msg)
	case noticeTickMsg:
		return m.handleNoticeTick()
	}
	return m, nil
}

// staleStream reports whether a message belongs to an older stream
// generation. A zero generation means unspecified (used by tests).
func (m *Model) staleStream(generation int) bool {
	return generation != 0 && generation != m.state.streamGeneration
}

func (m *Model) applyEffects(e Effects) (*Model, tea.Cmd) {
	var cmds []tea.Cmd
	if e.Quit {
		cmds = append(cmds, tea.Quit)
	}
	if e.Refresh {
		if e.Reconnect && m.hub != nil {
			m.hub.Reconnect()
			m.adoptStreamGeneration()
		}
		cmds = append(cmds, m.refreshNow())
	}
	if e.Switch != nil {
		m.state.pending = true
		m.state.pendingLabel = "switching…"
		cmds = append(cmds, switchCmd(m.ctx, m.state.cfg, *e.Switch))
	}
	if e.SetStatus != nil {
		m.state.pending = true
		m.state.pendingLabel = "saving status…"
		cmds = append(cmds, statusCmd(m.ctx, m.state.cfg, *e.SetStatus))
	}
	if e.PreflightRemove != nil {
		m.state.pending = true
		m.state.pendingLabel = "checking…"
		cmds = append(cmds, preflightRemoveCmd(m.ctx, m.state.cfg, *e.PreflightRemove))
	}
	if e.ExecuteRemove != nil {
		plan := *e.ExecuteRemove
		m.state.pending = true
		m.state.pendingLabel = "removing…"
		cmds = append(cmds, closeSessionCmd(m.ctx, m.state.cfg, plan))
	}
	if e.Create != nil {
		m.state.pending = true
		m.state.pendingLabel = "preparing…"
		cmds = append(cmds, prepareCreateCmd(m.ctx, m.state.cfg, *e.Create))
	}
	if e.BeginCleanup {
		token := 0
		if m.state.cleanup != nil {
			token = m.state.cleanup.token
		}
		cmds = append(cmds, cleanupPlanCmd(m.ctx, m.state.cfg, token))
	}
	if e.ApplyCleanup != nil {
		report := *e.ApplyCleanup
		cmds = append(cmds, applyCleanupCmd(m.ctx, m.state.cfg, report))
	}
	return m, batch(cmds)
}

func batch(cmds []tea.Cmd) tea.Cmd {
	nonNil := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			nonNil = append(nonNil, cmd)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return tea.Batch(nonNil...)
}

// loadedMsg is the result of a full config/state/Git/Rex load.
type loadedMsg struct {
	cfg               config.Config
	workstreams       []core.Workstream
	sessions          []core.Session
	discoveryWarnings []string
	configWarnings    []string
	sessionWarnings   []string
	rexAvailable      bool
	fatalKind         string
	fatalErr          error
	loadErr           error
}

// discoverProjects is ops.Discover. It is a variable so tests can supply a
// deterministic worktree list without a real repository.
var discoverProjects = ops.Discover

// buildWorkstreams builds the dashboard rows with core.BuildWithWarnings and
// folds skipped-worktree warnings into the per-project discovery warning
// channel, so an uncanonicalizable worktree is named instead of vanishing.
// Build warnings come first: a project that cannot be read at all hides every
// worktree in it, so its discovery error keeps the banner slot over a single
// skipped worktree. Duplicate texts are dropped so one condition is reported
// once.
func buildWorkstreams(inputs []core.Input, state config.State, sessions []core.Session, cfg config.Config, discoveryErrors []ops.ProjectError) ([]core.Workstream, []string) {
	workstreams, buildWarnings := core.BuildWithWarnings(inputs, state, sessions, cfg)
	discoveryWarnings := make([]string, 0, len(discoveryErrors))
	for _, projectErr := range discoveryErrors {
		discoveryWarnings = append(discoveryWarnings, projectErr.Error())
	}
	return workstreams, mergeStrings(buildWarnings, discoveryWarnings)
}

func loadCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return loadedMsg{fatalKind: "config", fatalErr: err}
		}
		state, err := config.LoadState(cfg.StatusIDs())
		if err != nil {
			return loadedMsg{cfg: cfg, fatalKind: "state", fatalErr: err}
		}
		discovery := discoverProjects(ctx, cfg)
		rexState := ops.ReadRex(ctx, cfg)

		var sessions []core.Session
		if rexState.Err == nil {
			sessions = rexState.Sessions
		}
		workstreams, discoveryWarnings := buildWorkstreams(discovery.Inputs, state, sessions, cfg, discovery.Errors)
		return loadedMsg{
			cfg:               cfg,
			workstreams:       workstreams,
			sessions:          sessions,
			discoveryWarnings: discoveryWarnings,
			configWarnings:    append([]string(nil), cfg.Warnings...),
			sessionWarnings:   append([]string(nil), rexState.Warnings...),
			rexAvailable:      rexState.Err == nil,
			loadErr:           err,
		}
	}
}

func (m *Model) handleLoaded(msg loadedMsg) (*Model, tea.Cmd) {
	m.state.loading = false
	if msg.fatalErr != nil {
		m.state.fatalKind = msg.fatalKind
		m.state.fatalErr = msg.fatalErr
		m.state.mode = ModeFatal
		m.state.reloadWanted = false
		m.state.rexCheckPending = false
		m.state.rexCheckToken++
		if m.hub != nil {
			m.hub.Close()
			m.hub = nil
		}
		return m, nil
	}

	wasFatal := m.state.mode == ModeFatal
	m.state.cfg = msg.cfg
	m.state.haveConfig = true
	m.state.fatalKind = ""
	m.state.fatalErr = nil
	if wasFatal {
		m.state.mode = ModeNormal
	}

	m.state.rexAvailable = msg.rexAvailable
	if msg.loadErr != nil {
		m.state = m.state.addError(msg.loadErr.Error())
	} else {
		useLoaded := true
		if !msg.rexAvailable && m.state.rexEverLoaded && len(m.state.workstreams) > 0 {
			// Keep the last-known rows and badges during a Rex outage.
			useLoaded = false
		}
		if useLoaded {
			m.state.workstreams = msg.workstreams
		}
		if msg.rexAvailable {
			m.state.rexEverLoaded = true
			m.state.sessions = msg.sessions
		}
	}
	ambiguity := analyzeAmbiguity(m.state.sessions, m.state.workstreams)
	m.state.ambiguousSessions = ambiguity.sessions
	m.state.ambiguousLabels = ambiguity.labels
	warnings := make([]persistentWarning, 0, len(ambiguity.warnings)+len(msg.discoveryWarnings)+len(msg.configWarnings)+len(msg.sessionWarnings))
	warnings = append(warnings, ambiguity.warnings...)
	for _, text := range msg.discoveryWarnings {
		warnings = append(warnings, persistentWarning{text: text, class: warningDiscovery})
	}
	for _, text := range msg.configWarnings {
		warnings = append(warnings, persistentWarning{text: text, class: warningConfig})
	}
	for _, text := range msg.sessionWarnings {
		warnings = append(warnings, persistentWarning{text: text, class: warningSession})
	}
	m.state = m.state.setPersistentWarnings(warnings)
	m.state = m.state.pruneStreamState()
	m.state = m.state.rebuild()

	var cmds []tea.Cmd
	if m.hub == nil {
		m.hub = newStreamHub(msg.cfg.RexBinary())
		m.hub.Start()
		cmds = append(cmds, waitForStream(m.hub))
	} else {
		m.hub.SetBin(msg.cfg.RexBinary())
	}
	if msg.rexAvailable {
		m.hub.SyncSessions(liveSessionIDs(m.state.workstreams))
	} else {
		m.hub.SyncSessions(nil)
	}
	m.adoptStreamGeneration()
	m.state.lastLoadAt = m.now()
	m.state.lastLoadHealthy = msg.rexAvailable && msg.loadErr == nil

	// A refresh requested while this load was in flight must not be lost: the
	// in-flight result may predate a Rex recovery.
	if m.state.reloadWanted {
		m.state.reloadWanted = false
		m.state.loading = true
		cmds = append(cmds, loadCmd(m.ctx))
	}
	return m, batch(cmds)
}

// adoptStreamGeneration follows a hub restart: messages from the old streams
// are invalid, and visible stream health is re-learned from scratch.
func (m *Model) adoptStreamGeneration() {
	if m.hub == nil {
		return
	}
	generation := m.hub.Generation()
	if generation == m.state.streamGeneration {
		return
	}
	m.state.streamGeneration = generation
	m.state = m.state.resetStreamState()
	m.state = m.state.rebuild()
}

func liveSessionIDs(workstreams []core.Workstream) []string {
	seen := make(map[string]bool, len(workstreams))
	ids := make([]string, 0, len(workstreams))
	for _, ws := range workstreams {
		if ws.RexSessionID == "" || seen[ws.RexSessionID] {
			continue
		}
		seen[ws.RexSessionID] = true
		ids = append(ids, ws.RexSessionID)
	}
	return ids
}

// ambiguityAnalysis is the ambiguity condition in both directions plus its
// persistent banner copy. Each direction produces at most one warning; Help
// carries the full list of affected sessions or labels.
type ambiguityAnalysis struct {
	sessions map[string]bool // session ID bound to more than one workstream
	labels   map[string]bool // label shared by more than one live session
	warnings []persistentWarning
}

// analyzeAmbiguity compares all live session labels with the workstreams that
// bind to them. Workstreams keep binding to the first session with their label
// (core.Build); this only warns.
func analyzeAmbiguity(sessions []core.Session, workstreams []core.Workstream) ambiguityAnalysis {
	out := ambiguityAnalysis{
		sessions: map[string]bool{},
		labels:   map[string]bool{},
	}

	labelByID := make(map[string]string, len(sessions))
	for _, session := range sessions {
		if session.ID != "" && labelByID[session.ID] == "" {
			labelByID[session.ID] = session.Label
		}
	}

	// Direction one: a live session bound to several workstreams.
	bySession := make(map[string]int, len(workstreams))
	for _, ws := range workstreams {
		if ws.RexSessionID != "" {
			bySession[ws.RexSessionID]++
		}
	}
	type group struct {
		key   string
		count int
	}
	var shared []group
	largest := 0
	for sessionID, count := range bySession {
		if count <= 1 {
			continue
		}
		out.sessions[sessionID] = true
		shared = append(shared, group{key: sessionID, count: count})
		if count > largest {
			largest = count
		}
	}
	if largest > 0 {
		sort.Slice(shared, func(i, j int) bool { return shared[i].key < shared[j].key })
		details := make([]string, 0, len(shared))
		for _, item := range shared {
			label := render.Sanitize(labelByID[item.key])
			if label == "" {
				label = render.Sanitize(item.key)
			}
			details = append(details, fmt.Sprintf("%s (%d workstreams)", label, item.count))
		}
		out.warnings = append(out.warnings, persistentWarning{
			text:   fmt.Sprintf("ambiguous: %d workstreams, one live session", largest),
			detail: "Affected sessions: " + strings.Join(details, "; "),
			class:  warningAmbiguity,
		})
	}

	// Direction two: several distinct live sessions sharing one label. Duplicate
	// listings of the same session ID are not ambiguous.
	seenIDs := make(map[string]bool, len(sessions))
	byLabel := make(map[string]int, len(sessions))
	for _, session := range sessions {
		if session.ID != "" {
			if seenIDs[session.ID] {
				continue
			}
			seenIDs[session.ID] = true
		}
		byLabel[session.Label]++
	}
	var duplicates []group
	largest = 0
	for label, count := range byLabel {
		if count <= 1 {
			continue
		}
		out.labels[label] = true
		duplicates = append(duplicates, group{key: label, count: count})
		if count > largest {
			largest = count
		}
	}
	if largest > 0 {
		sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].key < duplicates[j].key })
		details := make([]string, 0, len(duplicates))
		for _, item := range duplicates {
			details = append(details, fmt.Sprintf("%s (%d live sessions)", render.Sanitize(item.key), item.count))
		}
		out.warnings = append(out.warnings, persistentWarning{
			text:   fmt.Sprintf("ambiguous: %d live sessions, one label", largest),
			detail: "Duplicate labels: " + strings.Join(details, "; "),
			class:  warningAmbiguity,
		})
	}
	return out
}

// switchDoneMsg reports a session/window switch.
type switchDoneMsg struct {
	request SwitchRequest
	err     error
}

func switchCmd(ctx context.Context, cfg config.Config, request SwitchRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := ops.SwitchWorkstream(ctx, cfg, request.SessionName, request.Path, request.WindowID)
		return switchDoneMsg{request: request, err: err}
	}
}

func (m *Model) handleSwitchDone(msg switchDoneMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	var cmds []tea.Cmd
	if msg.err != nil {
		m.state = m.state.addError("switch: " + msg.err.Error())
	} else if msg.request.ExpandPath != "" {
		m.state.expanded = cloneSet(m.state.expanded)
		m.state.expanded[msg.request.ExpandPath] = true
		m.state.cursorKey = RowKey{Kind: RowWorkstream, ID: msg.request.ExpandPath}
	}
	m.state = m.state.rebuild()
	cmds = append(cmds, m.refreshNow())
	return m, batch(cmds)
}

// statusDoneMsg reports a status change.
type statusDoneMsg struct {
	request  StatusRequest
	warnings []string
	err      error
}

func statusCmd(ctx context.Context, cfg config.Config, request StatusRequest) tea.Cmd {
	return func() tea.Msg {
		_, warnings, err := ops.SetStatusWithWarnings(ctx, cfg, request.Path, request.Status)
		return statusDoneMsg{request: request, warnings: warnings, err: err}
	}
}

func (m *Model) handleStatusDone(msg statusDoneMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	var cmds []tea.Cmd
	if msg.err != nil {
		m.state = m.state.addError("status: " + msg.err.Error())
	} else {
		// Follow the workstream even if it moved to another status group.
		m.state.cursorKey = RowKey{Kind: RowWorkstream, ID: msg.request.Path}
	}
	// A skipped worktree warning that explains this request is already shown
	// persistently by the last load; only new conditions become a notice. The
	// refresh below will report them persistently either way.
	now := m.now()
	for _, warning := range msg.warnings {
		if !m.state.hasPersistentWarning(warning, now) {
			m.state = m.state.addWarning(warning)
		}
	}
	m.state = m.state.rebuild()
	cmds = append(cmds, m.refreshNow())
	return m, batch(cmds)
}

// removePreparedMsg carries the pre-check result.
type removePreparedMsg struct {
	plan ops.RemovePlan
	err  error
}

func preflightRemoveCmd(ctx context.Context, cfg config.Config, request RemoveRequest) tea.Cmd {
	return func() tea.Msg {
		// The confirmation screen is the TUI's consent for a forced removal:
		// it lists every path and commit the plan would discard. The branch is
		// always retained from the TUI.
		plan, err := ops.PrepareRemoveWithOptions(ctx, cfg, request.Path, ops.RemoveOptions{Force: true})
		return removePreparedMsg{plan: plan, err: err}
	}
}

func (m *Model) handleRemovePrepared(msg removePreparedMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	if msg.err != nil {
		m.state = m.state.addError(msg.err.Error())
		return m, nil
	}
	m.state.remove = &removeModal{
		plan:    msg.plan,
		project: msg.plan.Project.Name,
		branch:  msg.plan.Branch,
	}
	m.state.mode = ModeRemove
	return m, nil
}

// cleanupPlannedMsg carries a cleanup preview. Apply is true so the plan is
// apply-capable: it refuses when Rex is unavailable and never misses a live
// session. Force is true so every dirty or unmerged item has its loss and
// action populated; the TUI gates selecting those items on its own force flag.
type cleanupPlannedMsg struct {
	token  int
	report ops.CleanupReport
	err    error
}

func cleanupPlanCmd(ctx context.Context, cfg config.Config, token int) tea.Cmd {
	return func() tea.Msg {
		report, err := ops.PlanCleanup(ctx, cfg, ops.CleanupOptions{Apply: true, Force: true})
		return cleanupPlannedMsg{token: token, report: report, err: err}
	}
}

func (m *Model) handleCleanupPlanned(msg cleanupPlannedMsg) (*Model, tea.Cmd) {
	view := m.state.cleanup
	if view == nil || view.token != msg.token || !view.loading {
		// The screen was closed or a newer scan replaced this one.
		return m, nil
	}
	view.loading = false
	if msg.err != nil {
		view.err = msg.err.Error()
		view.report = ops.CleanupReport{}
		view.items = nil
		view.rows = nil
		view.cursor = -1
		return m, nil
	}
	view.err = ""
	view.report = msg.report
	view.applied = false
	view.force = false
	view.selected = map[cleanupItemKey]bool{}
	m.state = m.state.rebuildCleanup()
	for _, warning := range msg.report.Warnings {
		m.state = m.state.addWarning(warning)
	}
	for _, text := range msg.report.Errors {
		m.state = m.state.addError(text)
	}
	return m, nil
}

// cleanupAppliedMsg carries the report after ApplyCleanup. The report contains
// every selected item's result; the preview is merged with it so the screen can
// show removed, failed, and kept rows in place.
type cleanupAppliedMsg struct {
	report ops.CleanupReport
	err    error
}

func applyCleanupCmd(ctx context.Context, cfg config.Config, report ops.CleanupReport) tea.Cmd {
	return func() tea.Msg {
		applied, err := ops.ApplyCleanup(ctx, cfg, report)
		return cleanupAppliedMsg{report: applied, err: err}
	}
}

func (m *Model) handleCleanupApplied(msg cleanupAppliedMsg) (*Model, tea.Cmd) {
	view := m.state.cleanup
	if view == nil {
		if msg.err != nil {
			m.state = m.state.addError("cleanup: " + msg.err.Error())
		}
		return m, m.refreshNow()
	}
	view.loading = false
	view.applying = false
	view.confirm = nil
	if msg.err != nil {
		view.err = msg.err.Error()
		m.state = m.state.addError("cleanup: " + msg.err.Error())
	} else {
		view.err = ""
	}
	if !msg.report.Applied {
		// ApplyCleanup refused before mutating anything (for example its Rex
		// gate). Keep the preview and selection so the user can retry, and never
		// claim a successful apply.
		return m, m.refreshNow()
	}
	view.report = mergeCleanupResults(view.report, msg.report)
	view.applied = true
	view.selected = map[cleanupItemKey]bool{}
	m.state = m.state.rebuildCleanup()
	totals := msg.report.Totals
	info := fmt.Sprintf("cleanup: %d removed, %d failed, %d kept", totals.Removed, totals.Failed, totals.Kept)
	if retained := cleanupRetainedBranches(view.report); len(retained) > 0 {
		info += fmt.Sprintf("; %d branch(es) retained", len(retained))
	}
	m.state = m.state.addInfo(info)
	return m, m.refreshNow()
}

// removeSessionClosedMsg reports that the Rex session was closed.
type removeSessionClosedMsg struct {
	plan     ops.RemovePlan
	closedID string
	err      error
}

func closeSessionCmd(ctx context.Context, cfg config.Config, plan ops.RemovePlan) tea.Cmd {
	return func() tea.Msg {
		// Re-check the worktree and session immediately before the first
		// mutation, exactly as ExecuteRemove does for the CLI.
		if err := plan.VerifyRemoval(ctx, cfg); err != nil {
			return removeSessionClosedMsg{plan: plan, err: err}
		}
		closed, err := plan.CloseSession(ctx, cfg)
		return removeSessionClosedMsg{plan: plan, closedID: closed, err: err}
	}
}

func (m *Model) handleRemoveSessionClosed(msg removeSessionClosedMsg) (*Model, tea.Cmd) {
	if msg.err != nil {
		m.state.pending = false
		m.state.pendingLabel = ""
		m.state = m.state.addError(msg.err.Error())
		return m, m.refreshNow()
	}
	if err := msg.plan.VerifyRemoval(m.ctx, m.state.cfg); err != nil {
		m.state.pending = false
		m.state.pendingLabel = ""
		m.state = m.state.addError(err.Error())
		return m, m.refreshNow()
	}
	cmd, err := msg.plan.Cmd()
	if err != nil {
		m.state.pending = false
		m.state.pendingLabel = ""
		m.state = m.state.addError(err.Error())
		return m, m.refreshNow()
	}
	m.state.pendingLabel = "removing…"
	plan := msg.plan
	closedID := msg.closedID
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return removeExecDoneMsg{plan: plan, closedID: closedID, err: err}
	})
}

// removeExecDoneMsg reports the provider remove process exiting.
type removeExecDoneMsg struct {
	plan     ops.RemovePlan
	closedID string
	err      error
}

func (m *Model) handleRemoveExecDone(msg removeExecDoneMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	var cmds []tea.Cmd
	if msg.err != nil {
		m.state = m.state.addError("provider remove: " + msg.err.Error())
	} else if leftover, verifyErr := msg.plan.VerifyRemoved(m.ctx); verifyErr != nil {
		m.state = m.state.addError(verifyErr.Error())
	} else {
		if err := msg.plan.Cleanup(m.state.cfg); err != nil {
			m.state = m.state.addError(err.Error())
		}
		if leftover != "" {
			m.state = m.state.addWarning(leftover)
		}
		if warning := msg.plan.MissingBranchWarning(m.ctx); warning != "" {
			m.state = m.state.addWarning(warning)
		}
		if retained := msg.plan.RetainedBranch(m.ctx); retained != nil {
			m.state = m.state.addInfo(retainedBranchNotice(retained))
		}
	}
	cmds = append(cmds, m.refreshNow())
	return m, batch(cmds)
}

// retainedBranchNotice is the TUI form of the CLI's retained-branch report.
func retainedBranchNotice(branch *ops.RetainedBranch) string {
	if branch.Error != "" {
		return "Retained branch " + branch.Branch + ": state unknown (" + branch.Error + ")"
	}
	switch branch.State {
	case ops.CleanupBranchMerged:
		return "Retained branch " + branch.Branch + ": merged into " + branch.DefaultBranch + "; no unmerged commits."
	case ops.CleanupBranchPatchEquivalent:
		return "Retained branch " + branch.Branch + ": patch-equivalent to " + branch.DefaultBranch + "."
	case ops.CleanupBranchUnmerged:
		return fmt.Sprintf("Retained branch %s: unmerged, %d commit(s) not in %s.", branch.Branch, len(branch.Commits), branch.DefaultBranch)
	default:
		return "Retained branch " + branch.Branch + "."
	}
}

// createPreparedMsg carries the validated, rendered create plan.
type createPreparedMsg struct {
	plan ops.CreatePlan
	err  error
}

func prepareCreateCmd(ctx context.Context, cfg config.Config, request CreateRequest) tea.Cmd {
	return func() tea.Msg {
		plan, err := ops.PrepareCreate(ctx, cfg, request.Project, request.Branch)
		return createPreparedMsg{plan: plan, err: err}
	}
}

func (m *Model) handleCreatePrepared(msg createPreparedMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	if msg.err != nil {
		m.state = m.state.addError(msg.err.Error())
		return m, nil
	}
	cmd, err := msg.plan.Cmd()
	if err != nil {
		m.state = m.state.addError(err.Error())
		return m, nil
	}
	m.state.createPlan = &msg.plan
	m.state.mode = ModeNormal
	m.state.pending = true
	m.state.pendingLabel = "creating…"
	plan := msg.plan
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return createExecDoneMsg{plan: plan, err: err}
	})
}

// createExecDoneMsg reports the provider create process exiting.
type createExecDoneMsg struct {
	plan ops.CreatePlan
	err  error
}

func (m *Model) handleCreateExecDone(msg createExecDoneMsg) (*Model, tea.Cmd) {
	if msg.err != nil {
		m.state.pending = false
		m.state.pendingLabel = ""
		m.state = m.state.addError("provider create: " + msg.err.Error())
		return m, m.refreshNow()
	}
	m.state.pendingLabel = "verifying…"
	plan := msg.plan
	return m, func() tea.Msg {
		path, err := plan.Verify(m.ctx)
		return createVerifiedMsg{plan: plan, path: path, err: err}
	}
}

// createVerifiedMsg reports post-create verification.
type createVerifiedMsg struct {
	plan ops.CreatePlan
	path string
	err  error
}

func (m *Model) handleCreateVerified(msg createVerifiedMsg) (*Model, tea.Cmd) {
	m.state.pending = false
	m.state.pendingLabel = ""
	m.state.createPlan = nil
	var cmds []tea.Cmd
	if msg.err != nil {
		m.state = m.state.addError(msg.err.Error())
	} else {
		m.state.cursorKey = RowKey{Kind: RowWorkstream, ID: msg.path}
	}
	cmds = append(cmds, m.refreshNow())
	return m, batch(cmds)
}

// refreshNow starts a data load unless one is already running. A request that
// arrives during a load is remembered and re-issued when that load finishes.
func (m *Model) refreshNow() tea.Cmd {
	if m.state.loading {
		m.state.reloadWanted = true
		return nil
	}
	m.state.loading = true
	return loadCmd(m.ctx)
}

// backgroundRefresh is refreshNow with clause clarity at event call sites.
func (m *Model) backgroundRefresh() tea.Cmd { return m.refreshNow() }

// sessionAgentsMsg carries a per-session agent refresh.
type sessionAgentsMsg struct {
	sessionID string
	label     string
	agents    []core.Agent
	warnings  []string
	err       error
}

func agentsRefreshCmd(ctx context.Context, cfg config.Config, sessionID, label string) tea.Cmd {
	return func() tea.Msg {
		blocks, err := rex.New(cfg.RexBinary()).SessionAgents(ctx, sessionID)
		if err != nil {
			return sessionAgentsMsg{sessionID: sessionID, label: label, err: err}
		}
		agents, warnings := ops.AgentsFromBlocks(label, blocks)
		return sessionAgentsMsg{sessionID: sessionID, label: label, agents: agents, warnings: warnings}
	}
}

func (m *Model) handleSessionAgents(msg sessionAgentsMsg) (*Model, tea.Cmd) {
	indexes := m.sessionIndexes(msg.sessionID)
	if len(indexes) == 0 {
		return m, nil
	}
	if msg.err != nil {
		m.state = m.state.addError(fmt.Sprintf("session %s: %v", msg.label, msg.err))
		return m, nil
	}
	for _, index := range indexes {
		ws := m.state.workstreams[index]
		ws.Agents = append([]core.Agent{}, msg.agents...)
		ws.AgentState, ws.AgentDetail = core.Summarize(ws.Agents)
		m.state = m.state.replaceWorkstream(index, ws)
	}
	for _, warning := range msg.warnings {
		m.state = m.state.addError(warning)
	}
	return m, nil
}

// sessionWindowsMsg carries a per-session window refresh.
type sessionWindowsMsg struct {
	sessionID string
	label     string
	windows   []core.Window
	err       error
}

func windowsRefreshCmd(ctx context.Context, cfg config.Config, sessionID, label string) tea.Cmd {
	return func() tea.Msg {
		windows, err := rex.New(cfg.RexBinary()).SessionWindows(ctx, sessionID)
		if err != nil {
			return sessionWindowsMsg{sessionID: sessionID, label: label, err: err}
		}
		return sessionWindowsMsg{sessionID: sessionID, label: label, windows: ops.WindowsFromRex(windows)}
	}
}

func (m *Model) handleSessionWindows(msg sessionWindowsMsg) (*Model, tea.Cmd) {
	indexes := m.sessionIndexes(msg.sessionID)
	if len(indexes) == 0 {
		return m, nil
	}
	if msg.err != nil {
		m.state = m.state.addError(fmt.Sprintf("session %s windows: %v", msg.label, msg.err))
		return m, nil
	}
	for _, index := range indexes {
		ws := m.state.workstreams[index]
		ws.Windows = append([]core.Window{}, msg.windows...)
		m.state = m.state.replaceWorkstream(index, ws)
	}
	return m, nil
}

// windowRefreshTickMsg is a debounced window refresh.
type windowRefreshTickMsg struct {
	sessionID string
	token     int
}

func (m *Model) scheduleWindowRefresh(sessionID string) tea.Cmd {
	if sessionID == "" || m.state.refreshPending[sessionID] {
		return nil
	}
	m.state.refreshPending[sessionID] = true
	m.state.refreshSeq[sessionID]++
	token := m.state.refreshSeq[sessionID]
	m.state.refreshToken[sessionID] = token
	return m.tick(200*time.Millisecond, func(time.Time) tea.Msg {
		return windowRefreshTickMsg{sessionID: sessionID, token: token}
	})
}

func (m *Model) handleWindowRefreshTick(msg windowRefreshTickMsg) (*Model, tea.Cmd) {
	if !m.state.refreshPending[msg.sessionID] || m.state.refreshToken[msg.sessionID] != msg.token {
		return m, nil
	}
	m.state.refreshPending[msg.sessionID] = false
	indexes := m.sessionIndexes(msg.sessionID)
	if len(indexes) == 0 {
		return m, nil
	}
	ws := m.state.workstreams[indexes[0]]
	return m, windowsRefreshCmd(m.ctx, m.state.cfg, msg.sessionID, ws.SessionName)
}

// noticeTickMsg prunes expired transient notices.
type noticeTickMsg struct{}

func noticeTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return noticeTickMsg{} })
}

func (m *Model) handleNoticeTick() (*Model, tea.Cmd) {
	m.noticeTicking = false
	m.state = m.state.pruneNotices(time.Now())
	return m, nil
}

// sessionIndexes returns every workstream bound to a Rex session ID. Two
// workstreams can resolve to one session, so updates must reach all of them.
func (m *Model) sessionIndexes(sessionID string) []int {
	if sessionID == "" {
		return nil
	}
	var indexes []int
	for i, ws := range m.state.workstreams {
		if ws.RexSessionID == sessionID {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// handleStreamEvent applies one Rex event. It returns an optional command to
// refresh agents or windows. Stream health changes only through
// streamStateMsg, never an arbitrary event.
func (m *Model) handleStreamEvent(event rex.Event) tea.Cmd {
	switch event.Type {
	case rex.EventProgramStatusChanged:
		return m.applyProgramStatus(event)
	case rex.EventProgramStatusRemoved:
		indexes := m.sessionIndexes(event.SessionID)
		if len(indexes) == 0 {
			return m.backgroundRefresh()
		}
		ws := m.state.workstreams[indexes[0]]
		return agentsRefreshCmd(m.ctx, m.state.cfg, event.SessionID, ws.SessionName)

	case rex.EventSessionCreated, rex.EventSessionDestroyed:
		if event.Type == rex.EventSessionDestroyed {
			delete(m.state.refreshPending, event.SessionID)
			delete(m.state.refreshToken, event.SessionID)
			delete(m.state.refreshSeq, event.SessionID)
		}
		return m.backgroundRefresh()

	case rex.EventSessionViewChanged,
		rex.EventActiveWindowChanged,
		rex.EventWindowCreated,
		rex.EventWindowClosed,
		rex.EventWindowLabelChanged:
		return m.scheduleWindowRefresh(event.SessionID)
	}
	return nil
}

func (m *Model) applyProgramStatus(event rex.Event) tea.Cmd {
	if event.Record == nil {
		return nil
	}
	indexes := m.sessionIndexes(event.SessionID)
	if len(indexes) == 0 {
		return m.backgroundRefresh()
	}
	needRefresh := false
	for _, index := range indexes {
		ws := m.state.workstreams[index]
		found := false
		for i := range ws.Agents {
			if ws.Agents[i].BlockID == event.BlockID {
				applyStatusRecord(&ws.Agents[i], *event.Record)
				found = true
				break
			}
		}
		if !found {
			needRefresh = true
			continue
		}
		ws.AgentState, ws.AgentDetail = core.Summarize(ws.Agents)
		m.state = m.state.replaceWorkstream(index, ws)
	}
	if needRefresh {
		ws := m.state.workstreams[indexes[0]]
		return agentsRefreshCmd(m.ctx, m.state.cfg, event.SessionID, ws.SessionName)
	}
	return nil
}

func applyStatusRecord(agent *core.Agent, record rex.StatusRecord) {
	state := agentstate.Normalize(record.State)
	agent.State = state
	agent.Detail = ""
	if state == core.AgentBlocked {
		agent.Detail = record.Kind
	}
	agent.App = record.App
	agent.EffectiveApp = record.EffectiveApp
	agent.Msg = record.Msg
	agent.Title = record.Title
	agent.UpdatedAt = record.UpdatedAt
	agent.Error = ""
}

func (m *Model) handleStreamState(msg streamStateMsg) tea.Cmd {
	switch {
	case msg.connecting:
		// A reconnect attempt is underway. Cancel the pause debounce so a
		// reconnect that lands inside the 2s window cannot flash a pause, but
		// leave an already-visible pause until a real connect. The server-wide
		// stream does not pre-empt the availability probe: only a real connect
		// cancels it.
		if msg.sessionID != "" {
			m.state = m.state.markStreamConnecting(msg.sessionID)
		}
		return nil

	case msg.connected:
		wasPaused := m.state.streamPaused[msg.sessionID]
		m.state = m.state.markStreamConnected(msg.sessionID)
		if msg.sessionID == "" {
			// The server-wide stream is an availability probe: reconnect proves
			// Rex is reachable, and a debounced reload reconciles anything that
			// happened while it was down.
			wasAvailable := m.state.rexAvailable
			m.state.rexAvailable = true
			if !wasAvailable {
				m.state = m.state.rebuild()
			}
			// A healthy load that just completed already covers this reconnect,
			// so startup and `r` must not cause a redundant second load.
			if m.state.lastLoadHealthy && !m.state.lastLoadAt.IsZero() &&
				m.now().Sub(m.state.lastLoadAt) < rexCooldown {
				return nil
			}
			return m.scheduleRexCheck()
		}
		if wasPaused {
			m.state = m.state.rebuild()
		}
		return nil
	}

	// Disconnected.
	if msg.sessionID == "" {
		// Probe Rex's availability once, and only while it is believed to be
		// up. Repeated stream retries must not storm the disk.
		if m.state.rexAvailable {
			return m.scheduleRexCheck()
		}
		return nil
	}
	return m.schedulePauseCheck(msg.sessionID)
}

// rexCheckDelay debounces the availability probe/reconcile so a flapping
// server-wide stream causes at most one load. rexCooldown bounds the rate
// further: a new check is deferred, never dropped.
const (
	rexCheckDelay = time.Second
	rexCooldown   = 5 * time.Second
)

type rexCheckTickMsg struct{ token int }

// scheduleRexCheck schedules one debounced reload at least rexCheckDelay away
// and no sooner than rexCooldown after the last check. A newer signal replaces
// an older pending check, so flapping coalesces into a single load.
func (m *Model) scheduleRexCheck() tea.Cmd {
	now := m.now()
	target := now.Add(rexCheckDelay)
	if earliest := m.state.lastRexCheck.Add(rexCooldown); earliest.After(target) {
		target = earliest
	}
	delay := target.Sub(now)
	if delay < 0 {
		delay = 0
	}
	m.state.rexCheckSeq++
	token := m.state.rexCheckSeq
	m.state.rexCheckToken = token
	m.state.rexCheckPending = true
	return m.tick(delay, func(time.Time) tea.Msg {
		return rexCheckTickMsg{token: token}
	})
}

func (m *Model) handleRexCheckTick(msg rexCheckTickMsg) (*Model, tea.Cmd) {
	if !m.state.rexCheckPending || m.state.rexCheckToken != msg.token {
		return m, nil
	}
	m.state.rexCheckPending = false
	m.state.lastRexCheck = m.now()
	return m, m.refreshNow()
}

// pauseTickMsg flips a session's pause marker on after the 2s debounce.
type pauseTickMsg struct {
	sessionID string
	token     int
}

// pauseDebounce is the visible-pause debounce.
const pauseDebounce = 2 * time.Second

// schedulePauseCheck arms the pause tick at an absolute deadline. The deadline
// is set on the first disconnect after a connect and survives reconnect
// attempts, so repeated failing retries cannot push the visible pause past 2s.
func (m *Model) schedulePauseCheck(sessionID string) tea.Cmd {
	if sessionID == "" || m.state.streamPaused[sessionID] {
		return nil
	}
	now := m.now()
	due, ok := m.state.pauseDue[sessionID]
	if !ok || due.IsZero() {
		due = now.Add(pauseDebounce)
		m.state.pauseDue[sessionID] = due
	}
	delay := due.Sub(now)
	if delay < 0 {
		delay = 0
	}
	m.state.pausePending[sessionID] = true
	m.state.pauseSeq[sessionID]++
	token := m.state.pauseSeq[sessionID]
	m.state.pauseToken[sessionID] = token
	return m.tick(delay, func(time.Time) tea.Msg {
		return pauseTickMsg{sessionID: sessionID, token: token}
	})
}

func (m *Model) handlePauseTick(msg pauseTickMsg) (*Model, tea.Cmd) {
	if !m.state.pausePending[msg.sessionID] || m.state.pauseToken[msg.sessionID] != msg.token {
		return m, nil
	}
	m.state.pausePending[msg.sessionID] = false
	delete(m.state.pauseDue, msg.sessionID)
	m.state.streamPaused[msg.sessionID] = true
	m.state = m.state.rebuild()
	return m, nil
}
