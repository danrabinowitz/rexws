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
	"github.com/danrabinowitz/rexws/internal/rex"
)

// Model is the Bubbletea model. It owns the pure State plus the event hub.
type Model struct {
	state         State
	hub           *streamHub
	ctx           context.Context
	cancel        context.CancelFunc
	noticeTicking bool
}

// NewModel returns a dashboard that loads its data on Init.
func NewModel() *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{state: newState(), ctx: ctx, cancel: cancel}
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
		cmd := m.handleStreamEvent(msg.event)
		return m, batch([]tea.Cmd{cmd, waitForStream(m.hub)})
	case streamStateMsg:
		if m.hub == nil {
			return m, nil
		}
		cmd := m.handleStreamState(msg)
		return m, batch([]tea.Cmd{cmd, waitForStream(m.hub)})
	case sessionAgentsMsg:
		return m.handleSessionAgents(msg)
	case sessionWindowsMsg:
		return m.handleSessionWindows(msg)
	case windowRefreshTickMsg:
		return m.handleWindowRefreshTick(msg)
	case noticeTickMsg:
		return m.handleNoticeTick()
	}
	return m, nil
}

func (m *Model) applyEffects(e Effects) (*Model, tea.Cmd) {
	var cmds []tea.Cmd
	if e.Quit {
		cmds = append(cmds, tea.Quit)
	}
	if e.Refresh {
		if e.Reconnect && m.hub != nil {
			m.hub.Reconnect()
		}
		if !m.state.loading {
			m.state.loading = true
			cmds = append(cmds, loadCmd(m.ctx))
		}
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
	cfg          config.Config
	workstreams  []core.Workstream
	warnings     []string
	rexAvailable bool
	fatalKind    string
	fatalErr     error
	loadErr      error
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
		discovery := ops.Discover(ctx, cfg)
		rexState := ops.ReadRex(ctx, cfg)

		var workstreams []core.Workstream
		if rexState.Err == nil {
			workstreams, err = core.Build(discovery.Inputs, state, rexState.Sessions, cfg)
		} else {
			workstreams, err = core.Build(discovery.Inputs, state, nil, cfg)
		}
		warnings := make([]string, 0, len(discovery.Errors)+len(rexState.Warnings))
		for _, projectErr := range discovery.Errors {
			warnings = append(warnings, projectErr.Error())
		}
		warnings = append(warnings, rexState.Warnings...)
		if err == nil {
			warnings = append(warnings, duplicateSessionWarnings(workstreams)...)
		}
		return loadedMsg{
			cfg:          cfg,
			workstreams:  workstreams,
			warnings:     warnings,
			rexAvailable: rexState.Err == nil,
			loadErr:      err,
		}
	}
}

func (m *Model) handleLoaded(msg loadedMsg) (*Model, tea.Cmd) {
	m.state.loading = false
	if msg.fatalErr != nil {
		m.state.fatalKind = msg.fatalKind
		m.state.fatalErr = msg.fatalErr
		m.state.mode = ModeFatal
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
	m.state.streamsPaused = !msg.rexAvailable
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
		}
	}
	m.state = m.state.setPersistentWarnings(msg.warnings)
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
	return m, batch(cmds)
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

