package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Batch 11: a new chat message from another player is highlighted for
// 5 seconds.

var batch11Sizes = [][2]int{{60, 20}, {80, 24}, {120, 30}, {162, 51}, {188, 55}}

// hlBgDark is the SGR code of the highlight background in a dark terminal.
const hlBgDark = "48;2;59;50;20"

// hlHarness is a harness on the 6-seat fixture (Devon's view, chat tab
// open) with the renderer r.
func hlHarness(t *testing.T, r *lipgloss.Renderer) *harness {
	t.Helper()
	f := fixture(t, "6 seats, flop, waiting")
	h := &harness{t: t, sess: newFake(), now: f.Snap.Now}
	h.m = newModelWith(h.sess, r, 100, 30, func() time.Time { return h.now })
	h.m.Update(snapshotMsg{f.Snap})
	return h
}

func profileRenderer(p termenv.Profile, dark bool) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(p)
	r.SetHasDarkBackground(dark)
	return r
}

// say sends a snapshot with new chat lines and returns nothing: the test
// does not wait for the highlight tick.
func (h *harness) say(lines ...server.ChatLine) {
	h.t.Helper()
	s := h.m.snap
	s.Chat = slices.Concat(s.Chat, lines)
	h.m.Update(snapshotMsg{s})
}

func (h *harness) line(name, text string) server.ChatLine {
	return server.ChatLine{At: h.now, Name: name, Text: text}
}

// litLines returns the screen lines that show the highlight marker.
func litLines(view string) []string {
	var out []string
	for _, l := range strings.Split(stripANSI(view), "\n") {
		if strings.Contains(l, chatMarker) {
			out = append(out, l)
		}
	}
	return out
}

// hasPanel reports whether the view shows the chat, log and players panel.
// At 60x20 the table takes the whole screen.
func hasPanel(view string) bool {
	return strings.Contains(stripANSI(view), "Players")
}

// The first snapshot is the baseline. A new message from another player
// gets the highlight, with a tick for its end; an own message does not.
func TestChatHLOtherNotOwn(t *testing.T) {
	h := hlHarness(t, testRenderer())
	if v := h.viewAt(100, 30); len(litLines(v)) != 0 || strings.Contains(v, hlBgDark) {
		t.Fatalf("the first snapshot has a highlight\n%s", stripANSI(v))
	}
	if h.m.hl.until != nil || !h.m.hl.tickAt.IsZero() {
		t.Fatalf("the first snapshot started a highlight: %+v", h.m.hl)
	}

	h.say(h.line("Devon", "my own words"))
	if v := h.viewAt(100, 30); len(litLines(v)) != 0 || !h.m.hl.tickAt.IsZero() {
		t.Fatalf("an own message has a highlight\n%s", stripANSI(v))
	}

	h.say(h.line("Priya", "hello there"))
	v := h.viewAt(100, 30)
	lit := litLines(v)
	if len(lit) != 1 || !strings.Contains(lit[0], "Priya: hello there") {
		t.Fatalf("lit lines %q\n%s", lit, stripANSI(v))
	}
	if !strings.Contains(v, hlBgDark) {
		t.Errorf("no highlight background in the view")
	}
	if want := h.now.Add(chatHLFor); !h.m.hl.tickAt.Equal(want) {
		t.Errorf("tick at %v, want %v", h.m.hl.tickAt, want)
	}
}

