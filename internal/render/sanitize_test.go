package render

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "claude-code", "claude-code"},
		{"unicode", "café → ok", "café → ok"},
		{"newline and tab are replaced", "a\n\tb", "a?b"},
		{"control run is one replacement", "a\x00\x01\x02b", "a?b"},
		{"separate control runs", "a\x00b\tc", "a?b?c"},
		{"DEL", "a\x7fb", "a?b"},
		{"C1 control rune", "a\u0085b", "a?b"},
		{"CSI removed", "\x1b[31mred\x1b[0m", "red"},
		{"OSC removed", "\x1b]0;title\x07text", "text"},
		{"OSC terminated by ST", "\x1b]8;;https://x\x1b\\link", "link"},
		{"two-character escape removed", "a\x1bcb", "ab"},
		{"trailing escape", "a\x1b[", "a"},
		{"inside text", "a\x1b[1mb\x1b[0mc", "abc"},
		{"invalid utf-8", string([]byte{'a', 0xff, 'b'}), "a?b"},
		{"empty", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Sanitize(test.in); got != test.want {
				t.Errorf("Sanitize(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
