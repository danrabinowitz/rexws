package tui

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
)

// colorToken is one semantic role with an explicit value for every profile
// and background (TUI-DESIGN.md §2.2). ANSI-16 is not a downgrade of the
// truecolor value: the table gives the intended index per profile.
type colorToken struct {
	ansiLight string
	ansiDark  string
	c256Light string
	c256Dark  string
	tcLight   string
	tcDark    string
}

func (t colorToken) zero() bool {
	return t.ansiLight == "" && t.ansiDark == "" &&
		t.c256Light == "" && t.c256Dark == "" &&
		t.tcLight == "" && t.tcDark == ""
}

var (
	tokText = colorToken{}

	tokMuted = colorToken{
		ansiLight: "8", ansiDark: "8",
		c256Light: "240", c256Dark: "245",
		tcLight: "#475569", tcDark: "#94A3B8",
	}
	tokAccent = colorToken{
		ansiLight: "4", ansiDark: "12",
		c256Light: "25", c256Dark: "111",
		tcLight: "#1D4ED8", tcDark: "#7AA2F7",
	}
	tokSelectionBG = colorToken{
		// ANSI-16 selection uses Reverse, handled by selectionStyle.
		ansiLight: "reverse", ansiDark: "reverse",
		c256Light: "254", c256Dark: "24",
		tcLight: "#DCE6FF", tcDark: "#2A3F5F",
	}
	tokInfo = colorToken{
		ansiLight: "6", ansiDark: "14",
		c256Light: "37", c256Dark: "81",
		tcLight: "#0E7490", tcDark: "#22D3EE",
	}
	tokWarning = colorToken{
		ansiLight: "3", ansiDark: "11",
		c256Light: "130", c256Dark: "216",
		tcLight: "#9A3412", tcDark: "#FDBA74",
	}
	tokError = colorToken{
		ansiLight: "1", ansiDark: "9",
		c256Light: "160", c256Dark: "203",
		tcLight: "#B91C1C", tcDark: "#F87171",
	}
	tokDanger = colorToken{
		ansiLight: "1", ansiDark: "9",
		c256Light: "124", c256Dark: "217",
		tcLight: "#991B1B", tcDark: "#FCA5A5",
	}
	tokInReview = colorToken{
		ansiLight: "5", ansiDark: "13",
		c256Light: "61", c256Dark: "141",
		tcLight: "#6D28D9", tcDark: "#C084FC",
	}
	tokReady = colorToken{
		ansiLight: "2", ansiDark: "10",
		c256Light: "28", c256Dark: "114",
		tcLight: "#15803D", tcDark: "#4ADE80",
	}
	tokAgentError = colorToken{
		ansiLight: "5", ansiDark: "13",
		c256Light: "125", c256Dark: "211",
		tcLight: "#BE123C", tcDark: "#FB7185",
	}
	tokAgentUnknown = colorToken{
		ansiLight: "3", ansiDark: "11",
		c256Light: "136", c256Dark: "179",
		tcLight: "#B45309", tcDark: "#FBBF24",
	}
)

// theme carries the glyph set and the resolved color context. Resolving at
// theme construction keeps one frame consistent even if the renderer profile
// changes later.
type theme struct {
	glyphs  glyphSet
	profile termenv.Profile
	dark    bool
}

func currentTheme() theme {
	return newTheme(lipgloss.ColorProfile(), lipgloss.HasDarkBackground())
}

func newTheme(profile termenv.Profile, dark bool) theme {
	return theme{glyphs: currentGlyphs(), profile: profile, dark: dark}
}

func pick(dark bool, light, darkValue string) string {
	if dark {
		return darkValue
	}
	return light
}

