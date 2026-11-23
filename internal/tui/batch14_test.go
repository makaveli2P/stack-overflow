package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Batch 14: a shot goes to the table chat. Batch 15 removed the private
// rule: every shot goes to the chat (see batch15_test.go).

const (
	testOwnURL   = "http://127.0.0.1:2280/abcdefghjk"
	testTableURL = "http://192.168.1.20:2280/abcdefghjk"
	testOtherURL = "http://192.168.1.20:2280/zyxwvutsrq"
)

// shareHarness is a harness on the fixture name with a fake hook that
// gives the host links: Own on 127.0.0.1, Table on the office address.
func shareHarness(t *testing.T, name string) (*harness, *shotHook) {
	t.Helper()
	return shareHarnessSnap(t, fixture(t, name).Snap)
}

func shareHarnessSnap(t *testing.T, s server.Snapshot) (*harness, *shotHook) {
	t.Helper()
	h := newHarness(t, Fixture{Snap: s})
	f := &shotHook{url: testOwnURL, table: testTableURL}
	h.m.shot.hook = f.hook
	return h, f
}

// shareCalls returns the ShareShot calls of the fake session.
func (h *harness) shareCalls() []string {
	var out []string
	for _, c := range h.sess.callList() {
		if strings.HasPrefix(c, "ShareShot(") {
			out = append(out, c)
		}
	}
	return out
}

// A shared frame leaves out the notice line: it can hold the link of an
// earlier shot or other text for this viewer only. Batch 15: every frame,
// also in a hand.
func TestSharedFrameHasNoNotice(t *testing.T) {
	for _, name := range []string{"waiting to start, seated", "6 seats, flop, waiting"} {
		h, f := shareHarness(t, name)
		h.m.notice = h.m.linkNotice(testOtherURL, "Screenshot: "+testOtherURL+" · only for this test")
		if !strings.Contains(h.view(), testOtherURL) {
			t.Fatal("the test notice does not show")
		}
		h.finish(h.key("p"))
		if strings.Contains(f.frames[0].View, testOtherURL) || strings.Contains(f.frames[0].View, "only for this test") {
			t.Errorf("%s: the frame shows the notice:\n%s", name, stripANSI(f.frames[0].View))
		}
	}
}

// Without an office address the hook gives Table == Own; the chat gets
// that link.
func TestShareUsesTableLink(t *testing.T) {
	h, f := shareHarness(t, "waiting to start, seated")
	f.table = testOwnURL
	h.finish(h.key("p"))
	if got := h.shareCalls(); len(got) != 1 || got[0] != "ShareShot("+testOwnURL+")" {
		t.Errorf("share calls = %v", got)
	}
	// An empty Table falls back to Own.
	h, f = shareHarness(t, "waiting to start, seated")
	f.table = ""
	h.finish(h.key("p"))
	if got := h.shareCalls(); len(got) != 1 || got[0] != "ShareShot("+testOwnURL+")" {
		t.Errorf("share calls with no Table = %v", got)
	}
	// A hook error shares nothing.
	h, f = shareHarness(t, "waiting to start, seated")
	f.url, f.err = "", errors.New("the store is full")
	h.finish(h.key("p"))
	if got := h.shareCalls(); len(got) != 0 || h.m.notice.text != "Screenshot failed: the store is full" {
		t.Errorf("hook error: calls %v, notice %q", got, h.m.notice.text)
	}
}

// A ShareShot error keeps the link with the player.
func TestShareErrorKeepsTheLink(t *testing.T) {
	h, _ := shareHarness(t, "waiting to start, seated")
	h.sess.err = errors.New("too many messages: wait a few seconds")
	h.finish(h.key("p"))
	want := "Screenshot: " + testOwnURL + " (not shared: too many messages: wait a few seconds)"
	if h.m.notice.text != want || h.m.notice.url != testOwnURL {
		t.Errorf("notice = %q, want %q", h.m.notice.text, want)
	}
	if v := h.view(); !strings.Contains(v, testOwnURL) {
		t.Errorf("the link does not show:\n%s", v)
	}
	h.now = h.now.Add(59 * time.Second)
	h.tick()
	if !strings.Contains(h.view(), testOwnURL) {
		t.Error("the link is gone after 59 s")
	}
}

