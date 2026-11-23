package tui

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// fakeSession records every Session call.
type fakeSession struct {
	mu      sync.Mutex
	calls   []string
	actions []engine.Action
	err     error
	ch      chan server.Snapshot
	// later is the answer of ShareShot: true makes the link wait for the
	// end of the hand (batch 15).
	later bool
}

func newFake() *fakeSession { return &fakeSession{ch: make(chan server.Snapshot, 1)} }

func (f *fakeSession) record(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakeSession) Updates() <-chan server.Snapshot { return f.ch }
func (f *fakeSession) Sit(seat int) error              { return f.record(fmt.Sprintf("Sit(%d)", seat)) }
func (f *fakeSession) SitWith(seat int, c server.SitChoice) error {
	return f.record(fmt.Sprintf("SitWith(%d,%s)", seat, choiceName(c)))
}
func (f *fakeSession) Stand() error           { return f.record("Stand") }
func (f *fakeSession) SitOut(out bool) error  { return f.record(fmt.Sprintf("SitOut(%v)", out)) }
func (f *fakeSession) Rebuy() error           { return f.record("Rebuy") }
func (f *fakeSession) Chat(text string) error { return f.record("Chat(" + text + ")") }
func (f *fakeSession) ShareShot(url string) (bool, error) {
	err := f.record("ShareShot(" + url + ")")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.later && err == nil, err
}
func (f *fakeSession) Close()                    { _ = f.record("Close") }
func (f *fakeSession) SetName(name string) error { return f.record("SetName(" + name + ")") }
func (f *fakeSession) LinkPIN(login, pin string) error {
	return f.record("LinkPIN(" + login + "," + pin + ")")
}
func (f *fakeSession) RabbitHunt() error { return f.record("RabbitHunt") }
func (f *fakeSession) ShowCards(left, right bool) error {
	return f.record(fmt.Sprintf("ShowCards(%v,%v)", left, right))
}
func (f *fakeSession) SetAlerts(on bool) error { return f.record(fmt.Sprintf("SetAlerts(%v)", on)) }
func (f *fakeSession) MarkSettled() error      { return f.record("MarkSettled") }
func (f *fakeSession) Extend() error           { return f.record("Extend") }
func (f *fakeSession) Panic() error            { return f.record("Panic") }
func (f *fakeSession) Start() error            { return f.record("Start") }
func (f *fakeSession) Act(a engine.Action) error {
	f.mu.Lock()
	f.actions = append(f.actions, a)
	f.mu.Unlock()
	return f.record("Act")
}

// choiceName names a SitChoice in the call list.
func choiceName(c server.SitChoice) string {
	switch c {
	case server.SitBuyIn:
		return "buyin"
	case server.SitWholeBank:
		return "bank"
	}
	return fmt.Sprint(int(c))
}

func (f *fakeSession) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func testRenderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	return r
}

// harness drives a model with a fake session and a fake clock.
type harness struct {
	t    *testing.T
	m    *model
	sess *fakeSession
	now  time.Time
	msgs []tea.Msg
}

func newHarness(t *testing.T, f Fixture) *harness {
	t.Helper()
	h := &harness{t: t, sess: newFake(), now: f.Snap.Now}
	h.m = newModelWith(h.sess, testRenderer(), 100, 30, func() time.Time { return h.now })
	h.m.Update(snapshotMsg{f.Snap})
	return h
}

// press sends keys and runs every command they return, feeding the
// resulting messages back into the model.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		_, cmd := h.m.Update(keyMsg(k))
		h.run(cmd)
	}
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			h.run(c)
		}
		return
	}
	h.msgs = append(h.msgs, msg)
	if done, ok := msg.(callDoneMsg); ok {
		h.m.Update(done)
	}
}

