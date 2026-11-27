package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func setProfile(t *testing.T, profile termenv.Profile) {
	t.Helper()
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(profile)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
}

func setGlyphs(t *testing.T, ascii bool) {
	t.Helper()
	value := "0"
	if ascii {
		value = "1"
	}
	t.Setenv("REXWS_ASCII", value)
}

func TestRenderWidthsUnderFixedProfiles(t *testing.T) {
	profiles := []struct {
		name    string
		profile termenv.Profile
	}{
		{"ascii", termenv.Ascii},
		{"truecolor", termenv.TrueColor},
	}
	for _, p := range profiles {
		for _, width := range []int{48, 60, 80, 120, 200} {
			t.Run(fmt.Sprintf("%s-%d", p.name, width), func(t *testing.T) {
				setProfile(t, p.profile)
				setGlyphs(t, false)
				s := renderState(t, width, 24)
				out := Render(s)
				lines := assertScreen(t, out, width, 24)

				// Every line is padded to the terminal width; at 200 that is the
				// 120-cell block plus symmetric margins.
				for i, line := range lines {
					if got := lipgloss.Width(line); got != width {
						t.Errorf("line %d is %d cells, want %d: %q", i, got, width, line)
					}
				}

				if p.profile == termenv.Ascii && strings.Contains(out, "\x1b") {
					t.Errorf("ASCII profile render contains an escape:\n%q", out)
				}
				if p.profile == termenv.TrueColor && !strings.Contains(out, "\x1b[38;2;") {
					t.Errorf("truecolor render has no palette escape:\n%q", out)
				}

				plain := stripANSI(out)
				header := stripANSI(lines[0])
				if !strings.Contains(header, "REXWS") {
					t.Errorf("header lost the brand: %q", header)
				}
				if width >= 80 {
					if !strings.Contains(header, "4 workstreams · 3 live · 2 agent-blocked") {
						t.Errorf("wide counts chip is incomplete: %q", header)
					}
				} else if !strings.Contains(header, "4 ws · 3 live") || strings.Contains(header, "agent-blocked") {
					t.Errorf("compact counts chip is wrong: %q", header)
				}
				if width >= 120 && !strings.HasSuffix(strings.TrimRight(header, " "), "]") {
					t.Errorf("header counts are not right-aligned at %d: %q", width, header)
				}

				// Semantic text carriers survive with and without color.
				if !strings.Contains(plain, "IN PROGRESS (2)") || !strings.Contains(plain, "BLOCKED (1)") {
					t.Errorf("groups missing:\n%s", plain)
				}
				wantBadge := "blocked (permissi"
				switch {
				case width < 60:
					wantBadge = "blocked (per…"
				case width >= 80:
					wantBadge = "blocked (permission)"
				}
				if !strings.Contains(plain, wantBadge) {
					t.Errorf("badge missing %q:\n%s", wantBadge, plain)
				}
				if !strings.Contains(plain, "> ▸ rexws") && !strings.Contains(plain, "> v rexws") {
					t.Errorf("selection marker missing:\n%s", plain)
				}
				if !strings.Contains(plain, "[Enter] switch") || !strings.Contains(plain, "[/] filter") {
					t.Errorf("footer missing priority keys: %q", stripANSI(lines[len(lines)-1]))
				}
			})
		}
	}
}

func TestRenderContentCapAt200(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 200, 24)
	out := Render(s)
	lines := assertScreen(t, out, 200, 24)

	margin := strings.Repeat(" ", 40)
	rule := strings.Repeat("─", 120)
	if want := margin + rule + margin; lines[1] != want {
		t.Errorf("separator is not the centered 120-cell rule:\n%q", lines[1])
	}
	for i, line := range lines {
		if i == 1 || strings.TrimRight(line, " ") == "" {
			continue
		}
		if !strings.HasPrefix(line, margin) {
			t.Errorf("line %d starts outside the centered block: %q", i, line)
			continue
		}
		content := strings.TrimSuffix(strings.TrimPrefix(line, margin), margin)
		if got := lipgloss.Width(content); got > 120 {
			t.Errorf("line %d content is %d cells, want <= 120: %q", i, got, line)
		}
	}
}

