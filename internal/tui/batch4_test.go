package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// ---- batch 4: time extensions ----

// E and e use one extension on the viewer's own turn.
func TestExtendKeyOnTurn(t *testing.T) {
	for _, k := range []string{"E", "e"} {
		h := newHarness(t, fixture(t, "heads-up"))
		h.press(k)
		h.wantCalls("Extend")
		if h.m.mode != modeTable {
			t.Errorf("%s: mode = %v, want the table", k, h.m.mode)
		}
	}
	// More than one extension in the same turn is correct.
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("E", "E")
	h.wantCalls("Extend", "Extend")
}

// E in the amount editor uses an extension and keeps the typed amount.
func TestExtendKeyInEditor(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("r", "5", "0", "E")
	h.wantCalls("Extend")
	if h.m.mode != modeRaise {
		t.Fatalf("mode = %v, want the editor to stay open", h.m.mode)
	}
	if h.m.raise.amount != 50 {
		t.Errorf("amount = %d, want 50", h.m.raise.amount)
	}
}

// E is a typed letter in the chat input, on the welcome screen and in the
// rename field, also on the viewer's turn.
func TestExtendKeyTypesInInputs(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("t", "E", "e")
	h.wantCalls()
	if got := string(h.m.chatBuf); got != "Ee" {
		t.Errorf("chat = %q, want %q", got, "Ee")
	}

	f := edited(t, "welcome screen, new SSH key", func(s *server.Snapshot) {
		s.Table.Legal = &engine.Legal{Fold: true, Check: true}
		s.Deadline = s.Now.Add(20 * time.Second)
	})
	h = newHarness(t, f)
	if h.m.mode != modeWelcome {
		t.Fatalf("mode = %v, want the welcome screen", h.m.mode)
	}
	h.press("E")
	h.wantCalls()
	if !strings.Contains(h.m.forms.name.String(), "E") {
		t.Errorf("name field = %q, want the typed E", h.m.forms.name.String())
	}

	rename := fixture(t, "profile box rename")
	h = newHarness(t, rename)
	h.press(rename.Keys...)
	h.sess.calls = nil
	if h.m.mode != modeRename {
		t.Fatalf("mode = %v, want the rename field", h.m.mode)
	}
	h.m.snap.Table.Legal = &engine.Legal{Fold: true, Check: true}
	h.press("E")
	h.wantCalls()
}

// Outside the viewer's turn, E does not call the server and says why.
func TestExtendKeyNotOnTurn(t *testing.T) {
	for _, name := range []string{"6 seats, flop", "spectator", "busted"} {
		h := newHarness(t, fixture(t, name))
		h.press("E")
		h.wantCalls()
		if !h.m.notice.err || !strings.Contains(h.m.notice.text, "not your turn") {
			t.Errorf("%s: notice = %+v", name, h.m.notice)
		}
	}
	// The stats screen keeps its own keys.
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("d", "E")
	h.wantCalls()
}

// A server error shows on the notice line.
func TestExtendErrorShows(t *testing.T) {
	for _, err := range []error{server.ErrNoExtensions, server.ErrCannotExtend} {
		h := newHarness(t, fixture(t, "heads-up"))
		h.sess.err = err
		h.press("E")
		if v := stripANSI(h.viewAt(80, 24)); !strings.Contains(v, "✗ "+err.Error()) {
			t.Errorf("error %q is not on the screen\n%s", err, v)
		}
	}
}

