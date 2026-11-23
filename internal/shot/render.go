package shot

import (
	"bytes"
	"image"
	"image/png"

	"github.com/charmbracelet/x/ansi"
)

// Window frame sizes in pixels.
const (
	titleH    = 56 // the title bar, with its line at the bottom
	barLineH  = 2
	dotR      = 12 // radius of the three dots
	dotStep   = 40 // from one dot centre to the next
	dotLeft   = 32 // x of the first dot centre
	underOff  = 3  // underline: pixels under the baseline
	underH    = 2
	strikeOff = 9 // strike-through: pixels over the baseline
)

// render draws f as a PNG. See Render.
func render(f Frame) ([]byte, error) {
	if f.Width < 1 || f.Height < 1 || f.Width > MaxWidth || f.Height > MaxHeight {
		return nil, ErrTooBig
	}
	fs, err := loadFonts()
	if err != nil {
		return nil, err
	}
	pal := &lightPalette
	if f.Dark {
		pal = &darkPalette
	}
	g := parseView(f.View, f.Width, f.Height)

	w := (f.Width + 2) * cellW
	h := titleH + (f.Height+2)*cellH
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, image.Rect(0, 0, w, titleH-barLineH), pal.bar)
	fillRect(img, image.Rect(0, titleH-barLineH, w, titleH), pal.barLine)
	fillRect(img, image.Rect(0, titleH, w, h), pal.bg)
	drawTitleBar(img, fs, pal, f.Title)
	drawGrid(img, fs, pal, g, image.Pt(cellW, titleH+cellH))

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawGrid draws the cells with their top-left corner at o.
func drawGrid(img *image.RGBA, fs *fontSet, pal *palette, g *grid, o image.Point) {
	// Ink can reach into the margin around the grid, for example the
	// marks over a capital letter in the top row. It stays out of the
	// title bar.
	area := image.Rect(0, o.Y-cellH, img.Rect.Dx(), img.Rect.Dy())
	type ink struct {
		x, y int
		c    *cell
		fg   rgb
	}
	inks := make([]ink, 0, len(g.cells)/2)
	// Backgrounds first, so that a glyph that reaches over its cell is
	// not painted over by the next cell.
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			c := g.at(x, y)
			st := c.st
			fg := pal.resolve(st.fg, pal.fg, st.attr&attrBold != 0)
			bg := pal.resolve(st.bg, pal.bg, false)
			if st.attr&attrReverse != 0 {
				fg, bg = bg, fg
			}
			if st.attr&attrFaint != 0 {
				fg = mix(fg, bg, 120)
			}
			px, py := o.X+x*cellW, o.Y+y*cellH
			if bg != pal.bg {
				fillRect(img, image.Rect(px, py, px+cellW, py+cellH), bg)
			}
			if c.width > 0 && st.attr&attrHidden == 0 &&
				(c.r != ' ' || c.ext != "" || st.attr&(attrUnderline|attrStrike) != 0) {
				inks = append(inks, ink{px, py, c, fg})
			}
		}
	}
	type localKey struct {
		maskKey
		ext string
	}
	local := map[localKey]*mask{}
	for _, k := range inks {
		c := k.c
		face := faceRegular
		if c.st.attr&attrBold != 0 {
			face |= faceBold
		}
		if c.st.attr&attrItalic != 0 {
			face |= faceItalic
		}
		key := localKey{maskKey{c.r, uint8(face), c.width}, c.ext}
		m, ok := local[key]
		if !ok {
			m = cellMask(fs, c.r, c.ext, face, int(c.width))
			local[key] = m
		}
		if m != nil {
			blendMask(img, m.a, image.Pt(k.x+m.dx, k.y+m.dy), k.fg, area)
		}
		cw := cellW * int(c.width)
		if c.st.attr&attrUnderline != 0 {
			y := k.y + fs.baseline + underOff
			fillRect(img, image.Rect(k.x, y, k.x+cw, y+underH).Intersect(area), k.fg)
		}
		if c.st.attr&attrStrike != 0 {
			y := k.y + fs.baseline - strikeOff
			fillRect(img, image.Rect(k.x, y, k.x+cw, y+underH).Intersect(area), k.fg)
		}
	}
}

// drawTitleBar draws the three dots and the title, centred and cut with
// "…" when it is too long.
func drawTitleBar(img *image.RGBA, fs *fontSet, pal *palette, title string) {
	cy := float64(titleH-barLineH) / 2
	var c canvas
	dots := image.NewAlpha(image.Rect(0, 0, 2*dotR+2, 2*dotR+2))
	c.disc(float64(dotR+1), float64(dotR+1), dotR)
	c.paint(dots, 0, 0)
	for i, col := range dotColors {
		x := dotLeft + i*dotStep - dotR - 1
		blendMask(img, dots, image.Pt(x, int(cy)-dotR-1), col, img.Rect)
	}

	// The title keeps clear of the dots on both sides.
	side := dotLeft + 2*dotStep + dotR + cellW
	n := (img.Rect.Dx() - 2*side) / cellW
	title = ansi.Strip(title)
	if n < 1 || title == "" {
		return
	}
	title = ansi.Truncate(title, n, "…")
	tw := ansi.StringWidth(title)
	x := (img.Rect.Dx() - tw*cellW) / 2
	y := (titleH - barLineH - cellH) / 2
	bar := image.Rect(0, 0, img.Rect.Dx(), titleH-barLineH)
	// parseView keeps each cluster whole, with its marks.
	row := parseView(title, max(tw, 1), 1)
	for i, c := range row.cells {
		if c.width == 0 {
			continue
		}
		if m := cellMask(fs, c.r, c.ext, faceRegular, int(c.width)); m != nil {
			blendMask(img, m.a, image.Pt(x+i*cellW+m.dx, y+m.dy), pal.title, bar)
		}
	}
}

// fillRect paints r (clipped to img) with c.
func fillRect(img *image.RGBA, r image.Rectangle, c rgb) {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return
	}
	row := img.Pix[img.PixOffset(r.Min.X, r.Min.Y):]
	n := r.Dx() * 4
	for i := 0; i < n; i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = c.r, c.g, c.b, 0xff
	}
	first := row[:n]
	for y := r.Min.Y + 1; y < r.Max.Y; y++ {
		copy(img.Pix[img.PixOffset(r.Min.X, y):], first)
	}
}

// blendMask paints colour c through mask a, placed with its top-left
// corner at at, inside clip.
func blendMask(img *image.RGBA, a *image.Alpha, at image.Point, c rgb, clip image.Rectangle) {
	r := image.Rectangle{at, at.Add(a.Rect.Size())}.Intersect(clip).Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := a.Pix[(y-at.Y)*a.Stride+(r.Min.X-at.X):]
		dst := img.Pix[img.PixOffset(r.Min.X, y):]
		for i := 0; i < r.Dx(); i++ {
			v := uint32(src[i])
			if v == 0 {
				continue
			}
			d := dst[i*4 : i*4+3 : i*4+3]
			if v == 0xff {
				d[0], d[1], d[2] = c.r, c.g, c.b
				continue
			}
			u := 255 - v
			d[0] = uint8((uint32(c.r)*v + uint32(d[0])*u + 127) / 255)
			d[1] = uint8((uint32(c.g)*v + uint32(d[1])*u + 127) / 255)
			d[2] = uint8((uint32(c.b)*v + uint32(d[2])*u + 127) / 255)
		}
	}
}