func TestRenderColumnGridAlignsBadges(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	cases := []struct {
		width      int
		project    int
		badge      int
		count      int
		showCount  bool
		branch     int
		badgeStart int
		badgeText  string
	}{
		{48, 10, 13, 0, false, 14, 35, "blocked (per…"},
		{60, 10, 18, 0, false, 21, 42, "blocked (permissi"},
		{80, 12, 28, 8, true, 19, 42, "blocked (permissi"},
		{120, 16, 28, 8, true, 55, 82, "blocked (permissi"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			s := renderState(t, tc.width, 24)
			s = s.withExpanded("/wt/rexws", true)
			out := Render(s)
			assertScreen(t, out, tc.width, 24)

			g := newGrid(s.rows, tc.width)
			if g.project != tc.project || g.badge != tc.badge || g.count != tc.count || g.showCount != tc.showCount {
				t.Errorf("grid = project %d badge %d count %d show %t, want %d/%d/%d/%t",
					g.project, g.badge, g.count, g.showCount, tc.project, tc.badge, tc.count, tc.showCount)
			}
			if g.branch != tc.branch {
				t.Errorf("branch = %d, want %d", g.branch, tc.branch)
			}
			// The parent layout is literal: gutter/selection/tree (5), project,
			// gap (2), branch, gap (1), marker (2), gap (1), badge.
			badgeStart := 5 + tc.project + 2 + tc.branch + 1 + 2 + 1
			if badgeStart != tc.badgeStart {
				t.Fatalf("badge start formula = %d, want %d", badgeStart, tc.badgeStart)
			}

			parent := lineContaining(t, out, "m1-m2-core-rex")
			parentBadge := strings.Index(parent, tc.badgeText)
			if parentBadge < 0 {
				t.Fatalf("parent badge missing: %q", parent)
			}
			if got := lipgloss.Width(parent[:parentBadge]); got != badgeStart {
				t.Errorf("parent badge starts at %d, want %d: %q", got, badgeStart, parent)
			}

			child := lineContaining(t, out, "• tests")
			childBadge := strings.Index(child, tc.badgeText)
			if childBadge < 0 {
				t.Fatalf("child badge missing: %q", child)
			}
			if got := lipgloss.Width(child[:childBadge]); got != badgeStart {
				t.Errorf("child badge starts at %d, want %d: %q", got, badgeStart, child)
			}
		})
	}
}

func TestRenderFooterStatusRightAligned(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)

	s := renderState(t, 80, 24)
	s.pending = true
	s.pendingLabel = "switching…"
	s = s.rebuild()
	out := Render(s)
	lines := assertScreen(t, out, 80, 24)
	footer := lines[len(lines)-1]
	if !strings.HasSuffix(footer, "switching…") {
		t.Errorf("footer status is not right-aligned: %q", footer)
	}
	if got := lipgloss.Width(footer); got != 80 {
		t.Errorf("footer is %d cells, want 80: %q", got, footer)
	}

	narrow := renderState(t, 60, 18)
	narrow.pending = true
	narrow.pendingLabel = "switching…"
	narrow = narrow.rebuild()
	narrowOut := Render(narrow)
	narrowLines := assertScreen(t, narrowOut, 60, 18)
	if !strings.Contains(narrowLines[len(narrowLines)-1], "switching…") {
		t.Errorf("compact footer dropped the status: %q", narrowLines[len(narrowLines)-1])
	}

	// The longest pending label plus an active filter must still fit at 60.
	crowded := renderState(t, 60, 18)
	crowded = crowded.setQuery("rexws")
	crowded.pending = true
	crowded.pendingLabel = "saving status…"
	crowded = crowded.rebuild()
	crowdedOut := Render(crowded)
	crowdedLines := assertScreen(t, crowdedOut, 60, 18)
	crowdedFooter := strings.TrimRight(crowdedLines[len(crowdedLines)-1], " ")
	if !strings.HasSuffix(crowdedFooter, "saving status…") {
		t.Errorf("compact footer dropped a crowded status: %q", crowdedFooter)
	}
}

func TestRenderASCIIGlyphFallback(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, true)
	s := renderState(t, 80, 24)
	s = s.withExpanded("/wt/rexws", true)
	out := Render(s)
	assertScreen(t, out, 80, 24)

	for _, glyph := range []string{"▸", "▾", "↳", "•", "▌", "—", "…", "·", "×", "─", "╭", "╮", "╰", "╯", "│"} {
		if strings.Contains(out, glyph) {
			t.Errorf("ASCII mode rendered Unicode glyph %q:\n%s", glyph, out)
		}
	}
	for _, want := range []string{"| IN PROGRESS (2)", "v rexws", "- *agent", "- tests"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASCII mode missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("ASCII mode contains an escape:\n%q", out)
	}

	s.pending = true
	s.pendingLabel = "switching…"
	s = s.rebuild()
	out = Render(s)
	if strings.Contains(out, "…") {
		t.Errorf("ASCII mode kept an ellipsis in the pending label:\n%s", out)
	}
	if !strings.Contains(out, "switching~") {
		t.Errorf("ASCII pending label missing:\n%s", out)
	}
}

func TestRenderTruecolorKeepsNonColorCarriers(t *testing.T) {
	setProfile(t, termenv.TrueColor)
	setGlyphs(t, false)
	s := renderState(t, 80, 24)
	out := Render(s)

	group := lineContaining(t, out, "IN PROGRESS")
	if !strings.Contains(group, "\x1b[") {
		t.Errorf("group accent has no color: %q", group)
	}
	if !strings.Contains(stripANSI(group), "▌ IN PROGRESS (2)") {
		t.Errorf("group label carrier missing: %q", stripANSI(group))
	}

	selected := lineContaining(t, out, "m1-m2-core-rex")
	if !strings.Contains(stripANSI(selected), "> ") {
		t.Errorf("selection marker missing under color: %q", stripANSI(selected))
	}
	if !strings.Contains(out, "blocked (permission)") {
		t.Errorf("badge text carrier missing:\n%s", out)
	}

	s = s.withExpanded("/wt/rexws", true)
	out = Render(s)
	if !strings.Contains(stripANSI(out), "• *agent") {
		t.Errorf("active-window carrier missing:\n%s", stripANSI(out))
	}
}

