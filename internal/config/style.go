package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// ColorAuto is the default status color. The TUI resolves it from the status
// ID: the built-in five have fixed roles and custom IDs get a deterministic
// hash color.
const ColorAuto = "auto"

// Decorative contrast thresholds. Configured colors only paint the group
// marker and icon, never body text, so WCAG's 3:1 non-text threshold applies.
const (
	lightBackground = "#FFFFFF"
	darkBackground  = "#1E1E1E"
	decorativeRatio = 3.0
)

// Named status colors are adaptive ANSI-16 colors: the base name uses the
// normal index on a light background and the bright index on a dark one;
// bright-<name> uses the bright index on both.
var namedStatusColorIndex = map[string]int{
	"black":   0,
	"red":     1,
	"green":   2,
	"yellow":  3,
	"blue":    4,
	"magenta": 5,
	"cyan":    6,
	"white":   7,
}

// ParseStatusColor validates a configured status color and returns its
// normalized form. The grammar is "auto", a named ANSI color, #RGB/#RRGGBB, or
// ansi256:0-255. An empty value is valid and means auto.
func ParseStatusColor(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "" || value == ColorAuto:
		return value, true
	case strings.HasPrefix(value, "#"):
		return parseHexColor(value)
	case strings.HasPrefix(value, "ansi256:"):
		return parseANSI256Color(value)
	default:
		if _, _, ok := StatusColorANSIIndices(value); ok {
			return value, true
		}
		return "", false
	}
}

// StatusColorANSIIndices returns the light and dark ANSI-16 indices for a named
// status color, such as blue (4/12) or bright-magenta (13/13).
func StatusColorANSIIndices(value string) (light, dark int, ok bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	base, bright := value, false
	if strings.HasPrefix(base, "bright-") {
		base = strings.TrimPrefix(base, "bright-")
		bright = true
	}
	index, found := namedStatusColorIndex[base]
	if !found {
		return 0, 0, false
	}
	if bright {
		return index + 8, index + 8, true
	}
	return index, index + 8, true
}

func parseHexColor(value string) (string, bool) {
	if len(value) != 4 && len(value) != 7 {
		return "", false
	}
	for i := 1; i < len(value); i++ {
		if !isHexDigit(value[i]) {
			return "", false
		}
	}
	return value, true
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}

func parseANSI256Color(value string) (string, bool) {
	digits := strings.TrimPrefix(value, "ansi256:")
	if digits == "" {
		return "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", false
		}
	}
	index, err := strconv.Atoi(digits)
	if err != nil || index > 255 {
		return "", false
	}
	return fmt.Sprintf("ansi256:%d", index), true
}

// ResolvedIcon returns the icon to render, or empty when it is invalid. The
// configured value is preserved in config.json; only the resolved display
// value is sanitized and trimmed.
func (d StatusDef) ResolvedIcon() string {
	icon, ok := sanitizeStatusIcon(d.Icon)
	if !ok {
		return ""
	}
	return icon
}

// sanitizeStatusIcon trims an icon and accepts exactly one grapheme cluster
// that occupies one or two display cells and contains no control characters.
func sanitizeStatusIcon(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if r == 0x1b || r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return "", false
		}
	}
	if uniseg.GraphemeClusterCount(value) != 1 {
		return "", false
	}
	if width := uniseg.StringWidth(value); width < 1 || width > 2 {
		return "", false
	}
	return value, true
}

// collectStyleWarnings validates the cosmetic status fields and records a
// visible warning for each problem. Invalid values are preserved verbatim:
// the TUI resolves them to auto / no icon at render time, and a later Save
// keeps the user's text so the warning repeats until they fix it.
func (c *Config) collectStyleWarnings() {
	seen := make(map[string]bool, len(c.Warnings))
	for _, warning := range c.Warnings {
		seen[warning] = true
	}
	warn := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		if seen[message] {
			return
		}
		seen[message] = true
		c.Warnings = append(c.Warnings, message)
	}

	for i := range c.Statuses {
		def := &c.Statuses[i]
		if strings.TrimSpace(def.Color) != "" {
			normalized, ok := ParseStatusColor(def.Color)
			if !ok {
				warn("status %q: invalid color %q; using auto", def.ID, def.Color)
			} else if message := lowContrastWarning(def.ID, normalized); message != "" {
				warn("%s", message)
			}
		}
		if strings.TrimSpace(def.Icon) != "" {
			if _, ok := sanitizeStatusIcon(def.Icon); !ok {
				warn("status %q: invalid icon %q; icon omitted", def.ID, def.Icon)
			}
		}
	}
}

// lowContrastWarning reports a fixed color that is hard to see on one of the
// two assumed backgrounds. Named colors are adaptive and stay in the
// terminal's own palette, so they are skipped.
func lowContrastWarning(id Status, normalized string) string {
	r, g, b, ok := fixedColorRGB(normalized)
	if !ok {
		return ""
	}
	var failed []string
	if contrastRatio(colorLuminance(r, g, b), hexLuminance(lightBackground)) < decorativeRatio {
		failed = append(failed, "light")
	}
	if contrastRatio(colorLuminance(r, g, b), hexLuminance(darkBackground)) < decorativeRatio {
		failed = append(failed, "dark")
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("status %q: color %q is below 3:1 contrast on a %s background; the color only decorates the group marker and icon",
		id, normalized, strings.Join(failed, " and "))
}

func fixedColorRGB(color string) (r, g, b int, ok bool) {
	if strings.HasPrefix(color, "#") {
		return hexRGB(color)
	}
	index, found := strings.CutPrefix(color, "ansi256:")
	if !found {
		return 0, 0, 0, false
	}
	n, err := strconv.Atoi(index)
	if err != nil || n < 16 || n > 255 {
		// ANSI 0-15 are terminal-theme dependent; do not guess.
		return 0, 0, 0, false
	}
	if n >= 232 {
		value := 8 + (n-232)*10
		return value, value, value, true
	}
	n -= 16
	level := func(v int) int {
		if v == 0 {
			return 0
		}
		return 55 + v*40
	}
	return level(n / 36), level((n % 36) / 6), level(n % 6), true
}

func hexRGB(color string) (r, g, b int, ok bool) {
	if len(color) != 4 && len(color) != 7 {
		return 0, 0, 0, false
	}
	digit := func(i int) int {
		v := color[i]
		if v >= '0' && v <= '9' {
			return int(v - '0')
		}
		return int(v-'a') + 10
	}
	if len(color) == 4 {
		return digit(1) * 17, digit(2) * 17, digit(3) * 17, true
	}
	return digit(1)*16 + digit(2), digit(3)*16 + digit(4), digit(5)*16 + digit(6), true
}

func hexLuminance(color string) float64 {
	r, g, b, ok := hexRGB(strings.ToLower(color))
	if !ok {
		return 0
	}
	return colorLuminance(r, g, b)
}

func colorLuminance(r, g, b int) float64 {
	channel := func(value int) float64 {
		f := float64(value) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func contrastRatio(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}
