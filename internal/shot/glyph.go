package shot

import (
	"image"
	"image/draw"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

// The cell size in pixels. Go Mono has an advance of 0.6 em, so a 30 px
// font fills an 18 px cell. The line height is 1.2 em.
const (
	cellW    = 18
	cellH    = 36
	fontSize = 30
)

// mask is the ink of one cell content: alpha values at an offset from
// the top-left corner of the cell. It can be larger than the cell.
type mask struct {
	a      *image.Alpha // Rect starts at 0,0
	dx, dy int
	box    bool // the empty box of a rune that nothing can draw
}

// Face variants.
const (
	faceRegular = iota
	faceBold
	faceItalic
	faceBoldItalic
)

type fontSet struct {
	fonts    [4]*sfnt.Font
	faces    [4]font.Face
	small    font.Face // digits of the fractions that the font does not have
	baseline int       // from the top of the cell
	xTop     int       // the top of "x" (the x-height), from the top of the cell
}

var (
	fontsOnce sync.Once
	fonts     *fontSet
	fontsErr  error

	// maskMu guards the mask cache and the faces, which are not safe for
	// concurrent use. The cache keeps the masks of single runes only, so
	// that text with many different clusters cannot make it grow without
	// a limit.
	maskMu    sync.Mutex
	maskCache = map[maskKey]*mask{}
)

type maskKey struct {
	r     rune
	face  uint8
	width uint8
}

func loadFonts() (*fontSet, error) {
	fontsOnce.Do(func() {
		fs := &fontSet{}
		for i, ttf := range [][]byte{gomono.TTF, gomonobold.TTF, gomonoitalic.TTF, gomonobolditalic.TTF} {
			f, err := opentype.Parse(ttf)
			if err != nil {
				fontsErr = err
				return
			}
			face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: fontSize, DPI: 72, Hinting: font.HintingNone})
			if err != nil {
				fontsErr = err
				return
			}
			fs.fonts[i], fs.faces[i] = f, face
		}
		small, err := opentype.NewFace(fs.fonts[faceRegular], &opentype.FaceOptions{Size: fontSize * 0.55, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			fontsErr = err
			return
		}
		fs.small = small
		m := fs.faces[faceRegular].Metrics()
		asc, desc := m.Ascent.Ceil(), m.Descent.Ceil()
		fs.baseline = (cellH-asc-desc)/2 + asc
		xb, _, _ := fs.faces[faceRegular].GlyphBounds('x')
		fs.xTop = fs.baseline + xb.Min.Y.Floor()
		fonts = fs
	})
	return fonts, fontsErr
}

// hasGlyph reports whether the font has a glyph for r.
func (fs *fontSet) hasGlyph(face int, r rune) bool {
	var b sfnt.Buffer
	i, err := fs.fonts[face].GlyphIndex(&b, r)
	return err == nil && i != 0
}

// cellMask returns the ink of a cell of width 1 or 2 that holds rune r
// and then the runes in ext (see cell). Box drawing, block elements and
// the symbols that Go Mono lacks are drawn by code. Combining marks are
// drawn over or under their base (see clusterMask). A rune or a cluster
// that nothing can draw becomes an empty box. The result is shared and
// must not be changed. It returns nil for a blank cell.
func cellMask(fs *fontSet, r rune, ext string, face, width int) *mask {
	maskMu.Lock()
	defer maskMu.Unlock()
	if ext != "" {
		return clusterMask(fs, r, ext, face, width)
	}
	return runeMask(fs, r, face, width)
}

// runeMask is cellMask for one rune, through the cache. The caller holds
// maskMu.
func runeMask(fs *fontSet, r rune, face, width int) *mask {
	key := maskKey{r, uint8(face), uint8(width)}
	if m, ok := maskCache[key]; ok {
		return m
	}
	m := makeMask(fs, r, face, width)
	maskCache[key] = m
	return m
}

// makeMask draws one rune. The caller holds maskMu.
func makeMask(fs *fontSet, r rune, face, width int) *mask {
	w := cellW * width
	if r == ' ' || r == 0 || r == ' ' {
		return nil
	}
	if m := boxMask(r, w); m != nil {
		return m
	}
	if m := symbolMask(fs, r, w, face&faceBold != 0); m != nil {
		return m
	}
	if fs.hasGlyph(face, r) {
		return glyphMask(fs.faces[face], r, w, fs.baseline)
	}
	if face != faceRegular && fs.hasGlyph(faceRegular, r) {
		return glyphMask(fs.faces[faceRegular], r, w, fs.baseline)
	}
	// A letter with marks that Go Mono lacks, for example "ọ" or "ế", is
	// drawn as its base letter and its marks.
	if d := []rune(norm.NFD.String(string(r))); len(d) > 1 {
		if m, ok := markedMask(fs, d, face, width); ok {
			return m
		}
	}
	return emptyBox(w)
}

// glyphMask draws r from the font face, centred in a cell of width w.
func glyphMask(face font.Face, r rune, w, baseline int) *mask {
	adv, ok := face.GlyphAdvance(r)
	if !ok {
		return emptyBox(w)
	}
	x := (fixed.I(w) - adv) / 2
	dot := fixed.Point26_6{X: x, Y: fixed.I(baseline)}
	dr, src, sp, _, ok := face.Glyph(dot, r)
	if !ok || dr.Empty() {
		return nil
	}
	a := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	draw.Draw(a, a.Rect, src, sp, draw.Src)
	if !hasInk(a) {
		return nil
	}
	return &mask{a: a, dx: dr.Min.X, dy: dr.Min.Y}
}

func hasInk(a *image.Alpha) bool {
	for _, v := range a.Pix {
		if v != 0 {
			return true
		}
	}
	return false
}

// emptyBox is the mark for a rune that no font has: the outline of a
// box, so the columns still line up.
func emptyBox(w int) *mask {
	a := image.NewAlpha(image.Rect(0, 0, w, cellH))
	x0, x1 := 3, w-3
	y0, y1 := 7, cellH-6
	for x := x0; x < x1; x++ {
		for _, y := range []int{y0, y0 + 1, y1 - 2, y1 - 1} {
			a.Pix[y*a.Stride+x] = 0xff
		}
	}
	for y := y0; y < y1; y++ {
		for _, x := range []int{x0, x0 + 1, x1 - 2, x1 - 1} {
			a.Pix[y*a.Stride+x] = 0xff
		}
	}
	return &mask{a: a, box: true}
}
