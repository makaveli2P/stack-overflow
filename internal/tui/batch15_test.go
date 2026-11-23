package tui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Batch 15: every screenshot goes to the chat. Between hands the server
// posts the link at once; during a hand it posts the link when the hand
// ends (ShareShot gives later).

// A shot in a live hand: ShareShot gets the table link, and the own
// notice says that the link goes to the chat when the hand ends.
func TestShotInHandWaits(t *testing.T) {
	for _, name := range []string{
		"6 seats, flop, waiting",
		"heads-up preflop, my turn",
		"run-out, I folded",
		"folded in the hand, may store a show",
		"spectator, 6 seats, flop",
	} {
		h, f := shareHarness(t, name)
		h.sess.later = true
		h.finish(h.key("p"))
		if f.calls() != 1 {
			t.Fatalf("%s: hook calls = %d", name, f.calls())
		}
		if got := h.shareCalls(); len(got) != 1 || got[0] != "ShareShot("+testTableURL+")" {
			t.Errorf("%s: share calls %v, want ShareShot(%s)", name, got, testTableURL)
		}
		want := "Screenshot: " + testOwnURL + " · goes to the chat when the hand ends"
		if h.m.notice.text != want || h.m.notice.err || h.m.notice.url != testOwnURL {
			t.Errorf("%s: notice %+v, want %q", name, h.m.notice, want)
		}
	}
}

// A shot with no hand: shared at once.
func TestShotNoHandSharedNow(t *testing.T) {
	for _, name := range []string{
		"waiting to start, seated",
		"waiting to start, spectator",
		"9 seats, showdown result",
		"folded after the hand, may show",
		"winner may show cards and hunt the rabbit",
		"rabbit hunt, would have had, winner",
		"busted, between hands",
	} {
		h, _ := shareHarness(t, name)
		h.finish(h.key("p"))
		if got := h.shareCalls(); len(got) != 1 || got[0] != "ShareShot("+testTableURL+")" {
			t.Errorf("%s: share calls %v", name, got)
		}
		want := "Screenshot shared in the chat: " + testOwnURL + " · Cmd+click to open"
		if h.m.notice.text != want || h.m.notice.err {
			t.Errorf("%s: notice %q, want %q", name, h.m.notice.text, want)
		}
	}
}

// The frame of a shot in a hand shows the own hole cards: the player
// chose to share them after the hand. It still leaves out the bank.
func TestShotInHandFrame(t *testing.T) {
	h, f := shareHarness(t, "6 seats, flop, waiting")
	h.sess.later = true
	screen := h.view()
	h.finish(h.key("p"))
	frame := stripANSI(f.frames[0].View)
	for _, c := range []string{"Q♠", "Q♦"} {
		if !strings.Contains(screen, c) {
			t.Fatalf("the screen lacks the own card %s:\n%s", c, screen)
		}
		if !strings.Contains(frame, c) {
			t.Errorf("the frame lacks the own card %s:\n%s", c, frame)
		}
	}
	if f.frames[0].Title != "Stack Overflow — Devon · hand #42" {
		t.Errorf("title = %q", f.frames[0].Title)
	}
}

// A ShareShot error keeps the link, also for ErrShotsWaiting.
func TestShotWaitingCapNotice(t *testing.T) {
	h, _ := shareHarness(t, "6 seats, flop, waiting")
	h.sess.err = server.ErrShotsWaiting
	h.finish(h.key("p"))
	want := "Screenshot: " + testOwnURL + " (not shared: " + errText(server.ErrShotsWaiting) + ")"
	if h.m.notice.text != want || h.m.notice.url != testOwnURL {
		t.Errorf("notice = %q, want %q", h.m.notice.text, want)
	}
	if !errors.Is(h.sess.err, server.ErrShotsWaiting) {
		t.Fatal("test setup")
	}
}

// The full flow: Devon presses P during the hand and gets the own notice.
// The snapshot after the hand holds the line: another viewer gets the
// 60 s notice, Devon does not get a second one.
func TestShotPostedAtHandEnd(t *testing.T) {
	h, _ := shareHarness(t, "6 seats, flop, waiting")
	h.sess.later = true
	h.finish(h.key("p"))
	own := h.m.notice
	if !strings.Contains(own.text, "goes to the chat when the hand ends") {
		t.Fatalf("own notice = %q", own.text)
	}

	other := hlHarness(t, testRenderer())
	other.m.snap.Me.Name = "Priya" // another viewer of the same table
	other.m.notice = notice{}

	// The snapshot of the hand end: no hand, the result, and the line.
	line := server.ChatLine{At: h.now, Name: "Devon", Text: "shared a screenshot", Shot: testTableURL}
	end := func(s server.Snapshot) server.Snapshot {
		s.Table.InHand = false
		s.Chat = append(append([]server.ChatLine(nil), s.Chat...), line)
		return s
	}
	other.m.Update(snapshotMsg{end(other.m.snap)})
	want := "Devon shared a screenshot: " + testTableURL + " · Cmd+click to open"
	if other.m.notice.text != want || other.m.notice.url != testTableURL {
		t.Errorf("other viewer: notice %q, want %q", other.m.notice.text, want)
	}
	if v := stripANSI(other.viewAt(120, 32)); !strings.Contains(v, want) {
		t.Errorf("other viewer: the notice line lacks %q:\n%s", want, v)
	}
	if lit := litLines(other.viewAt(160, 40)); len(lit) != 2 {
		t.Errorf("other viewer: %d highlighted lines, want 2 (text and link)", len(lit))
	}

	h.m.Update(snapshotMsg{end(h.m.snap)})
	if h.m.notice.url != testOwnURL || h.m.notice.text != own.text {
		t.Errorf("own viewer: notice changed to %q at the hand end", h.m.notice.text)
	}
}

