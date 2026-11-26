package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/rex"
)

func lineContaining(t *testing.T, out, substr string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", substr, out)
	return ""
}

func headerLine(out string) string { return strings.SplitN(out, "\n", 2)[0] }

// fastTick replaces the model clock so scheduled messages arrive immediately
// and their delays are recorded.
func fastTick(m *Model, delays *[]time.Duration) {
	m.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		if delays != nil {
			*delays = append(*delays, d)
		}
		return func() tea.Msg { return fn(time.Time{}) }
	}
}

// assertCountsWholeOrAbsent checks that the counts chip is either fully
// present in its width-appropriate form or absent.
func assertCountsWholeOrAbsent(t *testing.T, header string, width int) {
	t.Helper()
	if !strings.Contains(header, "[") {
		return
	}
	if !strings.Contains(header, "]") {
		t.Errorf("counts chip is truncated: %q", header)
	}
	if width >= 80 {
		if !strings.Contains(header, " workstreams · ") || !strings.Contains(header, " agent-blocked]") {
			t.Errorf("80-column counts chip is incomplete: %q", header)
		}
		return
	}
	if !strings.Contains(header, " ws · ") || !strings.Contains(header, " live]") {
		t.Errorf("narrow counts chip is incomplete: %q", header)
	}
}

func withPausedSession(s State, sessionID string) State {
	s.streamPaused[sessionID] = true
	return s.rebuild()
}

func TestRenderHealthTokens(t *testing.T) {
	for _, width := range []int{60, 80} {
		t.Run(fmt.Sprintf("normal-%d", width), func(t *testing.T) {
			s := renderState(t, width, 24)
			out := Render(s)
			assertScreen(t, out, width, 24)
			header := headerLine(out)
			if strings.Contains(header, "(r)") {
				t.Errorf("healthy header has a health token: %q", header)
			}
			assertCountsWholeOrAbsent(t, header, width)
		})

		t.Run(fmt.Sprintf("partial-%d", width), func(t *testing.T) {
			s := withPausedSession(renderState(t, width, 24), "session:rexws")
			out := Render(s)
			assertScreen(t, out, width, 24)
			header := headerLine(out)
			if !strings.Contains(header, "1 stream paused (r)") {
				t.Errorf("partial header = %q", header)
			}
			assertCountsWholeOrAbsent(t, header, width)
			if !strings.Contains(lineContaining(t, out, "m1-m2-core-rex"), "‖") {
				t.Errorf("paused workstream row has no marker:\n%s", out)
			}
			if strings.Contains(lineContaining(t, out, "refactor-sync"), "‖") {
				t.Errorf("unpaused workstream row has a pause marker:\n%s", out)
			}
		})

		t.Run(fmt.Sprintf("all-paused-%d", width), func(t *testing.T) {
			s := renderState(t, width, 24)
			for _, id := range s.liveSessionIDs() {
				s.streamPaused[id] = true
			}
			s = s.rebuild()
			out := Render(s)
			assertScreen(t, out, width, 24)
			header := headerLine(out)
			if !strings.Contains(header, "live paused (r)") {
				t.Errorf("all-paused header = %q", header)
			}
			if strings.Contains(out, "‖") {
				t.Errorf("all-paused render kept per-session markers:\n%s", out)
			}
		})

		t.Run(fmt.Sprintf("rex-down-loaded-%d", width), func(t *testing.T) {
			s := renderState(t, width, 24)
			s.rexAvailable = false
			s.rexEverLoaded = true
			s.streamPaused["session:rexws"] = true
			s = s.rebuild()
			out := Render(s)
			assertScreen(t, out, width, 24)
			header := headerLine(out)
			if !strings.Contains(header, "Rex unavailable; paused (r)") {
				t.Errorf("outage header = %q", header)
			}
			if strings.Contains(out, "‖") {
				t.Errorf("outage render kept pause markers:\n%s", out)
			}
			if width >= 80 {
				if !strings.Contains(out, "blocked (permission)") {
					t.Errorf("outage lost last-known badges:\n%s", out)
				}
			} else if !strings.Contains(out, "blocked (permissi") {
				t.Errorf("outage lost last-known badges:\n%s", out)
			}
			assertCountsWholeOrAbsent(t, header, width)
		})

		t.Run(fmt.Sprintf("rex-down-never-loaded-%d", width), func(t *testing.T) {
			s := renderState(t, width, 24)
			s.rexAvailable = false
			s.rexEverLoaded = false
			s = s.rebuild()
			out := Render(s)
			assertScreen(t, out, width, 24)
			header := headerLine(out)
			if !strings.Contains(header, "Rex unavailable (r)") {
				t.Errorf("never-loaded header = %q", header)
			}
			if !strings.Contains(out, "?") {
				t.Errorf("never-loaded badges missing:\n%s", out)
			}
			assertCountsWholeOrAbsent(t, header, width)
		})
	}
}

func TestRenderAmbiguityBothDirections(t *testing.T) {
	t.Run("one session many workstreams", func(t *testing.T) {
		s := renderState(t, 80, 24)
		s.workstreams[1].RexSessionID = s.workstreams[0].RexSessionID
		s.workstreams[1].SessionName = s.workstreams[0].SessionName
		s.ambiguousSessions = map[string]bool{"session:rexws": true}
		s = s.setPersistentWarnings([]persistentWarning{{
			text:  "ambiguous: 2 workstreams, one live session",
			class: warningAmbiguity,
		}})
		s = s.rebuild()
		out := Render(s)
		assertScreen(t, out, 80, 24)
		for _, branch := range []string{"m1-m2-core-rex", "refactor-sync"} {
			line := lineContaining(t, out, branch)
			if !strings.Contains(line, "!") {
				t.Errorf("row %q is not marked ambiguous: %q", branch, line)
			}
		}
		lines := strings.Split(out, "\n")
		if !strings.Contains(lines[len(lines)-2], "! ambiguous: 2 workstreams, one live session") {
			t.Errorf("banner = %q", lines[len(lines)-2])
		}
	})

	t.Run("one label many live sessions", func(t *testing.T) {
		s := renderState(t, 80, 24)
		s.ambiguousLabels = map[string]bool{"rexws/m1-m2-core-rex": true}
		s = s.setPersistentWarnings([]persistentWarning{{
			text:  "ambiguous: 2 live sessions, one label",
			class: warningAmbiguity,
		}})
		s = s.rebuild()
		out := Render(s)
		assertScreen(t, out, 80, 24)
		if !strings.Contains(lineContaining(t, out, "m1-m2-core-rex"), "!") {
			t.Errorf("bound row is not marked:\n%s", out)
		}
		if strings.Contains(lineContaining(t, out, "refactor-sync"), "!") {
			t.Errorf("an unbound row was marked:\n%s", out)
		}
		lines := strings.Split(out, "\n")
		if !strings.Contains(lines[len(lines)-2], "! ambiguous: 2 live sessions, one label") {
			t.Errorf("banner = %q", lines[len(lines)-2])
		}
	})
}

