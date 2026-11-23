package shot_test

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/makaveli2p/stack-overflow/internal/shot"
	"github.com/makaveli2p/stack-overflow/internal/tui"
)

// TestRenderFixtures draws the real TUI screens: every fixture at 80x24
// and 188x55, dark and light, with 24-bit colours (and 256 colours at
// 80x24). A 188x55 screen must take less than 300 ms and give less than
// 2 MB. With SHOT_DIR set, it writes the PNGs there, so you can look at
// them. Under -race or -short it draws only a few fixtures, because the
// race detector makes the PNG encoder very slow.
func TestRenderFixtures(t *testing.T) {
	dir := os.Getenv("SHOT_DIR")
	fixtures := tui.Fixtures()
	if testing.Short() || raceEnabled {
		fixtures = fixtures[:2]
	}
	sizes := [][2]int{{80, 24}, {188, 55}}
	for _, dark := range []bool{true, false} {
		for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256} {
			r := lipgloss.NewRenderer(io.Discard)
			r.SetColorProfile(profile)
			r.SetHasDarkBackground(dark)
			for _, sz := range sizes {
				if profile != termenv.TrueColor && sz[0] > 80 {
					continue
				}
				for _, f := range fixtures {
					view := tui.Render(f, r, sz[0], sz[1])
					fr := shot.Frame{View: view, Width: sz[0], Height: sz[1], Dark: dark,
						Title: "Stack Overflow — Devon · hand #42"}
					start := time.Now()
					b, err := shot.Render(fr)
					took := time.Since(start)
					if err != nil {
						t.Fatalf("%s %dx%d: %v", f.Name, sz[0], sz[1], err)
					}
					cfg, err := png.DecodeConfig(bytes.NewReader(b))
					if err != nil {
						t.Fatalf("%s: not a PNG: %v", f.Name, err)
					}
					if cfg.Width != (sz[0]+2)*18 || cfg.Height != 56+(sz[1]+2)*36 {
						t.Errorf("%s: size %dx%d", f.Name, cfg.Width, cfg.Height)
					}
					if len(b) >= 2<<20 {
						t.Errorf("%s %dx%d: %d bytes, want < 2 MB", f.Name, sz[0], sz[1], len(b))
					}
					if sz[0] == 188 && !raceEnabled && !testing.Short() && took > 300*time.Millisecond {
						t.Errorf("%s 188x55: took %v, want < 300 ms", f.Name, took)
					}
					if dir != "" && profile == termenv.TrueColor {
						mode := "light"
						if dark {
							mode = "dark"
						}
						name := fmt.Sprintf("%s-%dx%d-%s.png", slug(f.Name), sz[0], sz[1], mode)
						if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// BenchmarkRender188x55 draws a full table at 188x55.
func BenchmarkRender188x55(b *testing.B) {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	view := tui.Render(tui.Fixtures()[3], r, 188, 55)
	f := shot.Frame{View: view, Width: 188, Height: 55, Dark: true, Title: "Stack Overflow"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := shot.Render(f); err != nil {
			b.Fatal(err)
		}
	}
}
