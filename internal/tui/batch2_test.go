package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

var allSizes = [][2]int{{80, 24}, {100, 30}, {140, 40}}

// wantFits fails when the view is larger than w x h.
func wantFits(t *testing.T, name, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("%s at %dx%d: %d lines", name, w, h, len(lines))
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("%s at %dx%d: line %d is %d cells wide: %q", name, w, h, i, lw, stripANSI(l))
		}
	}
}

// viewAt draws the harness model at w x h.
func (h *harness) viewAt(w, ht int) string {
	h.m.width, h.m.height = w, ht
	return h.m.View()
}

// ---- 1. amount editor ----

func TestEditorCheckAndFold(t *testing.T) {
	h := newHarness(t, fixture(t, "to bet"))
	h.press("b", "4", "c")
	h.wantAction(engine.Action{Type: engine.Check})

	h = newHarness(t, fixture(t, "to bet"))
	h.press("b", "f")
	h.wantAction(engine.Action{Type: engine.Fold})
}

func TestEditorDigitsNeverPickPresets(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("r", "3")
	if h.m.raise.amount != 3 || !h.m.raise.typing {
		t.Fatalf("3 must type a digit, amount = %d", h.m.raise.amount)
	}
	h.press("h")
	if h.m.raise.amount != 40 || h.m.raise.typing {
		t.Fatalf("h after typing must pick ½ pot, amount = %d", h.m.raise.amount)
	}
}

func TestEditorShowsAllIn(t *testing.T) {
	f := fixture(t, "editor, a preset")
	for _, sz := range allSizes {
		if v := stripANSI(Render(f, testRenderer(), sz[0], sz[1])); !strings.Contains(v, "ALL-IN") {
			t.Errorf("%dx%d: amount = MaxTo must show ALL-IN\n%s", sz[0], sz[1], v)
		}
	}
}

func TestPotRaiseMath(t *testing.T) {
	// Facing a bet of 100 into a pot of 250 (150 before the bet), with
	// nothing in yet: call 100, pot after calling 350, pot raise to 450.
	rm := raiseMath{legal: engine.Legal{Raise: true, CallAmount: 100, MinTo: 200, MaxTo: 5000, AllInTo: 5000}, pot: 250, bigBet: 20}
	for p, want := range map[potShare]int64{{1, 1}: 450, {1, 2}: 275, {2, 3}: 333} {
		if got := rm.potTo(p); got != want {
			t.Errorf("potTo(%d/%d) = %d, want %d", p.num, p.den, got, want)
		}
	}
}

// ---- 2. hand hint ----

func TestHandHint(t *testing.T) {
	cases := []struct{ fixture, want string }{
		{"hand hint on the turn", "Two Pair, Kings and Sevens"},
		{"hand hint on the river", "Flush, Ace high"},
		{"6 seats, flop", "Three of a Kind, Queens"},
		{"heads-up", "Ace-King suited"},
	}
	for _, c := range cases {
		for _, sz := range allSizes {
			v := stripANSI(Render(fixture(t, c.fixture), testRenderer(), sz[0], sz[1]))
			if !strings.Contains(v, c.want) {
				t.Errorf("%s at %dx%d: no %q\n%s", c.fixture, sz[0], sz[1], c.want, v)
			}
		}
	}
	// A spectator never sees a hand name for hidden cards.
	v := stripANSI(Render(fixture(t, "spectator"), testRenderer(), 140, 40))
	for _, name := range []string{"Three of a Kind", "Pair of", "High Card"} {
		if strings.Contains(v, name) {
			t.Errorf("spectator sees %q\n%s", name, v)
		}
	}
}

// After a showdown that the viewer lost, the viewer's box names the hand
// once (under the cards), not also in the status column beside them.
func TestHandHintNotTwiceAfterShowdown(t *testing.T) {
	f := fixture(t, "9 seats, showdown result")
	f.Snap.Me = server.Me{Key: "key-Priya", Name: "Priya", Seat: 1}
	f.Snap.Table.MySeat = 1
	for _, sz := range allSizes {
		v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
		if n := strings.Count(v, "Pair of Kings"); n != 1 {
			t.Errorf("%dx%d: %q shows %d times, want 1\n%s", sz[0], sz[1], "Pair of Kings", n, v)
		}
	}
}