func TestRenderCombinedWorstCase60(t *testing.T) {
	s := renderState(t, 60, 18)
	s.query = strings.Repeat("verylongquery", 4)
	s.streamPaused["session:rexws"] = true
	s.ambiguousLabels["rexws/m1-m2-core-rex"] = true
	s = s.setPersistentWarnings([]persistentWarning{{
		text:  "ambiguous: 2 live sessions, one label",
		class: warningAmbiguity,
	}})
	s = s.rebuild()
	out := Render(s)
	lines := assertScreen(t, out, 60, 18)
	header := lines[0]
	if !strings.Contains(header, "1 stream paused (r)") {
		t.Errorf("worst-case header truncated health: %q", header)
	}
	if lipgloss.Width(header) > 60 {
		t.Errorf("header is %d cells", lipgloss.Width(header))
	}
	assertCountsWholeOrAbsent(t, header, 60)
	if !strings.Contains(header, "filter: ") {
		t.Errorf("filter chip missing: %q", header)
	}
	// The query is capped at 12 cells whenever a health token is present.
	if strings.Contains(header, s.query) {
		t.Errorf("query was not capped: %q", header)
	}

	// The same conditions with a matching query keep both markers on the row.
	s.query = "rexws"
	s = s.rebuild()
	out = Render(s)
	assertScreen(t, out, 60, 18)
	row := lineContaining(t, out, "m1-m2-core-rex")
	if !strings.Contains(row, "!‖") {
		t.Errorf("combined markers missing from row: %q", row)
	}
}

func TestRenderBannerReservesMoreAndXDismissesTransientOnly(t *testing.T) {
	s := renderState(t, 60, 18)
	s = s.addError("switch: " + strings.Repeat("boom ", 60))
	s = s.setPersistentWarnings([]persistentWarning{
		{text: "project alpha: repository missing", class: warningDiscovery},
		{text: `session "beta": unreadable`, class: warningSession},
	})
	s = s.rebuild()

	out := Render(s)
	lines := assertScreen(t, out, 60, 18)
	banner := lines[len(lines)-2]
	if !strings.Contains(banner, "+2 more") {
		t.Errorf("banner did not reserve +N more: %q", banner)
	}

	// x dismisses the transient and reveals the highest-priority warning.
	s, _ = Reduce(s, runeKey('x'))
	out = Render(s)
	lines = assertScreen(t, out, 60, 18)
	banner = lines[len(lines)-2]
	if !strings.Contains(banner, "project alpha: repository missing") {
		t.Errorf("banner after x = %q", banner)
	}
	if !strings.Contains(banner, "+1 more") {
		t.Errorf("banner after x did not count the remaining warning: %q", banner)
	}

	// x never dismisses a persistent warning.
	s, _ = Reduce(s, runeKey('x'))
	out = Render(s)
	lines = strings.Split(out, "\n")
	if !strings.Contains(lines[len(lines)-2], "project alpha: repository missing") {
		t.Errorf("x dismissed a persistent warning: %q", lines[len(lines)-2])
	}
}

func TestXDismissesNewestTransientOnly(t *testing.T) {
	s := testState(t)
	s = s.addError("older error")
	s = s.addError("newest error")
	if got := s.visibleNotices(time.Now()); len(got) != 2 {
		t.Fatalf("notices = %#v", got)
	}
	s, _ = Reduce(s, runeKey('x'))
	visible := s.visibleNotices(time.Now())
	if len(visible) != 1 || visible[0].text != "older error" {
		t.Fatalf("after x = %#v", visible)
	}
	s, _ = Reduce(s, runeKey('x'))
	if len(s.visibleNotices(time.Now())) != 0 {
		t.Errorf("second x left notices: %#v", s.notices)
	}
}

func TestRenderHeightBeforeAndAfterNoticeExpiry(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.addError("expired problem")
	s.notices[len(s.notices)-1].expires = time.Now().Add(-time.Second)

	before := Render(s)
	assertScreen(t, before, 80, 24)
	if strings.Contains(before, "expired problem") {
		t.Errorf("expired notice rendered:\n%s", before)
	}

	after := Render(s.pruneNotices(time.Now()))
	assertScreen(t, after, 80, 24)

	// An unexpired notice keeps the screen exactly one row shorter in the body
	// but the total height unchanged.
	s = s.addError("current problem")
	assertScreen(t, Render(s), 80, 24)
}

func TestRenderControlCharactersNeverReachChrome(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.addError("switch: bad\x1b[31m\nerror")
	s = s.setPersistentWarnings([]persistentWarning{{
		text:   "project x: bad\x00\x1b[0m\nlabel",
		detail: "detail\x1b[31m\nBEL\x07end",
		class:  warningDiscovery,
	}})
	s = s.rebuild()
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape reached the render: %q", out)
	}

	s.mode = ModeHelp
	help := Render(s)
	assertScreen(t, help, 80, 24)
	if strings.Contains(help, "\x1b") || strings.Contains(help, "\x07") {
		t.Errorf("control character reached help: %q", help)
	}
	if strings.Contains(help, "\nlabel") {
		t.Errorf("control newline split help")
	}

	// The no-match message quotes the sanitized query.
	query := renderState(t, 80, 24)
	query = query.setQuery("bad\x1b[31mzz")
	noMatch := Render(query)
	assertScreen(t, noMatch, 80, 24)
	if strings.Contains(noMatch, "\x1b") {
		t.Errorf("escape reached the no-match message: %q", noMatch)
	}
}