// The E hint shows only on the viewer's turn, while the clock runs and
// extensions are left.
func TestExtendHint(t *testing.T) {
	cases := []struct {
		name   string
		change func(s *server.Snapshot)
		want   string // "" = no hint
	}{
		{"default", func(*server.Snapshot) {}, "E  +30s (3)"},
		{"one left", func(s *server.Snapshot) { s.Me.ExtensionsLeft = 1 }, "E  +30s (1)"},
		{"45 seconds", func(s *server.Snapshot) { s.Config.ExtensionTime = 45 * time.Second }, "E  +45s (3)"},
		{"none left", func(s *server.Snapshot) { s.Me.ExtensionsLeft = 0 }, ""},
		{"no clock", func(s *server.Snapshot) { s.Deadline = time.Time{} }, ""},
		{"feature off", func(s *server.Snapshot) { s.Config.Extensions, s.Config.ExtensionTime, s.Me.ExtensionsLeft = 0, 0, 0 }, ""},
	}
	for _, c := range cases {
		f := edited(t, "heads-up", c.change)
		for _, sz := range [][2]int{{80, 24}, {100, 30}, {140, 40}} {
			bar := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
			has := strings.Contains(bar, "+30s") || strings.Contains(bar, "+45s")
			switch {
			case c.want == "" && has:
				t.Errorf("%s %v: hint shows\n%s", c.name, sz, bar)
			case c.want != "" && !strings.Contains(bar, c.want):
				t.Errorf("%s %v: want %q\n%s", c.name, sz, c.want, bar)
			}
		}
	}
	// Not on another player's turn.
	if v := stripANSI(Render(fixture(t, "6 seats, flop"), testRenderer(), 140, 40)); strings.Contains(v, "+30s") {
		t.Errorf("hint shows on another player's turn\n%s", v)
	}
	// Not while an action is on its way to the server.
	h := newHarness(t, fixture(t, "heads-up"))
	h.m.acting = true
	if v := stripANSI(h.viewAt(140, 40)); strings.Contains(v, "+30s") {
		t.Errorf("hint shows while sending\n%s", v)
	}
}

// The hint never pushes the action bar past the terminal width, and it is
// never cut: it shows whole or not at all.
func TestExtendHintFitsBar(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		me := &s.Table.Seats[s.Table.MySeat]
		me.Stack = 1234567
		*s.Table.Legal = engine.Legal{Fold: true, Call: true, CallAmount: 123456, Raise: true, MinTo: 246912, MaxTo: 1234567, AllIn: true, AllInTo: 1234567}
		s.Me.ExtensionsLeft = 10
	})
	for _, fx := range []Fixture{f, fixture(t, "heads-up"), fixture(t, "to bet"), fixture(t, "after one time extension")} {
		for w := 60; w <= 140; w++ {
			h := newHarness(t, fx)
			h.m.width = w
			bar := h.m.actionBar(w)
			plain := stripANSI(bar)
			// A bar without the hint can be too wide already; the footer
			// cuts it as before. The hint must never make it too wide.
			if bw := lipgloss.Width(bar); bw > w && strings.Contains(plain, "+30s") {
				t.Errorf("%s: bar with the hint is %d cells at width %d", fx.Name, bw, w)
			}
			if strings.Contains(plain, " E ") && !strings.Contains(plain, "+30s") {
				t.Errorf("%s: cut hint at width %d: %q", fx.Name, w, plain)
			}
		}
	}
	// At 80 columns the usual turn bars keep the full hint.
	for _, name := range []string{"heads-up", "to bet", "after one time extension"} {
		h := newHarness(t, fixture(t, name))
		if bar := stripANSI(h.m.actionBar(80)); !strings.Contains(bar, "E  +30s (") {
			t.Errorf("%s: no full hint at 80 columns: %q", name, bar)
		}
	}
}

// The timer bar measures the time left against ClockTotal, and falls back
// to TurnTimeout when ClockTotal is zero.
func TestTimerBarUsesClockTotal(t *testing.T) {
	cases := []struct {
		total, left time.Duration
		empty       int // cells of the bar that are not filled
	}{
		{60 * time.Second, 52 * time.Second, 3}, // label " 52s": bar 20, filled 17
		{60 * time.Second, 60 * time.Second, 0},
		{0, 15 * time.Second, 10},                 // TurnTimeout 30 s: filled 10 of 20
		{60 * time.Second, 100 * time.Second, 0},  // more than the total: full, no overflow
		{90 * time.Second, 5 * time.Second, 19},   // label "  5s": filled 1 of 20
		{0, 45 * time.Second, 0},                  // more than TurnTimeout
		{120 * time.Second, 119 * time.Second, 0}, //
	}
	for _, c := range cases {
		f := edited(t, "heads-up", func(s *server.Snapshot) {
			s.ClockTotal = c.total
			s.Deadline = s.Now.Add(c.left)
		})
		h := newHarness(t, f)
		bar := h.m.timerBar(24)
		if w := lipgloss.Width(bar); w != 24 {
			t.Errorf("%v of %v: bar is %d cells, want 24: %q", c.left, c.total, w, stripANSI(bar))
		}
		plain := stripANSI(bar)
		label := strings.TrimLeft(plain, "━")
		if n := strings.Count(plain, "━"); n != 24-len(label) {
			t.Errorf("%v of %v: bad bar %q", c.left, c.total, plain)
		}
		faint := h.m.st.faint.Render("━")
		faint = faint[:strings.Index(faint, "━")] // the style prefix
		if c.empty == 0 {
			if faint != "" && strings.Contains(bar, faint+"━") {
				t.Errorf("%v of %v: want a full bar: %q", c.left, c.total, bar)
			}
		} else if !strings.Contains(bar, h.m.st.faint.Render(strings.Repeat("━", c.empty))) {
			t.Errorf("%v of %v: want %d empty cells: %q", c.left, c.total, c.empty, bar)
		}
	}
	// The seat box timer uses the same total.
	v := stripANSI(Render(fixture(t, "after one time extension"), testRenderer(), 80, 24))
	if !strings.Contains(v, " 52s") {
		t.Errorf("seat timer lacks 52s\n%s", v)
	}
}

