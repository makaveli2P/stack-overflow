package shot

import (
	"image"
	"math"
)

// Line widths of box drawing, in pixels. Light lines are 2 px wide and
// heavy lines 4 px, centred on the cell, so they meet from cell to cell.
const (
	lightW  = 2
	heavyW  = 4
	dblGap  = 3 // offset of each line of a double line from the centre
	shadeFx = 6 // size of the shade pattern tile
)

// boxArms holds the arms of U+2500-U+257F as four digits: up, right,
// down, left. 0 is no arm, 1 light, 2 heavy, 3 double. "" marks a rune
// with its own code (dashes, arcs, diagonals).
var boxArms = [128]string{
	"0101", "0202", "1010", "2020", "", "", "", "", "", "", "", "", "0110", "0210", "0120", "0220",
	"0011", "0012", "0021", "0022", "1100", "1200", "2100", "2200", "1001", "1002", "2001", "2002", "1110", "1210", "2110", "1120",
	"2120", "2210", "1220", "2220", "1011", "1012", "2011", "1021", "2021", "2012", "1022", "2022", "0111", "0112", "0211", "0212",
	"0121", "0122", "0221", "0222", "1101", "1102", "1201", "1202", "2101", "2102", "2201", "2202", "1111", "1112", "1211", "1212",
	"2111", "1121", "2121", "2112", "2211", "1122", "1221", "2212", "1222", "2122", "2221", "2222", "", "", "", "",
	"0303", "3030", "0310", "0130", "0330", "0013", "0031", "0033", "1300", "3100", "3300", "1003", "3001", "3003", "1310", "3130",
	"3330", "1013", "3031", "3033", "0313", "0131", "0333", "1303", "3101", "3303", "1313", "3131", "3333", "", "", "",
	"", "", "", "", "0001", "1000", "0100", "0010", "0002", "2000", "0200", "0020", "0201", "1020", "0102", "2010",
}

// dashes holds the dashed lines: rune → (dash count, heavy, vertical).
var dashes = map[rune]struct {
	n           int
	heavy, vert bool
}{
	'┄': {3, false, false}, '┅': {3, true, false}, '┆': {3, false, true}, '┇': {3, true, true},
	'┈': {4, false, false}, '┉': {4, true, false}, '┊': {4, false, true}, '┋': {4, true, true},
	'╌': {2, false, false}, '╍': {2, true, false}, '╎': {2, false, true}, '╏': {2, true, true},
}

// boxMask draws box drawing and block element runes. It returns nil for
// other runes. w is the cell width in pixels.
func boxMask(r rune, w int) *mask {
	switch {
	case r >= 0x2500 && r <= 0x257f:
	case r >= 0x2580 && r <= 0x259f:
		return blockMask(r, w)
	default:
		return nil
	}
	a := image.NewAlpha(image.Rect(0, 0, w, cellH))
	m := &mask{a: a}
	if d, ok := dashes[r]; ok {
		drawDashes(a, d.n, d.heavy, d.vert)
		return m
	}
	switch r {
	case '╭', '╮', '╯', '╰':
		drawArc(a, r)
		return m
	case '╱', '╲', '╳':
		var c canvas
		wf, hf := float64(w), float64(cellH)
		if r != '╲' {
			c.line(wf, 0, 0, hf, lightW*0.75)
		}
		if r != '╱' {
			c.line(0, 0, wf, hf, lightW*0.75)
		}
		c.paint(a, 0, 0)
		return m
	}
	arms := boxArms[r-0x2500]
	var k [4]int // up, right, down, left
	for i := range k {
		k[i] = int(arms[i] - '0')
	}
	drawArms(a, k)
	return m
}

// fill sets the rectangle [x0,x1) x [y0,y1) of a to full ink.
func fill(a *image.Alpha, x0, y0, x1, y1 int) {
	r := image.Rect(x0, y0, x1, y1).Intersect(a.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := a.Pix[y*a.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			row[x] = 0xff
		}
	}
}