func (h *harness) quitSent() bool {
	for _, m := range h.msgs {
		if _, ok := m.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

func (h *harness) wantCalls(want ...string) {
	h.t.Helper()
	got := h.sess.callList()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		h.t.Fatalf("calls = %v, want %v", got, want)
	}
}

func (h *harness) wantAction(want engine.Action) {
	h.t.Helper()
	h.wantCalls("Act")
	if got := h.sess.actions[0]; got != want {
		h.t.Fatalf("action = %+v, want %+v", got, want)
	}
}

func fixture(t *testing.T, name string) Fixture {
	t.Helper()
	for _, f := range Fixtures() {
		if strings.Contains(f.Name, name) {
			return f
		}
	}
	t.Fatalf("no fixture %q", name)
	return Fixture{}
}

func TestViewFitsTerminal(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {140, 40}, {90, 26}, {120, 32}, {100, 24}, {80, 40}, {60, 18}, {30, 8}}
	r := testRenderer()
	for _, f := range Fixtures() {
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%s/%dx%d", f.Name, w, h), func(t *testing.T) {
				view := Render(f, r, w, h)
				lines := strings.Split(view, "\n")
				if len(lines) > h {
					t.Errorf("view has %d lines, terminal has %d", len(lines), h)
				}
				for i, l := range lines {
					if lw := lipgloss.Width(l); lw > w {
						t.Errorf("line %d is %d cells wide (max %d): %q", i, lw, w, l)
					}
				}
			})
		}
	}
}

func TestViewContent(t *testing.T) {
	cases := []struct {
		fixture string
		want    []string // at every size
		full    []string // in full mode only
	}{
		{"heads-up", []string{"YOUR TURN", "Fold", "Call 10", "Raise", "All-in 1,990", "Priya", "A♥", "K♥"}, []string{"pot odds 25%"}},
		{"6 seats, flop", []string{"Waiting for", "Priya", "Marta", "FLOP", "folded", "sitting out"}, nil},
		{"raise editor", []string{"Raise to", "3,560", "main 820", "side 1 600", "away"}, []string{"A all-in", "Enter"}},
		{"showdown", []string{"RESULT", "Devon wins 3,360", "Straight Flush", "busted", "Trips"}, []string{"Pair of Kings", "next hand in 4s"}},
		{"spectator", []string{"You are watching", "Sit down"}, nil},
		{"busted", []string{"Buy back in for 2,000", "busted"}, nil},
		{"to bet", []string{"Check", "Bet", "Bartholomew", "Kim"}, []string{"High Card, Ace"}},
		{"chat input", []string{"Say", "gg wp", "who ordered pizza?"}, nil},
		{"alone", []string{"Waiting for", "open"}, nil},
	}
	r := testRenderer()
	for _, c := range cases {
		f := fixture(t, c.fixture)
		for _, sz := range [][2]int{{80, 24}, {140, 40}} {
			view := stripANSI(Render(f, r, sz[0], sz[1]))
			want := c.want
			if sz[0] >= fullW {
				want = append(append([]string{}, want...), c.full...)
			}
			for _, w := range want {
				if !strings.Contains(view, w) {
					t.Errorf("%s at %dx%d: view lacks %q\n%s", f.Name, sz[0], sz[1], w, view)
				}
			}
		}
	}
}

func stripANSI(s string) string { return ansi.Strip(s) }

func TestBasicActions(t *testing.T) {
	cases := []struct {
		fixture string
		keys    []string
		want    engine.Action
	}{
		{"heads-up", []string{"f"}, engine.Action{Type: engine.Fold}},
		{"heads-up", []string{"c"}, engine.Action{Type: engine.Call}},
		{"heads-up", []string{"C"}, engine.Action{Type: engine.Call}},
		{"to bet", []string{"c"}, engine.Action{Type: engine.Check}},
		{"to bet", []string{"k"}, engine.Action{Type: engine.Check}},
		{"heads-up", []string{"a", "a"}, engine.Action{Type: engine.AllIn}},
	}
	for _, c := range cases {
		t.Run(c.fixture+strings.Join(c.keys, ""), func(t *testing.T) {
			h := newHarness(t, fixture(t, c.fixture))
			h.press(c.keys...)
			h.wantAction(c.want)
		})
	}
}

func TestAllInNeedsTwoPresses(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("a")
	h.wantCalls()
	h.press("c")
	h.wantAction(engine.Action{Type: engine.Call})

	h = newHarness(t, fixture(t, "heads-up"))
	h.press("a")
	h.now = h.now.Add(armedFor + time.Second)
	h.press("a")
	h.wantCalls()
}

func TestNotMyTurn(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	h.press("c", "f", "r", "a")
	h.wantCalls()
	if !h.m.notice.err || !strings.Contains(h.m.notice.text, "not your turn") {
		t.Fatalf("notice = %+v", h.m.notice)
	}
}