// The hint marks only the cards that make the category of the hand (Card
// stability): for Two Pair the four paired cards, not the kicker. The mark
// is on the card face; the card edge stays the same.
func TestHandHintMarksCards(t *testing.T) {
	h := newHarness(t, fixture(t, "hand hint on the turn"))
	hl := h.m.highlights()
	for _, c := range parseCards("Kh 7c Ks 7d") {
		if hl[c] != hlHint {
			t.Errorf("%v is part of the two pair but has no mark", c)
		}
	}
	for _, c := range parseCards("9h 2c") {
		if hl[c] != hlNone {
			t.Errorf("%v is a kicker or not in the best hand, but has a mark", c)
		}
	}
	v := h.viewAt(140, 40)
	if strings.Contains(v, "┏") || strings.Contains(v, "┃") {
		t.Error("full mode draws a heavy card edge for the hint")
	}
}

// ---- 3 and 4. dashboard and settle up ----

func TestDashboardKeys(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard stats tab"))
	h.press("d")
	if h.m.mode != modeDash || h.m.dash.tab != dashStats {
		t.Fatalf("D must open the stats tab, mode = %v", h.m.mode)
	}
	if got := h.m.snap.Stats[h.m.dash.row].Name; got != "Devon" {
		t.Errorf("the cursor starts on %q, want the viewer", got)
	}
	h.press("up", "up", "up")
	if h.m.dash.row != 0 {
		t.Errorf("row = %d, want 0", h.m.dash.row)
	}
	h.press("enter")
	v := stripANSI(h.viewAt(80, 24))
	for _, want := range []string{"PRIYA", "VPIP", "put chips in preflop by choice", "Full House, Kings over Sevens", "K♠", "W$SD"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail card lacks %q\n%s", want, v)
		}
	}
	h.press("esc")
	if h.m.mode != modeDash || h.m.dash.detail {
		t.Fatal("Esc must close the detail card only")
	}
	h.press("right")
	if h.m.dash.tab != dashFun {
		t.Fatal("→ must switch to the fun tab")
	}
	h.press("right")
	if h.m.dash.tab != dashSettle {
		t.Fatal("→ → must switch to the settle tab")
	}
	v = stripANSI(h.viewAt(100, 30))
	for _, want := range []string{"Sam pays Priya ₹350", "Kim pays Marta ₹37.50", "Period 2", "Leo paid Sam ₹1,250.50", "receives", "pays"} {
		if !strings.Contains(v, want) {
			t.Errorf("settle tab lacks %q\n%s", want, v)
		}
	}
	h.press("m")
	h.wantCalls()
	if h.m.dash.confirm {
		t.Fatal("a player who is not admin must not get the settle confirmation")
	}
	h.press("D")
	if h.m.mode != modeTable {
		t.Fatal("D must close the dashboard")
	}
}

func TestDashboardColumnsDropByPriority(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard stats tab"))
	names := func(w int) string {
		var out []string
		for _, c := range pickCols(h.m.statCols(), w) {
			out = append(out, c.head)
		}
		return strings.Join(out, ",")
	}
	if got := names(76); got != "Player,Net,₹,Hands,Won,VPIP,PFR,WTSD,W$SD" {
		t.Errorf("76 cells: %s", got)
	}
	if got := names(200); !strings.HasSuffix(got, "Best hand,All-ins,Buy-ins,Lifetime") {
		t.Errorf("200 cells: %s", got)
	}
	if pct(3, 0) != "—" || pct(1, 3) != "33%" || pct(2, 3) != "67%" {
		t.Error("pct")
	}
}

func TestMarkSettledNeedsYes(t *testing.T) {
	f := fixture(t, "admin types yes")
	f.Keys = nil
	h := newHarness(t, f)
	h.press("d", "tab", "tab", "m")
	if !h.m.dash.confirm {
		t.Fatal("M must ask the admin to confirm")
	}
	h.press("n", "o", "enter")
	h.wantCalls()
	if !strings.Contains(stripANSI(h.viewAt(100, 30)), "Type yes to confirm") {
		t.Error("a wrong word must say how to confirm")
	}
	h.press("esc")
	if h.m.dash.confirm {
		t.Fatal("Esc must cancel")
	}
	h.press("m", "Y", "e", "s", "enter")
	h.wantCalls("MarkSettled")
	if h.m.dash.confirm {
		t.Error("the confirmation must close after the call")
	}
}

