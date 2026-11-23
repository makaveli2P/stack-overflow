package tui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makaveli2p/stack-overflow/internal/shot"
)

// Batch 13: P draws the screen as a PNG on the server and shows a link.

const testShotURL = "http://192.168.1.20:2280/abcdefghjk"

// shotHook is a fake Options.Shot. It records each frame and returns url
// (as Links.Own), table (as Links.Table) and err. When gate is not nil,
// the hook waits for it.
type shotHook struct {
	mu     sync.Mutex
	frames []shot.Frame
	url    string
	table  string
	err    error
	gate   chan struct{}
}

func (f *shotHook) hook(fr shot.Frame) (shot.Links, error) {
	f.mu.Lock()
	f.frames = append(f.frames, fr)
	gate, links, err := f.gate, shot.Links{Own: f.url, Table: f.table}, f.err
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return links, err
}

func (f *shotHook) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

// shotHarness is the 6-seat flop table (Devon holds Qs Qd in hand #42),
// with the fake hook.
func shotHarness(t *testing.T) (*harness, *shotHook) {
	t.Helper()
	h := newHarness(t, fixture(t, "6 seats, flop"))
	f := &shotHook{url: testShotURL}
	h.m.shot.hook = f.hook
	h.sess.later = true // a hand is on: the server keeps the link
	return h, f
}

// key sends one key and returns its command, without running it.
func (h *harness) key(k string) tea.Cmd {
	_, cmd := h.m.Update(keyMsg(k))
	return cmd
}

// finish runs a screenshot command and gives its result to the model.
func (h *harness) finish(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		h.t.Fatal("no command to run")
	}
	msg := cmd()
	if _, ok := msg.(shotDoneMsg); !ok {
		h.t.Fatalf("command returned %T, want shotDoneMsg", msg)
	}
	h.m.Update(msg)
}

func (h *harness) tick() { h.m.Update(tickMsg(h.now)) }

func TestScreenshotKey(t *testing.T) {
	for _, k := range []string{"p", "P"} {
		h, f := shotHarness(t)
		before := h.m.View()
		cmd := h.key(k)
		if f.calls() != 0 {
			t.Fatalf("%s: the hook ran inside Update", k)
		}
		if got := h.m.notice.text; got != "Screenshot…" {
			t.Errorf("%s: notice while the shot runs = %q", k, got)
		}
		h.finish(cmd)
		if f.calls() != 1 {
			t.Fatalf("%s: hook calls = %d, want 1", k, f.calls())
		}
		fr := f.frames[0]
		if fr.View != before {
			t.Errorf("%s: the frame is not the screen before the notice", k)
		}
		if fr.Width != 100 || fr.Height != 30 || !fr.Dark {
			t.Errorf("%s: frame size %dx%d dark %v, want 100x30 dark", k, fr.Width, fr.Height, fr.Dark)
		}
		if want := "Stack Overflow — Devon · hand #42"; fr.Title != want {
			t.Errorf("%s: title %q, want %q", k, fr.Title, want)
		}
		// A hand is on: the link goes to the chat when the hand ends
		// (batch 15).
		want := "Screenshot: " + testShotURL + " · goes to the chat when the hand ends"
		if h.m.notice.text != want || h.m.notice.err {
			t.Errorf("%s: notice = %+v, want %q", k, h.m.notice, want)
		}
		if v := stripANSI(h.m.View()); !strings.Contains(v, want) {
			t.Errorf("%s: the screen does not show %q:\n%s", k, want, v)
		}
		// The link shows for 60 seconds.
		h.now = h.now.Add(59 * time.Second)
		h.tick()
		if !strings.Contains(stripANSI(h.m.View()), testShotURL) {
			t.Errorf("%s: the link is gone after 59 s", k)
		}
		h.now = h.now.Add(2 * time.Second)
		h.tick()
		if strings.Contains(stripANSI(h.m.View()), testShotURL) {
			t.Errorf("%s: the link still shows after 61 s", k)
		}
	}
}

func TestScreenshotTitleBetweenHands(t *testing.T) {
	h, f := shotHarness(t)
	s := h.m.snap
	s.Table.InHand = false
	h.m.Update(snapshotMsg{s})
	h.finish(h.key("p"))
	if got := f.frames[0].Title; got != "Stack Overflow — Devon" {
		t.Errorf("title between hands = %q", got)
	}
	// A light terminal gives a light frame.
	h.m.st.r.SetHasDarkBackground(false)
	h.now = h.now.Add(shotEvery)
	h.finish(h.key("p"))
	if f.frames[1].Dark {
		t.Error("frame of a light terminal is dark")
	}
}

