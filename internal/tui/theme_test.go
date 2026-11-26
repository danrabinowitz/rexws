package tui

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
)

// setRenderer pins both the profile and the background detection, matching how
// currentTheme resolves production colors.
func setRenderer(t *testing.T, profile termenv.Profile, dark bool) {
	t.Helper()
	previousProfile := lipgloss.ColorProfile()
	previousDark := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(profile)
	lipgloss.SetHasDarkBackground(dark)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDark)
	})
}

func TestTokenValuesMatchDesign(t *testing.T) {
	// Literal values from TUI-DESIGN.md §2.2; the renderer matrix below proves
	// they are selected for the right profile and background.
	wants := map[string]colorToken{
		"muted":         {ansiLight: "8", ansiDark: "8", c256Light: "240", c256Dark: "245", tcLight: "#475569", tcDark: "#94A3B8"},
		"accent":        {ansiLight: "4", ansiDark: "12", c256Light: "25", c256Dark: "111", tcLight: "#1D4ED8", tcDark: "#7AA2F7"},
		"selection.bg":  {ansiLight: "reverse", ansiDark: "reverse", c256Light: "254", c256Dark: "24", tcLight: "#DCE6FF", tcDark: "#2A3F5F"},
		"info":          {ansiLight: "6", ansiDark: "14", c256Light: "37", c256Dark: "81", tcLight: "#0E7490", tcDark: "#22D3EE"},
		"warning":       {ansiLight: "3", ansiDark: "11", c256Light: "130", c256Dark: "216", tcLight: "#9A3412", tcDark: "#FDBA74"},
		"error":         {ansiLight: "1", ansiDark: "9", c256Light: "160", c256Dark: "203", tcLight: "#B91C1C", tcDark: "#F87171"},
		"danger":        {ansiLight: "1", ansiDark: "9", c256Light: "124", c256Dark: "217", tcLight: "#991B1B", tcDark: "#FCA5A5"},
		"in_review":     {ansiLight: "5", ansiDark: "13", c256Light: "61", c256Dark: "141", tcLight: "#6D28D9", tcDark: "#C084FC"},
		"ready":         {ansiLight: "2", ansiDark: "10", c256Light: "28", c256Dark: "114", tcLight: "#15803D", tcDark: "#4ADE80"},
		"agent.error":   {ansiLight: "5", ansiDark: "13", c256Light: "125", c256Dark: "211", tcLight: "#BE123C", tcDark: "#FB7185"},
		"agent.unknown": {ansiLight: "3", ansiDark: "11", c256Light: "136", c256Dark: "179", tcLight: "#B45309", tcDark: "#FBBF24"},
	}
	got := map[string]colorToken{
		"muted": tokMuted, "accent": tokAccent, "selection.bg": tokSelectionBG,
		"info": tokInfo, "warning": tokWarning, "error": tokError,
		"danger": tokDanger, "in_review": tokInReview, "ready": tokReady,
		"agent.error": tokAgentError, "agent.unknown": tokAgentUnknown,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Errorf("%s = %#v, want %#v", name, got[name], want)
		}
	}
}

func TestTokenProfileMatrix(t *testing.T) {
	tokens := []struct {
		name  string
		token colorToken
	}{
		{"muted", tokMuted},
		{"accent", tokAccent},
		{"info", tokInfo},
		{"warning", tokWarning},
		{"error", tokError},
		{"danger", tokDanger},
		{"in_review", tokInReview},
		{"ready", tokReady},
		{"agent.error", tokAgentError},
		{"agent.unknown", tokAgentUnknown},
	}
	profiles := []struct {
		name    string
		profile termenv.Profile
	}{
		{"ansi", termenv.ANSI},
		{"ansi256", termenv.ANSI256},
		{"truecolor", termenv.TrueColor},
	}
	for _, p := range profiles {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-dark=%t", p.name, dark), func(t *testing.T) {
				setRenderer(t, p.profile, dark)
				theme := currentTheme()
				for _, item := range tokens {
					rendered := theme.style(item.token).Render("x")
					want := expectedForeground(t, p.profile, dark, item.token)
					if !strings.Contains(rendered, want) {
						t.Errorf("%s: rendered %q, want SGR %q", item.name, rendered, want)
					}
				}
			})
		}
	}

	// Ascii strips every escape, while the text survives.
	setRenderer(t, termenv.Ascii, false)
	rendered := currentTheme().muted().Render("x")
	if rendered != "x" {
		t.Errorf("Ascii render = %q, want x", rendered)
	}
}

func expectedForeground(t *testing.T, profile termenv.Profile, dark bool, token colorToken) string {
	t.Helper()
	switch profile {
	case termenv.ANSI:
		value := pick(dark, token.ansiLight, token.ansiDark)
		index, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("ANSI token is not an index: %q", value)
		}
		code := 30 + index
		if index >= 8 {
			code = 90 + index - 8
		}
		return fmt.Sprintf("\x1b[%dm", code)
	case termenv.ANSI256:
		value := pick(dark, token.c256Light, token.c256Dark)
		return "\x1b[38;5;" + value + "m"
	case termenv.TrueColor:
		// termenv converts hex through float32 RGB; use the same conversion so
		// the expected sequence matches the renderer exactly.
		color := termenv.RGBColor(pick(dark, token.tcLight, token.tcDark))
		return "\x1b[" + color.Sequence(false) + "m"
	}
	t.Fatalf("unexpected profile %v", profile)
	return ""
}