// stripANSI removes SGR sequences so assertions can look for text carriers.
func stripANSI(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '[' {
			j := i + 2
			for j < len(value) && (value[j] < 0x40 || value[j] > 0x7e) {
				j++
			}
			if j < len(value) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func TestRenderStateCopies(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)

	t.Run("empty-60", func(t *testing.T) {
		s := renderState(t, 60, 18)
		s.workstreams = nil
		s = s.rebuild()
		out := Render(s)
		assertScreen(t, out, 60, 18)
		if !strings.Contains(out, "No workstreams.") || !strings.Contains(out, "Register one: rexws project add <path>") {
			t.Errorf("compact empty copy missing:\n%s", out)
		}
		if strings.Contains(out, "Register a repository") {
			t.Errorf("old empty copy remains:\n%s", out)
		}
		if strings.Contains(out, "[0 ws") {
			t.Errorf("zero counts chip should be suppressed:\n%s", out)
		}
	})

	t.Run("empty-80", func(t *testing.T) {
		s := renderState(t, 80, 24)
		s.workstreams = nil
		s = s.rebuild()
		out := Render(s)
		assertScreen(t, out, 80, 24)
		if !strings.Contains(out, "No workstreams. Register one with: rexws project add <path>") {
			t.Errorf("wide empty copy missing:\n%s", out)
		}
	})

	t.Run("loading", func(t *testing.T) {
		for _, width := range []int{60, 80} {
			s := newState()
			s.width, s.height = width, 24
			out := Render(s)
			assertScreen(t, out, width, 24)
			if !strings.Contains(out, "Loading…") {
				t.Errorf("%d loading copy missing:\n%s", width, out)
			}
			if strings.Contains(out, "Rex unavailable") {
				t.Errorf("%d loading claims Rex is unavailable:\n%s", width, out)
			}
			hasDetail := strings.Contains(out, "Reading Git worktrees and Rex sessions")
			if width >= 80 && !hasDetail {
				t.Errorf("wide loading detail missing:\n%s", out)
			}
			if width < 80 && hasDetail {
				t.Errorf("compact loading kept the second line:\n%s", out)
			}
		}
	})

	t.Run("no-matches-60", func(t *testing.T) {
		s := renderState(t, 60, 18)
		s = s.setQuery("zzzz")
		out := Render(s)
		assertScreen(t, out, 60, 18)
		if !strings.Contains(out, `No matches for "zzzz".`) || !strings.Contains(out, "Press Esc to clear.") {
			t.Errorf("compact no-match copy missing:\n%s", out)
		}
	})

	t.Run("too-small", func(t *testing.T) {
		s := renderState(t, 42, 10)
		out := Render(s)
		if !strings.Contains(out, "terminal too small") || !strings.Contains(out, "need 48x18, have 42x10") {
			t.Errorf("too-small copy missing:\n%s", out)
		}
	})

	t.Run("fatal-config", func(t *testing.T) {
		s := renderState(t, 80, 24)
		s.mode = ModeFatal
		s.fatalKind = "config"
		s.fatalErr = errors.New("parse /tmp/config.json: bad field")
		out := Render(s)
		assertScreen(t, out, 80, 24)
		for _, want := range []string{"Cannot load config.json", "Fix ~/.config/rexws/config.json", "rexws list shows the same error outside the TUI.", "[r] retry"} {
			if !strings.Contains(out, want) {
				t.Errorf("fatal config copy missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("fatal-state", func(t *testing.T) {
		s := renderState(t, 80, 24)
		s.mode = ModeFatal
		s.fatalKind = "state"
		s.fatalErr = errors.New(strings.Repeat("corrupt state record ", 12))
		out := Render(s)
		assertScreen(t, out, 80, 24)
		if !strings.Contains(out, "Cannot load state.json") || !strings.Contains(out, "Fix ~/.config/rexws/state.json") {
			t.Errorf("fatal state copy missing:\n%s", out)
		}
	})
}

func TestRenderFatalReservesKeysAt60(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 60, 18)
	s.mode = ModeFatal
	s.fatalKind = "config"
	s.fatalErr = errors.New(strings.Repeat("long failure detail ", 40))
	out := Render(s)
	assertScreen(t, out, 60, 18)
	for _, want := range []string{"[r] retry", "[q] quit", "Fix ~/.config/rexws/config.json", "rexws list shows the same error outside the TUI."} {
		if !strings.Contains(out, want) {
			t.Errorf("fatal screen lost %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "…") {
		t.Errorf("long fatal error was not marked as truncated:\n%s", out)
	}
	if strings.Contains(out, "……") {
		t.Errorf("long fatal error has a doubled truncation marker:\n%s", out)
	}
}

func TestRenderMarkersAtWidthsAndGlyphs(t *testing.T) {
	setProfile(t, termenv.Ascii)
	for _, ascii := range []bool{false, true} {
		setGlyphs(t, ascii)
		g := currentGlyphs()
		for _, width := range []int{60, 80, 120} {
			t.Run(fmt.Sprintf("%s-%d", glyphName(ascii), width), func(t *testing.T) {
				badgeStart := 42
				if width >= 120 {
					badgeStart = 82
				}
				ambiguous := renderState(t, width, 24)
				ambiguous.ambiguousLabels["rexws/m1-m2-core-rex"] = true
				ambiguous = ambiguous.rebuild()
				if row := lineContaining(t, Render(ambiguous), "m1-m2-core-rex"); !strings.Contains(row, g.ambiguous+" ") {
					t.Errorf("ambiguous marker missing: %q", row)
				}

				paused := renderState(t, width, 24)
				paused.streamPaused["session:rexws"] = true
				paused = paused.rebuild()
				if row := lineContaining(t, Render(paused), "m1-m2-core-rex"); !strings.Contains(row, g.paused) {
					t.Errorf("paused marker missing: %q", row)
				}

				both := renderState(t, width, 24)
				both.ambiguousLabels["rexws/m1-m2-core-rex"] = true
				both.streamPaused["session:rexws"] = true
				both = both.rebuild()
				row := lineContaining(t, Render(both), "m1-m2-core-rex")
				if !strings.Contains(row, g.both) {
					t.Errorf("combined marker missing: %q", row)
				}
				index := strings.Index(row, "blocked (permissi")
				if index < 0 {
					t.Fatalf("marked row badge missing: %q", row)
				}
				if got := lipgloss.Width(row[:index]); got != badgeStart {
					t.Errorf("marked badge starts at %d, want %d: %q", got, badgeStart, row)
				}
			})
		}
	}
}

func glyphName(ascii bool) string {
	if ascii {
		return "ascii"
	}
	return "unicode"
}

func TestRenderLongModalPathsAndWarnings(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)

	t.Run("new-target", func(t *testing.T) {
		s := renderState(t, 60, 18)
		s, _ = Reduce(s, runeKey('n'))
		s.newForm.branch.SetValue("feat/a-very-long-branch-name-that-keeps-going")
		out := Render(s)
		assertScreen(t, out, 60, 18)
		target := lineContaining(t, out, "Target:")
		if !strings.Contains(target, "keeps-going") {
			t.Errorf("target lost its basename: %q", target)
		}
		if !strings.Contains(target, "…") {
			t.Errorf("long target was not middle-truncated: %q", target)
		}
	})

	t.Run("status-path", func(t *testing.T) {
		s := renderState(t, 60, 18)
		s, _ = Reduce(s, runeKey('s'))
		s.status.project = strings.Repeat("long-project-name/", 4) + "rexws"
		s.status.branch = "a-very-long-branch-name-that-keeps-going"
		out := Render(s)
		assertScreen(t, out, 60, 18)
		path := lineContaining(t, out, "keeps-going")
		if !strings.Contains(path, "…") {
			t.Errorf("long status path was not middle-truncated: %q", path)
		}
	})

	t.Run("remove-path", func(t *testing.T) {
		s := renderState(t, 60, 18)
		s.remove = &removeModal{
			plan: ops.RemovePlan{
				Project: config.Project{Name: "rexws"},
				Path:    "/wt/rexws/a/very/long/path/that/ends-in-a-basename",
				Branch:  "m1-m2-core-rex",
			},
			project: "rexws",
			branch:  "m1-m2-core-rex",
		}
		s.mode = ModeRemove
		out := Render(s)
		assertScreen(t, out, 60, 18)
		worktree := lineContaining(t, out, "Worktree:")
		if !strings.Contains(worktree, "basename") {
			t.Errorf("worktree path lost its basename: %q", worktree)
		}
		if !strings.Contains(worktree, "…") {
			t.Errorf("long worktree path was not middle-truncated: %q", worktree)
		}
	})

	t.Run("warning-overflow", func(t *testing.T) {
		s := renderState(t, 60, 18)
		warnings := make([]error, 0, 5)
		for i := 0; i < 5; i++ {
			warnings = append(warnings, errors.New(strings.Repeat("very long warning detail ", 6)))
		}
		s.remove = &removeModal{
			plan: ops.RemovePlan{
				Project:  config.Project{Name: "rexws"},
				Path:     "/wt/rexws",
				Branch:   "m1-m2-core-rex",
				Warnings: warnings,
			},
			project: "rexws",
			branch:  "m1-m2-core-rex",
		}
		s.mode = ModeRemove
		out := Render(s)
		assertScreen(t, out, 60, 18)
		if !strings.Contains(out, "Warning:") || !strings.Contains(out, "more warnings") {
			t.Errorf("warning overflow was not summarized:\n%s", out)
		}
	})
}

func TestRenderHelpPanelComplete(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	cases := []struct {
		width    int
		height   int
		boxWidth int
	}{
		{48, 44, 46},
		{60, 18, 56},
		{60, 24, 56},
		{80, 24, 64},
		{120, 24, 88},
		{200, 24, 88},
	}
	wants := []string{
		"╭─ Help",
		"Markers", "> selected", "collapsed/expanded", "context",
		"? unknown", "— none",
		"ambiguous session binding", "live updates paused for this session",
		"dismiss the newest action error",
		"Keys", "move selection", "expand / first child", "back / collapse",
		"toggle / switch", "status/new/remove/refresh", "search / help / quit",
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			s := renderState(t, tc.width, tc.height)
			s.mode = ModeHelp
			out := Render(s)
			assertScreen(t, out, tc.width, tc.height)
			plain := stripANSI(out)
			for _, want := range wants {
				if !strings.Contains(plain, want) {
					t.Errorf("help missing %q:\n%s", want, plain)
				}
			}
			border := lineContaining(t, out, "╭─ Help")
			start := strings.Index(border, "╭")
			end := strings.LastIndex(border, "╮")
			if start < 0 || end < 0 {
				t.Fatalf("help border missing: %q", border)
			}
			if got := lipgloss.Width(border[start : end+len("╮")]); got != tc.boxWidth {
				t.Errorf("help box width = %d, want %d: %q", got, tc.boxWidth, border)
			}
			contentWidth := min(tc.width, 120)
			wantStart := (tc.width-contentWidth)/2 + (contentWidth-tc.boxWidth)/2
			if start != wantStart {
				t.Errorf("help box starts at %d, want %d: %q", start, wantStart, border)
			}
		})
	}
}

func TestRenderHelpWithWarningAt60(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 60, 18)
	s = s.setPersistentWarnings([]persistentWarning{
		{text: "project alpha: repository missing", class: warningDiscovery},
	})
	s.mode = ModeHelp
	out := Render(s)
	assertScreen(t, out, 60, 18)
	for _, want := range []string{
		"Warnings", "project alpha: repository missing",
		"ambiguous session binding", "dismiss the newest action error",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help with a warning missing %q:\n%s", want, out)
		}
	}
	// The reduced body scrolls instead of dropping the key map.
	s.helpOffset = 100
	scrolled := Render(s)
	if !strings.Contains(scrolled, "search / help / quit") {
		t.Errorf("scrolled help did not reach the key map:\n%s", scrolled)
	}
}