func TestMoney(t *testing.T) {
	cases := []struct {
		m    server.Money
		cur  string
		want string
	}{
		{35000, "₹", "₹350"}, {3750, "₹", "₹37.50"}, {-7025, "₹", "-₹70.25"},
		{12345650, "₹", "₹1,23,456.50"}, {123456700, "₹", "₹12,34,567"},
		{123456700, "$", "$1,234,567"}, {5, "€", "€0.05"}, {0, "₹", "₹0"},
	}
	for _, c := range cases {
		if got := money(c.m, c.cur); got != c.want {
			t.Errorf("money(%d, %s) = %q, want %q", c.m, c.cur, got, c.want)
		}
	}
	for _, c := range []struct {
		chips, per int64
		want       server.Money
	}{{75, 2, 3750}, {1, 3, 33}, {2, 3, 67}, {-2, 3, -67}, {1, 200, 1}, {-1, 200, -1}, {10, 0, 1000}} {
		if got := toMoney(c.chips, c.per); got != c.want {
			t.Errorf("toMoney(%d, %d) = %d, want %d", c.chips, c.per, got, c.want)
		}
	}
}

// ---- 5. rabbit hunt ----

func TestRabbitHunt(t *testing.T) {
	h := newHarness(t, fixture(t, "winner may show"))
	h.press("h")
	h.wantCalls("RabbitHunt")

	h = newHarness(t, edited(t, "winner may show", func(s *server.Snapshot) {
		s.Table.MySeat = -1
		s.Me.Seat = -1
	}))
	h.press("h")
	h.wantCalls()

	v := stripANSI(Render(fixture(t, "rabbit hunt cards"), testRenderer(), 140, 40))
	for _, want := range []string{"rabbit", "Q♣", "2♠", "╭┄┄┄╮"} {
		if !strings.Contains(v, want) {
			t.Errorf("rabbit view lacks %q\n%s", want, v)
		}
	}
}

// ---- 6. winner shows ----

func TestWinnerShows(t *testing.T) {
	for key, want := range map[string]string{"1": "ShowCards(true,false)", "2": "ShowCards(false,true)", "3": "ShowCards(true,true)"} {
		h := newHarness(t, fixture(t, "winner may show"))
		if v := stripANSI(h.m.View()); !strings.Contains(v, "Show your cards?") {
			t.Fatalf("the winner gets no show prompt\n%s", v)
		}
		h.press(key)
		h.wantCalls(want)
	}
	// After one card is shown, 3 shows only the other one.
	h := newHarness(t, edited(t, "winner may show", func(s *server.Snapshot) {
		r := *s.Table.Result
		r.Revealed = []engine.Shown{{Seat: 3, PlayerID: "key-Marta", Cards: parseCards("As")}}
		s.Table.Result = &r
	}))
	h.press("1", "3")
	h.wantCalls("ShowCards(false,true)")

	// Not the winner, and not after a showdown.
	for _, name := range []string{"rabbit hunt cards", "showdown"} {
		h := newHarness(t, fixture(t, name))
		h.press("1", "2", "3")
		h.wantCalls()
	}
}

func TestRevealedCardsShowForEveryone(t *testing.T) {
	f := fixture(t, "winner showed one card")
	for _, sz := range allSizes {
		v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
		if !strings.Contains(v, "A♠") || !strings.Contains(v, "shown") {
			t.Errorf("%dx%d: the shown card or the mark is missing\n%s", sz[0], sz[1], v)
		}
	}
}

// ---- 7. alerts ----

type alertLog struct {
	mu sync.Mutex
	as []Alert
}

func (l *alertLog) add(a Alert) { l.mu.Lock(); l.as = append(l.as, a); l.mu.Unlock() }
func (l *alertLog) take() []Alert {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.as
	l.as = nil
	return out
}

func alertHarness(t *testing.T) (*harness, *alertLog, server.Snapshot) {
	f := fixture(t, "6 seats, flop")
	f.Snap.Me.Alerts = true
	log := &alertLog{}
	h := &harness{t: t, sess: newFake(), now: f.Snap.Now}
	h.m = newModelWith(h.sess, testRenderer(), 100, 30, func() time.Time { return h.now })
	h.m.alert = log.add
	return h, log, f.Snap
}

func (h *harness) snapshot(s server.Snapshot) {
	_, cmd := h.m.Update(snapshotMsg{s})
	h.runNoWait(cmd)
}

