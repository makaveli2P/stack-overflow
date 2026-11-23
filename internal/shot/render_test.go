package shot

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Where the grid starts in the image.
const (
	gridX = cellW
	gridY = titleH + cellH
)

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return img
}

func renderImg(t *testing.T, f Frame) image.Image {
	t.Helper()
	b, err := Render(f)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return decode(t, b)
}

func at(img image.Image, x, y int) rgb {
	r, g, b, _ := img.At(x, y).RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

// atCell is the colour at the centre of cell (cx, cy).
func atCell(img image.Image, cx, cy int) rgb {
	return at(img, gridX+cx*cellW+cellW/2, gridY+cy*cellH+cellH/2)
}

func TestTooBig(t *testing.T) {
	for _, sz := range [][2]int{{0, 0}, {0, 10}, {10, 0}, {-1, 5}, {MaxWidth + 1, 10}, {10, MaxHeight + 1}} {
		_, err := Render(Frame{View: "x", Width: sz[0], Height: sz[1]})
		if !errors.Is(err, ErrTooBig) {
			t.Errorf("%dx%d: err %v, want ErrTooBig", sz[0], sz[1], err)
		}
	}
	if _, err := Render(Frame{View: "x", Width: 1, Height: 1}); err != nil {
		t.Errorf("1x1: %v", err)
	}
}

func TestPixelSize(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {80, 24}, {MaxWidth, 3}} {
		b, err := Render(Frame{View: "hello", Width: sz[0], Height: sz[1], Dark: true, Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		wantW, wantH := (sz[0]+2)*cellW, titleH+(sz[1]+2)*cellH
		if cfg.Width != wantW || cfg.Height != wantH {
			t.Errorf("%dx%d cells: %dx%d px, want %dx%d", sz[0], sz[1], cfg.Width, cfg.Height, wantW, wantH)
		}
	}
}

func TestBackgroundColours(t *testing.T) {
	cases := []struct {
		name, view string
		want       rgb
	}{
		{"24-bit", "\x1b[48;2;12;200;99m \x1b[0m", rgb{12, 200, 99}},
		{"24-bit colon", "\x1b[48:2::12:200:99m \x1b[0m", rgb{12, 200, 99}},
		{"256 cube", "\x1b[48;5;196m \x1b[0m", rgb{255, 0, 0}},
		{"256 grey", "\x1b[48;5;232m \x1b[0m", rgb{8, 8, 8}},
		{"16", "\x1b[42m \x1b[0m", darkPalette.ansi[2]},
		{"16 bright", "\x1b[103m \x1b[0m", darkPalette.ansi[11]},
		{"256 low", "\x1b[48;5;4m \x1b[0m", darkPalette.ansi[4]},
		{"reset 49", "\x1b[48;2;1;2;3m\x1b[49m \x1b[0m", darkPalette.bg},
		{"reset 0", "\x1b[48;2;1;2;3m\x1b[0m ", darkPalette.bg},
		{"bare reset", "\x1b[48;2;1;2;3m\x1b[m ", darkPalette.bg},
		{"reverse", "\x1b[7;38;2;10;20;30m \x1b[0m", rgb{10, 20, 30}},
		{"reverse default", "\x1b[7m \x1b[0m", darkPalette.fg},
		{"reverse off", "\x1b[7m\x1b[27;48;2;5;6;7m \x1b[0m", rgb{5, 6, 7}},
		{"after OSC", "\x1b]8;;http://x\x07\x1b[48;2;9;9;9m \x1b[0m", rgb{9, 9, 9}},
	}
	for _, c := range cases {
		img := renderImg(t, Frame{View: c.view, Width: 3, Height: 1, Dark: true})
		if got := at(img, gridX+cellW/2, gridY+cellH/2); got != c.want {
			t.Errorf("%s: centre %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReverseSwaps(t *testing.T) {
	fg, bg := rgb{250, 10, 10}, rgb{10, 10, 250}
	view := "\x1b[38;2;250;10;10;48;2;10;10;250m█\x1b[7m█\x1b[0m"
	img := renderImg(t, Frame{View: view, Width: 2, Height: 1, Dark: true})
	if got := atCell(img, 0, 0); got != fg {
		t.Errorf("plain █: %v, want fg %v", got, fg)
	}
	if got := atCell(img, 1, 0); got != bg {
		t.Errorf("reversed █: %v, want bg %v", got, bg)
	}
	// A reversed space shows the foreground colour.
	img = renderImg(t, Frame{View: "\x1b[38;2;250;10;10;48;2;10;10;250;7m \x1b[0m", Width: 1, Height: 1})
	if got := atCell(img, 0, 0); got != fg {
		t.Errorf("reversed space: %v, want %v", got, fg)
	}
}

func TestStyles(t *testing.T) {
	ink := func(view string) (n int, ul bool) {
		img := renderImg(t, Frame{View: view, Width: 1, Height: 1, Dark: true})
		for y := gridY; y < gridY+cellH; y++ {
			for x := gridX; x < gridX+cellW; x++ {
				if at(img, x, y) != darkPalette.bg {
					n++
				}
			}
		}
		fs, _ := loadFonts()
		return n, at(img, gridX+cellW/2, gridY+fs.baseline+underOff) != darkPalette.bg
	}
	plain, ul := ink("H")
	if ul {
		t.Error("plain H has an underline")
	}
	if bold, _ := ink("\x1b[1mH"); bold <= plain {
		t.Errorf("bold H has %d ink pixels, plain %d", bold, plain)
	}
	if _, ul := ink("\x1b[4mH"); !ul {
		t.Error("SGR 4 draws no underline")
	}
	if _, ul := ink("\x1b[4m\x1b[24mH"); ul {
		t.Error("SGR 24 does not end the underline")
	}
	if _, ul := ink("\x1b[4:0mH"); ul {
		t.Error("SGR 4:0 draws an underline")
	}
	// Faint mixes the colour with the background.
	img := renderImg(t, Frame{View: "\x1b[2;38;2;255;255;255m█", Width: 1, Height: 1, Dark: true})
	if got := atCell(img, 0, 0); got.r == 255 || got.r <= darkPalette.bg.r {
		t.Errorf("faint █: %v", got)
	}
	img = renderImg(t, Frame{View: "\x1b[2m\x1b[22;38;2;255;255;255m█", Width: 1, Height: 1, Dark: true})
	if got := atCell(img, 0, 0); got != (rgb{255, 255, 255}) {
		t.Errorf("SGR 22 does not end faint: %v", got)
	}
}

// TestHorizontalLineIsContinuous checks that ─ joins across cell borders.
func TestHorizontalLineIsContinuous(t *testing.T) {
	for _, r := range []string{"─", "━", "═", "╌", "┄"} {
		fg := rgb{200, 100, 50}
		view := "\x1b[38;2;200;100;50m" + strings.Repeat(r, 10)
		img := renderImg(t, Frame{View: view, Width: 10, Height: 1, Dark: true})
		y := gridY + cellH/2
		if r == "═" {
			y = gridY + cellH/2 - dblGap
		}
		gaps := 0
		for x := gridX; x < gridX+10*cellW; x++ {
			if at(img, x, y) != fg {
				gaps++
			}
		}
		dashed := r == "╌" || r == "┄"
		if !dashed && gaps > 0 {
			t.Errorf("%s: %d gap pixels in the line", r, gaps)
		}
		if dashed && (gaps == 0 || gaps > 10*cellW/2) {
			t.Errorf("%s: %d gap pixels, want some but not most", r, gaps)
		}
	}
	// A vertical line joins across rows, and a corner meets both lines.
	view := "╭─╮\n│ │\n╰─╯"
	img := renderImg(t, Frame{View: view, Width: 3, Height: 3, Dark: true})
	x := gridX + cellW/2
	for y := gridY + cellH; y < gridY+2*cellH; y++ {
		if at(img, x, y) != darkPalette.fg {
			t.Fatalf("│: gap at y %d", y)
		}
	}
	for _, p := range [][2]int{{gridX + cellW - 1, gridY + cellH/2}, {gridX + cellW/2, gridY + cellH}} {
		if at(img, p[0], p[1]) != darkPalette.fg {
			t.Errorf("╭ does not reach %v", p)
		}
	}
}

func TestWideRunes(t *testing.T) {
	// The wide rune takes 2 cells, so "x" lands in the fourth cell.
	view := "界\x1b[48;2;1;200;1mx"
	img := renderImg(t, Frame{View: view, Width: 4, Height: 1, Dark: true})
	if got := at(img, gridX+2*cellW+2, gridY+2); got != (rgb{1, 200, 1}) {
		t.Errorf("cell 2 is %v, want the bg of x", got)
	}
	// Lines past the size are cut, and a short line is padded.
	img = renderImg(t, Frame{View: "\x1b[48;2;9;9;9mabcdef\n\nzz\nmore", Width: 3, Height: 2, Dark: false})
	if b := img.Bounds(); b.Dx() != 5*cellW || b.Dy() != titleH+4*cellH {
		t.Errorf("bounds %v", b)
	}
	if got := atCell(img, 2, 1); got != lightPalette.bg {
		t.Errorf("padded cell %v", got)
	}
}

// TestTitle checks the title bar: a long title is cut and the dots show.
func TestTitle(t *testing.T) {
	img := renderImg(t, Frame{View: "", Width: 30, Height: 1, Dark: true, Title: strings.Repeat("very long title ", 10)})
	if got := at(img, dotLeft, (titleH-barLineH)/2); got != dotColors[0] {
		t.Errorf("red dot is %v", got)
	}
	// The ink of the title stays clear of the dots.
	side := dotLeft + 2*dotStep + dotR
	for y := 0; y < titleH-barLineH; y++ {
		for x := side; x < side+cellW/2; x++ {
			if at(img, x, y) != darkPalette.bar {
				t.Fatalf("title ink at %d,%d", x, y)
			}
		}
	}
	// Narrow screens get no title, and no panic.
	renderImg(t, Frame{View: "", Width: 1, Height: 1, Title: "Stack Overflow — Devon"})
}

// tuiRunes lists every non-ASCII rune in the string and rune literals of
// internal/tui/*.go, without the tests.
func tuiRunes(t *testing.T) []rune {
	t.Helper()
	files, err := filepath.Glob("../tui/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no tui files: %v", err)
	}
	seen := map[rune]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
				return true
			}
			s := lit.Value
			if lit.Kind == token.CHAR {
				r, _, _, err := strconv.UnquoteChar(s[1:len(s)-1], '\'')
				if err != nil {
					t.Fatalf("%s: %s: %v", name, s, err)
				}
				s = string(r)
			} else if s, err = strconv.Unquote(s); err != nil {
				t.Fatalf("%s: %s: %v", name, lit.Value, err)
			}
			for _, r := range s {
				if r >= 0x80 {
					seen[r] = true
				}
			}
			return true
		})
	}
	var out []rune
	for r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestEveryTUIRuneDraws checks that each non-ASCII rune of the TUI puts
// ink in its cell, so nothing is dropped.
func TestEveryTUIRuneDraws(t *testing.T) {
	runes := tuiRunes(t)
	if len(runes) < 20 {
		t.Fatalf("found only %d runes; the scan is broken", len(runes))
	}
	for _, r := range runes {
		if unicode.IsSpace(r) || !unicode.IsGraphic(r) {
			continue
		}
		w := ansi.StringWidth(string(r))
		if w == 0 {
			continue
		}
		img := renderImg(t, Frame{View: " " + string(r) + " ", Width: 4, Height: 1, Dark: true})
		ink := 0
		for y := gridY; y < gridY+cellH; y++ {
			for x := gridX + cellW; x < gridX+(1+w)*cellW; x++ {
				if at(img, x, y) != darkPalette.bg {
					ink++
				}
			}
		}
		if ink == 0 {
			t.Errorf("%c (U+%04X) draws nothing", r, r)
		}
	}
}

// TestEveryBoxRuneDraws checks box drawing and block elements, and that
// an unknown rune becomes a box.
func TestEveryBoxRuneDraws(t *testing.T) {
	fs, err := loadFonts()
	if err != nil {
		t.Fatal(err)
	}
	for r := rune(0x2500); r <= 0x259f; r++ {
		m := cellMask(fs, r, "", faceRegular, 1)
		if m == nil || !hasInk(m.a) {
			t.Errorf("%c (U+%04X) draws nothing", r, r)
		}
	}
	for _, r := range []rune{'\U0001F92B', '\U000F0000', '⌘'} {
		if m := cellMask(fs, r, "", faceRegular, 2); m == nil || !hasInk(m.a) {
			t.Errorf("%c (U+%04X) draws nothing", r, r)
		}
	}
}

// TestGlyphSheet draws every TUI rune, the box drawing and the block
// elements on one screen. With SHOT_DIR set, it writes the PNG there.
func TestGlyphSheet(t *testing.T) {
	var sb strings.Builder
	for i, r := range tuiRunes(t) {
		if i > 0 && i%24 == 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(string(r) + " ")
	}
	sb.WriteString("\n\n")
	for r := rune(0x2500); r <= 0x259f; r++ {
		sb.WriteRune(r)
		if (r-0x2500)%32 == 31 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n╭──┬──╮ ┏━━┳━━┓ ╔══╦══╗ ┌┄┄┬┈┈┐\n│  │  │ ┃  ┃  ┃ ║  ║  ║ ┆  ┊  ┊\n├──┼──┤ ┣━━╋━━┫ ╠══╬══╣ ╞══╪══╡\n╰──┴──╯ ┗━━┻━━┛ ╚══╩══╝ └──┴──┘\n")
	sb.WriteString("\x1b[1mBold ₹1,240 ▶ ✗\x1b[0m \x1b[3mitalic\x1b[0m \x1b[4munder\x1b[0m \x1b[2mfaint\x1b[0m \x1b[9mstrike\x1b[0m 界 🤫 ☕")
	for _, dark := range []bool{true, false} {
		b, err := Render(Frame{View: sb.String(), Width: 50, Height: 18, Dark: dark, Title: "Glyph sheet"})
		if err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("SHOT_DIR"); dir != "" {
			name := "glyph-sheet-light.png"
			if dark {
				name = "glyph-sheet-dark.png"
			}
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestRenderConcurrent renders from many goroutines at once; run it with -race.
func TestRenderConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			view := strings.Repeat("\x1b[1m♠♥ ─│ ₹"+string(rune('a'+i))+"\x1b[0m\n", 4)
			if _, err := Render(Frame{View: view, Width: 20, Height: 4, Dark: i%2 == 0}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}