func TestRenderHelpListsWarningsMarkersAndX(t *testing.T) {
	s := renderState(t, 60, 24)
	s = s.setPersistentWarnings([]persistentWarning{
		{text: "project alpha: " + strings.Repeat("very long reason ", 12), class: warningDiscovery},
		{text: `session "beta": unreadable`, class: warningSession, detail: "Duplicate labels: alpha (2 live sessions); beta (2 live sessions)"},
	})
	s.mode = ModeHelp
	out := Render(s)
	assertScreen(t, out, 60, 24)
	for _, want := range []string{
		"Warnings", "project alpha:", "very long reason",
		`session "beta": unreadable`,
		"Duplicate labels: alpha (2 live sessions)",
		"ambiguous session binding",
		"live updates paused for this session",
		"both",
		"dismiss the newest action error",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}

func TestPerSessionPauseDebouncesThenClearsOnConnect(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	fastTick(m, nil)

	cmd := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if cmd == nil {
		t.Fatal("disconnect did not start the debounce")
	}
	if m.state.streamPaused["session:rexws"] {
		t.Error("pause became visible before the debounce")
	}
	if !m.state.pausePending["session:rexws"] {
		t.Error("debounce was not marked pending")
	}
	if token := m.state.healthToken(); token != "" {
		t.Errorf("header token before the debounce = %q", token)
	}

	tick, ok := cmd().(pauseTickMsg)
	if !ok {
		t.Fatalf("tick message = %T", tick)
	}
	m.handlePauseTick(tick)
	if !m.state.streamPaused["session:rexws"] {
		t.Error("pause did not become visible after the debounce")
	}
	if token := m.state.healthToken(); token != "1 stream paused (r)" {
		t.Errorf("header token = %q", token)
	}
	row, ok := rowByKey(m.state.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !row.MarkerPaused {
		t.Errorf("row marker = %+v", row)
	}

	// A real connect clears the pause immediately.
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: true})
	if m.state.streamPaused["session:rexws"] {
		t.Error("connect did not clear the visible pause")
	}
	row, _ = rowByKey(m.state.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if row.MarkerPaused {
		t.Errorf("row kept the marker after connect: %+v", row)
	}
}

func TestPauseDebounceCancelledByReconnect(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	fastTick(m, nil)

	cmd := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if cmd == nil {
		t.Fatal("disconnect did not start the debounce")
	}
	// Reconnect before the 2s tick: the stale tick must not pause the session.
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: true})
	tick, _ := cmd().(pauseTickMsg)
	m.handlePauseTick(tick)
	if m.state.streamPaused["session:rexws"] {
		t.Error("a quick drop/reconnect produced a visible pause")
	}
	if token := m.state.healthToken(); token != "" {
		t.Errorf("header token = %q", token)
	}
}

func TestConnectingCancelsPendingPauseButKeepsVisiblePause(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	fastTick(m, nil)

	// A reconnect attempt inside the debounce window cancels the pending tick.
	cmd := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	tick := cmd().(pauseTickMsg)
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connecting: true})
	m.handlePauseTick(tick)
	if m.state.streamPaused["session:rexws"] {
		t.Error("connecting did not cancel the pending pause")
	}

	// A visible pause survives an attempt and clears only on a real connect.
	cmd = m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	tick = cmd().(pauseTickMsg)
	m.handlePauseTick(tick)
	if !m.state.streamPaused["session:rexws"] {
		t.Fatal("pause did not become visible")
	}
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connecting: true})
	if !m.state.streamPaused["session:rexws"] {
		t.Error("connecting cleared an already-visible pause")
	}
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: true})
	if m.state.streamPaused["session:rexws"] {
		t.Error("connect did not clear the visible pause")
	}
}

func TestPauseDebounceIsTwoSeconds(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	var delays []time.Duration
	fastTick(m, &delays)

	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if len(delays) != 1 || delays[0] != pauseDebounce {
		t.Errorf("debounce delays = %v, want %v", delays, pauseDebounce)
	}
}

func TestStreamFailuresAddNoNotice(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	fastTick(m, nil)
	before := len(m.state.notices)
	for i := 0; i < 5; i++ {
		cmd := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
		if cmd == nil {
			continue
		}
		tick, ok := cmd().(pauseTickMsg)
		if !ok {
			t.Fatalf("tick = %T", tick)
		}
		m.handlePauseTick(tick)
	}
	if len(m.state.notices) != before {
		t.Errorf("stream failures changed the notice count: %#v", m.state.notices)
	}
}

func TestServerWideStreamNeverSetsLivePaused(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	m.handleStreamState(streamStateMsg{sessionID: "", connected: false})
	if token := m.state.healthToken(); token != "" {
		t.Errorf("server stream disconnect set %q", token)
	}
}

func TestStreamEventUpdatesEveryBoundWorkstream(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	m.state.workstreams[1].RexSessionID = m.state.workstreams[0].RexSessionID
	m.state.workstreams[1].SessionName = m.state.workstreams[0].SessionName
	m.state.workstreams[1].Agents = []core.Agent{{
		BlockID: "block:agent", WindowID: "window:agent", State: core.AgentWorking,
	}}
	m.state = m.state.rebuild()

	m.handleStreamEvent(rex.Event{
		Type:      rex.EventProgramStatusChanged,
		SessionID: "session:rexws",
		BlockID:   "block:agent",
		Record:    &rex.StatusRecord{State: "blocked", Kind: "question"},
	})
	for _, path := range []string{"/wt/rexws", "/wt/dosido"} {
		ws, ok := m.state.workstream(path)
		if !ok {
			t.Fatalf("workstream %s missing", path)
		}
		if len(ws.Agents) == 0 || ws.Agents[0].State != core.AgentBlocked || ws.Agents[0].Detail != "question" {
			t.Errorf("%s agent = %#v", path, ws.Agents)
		}
	}

	m.handleSessionAgents(sessionAgentsMsg{
		sessionID: "session:rexws",
		label:     "rexws/m1-m2-core-rex",
		agents:    []core.Agent{{BlockID: "block:refreshed", State: core.AgentIdle}},
	})
	for _, path := range []string{"/wt/rexws", "/wt/dosido"} {
		ws, _ := m.state.workstream(path)
		if len(ws.Agents) != 1 || ws.Agents[0].BlockID != "block:refreshed" {
			t.Errorf("%s agents after refresh = %#v", path, ws.Agents)
		}
	}
}