func TestCompactFooterPendingUsesInfoRole(t *testing.T) {
	setRenderer(t, termenv.TrueColor, false)
	setGlyphs(t, false)

	s := renderState(t, 60, 18)
	s.pending = true
	s.pendingLabel = "switching…"
	s = s.rebuild()
	out := Render(s)
	assertScreen(t, out, 60, 18)
	footer := lineContaining(t, out, "switching…")
	if !strings.Contains(footer, "38;2;14;116;144") {
		t.Errorf("compact pending suffix has no info color: %q", footer)
	}
	if !strings.Contains(stripANSI(footer), "switching…") {
		t.Errorf("compact pending suffix text missing: %q", footer)
	}
}

func TestCompactFooterDropsHelpForCrowdedStatus(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	for _, query := range []string{"", "rexws"} {
		t.Run("query="+query, func(t *testing.T) {
			s := renderState(t, 60, 18)
			if query != "" {
				s = s.setQuery(query)
			}
			s.pending = true
			s.pendingLabel = "saving status…"
			s = s.rebuild()
			out := Render(s)
			lines := assertScreen(t, out, 60, 18)
			footer := strings.TrimRight(stripANSI(lines[len(lines)-1]), " ")
			if !strings.HasSuffix(footer, "saving status…") {
				t.Errorf("crowded status was dropped: %q", footer)
			}
			if strings.Contains(footer, "[?] help") {
				t.Errorf("help key was kept when the status needed its room: %q", footer)
			}
		})
	}
}