func TestRaiseEditor(t *testing.T) {
	// Heads-up preflop: my bet 10, call 10, pot 30, big blind 20,
	// MinTo 40, MaxTo = AllInTo = 2000. Pot raise = 10+10+(30+10) = 60.
	raise := func(v int64) engine.Action { return engine.Action{Type: engine.Raise, Amount: v} }
	cases := []struct {
		name string
		keys []string
		want engine.Action
	}{
		{"min", []string{"r", "enter"}, raise(40)},
		{"right steps one big blind", []string{"r", "right", "right", "enter"}, raise(80)},
		{"left clamps to min", []string{"r", "left", "left", "enter"}, raise(40)},
		{"h is half pot", []string{"r", "h", "enter"}, raise(40)},
		{"t is two thirds pot", []string{"r", "t", "enter"}, raise(46)},
		{"p is pot", []string{"r", "p", "enter"}, raise(60)},
		{"P works in upper case", []string{"r", "P", "enter"}, raise(60)},
		{"a is all-in", []string{"r", "a", "enter"}, engine.Action{Type: engine.AllIn}},
		{"typed digits", []string{"r", "5", "5", "0", "enter"}, raise(550)},
		{"1-4 always type", []string{"r", "1", "5", "0", "enter"}, raise(150)},
		{"first digit replaces a preset", []string{"r", "p", "2", "0", "0", "enter"}, raise(200)},
		{"typed amount clamps to min", []string{"r", "9", "enter"}, raise(40)},
		{"typed amount clamps to max", []string{"r", "9", "9", "9", "9", "9", "enter"}, engine.Action{Type: engine.AllIn}},
		{"backspace edits", []string{"r", "5", "5", "5", "backspace", "enter"}, raise(55)},
		{"backspace edits a preset", []string{"r", "p", "backspace", "5", "enter"}, raise(65)},
		{"up steps through pot sizes", []string{"r", "up", "up", "enter"}, raise(50)},
		{"down from all-in", []string{"r", "a", "down", "enter"}, raise(100)},
		{"no hidden keys", []string{"r", "l", "k", "j", "q", "r", "b", "+", "-", "=", "enter"}, raise(40)},
		{"c calls from the editor", []string{"r", "p", "c"}, engine.Action{Type: engine.Call}},
		{"C calls from the editor", []string{"r", "C"}, engine.Action{Type: engine.Call}},
		{"f folds from the editor", []string{"r", "5", "f"}, engine.Action{Type: engine.Fold}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, fixture(t, "heads-up"))
			h.press(c.keys...)
			h.wantAction(c.want)
			if h.m.mode != modeTable {
				t.Fatalf("mode = %v after confirm", h.m.mode)
			}
		})
	}
}

func TestBetUsesBetAction(t *testing.T) {
	h := newHarness(t, fixture(t, "to bet"))
	h.press("b", "right", "enter")
	h.wantAction(engine.Action{Type: engine.Bet, Amount: 40})
}

func TestRaiseEditorCancel(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("r", "3", "esc")
	h.wantCalls()
	if h.m.mode != modeTable {
		t.Fatal("esc must close the editor")
	}
}

func TestEditorClosesWhenTurnEnds(t *testing.T) {
	f := fixture(t, "heads-up")
	h := newHarness(t, f)
	h.press("r")
	snap := f.Snap
	snap.Table.Legal = nil
	h.m.Update(snapshotMsg{snap})
	if h.m.mode != modeTable {
		t.Fatal("editor must close when the turn ends")
	}
}

func TestStepTargets(t *testing.T) {
	rm := raiseMath{legal: engine.Legal{MinTo: 40, MaxTo: 2000, AllInTo: 2000, Raise: true, CallAmount: 10}, myBet: 10, pot: 30, bigBet: 20}
	got := fmt.Sprint(rm.stepTargets())
	if want := "[40 46 50 60 80 100 2000]"; got != want {
		t.Fatalf("stepTargets = %s, want %s", got, want)
	}
	if rm.stepUp(2000) != 2000 || rm.stepDown(40) != 40 {
		t.Fatal("steps must stay inside MinTo..MaxTo")
	}
}

func TestChat(t *testing.T) {
	for _, open := range []string{"t", "/"} {
		h := newHarness(t, fixture(t, "6 seats, flop"))
		h.press(open, "h", "i", " ", "a", "l", "l", "enter")
		h.wantCalls("Chat(hi all)")
	}
	h := newHarness(t, fixture(t, "6 seats, flop"))
	h.press("tgl hf", "enter")
	h.wantCalls("Chat(gl hf)")

	h = newHarness(t, fixture(t, "6 seats, flop"))
	h.press("t", "x", "esc", "f")
	h.wantCalls()
	h.press("t", " ", "enter")
	h.wantCalls()
}

