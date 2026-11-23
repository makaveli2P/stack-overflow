package server

import (
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

const (
	maxChatRunes = 200
	maxNameRunes = 12
	defaultName  = "player"
)

// cleanChat makes text safe to draw: no escape sequences, no control
// characters, at most maxChatRunes runes.
func cleanChat(text string) string {
	return clean(text, maxChatRunes)
}

// cleanName makes a display name: printable, trimmed, 1..maxNameRunes runes.
func cleanName(name string) string {
	if name = clean(name, maxNameRunes); name == "" {
		return defaultName
	}
	return name
}

// clean removes terminal escape sequences and characters that are not
// printable, changes all white space to plain spaces, removes text that
// terminals draw at different widths (see sameWidth), trims the result and
// cuts it to maxRunes runes.
func clean(s string, maxRunes int) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == 0x1b:
			i = skipEscape(rs, i+1)
			continue
		case c == 0x9b: // C1 CSI
			i = skipCSI(rs, i+1)
			continue
		case isC1StringStart(c):
			i = skipString(rs, i+1)
			continue
		case unicode.IsSpace(c):
			b.WriteByte(' ')
		case widthRune(c):
			// Dropped: see widthRune.
		case unicode.IsPrint(c):
			b.WriteRune(c)
		}
		i++
	}
	out := []rune(strings.TrimSpace(sameWidth(b.String())))
	if len(out) > maxRunes {
		out = out[:maxRunes]
	}
	return strings.TrimRight(string(out), " ")
}

// widthRune reports whether c is a rune that changes the width of the
// character before it. Terminals do not agree on that width. When a
// terminal draws a line wider or narrower than the server measures it, old
// cells can stay on the screen, and the table can look wrong (DESIGN.md
// "Card stability"). Names and chat thus never hold these runes:
//
//   - U+FE0E and U+FE0F, the text and emoji variation selectors ("❤️").
//   - U+20E3, the keycap mark ("1️⃣").
//   - U+200D, the zero width joiner of emoji sequences ("👨‍👩‍👧").
//   - U+1F3FB to U+1F3FF, the skin tone modifiers ("👍🏽").
//   - U+1F1E6 to U+1F1FF, the regional indicators of flags ("🇮🇳").
//   - U+E0020 to U+E007F, the tag characters of subdivision flags.
func widthRune(c rune) bool {
	switch {
	case c == 0xFE0E, c == 0xFE0F, c == 0x20E3, c == 0x200D:
		return true
	case c >= 0x1F3FB && c <= 0x1F3FF:
		return true
	case c >= 0x1F1E6 && c <= 0x1F1FF:
		return true
	case c >= 0xE0020 && c <= 0xE007F:
		return true
	}
	return false
}

// cellRunes measures one rune as a terminal without grapheme support does
// (wcwidth): 0 for marks and format characters, 2 for wide characters, 1
// for the rest. Ambiguous characters count as 1.
var cellRunes = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// runeCells returns the width of s as the sum of the widths of its runes.
func runeCells(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		case r >= 0x1160 && r <= 0x11FF: // Hangul vowels and finals join the syllable.
		default:
			n += cellRunes.RuneWidth(r)
		}
	}
	return n
}

// sameWidth keeps each grapheme cluster of s that has the same width under
// two measures: the grapheme width that the TUI uses (uniseg, as lipgloss
// does), and the sum of its rune widths, as an older terminal counts it.
// It drops the other clusters. Accents and Indic vowel signs pass; a
// cluster that only some terminals draw as one wide character does not.
func sameWidth(s string) string {
	var b strings.Builder
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		if g.Width() == runeCells(g.Str()) {
			b.WriteString(g.Str())
		}
	}
	return b.String()
}

// isC1StringStart reports whether c is a C1 control that starts a string
// (DCS, SOS, OSC, PM or APC).
func isC1StringStart(c rune) bool {
	switch c {
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
		return true
	}
	return false
}

// skipEscape skips the rest of an escape sequence. i is the index after the
// ESC. It returns the index of the first rune after the sequence.
func skipEscape(rs []rune, i int) int {
	if i >= len(rs) {
		return i
	}
	switch c := rs[i]; {
	case c == '[':
		return skipCSI(rs, i+1)
	case c == ']', c == 'P', c == 'X', c == '^', c == '_':
		return skipString(rs, i+1)
	case c >= 0x20 && c <= 0x2f: // nF escape: intermediates, then one final rune.
		for i < len(rs) && rs[i] >= 0x20 && rs[i] <= 0x2f {
			i++
		}
		if i < len(rs) && rs[i] >= 0x30 && rs[i] <= 0x7e {
			i++
		}
		return i
	case c >= 0x30 && c <= 0x7e: // Two-rune escape, e.g. ESC c.
		return i + 1
	}
	return i // A lone ESC: drop only the ESC.
}

// skipCSI skips the parameters, intermediates and final rune of a control
// sequence. i is the index after "ESC [". A rune that cannot be part of the
// sequence ends it, and the caller then reads that rune normally.
func skipCSI(rs []rune, i int) int {
	for ; i < len(rs); i++ {
		c := rs[i]
		if c >= 0x40 && c <= 0x7e {
			return i + 1
		}
		if c < 0x20 || c > 0x3f {
			return i
		}
	}
	return i
}

// skipString skips an OSC, DCS, SOS, PM or APC string up to and including
// its terminator (BEL, ESC \ or the C1 ST). Without a terminator, it skips
// to the end.
func skipString(rs []rune, i int) int {
	for ; i < len(rs); i++ {
		switch rs[i] {
		case 0x07, 0x9c:
			return i + 1
		case 0x1b:
			if i+1 < len(rs) && rs[i+1] == '\\' {
				return i + 2
			}
		}
	}
	return i
}

// rateLimiter allows at most limit events in each window.
type rateLimiter struct {
	limit  int
	window time.Duration
	times  []time.Time
}

func (l *rateLimiter) allow(now time.Time) bool {
	n := 0
	for n < len(l.times) && now.Sub(l.times[n]) >= l.window {
		n++
	}
	l.times = l.times[n:]
	if len(l.times) >= l.limit {
		return false
	}
	l.times = append(l.times, now)
	return true
}