// The other viewers get the chat line and a notice with the link.
func TestSharedShotReachesOthers(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.say(server.ChatLine{At: h.now, Name: "Priya", Text: "shared a screenshot", Shot: testOtherURL})
	want := "Priya shared a screenshot: " + testOtherURL + " · Cmd+click to open"
	if h.m.notice.text != want || h.m.notice.url != testOtherURL {
		t.Fatalf("notice = %q, want %q", h.m.notice.text, want)
	}
	v := stripANSI(h.viewAt(120, 32))
	if !strings.Contains(v, want) {
		t.Errorf("the notice line does not show %q:\n%s", want, v)
	}
	// The chat panel: "<name> shared a screenshot", the link on the next
	// line, and the batch 11 highlight.
	lines := strings.Split(v, "\n")
	found := false
	for i, l := range lines {
		if strings.Contains(l, "Priya shared a screenshot") && !strings.Contains(l, "http") {
			found = true
			// The next line has the whole link, or a dim line that says
			// that it does not fit; never a cut link.
			next := ""
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			if !strings.Contains(next, testOtherURL) && !strings.Contains(next, shotLinkTooLong) {
				t.Errorf("no link or too-long line under the chat line:\n%s", v)
			}
			if strings.Contains(next, "…") {
				t.Errorf("a cut link under the chat line: %q", next)
			}
		}
	}
	if !found {
		t.Errorf("the chat panel lacks the shot line:\n%s", v)
	}
	if lit := litLines(h.viewAt(120, 32)); len(lit) != 2 {
		t.Errorf("highlighted lines = %d, want 2 (text and link):\n%s", len(lit), strings.Join(lit, "\n"))
	}
	// The notice shows for 60 seconds.
	h.now = h.now.Add(59 * time.Second)
	h.tick()
	if h.m.notice.url != testOtherURL {
		t.Error("the notice is gone after 59 s")
	}
	h.now = h.now.Add(2 * time.Second)
	h.tick()
	if h.m.notice.text != "" {
		t.Errorf("the notice still shows after 61 s: %q", h.m.notice.text)
	}
	// The chat line stays.
	if !strings.Contains(stripANSI(h.viewAt(120, 32)), "Priya shared a screenshot") {
		t.Error("the chat line is gone")
	}
}

func TestSharedShotNoNotice(t *testing.T) {
	shotLine := func(h *harness, name string) server.ChatLine {
		return server.ChatLine{At: h.now, Name: name, Text: "shared a screenshot", Shot: testOtherURL}
	}

	// The first snapshot of a connection is the baseline.
	s := fixture(t, "6 seats, flop, waiting").Snap
	s.Chat = append(s.Chat, server.ChatLine{At: s.Now, Name: "Priya", Text: "shared a screenshot", Shot: testOtherURL})
	h := newHarness(t, Fixture{Snap: s})
	if h.m.notice.text != "" {
		t.Errorf("first snapshot: notice %q", h.m.notice.text)
	}
	if !strings.Contains(stripANSI(h.viewAt(120, 32)), "Priya shared a screenshot") {
		t.Error("first snapshot: no chat line")
	}

	// The own line: the player has the own notice already.
	h = hlHarness(t, testRenderer())
	h.say(shotLine(h, "Devon"))
	if h.m.notice.text != "" {
		t.Errorf("own line: notice %q", h.m.notice.text)
	}

	// While the viewer types in the chat.
	h = hlHarness(t, testRenderer())
	h.press("t")
	h.say(shotLine(h, "Priya"))
	if h.m.notice.text != "" || h.m.mode != modeChat {
		t.Errorf("chat input: notice %q, mode %v", h.m.notice.text, h.m.mode)
	}

	// While the viewer types a new name.
	h = hlHarness(t, testRenderer())
	h.press("m", "r")
	if h.m.mode != modeRename {
		t.Fatalf("m r did not open the rename input (mode %v)", h.m.mode)
	}
	h.say(shotLine(h, "Priya"))
	if h.m.notice.text != "" {
		t.Errorf("rename input: notice %q", h.m.notice.text)
	}

	// On the panic screen, and after it: the chat line is enough.
	h = hlHarness(t, testRenderer())
	h.startPanic()
	h.say(shotLine(h, "Priya"))
	if strings.Contains(h.view(), testOtherURL) {
		t.Errorf("the panic screen shows the link:\n%s", h.view())
	}
	h.press("x")
	if h.m.panic.on {
		t.Fatal("a key did not end the panic screen")
	}
	if h.m.notice.url == testOtherURL {
		t.Error("the notice shows after the panic screen")
	}

	// A panic that starts in the same snapshot as the line.
	h = hlHarness(t, testRenderer())
	s = h.m.snap
	s.PanicSeq++
	s.Chat = append(s.Chat, shotLine(h, "Priya"))
	h.m.Update(snapshotMsg{s})
	if !h.m.panic.on || h.m.notice.url == testOtherURL {
		t.Errorf("panic with the line: panic %v, notice %q", h.m.panic.on, h.m.notice.text)
	}

	// An old line in a later snapshot is not new.
	h = hlHarness(t, testRenderer())
	h.say(shotLine(h, "Priya"))
	h.m.notice = notice{}
	h.say(h.line("Sam", "hi"))
	if h.m.notice.url != "" {
		t.Errorf("an old line gave the notice again: %q", h.m.notice.text)
	}
}