func TestHeaderCountsYieldToLongQuery(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	long := strings.Repeat("q", 80)
	s := renderState(t, 80, 24)
	// Make the long query match a visible workstream so the counts chip is
	// non-empty; otherwise a zero-visible count would hide it for the wrong
	// reason.
	s.workstreams[0].Project = long
	s = s.setQuery(long)
	if s.build.Workstreams == 0 {
		t.Fatalf("long query matched no workstreams: %+v", s.build)
	}
	out := Render(s)
	lines := assertScreen(t, out, 80, 24)
	header := stripANSI(lines[0])
	if strings.Contains(header, "workstreams") || strings.Contains(header, "agent-blocked") || strings.Contains(header, "[") {
		t.Errorf("header kept counts instead of the query: %q", header)
	}
	if !strings.Contains(header, strings.Repeat("q", 40)) {
		t.Errorf("header query was truncated too early: %q", header)
	}
}

func TestSearchFooterLongQueryKeepsKeys(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	for _, width := range []int{60, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s := renderState(t, width, 24)
			s = s.setQuery(strings.Repeat("query", 16))
			s = s.enterSearch()
			out := Render(s)
			lines := assertScreen(t, out, width, 24)
			footer := stripANSI(lines[len(lines)-1])
			if got := lipgloss.Width(footer); got != width {
				t.Errorf("search footer is %d cells, want %d: %q", got, width, footer)
			}
			for _, want := range []string{"[↑↓] move", "[Enter] accept", "[Esc] clear"} {
				if !strings.Contains(footer, want) {
					t.Errorf("search footer missing %q: %q", want, footer)
				}
			}
			if !strings.Contains(out, "…") {
				t.Errorf("long no-match copy was cut without a truncation marker:\n%s", out)
			}
		})
	}
}