// runNoWait runs the commands of an update, except the one that waits
// for the next snapshot on the channel.
func (h *harness) runNoWait(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.runNoWait(c)
			}
		}
	case <-time.After(50 * time.Millisecond):
		// waitSnapshot blocks on the empty channel; ignore it.
	}
}

func TestAlerts(t *testing.T) {
	h, log, snap := alertHarness(t)
	h.snapshot(snap)
	if got := log.take(); len(got) != 0 {
		t.Fatalf("a snapshot without a cue sent %v", got)
	}
	turn := snap
	turn.Alert = "turn"
	h.snapshot(turn)
	got := log.take()
	if len(got) != 1 {
		t.Fatalf("turn cue sent %d alerts, want 1", len(got))
	}
	if a := got[0]; !a.Bell || a.Notify || a.Title != "Stack Overflow" || !strings.HasPrefix(a.Body, "Your turn") {
		t.Errorf("turn alert = %+v", a)
	}
	h.snapshot(snap) // the next snapshot has no cue
	if got := log.take(); len(got) != 0 {
		t.Fatalf("one cue sent alerts twice: %v", got)
	}

	h.m.Update(tea.BlurMsg{})
	hurry := snap
	hurry.Alert = "hurry"
	h.snapshot(hurry)
	got = log.take()
	if len(got) != 1 || !got[0].Bell || !got[0].Notify || got[0].Body != "5 seconds left" {
		t.Fatalf("hurry alert while blurred = %+v", got)
	}
	h.m.Update(tea.FocusMsg{})
	h.snapshot(hurry)
	if got = log.take(); len(got) != 1 || got[0].Notify {
		t.Fatalf("focused: want a bell only, got %+v", got)
	}

	off := turn
	off.Me.Alerts = false
	h.snapshot(off)
	if got := log.take(); len(got) != 0 {
		t.Fatalf("alerts are off, but %v were sent", got)
	}

	// No Alert hook: nothing happens.
	h.m.alert = nil
	h.snapshot(turn)
}

func TestTurnAlertNamesLastAction(t *testing.T) {
	h, log, snap := alertHarness(t)
	snap.Log = append(append([]string(nil), snap.Log...), "Priya posts small blind 10", "Sam raises to 60", "Bob joined")
	snap.Alert = "turn"
	h.snapshot(snap)
	got := log.take()
	if len(got) != 1 || got[0].Body != "Your turn — Sam raises to 60" {
		t.Fatalf("alert = %+v", got)
	}
}

func TestToggleAlerts(t *testing.T) {
	f := fixture(t, "profile box")
	f.Keys = nil
	h := newHarness(t, f)
	h.press("n")
	h.wantCalls("SetAlerts(false)")
	if !strings.Contains(h.m.notice.text, "Alerts off") {
		t.Errorf("notice = %q", h.m.notice.text)
	}
}

// ---- 8. branding ----

func TestBranding(t *testing.T) {
	h := newHarness(t, fixture(t, "6 seats, flop"))
	if v := stripANSI(h.m.View()); !strings.Contains(v, "STACK OVERFLOW") || strings.Contains(v, "Office Poker") {
		t.Errorf("header must say STACK OVERFLOW\n%s", v)
	}
	h.press("?")
	if v := stripANSI(h.viewAt(80, 24)); !strings.Contains(v, "S T A C K") {
		t.Errorf("help must show the brand\n%s", v)
	}
	if appTitle != "Stack Overflow" {
		t.Errorf("appTitle = %q", appTitle)
	}
	m := newModelWith(newFake(), testRenderer(), 80, 24, time.Now)
	if v := stripANSI(m.View()); !strings.Contains(v, "STACK OVERFLOW") {
		t.Errorf("connecting screen must show the brand\n%s", v)
	}
}

// ---- 9. welcome, link, profile ----

func TestWelcomeSetsName(t *testing.T) {
	h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
	if h.m.mode != modeWelcome {
		t.Fatalf("mode = %v, want the welcome screen", h.m.mode)
	}
	v := stripANSI(h.m.View())
	for _, want := range []string{"STACK OVERFLOW", "Pick the name others see", "devon", "Played before with a PIN?"} {
		if !strings.Contains(v, want) {
			t.Errorf("welcome lacks %q\n%s", want, v)
		}
	}
	// Table keys type into the name.
	h.press("s", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "Z", "e", "d", "enter")
	h.wantCalls("SetName(Zed)")
	if h.m.mode != modeTable || !h.m.welcomeDone {
		t.Fatalf("mode = %v after the name is saved", h.m.mode)
	}
	// A late snapshot that still says NeedsName does not open it again.
	h.m.Update(snapshotMsg{h.m.snap})
	if h.m.mode != modeTable {
		t.Fatal("the welcome screen opened twice")
	}
}