// The help names E with the configured time, and only when the feature is on.
func TestExtendHelp(t *testing.T) {
	for _, sz := range allSizes {
		h := newHarness(t, fixture(t, "heads-up"))
		h.press("?")
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		if !strings.Contains(v, "add 30s to your clock") {
			t.Errorf("help at %v lacks E\n%s", sz, v)
		}
		wantFits(t, "help", h.viewAt(sz[0], sz[1]), sz[0], sz[1])
	}
	f := edited(t, "heads-up", func(s *server.Snapshot) { s.Config.Extensions, s.Config.ExtensionTime = 0, 0 })
	h := newHarness(t, f)
	h.press("?")
	if v := stripANSI(h.viewAt(100, 30)); strings.Contains(v, "to your clock") {
		t.Errorf("help names E while the feature is off\n%s", v)
	}
}

// Every batch 4 state fits in every terminal size.
func TestBatch4StatesFit(t *testing.T) {
	states := []struct {
		fixture string
		keys    []string
	}{
		{"after one time extension", nil},
		{"after one time extension", []string{"E"}},
		{"after one time extension", []string{"?"}},
		{"amount editor with time extensions", []string{"E"}},
		{"no time extensions left", []string{"E"}},
		{"heads-up", []string{"t", "E"}},
	}
	for _, s := range states {
		for _, sz := range append(allSizes, [2]int{60, 18}, [2]int{90, 26}, [2]int{30, 8}) {
			h := newHarness(t, fixture(t, s.fixture))
			h.sess.err = server.ErrNoExtensions
			h.press(s.keys...)
			wantFits(t, fmt.Sprintf("%s %v", s.fixture, s.keys), h.viewAt(sz[0], sz[1]), sz[0], sz[1])
		}
	}
}

// A clock of 10000 s or more gives a seconds label of 7 cells or more. The
// timer bar must not panic when that label does not leave a cell for the
// bar, and it must stay in its width (regression: "negative Repeat count").
func TestTimerBarLongLabel(t *testing.T) {
	for _, left := range []time.Duration{3 * time.Hour, 30 * time.Hour} {
		f := edited(t, "heads-up", func(s *server.Snapshot) {
			s.ClockTotal = left + time.Minute
			s.Deadline = s.Now.Add(left)
		})
		h := newHarness(t, f)
		for w := 0; w <= 30; w++ {
			bar := h.m.timerBar(w)
			if bw := lipgloss.Width(bar); bw > w {
				t.Errorf("%v left: timerBar(%d) is %d cells: %q", left, w, bw, stripANSI(bar))
			}
		}
		for w := 60; w <= 140; w++ {
			wantFits(t, fmt.Sprintf("%v left", left), Render(f, testRenderer(), w, 24), w, 24)
		}
	}
}

// An extension time that is not a whole number of seconds shows in the hint
// and the help as the server log writes it, not rounded up.
func TestExtendHintSubSecond(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) { s.Config.ExtensionTime = 1500 * time.Millisecond })
	v := stripANSI(Render(f, testRenderer(), 140, 40))
	if !strings.Contains(v, "E  +1.5s (3)") || strings.Contains(v, "+2s") {
		t.Errorf("want the hint E  +1.5s (3)\n%s", v)
	}
	h := newHarness(t, f)
	h.press("?")
	if v := stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "add 1.5s to your clock") {
		t.Errorf("help lacks 1.5s\n%s", v)
	}
	for d, want := range map[time.Duration]string{30 * time.Second: "30s", 1500 * time.Millisecond: "1.5s", 250 * time.Millisecond: "0.25s", 90 * time.Second: "90s"} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%v) = %q, want %q", d, got, want)
		}
	}
}