func TestSearchFooterFitsAt60(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 60, 18)
	s, _ = Reduce(s, runeKey('/'))
	out := Render(s)
	lines := assertScreen(t, out, 60, 18)
	footer := stripANSI(lines[len(lines)-1])
	for _, want := range []string{"[Enter] accept", "[Esc] clear", "move"} {
		if !strings.Contains(footer, want) {
			t.Errorf("search footer missing %q: %q", want, footer)
		}
	}
}

func TestNewFormLongBranchFitsAt60(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, true)
	s := renderState(t, 60, 18)
	s, _ = Reduce(s, runeKey('n'))
	s.newForm.branch.SetValue("feat/" + strings.Repeat("long-branch-", 6) + "TAIL")
	out := Render(s)
	assertScreen(t, out, 60, 18)
	branch := lineContaining(t, out, "Branch:")
	if !strings.Contains(branch, "TAIL ") {
		t.Errorf("long branch field lost its tail or cursor cell: %q", branch)
	}
	if got := lipgloss.Width(branch); got != 60 {
		t.Errorf("branch line is %d cells, want 60: %q", got, branch)
	}
	if !strings.HasSuffix(strings.TrimRight(branch, " "), "|") {
		t.Errorf("branch field was cut before the box border: %q", branch)
	}
}

func TestRenderASCIIEveryScreen(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, true)

	screens := []struct {
		name  string
		state State
	}{
		{"normal", func() State {
			s := renderState(t, 60, 18)
			return s.withExpanded("/wt/rexws", true)
		}()},
		{"filtered", func() State {
			s := renderState(t, 60, 18)
			return s.setQuery("agent")
		}()},
		{"search", func() State {
			s := renderState(t, 60, 18)
			s, _ = Reduce(s, runeKey('/'))
			return s
		}()},
		{"help", func() State {
			s := renderState(t, 60, 18)
			s.mode = ModeHelp
			return s
		}()},
		{"status", func() State {
			s := renderState(t, 60, 18)
			s, _ = Reduce(s, runeKey('s'))
			return s
		}()},
		{"remove", func() State {
			s := renderState(t, 60, 18)
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
			return s
		}()},
		{"new", func() State {
			s := renderState(t, 60, 18)
			s, _ = Reduce(s, runeKey('n'))
			s.newForm.branch.SetValue("feat/x")
			return s
		}()},
		{"fatal", func() State {
			s := renderState(t, 60, 18)
			s.mode = ModeFatal
			s.fatalKind = "config"
			s.fatalErr = errors.New("bad field")
			return s
		}()},
		{"empty", func() State {
			s := renderState(t, 60, 18)
			s.workstreams = nil
			return s.rebuild()
		}()},
		{"loading", func() State {
			s := newState()
			s.width, s.height = 60, 18
			return s
		}()},
		{"no-match", func() State {
			s := renderState(t, 60, 18)
			return s.setQuery("zzzz")
		}()},
		{"markers", func() State {
			s := renderState(t, 60, 18)
			s.ambiguousLabels["rexws/m1-m2-core-rex"] = true
			s.streamPaused["session:rexws"] = true
			return s.rebuild()
		}()},
		{"pending", func() State {
			s := renderState(t, 60, 18)
			s.pending = true
			s.pendingLabel = "saving status…"
			return s.rebuild()
		}()},
	}
	for _, screen := range screens {
		t.Run(screen.name, func(t *testing.T) {
			out := Render(screen.state)
			assertScreen(t, out, 60, 18)
			if strings.Contains(out, "\x1b") {
				t.Errorf("ASCII screen contains an escape:\n%q", out)
			}
			for _, r := range out {
				if r > 127 {
					t.Errorf("ASCII screen contains non-ASCII rune %q:\n%s", r, out)
					break
				}
			}
		})
	}
}

func TestNewGridWidthTiers(t *testing.T) {
	setGlyphs(t, false)
	rows := renderState(t, 80, 24).rows
	cases := []struct {
		width             int
		project, branch   int
		badge, label, app int
		count             int
		showCount         bool
	}{
		{48, 10, 14, 13, 12, 11, 0, false},
		{59, 10, 25, 13, 12, 22, 0, false},
		{60, 10, 21, 18, 12, 18, 0, false},
		{80, 12, 19, 28, 12, 18, 8, true},
	}
	for _, tc := range cases {
		g := newGrid(rows, tc.width)
		if g.project != tc.project || g.branch != tc.branch || g.badge != tc.badge ||
			g.label != tc.label || g.app != tc.app || g.count != tc.count || g.showCount != tc.showCount {
			t.Errorf("newGrid(%d) = project %d branch %d badge %d label %d app %d count %d show %t",
				tc.width, g.project, g.branch, g.badge, g.label, g.app, g.count, g.showCount)
		}
	}
}