// The notice of a waiting link holds the whole URL at 60, 80 and 120
// columns.
func TestShotLaterNoticeKeepsURL(t *testing.T) {
	for _, sz := range [][2]int{{60, 20}, {80, 24}, {120, 32}} {
		h, _ := shareHarness(t, "6 seats, flop, waiting")
		h.sess.later = true
		h.finish(h.key("p"))
		view := h.viewAt(sz[0], sz[1])
		wantFits(t, "later notice", view, sz[0], sz[1])
		var line string
		for _, l := range strings.Split(stripANSI(view), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "›") && strings.Contains(l, "http") {
				line = strings.TrimRight(l, " ")
			}
		}
		if !strings.Contains(line, testOwnURL) {
			t.Errorf("%v: notice line %q lacks %s", sz, line, testOwnURL)
		}
		if sz[0] >= 80 && !strings.Contains(line, "goes to the chat when the hand ends") {
			t.Errorf("%v: notice line %q lacks the hint", sz, line)
		}
	}
}

// V is not a key any more.
func TestNoVKey(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.m.notice = notice{}
	before := h.m.View()
	h.press("v")
	if h.m.notice.text != "" || h.m.View() != before {
		t.Errorf("V did something: notice %q", h.m.notice.text)
	}
}

// The help row is back to batch 13, and it fits 80x24.
func TestShotHelpBatch15(t *testing.T) {
	for _, name := range []string{"6 seats, flop", "waiting to start, seated"} {
		h := newHarness(t, fixture(t, name))
		h.press("?")
		view := h.viewAt(80, 24)
		wantFits(t, name+" help", view, 80, 24)
		v := stripANSI(view)
		if !strings.Contains(v, "M N I P     profile · alerts on/off · hand rankings · screenshot") {
			t.Errorf("%s help at 80x24:\n%s", name, v)
		}
	}
}

// chatPanelLink finds the screen line with the link of the shot line and
// returns the inner width of the panel on that line, and whether the
// link starts at the inner edge (no indent).
func chatPanelLink(t *testing.T, view, url string, boxed bool) (inner int, flush bool) {
	t.Helper()
	lines := strings.Split(stripANSI(view), "\n")
	for _, l := range lines {
		i := strings.Index(l, url)
		if i < 0 || strings.Contains(l, "›") {
			continue
		}
		if !boxed {
			// Compact mode: the panel has no border. Its width is the
			// width of its rule ("── Chat  Log  Players ───").
			for _, r := range lines {
				if strings.HasPrefix(r, "── ") && strings.Contains(r, "Players") {
					inner = ansi.StringWidth(r)
				}
			}
			lead := strings.TrimPrefix(l[:i], chatMarker)
			return inner, lead == ""
		}
		left := strings.LastIndex(l[:i], "│")
		right := strings.LastIndex(l, "│")
		if left < 0 || right <= i {
			t.Fatalf("no panel border around the link: %q", l)
		}
		body := l[left+len("│") : right]
		lead := strings.TrimPrefix(l[left+len("│"):i], " ")
		lead = strings.TrimPrefix(lead, chatMarker)
		return ansi.StringWidth(body) - 2, lead == ""
	}
	return -1, false
}

