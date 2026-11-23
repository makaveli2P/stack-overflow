package shot

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Cell attributes.
const (
	attrBold uint8 = 1 << iota
	attrFaint
	attrItalic
	attrUnderline
	attrReverse
	attrStrike
	attrHidden
)

// style is the SGR state of the parser.
type style struct {
	fg, bg colour
	attr   uint8
}

// cell is one terminal cell. A wide rune sits in its first cell with
// width 2; the next cell has width 0. A cell holds a whole grapheme
// cluster: r is its first rune, and ext has the runes after r (combining
// marks, joiners and variation selectors). ext is "" for most cells.
type cell struct {
	r     rune // 0 or ' ' for an empty cell
	ext   string
	width uint8
	st    style
}

// grid is the parsed screen: w x h cells, row by row.
type grid struct {
	w, h  int
	cells []cell
}

func (g *grid) at(x, y int) *cell { return &g.cells[y*g.w+x] }

// parseView splits the view into cells. It reads SGR codes and ignores
// all other escape codes. Lines and columns past the size are cut.
//
// A combining mark joins the cell of the rune before it, so it is never
// lost: x/ansi gives a mark after an ASCII rune as its own sequence of
// width 0. A mark with no rune before it on its line gets a cell of its
// own, on a space.
func parseView(view string, w, h int) *grid {
	g := &grid{w: w, h: h, cells: make([]cell, w*h)}
	for i := range g.cells {
		g.cells[i] = cell{r: ' ', width: 1}
	}
	var st style
	p := ansi.NewParser()
	lines := strings.Split(view, "\n")
	for y, line := range lines {
		if y >= h {
			break
		}
		x := 0
		last := -1 // the x of the last cell that got a rune on this line
		var state byte
		for len(line) > 0 {
			seq, width, n, newState := ansi.DecodeSequence(line, state, p)
			state = newState
			line = line[n:]
			if n == 0 {
				break
			}
			if width == 0 {
				switch {
				case ansi.HasCsiPrefix(seq) && isSGR(p):
					st = applySGR(st, p.Params())
				case seq == "\t":
					x = min((x/8+1)*8, w)
					last = x - 1
				case isText(seq):
					if last >= 0 {
						g.at(last, y).ext += seq
					} else if x < w && visible(seq) {
						*g.at(x, y) = cell{r: ' ', ext: seq, width: 1, st: st}
						last = x
						x++
					}
				}
				continue
			}
			if x >= w {
				last = -1 // cut: its marks are cut too
				continue  // keep reading SGR codes past the edge
			}
			r, size := utf8.DecodeRuneInString(seq)
			*g.at(x, y) = cell{r: r, ext: seq[size:], width: uint8(width), st: st}
			if width == 2 && x+1 < w {
				*g.at(x+1, y) = cell{r: 0, width: 0, st: st}
			}
			last = x
			x += width
		}
	}
	return g
}

// isText reports whether seq, a sequence of width 0, is text (a mark or
// a joiner) and not a control or an escape code.
func isText(seq string) bool {
	r, size := utf8.DecodeRuneInString(seq)
	if r == utf8.RuneError && size <= 1 {
		return false
	}
	return r >= 0x20 && r != 0x7f && (r < 0x80 || r >= 0xa0)
}

// visible reports whether seq has a rune that is not ignorable.
func visible(seq string) bool {
	for _, r := range seq {
		if !ignorable(r) {
			return true
		}
	}
	return false
}

// isSGR reports whether the last CSI sequence was SGR ("CSI ... m").
func isSGR(p *ansi.Parser) bool {
	c := ansi.Cmd(p.Command())
	return c.Final() == 'm' && c.Prefix() == 0 && c.Intermediate() == 0
}