// The tick ends the highlight after 5 seconds. A key press, a snapshot
// and an old tick do not change the end.
func TestChatHLEndsByTick(t *testing.T) {
	h := hlHarness(t, testRenderer())
	t0 := h.now
	h.say(h.line("Priya", "first"))
	gen := h.m.hl.gen

	h.now = t0.Add(2 * time.Second)
	h.say(h.line("Sam", "second"))
	if h.m.hl.gen != gen {
		t.Errorf("a second message started a second tick: the first tick comes in time")
	}
	h.press("tab", "tab", "tab") // back to the chat tab
	h.m.Update(snapshotMsg{h.m.snap})
	if got := h.m.hl.until; len(got) == 0 || !got[len(got)-2].Equal(t0.Add(chatHLFor)) {
		t.Fatalf("keys or a snapshot changed the end: %v", got)
	}

	// An old tick does nothing.
	h.now = t0.Add(5 * time.Second)
	if _, cmd := h.m.Update(chatHLMsg{gen - 1}); cmd != nil || h.m.hl.tickAt.IsZero() {
		t.Errorf("an old tick did something")
	}

	// The first tick ends "first" and starts a tick for "second".
	_, cmd := h.m.Update(chatHLMsg{gen})
	if cmd == nil || !h.m.hl.tickAt.Equal(t0.Add(2*time.Second+chatHLFor)) {
		t.Fatalf("no tick for the second end: %+v", h.m.hl)
	}
	lit := litLines(h.viewAt(100, 30))
	if len(lit) != 1 || !strings.Contains(lit[0], "Sam: second") {
		t.Fatalf("lit lines after the first end: %q", lit)
	}

	h.now = t0.Add(7 * time.Second)
	if _, cmd := h.m.Update(chatHLMsg{h.m.hl.gen}); cmd != nil {
		t.Errorf("a tick started with no highlight on")
	}
	if v := h.viewAt(100, 30); len(litLines(v)) != 0 || strings.Contains(v, hlBgDark) {
		t.Errorf("the highlight stays after 5 seconds\n%s", stripANSI(v))
	}
	if h.m.hl.until != nil || !h.m.hl.tickAt.IsZero() {
		t.Errorf("state after the end: %+v", h.m.hl)
	}
}

// Every wrapped line of a long message gets the marker and the
// background, in full and compact mode.
func TestChatHLWrapped(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.say(newChatLine)
	for _, sz := range [][2]int{{120, 30}, {80, 24}} {
		v := h.viewAt(sz[0], sz[1])
		lit := litLines(v)
		if len(lit) < 2 {
			t.Fatalf("at %v the message has %d lit lines\n%s", sz, len(lit), stripANSI(v))
		}
		if !strings.Contains(lit[0], "Priya: ") || !strings.Contains(lit[len(lit)-1], "shows.") {
			t.Errorf("at %v lit lines %q", sz, lit)
		}
		raw := strings.Split(v, "\n")
		n := 0
		for _, l := range raw {
			if strings.Contains(l, chatMarker) {
				n++
				if !strings.Contains(l, hlBgDark) {
					t.Errorf("at %v a lit line has no background: %q", sz, l)
				}
			}
		}
		if n != len(lit) {
			t.Errorf("at %v: %d raw lit lines, %d plain", sz, n, len(lit))
		}
	}
}

// The highlight never makes a line too wide, in each colour profile, on
// the chat tab and the log tab.
func TestChatHLFits(t *testing.T) {
	renderers := map[string]*lipgloss.Renderer{
		"dark":  profileRenderer(termenv.TrueColor, true),
		"light": profileRenderer(termenv.TrueColor, false),
		"none":  profileRenderer(termenv.Ascii, true),
	}
	for rn, r := range renderers {
		for _, keys := range [][]string{nil, {"tab"}} {
			h := hlHarness(t, r)
			h.press(keys...)
			h.say(newChatLine, h.line("Sam", strings.Repeat("word ", 60)))
			for _, sz := range batch11Sizes {
				view := h.viewAt(sz[0], sz[1])
				name := fmt.Sprintf("%s %v", rn, keys)
				wantFits(t, name, view, sz[0], sz[1])
				if n := len(strings.Split(view, "\n")); n != sz[1] {
					t.Errorf("%s at %v: %d lines", name, sz, n)
				}
				if keys == nil && hasPanel(view) && len(litLines(view)) == 0 {
					t.Errorf("%s at %v: no lit line\n%s", name, sz, stripANSI(view))
				}
			}
		}
	}
}