func TestRenderTooSmallBoundaries(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	rejected := []struct {
		width, height int
		copy          string
	}{
		{47, 44, "need 48x18, have 47x44"},
		{48, 17, "need 48x18, have 48x17"},
	}
	for _, tc := range rejected {
		out := Render(renderState(t, tc.width, tc.height))
		if !strings.Contains(out, "terminal too small") || !strings.Contains(out, tc.copy) {
			t.Errorf("%dx%d did not report too small with %q:\n%s", tc.width, tc.height, tc.copy, out)
		}
	}
	out := Render(renderState(t, 48, 18))
	if strings.Contains(out, "terminal too small") {
		t.Errorf("48x18 is rejected:\n%s", out)
	}
	assertScreen(t, out, 48, 18)
}

func TestHeaderWholeChipsAt48(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	health := []struct {
		name  string
		token string
		build func(t *testing.T) State
	}{
		{"none", "", func(t *testing.T) State { return renderState(t, 48, 44) }},
		{"rex-never-loaded", "Rex unavailable (r)", func(t *testing.T) State {
			s := renderState(t, 48, 44)
			s.rexAvailable, s.rexEverLoaded = false, false
			return s.rebuild()
		}},
		{"rex-paused", "Rex unavailable; paused (r)", func(t *testing.T) State {
			s := renderState(t, 48, 44)
			s.rexAvailable, s.rexEverLoaded = false, true
			return s.rebuild()
		}},
		{"live-paused", "live paused (r)", func(t *testing.T) State {
			s := renderState(t, 48, 44)
			for _, id := range s.liveSessionIDs() {
				s.streamPaused[id] = true
			}
			return s.rebuild()
		}},
		{"one-stream", "1 stream paused (r)", func(t *testing.T) State {
			s := renderState(t, 48, 44)
			s.streamPaused["session:rexws"] = true
			return s.rebuild()
		}},
		{"two-streams", "2 streams paused (r)", func(t *testing.T) State {
			s := renderState(t, 48, 44)
			s.streamPaused["session:rexws"] = true
			s.streamPaused["session:dosido"] = true
			return s.rebuild()
		}},
	}
	queries := []string{"", "a", "rex", strings.Repeat("q", 40)}
	for _, hc := range health {
		for _, query := range queries {
			t.Run(hc.name+"/"+fmt.Sprintf("%d", len(query)), func(t *testing.T) {
				s := hc.build(t)
				if query != "" {
					s = s.setQuery(query)
				}
				lines := assertScreen(t, Render(s), 48, 44)
				header := strings.TrimRight(stripANSI(lines[0]), " ")
				if hc.token != "" && !strings.Contains(header, hc.token) {
					t.Errorf("health token %q was cut: %q", hc.token, header)
				}
				// Counts are a whole chip or absent, and always end the line.
				if strings.Contains(header, "[") && !strings.HasSuffix(header, "]") {
					t.Errorf("counts chip was cut: %q", header)
				}
				// The filter chip is whole or absent, never a `filter: … ×`
				// sliver: when present it keeps its terminator.
				if at := strings.Index(header, "filter: "); at >= 0 {
					chip := header[at+len("filter: "):]
					terminator := strings.Index(chip, " "+currentGlyphs().times)
					if terminator < 0 {
						t.Errorf("filter chip lost its terminator: %q", header)
					}
				}
			})
		}
	}

	// A one-cell query with the longest health token leaves only one usable
	// query cell; the whole chip must be dropped even though the built line is
	// exactly 48 cells and would otherwise fit.
	t.Run("short-query-long-health-drops-chip", func(t *testing.T) {
		s := renderState(t, 48, 44)
		s.rexAvailable, s.rexEverLoaded = false, true
		s = s.setQuery("a")
		lines := assertScreen(t, Render(s), 48, 44)
		header := strings.TrimRight(stripANSI(lines[0]), " ")
		if strings.Contains(header, "filter:") {
			t.Errorf("one-cell query kept the chip: %q", header)
		}
		if !strings.Contains(header, "Rex unavailable; paused (r)") {
			t.Errorf("health token was cut: %q", header)
		}
	})
}

func TestRenderLongWindowLabelAt48(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 48, 24)
	s = s.withExpanded("/wt/rexws", true)
	s.workstreams[0].Windows[0].Label = "long-window-label-that-overflows"
	s = s.rebuild()

	g := newGrid(s.rows, 48)
	if g.label != 16 || g.app != 7 {
		t.Fatalf("grid = label %d app %d, want 16/7", g.label, g.app)
	}
	out := Render(s)
	assertScreen(t, out, 48, 24)
	window := lineContaining(t, out, "long-window")
	badge := strings.Index(window, "working")
	if badge < 0 {
		t.Fatalf("window badge missing: %q", window)
	}
	if got := lipgloss.Width(window[:badge]); got != 35 {
		t.Errorf("window badge starts at %d, want 35: %q", got, window)
	}
}