func TestAnalyzeAmbiguityBothDirections(t *testing.T) {
	shared := []core.Workstream{
		{Path: "/wt/a", SessionName: "repo/main", RexSessionID: "session:1"},
		{Path: "/wt/b", SessionName: "repo/main", RexSessionID: "session:1"},
	}
	got := analyzeAmbiguity(nil, shared)
	if !got.sessions["session:1"] {
		t.Errorf("session ambiguity not detected: %#v", got)
	}
	if len(got.warnings) != 1 || got.warnings[0].text != "ambiguous: 2 workstreams, one live session" {
		t.Errorf("warnings = %#v", got.warnings)
	}

	sessions := []core.Session{
		{ID: "session:1", Label: "repo/main"},
		{ID: "session:2", Label: "repo/main"},
	}
	bound := []core.Workstream{{Path: "/wt/a", SessionName: "repo/main", RexSessionID: "session:1"}}
	got = analyzeAmbiguity(sessions, bound)
	if !got.labels["repo/main"] {
		t.Errorf("label ambiguity not detected: %#v", got)
	}
	if len(got.warnings) != 1 || got.warnings[0].text != "ambiguous: 2 live sessions, one label" {
		t.Errorf("warnings = %#v", got.warnings)
	}
	if !strings.Contains(got.warnings[0].detail, "repo/main") {
		t.Errorf("detail = %q", got.warnings[0].detail)
	}
}

func TestAnalyzeAmbiguityDedupesSessionIDsAndAggregates(t *testing.T) {
	// A duplicated `session ls` row is not ambiguity.
	sameID := []core.Session{
		{ID: "session:1", Label: "repo/main"},
		{ID: "session:1", Label: "repo/main"},
	}
	if got := analyzeAmbiguity(sameID, nil); len(got.warnings) != 0 {
		t.Errorf("duplicate ID produced %#v", got.warnings)
	}

	// Two separate duplicate groups collapse to one warning per direction, and
	// Help names both labels.
	sessions := []core.Session{
		{ID: "s1", Label: "alpha"},
		{ID: "s2", Label: "alpha"},
		{ID: "s3", Label: "beta"},
		{ID: "s4", Label: "beta"},
	}
	got := analyzeAmbiguity(sessions, nil)
	if len(got.warnings) != 1 {
		t.Fatalf("warnings = %#v", got.warnings)
	}
	if got.warnings[0].text != "ambiguous: 2 live sessions, one label" {
		t.Errorf("banner text = %q", got.warnings[0].text)
	}
	if !strings.Contains(got.warnings[0].detail, "alpha") || !strings.Contains(got.warnings[0].detail, "beta") {
		t.Errorf("detail does not name both labels: %q", got.warnings[0].detail)
	}

	shared := []core.Workstream{
		{Path: "/a", RexSessionID: "s1"},
		{Path: "/b", RexSessionID: "s1"},
		{Path: "/c", RexSessionID: "s2"},
		{Path: "/d", RexSessionID: "s2"},
	}
	got = analyzeAmbiguity(nil, shared)
	if len(got.warnings) != 1 || got.warnings[0].text != "ambiguous: 2 workstreams, one live session" {
		t.Fatalf("warnings = %#v", got.warnings)
	}
	if !strings.Contains(got.warnings[0].detail, "s1") || !strings.Contains(got.warnings[0].detail, "s2") {
		t.Errorf("detail does not name both sessions: %q", got.warnings[0].detail)
	}
}

func TestHandleLoadedDetectsAmbiguity(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)

	workstreams := sampleWorkstreams()
	sessions := []core.Session{
		{ID: "session:rexws", Label: "rexws/m1-m2-core-rex"},
		{ID: "session:dup", Label: "rexws/m1-m2-core-rex"},
	}
	if _, cmd := m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  workstreams,
		sessions:     sessions,
		rexAvailable: true,
	}); cmd == nil {
		t.Fatal("load did not start the stream wait")
	}
	if !m.state.ambiguousLabels["rexws/m1-m2-core-rex"] {
		t.Errorf("ambiguous labels = %#v", m.state.ambiguousLabels)
	}
	row, ok := rowByKey(m.state.rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !row.MarkerAmbiguous {
		t.Errorf("bound row marker = %+v", row)
	}
	row, _ = rowByKey(m.state.rows, RowKey{Kind: RowWorkstream, ID: "/wt/dosido"})
	if row.MarkerAmbiguous {
		t.Errorf("unbound row was marked: %+v", row)
	}
	found := false
	for _, n := range m.state.notices {
		if n.persistent && strings.Contains(n.text, "ambiguous: 2 live sessions, one label") {
			found = true
			if !strings.Contains(n.detail, "rexws/m1-m2-core-rex") {
				t.Errorf("ambiguity detail does not name the label: %q", n.detail)
			}
		}
	}
	if !found {
		t.Errorf("ambiguity warning missing: %#v", m.state.notices)
	}
}

func TestStreamActivityDoesNotRecoverRex(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	m.state.rexAvailable = false
	m.state.rexEverLoaded = true
	m.state.streamPaused["session:rexws"] = true

	m.handleStreamEvent(rex.Event{
		Type:      rex.EventProgramStatusChanged,
		SessionID: "session:rexws",
		BlockID:   "block:agent",
		Record:    &rex.StatusRecord{State: "working"},
	})
	if m.state.rexAvailable {
		t.Error("an arbitrary event cleared the Rex outage")
	}
	if !m.state.streamPaused["session:rexws"] {
		t.Error("an arbitrary event cleared a per-session pause")
	}
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: true})
	if m.state.rexAvailable {
		t.Error("a per-session connect cleared the Rex outage")
	}
}