// Light and no-colour terminals get their own highlight.
func TestChatHLProfiles(t *testing.T) {
	h := hlHarness(t, profileRenderer(termenv.TrueColor, false))
	h.say(h.line("Priya", "hi"))
	if v := h.viewAt(100, 30); !strings.Contains(v, "48;2;255;248;197") {
		t.Errorf("no light background\n%q", v)
	}
	h = hlHarness(t, profileRenderer(termenv.Ascii, true))
	h.say(h.line("Priya", "hi"))
	v := h.viewAt(100, 30)
	if !strings.Contains(v, sgrBold+chatMarker) || strings.Contains(v, "48;") {
		t.Errorf("no-colour highlight is not bold with the marker\n%q", v)
	}
	h.press("tab")
	if v := h.viewAt(100, 30); !strings.Contains(v, bold("Chat")) {
		t.Errorf("no-colour tab name is not bold\n%q", v)
	}
}

// With the chat tab closed, the tab name "Chat" has the highlight style
// for the same 5 seconds, next to the unread dot.
func TestChatHLTabName(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.press("tab") // the log tab
	t0 := h.now
	h.say(h.line("Priya", "look at the chat"))
	litTab := h.m.st.tabLit.Render("Chat")
	v := h.viewAt(100, 30)
	if !strings.Contains(v, litTab) || !strings.Contains(stripANSI(v), "Chat•") {
		t.Fatalf("the closed chat tab has no highlight\n%q", v)
	}
	h.press("tab") // the players tab
	if !strings.Contains(h.viewAt(80, 24), litTab) {
		t.Errorf("compact: the closed chat tab has no highlight")
	}

	h.now = t0.Add(chatHLFor)
	h.m.Update(chatHLMsg{h.m.hl.gen})
	v = h.viewAt(100, 30)
	if strings.Contains(v, litTab) || !strings.Contains(stripANSI(v), "Chat•") {
		t.Errorf("after 5 seconds: want the unread dot and no highlight\n%s", stripANSI(v))
	}

	// With the chat tab open, the tab name has its usual style.
	h = hlHarness(t, testRenderer())
	h.say(h.line("Priya", "hi"))
	if strings.Contains(h.viewAt(100, 30), litTab) {
		t.Errorf("the open chat tab has the highlight style")
	}
}

// The chat keeps only the newest lines. When the cap drops old lines, no
// old line gets a highlight again, and a highlight stays on its message.
func TestChatHLCap(t *testing.T) {
	for _, limit := range []int{100, 200} {
		f := fixture(t, "6 seats, flop, waiting")
		s := f.Snap
		s.Chat = nil
		for i := range limit {
			s.Chat = append(s.Chat, server.ChatLine{At: s.Now.Add(time.Duration(i-limit) * time.Second), Name: "Sam", Text: "same text"})
		}
		f.Snap = s
		h := &harness{t: t, sess: newFake(), now: s.Now}
		h.m = newModelWith(h.sess, testRenderer(), 100, 30, func() time.Time { return h.now })
		h.m.Update(snapshotMsg{s})

		next := func(add server.ChatLine) {
			s := h.m.snap
			s.Chat = append(slices.Clone(s.Chat[1:]), add)
			h.m.Update(snapshotMsg{s})
		}
		lit := func() []int {
			var out []int
			for i := range h.m.snap.Chat {
				if h.m.chatLit(i, h.now) {
					out = append(out, i)
				}
			}
			return out
		}
		next(h.line("Priya", "new one"))
		if got := lit(); !slices.Equal(got, []int{limit - 1}) {
			t.Fatalf("cap %d: lit %v, want [%d]", limit, got, limit-1)
		}
		h.now = h.now.Add(time.Second)
		next(h.line("Devon", "own"))
		if got := lit(); !slices.Equal(got, []int{limit - 2}) {
			t.Fatalf("cap %d after a drop: lit %v, want [%d]", limit, got, limit-2)
		}
		// The same text again, from the same player, is a new message.
		h.now = h.now.Add(time.Second)
		next(h.line("Priya", "new one"))
		if got := lit(); !slices.Equal(got, []int{limit - 3, limit - 1}) {
			t.Fatalf("cap %d, repeat text: lit %v", limit, got)
		}
		// A chat with no line in common: only lines after the newest old
		// line are new.
		s2 := h.m.snap
		s2.Chat = []server.ChatLine{{At: s.Now.Add(-time.Hour), Name: "Leo", Text: "old"}}
		h.m.Update(snapshotMsg{s2})
		if got := lit(); len(got) != 0 {
			t.Errorf("cap %d: an old line got a highlight: %v", limit, got)
		}
	}
}

