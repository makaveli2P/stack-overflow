package shot

import (
	"image"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// canvas collects simple shapes and paints them with smooth edges. Each
// pixel takes the coverage of the shape that covers it most. It is for
// small cell-sized masks, where speed does not matter.
type canvas struct {
	segs  []seg
	polys [][][2]float64
	discs [][3]float64 // x, y, radius
}

// seg is a line with round ends.
type seg struct{ x0, y0, x1, y1, half float64 }

func (c *canvas) line(x0, y0, x1, y1, width float64) {
	c.segs = append(c.segs, seg{x0, y0, x1, y1, width / 2})
}

// path draws lines through the points.
func (c *canvas) path(width float64, pts ...[2]float64) {
	for i := 1; i < len(pts); i++ {
		c.line(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1], width)
	}
}

// poly fills a polygon.
func (c *canvas) poly(pts ...[2]float64) { c.polys = append(c.polys, pts) }

func (c *canvas) disc(x, y, r float64) { c.discs = append(c.discs, [3]float64{x, y, r}) }

// arc draws a circular arc from angle a0 to a1 (radians; 0 points right,
// pi/2 points down).
func (c *canvas) arc(cx, cy, r, a0, a1, width float64) {
	const steps = 20
	var pts [][2]float64
	for i := 0; i <= steps; i++ {
		t := a0 + (a1-a0)*float64(i)/steps
		pts = append(pts, [2]float64{cx + r*math.Cos(t), cy + r*math.Sin(t)})
	}
	c.path(width, pts...)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// paint draws the shapes into a, with the shapes moved by (ox, oy). It
// keeps ink that is already in a.
func (c *canvas) paint(a *image.Alpha, ox, oy float64) {
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			px, py := float64(x)+0.5-ox, float64(y)+0.5-oy
			cov := 0.0
			for _, s := range c.segs {
				cov = math.Max(cov, clamp01(s.half+0.5-segDist(px, py, s)))
			}
			for _, d := range c.discs {
				cov = math.Max(cov, clamp01(d[2]+0.5-math.Hypot(px-d[0], py-d[1])))
			}
			for _, p := range c.polys {
				d := polyEdgeDist(px, py, p)
				if inPoly(px, py, p) {
					cov = math.Max(cov, clamp01(0.5+d))
				} else {
					cov = math.Max(cov, clamp01(0.5-d))
				}
			}
			i := y*a.Stride + x
			if v := uint8(math.Round(cov * 255)); v > a.Pix[i] {
				a.Pix[i] = v
			}
		}
	}
}

func segDist(px, py float64, s seg) float64 {
	dx, dy := s.x1-s.x0, s.y1-s.y0
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((px-s.x0)*dx+(py-s.y0)*dy)/l))
	}
	return math.Hypot(px-(s.x0+t*dx), py-(s.y0+t*dy))
}

func polyEdgeDist(px, py float64, p [][2]float64) float64 {
	d := math.Inf(1)
	for i := range p {
		j := (i + 1) % len(p)
		d = math.Min(d, segDist(px, py, seg{p[i][0], p[i][1], p[j][0], p[j][1], 0}))
	}
	return d
}

func inPoly(px, py float64, p [][2]float64) bool {
	in := false
	for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
		if (p[i][1] > py) != (p[j][1] > py) &&
			px < (p[j][0]-p[i][0])*(py-p[i][1])/(p[j][1]-p[i][1])+p[i][0] {
			in = !in
		}
	}
	return in
}