func TestLiveSessionIDsDeduplicates(t *testing.T) {
	s := testState(t)
	s.workstreams[1].RexSessionID = s.workstreams[0].RexSessionID
	ids := s.liveSessionIDs()
	if len(ids) != 2 {
		t.Errorf("live session ids = %#v", ids)
	}
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		previous  time.Duration
		connected bool
		want      time.Duration
	}{
		{time.Second, false, 2 * time.Second},
		{2 * time.Second, false, 4 * time.Second},
		{16 * time.Second, false, 30 * time.Second},
		{30 * time.Second, false, 30 * time.Second},
		{30 * time.Second, true, time.Second},
		{time.Second, true, time.Second},
	}
	for _, tc := range cases {
		if got := nextBackoff(tc.previous, tc.connected); got != tc.want {
			t.Errorf("nextBackoff(%v, %v) = %v, want %v", tc.previous, tc.connected, got, tc.want)
		}
	}
}

func TestStreamHubConnectedOnlyAfterStart(t *testing.T) {
	t.Run("start failure never reports connected", func(t *testing.T) {
		hub := newStreamHub("rex")
		reached := make(chan struct{})
		var once sync.Once
		hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
			return errors.New("could not start")
		}
		hub.wait = func(ctx context.Context, d time.Duration) bool {
			once.Do(func() { close(reached) })
			return false
		}
		hub.Start()
		defer hub.Close()
		select {
		case <-reached:
		case <-time.After(2 * time.Second):
			t.Fatal("stream did not reach the retry wait")
		}
		messages := drainQueue(hub)
		if len(messages) != 1 {
			t.Fatalf("messages = %#v", messages)
		}
		state, ok := messages[0].(streamStateMsg)
		if !ok || state.connected {
			t.Errorf("start failure reported %#v", messages[0])
		}
	})

	t.Run("connected after start and grace", func(t *testing.T) {
		hub := newStreamHub("rex")
		hub.grace = 0
		hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
			started()
			<-ctx.Done()
			return ctx.Err()
		}
		hub.Start()
		defer hub.Close()

		state := waitForHubState(t, hub, true)
		if !state.connected {
			t.Errorf("state = %#v", state)
		}
	})

	t.Run("immediate exit is not a connect", func(t *testing.T) {
		hub := newStreamHub("rex")
		hub.grace = time.Hour // the process dies long before the grace period
		reached := make(chan struct{})
		var once sync.Once
		hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
			started()
			return errors.New("exited immediately")
		}
		hub.wait = func(ctx context.Context, d time.Duration) bool {
			once.Do(func() { close(reached) })
			return false
		}
		hub.Start()
		defer hub.Close()
		select {
		case <-reached:
		case <-time.After(2 * time.Second):
			t.Fatal("stream did not reach the retry wait")
		}
		for _, msg := range drainQueue(hub) {
			if state, ok := msg.(streamStateMsg); ok && state.connected {
				t.Errorf("an immediately failing process flashed healthy: %#v", state)
			}
		}
	})
}

func waitForHubState(t *testing.T, hub *streamHub, connected bool) streamStateMsg {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := hub.pop(); ok {
			if state, ok := msg.(streamStateMsg); ok && state.connected == connected {
				return state
			}
			continue
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no streamStateMsg connected=%v arrived", connected)
	return streamStateMsg{}
}

func drainQueue(hub *streamHub) []tea.Msg {
	var messages []tea.Msg
	for {
		msg, ok := hub.pop()
		if !ok {
			return messages
		}
		messages = append(messages, msg)
	}
}

func TestStreamHubBackoffResetsAfterSuccess(t *testing.T) {
	hub := newStreamHub("rex")
	hub.grace = 0
	var mu sync.Mutex
	var waits []time.Duration
	attempt := 0
	release := make(chan struct{})
	var releaseOnce sync.Once
	hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
		mu.Lock()
		attempt++
		n := attempt
		mu.Unlock()
		switch n {
		case 1, 2:
			return errors.New("failed")
		case 3:
			started()
			<-release
			return errors.New("dropped")
		}
		return errors.New("failed")
	}
	done := make(chan []time.Duration, 1)
	hub.wait = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		waits = append(waits, d)
		n := len(waits)
		snapshot := append([]time.Duration{}, waits...)
		mu.Unlock()
		if n >= 3 {
			done <- snapshot
			return false
		}
		return true
	}
	hub.Start()
	defer hub.Close()

	// Attempt 3 stays up through the (zero) grace period; release it once the
	// hub has reported it connected.
	waitForHubState(t, hub, true)
	releaseOnce.Do(func() { close(release) })

	var got []time.Duration
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not attempt three retries")
	}
	want := []time.Duration{time.Second, 2 * time.Second, time.Second}
	if len(got) != len(want) {
		t.Fatalf("waits = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
}

func TestStreamHubPostNeverBlocksAndCoalesces(t *testing.T) {
	hub := newStreamHub("rex")
	start := time.Now()
	for i := 0; i < maxQueuedMessages*4; i++ {
		hub.post(context.Background(), streamEventMsg{event: rex.Event{
			Type:      rex.EventSessionCreated,
			SessionID: fmt.Sprintf("session:%d", i),
		}})
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("post blocked for %v", elapsed)
	}
	hub.mu.Lock()
	queued := len(hub.queue)
	hub.mu.Unlock()
	if queued > maxQueuedMessages {
		t.Errorf("queue grew to %d", queued)
	}

	hub = newStreamHub("rex")
	oldRecord := &rex.StatusRecord{State: "working"}
	newRecord := &rex.StatusRecord{State: "blocked"}
	for _, record := range []*rex.StatusRecord{oldRecord, newRecord} {
		hub.post(context.Background(), streamEventMsg{event: rex.Event{
			Type:      rex.EventProgramStatusChanged,
			SessionID: "session:1",
			BlockID:   "block:1",
			Record:    record,
		}})
	}
	hub.mu.Lock()
	queued = len(hub.queue)
	hub.mu.Unlock()
	if queued != 1 {
		t.Fatalf("coalesced queue length = %d", queued)
	}
	msg, ok := hub.pop()
	if !ok {
		t.Fatal("queue empty")
	}
	event := msg.(streamEventMsg).event
	if event.Record != newRecord {
		t.Errorf("coalescing kept %#v, want the newest record", event.Record)
	}
}

func TestWindowEventsCoalescePerSession(t *testing.T) {
	hub := newStreamHub("rex")
	ctx := context.Background()
	hub.post(ctx, streamEventMsg{event: rex.Event{Type: rex.EventActiveWindowChanged, SessionID: "session:1"}})
	hub.post(ctx, streamEventMsg{event: rex.Event{Type: rex.EventSessionViewChanged, SessionID: "session:1"}})
	hub.mu.Lock()
	queued := len(hub.queue)
	hub.mu.Unlock()
	if queued != 1 {
		t.Errorf("window events queued separately: %d", queued)
	}
}

func TestStreamGraceDefaultAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock grace cancellation")
	}
	if got := newStreamHub("rex").grace; got != streamGrace {
		t.Errorf("default grace = %v, want %v", got, streamGrace)
	}

	hub := newStreamHub("rex")
	hub.grace = time.Hour
	hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
		started()
		<-ctx.Done()
		return ctx.Err()
	}
	hub.Start()
	time.Sleep(20 * time.Millisecond)
	for _, msg := range drainQueue(hub) {
		if state, ok := msg.(streamStateMsg); ok && state.connected {
			t.Errorf("connected before the grace period: %#v", state)
		}
	}
	hub.Close()
}