func lineW(k int) int {
	if k == 2 {
		return heavyW
	}
	return lightW
}

// drawArms draws the arms k (up, right, down, left) of a box drawing rune.
func drawArms(a *image.Alpha, k [4]int) {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	cx, cy := w/2, h/2
	const up, right, down, left = 0, 1, 2, 3
	// half is the half width of the widest single line on an axis, so
	// that a corner fills in.
	half := func(arms ...int) int {
		m := 0
		for _, i := range arms {
			switch {
			case k[i] == 3:
				m = max(m, dblGap+lightW/2)
			case k[i] > 0:
				m = max(m, lineW(k[i])/2)
			}
		}
		return m
	}
	isDbl := func(i int) bool { return k[i] == 3 }

	// Horizontal arms.
	for _, side := range []int{right, left} {
		kk := k[side]
		if kk == 0 {
			continue
		}
		edge := func(from int) (int, int) { // the span from the centre to the edge
			if side == right {
				return cx - from, w
			}
			return 0, cx + from
		}
		if kk != 3 {
			t := lineW(kk)
			ext := half(up, down)
			if isDbl(up) || isDbl(down) {
				ext = meetDouble(k[right] != 0 && k[left] != 0)
			}
			x0, x1 := edge(ext)
			fill(a, x0, cy-t/2, x1, cy+t/2+t%2)
			continue
		}
		for _, s := range []int{-1, 1} {
			perp, other := up, down
			if s > 0 {
				perp, other = down, up
			}
			var from int
			switch {
			case isDbl(perp):
				from = -(dblGap - lightW/2) // stop at the inner line
			case isDbl(other):
				from = dblGap + lightW/2 // go to the outer corner
			default:
				from = half(up, down)
			}
			x0, x1 := edge(from)
			y := cy + s*dblGap
			fill(a, x0, y-lightW/2, x1, y+lightW/2)
		}
	}
	// Vertical arms: the same with the axes swapped.
	for _, side := range []int{down, up} {
		kk := k[side]
		if kk == 0 {
			continue
		}
		edge := func(from int) (int, int) {
			if side == down {
				return cy - from, h
			}
			return 0, cy + from
		}
		if kk != 3 {
			t := lineW(kk)
			ext := half(left, right)
			if isDbl(left) || isDbl(right) {
				ext = meetDouble(k[up] != 0 && k[down] != 0)
			}
			y0, y1 := edge(ext)
			fill(a, cx-t/2, y0, cx+t/2+t%2, y1)
			continue
		}
		for _, s := range []int{-1, 1} {
			perp, other := left, right
			if s > 0 {
				perp, other = right, left
			}
			var from int
			switch {
			case isDbl(perp):
				from = -(dblGap - lightW/2)
			case isDbl(other):
				from = dblGap + lightW/2
			default:
				from = half(left, right)
			}
			y0, y1 := edge(from)
			x := cx + s*dblGap
			fill(a, x-lightW/2, y0, x+lightW/2, y1)
		}
	}
}

// meetDouble returns how far a single arm goes past the centre when it
// meets a double line. A line that crosses goes through both lines; an
// arm that ends stops at the near line.
func meetDouble(crosses bool) int {
	if crosses {
		return dblGap + lightW/2
	}
	return -(dblGap - lightW/2)
}

// drawDashes draws a dashed line with n dashes in the cell. The pattern
// is the same in every cell, so the dashes repeat evenly.
func drawDashes(a *image.Alpha, n int, heavy, vert bool) {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	t := lightW
	if heavy {
		t = heavyW
	}
	length := w
	if vert {
		length = h
	}
	part := float64(length) / float64(n)
	gap := math.Max(2, part/3)
	for i := 0; i < n; i++ {
		s := int(math.Round(float64(i)*part + gap/2))
		e := int(math.Round(float64(i+1)*part - gap/2))
		if vert {
			fill(a, w/2-t/2, s, w/2+t/2, e)
		} else {
			fill(a, s, h/2-t/2, e, h/2+t/2)
		}
	}
}