func TestScreenshotRateLimit(t *testing.T) {
	h, f := shotHarness(t)
	h.finish(h.key("p"))
	h.now = h.now.Add(2900 * time.Millisecond)
	if cmd := h.key("p"); cmd != nil {
		t.Fatal("a second P within 3 s started a shot")
	}
	if h.m.notice.text != "One screenshot every 3 seconds" {
		t.Errorf("notice = %q", h.m.notice.text)
	}
	h.now = h.now.Add(100 * time.Millisecond)
	h.finish(h.key("p"))
	if f.calls() != 2 {
		t.Errorf("hook calls = %d, want 2", f.calls())
	}
}

// A fast double press of P must not lose the link: the rate notice
// shows, and then the link shows again until its 60 seconds end.
func TestScreenshotRateNoticeKeepsTheLink(t *testing.T) {
	h, _ := shotHarness(t)
	start := h.now
	h.finish(h.key("p"))
	h.now = h.now.Add(300 * time.Millisecond)
	h.key("p")
	if h.m.notice.text != shotRateNotice {
		t.Fatalf("notice = %q, want the rate notice", h.m.notice.text)
	}
	h.now = h.now.Add(noticeFor + time.Second)
	h.tick()
	if !strings.Contains(stripANSI(h.m.View()), testShotURL) {
		t.Errorf("the link does not show again after the rate notice:\n%s", stripANSI(h.m.View()))
	}
	h.now = start.Add(shotFor + time.Second)
	h.tick()
	if strings.Contains(stripANSI(h.m.View()), testShotURL) {
		t.Error("the link still shows after 60 s")
	}
}

func TestScreenshotOneAtATime(t *testing.T) {
	h, f := shotHarness(t)
	f.gate = make(chan struct{})
	cmd := h.key("p")
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	for f.calls() == 0 {
		time.Sleep(time.Millisecond)
	}
	h.now = h.now.Add(10 * time.Second)
	if c := h.key("p"); c != nil {
		t.Fatal("P started a second shot while one runs")
	}
	if h.m.notice.text != "The screenshot is not ready yet" {
		t.Errorf("notice = %q", h.m.notice.text)
	}
	close(f.gate)
	h.m.Update(<-done)
	if !strings.Contains(h.m.notice.text, testShotURL) {
		t.Errorf("notice after the shot = %q", h.m.notice.text)
	}
	f.gate = nil
	h.finish(h.key("p"))
	if f.calls() != 2 {
		t.Errorf("hook calls = %d, want 2", f.calls())
	}
}

func TestScreenshotErrors(t *testing.T) {
	for _, c := range []struct {
		url  string
		err  error
		want string
	}{
		{"", errors.New("the store is full"), "Screenshot failed: the store is full"},
		{"", fmt.Errorf("render: %w", shot.ErrTooBig), "Screenshot failed: the screen is too big for a screenshot"},
		{"", nil, "Screenshot failed: the server gave no link"},
	} {
		h, f := shotHarness(t)
		f.url, f.err = c.url, c.err
		h.finish(h.key("p"))
		if h.m.notice.text != c.want || !h.m.notice.err {
			t.Errorf("notice = %+v, want the error %q", h.m.notice, c.want)
		}
		// After an error, P works again after 3 seconds.
		f.url, f.err = testShotURL, nil
		h.now = h.now.Add(shotEvery)
		h.finish(h.key("p"))
		if !strings.Contains(h.m.notice.text, testShotURL) {
			t.Errorf("P after an error: notice %q", h.m.notice.text)
		}
	}
}

func TestScreenshotNilHook(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	if cmd := h.key("p"); cmd != nil {
		t.Error("P with no hook returned a command")
	}
	if want := "Screenshot failed: screenshots are off on this server"; h.m.notice.text != want || !h.m.notice.err {
		t.Errorf("notice = %+v, want %q", h.m.notice, want)
	}
}

func TestScreenshotNotInChatRaiseOrPanic(t *testing.T) {
	// In the chat, p is a letter.
	h, f := shotHarness(t)
	h.press("t", "p")
	if f.calls() != 0 || !strings.Contains(string(h.m.chatBuf), "p") {
		t.Errorf("p in the chat: %d calls, chat %q", f.calls(), string(h.m.chatBuf))
	}

	// In the raise editor, p is the pot preset.
	h = newHarness(t, fixture(t, "my turn to bet"))
	f = &shotHook{url: testShotURL}
	h.m.shot.hook = f.hook
	h.press("r")
	if h.m.mode != modeRaise {
		t.Fatalf("r did not open the raise editor")
	}
	before := h.m.raise.amount
	if cmd := h.key("p"); cmd != nil || f.calls() != 0 {
		t.Error("p in the raise editor started a shot")
	}
	if h.m.raise.amount == before || h.m.mode != modeRaise {
		t.Errorf("p in the raise editor did not pick the pot: %d → %d", before, h.m.raise.amount)
	}

	// On the panic screen, p only ends the panic screen.
	h, f = shotHarness(t)
	h.startPanic()
	if cmd := h.key("p"); f.calls() != 0 || h.m.shot.busy {
		t.Errorf("p on the panic screen started a shot (cmd %v)", cmd != nil)
	}
}

