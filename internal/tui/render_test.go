package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	// Pin the glyph set so tests do not depend on the host locale; tests that
	// exercise the fallback set REXWS_ASCII themselves.
	_ = os.Setenv("REXWS_ASCII", "0")
	os.Exit(m.Run())
}

func renderState(t *testing.T, width, height int) State {
	t.Helper()
	s := testState(t)
	s.width, s.height = width, height
	s = s.rebuild()
	return s
}

func assertScreen(t *testing.T, out string, width, height int) []string {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Fatalf("got %d lines, want %d\n%s", len(lines), height, out)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("line %d is %d cells, want <= %d: %q", i, got, width, line)
		}
	}
	return lines
}

func TestRenderLoadingDoesNotClaimRexUnavailable(t *testing.T) {
	s := newState()
	s.width, s.height = 80, 24
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if strings.Contains(out, "Rex unavailable") {
		t.Errorf("loading screen claims Rex is unavailable:\n%s", out)
	}
	if !strings.Contains(out, "Loading…") {
		t.Errorf("loading screen missing:\n%s", out)
	}
}

func TestRender80Columns(t *testing.T) {
	s := renderState(t, 80, 24)
	out := Render(s)
	lines := assertScreen(t, out, 80, 24)

	if !strings.Contains(lines[0], "REXWS") || !strings.Contains(lines[0], "4 workstreams · 3 live · 2 agent-blocked") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(out, "IN PROGRESS (2)") || !strings.Contains(out, "BLOCKED (1)") {
		t.Errorf("groups missing:\n%s", out)
	}
	if !strings.Contains(out, "> rexws") && !strings.Contains(out, "> ▸ rexws") {
		t.Errorf("selection marker missing:\n%s", out)
	}
	if !strings.Contains(out, "blocked (permission)") {
		t.Errorf("agent badge missing:\n%s", out)
	}
	if !strings.Contains(out, "2 tabs") {
		t.Errorf("window count missing:\n%s", out)
	}
	if !strings.Contains(out, "[Enter] switch") || !strings.Contains(out, "[/] filter") {
		t.Errorf("footer = %q", lines[len(lines)-1])
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("ASCII profile render contains an escape:\n%q", out)
	}
}

func TestRenderExpandedShowsWindows(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.withExpanded("/wt/rexws", true)
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(out, "▾ rexws") {
		t.Errorf("expanded chevron missing:\n%s", out)
	}
	if !strings.Contains(out, "• *agent") {
		t.Errorf("active window marker missing:\n%s", out)
	}
	if !strings.Contains(out, "• tests") {
		t.Errorf("window row missing:\n%s", out)
	}
	if !strings.Contains(out, "claude-code") {
		t.Errorf("window app missing:\n%s", out)
	}
}

func TestRender60Columns(t *testing.T) {
	s := renderState(t, 60, 18)
	out := Render(s)
	lines := assertScreen(t, out, 60, 18)
	if !strings.Contains(lines[0], "ws") || strings.Contains(lines[0], "agent-blocked") {
		t.Errorf("narrow header = %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "[Enter] switch") {
		t.Errorf("narrow footer = %q", lines[len(lines)-1])
	}
	if !strings.Contains(out, "IN PROGRESS") {
		t.Errorf("groups missing:\n%s", out)
	}
}

func TestRenderStoppedWorkstreamHasNoChevron(t *testing.T) {
	s := renderState(t, 80, 24)
	out := Render(s)
	var aiLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, ".ai") {
			aiLine = line
		}
	}
	if aiLine == "" {
		t.Fatalf(".ai row missing:\n%s", out)
	}
	if strings.Contains(aiLine, "▸") || strings.Contains(aiLine, "▾") {
		t.Errorf("stopped workstream has a chevron: %q", aiLine)
	}
	if !strings.Contains(aiLine, "stopped") {
		t.Errorf("stopped badge missing: %q", aiLine)
	}
}

func TestRenderTerminalTooSmall(t *testing.T) {
	s := renderState(t, 59, 17)
	if out := Render(s); !strings.Contains(out, "terminal too small") {
		t.Errorf("out = %q", out)
	}
}

