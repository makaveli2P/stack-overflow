package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// The Stats tab takes the money from the settle balance, which takes the
// rounding remainder, so that it agrees with the Settle tab.
func TestStatsMoneyMatchesSettleBalance(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard stats tab"))
	s := h.m.snap
	s.Config.ChipsPerUnit = 3
	s.Config.Currency = "₹"
	s.Me.Name = "zed"
	s.Stats = []server.PlayerStats{
		{Name: "alice", Hands: 3, PeriodNet: 1, LifetimeNet: 1},
		{Name: "bob", Hands: 3, PeriodNet: 1, LifetimeNet: 1},
		{Name: "carol", Hands: 3, PeriodNet: -2, LifetimeNet: -2},
	}
	// Exactly what server.moneyBalances/greedyTransfers give for these nets
	// (server test case "cpu 3: rounding leaves -1, the largest debt takes it").
	s.Settle = server.Settlement{
		Period: 1, ChipsPerUnit: 3, Currency: "₹",
		Balances: []server.Balance{
			{Name: "alice", Chips: 1, Money: 33},
			{Name: "bob", Chips: 1, Money: 33},
			{Name: "carol", Chips: -2, Money: -66},
		},
		Transfers: []server.Transfer{
			{From: "carol", To: "alice", Amount: 33},
			{From: "carol", To: "bob", Amount: 33},
		},
	}
	h.snapshot(s)
	h.press("d")
	view := stripANSI(h.viewAt(140, 40))
	var carol string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "carol") {
			carol = l
			break
		}
	}
	if carol == "" {
		t.Fatalf("no carol row\n%s", view)
	}
	if !strings.Contains(carol, "-₹0.66") {
		t.Fatalf("Stats tab shows carol as %q; the Settle tab says carol pays ₹0.66", strings.TrimSpace(carol))
	}
	for h.m.dash.row < 2 {
		h.press("down")
	}
	h.press("enter")
	if v := stripANSI(h.viewAt(140, 40)); !strings.Contains(v, "-₹0.66") {
		t.Errorf("the detail card of carol must show -₹0.66\n%s", v)
	}
}

// An admin presses M during a hand, types yes, and the server refuses
// ("wait until the hand ends"). The dashboard must not stay stuck on
// "settling…": Esc must cancel and D must go back to the table.
func TestSettleErrorDoesNotLockDashboard(t *testing.T) {
	f := fixture(t, "admin types yes")
	f.Keys = nil
	h := newHarness(t, f)
	h.sess.err = errors.New("wait until the hand ends")
	h.press("d", "tab", "tab", "m", "y", "e", "s", "enter")
	h.wantCalls("MarkSettled")
	if v := stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "wait until the hand ends") || strings.Contains(v, "settling…") {
		t.Errorf("the settle footer must show the error, not \"settling…\":\n%s", v)
	}
	h.press("esc")
	if h.m.dash.confirm {
		v := strings.Split(strings.TrimRight(stripANSI(h.viewAt(100, 30)), "\n"), "\n")
		t.Errorf("Esc after a failed MarkSettled must cancel the confirmation; screen still shows:\n%s",
			strings.Join(v[len(v)-4:], "\n"))
	}
	h.press("d")
	if h.m.mode != modeTable {
		t.Errorf("D after a failed MarkSettled must close the dashboard; mode = %v", h.m.mode)
	}
}

// After one successful MarkSettled, the next period's M confirmation
// must accept typing again.
func TestSecondSettleAcceptsTyping(t *testing.T) {
	f := fixture(t, "admin types yes")
	f.Keys = nil
	h := newHarness(t, f)
	h.press("d", "tab", "tab", "m", "y", "e", "s", "enter")
	h.wantCalls("MarkSettled")
	h.press("m", "y", "e", "s")
	if got := h.m.dash.yes.String(); got != "yes" {
		t.Errorf("second settle: typed field = %q, want %q (keys ignored, busy never reset)", got, "yes")
	}
	h.press("esc")
	if h.m.dash.confirm {
		t.Error("second settle: Esc must cancel the confirmation")
	}
}

// A player opens the detail card of Priya. A hand ends and the server
// sends the stats in a new order (it sorts by period net). The card must
// still show Priya, not whoever moved into her row.
func TestDetailCardFollowsPlayerAcrossResort(t *testing.T) {
	f := fixture(t, "dashboard detail card")
	f.Keys = nil
	h := newHarness(t, f)
	h.press("d")
	for h.m.dash.row > 0 {
		h.press("up")
	}
	h.press("enter")
	if !h.m.dash.detail {
		t.Fatal("Enter must open the detail card")
	}
	if v := stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "PRIYA") {
		t.Fatalf("setup: the card must show Priya\n%s", v)
	}
	s := h.m.snap
	s.Stats = append(s.Stats[1:len(s.Stats):len(s.Stats)], s.Stats[0]) // Priya drops to the last row
	h.snapshot(s)
	v := stripANSI(h.viewAt(100, 30))
	if !strings.Contains(v, "PRIYA") {
		t.Errorf("after the stats re-sorted, the open detail card switched to another player:\n%s", v)
	}

	// Priya leaves the stats: the card closes instead of showing another player.
	s.Stats = s.Stats[:len(s.Stats)-1]
	h.snapshot(s)
	if h.m.dash.detail {
		t.Errorf("the detail card must close when its player is gone:\n%s", stripANSI(h.viewAt(100, 30)))
	}
}

