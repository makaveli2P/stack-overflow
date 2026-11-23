// Package shot draws a player's screen as a PNG and serves it over HTTP
// on the office network, at a random address that expires.
//
// CONTRACT FILE: the exported names and signatures are fixed. The TUI
// calls the hook in tui.Options.Shot; cmd/stack-overflow owns the Store
// and the HTTP listener. See DESIGN.md "Batch 13".
package shot

import (
	"errors"
	"net/http"
	"time"
)

// Frame is one screen of one player, as the TUI draws it.
type Frame struct {
	// View is the frame as tui's View returns it: lines split by "\n",
	// with SGR escape codes (16, 256 and 24-bit colours, bold, faint,
	// italic, underline, reverse). Other escape codes are ignored.
	View string
	// Width and Height are the terminal size in cells. Lines that are
	// shorter are padded; lines and rows past the size are cut.
	Width, Height int
	// Dark is true when the player's terminal has a dark background. It
	// selects the default colours and the 16-colour palette.
	Dark bool
	// Title is the text in the window title bar, for example
	// "Stack Overflow — Devon · hand #42".
	Title string
}

// Links are the two links to one stored shot.
type Links struct {
	// Own is for the player who took the shot: it uses the address that
	// their SSH client connected to (127.0.0.1 for the host).
	Own string
	// Table is for the whole table: it uses the office-network address of
	// the server (the banner address), found when the shot is made. It is
	// Own when that address is not known.
	Table string
}

// Limits for Render.
const (
	MaxWidth  = 400 // cells
	MaxHeight = 200 // cells
)

// ErrTooBig is returned by Render for a frame larger than MaxWidth x MaxHeight
// or smaller than 1x1.
var ErrTooBig = errors.New("the screen is too big for a screenshot")

// Render draws the frame as a PNG image inside a window frame (a title
// bar with three dots and the title). Box drawing (U+2500-U+257F) and
// block elements (U+2580-U+259F) are drawn as lines and rectangles, so
// they join from cell to cell. Wide runes take 2 cells. It is safe for
// concurrent use.
func Render(f Frame) ([]byte, error) { return render(f) }

// StoreConfig sets the limits of a Store. Zero values take the defaults.
type StoreConfig struct {
	TTL      time.Duration    // how long a shot stays; default 30 minutes
	MaxShots int              // shots kept at most; default 200 (the oldest go first)
	MaxBytes int              // total PNG bytes kept at most; default 64 MiB
	Now      func() time.Time // the clock; default time.Now
}

// Store keeps the PNGs in memory and serves them. It is safe for
// concurrent use.
type Store struct{ s *store }

// NewStore returns an empty store.
func NewStore(cfg StoreConfig) *Store { return &Store{s: newStore(cfg)} }

// Put stores png and returns its path, "/<token>". The token has 10
// characters of lowercase base32 (50 random bits) from crypto/rand. The
// path is short, so that the whole link fits in the chat panel (batch 15). name
// is the file name for a browser that saves the image (for example
// "stack-overflow-hand-42.png"); unsafe characters are removed. Put
// returns "" for an empty png or a png larger than MaxBytes, and stores
// nothing then.
func (s *Store) Put(png []byte, name string) string { return s.s.put(png, name) }

// Len returns the number of shots in the store, after expiry.
func (s *Store) Len() int { return s.s.len() }

// Handler serves GET and HEAD "/<token>" with the PNG
// (Content-Type image/png, nosniff, no-store, an inline Content-Disposition
// with the name). Every other path, method or token (unknown or expired)
// gets 404 with a short plain-text body.
func (s *Store) Handler() http.Handler { return s.s.handler() }