func TestSeatKeys(t *testing.T) {
	h := newHarness(t, fixture(t, "spectator"))
	h.press("s")
	h.wantCalls("SitWith(-1,buyin)")

	h = newHarness(t, fixture(t, "busted"))
	h.press("b")
	h.wantCalls("Rebuy")

	h = newHarness(t, fixture(t, "alone"))
	h.press("o", "l")
	h.wantCalls("SitOut(true)", "Stand")
}

func TestLeaveDuringHandAsks(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	h.press("l")
	h.wantCalls()
	if h.m.mode != modeConfirm {
		t.Fatal("leave during a live hand must ask first")
	}
	h.press("n")
	h.wantCalls()
	h.press("l", "y")
	h.wantCalls("Stand")
}

func TestQuit(t *testing.T) {
	h := newHarness(t, fixture(t, "alone"))
	h.press("q")
	h.wantCalls("Close")
	if !h.quitSent() {
		t.Fatal("q must quit when no hand is live")
	}

	h = newHarness(t, fixture(t, "6 seats, flop"))
	h.press("q")
	if h.quitSent() || h.m.mode != modeConfirm {
		t.Fatal("q during a live hand must ask first")
	}
	h.press("x")
	h.press("ctrl+c", "y")
	h.wantCalls("Close")
	if !h.quitSent() {
		t.Fatal("y must quit")
	}

	h = newHarness(t, fixture(t, "6 seats, flop"))
	h.press("ctrl+c", "ctrl+c")
	if !h.quitSent() {
		t.Fatal("a second ctrl+c must quit")
	}
}

func TestErrorNoticeExpires(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.sess.err = errors.New("it is not your turn")
	h.press("c")
	if h.m.acting {
		t.Fatal("acting must clear when the call returns")
	}
	if !strings.Contains(stripANSI(h.m.View()), "it is not your turn") {
		t.Fatal("error notice is not shown")
	}
	h.now = h.now.Add(noticeFor + time.Millisecond)
	h.m.Update(tickMsg(h.now))
	if h.m.notice.text != "" {
		t.Fatal("notice must expire after 3 seconds")
	}
}

func TestTabsAndHelp(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	if h.m.tab != tabChat {
		t.Fatalf("Chat must be the default tab, got %v", h.m.tab)
	}
	h.press("tab")
	if h.m.tab != tabLog {
		t.Fatalf("tab = %v", h.m.tab)
	}
	h.press("tab", "tab")
	if h.m.tab != tabChat {
		t.Fatalf("tab = %v", h.m.tab)
	}
	h.press("shift+tab")
	if h.m.tab != tabPlayers {
		t.Fatalf("tab = %v", h.m.tab)
	}
	h.press("?")
	if !h.m.help || !strings.Contains(stripANSI(h.m.View()), "Keys") {
		t.Fatal("? must show help")
	}
	h.press("?")
	if h.m.help {
		t.Fatal("? must close help")
	}
	h.press("?", "esc")
	if h.m.help {
		t.Fatal("Esc must close help")
	}
}

func TestUnreadChat(t *testing.T) {
	f := fixture(t, "6 seats, flop")
	h := newHarness(t, f)
	if h.m.unreadChat() {
		t.Fatal("chat history on connect is not unread")
	}
	h.press("tab") // Chat is the default tab; move to Log.
	snap := f.Snap
	snap.Chat = append(append([]server.ChatLine(nil), snap.Chat...), server.ChatLine{At: previewNow, Name: "Sam", Text: "hi"})
	h.m.Update(snapshotMsg{snap})
	if !h.m.unreadChat() {
		t.Fatal("new chat line must be unread")
	}
	h.press("shift+tab")
	if h.m.unreadChat() {
		t.Fatal("opening the chat tab must mark it read")
	}
}

