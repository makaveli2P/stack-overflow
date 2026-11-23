package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// fixtureHarness is newHarness with the keys of the fixture pressed.
func fixtureHarness(t *testing.T, name string) *harness {
	t.Helper()
	f := fixture(t, name)
	h := newHarness(t, f)
	h.press(f.Keys...)
	h.sess.calls = nil
	return h
}

// startPanic sends the snapshot of a new panic. It does not run the
// commands, because the notice tick waits one second.
func (h *harness) startPanic() {
	h.t.Helper()
	s := h.m.snap
	s.PanicSeq++
	h.m.Update(snapshotMsg{s})
	if !h.m.panic.on {
		h.t.Fatal("a higher PanicSeq did not start the panic screen")
	}
}

// toDecoy ends the notice phase.
func (h *harness) toDecoy() {
	h.t.Helper()
	h.m.Update(panicNoticeDoneMsg{h.m.panic.gen})
	if !h.m.panic.decoy {
		h.t.Fatal("the notice tick did not start the decoy")
	}
}

func (h *harness) view() string { return ansi.Strip(h.m.View()) }

// inputText returns the text of every input of the model, so a test can
// see a change in any of them.
func (h *harness) inputText() string {
	return fmt.Sprint(string(h.m.chatBuf), h.m.forms, h.m.dash.yes, h.m.raise)
}

// F20 (the Cmd+H mapping of the README) is the panic key on every screen.
// Ctrl+H (0x08) is the panic key only when no text input has focus. In a
// text input, 0x08 deletes one character (B7-3): many terminals send it
// for Ctrl+Backspace, and a word delete in the chat must not hide every
// screen in the room.
func TestPanicKeyOnEveryScreen(t *testing.T) {
	cases := []struct {
		fixture string
		keys    []string
		mode    mode
		text    bool // a text input has focus
	}{
		{"heads-up", nil, modeTable, false},
		{"heads-up", []string{"r", "5"}, modeRaise, true},
		{"heads-up", []string{"?"}, modeTable, false},
		{"heads-up", []string{"l"}, modeConfirm, false},
		{"chat input", nil, modeChat, true},
		{"buy-in picker, bank larger", nil, modeSit, false},
		{"dashboard stats tab", nil, modeDash, false},
		{"dashboard settle up, admin types yes", nil, modeDash, true},
		{"welcome screen, new SSH key", nil, modeWelcome, true},
		{"link form from the welcome screen", []string{"x"}, modeLink, true},
		{"profile box (M)", nil, modeProfile, false},
		{"profile box rename", nil, modeRename, true},
	}
	for _, c := range cases {
		t.Run(c.fixture+strings.Join(c.keys, ""), func(t *testing.T) {
			h := fixtureHarness(t, c.fixture)
			h.press(c.keys...)
			h.sess.calls = nil
			if h.m.mode != c.mode && !(c.mode == modeWelcome && h.m.inWelcome()) {
				t.Fatalf("mode %d, want %d", h.m.mode, c.mode)
			}
			if h.m.textFocus() != c.text {
				t.Fatalf("textFocus %v, want %v", h.m.textFocus(), c.text)
			}
			before := h.inputText()
			h.press("f20")
			h.wantCalls("Panic")
			if h.m.mode != c.mode || h.inputText() != before {
				t.Errorf("F20 changed the mode (%d -> %d) or the input (%s -> %s)", c.mode, h.m.mode, before, h.inputText())
			}
			// Ctrl+U (Cmd+Delete in Ghostty) panics outside text inputs and
			// clears the line inside them.
			hu := fixtureHarness(t, c.fixture)
			hu.press(c.keys...)
			hu.sess.calls = nil
			hu.press("ctrl+u")
			if c.text {
				hu.wantCalls()
			} else {
				hu.wantCalls("Panic")
				if hu.m.mode != c.mode || hu.inputText() != before {
					t.Errorf("Ctrl+U changed the mode (%d -> %d) or the input", c.mode, hu.m.mode)
				}
			}
			h.sess.calls = nil
			h.press("ctrl+h")
			if !c.text {
				h.wantCalls("Panic")
				if h.m.mode != c.mode || h.inputText() != before {
					t.Errorf("Ctrl+H changed the mode (%d -> %d) or the input", c.mode, h.m.mode)
				}
				return
			}
			// In a text input, Ctrl+H is Backspace.
			h.wantCalls()
			if h.m.mode != c.mode {
				t.Errorf("Ctrl+H changed the mode from %d to %d", c.mode, h.m.mode)
			}
			after := h.inputText()
			h2 := fixtureHarness(t, c.fixture)
			h2.press(c.keys...)
			h2.press("backspace")
			if after == before || after != h2.inputText() {
				t.Errorf("Ctrl+H in a text input: %s -> %s, want %s (as Backspace)", before, after, h2.inputText())
			}
		})
	}
}