func TestSelectionProfileMatrix(t *testing.T) {
	// ANSI-16 uses reverse; 256/truecolor use the documented background.
	setRenderer(t, termenv.ANSI, false)
	if ansi := currentTheme().selection().Render("x"); !strings.Contains(ansi, "7m") && !strings.Contains(ansi, ";7;") {
		t.Errorf("ANSI selection is not reverse: %q", ansi)
	}

	setRenderer(t, termenv.ANSI256, false)
	if got := currentTheme().selection().Render("x"); !strings.Contains(got, "48;5;254") {
		t.Errorf("ANSI256 light selection = %q", got)
	}
	setRenderer(t, termenv.ANSI256, true)
	if got := currentTheme().selection().Render("x"); !strings.Contains(got, "48;5;24m") {
		t.Errorf("ANSI256 dark selection = %q", got)
	}

	setRenderer(t, termenv.TrueColor, false)
	if got := currentTheme().selection().Render("x"); !strings.Contains(got, "48;2;220;230;255") {
		t.Errorf("truecolor light selection = %q", got)
	}
	setRenderer(t, termenv.TrueColor, true)
	if got := currentTheme().selection().Render("x"); !strings.Contains(got, "48;2;42;63;95") {
		t.Errorf("truecolor dark selection = %q", got)
	}
}

func TestTruecolorSemanticContrast(t *testing.T) {
	tokens := []struct {
		name  string
		token colorToken
	}{
		{"accent", tokAccent},
		{"muted", tokMuted},
		{"warning", tokWarning},
		{"error", tokError},
		{"danger", tokDanger},
		{"in_review", tokInReview},
		{"ready", tokReady},
		{"info", tokInfo},
		{"agent.error", tokAgentError},
		{"agent.unknown", tokAgentUnknown},
	}
	for _, item := range tokens {
		t.Run(item.name, func(t *testing.T) {
			if light := contrastRatio(t, item.token.tcLight, "#FFFFFF"); light < 4.5 {
				t.Errorf("light contrast = %.2f, want >= 4.5", light)
			}
			if dark := contrastRatio(t, item.token.tcDark, "#1E1E1E"); dark < 4.5 {
				t.Errorf("dark contrast = %.2f, want >= 4.5", dark)
			}
		})
	}
	// Selection text: black on the light bar, white on the dark bar.
	if got := contrastRatio(t, "#000000", tokSelectionBG.tcLight); got < 4.5 {
		t.Errorf("black on light selection = %.2f", got)
	}
	if got := contrastRatio(t, "#FFFFFF", tokSelectionBG.tcDark); got < 4.5 {
		t.Errorf("white on dark selection = %.2f", got)
	}
}

func TestStatusColors(t *testing.T) {
	cases := map[config.Status]colorToken{
		config.StatusInProgress: tokAccent,
		config.StatusBlocked:    tokError,
		config.StatusInReview:   tokInReview,
		config.StatusReady:      tokReady,
		config.StatusDone:       tokMuted,
	}
	for id, want := range cases {
		if got := statusToken(id); got != want {
			t.Errorf("statusToken(%q) = %#v, want %#v", id, got, want)
		}
	}
	// #12 keeps custom IDs muted; #13 owns custom status colors.
	if got := statusToken("custom-stage"); got != tokMuted {
		t.Errorf("custom status = %#v, want muted", got)
	}
}

func TestAgentStateColors(t *testing.T) {
	cases := map[core.AgentState]colorToken{
		core.AgentBlocked: tokError,
		core.AgentError:   tokAgentError,
		core.AgentWorking: tokInfo,
		core.AgentDone:    tokReady,
		core.AgentStopped: tokMuted,
		core.AgentUnknown: tokAgentUnknown,
		core.AgentIdle:    tokText,
	}
	for state, want := range cases {
		if got := agentToken(state); got != want {
			t.Errorf("agentToken(%q) = %#v, want %#v", state, got, want)
		}
	}
}