func TestUpdatesChannel(t *testing.T) {
	ch := make(chan server.Snapshot, 1)
	ch <- server.Snapshot{Now: previewNow}
	if _, ok := waitSnapshot(ch)().(snapshotMsg); !ok {
		t.Fatal("want snapshotMsg")
	}
	close(ch)
	if _, ok := waitSnapshot(ch)().(closedMsg); !ok {
		t.Fatal("want closedMsg after close")
	}

	h := newHarness(t, fixture(t, "alone"))
	_, cmd := h.m.Update(closedMsg{})
	if cmd == nil || !h.m.gone || !strings.Contains(stripANSI(h.m.View()), "Disconnected") {
		t.Fatal("a closed channel must show disconnected and quit")
	}
}

func TestTurnClock(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	left, ok := h.m.turnLeft()
	if !ok || left != 22*time.Second {
		t.Fatalf("turnLeft = %v, %v", left, ok)
	}
	h.now = h.now.Add(5 * time.Second)
	if left, _ = h.m.turnLeft(); left != 17*time.Second {
		t.Fatalf("turnLeft after 5s = %v", left)
	}
}

func TestChips(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -2500: "-2,500"} {
		if got := chips(in); got != want {
			t.Errorf("chips(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRingSlots(t *testing.T) {
	for n := 2; n <= 10; n++ {
		s := ringSlots(n)
		seen := map[int]bool{0: true}
		for _, k := range append(append([]int{}, s.top...), s.left, s.right, s.bottomLeft, s.bottomRight) {
			if k < 0 {
				continue
			}
			if seen[k] {
				t.Fatalf("n=%d: ring position %d used twice", n, k)
			}
			seen[k] = true
		}
		if len(seen) != n {
			t.Fatalf("n=%d: %d positions placed", n, len(seen))
		}
	}
}

// edited returns a copy of the named fixture after change. The copy has its
// own seats, leaderboard and Legal, so change cannot alter other tests.
func edited(t *testing.T, name string, change func(s *server.Snapshot)) Fixture {
	t.Helper()
	f := fixture(t, name)
	f.Snap.Table.Seats = append([]engine.SeatView(nil), f.Snap.Table.Seats...)
	f.Snap.Leaderboard = append([]server.Standing(nil), f.Snap.Leaderboard...)
	if l := f.Snap.Table.Legal; l != nil {
		c := *l
		f.Snap.Table.Legal = &c
	}
	change(&f.Snap)
	return f
}

func TestFoldedCardsStayHiddenAfterHand(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		s.Table.InHand, s.Table.Legal, s.Table.ToAct = false, nil, -1
		me := &s.Table.Seats[s.Table.MySeat]
		me.InHand, me.HasCards = false, false // the engine view of a folded hand after the hand
	})
	for _, sz := range [][2]int{{80, 24}, {140, 40}} {
		if view := stripANSI(Render(f, testRenderer(), sz[0], sz[1])); strings.Contains(view, "A♥") {
			t.Errorf("%dx%d: folded cards are visible\n%s", sz[0], sz[1], view)
		}
	}
}

func TestCallAllInHasNoSecondAllInButton(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		stack := s.Table.Seats[s.Table.MySeat].Stack
		*s.Table.Legal = engine.Legal{Fold: true, Call: true, CallAmount: stack, AllIn: true, AllInTo: stack}
	})
	view := stripANSI(Render(f, testRenderer(), 100, 30))
	if !strings.Contains(view, "(all-in)") || strings.Contains(view, "All-in ") {
		t.Errorf("want only the all-in call\n%s", view)
	}
}

func TestHelpFitsSmallTerminal(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	h.m.width, h.m.height = 80, 24
	h.press("?")
	view := stripANSI(h.m.View())
	for _, want := range []string{"Keys", "Q / Ctrl+C", "╰"} {
		if !strings.Contains(view, want) {
			t.Errorf("help at 80x24 lacks %q\n%s", want, view)
		}
	}
}

func TestHeaderUsesOnlineStanding(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		old := server.Standing{Name: s.Me.Name, Net: -500}
		s.Leaderboard = append([]server.Standing{old}, s.Leaderboard...)
		for i := range s.Leaderboard {
			if s.Leaderboard[i].Name == s.Me.Name && s.Leaderboard[i].Online {
				s.Leaderboard[i].Net = 100
			}
		}
	})
	view := stripANSI(Render(f, testRenderer(), 100, 30))
	if !strings.Contains(view, "net +100") || strings.Contains(view, "away") {
		t.Errorf("header must use the online row\n%s", view)
	}
}