func TestRenderPendingSuffixAt48(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	labels := []string{
		"switching…", "saving status…", "checking…", "removing…",
		"preparing…", "creating…", "verifying…",
	}
	for _, query := range []string{"", "rexws"} {
		for _, label := range labels {
			t.Run(label+"/"+query, func(t *testing.T) {
				s := renderState(t, 48, 18)
				if query != "" {
					s = s.setQuery(query)
				}
				s.pending = true
				s.pendingLabel = label
				s = s.rebuild()
				lines := assertScreen(t, Render(s), 48, 18)
				footer := strings.TrimRight(stripANSI(lines[len(lines)-1]), " ")
				if !strings.HasSuffix(footer, label) {
					t.Errorf("pending suffix %q was dropped: %q", label, footer)
				}
				if !strings.Contains(footer, "[Enter] switch") {
					t.Errorf("pending footer dropped the switch key: %q", footer)
				}
			})
		}
	}
	t.Run("refreshing", func(t *testing.T) {
		s := renderState(t, 48, 18)
		s.loading = true
		s = s.rebuild()
		lines := assertScreen(t, Render(s), 48, 18)
		if !strings.Contains(stripANSI(lines[len(lines)-1]), "refreshing…") {
			t.Errorf("refresh suffix was dropped: %q", stripANSI(lines[len(lines)-1]))
		}
	})
}

func TestRenderHelpAt48(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	s := renderState(t, 48, 18)
	s.mode = ModeHelp
	out := Render(s)
	assertScreen(t, out, 48, 18)
	plain := stripANSI(out)
	for _, want := range []string{
		"╭─ Help",
		"Markers",
		"▸▾ collapsed/expanded",
		"↳ context  ? unknown  — none",
		"ambiguous session binding",
		"live updates paused for this session",
		"!‖  both",
		"x   dismiss the newest action error",
		"Keys",
		"space enter  toggle / switch",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("48-column help missing %q:\n%s", want, plain)
		}
	}
	border := lineContaining(t, out, "╭─ Help")
	start := strings.Index(border, "╭")
	end := strings.LastIndex(border, "╮")
	if start < 0 || end < 0 {
		t.Fatalf("help border missing: %q", border)
	}
	if got := lipgloss.Width(border[start : end+len("╮")]); got != 46 {
		t.Errorf("48-column help box = %d cells, want 46: %q", got, border)
	}
	// The rest of the key map scrolls into view rather than clipping.
	s.helpOffset = 100
	scrolled := stripANSI(Render(s))
	for _, want := range []string{"s n d r  status/new/remove/refresh", "c  cleanup", "/ ? q        search / help / quit"} {
		if !strings.Contains(scrolled, want) {
			t.Errorf("scrolled 48-column help missing %q:\n%s", want, scrolled)
		}
	}
}

func TestRender48ASCII(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, true)
	s := renderState(t, 48, 44)
	s = s.withExpanded("/wt/rexws", true)
	for _, screen := range []State{s, func() State {
		h := renderState(t, 48, 44)
		h.mode = ModeHelp
		return h
	}()} {
		out := Render(screen)
		assertScreen(t, out, 48, 44)
		if strings.Contains(out, "\x1b") {
			t.Errorf("ASCII 48 render contains an escape:\n%q", out)
		}
		for _, r := range out {
			if r > 127 {
				t.Errorf("ASCII 48 render contains non-ASCII rune %q:\n%s", r, out)
				break
			}
		}
	}
}

func TestSearchInputWidthAgreesAt48(t *testing.T) {
	setProfile(t, termenv.Ascii)
	setGlyphs(t, false)
	if got := searchInputWidth(48); got != 10 {
		t.Errorf("searchInputWidth(48) = %d, want 10", got)
	}
	if got := searchInputWidth(60); got != 12 {
		t.Errorf("searchInputWidth(60) = %d, want 12", got)
	}
	if strings.Contains(searchKeys(48), "move") {
		t.Errorf("48-column search keys kept the move hint: %q", searchKeys(48))
	}
	if !strings.Contains(searchKeys(60), "move") {
		t.Errorf("60-column search keys lost the move hint: %q", searchKeys(60))
	}
	s := renderState(t, 48, 18)
	s = s.enterSearch()
	if s.input.Width != searchInputWidth(48) {
		t.Errorf("enterSearch width = %d, want %d", s.input.Width, searchInputWidth(48))
	}
	lines := assertScreen(t, Render(s), 48, 18)
	footer := stripANSI(lines[len(lines)-1])
	for _, want := range []string{"Search:", "[Enter] accept", "[Esc] clear"} {
		if !strings.Contains(footer, want) {
			t.Errorf("48-column search bar missing %q: %q", want, footer)
		}
	}
}