func TestWaitForStreamUnblocksOnClose(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock waiter test")
	}
	hub := newStreamHub("rex")
	returned := make(chan tea.Msg, 1)
	go func() { returned <- waitForStream(hub)() }()
	time.Sleep(10 * time.Millisecond)
	hub.Close()
	select {
	case msg := <-returned:
		if msg != nil {
			t.Errorf("waiter returned %#v after Close", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock the waiter")
	}
}

func TestWaitForStreamDeliversQueuedMessagesInOrder(t *testing.T) {
	hub := newStreamHub("rex")
	first := streamEventMsg{event: rex.Event{Type: rex.EventSessionCreated, SessionID: "first"}}
	second := streamEventMsg{event: rex.Event{Type: rex.EventSessionDestroyed, SessionID: "second"}}
	hub.post(context.Background(), first)
	hub.post(context.Background(), second)
	for _, want := range []streamEventMsg{first, second} {
		got, ok := waitForStream(hub)().(streamEventMsg)
		if !ok || got.event.SessionID != want.event.SessionID {
			t.Errorf("delivered %#v, want %#v", got, want)
		}
	}

	// A message queued before the waiter starts is delivered too.
	hub.post(context.Background(), first)
	if got, ok := waitForStream(hub)().(streamEventMsg); !ok || got.event.SessionID != "first" {
		t.Errorf("pre-queued message was lost: %#v", got)
	}
}

func TestWaitForStreamWakesBlockedWaiter(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock waiter test")
	}
	hub := newStreamHub("rex")
	returned := make(chan tea.Msg, 1)
	go func() { returned <- waitForStream(hub)() }()
	time.Sleep(10 * time.Millisecond) // let the waiter block

	hub.post(context.Background(), streamEventMsg{
		event: rex.Event{Type: rex.EventSessionCreated, SessionID: "woken"},
	})
	select {
	case msg := <-returned:
		got, ok := msg.(streamEventMsg)
		if !ok || got.event.SessionID != "woken" {
			t.Errorf("waiter got %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("a queued message did not wake the waiter")
	}
}

func TestStreamGenerationInvalidatesStaleMessages(t *testing.T) {
	hub := newStreamHub("rex")
	hub.openStream = func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
		started()
		<-ctx.Done()
		return ctx.Err()
	}
	hub.grace = 0
	hub.Start()
	defer hub.Close()
	first := hub.Generation()

	hub.post(context.Background(), streamEventMsg{
		event:      rex.Event{Type: rex.EventSessionCreated, SessionID: "session:1"},
		generation: first,
	})
	hub.Reconnect()
	if hub.Generation() == first {
		t.Fatal("generation did not advance")
	}
	if msg, ok := hub.pop(); ok {
		t.Errorf("stale queued message survived a restart: %#v", msg)
	}

	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	m.state.streamGeneration = hub.Generation()
	m.state.streamPaused["session:rexws"] = true
	if m.staleStream(first) != true || m.staleStream(0) || m.staleStream(m.state.streamGeneration) {
		t.Error("staleStream misclassifies generations")
	}
	m.hub = hub
	_, _ = m.update(streamStateMsg{sessionID: "session:rexws", connected: true, generation: first})
	if !m.state.streamPaused["session:rexws"] {
		t.Error("a stale connect cleared a pause")
	}
	_, _ = m.update(streamStateMsg{sessionID: "session:rexws", connected: true, generation: hub.Generation()})
	if m.state.streamPaused["session:rexws"] {
		t.Error("a current connect did not clear the pause")
	}
}

func TestServerWideOutageProbesAndReconnectReconciles(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)
	var delays []time.Duration
	fastTick(m, &delays)

	// A server-wide drop while Rex is believed up schedules one debounced
	// availability probe and does not touch per-session health.
	cmd := m.handleStreamState(streamStateMsg{sessionID: "", connected: false})
	if cmd == nil {
		t.Fatal("server-wide disconnect did not schedule a probe")
	}
	if len(delays) != 1 || delays[0] != rexCheckDelay {
		t.Errorf("probe delays = %v, want %v", delays, rexCheckDelay)
	}
	if token := m.state.healthToken(); token != "" {
		t.Errorf("server-wide drop set %q", token)
	}

	// A repeated drop replaces the pending probe: the older tick is stale.
	next := m.handleStreamState(streamStateMsg{sessionID: "", connected: false})
	if next == nil {
		t.Fatal("second drop did not reschedule the probe")
	}
	if _, cmd := m.handleRexCheckTick(cmd().(rexCheckTickMsg)); cmd != nil {
		t.Error("stale probe ran")
	}
	if _, cmd := m.handleRexCheckTick(next().(rexCheckTickMsg)); cmd == nil {
		t.Fatal("probe did not request a load")
	}

	// The probe reports Rex down; signing actions lock out.
	m.state.loading = true
	if _, cmd := m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		rexAvailable: false,
	}); cmd == nil {
		t.Fatal("load did not re-issue the stream wait")
	}
	if m.state.rexAvailable {
		t.Fatal("probe did not mark Rex unavailable")
	}
	if got := m.state.healthToken(); got != "Rex unavailable; paused (r)" {
		t.Errorf("outage token = %q", got)
	}
	s, effects := Reduce(m.state, tea.KeyMsg{Type: tea.KeyEnter})
	if effects.Switch != nil {
		t.Error("Enter switched during an outage")
	}
	if _, effects = Reduce(s, runeKey('d')); effects.PreflightRemove != nil {
		t.Error("d preflighted during an outage")
	}

	// A real reconnect restores availability and schedules a reconcile load.
	cmd = m.handleStreamState(streamStateMsg{sessionID: "", connected: true})
	if !m.state.rexAvailable {
		t.Error("reconnect did not restore availability")
	}
	if cmd == nil {
		t.Fatal("reconnect did not schedule a reconcile load")
	}
	if _, refresh := m.handleRexCheckTick(cmd().(rexCheckTickMsg)); refresh == nil {
		t.Error("reconcile did not request a load")
	}
}

