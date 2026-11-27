package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/rex"
)

func fakeRexEvents(t *testing.T, serverEvent, sessionEvent string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  "events --json") printf '%s\n' '` + serverEvent + `' ;;
  "events --json -s session:fake") printf '%s\n' '` + sessionEvent + `' ;;
  *) exit 0 ;;
esac
`
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, bin)
}

func TestStoppedWorkstreamEnterCreatesAndExpands(t *testing.T) {
	s := testState(t)
	for s.cursorKey.ID != "/wt/ai" {
		s, _ = Reduce(s, runeKey('j'))
	}
	s, effects := Reduce(s, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Switch == nil {
		t.Fatal("enter on a stopped workstream produced no switch")
	}
	if effects.Switch.WindowID != "" {
		t.Errorf("stopped workstream passed a window: %#v", effects.Switch)
	}
	if effects.Switch.ExpandPath != "/wt/ai" {
		t.Errorf("expand path = %q", effects.Switch.ExpandPath)
	}
	if effects.Switch.SessionName != ".ai/main" {
		t.Errorf("session name = %q", effects.Switch.SessionName)
	}
}

func TestHandleLoadedSurfacesConfigWarnings(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)

	_, _ = m.handleLoaded(loadedMsg{
		cfg:            config.Default(),
		workstreams:    sampleWorkstreams(),
		rexAvailable:   true,
		configWarnings: []string{`status "in_progress": invalid color "chartreuse"; using auto`},
	})

	found := false
	for _, notice := range m.state.persistentNotices(time.Now()) {
		if !strings.Contains(notice.text, "invalid color") {
			continue
		}
		found = true
		if notice.priority != warningConfig {
			t.Errorf("config warning priority = %d, want %d", notice.priority, warningConfig)
		}
	}
	if !found {
		t.Fatalf("config warning is not persisted: %#v", m.state.notices)
	}
	shown, _, ok := m.state.bannerNotice(time.Now())
	if !ok || !strings.Contains(shown.text, "invalid color") {
		t.Errorf("banner = %#v, %v", shown, ok)
	}
}

func TestConfigWarningRanksBelowSession(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)

	_, _ = m.handleLoaded(loadedMsg{
		cfg:             config.Default(),
		workstreams:     sampleWorkstreams(),
		rexAvailable:    true,
		configWarnings:  []string{"config problem"},
		sessionWarnings: []string{"session problem"},
	})

	shown, _, ok := m.state.bannerNotice(time.Now())
	if !ok || !strings.Contains(shown.text, "session problem") {
		t.Errorf("banner = %#v, %v; want the session warning first", shown, ok)
	}
}

// brokenWorktreePath returns a path git could list but Canonical cannot
// resolve: a symlink whose target is missing.
func brokenWorktreePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken")
	if err := os.Symlink(filepath.Join(dir, "missing"), broken); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}
	return broken
}

func TestBuildWorkstreamsSurfacesSkippedWorktree(t *testing.T) {
	readable := t.TempDir()
	missing := brokenWorktreePath(t)
	inputs := []core.Input{{
		Project: config.Project{Name: "alpha", Path: "/code/alpha"},
		Worktrees: []git.Worktree{
			{Path: missing, Branch: "feature"},
			{Path: missing, Branch: "feature"},
			{Path: readable, Branch: "main"},
		},
	}}
	discoveryErrors := []ops.ProjectError{{Project: "beta", Err: errors.New("not a git repository")}}

	workstreams, warnings := buildWorkstreams(inputs, config.NewState(), nil, config.Default(), discoveryErrors)

	if len(workstreams) != 1 || workstreams[0].Branch != "main" {
		t.Fatalf("readable worktree was not kept: %#v", workstreams)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v, want one deduplicated skipped-worktree warning and the discovery warning", warnings)
	}
	for _, want := range []string{"project alpha", missing, "feature", "skipped"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("skipped-worktree warning %q missing %q", warnings[0], want)
		}
	}
	// The whole-project failure is appended last, so it outranks the single
	// skipped worktree within the warningDiscovery class.
	if warnings[1] != "project beta: not a git repository" {
		t.Errorf("discovery warning = %q", warnings[1])
	}
}

func TestBuildWorkstreamsSilentWhenAllReadable(t *testing.T) {
	inputs := []core.Input{{
		Project:   config.Project{Name: "alpha", Path: "/code/alpha"},
		Worktrees: []git.Worktree{{Path: t.TempDir(), Branch: "main"}},
	}}

	workstreams, warnings := buildWorkstreams(inputs, config.NewState(), nil, config.Default(), nil)

	if len(workstreams) != 1 {
		t.Fatalf("workstreams = %#v", workstreams)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none when every candidate is readable", warnings)
	}
}

func TestLoadCmdSurfacesSkippedWorktree(t *testing.T) {
	configDir := t.TempDir()
	configJSON := `{"projects":[{"name":"alpha","path":"/code/alpha"}]}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvDir, configDir)
	// A Rex that reports no sessions and whose event stream emits one
	// connected event.
	rexBin := filepath.Join(t.TempDir(), "rex")
	rexScript := `#!/bin/sh
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  "events --json") printf '%s\n' '{"type":"server_connected","payload":{}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(rexBin, []byte(rexScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, rexBin)

	readable := t.TempDir()
	missing := brokenWorktreePath(t)
	previous := discoverProjects
	discoverProjects = func(context.Context, config.Config) ops.Discovery {
		return ops.Discovery{Inputs: []core.Input{{
			Project: config.Project{Name: "alpha", Path: "/code/alpha"},
			Worktrees: []git.Worktree{
				{Path: readable, Branch: "main"},
				{Path: missing, Branch: "feature"},
			},
		}}}
	}
	defer func() { discoverProjects = previous }()

	msg, ok := loadCmd(context.Background())().(loadedMsg)
	if !ok {
		t.Fatal("loadCmd did not return a loadedMsg")
	}
	if msg.fatalErr != nil || msg.loadErr != nil {
		t.Fatalf("load failed: fatal=%v load=%v", msg.fatalErr, msg.loadErr)
	}

	m := NewModel()
	defer m.Close()
	m.state = newState()
	_, _ = m.handleLoaded(msg)

	var persistent []notice
	for _, notice := range m.state.persistentNotices(time.Now()) {
		if strings.Contains(notice.text, missing) {
			persistent = append(persistent, notice)
		}
	}
	if len(persistent) != 1 {
		t.Fatalf("persistent skipped-worktree warnings = %#v", persistent)
	}
	if persistent[0].priority != warningDiscovery {
		t.Errorf("skipped-worktree priority = %d, want %d", persistent[0].priority, warningDiscovery)
	}
	shown, _, ok := m.state.bannerNotice(time.Now())
	if !ok || !strings.Contains(shown.text, "feature") {
		t.Errorf("banner = %#v, %v; want the skipped worktree named", shown, ok)
	}
	if len(m.state.workstreams) != 1 || m.state.workstreams[0].Branch != "main" {
		t.Errorf("readable workstreams were dropped: %#v", m.state.workstreams)
	}
}

func TestStatusDoneSurfacesSkippedWarnings(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	m.state.pending = true
	skipped := core.SkippedWorktree{
		Project: config.Project{Name: "alpha"},
		Path:    "/code/alpha/stale",
		Branch:  "feature",
		Err:     errors.New("gone"),
	}
	_, _ = m.handleStatusDone(statusDoneMsg{
		request:  StatusRequest{Path: "/wt/dosido", Status: config.StatusBlocked},
		warnings: []string{skipped.Warning()},
	})

	for _, notice := range m.state.visibleNotices(time.Now()) {
		if strings.Contains(notice.text, skipped.Warning()) {
			return
		}
	}
	t.Fatalf("status warning was dropped: %#v", m.state.notices)
}

func TestStatusDoneDoesNotDuplicatePersistentSkippedWarning(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	skipped := core.SkippedWorktree{
		Project: config.Project{Name: "alpha"},
		Path:    "/code/alpha/stale",
		Branch:  "feature",
		Err:     errors.New("gone"),
	}

	_, _ = m.handleLoaded(loadedMsg{
		cfg:               config.Default(),
		workstreams:       sampleWorkstreams(),
		rexAvailable:      true,
		discoveryWarnings: []string{skipped.Warning()},
	})
	before := len(m.state.notices)

	_, _ = m.handleStatusDone(statusDoneMsg{
		request:  StatusRequest{Path: "/wt/dosido", Status: config.StatusBlocked},
		warnings: []string{skipped.Warning()},
	})

	count := 0
	for _, notice := range m.state.notices {
		if !strings.Contains(notice.text, skipped.Warning()) {
			continue
		}
		count++
		if !notice.persistent {
			t.Errorf("duplicate transient notice for a persistent warning: %#v", notice)
		}
	}
	if count != 1 {
		t.Fatalf("notices mentioning the skipped worktree = %d, want 1", count)
	}
	if len(m.state.notices) != before {
		t.Errorf("status added a duplicate notice: %#v", m.state.notices)
	}
}

func TestHandleLoadedPreservesFilterExpansionAndCursor(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	m.state.query = "rexws"
	m.state.snapshot = map[string]bool{}
	m.state.expanded = map[string]bool{"/wt/rexws": true}
	m.state.cursorKey = RowKey{Kind: RowWorkstream, ID: "/wt/rexws"}
	m.state.cursorRow = -1
	m.state = m.state.rebuild()

	_, _ = m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		rexAvailable: true,
	})
	if m.state.query != "rexws" {
		t.Errorf("query = %q", m.state.query)
	}
	if !m.state.expanded["/wt/rexws"] {
		t.Error("expansion was lost across a refresh")
	}
	if m.state.cursorKey.ID != "/wt/rexws" {
		t.Errorf("cursor = %#v", m.state.cursorKey)
	}
	if m.state.loading {
		t.Error("loading flag was not cleared")
	}
}

func TestProgramStatusChangedUpdatesAgentInPlace(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	event := rex.Event{
		Type:      rex.EventProgramStatusChanged,
		SessionID: "session:rexws",
		BlockID:   "block:agent",
		Record: &rex.StatusRecord{
			State: "blocked", Kind: "question", App: "claude-code",
			Msg: "May I run the tests?", UpdatedAt: "2026-11-26T00:00:00Z",
		},
	}
	if cmd := m.handleStreamEvent(event); cmd != nil {
		t.Errorf("in-place update produced a command: %v", cmd)
	}
	ws, ok := m.state.workstream("/wt/rexws")
	if !ok {
		t.Fatal("workstream disappeared")
	}
	if ws.Agents[0].State != core.AgentBlocked || ws.Agents[0].Detail != "question" {
		t.Errorf("agent = %#v", ws.Agents[0])
	}
	if ws.AgentState != core.AgentBlocked || ws.AgentDetail != "question" {
		t.Errorf("aggregate = (%q, %q)", ws.AgentState, ws.AgentDetail)
	}
	if !strings.Contains(ws.Agents[0].Msg, "May I") {
		t.Errorf("message was not updated: %#v", ws.Agents[0])
	}
	row, ok := rowByKey(m.state.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !strings.Contains(row.Badge, "blocked (question)") {
		t.Errorf("badge = %q", row.Badge)
	}
}

func TestProgramStatusRemovedRefreshesSessionAgents(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	cmd := m.handleStreamEvent(rex.Event{
		Type:      rex.EventProgramStatusRemoved,
		SessionID: "session:rexws",
		BlockID:   "block:agent",
	})
	if cmd == nil {
		t.Fatal("program_status_removed did not schedule an agent refresh")
	}
}

func TestUnknownBlockRefreshesSessionAgents(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	cmd := m.handleStreamEvent(rex.Event{
		Type:      rex.EventProgramStatusChanged,
		SessionID: "session:rexws",
		BlockID:   "block:new",
		Record:    &rex.StatusRecord{State: "working"},
	})
	if cmd == nil {
		t.Fatal("a new block did not schedule an agent refresh")
	}
}

func TestWindowEventDebouncesRefresh(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	event := rex.Event{Type: rex.EventActiveWindowChanged, SessionID: "session:rexws", WindowID: "window:agent"}
	cmd := m.handleStreamEvent(event)
	if cmd == nil {
		t.Fatal("active window change did not schedule a refresh")
	}
	if !m.state.refreshPending["session:rexws"] {
		t.Error("refresh was not marked pending")
	}
	if again := m.handleStreamEvent(event); again != nil {
		t.Error("a second event scheduled a second refresh")
	}
	msg, ok := cmd().(windowRefreshTickMsg)
	if !ok {
		t.Fatalf("tick message = %T", msg)
	}
	refresh, _ := m.handleWindowRefreshTick(msg)
	if refresh == nil {
		t.Fatal("tick did not produce a window refresh")
	}
	if m.state.refreshPending["session:rexws"] {
		t.Error("pending flag was not cleared")
	}
	// A stale tick with the wrong token is ignored.
	m.state.refreshPending["session:rexws"] = true
	m.state.refreshToken["session:rexws"] = 99
	if _, cmd := m.handleWindowRefreshTick(windowRefreshTickMsg{sessionID: "session:rexws", token: 1}); cmd != nil {
		t.Error("stale tick was not ignored")
	}
}

func TestStatusDoneFollowsPathAndRefreshes(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	m.state.pending = true
	_, cmd := m.handleStatusDone(statusDoneMsg{
		request: StatusRequest{Path: "/wt/dosido", Status: config.StatusBlocked},
	})
	if m.state.cursorKey.ID != "/wt/dosido" {
		t.Errorf("cursor = %#v", m.state.cursorKey)
	}
	if !m.state.loading || cmd == nil {
		t.Error("status change did not start a refresh")
	}
}

func TestRemovePreparedOpensConfirmation(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	m.state.pending = true
	plan := ops.RemovePlan{Project: config.Project{Name: "rexws"}, Path: "/wt/rexws", Branch: "m1-m2-core-rex"}
	_, cmd := m.handleRemovePrepared(removePreparedMsg{plan: plan})
	if cmd != nil {
		t.Errorf("confirmation produced a command: %v", cmd)
	}
	if m.state.mode != ModeRemove || m.state.remove == nil {
		t.Fatalf("mode = %v modal = %#v", m.state.mode, m.state.remove)
	}
	if m.state.pending {
		t.Error("pending flag was not cleared")
	}
}

func TestRemovePreparedErrorDoesNotOpenConfirmation(t *testing.T) {
	m := NewModel()
	m.state = testState(t)
	m.state.pending = true
	_, cmd := m.Update(removePreparedMsg{err: os.ErrPermission})
	if cmd == nil {
		t.Error("pre-check error did not schedule a notice tick")
	}
	if m.state.mode == ModeRemove || m.state.remove != nil {
		t.Error("pre-check error opened the confirmation")
	}
	if len(m.state.notices) == 0 {
		t.Error("pre-check error was not surfaced")
	}
}

func TestEnsureCursorVisibleScrollsMinimum(t *testing.T) {
	s := newState()
	s.height = 10
	s.rows = make([]Row, 20)
	for i := range s.rows {
		s.rows[i] = Row{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: string(rune('a' + i))}}
	}
	s.cursorRow = 15
	s = s.ensureCursorVisible()
	if s.viewTop != 9 {
		t.Errorf("viewTop = %d, want 9", s.viewTop)
	}
	if s.cursorRow < s.viewTop || s.cursorRow >= s.viewTop+s.bodyHeight() {
		t.Errorf("cursor %d is outside [%d, %d)", s.cursorRow, s.viewTop, s.viewTop+s.bodyHeight())
	}
	s.cursorRow = 2
	s = s.ensureCursorVisible()
	if s.viewTop != 2 {
		t.Errorf("viewTop after moving up = %d, want 2", s.viewTop)
	}
	s.cursorRow = 0
	s = s.ensureCursorVisible()
	if s.viewTop != 0 {
		t.Errorf("viewTop at top = %d, want 0", s.viewTop)
	}
}

func TestPruneNotices(t *testing.T) {
	s := newState()
	s.notices = []notice{
		{text: "expired", expires: time.Now().Add(-time.Second)},
		{text: "persistent", persistent: true},
		{text: "current", expires: time.Now().Add(time.Second)},
	}
	s = s.pruneNotices(time.Now())
	if len(s.notices) != 2 || s.notices[0].text != "persistent" || s.notices[1].text != "current" {
		t.Errorf("notices = %#v", s.notices)
	}
	if !s.hasExpiringNotices(time.Now()) {
		t.Error("hasExpiringNotices = false")
	}
}

func TestStreamHubDeliversServerAndSessionEvents(t *testing.T) {
	server := `{"type":"session_created","payload":{"session":{"session_id":"session:fake","label":"repo/main"}}}`
	session := `{"type":"session_event","payload":{"session_id":"session:fake","event":{"type":"active_window_changed","payload":{"window_id":"window:1"}}}}`
	fakeRexEvents(t, server, session)

	hub := newStreamHub(os.Getenv(config.EnvRexBin))
	hub.Start()
	defer hub.Close()
	hub.SyncSessions([]string{"session:fake"})

	deadline := time.After(5 * time.Second)
	seenServer := false
	seenSession := false
	for !seenServer || !seenSession {
		select {
		case <-deadline:
			t.Fatalf("timed out: server=%v session=%v", seenServer, seenSession)
		default:
		}
		msg, ok := hub.pop()
		if !ok {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		event, ok := msg.(streamEventMsg)
		if !ok {
			continue
		}
		switch {
		case event.event.Type == rex.EventSessionCreated && event.event.SessionID == "session:fake":
			seenServer = true
		case event.event.Type == rex.EventActiveWindowChanged && event.event.SessionID == "session:fake":
			seenSession = true
		}
	}
}
