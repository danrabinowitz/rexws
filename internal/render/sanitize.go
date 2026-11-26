package render

import (
	"strings"
	"unicode/utf8"
)

// Sanitize makes an externally supplied display string safe to measure and
// render in a terminal. ESC sequences are removed, invalid UTF-8 bytes become
// "?", and each run of C0/C1 control characters (including DEL) becomes a
// single "?". Rows therefore never contain a newline or tab, and a Rex-derived
// escape cannot reach the terminal or corrupt lipgloss.Width.
func Sanitize(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inControls := false
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			writeReplacement(&b, &inControls)
			i++
			continue
		}
		if r == 0x1b { // ESC: drop the whole escape sequence
			i = skipEscapeSequence(value, i+size)
			inControls = false
			continue
		}
		if isControl(r) {
			writeReplacement(&b, &inControls)
			i += size
			continue
		}
		inControls = false
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func writeReplacement(b *strings.Builder, inControls *bool) {
	if *inControls {
		return
	}
	b.WriteByte('?')
	*inControls = true
}

func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// skipEscapeSequence returns the index just past the escape sequence that
// starts at i, which is the byte after ESC.
func skipEscapeSequence(value string, i int) int {
	if i >= len(value) {
		return i
	}
	switch value[i] {
	case '[': // CSI: parameters and intermediates end at a final byte
		for j := i + 1; j < len(value); j++ {
			if value[j] >= 0x40 && value[j] <= 0x7e {
				return j + 1
			}
		}
		return len(value)
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC strings end at BEL or ST
		for j := i + 1; j < len(value); j++ {
			switch value[j] {
			case 0x07: // BEL
				return j + 1
			case 0x1b: // ST is ESC \; another ESC starts a new sequence
				if j+1 < len(value) && value[j+1] == '\\' {
					return j + 2
				}
				return j
			}
		}
		return len(value)
	default:
		// Two-character escapes such as ESC c or ESC 7.
		_, size := utf8.DecodeRuneInString(value[i:])
		return i + size
	}
}