// applySGR applies one SGR sequence to st.
func applySGR(st style, ps ansi.Params) style {
	if len(ps) == 0 {
		return style{}
	}
	for i := 0; i < len(ps); i++ {
		n := ps[i].Param(0)
		switch {
		case n == 0:
			st = style{}
		case n == 1:
			st.attr |= attrBold
		case n == 2:
			st.attr |= attrFaint
		case n == 3:
			st.attr |= attrItalic
		case n == 4:
			on := true
			if ps[i].HasMore() && i+1 < len(ps) {
				i++
				on = ps[i].Param(1) != 0
			}
			st.attr = setAttr(st.attr, attrUnderline, on)
		case n == 7:
			st.attr |= attrReverse
		case n == 8:
			st.attr |= attrHidden
		case n == 9:
			st.attr |= attrStrike
		case n == 21:
			st.attr |= attrUnderline
		case n == 22:
			st.attr &^= attrBold | attrFaint
		case n == 23:
			st.attr &^= attrItalic
		case n == 24:
			st.attr &^= attrUnderline
		case n == 27:
			st.attr &^= attrReverse
		case n == 28:
			st.attr &^= attrHidden
		case n == 29:
			st.attr &^= attrStrike
		case n >= 30 && n <= 37:
			st.fg = colour{kind: colIndex, idx: uint8(n - 30)}
		case n == 39:
			st.fg = colour{}
		case n >= 40 && n <= 47:
			st.bg = colour{kind: colIndex, idx: uint8(n - 40)}
		case n == 49:
			st.bg = colour{}
		case n >= 90 && n <= 97:
			st.fg = colour{kind: colIndex, idx: uint8(n - 90 + 8)}
		case n >= 100 && n <= 107:
			st.bg = colour{kind: colIndex, idx: uint8(n - 100 + 8)}
		case n == 38 || n == 48 || n == 58:
			var c colour
			var ok bool
			c, i, ok = extColour(ps, i)
			if !ok {
				return st // a broken colour: ignore the rest, like xterm
			}
			switch n {
			case 38:
				st.fg = c
			case 48:
				st.bg = c
			}
		default:
			// Skip the sub-parameters of a code that we do not know.
			for ps[i].HasMore() && i+1 < len(ps) {
				i++
			}
		}
	}
	return st
}

func setAttr(a, bit uint8, on bool) uint8 {
	if on {
		return a | bit
	}
	return a &^ bit
}

// extColour reads an extended colour (38, 48 or 58) that starts at ps[i].
// It reads both forms: "38;5;n", "38;2;r;g;b" and "38:5:n",
// "38:2::r:g:b", "38:2:r:g:b". It returns the index of the last
// parameter that it used.
func extColour(ps ansi.Params, i int) (colour, int, bool) {
	var vals []int
	if ps[i].HasMore() {
		// Colon form: the group runs until a parameter without HasMore.
		for j := i + 1; j < len(ps); j++ {
			vals = append(vals, ps[j].Param(-1))
			i = j
			if !ps[j].HasMore() {
				break
			}
		}
		if len(vals) >= 5 && vals[0] == 2 {
			vals = append(vals[:1], vals[2:]...) // drop the colour space id
		}
	} else {
		if i+1 >= len(ps) {
			return colour{}, i, false
		}
		mode := ps[i+1].Param(-1)
		need := map[int]int{5: 1, 2: 3}[mode]
		if need == 0 || i+1+need >= len(ps) {
			return colour{}, len(ps), false
		}
		vals = append(vals, mode)
		for j := 0; j < need; j++ {
			vals = append(vals, ps[i+2+j].Param(0))
		}
		i += 1 + need
	}
	clamp := func(v int) uint8 { return uint8(max(0, min(v, 255))) }
	switch {
	case len(vals) >= 2 && vals[0] == 5:
		return colour{kind: colIndex, idx: clamp(vals[1])}, i, true
	case len(vals) >= 4 && vals[0] == 2:
		return colour{kind: colRGB, rgb: rgb{clamp(vals[1]), clamp(vals[2]), clamp(vals[3])}}, i, true
	}
	return colour{}, i, false
}