func TestReloadRequestedDuringLoadIsReissued(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m := NewModel()
	defer m.Close()
	m.state = testState(t)

	m.state.loading = true
	if cmd := m.refreshNow(); cmd != nil {
		t.Fatal("refresh ran while a load was in flight")
	}
	if !m.state.reloadWanted {
		t.Fatal("refresh during a load was forgotten")
	}

	// The stale in-flight load arrives last and reports an outage.
	_, cmd := m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		rexAvailable: false,
	})
	if cmd == nil {
		t.Fatal("follow-up load was not scheduled")
	}
	if !m.state.loading {
		t.Error("follow-up load did not mark loading")
	}
	if m.state.reloadWanted {
		t.Error("reloadWanted was not cleared")
	}

	// The follow-up load recovers.
	_, _ = m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		sessions:     []core.Session{{ID: "session:rexws", Label: "rexws/m1-m2-core-rex"}},
		rexAvailable: true,
	})
	if !m.state.rexAvailable {
		t.Error("recovery was lost")
	}
}

func TestHealthTokenPluralAndGlobalUnderFilter(t *testing.T) {
	s := renderState(t, 80, 24)
	s.streamPaused["session:rexws"] = true
	s.streamPaused["session:dosido"] = true
	s = s.rebuild()
	if got := s.healthToken(); got != "2 streams paused (r)" {
		t.Errorf("plural token = %q", got)
	}

	// Health is global: hiding the paused rows behind a filter changes nothing.
	s = s.setQuery(".ai")
	if got := s.healthToken(); got != "2 streams paused (r)" {
		t.Errorf("filtered health token = %q", got)
	}
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(headerLine(out), "2 streams paused (r)") {
		t.Errorf("header lost global health:\n%s", out)
	}
}

func TestPruneStreamStateDropsVanishedSessions(t *testing.T) {
	s := testState(t)
	s.streamPaused["session:rexws"] = true
	s.pausePending["session:rexws"] = true
	s.pauseSeq["session:rexws"] = 3
	s.pauseToken["session:rexws"] = 3
	s.workstreams[0].RexSessionID = ""
	s = s.pruneStreamState()
	if _, ok := s.streamPaused["session:rexws"]; ok {
		t.Error("paused state survived session disappearance")
	}
	if _, ok := s.pausePending["session:rexws"]; ok {
		t.Error("pending debounce survived session disappearance")
	}
	if _, ok := s.pauseSeq["session:rexws"]; ok {
		t.Error("pause sequence survived session disappearance")
	}
	if _, ok := s.pauseToken["session:rexws"]; ok {
		t.Error("pause token survived session disappearance")
	}
}

func TestOutageSuppressesPauseButKeepsAmbiguityMarker(t *testing.T) {
	s := renderState(t, 80, 24)
	s.rexAvailable = false
	s.rexEverLoaded = true
	s.streamPaused["session:rexws"] = true
	s.ambiguousLabels["rexws/m1-m2-core-rex"] = true
	s = s.rebuild()
	out := Render(s)
	assertScreen(t, out, 80, 24)
	row := lineContaining(t, out, "m1-m2-core-rex")
	if !strings.Contains(row, "!") {
		t.Errorf("outage dropped the ambiguity marker: %q", row)
	}
	if strings.Contains(row, "‖") {
		t.Errorf("outage kept the pause marker: %q", row)
	}
}

// clocked wires a controllable clock and immediate ticks into a model.
func clocked(t *testing.T) (*Model, *time.Time, *[]time.Duration) {
	t.Helper()
	m := NewModel()
	t.Cleanup(m.Close)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	m.now = func() time.Time { return now }
	delays := &[]time.Duration{}
	fastTick(m, delays)
	return m, &now, delays
}

func TestPauseDeadlineSurvivesFailingRetries(t *testing.T) {
	m, now, delays := clocked(t)
	m.state = testState(t)
	base := *now

	first := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if first == nil {
		t.Fatal("disconnect did not schedule the pause")
	}
	if due := m.state.pauseDue["session:rexws"]; !due.Equal(base.Add(pauseDebounce)) {
		t.Fatalf("pause due = %v, want %v", due, base.Add(pauseDebounce))
	}

	// A retry starts at 1.0s and fails immediately.
	*now = base.Add(time.Second)
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connecting: true})
	second := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if second == nil {
		t.Fatal("failed retry did not re-arm the pause")
	}
	if due := m.state.pauseDue["session:rexws"]; !due.Equal(base.Add(pauseDebounce)) {
		t.Fatalf("retry moved the pause deadline: %v", due)
	}

	// Another attempt at 1.6s.
	*now = base.Add(1600 * time.Millisecond)
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connecting: true})
	third := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if third == nil {
		t.Fatal("second failed retry did not re-arm the pause")
	}

	want := []time.Duration{pauseDebounce, pauseDebounce - time.Second, 400 * time.Millisecond}
	if len(*delays) != len(want) {
		t.Fatalf("delays = %v, want %v", *delays, want)
	}
	for i := range want {
		if (*delays)[i] != want[i] {
			t.Fatalf("delays = %v, want %v", *delays, want)
		}
	}

	// Stale ticks never pause; the deadline tick does.
	m.handlePauseTick(first().(pauseTickMsg))
	m.handlePauseTick(second().(pauseTickMsg))
	if m.state.streamPaused["session:rexws"] {
		t.Fatal("a stale tick paused the session")
	}
	*now = base.Add(pauseDebounce)
	m.handlePauseTick(third().(pauseTickMsg))
	if !m.state.streamPaused["session:rexws"] {
		t.Error("pause was not visible at the 2s deadline")
	}
	if token := m.state.healthToken(); token != "1 stream paused (r)" {
		t.Errorf("header token = %q", token)
	}
}

