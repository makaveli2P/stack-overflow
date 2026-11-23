package server

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

func TestCleanChat(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "hello table", "hello table"},
		{"trim", "  hi  ", "hi"},
		{"sgr colour", "\x1b[31mred\x1b[0m text", "red text"},
		{"csi with private params", "a\x1b[?25lb\x1b[2Jc", "abc"},
		{"cursor move", "x\x1b[10;20Hy", "xy"},
		{"csi cut by non-csi rune", "a\x1b[31éb", "aéb"},
		{"csi cut by control", "a\x1b[31\nb", "a b"},
		{"osc title bel", "\x1b]0;pwned\x07after", "after"},
		{"osc hyperlink st", "\x1b]8;;http://evil\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"osc 52 clipboard", "a\x1b]52;c;ZXZpbA==\x07b", "ab"},
		{"unterminated osc", "ok\x1b]0;title never ends", "ok"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"c1 csi", "a\u009b31mb", "ab"},
		{"c1 osc", "a\u009d0;t\u009cb", "ab"},
		{"two rune escape", "a\x1bcb", "ab"},
		{"charset escape", "a\x1b(Bb", "ab"},
		{"lone esc at end", "abc\x1b", "abc"},
		{"esc then esc", "a\x1b\x1b[1mb", "ab"},
		{"control chars", "a\x00b\x07c\x08d\x7fe", "abcde"},
		{"newlines become spaces", "line1\nline2\r\tx", "line1 line2  x"},
		{"bidi override", "abc‮def", "abcdef"},
		{"unicode kept", "pot 💰 für alle ♠", "pot 💰 für alle ♠"},
		{"only escapes", "\x1b[2J\x1b[H", ""},
		{"invalid utf8", "a\xffb", "a�b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanChat(tt.in); got != tt.want {
				t.Errorf("cleanChat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanChatLength(t *testing.T) {
	got := cleanChat(strings.Repeat("é", 500))
	if n := utf8.RuneCountInString(got); n != maxChatRunes {
		t.Errorf("rune count = %d, want %d", n, maxChatRunes)
	}
	// Escapes do not count toward the limit.
	in := strings.Repeat("\x1b[31m", 300) + strings.Repeat("x", 199)
	if got := cleanChat(in); got != strings.Repeat("x", 199) {
		t.Errorf("escapes counted toward the limit: got %d runes", utf8.RuneCountInString(got))
	}
}

func TestCleanName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"alice", "alice"},
		{"  bob  ", "bob"},
		{"", "player"},
		{"\x1b[31m\x00", "player"},
		{"averyveryverylongname", "averyveryver"},
		{"eleven char x", "eleven char"},
		{"\x1b[1mcarol\x1b[0m", "carol"},
	}
	for _, tt := range tests {
		if got := cleanName(tt.in); got != tt.want {
			t.Errorf("cleanName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	l := rateLimiter{limit: 5, window: 5 * time.Second}
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		if !l.allow(t0.Add(time.Duration(i) * time.Second)) {
			t.Fatalf("message %d refused", i)
		}
	}
	if l.allow(t0.Add(4 * time.Second)) {
		t.Fatal("sixth message in the window allowed")
	}
	if l.allow(t0.Add(4999 * time.Millisecond)) {
		t.Fatal("message before the first one expired allowed")
	}
	if !l.allow(t0.Add(5 * time.Second)) {
		t.Fatal("message after the first one expired refused")
	}
	if l.allow(t0.Add(5 * time.Second)) {
		t.Fatal("second message at 5s allowed, but the window is full again")
	}
	if !l.allow(t0.Add(20 * time.Second)) {
		t.Fatal("message after a long pause refused")
	}
}

// Chat and names never hold text that terminals draw at different widths
// (DESIGN.md "Card stability"). The TUI measures a line with grapheme
// widths (uniseg); an older terminal adds up the widths of the runes. When
// the two differ, a line can wrap on the client and leave old cells.
func TestCleanKeepsOneWidth(t *testing.T) {
	tests := []struct{ in, want string }{
		{"❤️", "❤"},                 // emoji variation selector
		{"I ❤️ poker", "I ❤ poker"}, // in a line
		{"👍🏽", "👍"},                 // skin tone
		{"1️⃣", "1"},                // keycap
		{"🇮🇳", ""},                  // flag: regional indicators
		{"go 🇮🇳 go", "go  go"},      // a flag in a line
		{"👨‍👩‍👧", "👨👩👧"},            // joined emoji become single emoji
		{"\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "\U0001F3F4"}, // tag flag
		{"मा", "मा"},         // Devanagari with a spacing vowel sign
		{"नमस्ते", "नमस्ते"}, // virama and vowel signs
		{"é", "é"},         // a combining accent
		{"日本", "日本"},
		{"☕ 🤫 😀", "☕ 🤫 😀"},
		{"ﾊﾞ", ""}, // a half-width sound mark that joins the letter
	}
	for _, tt := range tests {
		got := cleanChat(tt.in)
		if got != tt.want {
			t.Errorf("cleanChat(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if g, r := uniseg.StringWidth(got), runeCells(got); g != r {
			t.Errorf("cleanChat(%q) = %q: grapheme width %d, rune width %d", tt.in, got, g, r)
		}
	}
	if got := cleanName("❤️devon🇮🇳"); got != "❤devon" {
		t.Errorf("cleanName = %q, want %q", got, "❤devon")
	}
}