func TestTightBadgesKeepName(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	si := seatInfo{sv: engine.SeatView{Name: "carol"}, button: true, sb: true}
	if got := stripANSI(h.m.nameLine(si, 11, "")); !strings.Contains(got, "carol") {
		t.Errorf("nameLine = %q, want the full name", got)
	}
}

func TestPotOddsIgnoreChipsThatCannotBeWon(t *testing.T) {
	// The opponent moves all-in for 5,000 total; the viewer has 990 left
	// after the small blind, so only 1,000 of those chips can be won.
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		me, opp := &s.Table.Seats[0], &s.Table.Seats[1]
		opp.Bet, opp.Committed, opp.Stack = 5000, 5000, 0
		me.Stack = 990
		s.Table.TotalPot = me.Committed + opp.Committed
		*s.Table.Legal = engine.Legal{Fold: true, Call: true, CallAmount: 990, AllIn: true, AllInTo: 1000}
	})
	view := stripANSI(Render(f, testRenderer(), 100, 30))
	// Winnable: 10 (mine) + 1,000 (theirs, capped) = 1,010; 990 / 2,000 = 49%.
	if !strings.Contains(view, "pot odds 49%") {
		t.Errorf("want pot odds 49%%\n%s", view)
	}
}

// A client controls the window size: SSH pty-req and window-change carry
// uint32 columns and rows, and wish passes them to the model unchanged.
// The model must clamp the size, so that one client cannot make the server
// build a frame of gigabytes and kill the whole process (every player) with
// an out-of-memory crash.
func TestHugeWindowIsClamped(t *testing.T) {
	for _, withSnap := range []bool{false, true} {
		m := newModelWith(newFake(), testRenderer(), 80, 24, time.Now)
		if withSnap {
			m.Update(snapshotMsg{Fixtures()[0].Snap})
		}
		// 4000 x 4000 is small next to the 4294967295 x 4294967295 that
		// the protocol allows.
		m.Update(tea.WindowSizeMsg{Width: 4000, Height: 4000})
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		out := m.View()
		runtime.ReadMemStats(&after)
		alloc := after.TotalAlloc - before.TotalAlloc
		t.Logf("withSnap=%v frame bytes=%d allocated=%d", withSnap, len(out), alloc)
		if len(out) > 1<<20 {
			t.Errorf("withSnap=%v: one View() for a client-chosen 4000x4000 window is %d bytes (allocated %d bytes); "+
				"memory grows with width*height, so a 65535x65535 window needs gigabytes per frame", withSnap, len(out), alloc)
		}
	}
}

func TestFirstWindowSizeIsClamped(t *testing.T) {
	m := newModelWith(newFake(), testRenderer(), 1<<31, 1<<31, time.Now)
	if m.width != maxWidth || m.height != maxHeight {
		t.Errorf("size = %dx%d, want %dx%d", m.width, m.height, maxWidth, maxHeight)
	}
	m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	if m.width != 80 || m.height != 24 {
		t.Errorf("size = %dx%d after a zero size, want 80x24", m.width, m.height)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Errorf("size = %dx%d, want 120x40", m.width, m.height)
	}
}

// At 80x24, a player who has the chat input open when the turn comes must
// still see that it is their turn: the chat input replaces the action bar.
func TestChatInputKeepsYourTurnInCompact(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.m.width, h.m.height = 80, 24
	h.press("t", "h", "i")
	v := stripANSI(h.m.View())
	if !strings.Contains(v, "YOUR TURN") && !strings.Contains(v, "Your turn") {
		t.Errorf("my turn with the chat input open: the screen does not say it is my turn\n%s", v)
	}
	if !strings.Contains(v, "call 10") {
		t.Errorf("my turn with the chat input open: the screen does not show the price to call\n%s", v)
	}
}

func TestAllInArmExpiresOnScreen(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("a")
	h.now = h.now.Add(armedFor + time.Second)
	h.m.Update(tickMsg(h.now))
	if v := stripANSI(h.m.View()); strings.Contains(v, "press A again") {
		t.Errorf("arm window expired, but the action bar still says 'press A again'\n%s", v)
	}
}

func TestAllInArmExpiresBeforeTick(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("a")
	h.now = h.now.Add(armedFor + time.Second)
	if v := stripANSI(h.m.View()); strings.Contains(v, "press A again") {
		t.Errorf("arm window expired, but the action bar still says 'press A again'\n%s", v)
	}
}

