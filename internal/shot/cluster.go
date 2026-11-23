package shot

import (
	"image"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Go Mono has no combining marks (U+0300-U+036F). A name can still have
// them: for example "ọ̀" (U+1ECD U+0300) or "n̈" have no precomposed
// form. So the code draws a mark with the spacing form of the mark that
// Go Mono has ("`" for U+0300), and moves it over or under the base.

// markPos says where a combining mark goes.
type markPos uint8

const (
	markAbove markPos = iota // over the base, and over the marks before it
	markBelow                // under the base, and under the marks before it
	markFixed                // where the font puts the spacing form
)

// markSpec draws one combining mark with a spacing glyph of Go Mono.
type markSpec struct {
	glyph rune
	pos   markPos
}

// marks lists the combining marks that the code can draw. A cluster with
// another mark becomes an empty box.
var marks = map[rune]markSpec{
	0x0300: {'`', markAbove}, // grave
	0x0301: {'´', markAbove}, // acute
	0x0302: {'ˆ', markAbove}, // circumflex
	0x0303: {'˜', markAbove}, // tilde
	0x0304: {'¯', markAbove}, // macron
	0x0305: {'¯', markAbove}, // overline
	0x0306: {'˘', markAbove}, // breve
	0x0307: {'˙', markAbove}, // dot above
	0x0308: {'¨', markAbove}, // diaeresis
	0x030A: {'˚', markAbove}, // ring above
	0x030B: {'˝', markAbove}, // double acute
	0x030C: {'ˇ', markAbove}, // caron
	0x0340: {'`', markAbove}, // grave tone mark
	0x0341: {'´', markAbove}, // acute tone mark
	0x0323: {'˙', markBelow}, // dot below
	0x0324: {'¨', markBelow}, // diaeresis below
	0x0325: {'˚', markBelow}, // ring below
	0x0326: {',', markBelow}, // comma below
	0x032D: {'ˆ', markBelow}, // circumflex below
	0x032E: {'˘', markBelow}, // breve below
	0x0330: {'˜', markBelow}, // tilde below
	0x0331: {'¯', markBelow}, // macron below
	0x0327: {'¸', markFixed}, // cedilla
	0x0328: {'˛', markFixed}, // ogonek
	0x0332: {'_', markFixed}, // low line
	0x0335: {'-', markFixed}, // short stroke overlay
	0x0337: {'/', markFixed}, // short solidus overlay
	0x0338: {'/', markFixed}, // long solidus overlay
}

// The space in pixels between a mark and the ink next to it. These are
// the gaps of Go Mono: "é" has its accent 1 px over the "e", and "ạ" has
// its dot 2 px under the "a". So "e" + U+0301 draws as "é" does.
const (
	markGapAbove = 1
	markGapBelow = 2
)

// ignorable reports whether r has no ink of its own in a cluster: a
// format character (ZWJ, ZWNJ, a tag) or a variation selector.
func ignorable(r rune) bool {
	return unicode.Is(unicode.Cf, r) ||
		(r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

// clusterMask draws a cluster of several runes: r and then ext. It joins
// what NFC can join, for example "o" and U+0300 become "ò". Else it
// draws the base and each mark (see markedMask). A cluster with a part
// that nothing can draw becomes one empty box, so no rune is lost
// without a sign. The result is not cached. The caller holds maskMu.
func clusterMask(fs *fontSet, r rune, ext string, face, width int) *mask {
	rs := make([]rune, 0, 1+utf8.RuneCountInString(ext))
	for _, c := range string(r) + ext {
		if !ignorable(c) {
			rs = append(rs, c)
		}
	}
	if len(rs) == 0 {
		return nil
	}
	s := norm.NFC.String(string(rs))
	if c, n := utf8.DecodeRuneInString(s); n == len(s) {
		return runeMask(fs, c, face, width)
	}
	if m, ok := markedMask(fs, []rune(norm.NFD.String(s)), face, width); ok {
		return m
	}
	return emptyBox(cellW * width)
}

// markedMask draws rs[0] and the combining marks in rs[1:]. Marks over
// the base stack up, and marks under it stack down. It reports false
// when it cannot draw a part. The caller holds maskMu.
func markedMask(fs *fontSet, rs []rune, face, width int) (*mask, bool) {
	base, ms := rs[0], rs[1:]
	above := false
	for _, c := range ms {
		sp, ok := marks[c]
		if !ok {
			return nil, false
		}
		above = above || sp.pos == markAbove
	}
	// A mark over "i" or "j" takes the place of the dot.
	if above && (base == 'i' || base == 'j') {
		if dotless := map[rune]rune{'i': 'ı', 'j': 'ȷ'}[base]; fs.hasGlyph(faceRegular, dotless) {
			base = dotless
		}
	}
	bm := runeMask(fs, base, face, width)
	if bm != nil && bm.box {
		return nil, false
	}
	var parts []*mask
	// A blank base (a space) puts the marks around an "x".
	top, bottom := fs.xTop, fs.baseline
	if bm != nil {
		parts = append(parts, bm)
		top, bottom = bm.inkRows()
	}
	for _, c := range ms {
		sp := marks[c]
		mm := runeMask(fs, sp.glyph, face, width)
		if mm == nil || mm.box {
			return nil, false
		}
		t, b := mm.inkRows()
		dy := 0
		switch sp.pos {
		case markAbove:
			dy = top - markGapAbove - b
			top = t + dy
		case markBelow:
			dy = bottom + markGapBelow - t
			bottom = b + dy
		}
		parts = append(parts, &mask{a: mm.a, dx: mm.dx, dy: mm.dy + dy})
	}
	return mergeMasks(parts), true
}

// inkRows returns the first row with ink and the row after the last row
// with ink, from the top of the cell.
func (m *mask) inkRows() (top, bottom int) {
	a := m.a
	top, bottom = a.Rect.Dy(), 0
	for y := 0; y < a.Rect.Dy(); y++ {
		for _, v := range a.Pix[y*a.Stride : y*a.Stride+a.Rect.Dx()] {
			if v != 0 {
				top = min(top, y)
				bottom = y + 1
				break
			}
		}
	}
	if bottom == 0 {
		top = 0
	}
	return m.dy + top, m.dy + bottom
}

// mergeMasks puts the masks on top of each other. Each pixel keeps the
// most ink.
func mergeMasks(ms []*mask) *mask {
	if len(ms) == 0 {
		return nil
	}
	var r image.Rectangle
	for i, m := range ms {
		mr := m.a.Rect.Add(image.Pt(m.dx, m.dy))
		if i == 0 {
			r = mr
		} else {
			r = r.Union(mr)
		}
	}
	a := image.NewAlpha(image.Rect(0, 0, r.Dx(), r.Dy()))
	for _, m := range ms {
		ox, oy := m.dx-r.Min.X, m.dy-r.Min.Y
		for y := 0; y < m.a.Rect.Dy(); y++ {
			src := m.a.Pix[y*m.a.Stride : y*m.a.Stride+m.a.Rect.Dx()]
			dst := a.Pix[(y+oy)*a.Stride+ox:]
			for x, v := range src {
				dst[x] = max(dst[x], v)
			}
		}
	}
	return &mask{a: a, dx: r.Min.X, dy: r.Min.Y}
}