// A link that comes during the panic screen does not show on it. It shows
// when the panic screen ends.
func TestScreenshotResultDuringPanic(t *testing.T) {
	h, _ := shotHarness(t)
	cmd := h.key("p")
	h.startPanic()
	h.finish(cmd)
	for _, phase := range []string{"notice", "decoy"} {
		if v := h.view(); strings.Contains(v, testShotURL) || strings.Contains(v, "Screenshot") {
			t.Errorf("panic %s shows the link:\n%s", phase, v)
		}
		if phase == "notice" {
			h.toDecoy()
		}
	}
	h.press("x")
	if h.m.panic.on {
		t.Fatal("a key did not end the panic screen")
	}
	if v := h.view(); !strings.Contains(v, testShotURL) {
		t.Errorf("the link does not show after the panic:\n%s", v)
	}
}

// With the help open, P closes it and shoots the table.
func TestScreenshotClosesHelp(t *testing.T) {
	h, f := shotHarness(t)
	table := h.m.View()
	h.press("?")
	if !h.m.help {
		t.Fatal("? did not open the help")
	}
	h.finish(h.key("p"))
	if h.m.help {
		t.Error("P did not close the help")
	}
	if f.calls() != 1 || f.frames[0].View != table {
		t.Errorf("the frame is not the table without the help (%d calls)", f.calls())
	}
}

// The link is never cut: the notice drops the other words first. Batch 14
// tests the other notices in batch14_test.go.
func TestScreenshotLinkNotCut(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		// A hand is on: the link waits for the end of the hand (batch 15).
		{120, 32, "› Screenshot: " + testShotURL + " · goes to the chat when the hand ends"},
		{87, 24, "› Screenshot: " + testShotURL + " · goes to the chat when the hand ends"},
		{86, 24, "› " + testShotURL + " · goes to the chat when the hand ends"},
		{80, 24, "› " + testShotURL + " · goes to the chat when the hand ends"},
		{75, 24, "› " + testShotURL + " · goes to the chat when the hand ends"},
		{74, 20, "› " + testShotURL + " · to the chat after the hand"},
		{66, 20, "› " + testShotURL + " · to the chat after the hand"},
		{65, 20, "› " + testShotURL},
		{60, 20, "› " + testShotURL},
		{len(testShotURL), 18, testShotURL},
	}
	for _, c := range cases {
		h, _ := shotHarness(t)
		h.finish(h.key("p"))
		view := h.viewAt(c.w, c.h)
		wantFits(t, "link notice", view, c.w, c.h)
		var line string
		for _, l := range strings.Split(stripANSI(view), "\n") {
			if strings.Contains(l, "abcdefghjk") {
				line = l
			}
		}
		if strings.TrimRight(line, " ") != c.want {
			t.Errorf("%dx%d: notice line %q, want %q", c.w, c.h, line, c.want)
		}
	}
}

// A new hand does not replace the link with the "new cards" notice.
func TestScreenshotLinkStaysAtNewHand(t *testing.T) {
	h, _ := shotHarness(t)
	h.finish(h.key("p"))
	s := h.m.snap
	s.Table.HandNumber++
	h.m.Update(snapshotMsg{s})
	if !strings.Contains(h.m.notice.text, testShotURL) {
		t.Errorf("notice after a new hand = %q", h.m.notice.text)
	}
}

// The help names P and still fits in 80x24.
func TestScreenshotHelp(t *testing.T) {
	for _, name := range []string{"6 seats, flop", "waiting to start, seated", "my turn to bet"} {
		h := newHarness(t, fixture(t, name))
		h.press("?")
		for _, sz := range [][2]int{{80, 24}, {100, 30}} {
			view := h.viewAt(sz[0], sz[1])
			wantFits(t, name+" help", view, sz[0], sz[1])
			v := stripANSI(view)
			for _, want := range []string{"M N I P ", "profile · alerts on/off · hand rankings · screenshot", "Q / Ctrl+C", "╰"} {
				if !strings.Contains(v, want) {
					t.Errorf("%s help at %v lacks %q\n%s", name, sz, want, v)
				}
			}
			if strings.Contains(v, "copy screen") {
				t.Errorf("%s help still says copy screen", name)
			}
			// Batch 15 removed V.
			if strings.Contains(v, "M N I P V") || strings.Contains(v, "its link") {
				t.Errorf("%s help still names V", name)
			}
		}
	}
}