func TestWelcomeEscKeepsName(t *testing.T) {
	h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.press("x", "esc")
	h.wantCalls("SetName(devon)")
}

func TestWelcomeShowsError(t *testing.T) {
	h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.sess.err = server.ErrNameInUse
	h.press("enter")
	if h.m.mode != modeWelcome {
		t.Fatal("a failed SetName must keep the welcome screen")
	}
	for _, sz := range allSizes {
		v := h.viewAt(sz[0], sz[1])
		wantFits(t, "welcome error", v, sz[0], sz[1])
		if !strings.Contains(stripANSI(v), server.ErrNameInUse.Error()) {
			t.Errorf("%dx%d: no error text\n%s", sz[0], sz[1], stripANSI(v))
		}
	}
	h.sess.err = nil
	h.press("enter")
	h.wantCalls("SetName(devon)", "SetName(devon)")
}

func TestWelcomeKeylessTip(t *testing.T) {
	f := fixture(t, "welcome screen, PIN login")
	for _, sz := range allSizes {
		v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
		if !strings.Contains(v, "ssh-keygen -t ed25519") || strings.Contains(v, "Played before") {
			t.Errorf("%dx%d: want the ssh-keygen tip and no link offer\n%s", sz[0], sz[1], v)
		}
	}
	h := newHarness(t, f)
	h.press("tab")
	if h.m.mode != modeWelcome {
		t.Error("Tab must not open the link form when CanLink is false")
	}
}

func TestLinkPIN(t *testing.T) {
	h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.press("tab")
	if h.m.mode != modeLink {
		t.Fatalf("Tab must open the link form, mode = %v", h.m.mode)
	}
	h.press("Devon ", "enter", "1", "2", "enter")
	h.wantCalls()
	if h.m.forms.err == "" {
		t.Error("a short PIN must show an error")
	}
	v := stripANSI(h.m.View())
	if strings.Contains(v, "12") || !strings.Contains(v, "••") {
		t.Errorf("the PIN must be masked\n%s", v)
	}
	h.press("x", "3", "4", "5", "enter")
	h.wantCalls("LinkPIN(devon,1234)")
	if h.m.mode != modeTable || !h.m.welcomeDone {
		t.Errorf("mode = %v after the link", h.m.mode)
	}

	// Esc goes back to the welcome screen.
	h = newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.press("tab", "esc")
	if h.m.mode != modeWelcome {
		t.Errorf("Esc from the link form: mode = %v", h.m.mode)
	}

	// A wrong PIN keeps the form open with the error.
	h = newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.sess.err = server.ErrBadPIN
	h.press("tab", "devon", "tab", "9999", "enter")
	if h.m.mode != modeLink || !strings.Contains(stripANSI(h.m.View()), "wrong PIN") {
		t.Errorf("wrong PIN: mode = %v\n%s", h.m.mode, stripANSI(h.m.View()))
	}
	for _, sz := range allSizes {
		wantFits(t, "link error", h.viewAt(sz[0], sz[1]), sz[0], sz[1])
	}
}

func TestProfileBox(t *testing.T) {
	f := fixture(t, "profile box")
	f.Keys = nil
	h := newHarness(t, f)
	h.press("m")
	if h.m.mode != modeProfile {
		t.Fatal("M must open the profile box")
	}
	v := stripANSI(h.m.View())
	for _, want := range []string{"MY PROFILE", "SSH key", "Alerts", "on"} {
		if !strings.Contains(v, want) {
			t.Errorf("profile lacks %q\n%s", want, v)
		}
	}
	h.press("n")
	h.wantCalls("SetAlerts(false)")
	h.press("r", "ctrl+u", "D", "J", "enter")
	h.wantCalls("SetAlerts(false)", "SetName(DJ)")
	if h.m.mode != modeTable {
		t.Errorf("mode = %v after rename", h.m.mode)
	}
	h.press("m", "r", "esc")
	if h.m.mode != modeProfile {
		t.Errorf("Esc in rename must go back to the box, mode = %v", h.m.mode)
	}
	h.press("esc")
	if h.m.mode != modeTable {
		t.Error("Esc must close the box")
	}

	// A PIN login sees the tip.
	pin := fixture(t, "profile box rename, PIN login")
	for _, sz := range allSizes {
		v := stripANSI(Render(pin, testRenderer(), sz[0], sz[1]))
		if !strings.Contains(v, "PIN") || !strings.Contains(v, "ssh-keygen") {
			t.Errorf("%dx%d: PIN profile lacks the login type or the tip\n%s", sz[0], sz[1], v)
		}
	}
}