func duplicateSessionWarnings(workstreams []core.Workstream) []string {
	bySession := make(map[string][]string)
	for _, ws := range workstreams {
		if ws.RexSessionID != "" {
			bySession[ws.RexSessionID] = append(bySession[ws.RexSessionID], ws.Path)
		}
	}
	var warnings []string
	for sessionID, paths := range bySession {
		if len(paths) > 1 {
			sort.Strings(paths)
			warnings = append(warnings, fmt.Sprintf("%d workstreams resolve to Rex session %s: %s", len(paths), sessionID, joinPaths(paths)))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func joinPaths(paths []string) string { return strings.Join(paths, ", ") }

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
	if !m.state.loading {
		m.state.loading = true
		cmds = append(cmds, loadCmd(m.ctx))
	}
	return m, batch(cmds)
}

// statusDoneMsg reports a status change.
type statusDoneMsg struct {
	request StatusRequest
	err     error
}

func statusCmd(ctx context.Context, cfg config.Config, request StatusRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := ops.SetStatus(ctx, cfg, request.Path, request.Status)
		return statusDoneMsg{request: request, err: err}
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
	m.state = m.state.rebuild()
	if !m.state.loading {
		m.state.loading = true
		cmds = append(cmds, loadCmd(m.ctx))
	}
	return m, batch(cmds)
}

// removePreparedMsg carries the pre-check result.
type removePreparedMsg struct {
	plan ops.RemovePlan
	err  error
}

func preflightRemoveCmd(ctx context.Context, cfg config.Config, request RemoveRequest) tea.Cmd {
	return func() tea.Msg {
		plan, err := ops.PrepareRemove(ctx, cfg, request.Path)
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

// removeSessionClosedMsg reports that the Rex session was closed.
type removeSessionClosedMsg struct {
	plan     ops.RemovePlan
	closedID string
	err      error
}

func closeSessionCmd(ctx context.Context, cfg config.Config, plan ops.RemovePlan) tea.Cmd {
	return func() tea.Msg {
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
	} else if err := msg.plan.Cleanup(m.state.cfg); err != nil {
		m.state = m.state.addError(err.Error())
	}
	cmds = append(cmds, m.refreshNow())
	return m, batch(cmds)
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

// refreshNow starts a data load unless one is already running.
func (m *Model) refreshNow() tea.Cmd {
	if m.state.loading {
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
	index := m.sessionIndex(msg.sessionID)
	if index < 0 {
		return m, nil
	}
	if msg.err != nil {
		m.state = m.state.addError(fmt.Sprintf("session %s: %v", msg.label, msg.err))
		return m, nil
	}
	ws := m.state.workstreams[index]
	ws.Agents = msg.agents
	if ws.Agents == nil {
		ws.Agents = []core.Agent{}
	}
	ws.AgentState, ws.AgentDetail = core.Summarize(ws.Agents)
	m.state = m.state.replaceWorkstream(index, ws)
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
	index := m.sessionIndex(msg.sessionID)
	if index < 0 {
		return m, nil
	}
	if msg.err != nil {
		m.state = m.state.addError(fmt.Sprintf("session %s windows: %v", msg.label, msg.err))
		return m, nil
	}
	ws := m.state.workstreams[index]
	ws.Windows = msg.windows
	if ws.Windows == nil {
		ws.Windows = []core.Window{}
	}
	m.state = m.state.replaceWorkstream(index, ws)
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
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg {
		return windowRefreshTickMsg{sessionID: sessionID, token: token}
	})
}

func (m *Model) handleWindowRefreshTick(msg windowRefreshTickMsg) (*Model, tea.Cmd) {
	if !m.state.refreshPending[msg.sessionID] || m.state.refreshToken[msg.sessionID] != msg.token {
		return m, nil
	}
	m.state.refreshPending[msg.sessionID] = false
	index := m.sessionIndex(msg.sessionID)
	if index < 0 {
		return m, nil
	}
	ws := m.state.workstreams[index]
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

func (m *Model) sessionIndex(sessionID string) int {
	if sessionID == "" {
		return -1
	}
	for i, ws := range m.state.workstreams {
		if ws.RexSessionID == sessionID {
			return i
		}
	}
	return -1
}

// handleStreamEvent applies one Rex event. It returns an optional command to
// refresh agents or windows.
func (m *Model) handleStreamEvent(event rex.Event) tea.Cmd {
	wasUnavailable := !m.state.rexAvailable
	m.state.rexAvailable = true
	m.state.streamsPaused = false
	if wasUnavailable {
		return m.backgroundRefresh()
	}

	switch event.Type {
	case rex.EventProgramStatusChanged:
		return m.applyProgramStatus(event)
	case rex.EventProgramStatusRemoved:
		index := m.sessionIndex(event.SessionID)
		if index < 0 {
			return m.backgroundRefresh()
		}
		ws := m.state.workstreams[index]
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
	index := m.sessionIndex(event.SessionID)
	if index < 0 {
		return m.backgroundRefresh()
	}
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
		return agentsRefreshCmd(m.ctx, m.state.cfg, event.SessionID, ws.SessionName)
	}
	ws.AgentState, ws.AgentDetail = core.Summarize(ws.Agents)
	m.state = m.state.replaceWorkstream(index, ws)
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
	if msg.sessionID != "" {
		return nil
	}
	if msg.connected {
		m.state.streamsPaused = false
		if !m.state.rexAvailable {
			m.state.rexAvailable = true
			return m.backgroundRefresh()
		}
		return nil
	}
	m.state.streamsPaused = true
	return nil
}