func (t theme) resolve(token colorToken) lipgloss.TerminalColor {
	value := ""
	switch t.profile {
	case termenv.TrueColor:
		value = pick(t.dark, token.tcLight, token.tcDark)
	case termenv.ANSI256:
		value = pick(t.dark, token.c256Light, token.c256Dark)
	case termenv.ANSI:
		value = pick(t.dark, token.ansiLight, token.ansiDark)
	default:
		return lipgloss.NoColor{}
	}
	if value == "" || value == "reverse" {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color(value)
}

func (t theme) style(token colorToken) lipgloss.Style {
	if token.zero() {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(t.resolve(token))
}

func (t theme) text() lipgloss.Style    { return lipgloss.NewStyle() }
func (t theme) muted() lipgloss.Style   { return t.style(tokMuted) }
func (t theme) faint() lipgloss.Style   { return lipgloss.NewStyle().Faint(true) }
func (t theme) accent() lipgloss.Style  { return t.style(tokAccent) }
func (t theme) info() lipgloss.Style    { return t.style(tokInfo) }
func (t theme) warning() lipgloss.Style { return t.style(tokWarning) }
func (t theme) error() lipgloss.Style   { return t.style(tokError) }

func (t theme) danger() lipgloss.Style {
	return t.style(tokDanger).Bold(true)
}

func (t theme) match() lipgloss.Style {
	return t.accent().Bold(true).Underline(true)
}

// selection suppresses semantic foreground: `>`, bold, and the bar carry
// selection, so hue is not the only cue and not a second one. ANSI-16 uses
// reverse because a 16-color profile has no reliable selection background.
func (t theme) selection() lipgloss.Style {
	if t.profile == termenv.ANSI {
		return lipgloss.NewStyle().Bold(true).Reverse(true)
	}
	return lipgloss.NewStyle().Bold(true).Background(t.resolve(tokSelectionBG))
}

// Status returns the group accent for a workflow status. For #12 an ID outside
// the five built-ins is muted; #13 owns configurable/custom status colors.
func (t theme) Status(id config.Status) lipgloss.Style {
	return t.style(statusToken(id))
}

func statusToken(id config.Status) colorToken {
	switch id {
	case config.StatusInProgress:
		return tokAccent
	case config.StatusBlocked:
		return tokError
	case config.StatusInReview:
		return tokInReview
	case config.StatusReady:
		return tokReady
	case config.StatusDone:
		return tokMuted
	default:
		return tokMuted
	}
}

// Agent returns the aggregate badge color. The badge text still lists every
// state, so color is additive.
func (t theme) Agent(state core.AgentState) lipgloss.Style {
	return t.style(agentToken(state))
}

func agentToken(state core.AgentState) colorToken {
	switch state {
	case core.AgentBlocked:
		return tokError
	case core.AgentError:
		return tokAgentError
	case core.AgentWorking:
		return tokInfo
	case core.AgentDone:
		return tokReady
	case core.AgentStopped:
		return tokMuted
	case core.AgentUnknown:
		return tokAgentUnknown
	default: // idle, or no state: base text
		return tokText
	}
}

// Notice returns the banner role: persistent warnings are warning-colored and
// carry a `!` prefix; transient action failures are error-colored and rely on
// their action wording.
func (t theme) Notice(persistent bool) lipgloss.Style {
	if persistent {
		return t.warning()
	}
	return t.error()
}

func (t theme) health(health string) lipgloss.Style {
	if strings.HasPrefix(health, "Rex unavailable") {
		return t.error()
	}
	return t.warning()
}

// badgeText applies the glyph fallback only to the generated `×N` multiplier;
// user-supplied detail and labels are left untouched.
func (t theme) badgeText(value string) string {
	if t.glyphs.times == "×" {
		return value
	}
	return badgeCountPattern.ReplaceAllStringFunc(value, func(match string) string {
		return " " + t.glyphs.times + strings.TrimPrefix(match, " ×")
	})
}

// The generated multiplier is always " ×N" followed by a group separator or
// the end of the badge, which keeps user detail that merely contains × intact.
var badgeCountPattern = regexp.MustCompile(` ×[0-9]+(,|$)`)

func (t theme) pendingText(value string) string {
	if t.glyphs.ellipsis == "…" {
		return value
	}
	return strings.ReplaceAll(value, "…", t.glyphs.ellipsis)
}

// glyphSet is the Unicode set and its ASCII fallback (TUI-DESIGN.md §7.3).
type glyphSet struct {
	expanded  string
	collapsed string
	context   string
	child     string
	group     string
	empty     string
	ellipsis  string
	times     string
	dot       string
	rule      string
	ambiguous string
	paused    string
	both      string

	arrowUp    string
	arrowDown  string
	arrowLeft  string
	arrowRight string

	boxTopLeft     string
	boxTopRight    string
	boxBottomLeft  string
	boxBottomRight string
	boxHorizontal  string
	boxVertical    string
}

func unicodeGlyphs() glyphSet {
	return glyphSet{
		expanded:  "▾",
		collapsed: "▸",
		context:   "↳",
		child:     "•",
		group:     "▌",
		empty:     "—",
		ellipsis:  "…",
		times:     "×",
		dot:       "·",
		rule:      "─",
		ambiguous: "!",
		paused:    "‖",
		both:      "!‖",

		arrowUp:    "↑",
		arrowDown:  "↓",
		arrowLeft:  "←",
		arrowRight: "→",

		boxTopLeft:     "╭",
		boxTopRight:    "╮",
		boxBottomLeft:  "╰",
		boxBottomRight: "╯",
		boxHorizontal:  "─",
		boxVertical:    "│",
	}
}

func asciiGlyphs() glyphSet {
	return glyphSet{
		expanded:  "v",
		collapsed: ">",
		context:   ">",
		child:     "-",
		group:     "|",
		empty:     "-",
		ellipsis:  "~",
		times:     "x",
		dot:       "-",
		rule:      "-",
		ambiguous: "!",
		paused:    "||",
		both:      "!|",

		arrowUp:    "^",
		arrowDown:  "v",
		arrowLeft:  "<",
		arrowRight: ">",

		boxTopLeft:     "+",
		boxTopRight:    "+",
		boxBottomLeft:  "+",
		boxBottomRight: "+",
		boxHorizontal:  "-",
		boxVertical:    "|",
	}
}

func currentGlyphs() glyphSet {
	if asciiMode() {
		return asciiGlyphs()
	}
	return unicodeGlyphs()
}

// asciiMode honors REXWS_ASCII first, then a non-UTF-8 locale. With no locale
// set, keep the current Unicode behavior.
func asciiMode() bool {
	if value, ok := os.LookupEnv("REXWS_ASCII"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "0", "false", "no", "off":
			return false
		default:
			return true
		}
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		folded := strings.ToLower(value)
		return !strings.Contains(folded, "utf-8") && !strings.Contains(folded, "utf8")
	}
	return false
}