func TestProfilePassesActionKeys(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("m", "c")
	h.wantAction(engine.Action{Type: engine.Call})
	if h.m.mode != modeTable {
		t.Error("an action key must close the profile box")
	}
}

func TestRenameError(t *testing.T) {
	f := fixture(t, "profile box")
	f.Keys = nil
	h := newHarness(t, f)
	h.sess.err = server.ErrBadName
	h.press("m", "r", "!", "enter")
	if h.m.mode != modeRename {
		t.Fatal("a failed rename must keep the input open")
	}
	for _, sz := range allSizes {
		v := h.viewAt(sz[0], sz[1])
		wantFits(t, "rename error", v, sz[0], sz[1])
		if !strings.Contains(stripANSI(v), "name must be") {
			t.Errorf("%dx%d: no error\n%s", sz[0], sz[1], stripANSI(v))
		}
	}
}

// Every Session call runs in a tea.Cmd: a session whose methods block
// must not block Update.
func TestSessionCallsNeverBlockUpdate(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	s := &blockingSession{fakeSession: newFake(), block: block}
	f := fixture(t, "winner may show")
	m := newModelWith(s, testRenderer(), 100, 30, func() time.Time { return f.Snap.Now })
	m.Update(snapshotMsg{f.Snap})
	done := make(chan struct{})
	go func() {
		for _, k := range []string{"h", "3", "n", "m", "r", "enter", "d", "tab", "esc"} {
			m.Update(keyMsg(k))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on a Session call")
	}
}

type blockingSession struct {
	*fakeSession
	block chan struct{}
}

func (b *blockingSession) wait() error               { <-b.block; return nil }
func (b *blockingSession) RabbitHunt() error         { return b.wait() }
func (b *blockingSession) ShowCards(_, _ bool) error { return b.wait() }
func (b *blockingSession) SetAlerts(bool) error      { return b.wait() }
func (b *blockingSession) SetName(string) error      { return b.wait() }
func (b *blockingSession) MarkSettled() error        { return b.wait() }

// Every new screen state fits in every terminal size.
func TestBatch2StatesFit(t *testing.T) {
	states := []struct {
		fixture string
		keys    []string
	}{
		{"dashboard stats tab", []string{"d", "down", "down", "down", "down", "down", "down", "down"}},
		{"dashboard stats tab", []string{"d", "enter", "down", "down", "down"}},
		{"admin types yes", []string{"d", "tab", "tab", "m", "x", "enter"}},
		{"dashboard settle up", []string{"d", "tab", "tab", "down", "down", "down", "down", "down", "down", "down", "down"}},
		{"heads-up", []string{"d"}},
		{"heads-up", []string{"m"}},
		{"welcome screen, new SSH key", []string{"tab", "averyveryverylongloginname1234567"}},
		{"winner may show", []string{"?"}},
	}
	for _, s := range states {
		for _, sz := range append(allSizes, [2]int{60, 18}, [2]int{90, 26}) {
			h := newHarness(t, fixture(t, s.fixture))
			h.press(s.keys...)
			wantFits(t, fmt.Sprintf("%s %v", s.fixture, s.keys), h.viewAt(sz[0], sz[1]), sz[0], sz[1])
		}
	}
}

// The dashboard says when it is the viewer's turn.
func TestDashboardShowsTurn(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("d")
	if v := stripANSI(h.viewAt(80, 24)); !strings.Contains(v, "YOUR TURN") {
		t.Errorf("dashboard hides the turn\n%s", v)
	}
}

func TestStartingHandNames(t *testing.T) {
	for in, want := range map[string]string{"Kh Kd": "Pocket Kings", "Ah Kh": "Ace-King suited", "7c Qd": "Queen-Seven offsuit"} {
		cs := parseCards(in)
		if got := startingHand(cs[0], cs[1]); got != want {
			t.Errorf("startingHand(%s) = %q, want %q", in, got, want)
		}
	}
}
