package main

import (
	"errors"
	"io"
	"sync"
	"time"
)

// Input limits for one SSH session. A player types keys, a short chat line
// (at most 200 runes) or a raise amount, so real input is a few hundred
// bytes at most.
//
// The Bubble Tea input reader keeps and decodes again the whole buffer on
// each read while a run of printable runes or a bracketed paste is open, so
// its cost grows with the square of the run length. A run or a paste longer
// than maxInputRun closes the connection.
//
// Each key message also costs CPU: Bubble Tea draws the whole view after
// each message (about 2 ms for the table view). The guard charges each read
// with an upper bound of the messages in it (see inputGuard.Read). Input
// faster than maxInputRate messages per second, after a burst of
// maxInputBurst messages, is slowed down (the SSH flow control then stops
// the client); it does not close the connection.
const (
	// maxInputRun is the longest run of bytes without a space or a control
	// byte, and the longest bracketed paste, that a session can send.
	maxInputRun = 4 << 10
	// maxInputBurst is the number of messages that a session can send at
	// once without delay.
	maxInputBurst = 512
	// maxInputRate is the rate in messages per second at which the burst
	// allowance fills again. Fast typing or a held key stays below it for
	// a long time.
	maxInputRate = 64
)

// errInputFlood is the read error after a session went past an input limit.
var errInputFlood = errors.New("input limit exceeded")

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// inputGuard is an io.Reader between the SSH session and Bubble Tea. It
// counts the bytes that the client sends. It delays input that comes too
// fast. When a run or a paste is too long, it calls onFlood once and
// returns errInputFlood from then on.
type inputGuard struct {
	r        io.Reader
	done     <-chan struct{} // closed when the session ends; stops a delay
	maxRun   int
	maxBurst float64 // messages
	rate     float64 // messages per second
	now      func() time.Time
	wait     func(d time.Duration, done <-chan struct{}) bool
	onFlood  func(reason string)

	// Only Read changes these fields, and Bubble Tea calls Read from one
	// goroutine; mu guards against a second reader.
	mu       sync.Mutex
	run      int     // bytes in the open run of runes or the open paste
	paste    bool    // true between pasteStart and pasteEnd
	startPos int     // matched bytes of pasteStart
	endPos   int     // matched bytes of pasteEnd
	tokens   float64 // messages that the session can send now; < 0 is a debt
	last     time.Time
	tripped  bool
}

func newInputGuard(r io.Reader, done <-chan struct{}, onFlood func(reason string)) *inputGuard {
	return &inputGuard{
		r:        r,
		done:     done,
		maxRun:   maxInputRun,
		maxBurst: maxInputBurst,
		rate:     maxInputRate,
		now:      time.Now,
		wait:     waitOrDone,
		onFlood:  onFlood,
	}
}

// waitOrDone waits for d. It returns false when done closes first.
func waitOrDone(d time.Duration, done <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-done:
		return false
	}
}

// Read reads from the session, checks the limits and, when the input comes
// too fast, waits before it gives the bytes to Bubble Tea.
func (g *inputGuard) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tripped {
		return 0, errInputFlood
	}
	n, err := g.r.Read(p)
	if n == 0 {
		return n, err
	}
	breaks, reason := g.scan(p[:n])
	if reason != "" {
		g.tripped = true
		if g.onFlood != nil {
			g.onFlood(reason)
		}
		return 0, errInputFlood
	}
	// Bubble Tea makes one message from each run of runes, each paste and
	// each space, control byte or escape sequence. Each such message ends
	// at a break byte or at the end of a read, so a read has at most
	// 2*breaks+1 messages, and never more than n. A long run of runes or a
	// paste costs little here; maxInputRun limits those.
	if d := g.spend(min(n, 2*breaks+1)); d > 0 && !g.wait(d, g.done) {
		return 0, io.EOF
	}
	return n, err
}

// spend takes n messages from the allowance and returns how long the
// reader must wait to pay a debt.
func (g *inputGuard) spend(n int) time.Duration {
	now := g.now()
	if g.last.IsZero() {
		g.tokens = g.maxBurst
	} else {
		g.tokens = min(g.maxBurst, g.tokens+now.Sub(g.last).Seconds()*g.rate)
	}
	g.last = now
	g.tokens -= float64(n)
	if g.tokens >= 0 {
		return 0
	}
	return time.Duration(-g.tokens / g.rate * float64(time.Second))
}

// scan follows runs and bracketed pastes in b. It returns the number of
// break bytes (a space or a control byte outside a paste, or the end of a
// paste) and the reason when a run or a paste is too long.
func (g *inputGuard) scan(b []byte) (breaks int, reason string) {
	for _, c := range b {
		g.startPos = matchStep(g.startPos, c, pasteStart)
		g.endPos = matchStep(g.endPos, c, pasteEnd)
		switch {
		case g.paste:
			g.run++
			if g.endPos == len(pasteEnd) {
				g.paste, g.run, g.endPos = false, 0, 0
				breaks++
			}
		case g.startPos == len(pasteStart):
			g.paste, g.run, g.startPos, g.endPos = true, 0, 0, 0
		case c <= ' ' || c == 0x7f:
			// A space or a control byte ends a run of runes.
			g.run = 0
			breaks++
		default:
			g.run++
		}
		if g.run > g.maxRun {
			if g.paste {
				return breaks, "paste too long"
			}
			return breaks, "run of characters too long"
		}
	}
	return breaks, ""
}

// matchStep advances a match of the marker m by the byte c. The markers
// start with ESC and have no other ESC, so a mismatch can only restart at
// position 0 or 1.
func matchStep(pos int, c byte, m string) int {
	if pos < len(m) && c == m[pos] {
		return pos + 1
	}
	if c == m[0] {
		return 1
	}
	return 0
}