func TestReconnectInsideDebounceDoesNotFlash(t *testing.T) {
	m, now, _ := clocked(t)
	m.state = testState(t)
	base := *now

	first := m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: false})
	if first == nil {
		t.Fatal("disconnect did not schedule the pause")
	}
	*now = base.Add(1600 * time.Millisecond)
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connecting: true})
	*now = base.Add(2100 * time.Millisecond)
	m.handleStreamState(streamStateMsg{sessionID: "session:rexws", connected: true})

	m.handlePauseTick(first().(pauseTickMsg))
	if m.state.streamPaused["session:rexws"] {
		t.Error("a reconnect inside the window flashed the pause")
	}
	if token := m.state.healthToken(); token != "" {
		t.Errorf("header token = %q", token)
	}
}

func TestRexCheckCooldownBoundsLoads(t *testing.T) {
	fakeRexEvents(t, `{"type":"server_connected","payload":{}}`, `{"type":"server_connected","payload":{}}`)
	m, now, delays := clocked(t)
	m.state = testState(t)
	base := *now

	// A healthy load completes at startup.
	_, _ = m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		rexAvailable: true,
	})
	// The first connect must not schedule a redundant reconcile.
	*now = base.Add(500 * time.Millisecond)
	if cmd := m.handleStreamState(streamStateMsg{sessionID: "", connected: true}); cmd != nil {
		t.Error("connect after a fresh healthy load scheduled a redundant load")
	}

	// A drop schedules a probe.
	probe := m.handleStreamState(streamStateMsg{sessionID: "", connected: false})
	if probe == nil {
		t.Fatal("drop did not schedule a probe")
	}
	*now = base.Add(1500 * time.Millisecond)
	if _, cmd := m.handleRexCheckTick(probe().(rexCheckTickMsg)); cmd == nil {
		t.Fatal("probe did not request a load")
	}
	*now = base.Add(1600 * time.Millisecond)
	_, _ = m.handleLoaded(loadedMsg{
		cfg:          config.Default(),
		workstreams:  sampleWorkstreams(),
		rexAvailable: true,
	})

	// The next drop is deferred to lastRexCheck + rexCooldown, not loaded now.
	*now = base.Add(1700 * time.Millisecond)
	deferred := m.handleStreamState(streamStateMsg{sessionID: "", connected: false})
	if deferred == nil {
		t.Fatal("drop did not defer a probe")
	}
	if len(*delays) != 2 {
		t.Fatalf("delays = %v, want two scheduled checks", *delays)
	}
	if want := rexCooldown - 200*time.Millisecond; (*delays)[1] != want {
		t.Errorf("deferred delay = %v, want %v", (*delays)[1], want)
	}

	// At the cooldown boundary the deferred check fires.
	*now = base.Add(6500 * time.Millisecond)
	if _, cmd := m.handleRexCheckTick(deferred().(rexCheckTickMsg)); cmd == nil {
		t.Error("deferred probe never fired")
	}
}

func TestHubGenerationsAreMonotonicAcrossHubs(t *testing.T) {
	open := func(ctx context.Context, bin, id string, started func(), handle func(rex.Event)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	first := newStreamHub("rex")
	first.openStream = open
	first.Start()
	firstGen := first.Generation()
	first.Close()

	second := newStreamHub("rex")
	second.openStream = open
	second.Start()
	secondGen := second.Generation()
	second.Close()

	if secondGen <= firstGen {
		t.Errorf("new hub reused an old generation: %d then %d", firstGen, secondGen)
	}
}

func TestCoalescingRespectsGeneration(t *testing.T) {
	hub := newStreamHub("rex")
	current := &rex.StatusRecord{State: "blocked"}
	hub.post(context.Background(), streamEventMsg{
		event:      rex.Event{Type: rex.EventProgramStatusChanged, SessionID: "s", BlockID: "b", Record: current},
		generation: 2,
	})
	hub.post(context.Background(), streamEventMsg{
		event:      rex.Event{Type: rex.EventProgramStatusChanged, SessionID: "s", BlockID: "b", Record: &rex.StatusRecord{State: "working"}},
		generation: 1,
	})
	hub.mu.Lock()
	queued := len(hub.queue)
	hub.mu.Unlock()
	if queued != 2 {
		t.Fatalf("generations coalesced together: %d", queued)
	}
	msg, _ := hub.pop()
	if got := msg.(streamEventMsg); got.generation != 2 || got.event.Record != current {
		t.Errorf("current-generation event was displaced: %#v", got)
	}
}

func TestAmbiguityDetailIsSanitized(t *testing.T) {
	sessions := []core.Session{
		{ID: "s1", Label: "repo/\x1b[31mmain"},
		{ID: "s2", Label: "repo/\x1b[31mmain"},
	}
	got := analyzeAmbiguity(sessions, nil)
	if len(got.warnings) != 1 {
		t.Fatalf("warnings = %#v", got.warnings)
	}
	if strings.Contains(got.warnings[0].detail, "\x1b") {
		t.Errorf("detail carries an escape: %q", got.warnings[0].detail)
	}

	s := renderState(t, 80, 24)
	s = s.setPersistentWarnings(got.warnings)
	s.mode = ModeHelp
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Errorf("help rendered a control character: %q", out)
	}
}