// The URL is never cut in any notice of batch 14 and batch 15. The words
// go first.
func TestShotNoticesKeepTheURL(t *testing.T) {
	setups := []struct {
		name string
		url  string
		hint string // the hint at 120 columns
		do   func(t *testing.T) *harness
	}{
		{"shared", testOwnURL, "Cmd+click", func(t *testing.T) *harness {
			h, _ := shareHarness(t, "waiting to start, seated")
			h.finish(h.key("p"))
			return h
		}},
		{"after the hand", testOwnURL, "goes to the chat when the hand ends", func(t *testing.T) *harness {
			h, _ := shareHarness(t, "6 seats, flop, waiting")
			h.sess.later = true
			h.finish(h.key("p"))
			return h
		}},
		{"after the hand, cards", testOwnURL, "goes to the chat when the hand ends", func(t *testing.T) *harness {
			h, _ := shareHarness(t, "folded in the hand, may store a show")
			h.sess.later = true
			h.finish(h.key("p"))
			return h
		}},
		{"not shared", testOwnURL, "not shared", func(t *testing.T) *harness {
			h, _ := shareHarness(t, "waiting to start, seated")
			h.sess.err = errors.New("too many messages: wait a few seconds")
			h.finish(h.key("p"))
			return h
		}},
		{"other viewer", testOtherURL, "Cmd+click", func(t *testing.T) *harness {
			h := hlHarness(t, testRenderer())
			h.say(server.ChatLine{At: h.now, Name: "Bartholomew-the-Long", Text: "shared a screenshot", Shot: testOtherURL})
			return h
		}},
	}
	for _, su := range setups {
		for _, sz := range [][2]int{{60, 20}, {80, 24}, {120, 32}} {
			h := su.do(t)
			view := h.viewAt(sz[0], sz[1])
			wantFits(t, su.name, view, sz[0], sz[1])
			var line string
			for _, l := range strings.Split(stripANSI(view), "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "›") && strings.Contains(l, "http") {
					line = strings.TrimRight(l, " ")
				}
			}
			if !strings.Contains(line, su.url) {
				t.Errorf("%s at %v: notice line %q does not hold %s", su.name, sz, line, su.url)
			}
			if sz[0] == 120 && !strings.Contains(line, su.hint) {
				t.Errorf("%s at 120 cols: notice line %q lacks %q", su.name, line, su.hint)
			}
		}
	}
}

// The chat panel never cuts a link: a narrow panel says that the link
// does not fit, in dim text. The text line stays.
func TestChatShotLineNarrow(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.m.snap.Chat = append(h.m.snap.Chat, server.ChatLine{At: h.now, Name: "Priya", Text: "shared a screenshot", Shot: testOtherURL})
	lines := h.m.chatLines(30)
	n := len(lines)
	text, link := stripANSI(lines[n-2]), stripANSI(lines[n-1])
	if strings.TrimSpace(text) != "Priya shared a screenshot" {
		t.Errorf("text line = %q", text)
	}
	// A cut link is never drawn: the terminal would open the cut text.
	if link != shotLinkTooLong || strings.Contains(link, "http") {
		t.Errorf("link line = %q, want %q and no part of the link", link, shotLinkTooLong)
	}
	for _, l := range lines {
		if w := len([]rune(stripANSI(l))); w > 30 {
			t.Errorf("line %q is %d cells wide", stripANSI(l), w)
		}
	}
	// A wide panel shows the whole link, with no indent.
	lines = h.m.chatLines(60)
	if got := stripANSI(lines[len(lines)-1]); got != testOtherURL {
		t.Errorf("wide link line = %q", got)
	}
}

// The help still fits 80x24 between hands, where P shares the shot.
func TestShotHelpBetweenHands(t *testing.T) {
	for _, name := range []string{"waiting to start, seated", "9 seats, showdown result", "folded after the hand, may show"} {
		h := newHarness(t, fixture(t, name))
		h.press("?")
		view := h.viewAt(80, 24)
		wantFits(t, name+" help", view, 80, 24)
		if v := stripANSI(view); !strings.Contains(v, "M N I P") || !strings.Contains(v, "Q / Ctrl+C") {
			t.Errorf("%s help at 80x24:\n%s", name, v)
		}
	}
}

