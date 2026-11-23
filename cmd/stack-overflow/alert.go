package main

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/makaveli2p/stack-overflow/internal/tui"
)

// appName is the name of the game on screens, in prompts and in messages.
const appName = "Stack Overflow"

// maxAlertText is the longest title or body (in runes) that an alert sends.
const maxAlertText = 200

// lockedWriter is the output of one SSH session. Bubble Tea writes each
// frame with one Write call, and alerts go through the same lock, so an
// alert never comes inside a frame.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
	// busy is true while a goroutine writes an alert. A new alert that
	// comes during that time is dropped, so a slow client never collects
	// blocked goroutines.
	busy atomic.Bool
}

func newLockedWriter(w io.Writer) *lockedWriter { return &lockedWriter{w: w} }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

// alert sends a cue to the client terminal. It does not wait for the write,
// because the TUI calls it from the Bubble Tea update loop.
func (w *lockedWriter) alert(a tui.Alert) {
	b := alertBytes(a)
	if len(b) == 0 || !w.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer w.busy.Store(false)
		_, _ = w.Write(b)
	}()
}

// alertBytes encodes an alert: BEL for the bell and OSC 9
// ("\x1b]9;<title>: <body>\x07") for the desktop notification.
func alertBytes(a tui.Alert) []byte {
	var b strings.Builder
	if a.Bell {
		b.WriteString("\a")
	}
	if a.Notify {
		title := cleanAlertText(a.Title)
		if title == "" {
			title = appName
		}
		text := title
		if body := cleanAlertText(a.Body); body != "" {
			text += ": " + body
		}
		b.WriteString("\x1b]9;" + text + "\x07")
	}
	return []byte(b.String())
}

// cleanAlertText removes control characters, escapes and other runes that
// are not graphic (for example a right-to-left override), and cuts the text
// to maxAlertText runes. The text then cannot end the OSC sequence early or
// start a new one.
func cleanAlertText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == unicode.ReplacementChar || !unicode.IsGraphic(r) {
			continue
		}
		if n == maxAlertText {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