func TestRenderFilteredContextParent(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.setQuery("tests")
	s = s.enterSearch()
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(out, "filter: tests ×") {
		t.Errorf("filter chip missing:\n%s", out)
	}
	if !strings.Contains(out, "↳ rexws") {
		t.Errorf("context marker missing:\n%s", out)
	}
	if !strings.Contains(out, "1/2 tabs") {
		t.Errorf("context window count missing:\n%s", out)
	}
	if !strings.Contains(out, "Search: tests") || !strings.Contains(out, "[↑↓] move") {
		t.Errorf("search bar missing:\n%s", out)
	}
}

func TestRenderNoMatches(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.setQuery("zzzz")
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(out, `No matches for "zzzz". Press Esc to clear.`) {
		t.Errorf("no-match message missing:\n%s", out)
	}
}

func TestRenderRexUnavailableUsesQuestionBadges(t *testing.T) {
	s := renderState(t, 80, 24)
	s.rexAvailable = false
	s.rexEverLoaded = false
	s = s.rebuild()
	out := Render(s)
	if !strings.Contains(out, "Rex unavailable") {
		t.Errorf("header missing Rex-unavailable chip:\n%s", out)
	}
	if !strings.Contains(out, "?") {
		t.Errorf("unknown badge missing:\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("Rex outage rendered a workstream as stopped:\n%s", out)
	}
}

func TestRenderOutageKeepsLastKnownBadges(t *testing.T) {
	s := renderState(t, 80, 24)
	s.rexAvailable = false
	s.rexEverLoaded = true
	s = s.rebuild()
	out := Render(s)
	if !strings.Contains(out, "Rex unavailable; paused (r)") {
		t.Errorf("outage header missing:\n%s", out)
	}
	if strings.Contains(out, "‖") {
		t.Errorf("outage header kept per-session pause markers:\n%s", out)
	}
	if !strings.Contains(out, "blocked (permission)") {
		t.Errorf("last-known badge was lost:\n%s", out)
	}
}

func TestRenderNoticeAndMore(t *testing.T) {
	s := renderState(t, 80, 24)
	s = s.addError("first problem")
	s = s.addError("newest problem")
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if !strings.Contains(out, "newest problem") || !strings.Contains(out, "+1 more") {
		t.Errorf("notice missing:\n%s", out)
	}
}

func TestRenderFatal(t *testing.T) {
	s := renderState(t, 80, 24)
	s.mode = ModeFatal
	s.fatalErr = errors.New("parse /tmp/config.json: bad field")
	out := Render(s)
	if !strings.Contains(out, "bad field") || !strings.Contains(out, "[r] retry") {
		t.Errorf("fatal screen missing:\n%s", out)
	}
}

func TestRenderHelpFitsEighteenRows(t *testing.T) {
	s := renderState(t, 60, 18)
	s.mode = ModeHelp
	out := Render(s)
	assertScreen(t, out, 60, 18)
	if !strings.Contains(out, "move selection") {
		t.Errorf("help missing navigation:\n%s", out)
	}
	if !strings.Contains(out, "[↑↓] scroll") {
		t.Errorf("help footer missing:\n%s", out)
	}
}

func TestRenderStatusModal(t *testing.T) {
	s := renderState(t, 80, 24)
	s, _ = Reduce(s, runeKey('s'))
	out := Render(s)
	assertScreen(t, out, 80, 24)
	for _, want := range []string{"╭─ Status", "rexws/m1-m2-core-rex", "In Progress (current)", "Blocked", "[1-9] shortcut"} {
		if !strings.Contains(out, want) {
			t.Errorf("status modal missing %q:\n%s", want, out)
		}
	}
}

func TestRenderNewForm(t *testing.T) {
	s := renderState(t, 80, 24)
	s, _ = Reduce(s, runeKey('n'))
	s.newForm.branch.SetValue("feat/x")
	out := Render(s)
	assertScreen(t, out, 80, 24)
	for _, want := range []string{"New worktree", "Project:", "Branch:", "Provider: git", "Target:"} {
		if !strings.Contains(out, want) {
			t.Errorf("new form missing %q:\n%s", want, out)
		}
	}
}

func TestRenderRemoveModal(t *testing.T) {
	s := renderState(t, 80, 24)
	s.remove = &removeModal{
		plan: ops.RemovePlan{
			Project:  config.Project{Name: "rexws"},
			Path:     "/wt/rexws",
			Branch:   "m1-m2-core-rex",
			Warnings: []error{errors.New("project bad: missing")},
		},
		project: "rexws",
		branch:  "m1-m2-core-rex",
	}
	s.mode = ModeRemove
	out := Render(s)
	assertScreen(t, out, 80, 24)
	for _, want := range []string{"Remove rexws/m1-m2-core-rex?", "/wt/rexws", "closes its Rex session", "Warning: project bad: missing", "[y/Enter] confirm"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove modal missing %q:\n%s", want, out)
		}
	}
}