// B14-1: the bank is text that only the viewer sees (no other snapshot
// holds Me.Bank). A shared frame leaves it out; the screen keeps it.
func TestSharedFrameHasNoBank(t *testing.T) {
	cases := []struct {
		fixture string
		screen  string // the bank text on the viewer's screen
	}{
		{"result with folded players' shown cards and the winner's, spectator", "(bank 2,000)"},
		{"waiting to start, spectator", "bank 0"},
		{"busted with a bank that covers the rebuy", "from bank"},
		{"busted with a bank that covers part of the rebuy", "300 from bank + 1,700 top-up"},
	}
	for _, c := range cases {
		for _, size := range [][2]int{{100, 30}, {170, 48}} {
			h, f := shareHarness(t, c.fixture)
			h.m.width, h.m.height = size[0], size[1]
			if v := h.view(); !strings.Contains(v, c.screen) {
				t.Fatalf("%s %v: the screen does not show %q:\n%s", c.fixture, size, c.screen, v)
			}
			h.finish(h.key("p"))
			if len(h.shareCalls()) != 1 {
				t.Fatalf("%s: share calls %v", c.fixture, h.shareCalls())
			}
			frame := stripANSI(f.frames[0].View)
			if strings.Contains(frame, "bank") || strings.Contains(frame, "top-up") || strings.Contains(frame, "you buy in for") {
				t.Errorf("%s %v: the shared frame shows the bank:\n%s", c.fixture, size, frame)
			}
			if v := h.view(); !strings.Contains(v, c.screen) {
				t.Errorf("%s %v: the screen lost %q after the shot:\n%s", c.fixture, size, c.screen, v)
			}
		}
	}
	// The status line of a shared frame still offers S, and the busted
	// line still offers B with the amount.
	h, f := shareHarness(t, "result with folded players' shown cards and the winner's, spectator")
	h.finish(h.key("p"))
	if frame := stripANSI(f.frames[0].View); !strings.Contains(frame, "You are watching.") || !strings.Contains(frame, "Sit down") {
		t.Errorf("the shared frame lost the status line:\n%s", frame)
	}
	h, f = shareHarness(t, "busted with a bank that covers part of the rebuy")
	h.finish(h.key("p"))
	if frame := stripANSI(f.frames[0].View); !strings.Contains(frame, "Buy back in for 2,000") {
		t.Errorf("the shared frame lost the rebuy line:\n%s", frame)
	}
}

// B14-1: the rabbit hunt offer "H rabbit hunt (N left)" tells the
// viewer's hunts left. A shared frame leaves the offer out.
func TestSharedFrameHasNoRabbitCount(t *testing.T) {
	s := fixture(t, "own folded hand shown after the hand").Snap
	s.Table.Result.CanRabbit = true
	s.Config.RabbitHunts = 3
	s.Me.RabbitsLeft = 1
	for _, size := range [][2]int{{100, 30}, {170, 48}} {
		h, f := shareHarnessSnap(t, s)
		h.m.width, h.m.height = size[0], size[1]
		if v := h.view(); !strings.Contains(v, "rabbit hunt (1 left)") {
			t.Fatalf("%v: the screen does not offer the hunt:\n%s", size, v)
		}
		h.finish(h.key("p"))
		if len(h.shareCalls()) != 1 {
			t.Fatalf("share calls %v", h.shareCalls())
		}
		frame := stripANSI(f.frames[0].View)
		if strings.Contains(frame, "rabbit hunt") || strings.Contains(frame, "left)") {
			t.Errorf("%v: the shared frame shows the rabbit hunt offer:\n%s", size, frame)
		}
		if v := h.view(); !strings.Contains(v, "rabbit hunt (1 left)") {
			t.Errorf("%v: the screen lost the offer after the shot:\n%s", size, v)
		}
	}
}

// B14-1: no fixture puts the bank or the count of rabbit hunts left in
// the frame of a shot. Batch 15: every shot goes to the chat, so every
// fixture counts, also a live hand.
func TestSharedFramesHaveNoViewerText(t *testing.T) {
	for _, fx := range Fixtures() {
		h, f := shareHarnessSnap(t, fx.Snap)
		if h.m.mode != modeTable {
			continue
		}
		h.m.width, h.m.height = 170, 48
		h.finish(h.key("p"))
		if f.calls() != 1 {
			continue
		}
		frame := stripANSI(f.frames[0].View)
		for _, bad := range []string{" · bank ", "(bank ", "from bank", "top-up", "you buy in for", " left)"} {
			if strings.Contains(frame, bad) {
				t.Errorf("%s: the shared frame shows %q:\n%s", fx.Name, bad, frame)
			}
		}
	}
}
