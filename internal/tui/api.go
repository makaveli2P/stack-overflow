// Package tui is the Bubble Tea front end for one SSH session.
//
// CONTRACT FILE: New, Options and Alert are fixed; cmd/stack-overflow calls them.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/server"
	"github.com/makaveli2p/stack-overflow/internal/shot"
)

// Alert is one out-of-band cue for the player's terminal.
type Alert struct {
	Bell   bool   // ring the terminal bell (BEL)
	Notify bool   // show a desktop notification (OSC 9) with Title and Body
	Title  string // e.g. "Stack Overflow"
	Body   string // e.g. "Your turn — Bob raised to 60"
}

// Options are hooks from the SSH layer.
type Options struct {
	// Alert sends a cue to the client terminal outside the Bubble Tea frame.
	// The SSH layer writes it through the same locked writer as the program
	// output, so it never splits a frame. nil disables alerts.
	Alert func(Alert)
	// Shot draws the frame as a PNG, stores it and returns its links, for
	// example "http://192.168.1.20:2280/k3jq7xw2mf". The TUI calls it
	// from a tea.Cmd (never from Update), because drawing takes time.
	// nil disables screenshots.
	Shot func(shot.Frame) (shot.Links, error)
}

// New returns the model for one session. r is the session's lipgloss
// renderer (from wish's bubbletea middleware), so colours match the
// client's terminal. width and height are the first window size.
func New(sess *server.Session, r *lipgloss.Renderer, width, height int, opts Options) tea.Model {
	return newModel(sess, r, width, height, opts)
}