func TestAllInNoticeClearsAfterSend(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("a", "a")
	h.wantAction(engine.Action{Type: engine.AllIn})
	if v := stripANSI(h.m.View()); strings.Contains(v, "Press A again") {
		t.Errorf("all-in was sent, but the footer still says 'Press A again'\n%s", v)
	}
}

func TestAllInNoticeClearsOnOtherKey(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("a", "tab")
	if v := stripANSI(h.m.View()); strings.Contains(v, "Press A again") || strings.Contains(v, "press A again") {
		t.Errorf("another key cancelled the all-in, but the screen still says 'press A again'\n%s", v)
	}
}

// A player who sits down in the seat of a big blind that left during the
// hand is not in the hand, but the seat showed the BB badge.
func TestBlindBadgeNotOnNewOccupant(t *testing.T) {
	f := edited(t, "6 seats, flop", func(s *server.Snapshot) {
		s.Table.Seats[1] = player(1, "Zoe", 2000) // BigBlindSeat is 1; Zoe sat down after the deal
	})
	h := newHarness(t, f)
	box := stripANSI(h.m.seatBox(h.m.seatInfo(1), 22, true))
	if !strings.Contains(box, "Zoe") {
		t.Fatalf("precondition: box shows Zoe\n%s", box)
	}
	if strings.Contains(box, "BB") {
		t.Errorf("Zoe is not in this hand, but her seat shows the BB badge\n%s", box)
	}
}

// After the hand ends, the badges stay on the players who were dealt in,
// and do not move to a new occupant of the seat.
func TestBlindBadgeBetweenHands(t *testing.T) {
	f := fixture(t, "6 seats, flop")
	if f.Snap.Table.BigBlindSeat != 1 {
		t.Fatalf("precondition: BigBlindSeat = %d, want 1", f.Snap.Table.BigBlindSeat)
	}
	h := newHarness(t, f)
	ended := func(change func(s *server.Snapshot)) {
		s := edited(t, "6 seats, flop", func(s *server.Snapshot) {
			s.Table.InHand, s.Table.Legal, s.Table.ToAct = false, nil, -1
			for i := range s.Table.Seats {
				s.Table.Seats[i].InHand = false
			}
			change(s)
		}).Snap
		h.m.Update(snapshotMsg{s})
	}
	ended(func(*server.Snapshot) {})
	if !h.m.seatInfo(1).bb {
		t.Error("the big blind of the last hand lost the BB badge after the hand")
	}
	ended(func(s *server.Snapshot) { s.Table.Seats[1] = player(1, "Zoe", 2000) })
	if h.m.seatInfo(1).bb {
		t.Error("Zoe sat down after the last hand was dealt, but her seat shows the BB badge")
	}
}

// While the help is open on the viewer's turn, the footer still shows
// "C Call 10". C must close the help and call.
func TestHelpPassesActionKeys(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("?")
	if v := stripANSI(h.m.View()); !strings.Contains(v, "Call 10") {
		t.Fatalf("precondition: footer shows the call button under the help\n%s", v)
	}
	h.press("c")
	if len(h.sess.callList()) == 0 && h.m.notice.text == "" {
		t.Errorf("C under the help: no action was sent and no notice explains why")
	}
	h.wantAction(engine.Action{Type: engine.Call})
	if h.m.help {
		t.Error("an action key must close the help")
	}
}

// A call of 40 into a pot of 6,070 showed "pot odds 0%".
func TestPotOddsUnderOnePercent(t *testing.T) {
	f := edited(t, "heads-up", func(s *server.Snapshot) {
		me, opp := &s.Table.Seats[0], &s.Table.Seats[1]
		me.Stack, me.Bet, me.Committed = 3820, 0, 1980
		opp.Stack, opp.Bet, opp.Committed = 3000, 40, 2020
		s.Table.TotalPot = 6070
		*s.Table.Legal = engine.Legal{Fold: true, Call: true, CallAmount: 40, Raise: true, MinTo: 80, MaxTo: 3820, AllIn: true, AllInTo: 3820}
	})
	v := stripANSI(Render(f, testRenderer(), 100, 30))
	if strings.Contains(v, "pot odds 0%") {
		t.Errorf("a real price shows as 'pot odds 0%%'\n%s", v)
	}
	if !strings.Contains(v, "pot odds <1%") {
		t.Errorf("want 'pot odds <1%%'\n%s", v)
	}
}