// The welcome screen types L into the name field. Only Tab and Ctrl+L
// open the link form. README must name these keys and not L.
func TestReadmeLinkKeyMatchesWelcomeScreen(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "push `L` on the welcome screen") {
		t.Error("README tells players to push L on the welcome screen, but L types into the name")
	}
	if !strings.Contains(string(readme), "| Tab or Ctrl+L | Link your SSH key") {
		t.Error("the README welcome screen key table does not name Tab or Ctrl+L for the link form")
	}
	for _, k := range []tea.KeyMsg{keyMsg("tab"), {Type: tea.KeyCtrlL}} {
		h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
		_, cmd := h.m.Update(k)
		h.run(cmd)
		if h.m.mode != modeLink {
			t.Errorf("%s on the welcome screen: mode = %v, want the link form", k, h.m.mode)
		}
	}
	h := newHarness(t, fixture(t, "welcome screen, new SSH key"))
	h.press("L")
	if h.m.mode == modeLink || !strings.Contains(h.m.forms.name.String(), "L") {
		t.Errorf("L on the welcome screen must type into the name: field = %q, mode = %v",
			h.m.forms.name.String(), h.m.mode)
	}
}

// Between hands the engine sends InHand=false and Folded=false for every
// seat (engine/view.go seatView: InHand is set only while the hand runs).
// The viewer's own box must not say "waiting for a hand" in the result
// window, also not for the player who just won the pot.
func TestResultWindowDoesNotSayWaiting(t *testing.T) {
	f := edited(t, "winner may show", func(s *server.Snapshot) {
		for i := range s.Table.Seats {
			s.Table.Seats[i].InHand = false
			s.Table.Seats[i].Folded = false
		}
		s.Table.InHand = false
	})
	for _, sz := range allSizes {
		v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
		if strings.Contains(v, "waiting for a hand") {
			t.Errorf("%dx%d: the winner's own box says \"waiting for a hand\" in the result window:\n%s", sz[0], sz[1], v)
		}
	}
}

// The M box is open when the turn comes. The action bar under it shows
// "R Raise", so R must open the raise editor and not the rename field.
func TestProfileRRaisesOnTurn(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up"))
	h.press("m")
	v := stripANSI(h.viewAt(100, 30))
	if !strings.Contains(v, "Raise") {
		t.Fatalf("setup: the action bar must show under the profile box\n%s", v)
	}
	if strings.Contains(v, "R rename") {
		t.Errorf("on the viewer's turn the profile box must not offer R rename\n%s", v)
	}
	h.press("r")
	if h.m.mode != modeRaise {
		t.Errorf("on the viewer's turn, R under the visible action bar must open the raise editor; mode = %v (rename)", h.m.mode)
	}
}

// L in a profile box that shows no "L link PIN" only closes the box.
func TestProfileLWithoutLinkOnlyCloses(t *testing.T) {
	f := fixture(t, "profile box")
	f.Keys = nil
	f.Snap.Me.CanLink = false
	h := newHarness(t, f)
	h.press("m", "l")
	h.wantCalls()
	if h.m.mode != modeTable {
		t.Errorf("L must close the profile box; mode = %v", h.m.mode)
	}
}

// The turn notification names the last action. A room line in the log
// (someone sits down or renames mid-hand) must not replace it.
func TestTurnAlertSkipsRoomLines(t *testing.T) {
	for _, c := range []struct {
		lines []string
		want  string
	}{
		{[]string{"Priya bets 120", "Omar sits down with 1000"}, "Priya bets 120"},
		{[]string{"Priya bets 120", "erin joined", "erin is now Carol W"}, "Priya bets 120"},
		{[]string{"Bob timed out", "Bob folds", "Bob sits out"}, "Bob folds"},
	} {
		h, log, snap := alertHarness(t)
		h.snapshot(snap)
		log.take()
		s := snap
		s.Log = append(append([]string(nil), s.Log...), c.lines...)
		s.Alert = "turn"
		h.m.blurred = true
		h.snapshot(s)
		got := log.take()
		if len(got) != 1 {
			t.Fatalf("alerts = %v, want one", got)
		}
		if got[0].Body != "Your turn — "+c.want {
			t.Errorf("log %q: turn notification = %q, want it to name the last action %q", c.lines, got[0].Body, c.want)
		}
	}
}