// The chat panel widths (batch 15 asks for them): full mode at 140, 160
// and 188 columns, and compact mode. The usual office link has 35
// characters and must fit at 160 columns and wider, also while the line
// is highlighted (the marker takes one cell).
func TestChatPanelWidths(t *testing.T) {
	const office = "http://192.168.1.20:2280/k3jq7xw2mf" // 35 characters
	if len(office) != 35 {
		t.Fatalf("office link has %d characters", len(office))
	}
	cases := []struct {
		w, h  int
		boxed bool
		inner int
	}{
		{140, 40, true, 38},
		{160, 40, true, 42},
		{188, 55, true, 42},
		{99, 40, false, 99},
		{80, 40, false, 80},
	}
	for _, c := range cases {
		for _, lit := range []bool{true, false} {
			h := hlHarness(t, testRenderer())
			h.m.snap.Me.Name = "Devon"
			line := server.ChatLine{At: h.now, Name: "Priya", Text: "shared a screenshot", Shot: office}
			if lit {
				h.say(line)
				h.m.notice = notice{} // only the panel holds the link
			} else {
				h.m.snap.Chat = append(h.m.snap.Chat, line)
			}
			view := h.viewAt(c.w, c.h)
			wantFits(t, "chat panel", view, c.w, c.h)
			inner, flush := chatPanelLink(t, view, office, c.boxed)
			if inner < 0 {
				t.Errorf("%dx%d lit %v: the link is not whole in the panel:\n%s", c.w, c.h, lit, stripANSI(view))
				continue
			}
			if inner != c.inner {
				t.Errorf("%dx%d: panel inner width %d, want %d", c.w, c.h, inner, c.inner)
			}
			if !flush {
				t.Errorf("%dx%d lit %v: the link line has an indent", c.w, c.h, lit)
			}
			if !lit {
				t.Logf("chat panel inner width at %dx%d (%s): %d", c.w, c.h, map[bool]string{true: "full", false: "compact"}[c.boxed], inner)
			}
		}
	}
	// The longest IPv4 link (39 characters) fits at 160 columns too.
	h := hlHarness(t, testRenderer())
	long := "http://255.255.255.255:65535/k3jq7xw2mf"
	h.say(server.ChatLine{At: h.now, Name: "Priya", Text: "shared a screenshot", Shot: long})
	h.m.notice = notice{}
	if inner, _ := chatPanelLink(t, h.viewAt(160, 40), long, true); inner < 0 {
		t.Errorf("a 39-character link is not whole at 160 columns")
	}
}

// A panel that is too narrow for the link says so in dim text, and never
// shows a part of the link. Full mode at 100 columns has a panel of 26.
func TestChatPanelLinkTooLong(t *testing.T) {
	h := hlHarness(t, testRenderer())
	h.m.snap.Chat = append(h.m.snap.Chat, server.ChatLine{At: h.now, Name: "Priya", Text: "shared a screenshot", Shot: testOtherURL})
	v := stripANSI(h.viewAt(100, 30))
	if strings.Contains(v, "zyxwvutsrq") || strings.Contains(v, "http://172") {
		t.Errorf("a part of the link shows:\n%s", v)
	}
	if !strings.Contains(v, shotLinkTooLongShort) {
		t.Errorf("no too-long line at 100 columns:\n%s", v)
	}
	// The full form where it fits, and the dim style.
	lines := h.m.chatLines(30)
	last := lines[len(lines)-1]
	if stripANSI(last) != shotLinkTooLong {
		t.Errorf("line at 30 = %q", stripANSI(last))
	}
	if last != h.m.st.dim.Render(shotLinkTooLong) {
		t.Errorf("the too-long line is not dim: %q", last)
	}
	lines = h.m.chatLines(20)
	if got := stripANSI(lines[len(lines)-1]); got != shotLinkTooLongShort {
		t.Errorf("line at 20 = %q", got)
	}
	// A link that fits uses the link style.
	lines = h.m.chatLines(40)
	if last := lines[len(lines)-1]; last != h.m.st.info.Render(testOtherURL) {
		t.Errorf("the link line = %q", last)
	}
}

// B15-order: the TUI sends the link to the server (ShareShot) only when
// the picture is ready, so the server orders the lines by the time that
// the links arrive. One player has one shot at a time, so the own links
// reach the server in the order of the presses.
func TestShotSharedWhenPictureReady(t *testing.T) {
	h, f := shareHarness(t, "6 seats, flop, waiting")
	h.sess.later = true
	f.gate = make(chan struct{})
	cmd := h.key("p")
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	for f.calls() == 0 {
		time.Sleep(time.Millisecond)
	}
	if got := h.shareCalls(); len(got) != 0 {
		t.Fatalf("ShareShot before the picture is ready: %v", got)
	}
	h.now = h.now.Add(shotEvery)
	if c := h.key("p"); c != nil {
		t.Fatal("P started a second shot while one runs")
	}
	close(f.gate)
	h.m.Update(<-done)
	if got := h.shareCalls(); len(got) != 1 || got[0] != "ShareShot("+testTableURL+")" {
		t.Fatalf("share calls after the picture = %v", got)
	}

	f.mu.Lock()
	f.gate, f.table = nil, testOtherURL
	f.mu.Unlock()
	h.now = h.now.Add(shotEvery)
	h.finish(h.key("p"))
	want := []string{"ShareShot(" + testTableURL + ")", "ShareShot(" + testOtherURL + ")"}
	if got := h.shareCalls(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("share calls = %v, want %v (the order of the presses)", got, want)
	}
}

// B15-order: README and DESIGN.md must not promise the order of the
// presses across players. The server gets no press time; it posts the
// links in the order in which they arrive.
func TestShotOrderDocs(t *testing.T) {
	for _, c := range []struct{ file, bad, good string }{
		{"../../docs/GUIDE.md", "the links come in the\norder of the pushes", "order in which the server gets them"},
		{"../../DESIGN.md", "The order of the lines is the order of the presses", "the order in which the links\n    reach the server"},
	} {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, c.bad) {
			t.Errorf("%s still says %q", c.file, c.bad)
		}
		if !strings.Contains(text, c.good) {
			t.Errorf("%s does not say %q", c.file, c.good)
		}
	}
}