func TestRenderRemoveModalShowsExactPlan(t *testing.T) {
	s := renderState(t, 100, 30)
	plan := ops.RemovePlan{
		Project:      config.Project{Name: "rexws"},
		Path:         "/wt/rexws",
		Branch:       "feat/x",
		SessionID:    "session:x",
		ProviderName: "git",
		Command:      []string{"git", "-C", "/repo", "worktree", "remove", "--force", "/wt/rexws"},
		Force:        true,
		Loss: ops.RemoveLoss{
			DirtyFiles:    []string{" M tracked.txt", "?? untracked.txt", "!! ignored.txt"},
			Unpushed:      []git.Commit{{SHA: "abc123", Subject: "work"}},
			Unmerged:      []git.Commit{{SHA: "abc123", Subject: "work"}},
			BranchState:   ops.CleanupBranchUnmerged,
			DefaultBranch: "main",
		},
	}
	s.remove = &removeModal{plan: plan, project: "rexws", branch: "feat/x"}
	s.mode = ModeRemove
	out := Render(s)
	assertScreen(t, out, 100, 30)
	for _, want := range []string{
		"Loss:",
		"tracked.txt",
		"untracked.txt",
		"ignored.txt",
		"Retained:",
		"abc123 work",
		"is unmerged",
		"Actions:",
		"close Rex session session:x",
		"run provider \"git\" remove",
		"--force /wt/rexws",
		"retain branch feat/x",
		"clear workflow state",
		"[y/Enter] confirm",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("remove modal missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSanitizesRexStrings(t *testing.T) {
	s := renderState(t, 80, 24)
	s.workstreams[0].Windows = []core.Window{{ID: "window:1", Label: "bad\x1b[31m\nlabel", FocusedBlockID: "block:1"}}
	s.workstreams[0].Agents = append(s.workstreams[0].Agents, core.Agent{
		BlockID: "block:1", WindowID: "window:1", App: "cli\x00ent", State: core.AgentBlocked, Detail: "perm\x1b[0m",
	})
	s.expanded["/wt/rexws"] = true
	s = s.rebuild()
	out := Render(s)
	assertScreen(t, out, 80, 24)
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape reached the render:\n%q", out)
	}
	if strings.Count(out, "\n") != 23 {
		t.Errorf("control character added a line:\n%q", out)
	}
}

func TestRenderRemoveModalDetached(t *testing.T) {
	s := renderState(t, 100, 30)
	plan := ops.RemovePlan{
		Project:      config.Project{Name: "rexws"},
		Path:         "/wt/detached",
		SessionID:    "session:x",
		ProviderName: "git",
		Command:      []string{"git", "worktree", "remove", "/wt/detached"},
		Force:        true,
		Loss: ops.RemoveLoss{
			Detached:        true,
			DetachedCommits: []git.Commit{{SHA: "abc123", Subject: "detached work"}},
		},
	}
	s.remove = &removeModal{plan: plan, project: "rexws", branch: "0123456"}
	s.mode = ModeRemove
	out := Render(s)
	assertScreen(t, out, 100, 30)
	for _, want := range []string{
		"Loss:",
		"commits unique to the detached HEAD",
		"abc123 detached work",
		"Actions:",
		"close Rex session session:x",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detached remove modal missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "retain branch") {
		t.Errorf("detached remove modal claimed a retained branch:\n%s", out)
	}
}