// The panic screen shows no highlight. A message that comes during the
// panic shows the rest of its 5 seconds after the exit, if any is left.
func TestChatHLPanic(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.say(h.line("Priya", "before"))
	h.startPanic()
	for _, v := range []string{h.m.View(), func() string { h.toDecoy(); return h.m.View() }()} {
		if strings.Contains(v, chatMarker) || strings.Contains(v, hlBgDark) {
			t.Fatalf("the panic screen shows a highlight\n%q", v)
		}
	}

	// A message during the panic starts no tick.
	h.now = h.now.Add(chatHLFor) // "before" ends
	h.m.Update(chatHLMsg{h.m.hl.gen})
	t1 := h.now
	h.say(h.line("Sam", "during"))
	if !h.m.hl.tickAt.IsZero() {
		t.Errorf("a tick started on the panic screen")
	}
	if strings.Contains(h.m.View(), hlBgDark) {
		t.Errorf("the panic screen shows a highlight")
	}

	// The exit 2 seconds later: the rest of the 5 seconds shows.
	h.now = t1.Add(2 * time.Second)
	if _, cmd := h.m.Update(keyMsg("x")); cmd == nil {
		t.Fatal("the exit returned no command")
	}
	if want := t1.Add(chatHLFor); !h.m.hl.tickAt.Equal(want) {
		t.Errorf("tick at %v, want %v", h.m.hl.tickAt, want)
	}
	lit := litLines(h.viewAt(100, 30))
	if len(lit) != 1 || !strings.Contains(lit[0], "Sam: during") {
		t.Errorf("lit lines after the exit: %q", lit)
	}

	// The exit after the 5 seconds: no highlight and no tick.
	h = hlHarness(t, testRenderer())
	h.startPanic()
	h.say(h.line("Sam", "during"))
	h.now = h.now.Add(6 * time.Second)
	h.m.Update(keyMsg("x"))
	if len(litLines(h.viewAt(100, 30))) != 0 || !h.m.hl.tickAt.IsZero() {
		t.Errorf("a late exit shows a highlight or starts a tick: %+v", h.m.hl)
	}
}

// With no highlight on, nothing starts a tick.
func TestChatHLIdle(t *testing.T) {
	h := hlHarness(t, testRenderer())
	gen := h.m.hl.gen
	for range 3 {
		h.now = h.now.Add(time.Second)
		h.m.Update(snapshotMsg{h.m.snap})
		h.press("tab")
	}
	h.m.Update(tickMsg(h.now))
	if h.m.hl.gen != gen || !h.m.hl.tickAt.IsZero() || h.m.hl.until != nil {
		t.Errorf("an idle screen started a highlight tick: %+v", h.m.hl)
	}
	if cmd := h.m.chatHLTick(); cmd != nil {
		t.Errorf("chatHLTick with no highlight returned a command")
	}
}

// The preview fixtures of batch 11 show the highlight.
func TestChatHLFixtures(t *testing.T) {
	r := testRenderer()
	for _, sz := range batch11Sizes {
		v := Render(fixture(t, "new chat message highlighted"), r, sz[0], sz[1])
		if !hasPanel(v) {
			continue
		}
		if len(litLines(v)) < 2 {
			t.Errorf("at %v: the fixture shows no wrapped highlight\n%s", sz, stripANSI(v))
		}
		f := fixture(t, "chat tab highlighted, log tab open")
		v = Render(f, r, sz[0], sz[1])
		if !strings.Contains(v, newStyles(r).tabLit.Render("Chat")) || len(litLines(v)) != 0 {
			t.Errorf("at %v: the log tab fixture\n%s", sz, stripANSI(v))
		}
	}
}