func TestPanicNoticeThenDecoy(t *testing.T) {
	h := fixtureHarness(t, "heads-up")
	h.press("ctrl+h")
	h.wantCalls("Panic")
	if h.m.panic.on {
		t.Fatal("the panic screen started before the room answered")
	}
	h.startPanic()
	v := h.view()
	if !strings.Contains(v, "🤫  Shh! Someone is coming!") || strings.Contains(v, "Priya") {
		t.Fatalf("notice screen:\n%s", v)
	}
	// An old tick does nothing.
	h.m.Update(panicNoticeDoneMsg{h.m.panic.gen - 1})
	if h.m.panic.decoy {
		t.Fatal("a stale tick started the decoy")
	}
	h.toDecoy()
	v = h.view()
	if !strings.Contains(v, "go test -v ./...") || strings.Contains(v, "Shh!") {
		t.Fatalf("decoy screen:\n%s", v)
	}
	// The decoy adds one line for each step.
	before := v
	_, cmd := h.m.Update(decoyStepMsg{h.m.panic.gen})
	if cmd == nil || h.m.panic.steps != 1 {
		t.Fatal("a decoy step did not schedule the next step")
	}
	if after := h.view(); after == before {
		t.Error("a decoy step did not move the screen")
	}
	// A new panic during the decoy does not start the notice again.
	h.startPanic()
	if !h.m.panic.decoy || h.m.panic.steps != 1 {
		t.Errorf("a new panic during the decoy restarted it: %+v", h.m.panic)
	}
}

func TestPanicBaseline(t *testing.T) {
	f := fixture(t, "heads-up")
	f.Snap.PanicSeq = 5
	h := newHarness(t, f)
	if h.m.panic.on {
		t.Fatal("the first snapshot started the panic screen")
	}
	h.m.Update(snapshotMsg{f.Snap})
	if h.m.panic.on {
		t.Fatal("the same PanicSeq started the panic screen")
	}
	f.Snap.PanicSeq = 6
	h.m.Update(snapshotMsg{f.Snap})
	if !h.m.panic.on {
		t.Fatal("a higher PanicSeq did not start the panic screen")
	}
}

func TestPanicExitKeyIsSwallowed(t *testing.T) {
	for _, phase := range []string{"notice", "decoy"} {
		t.Run(phase, func(t *testing.T) {
			h := fixtureHarness(t, "heads-up")
			h.startPanic()
			if phase == "decoy" {
				h.toDecoy()
			}
			// A panic key on the panic screen does nothing.
			h.press("ctrl+h", "f20")
			h.wantCalls()
			if !h.m.panic.on {
				t.Fatal("a panic key ended the panic screen")
			}
			// Mouse and focus events and a resize do not end it.
			h.m.Update(tea.MouseMsg{X: 3, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			h.m.Update(tea.BlurMsg{})
			h.m.Update(tea.FocusMsg{})
			h.m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
			if !h.m.panic.on {
				t.Fatal("a mouse, focus or resize event ended the panic screen")
			}
			if lines := strings.Split(h.m.View(), "\n"); len(lines) != 20 {
				t.Errorf("after a resize the panic screen has %d lines, want 20", len(lines))
			}
			// F ends the panic screen and does not fold.
			h.press("f")
			h.wantCalls()
			if h.m.panic.on {
				t.Fatal("F did not end the panic screen")
			}
			if v := h.view(); !strings.Contains(v, "Priya") || strings.Contains(v, "go test") {
				t.Fatalf("after the exit the table does not show:\n%s", v)
			}
			// Old ticks do nothing after the exit.
			h.m.Update(decoyStepMsg{h.m.panic.gen - 1})
			h.m.Update(panicNoticeDoneMsg{h.m.panic.gen - 1})
			if h.m.panic.on {
				t.Fatal("an old tick started the panic screen again")
			}
			// The keys work again.
			h.press("f")
			h.wantCalls("Act")
		})
	}
}

func TestPanicSwallowsTypedRunes(t *testing.T) {
	h := fixtureHarness(t, "heads-up")
	h.startPanic()
	h.toDecoy()
	h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("faa")})
	h.wantCalls()
	if h.m.panic.on || h.m.mode != modeTable || !h.m.allInAt.IsZero() {
		t.Fatalf("typed runes: panic %v mode %d all-in armed %v", h.m.panic.on, h.m.mode, !h.m.allInAt.IsZero())
	}
}

func TestNoAlertOnPanicScreen(t *testing.T) {
	h, log, snap := alertHarness(t)
	h.snapshot(snap)
	snap.PanicSeq = 1
	snap.Alert = "turn"
	h.snapshot(snap) // The panic starts with the cue in the same snapshot.
	if !h.m.panic.on {
		t.Fatal("no panic screen")
	}
	h.toDecoy()
	snap.Alert = "hurry"
	h.m.Update(tea.BlurMsg{})
	h.snapshot(snap)
	if got := log.take(); len(got) != 0 {
		t.Fatalf("the panic screen sent alerts: %+v", got)
	}
	h.m.Update(keyMsg("x"))
	h.snapshot(snap)
	if got := log.take(); len(got) != 1 {
		t.Fatalf("after the exit: %d alerts, want 1", len(got))
	}
}