// symbolMask draws the symbols that the TUI uses and Go Mono does not
// have. It returns nil for other runes. The shapes use the font metrics:
// the baseline is at y 29 and the capitals start at about y 7.5 in a
// 36 px cell.
func symbolMask(fs *fontSet, r rune, w int, bold bool) *mask {
	var c canvas
	sw := 2.6 // the stem width of Go Mono at 30 px
	if bold {
		sw = 3.8
	}
	b := float64(fs.baseline)
	top := b - 21.5 // cap height
	mid := float64(cellH) / 2
	switch r {
	case '₹':
		l, rt := 3.5, 14.5
		c.line(l, top+1, rt, top+1, sw*0.85)
		c.line(l, top+6.5, rt, top+6.5, sw*0.85)
		// The bowl: from the top bar, round on the right, back to the left.
		c.arc(7.5, top+6.5, 5.5, -math.Pi/2, math.Pi/2, sw)
		c.line(l+0.5, top+12, 7.5, top+12, sw)
		c.line(l+1, top+12, rt-0.5, b-sw/2, sw)
	case '⅔':
		c.line(14.5, top, 3.5, b, sw*0.7)
		a := image.NewAlpha(image.Rect(0, 0, w, cellH))
		c.paint(a, 0, 0)
		smallGlyph(a, fs.small, '2', 0.5, top+11.5)
		smallGlyph(a, fs.small, '3', 8, b)
		return &mask{a: a}
	case '▶', '▸', '◀', '◂', '▷', '◁':
		hw, x0, x1 := 8.0, 3.5, 16.0 // half height, back, tip
		if r == '▸' || r == '◂' {
			hw, x0, x1 = 6.5, 4.5, 15
		}
		if r == '◀' || r == '◂' || r == '◁' {
			x0, x1 = float64(w)-x0, float64(w)-x1
		}
		pts := [][2]float64{{x0, mid - hw}, {x1, mid}, {x0, mid + hw}}
		if r == '▷' || r == '◁' {
			c.path(2, pts[0], pts[1], pts[2], pts[0])
		} else {
			c.poly(pts...)
		}
	case '✗', '✘':
		width := sw
		if r == '✘' {
			width = sw + 1.4
		}
		c.line(4, top+3, 14.5, b-1, width)
		c.line(14, top+2.5, 3.5, b-0.5, width)
	case '✓', '✔':
		width := sw
		if r == '✔' {
			width = sw + 1.4
		}
		c.path(width, [2]float64{3.5, mid + 1}, [2]float64{7.5, b - 2}, [2]float64{15, top + 1})
	case '⌫':
		y0, y1 := top+3, b-2
		c.path(sw*0.8, [2]float64{1.5, mid}, [2]float64{6.5, y0}, [2]float64{16.5, y0},
			[2]float64{16.5, y1}, [2]float64{6.5, y1}, [2]float64{1.5, mid})
		c.line(8.5, mid-3.5, 13.5, mid+3.5, sw*0.7)
		c.line(13.5, mid-3.5, 8.5, mid+3.5, sw*0.7)
	case '☕':
		if w < 2*cellW {
			return nil
		}
		// A cup with a handle on a saucer, and steam.
		c.poly([2]float64{6, 15}, [2]float64{25, 15}, [2]float64{23.5, 25}, [2]float64{20, 28.5},
			[2]float64{11, 28.5}, [2]float64{7.5, 25})
		c.arc(25, 20.5, 3.8, -math.Pi/2, math.Pi/2, 2.4)
		c.line(4, 31, 28, 31, 2.4)
		for _, x := range []float64{11, 15.5, 20} {
			c.path(1.8, [2]float64{x, 12}, [2]float64{x - 1.5, 9.5}, [2]float64{x, 7}, [2]float64{x - 1.5, 4.5})
		}
	default:
		return nil
	}
	a := image.NewAlpha(image.Rect(0, 0, w, cellH))
	c.paint(a, 0, 0)
	return &mask{a: a}
}

// smallGlyph adds rune r of face at (x, baseline) to a.
func smallGlyph(a *image.Alpha, face font.Face, r rune, x, baseline float64) {
	dot := fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}
	dr, src, sp, _, ok := face.Glyph(dot, r)
	if !ok {
		return
	}
	for y := dr.Min.Y; y < dr.Max.Y; y++ {
		for x := dr.Min.X; x < dr.Max.X; x++ {
			if !(image.Point{x, y}.In(a.Rect)) {
				continue
			}
			_, _, _, v := src.At(sp.X+x-dr.Min.X, sp.Y+y-dr.Min.Y).RGBA()
			i := y*a.Stride + x
			if u := uint8(v >> 8); u > a.Pix[i] {
				a.Pix[i] = u
			}
		}
	}
}
