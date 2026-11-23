// Command tuipreview prints the TUI fixtures to stdout, so you can look at
// the layout at any terminal size without an SSH server.
//
//	go run ./cmd/tuipreview -w 100 -h 30
//	go run ./cmd/tuipreview -w 80 -h 24 -only showdown -color none
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/makaveli2p/stack-overflow/internal/tui"
)

func main() {
	w := flag.Int("w", 100, "terminal width")
	h := flag.Int("h", 30, "terminal height")
	only := flag.String("only", "", "draw only fixtures whose name contains this text")
	color := flag.String("color", "true", "colour profile: true, 256, 16 or none")
	light := flag.Bool("light", false, "draw for a light terminal background")
	flag.Parse()

	profiles := map[string]termenv.Profile{"true": termenv.TrueColor, "256": termenv.ANSI256, "16": termenv.ANSI, "none": termenv.Ascii}
	p, ok := profiles[*color]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown -color %q\n", *color)
		os.Exit(2)
	}
	r := lipgloss.NewRenderer(os.Stdout)
	r.SetColorProfile(p)
	r.SetHasDarkBackground(!*light)

	for _, f := range tui.Fixtures() {
		if *only != "" && !strings.Contains(f.Name, *only) {
			continue
		}
		view := tui.Render(f, r, *w, *h)
		fmt.Printf("── %s (%dx%d) %s\n", f.Name, *w, *h, strings.Repeat("─", max(*w-len(f.Name)-16, 0)))
		lines := strings.Split(view, "\n")
		reset := "\x1b[0m"
		if p == termenv.Ascii {
			reset = ""
		}
		for _, l := range lines {
			fmt.Println(l + reset + strings.Repeat(" ", max(*w-lipgloss.Width(l), 0)) + "┊")
		}
		for i := len(lines); i < *h; i++ {
			fmt.Println(strings.Repeat(" ", *w) + "┊")
		}
		fmt.Println(strings.Repeat("┈", *w) + "┘")
		fmt.Println()
	}
}