func TestPanicTooSoonNotice(t *testing.T) {
	h := fixtureHarness(t, "heads-up")
	h.sess.err = server.ErrPanicTooSoon
	h.press("ctrl+h")
	if !strings.Contains(h.view(), "wait a moment") {
		t.Errorf("ErrPanicTooSoon does not show on the table:\n%s", h.view())
	}
	h.m.notice = notice{}

	// On the panic screen the error does not show, also after the exit.
	h.startPanic()
	h.m.Update(callDoneMsg{kind: callPanic, err: server.ErrPanicTooSoon})
	h.press("x")
	if h.m.notice.text != "" {
		t.Errorf("notice %q after the panic screen", h.m.notice.text)
	}
}

// decoyBanned are words that must not show on the decoy.
var decoyBanned = []string{"poker", "chip", "fold", "raise", "call", "bet", "pot", "hand", "card", "table",
	"stack overflow", "all-in", "blind", "flop", "river", "turn", "♠", "♥", "♦", "♣"}

func TestDecoyHasNoGameWords(t *testing.T) {
	for _, line := range decoyScript {
		low := strings.ToLower(line)
		for _, w := range decoyBanned {
			if strings.Contains(low, w) {
				t.Errorf("decoy line %q has %q", line, w)
			}
		}
	}
	// The rendered decoy has no player name and no game word at any size,
	// at any step of the loop.
	for _, name := range []string{"heads-up", "6 seats", "spectator", "chat input"} {
		f := fixture(t, name)
		var names []string
		for _, s := range f.Snap.Table.Seats {
			if s.Name != "" {
				names = append(names, strings.ToLower(s.Name))
			}
		}
		for _, c := range f.Snap.Chat {
			names = append(names, strings.ToLower(c.Name))
		}
		names = append(names, strings.ToLower(f.Snap.Me.Name))
		for _, sz := range [][2]int{{60, 20}, {80, 24}, {140, 40}} {
			h := newHarness(t, f)
			h.m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			h.startPanic()
			h.toDecoy()
			for step := 0; step <= len(decoyScript); step++ {
				v := strings.ToLower(h.m.View())
				for _, w := range append(slicesClone(decoyBanned), names...) {
					if strings.Contains(v, w) {
						t.Fatalf("%s %dx%d step %d: decoy shows %q:\n%s", name, sz[0], sz[1], step, w, v)
					}
				}
				h.m.Update(decoyStepMsg{h.m.panic.gen})
			}
		}
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

func TestPanicLayout(t *testing.T) {
	for _, sz := range [][2]int{{60, 20}, {80, 24}, {140, 40}} {
		w, hh := sz[0], sz[1]
		for _, name := range []string{"heads-up", "spectator"} {
			t.Run(fmt.Sprintf("%s/%dx%d", name, w, hh), func(t *testing.T) {
				h := fixtureHarness(t, name)
				h.m.Update(tea.WindowSizeMsg{Width: w, Height: hh})
				h.startPanic()
				check := func(phase string) []string {
					raw := h.m.View()
					if ansi.Strip(raw) != raw {
						t.Errorf("%s: the panic screen uses colours or styles", phase)
					}
					lines := strings.Split(raw, "\n")
					if len(lines) != hh {
						t.Errorf("%s: %d lines, want %d", phase, len(lines), hh)
					}
					for i, l := range lines {
						if lw := ansi.StringWidth(l); lw > w {
							t.Errorf("%s: line %d is %d cells wide", phase, i, lw)
						}
					}
					return lines
				}
				lines := check("notice")
				mid := lines[(hh-1)/2]
				if !strings.Contains(mid, "Shh! Someone is coming!") {
					t.Errorf("the notice is not in the middle line:\n%s", strings.Join(lines, "\n"))
				}
				h.toDecoy()
				lines = check("decoy")
				// The decoy fills the window: no empty line.
				for i, l := range lines {
					if strings.TrimSpace(l) == "" {
						t.Errorf("decoy line %d is empty", i)
					}
				}
				last := lines[len(lines)-1]
				wait := strings.HasPrefix(last, "--- WAIT  TestCheckoutFlow")
				if myTurn := name == "heads-up"; wait != myTurn {
					t.Errorf("WAIT line %v on my turn %v: %q", wait, myTurn, last)
				}
				if wait && !strings.HasSuffix(last, "(22s)") {
					t.Errorf("WAIT line %q lacks the seconds left", last)
				}
				// The seconds count down with the local clock.
				if wait {
					h.now = h.now.Add(2 * time.Second)
					lines = check("decoy later")
					if got := lines[len(lines)-1]; !strings.HasSuffix(got, "(20s)") {
						t.Errorf("WAIT line 2 s later: %q", got)
					}
				}
			})
		}
	}
}