func TestGlyphsASCIIFallback(t *testing.T) {
	t.Setenv("REXWS_ASCII", "1")
	g := currentGlyphs()
	if g.expanded != "v" || g.collapsed != ">" || g.context != ">" || g.child != "-" {
		t.Errorf("ASCII tree glyphs = %+v", g)
	}
	if g.group != "|" || g.rule != "-" || g.ellipsis != "~" || g.times != "x" || g.dot != "-" || g.empty != "-" {
		t.Errorf("ASCII structural glyphs = %+v", g)
	}
	if g.paused != "||" || g.both != "!|" || g.ambiguous != "!" {
		t.Errorf("ASCII markers = %+v", g)
	}
	if g.arrowUp != "^" || g.arrowDown != "v" || g.arrowLeft != "<" || g.arrowRight != ">" {
		t.Errorf("ASCII arrows = %+v", g)
	}
	if g.boxTopLeft != "+" || g.boxVertical != "|" || g.boxHorizontal != "-" {
		t.Errorf("ASCII box glyphs = %+v", g)
	}

	t.Setenv("REXWS_ASCII", "0")
	g = currentGlyphs()
	if g.expanded != "▾" || g.collapsed != "▸" || g.paused != "‖" || g.both != "!‖" {
		t.Errorf("Unicode glyphs = %+v", g)
	}
	if g.arrowUp != "↑" || g.arrowRight != "→" {
		t.Errorf("Unicode arrows = %+v", g)
	}
}

func TestBadgeTextOnlyConvertsGeneratedMultiplier(t *testing.T) {
	ascii := newTheme(termenv.Ascii, false)
	ascii.glyphs = asciiGlyphs()
	if got := ascii.badgeText("blocked (permission) ×2, working ×1"); got != "blocked (permission) x2, working x1" {
		t.Errorf("badgeText = %q", got)
	}
	if got := ascii.badgeText("blocked (a…b) ×1"); got != "blocked (a…b) x1" {
		t.Errorf("user ellipsis was converted: %q", got)
	}
	if got := ascii.badgeText("working (ratio ×2 units)"); got != "working (ratio ×2 units)" {
		t.Errorf("user multiplier-like detail was converted: %q", got)
	}
	unicode := newTheme(termenv.Ascii, false)
	unicode.glyphs = unicodeGlyphs()
	if got := unicode.badgeText("blocked ×2"); got != "blocked ×2" {
		t.Errorf("Unicode badgeText = %q", got)
	}
}

func TestGlyphsLocaleDetection(t *testing.T) {
	unsetEnv(t, "REXWS_ASCII")
	unsetEnv(t, "LC_ALL")
	unsetEnv(t, "LC_CTYPE")

	t.Setenv("LANG", "C")
	if !asciiMode() {
		t.Error("LANG=C should select the ASCII set")
	}

	t.Setenv("LANG", "en_US.UTF-8")
	if asciiMode() {
		t.Error("a UTF-8 locale should select the Unicode set")
	}

	t.Setenv("LC_ALL", "POSIX")
	if !asciiMode() {
		t.Error("LC_ALL should take precedence over LANG")
	}
}

func TestWorkstreamHighestStateMatchesBadgeOrder(t *testing.T) {
	// core.Summarize skips unknown when a known state exists, but the badge
	// text sorts all agent states including unknown. The color must match the
	// first label.
	ws := core.Workstream{
		AgentState: core.AgentDone,
		Agents: []core.Agent{
			{State: core.AgentDone},
			{State: core.AgentUnknown},
		},
	}
	if got := workstreamHighestState(ws); got != core.AgentUnknown {
		t.Errorf("mixed [done, unknown] state = %q, want unknown", got)
	}

	stopped := core.Workstream{AgentState: core.AgentStopped}
	if got := workstreamHighestState(stopped); got != core.AgentStopped {
		t.Errorf("stopped state = %q", got)
	}
	idle := core.Workstream{AgentState: core.AgentIdle}
	if got := workstreamHighestState(idle); got != core.AgentIdle {
		t.Errorf("idle state = %q", got)
	}
}

func TestWindowHighestState(t *testing.T) {
	ws := sampleWorkstreams()[0]
	if got := windowHighestState(ws, "window:agent"); got != core.AgentWorking {
		t.Errorf("agent window state = %q", got)
	}
	if got := windowHighestState(ws, "window:tests"); got != core.AgentBlocked {
		t.Errorf("tests window state = %q", got)
	}
	if got := windowHighestState(ws, "window:missing"); got != "" {
		t.Errorf("missing window state = %q", got)
	}
}

// unsetEnv removes a variable for the duration of a test and restores it.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func contrastRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	fgLum := relativeLuminance(t, fg)
	bgLum := relativeLuminance(t, bg)
	if fgLum < bgLum {
		fgLum, bgLum = bgLum, fgLum
	}
	return (fgLum + 0.05) / (bgLum + 0.05)
}

func relativeLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	r, g, b := hexRGB(t, hex)
	channel := func(value int) float64 {
		f := float64(value) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func hexRGB(t *testing.T, hex string) (int, int, int) {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("not a #RRGGBB color: %q", hex)
	}
	return hexValue(t, hex[1:3]), hexValue(t, hex[3:5]), hexValue(t, hex[5:7])
}

func hexValue(t *testing.T, value string) int {
	t.Helper()
	result := 0
	for _, r := range value {
		result *= 16
		switch {
		case r >= '0' && r <= '9':
			result += int(r - '0')
		case r >= 'a' && r <= 'f':
			result += int(r-'a') + 10
		case r >= 'A' && r <= 'F':
			result += int(r-'A') + 10
		default:
			t.Fatalf("invalid hex digit %q", r)
		}
	}
	return result
}