// drawArc draws a rounded corner: a quarter circle that joins a light
// vertical line at the cell centre with a light horizontal line.
func drawArc(a *image.Alpha, r rune) {
	w, h := float64(a.Rect.Dx()), float64(a.Rect.Dy())
	cx, cy := w/2, h/2
	rad := cx
	// The arc for ╭; the others mirror it.
	var pts [][2]float64
	pts = append(pts, [2]float64{cx, h + 1}, [2]float64{cx, cy + rad})
	const steps = 24
	for i := 1; i <= steps; i++ {
		t := math.Pi / 2 * float64(i) / steps
		pts = append(pts, [2]float64{cx + rad - rad*math.Cos(t), cy + rad - rad*math.Sin(t)})
	}
	pts = append(pts, [2]float64{w + 1, cy})
	var c canvas
	for i := range pts {
		p := &pts[i]
		if r == '╮' || r == '╯' {
			p[0] = w - p[0]
		}
		if r == '╯' || r == '╰' {
			p[1] = h - p[1]
		}
	}
	for i := 1; i < len(pts); i++ {
		c.line(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1], lightW)
	}
	c.paint(a, 0, 0)
}

// blockMask draws the block elements U+2580-U+259F.
func blockMask(r rune, w int) *mask {
	a := image.NewAlpha(image.Rect(0, 0, w, cellH))
	h := cellH
	ex := func(n int) int { return int(math.Round(float64(w*n) / 8)) } // eighths of the width
	ey := func(n int) int { return int(math.Round(float64(h*n) / 8)) } // eighths of the height
	cx, cy := w/2, h/2
	switch {
	case r == '▀':
		fill(a, 0, 0, w, cy)
	case r >= '▁' && r <= '█': // lower eighths
		fill(a, 0, h-ey(int(r-'▁')+1), w, h)
	case r >= '▉' && r <= '▏': // left eighths, 7/8 down to 1/8
		fill(a, 0, 0, ex(int('▏'-r)+1), h)
	case r == '▐':
		fill(a, cx, 0, w, h)
	case r == '░' || r == '▒' || r == '▓':
		shade(a, r)
	case r == '▔':
		fill(a, 0, 0, w, ey(1))
	case r == '▕':
		fill(a, w-ex(1), 0, w, h)
	default:
		// Quadrants: bits are upper left, upper right, lower left, lower right.
		q := map[rune]int{'▖': 4, '▗': 8, '▘': 1, '▙': 1 | 4 | 8, '▚': 1 | 8, '▛': 1 | 2 | 4, '▜': 1 | 2 | 8, '▝': 2, '▞': 2 | 4, '▟': 2 | 4 | 8}[r]
		if q&1 != 0 {
			fill(a, 0, 0, cx, cy)
		}
		if q&2 != 0 {
			fill(a, cx, 0, w, cy)
		}
		if q&4 != 0 {
			fill(a, 0, cy, cx, h)
		}
		if q&8 != 0 {
			fill(a, cx, cy, w, h)
		}
	}
	return &mask{a: a}
}

// shade draws a light, medium or dark shade as a pattern of square dots.
// The tile divides the cell size, so the pattern runs on across cells.
func shade(a *image.Alpha, r rune) {
	const d = shadeFx / 2
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			tx, ty := x%shadeFx/d, y%(2*shadeFx)/d // tx 0..1, ty 0..3
			var on bool
			switch r {
			case '░':
				on = (tx == 0 && ty == 0) || (tx == 1 && ty == 2)
			case '▒':
				on = (tx+ty)%2 == 0
			case '▓':
				on = !((tx == 0 && ty == 0) || (tx == 1 && ty == 2))
			}
			if on {
				a.Pix[y*a.Stride+x] = 0xff
			}
		}
	}
}
