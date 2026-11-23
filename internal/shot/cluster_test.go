package shot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func mustRender(t *testing.T, f Frame) []byte {
	t.Helper()
	b, err := Render(f)
	if err != nil {
		t.Fatalf("Render(%q): %v", f.View, err)
	}
	return b
}

// TestParseViewKeepsMarks checks that a combining mark stays in the cell
// of its base. x/ansi gives a mark after an ASCII rune as its own
// sequence of width 0, and a mark after another rune in the same
// sequence.
func TestParseViewKeepsMarks(t *testing.T) {
	type want struct {
		x   int
		r   rune
		ext string
	}
	for _, tc := range []struct {
		view  string
		w     int
		cells []want
	}{
		{"o\u0300x", 3, []want{{0, 'o', "\u0300"}, {1, 'x', ""}, {2, ' ', ""}}},
		{"ọ\u0300x", 3, []want{{0, 0x1ecd, "\u0300"}, {1, 'x', ""}}},
		{"n\u0308\u0301x", 3, []want{{0, 'n', "\u0308\u0301"}, {1, 'x', ""}}},
		// An SGR code between the base and the mark does not split them.
		{"e\x1b[31m\u0301x", 3, []want{{0, 'e', "\u0301"}, {1, 'x', ""}}},
		// After a wide rune, the mark goes to the first cell of the rune.
		{"界\u0301x", 4, []want{{0, '界', "\u0301"}, {1, 0, ""}, {2, 'x', ""}}},
		// A mark with no base takes a cell of its own, on a space.
		{"\u0300ab", 3, []want{{0, ' ', "\u0300"}, {1, 'a', ""}, {2, 'b', ""}}},
		// A lone joiner has no ink, so it takes no cell.
		{"\u200dab", 3, []want{{0, 'a', ""}, {1, 'b', ""}}},
		// A mark past the edge is cut with its base.
		{"ab\u0300", 1, []want{{0, 'a', ""}}},
	} {
		g := parseView(tc.view, tc.w, 1)
		for _, c := range tc.cells {
			got := g.at(c.x, 0)
			if got.r != c.r || got.ext != c.ext {
				t.Errorf("%q: cell %d = %q + %q, want %q + %q", tc.view, c.x, got.r, got.ext, c.r, c.ext)
			}
		}
	}
}

// TestMarksRender checks that a combining mark changes the image. Before
// the fix, "ò" (o and U+0300) gave the same PNG as "o".
func TestMarksRender(t *testing.T) {
	for _, pair := range [][2]string{
		{"o", "o\u0300"},
		{"ọ", "ọ\u0300"}, // Yoruba ọ\u0300, as in "Adébáyọ\u0300"; NFC has no single rune
		{"n", "n\u0308"}, // n\u0308; NFC has no single rune
		{"N", "N\u0308"},
		{"a", "a\u0300\u0301"},
		{" ", " \u0301"},
		{"a", "a\u0489"}, // a mark that the code cannot draw
	} {
		for _, dark := range []bool{true, false} {
			plain := mustRender(t, Frame{View: pair[0] + "x", Width: 3, Height: 1, Dark: dark})
			marked := mustRender(t, Frame{View: pair[1] + "x", Width: 3, Height: 1, Dark: dark})
			if bytes.Equal(plain, marked) {
				t.Errorf("%q renders the same as %q", pair[1], pair[0])
			}
			// The title keeps the marks too.
			plain = mustRender(t, Frame{View: "", Width: 40, Height: 1, Dark: dark, Title: "Stack Overflow — " + pair[0]})
			marked = mustRender(t, Frame{View: "", Width: 40, Height: 1, Dark: dark, Title: "Stack Overflow — " + pair[1]})
			if bytes.Equal(plain, marked) {
				t.Errorf("title %q renders the same as %q", pair[1], pair[0])
			}
		}
	}
	// What NFC can join draws as the single rune of the font.
	if a, b := mustRender(t, Frame{View: "o\u0300", Width: 2, Height: 1}), mustRender(t, Frame{View: "ò", Width: 2, Height: 1}); !bytes.Equal(a, b) {
		t.Error(`"o" + U+0300 does not render as "ò"`)
	}
}

// TestMarkMasks checks the masks: marks go over and under the base, a
// letter that Go Mono lacks is drawn from its parts, and a cluster with
// a part that nothing can draw is one empty box.
func TestMarkMasks(t *testing.T) {
	fs, err := loadFonts()
	if err != nil {
		t.Fatal(err)
	}
	for r, sp := range marks {
		if !fs.hasGlyph(faceRegular, sp.glyph) {
			t.Errorf("U+%04X: Go Mono has no %q", r, sp.glyph)
		}
	}
	for _, s := range []string{"ọ", "ế", "ệ", "ṣ"} { // ọ ế ệ ṣ
		if m := cellMask(fs, []rune(s)[0], "", faceRegular, 1); m == nil || m.box {
			t.Errorf("%s (U+%04X) is not drawn from its parts", s, []rune(s)[0])
		}
	}
	for _, ext := range []string{"\u0300", "\u0308", "\u0300\u0301"} {
		for _, base := range []rune{'n', 'N'} {
			bt, _ := cellMask(fs, base, "", faceRegular, 1).inkRows()
			mt, _ := cellMask(fs, base, ext, faceRegular, 1).inkRows()
			if mt >= bt-markGapAbove {
				t.Errorf("%c%+q: top of ink %d, base top %d: the mark is not over the base", base, ext, mt, bt)
			}
		}
	}
	_, bb := cellMask(fs, 'o', "", faceRegular, 1).inkRows()
	if _, mb := cellMask(fs, 'o', "\u0323", faceRegular, 1).inkRows(); mb <= bb+markGapBelow {
		t.Errorf("o + dot below: bottom of ink %d, base bottom %d", mb, bb)
	}
	for _, c := range []struct {
		r   rune
		ext string
	}{{'a', "\u0489"}, {'\U0001F44D', "\U0001F3FD"}, {'a', "\u0300\u0489"}} {
		if m := cellMask(fs, c.r, c.ext, faceRegular, 1); m == nil || !m.box {
			t.Errorf("%c%+q: want the empty box", c.r, c.ext)
		}
	}
	// Variation selectors and joiners have no ink of their own.
	if a, b := cellMask(fs, 'a', "\ufe0f", faceRegular, 1), cellMask(fs, 'a', "", faceRegular, 1); a != b {
		t.Error(`"a" + U+FE0F does not draw as "a"`)
	}
}

// TestMarksSheet draws names with marks. With SHOT_DIR set, it writes the
// PNG there.
func TestMarksSheet(t *testing.T) {
	view := "Adébáyọ\u0300 n\u0308 Ñ N\u0308 o\u0300 ò ọ ế Ệ ṣ ı\u0300\n" +
		"\x1b[1mAdébáyọ\u0300 n\u0308 N\u0308 ṣ\x1b[0m \x1b[3mn\u0308 ọ\u0300\x1b[0m a\u0489 👍🏽 ç c\u0327 ą a\u0328\n" +
		" \u0301 x\u0332 o\u0338 ñ n\u0303 Å A\u030a q\u0323\u0300"
	for _, dark := range []bool{true, false} {
		b := mustRender(t, Frame{View: view, Width: 40, Height: 3, Dark: dark, Title: "Stack Overflow — Adébáyọ\u0300 n\u0308"})
		if dir := os.Getenv("SHOT_DIR"); dir != "" {
			name := "marks-light.png"
			if dark {
				name = "marks-dark.png"
			}
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
