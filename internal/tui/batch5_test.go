package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// ---- batch 5: buy-in picker ----

// watching returns the spectator fixture with bank chips in the bank and
// the default price of 2 chips for ₹1. The buy-in is 2,000.
func watching(t *testing.T, bank int64) Fixture {
	t.Helper()
	return edited(t, "watching with a bank", func(s *server.Snapshot) { s.Me.Bank = bank })
}

// With an empty bank, S buys in at once and opens no picker.
func TestSitWithEmptyBankSitsAtOnce(t *testing.T) {
	h := newHarness(t, watching(t, 0))
	h.press("S")
	h.wantCalls("SitWith(-1,buyin)")
	if h.m.mode != modeTable {
		t.Errorf("mode = %v, want the table", h.m.mode)
	}
}

// With chips in the bank, S opens the picker and calls nothing yet.
func TestSitWithBankOpensPicker(t *testing.T) {
	for _, bank := range []int64{1, 300, 2000, 3450} {
		h := newHarness(t, watching(t, bank))
		h.press("s")
		h.wantCalls()
		if h.m.mode != modeSit {
			t.Errorf("bank %d: mode = %v, want the picker", bank, h.m.mode)
		}
	}
}

// A seated player gets no picker from S.
func TestSitPickerNotWhenSeated(t *testing.T) {
	f := edited(t, "alone", func(s *server.Snapshot) { s.Me.Bank = 5000 })
	h := newHarness(t, f)
	h.press("s")
	h.wantCalls()
	if h.m.mode != modeTable {
		t.Errorf("mode = %v, want the table", h.m.mode)
	}
}

// 1, 2 and Enter call SitWith with the right choice.
func TestSitPickerChoices(t *testing.T) {
	cases := []struct {
		keys []string
		want string
	}{
		{[]string{"s", "1"}, "SitWith(-1,buyin)"},
		{[]string{"s", "2"}, "SitWith(-1,bank)"},
		{[]string{"s", "enter"}, "SitWith(-1,buyin)"},
		{[]string{"s", "down", "enter"}, "SitWith(-1,bank)"},
		{[]string{"s", "down", "down", "enter"}, "SitWith(-1,buyin)"},
		{[]string{"s", "up", "enter"}, "SitWith(-1,bank)"},
		{[]string{"s", "tab", "enter"}, "SitWith(-1,bank)"},
		{[]string{"s", "down", "1"}, "SitWith(-1,buyin)"},
		{[]string{"s2"}, "SitWith(-1,bank)"}, // fast typing in one message
	}
	for _, c := range cases {
		for _, bank := range []int64{300, 3450} {
			h := newHarness(t, watching(t, bank))
			h.press(c.keys...)
			h.wantCalls(c.want)
			if h.m.mode != modeTable {
				t.Errorf("%v bank %d: the picker stays open after the call", c.keys, bank)
			}
		}
	}
}

// Esc closes the picker and calls nothing.
func TestSitPickerEscCloses(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s", "down", "esc")
	h.wantCalls()
	if h.m.mode != modeTable {
		t.Errorf("mode = %v, want the table", h.m.mode)
	}
	if v := stripANSI(h.viewAt(80, 24)); strings.Contains(v, "SIT DOWN WITH") {
		t.Errorf("the picker shows after Esc\n%s", v)
	}
	// S opens it again with the first option highlighted.
	h.press("s", "enter")
	h.wantCalls("SitWith(-1,buyin)")
}

// Keys that work on the table do nothing while the picker is open.
func TestSitPickerKeysDoNotLeak(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s")
	tab := h.m.tab
	for _, k := range []string{"q", "Q", "d", "3", "0", "t", "/", "m", "?", "l", "o", "b", "B", "h", "a", "f", "c", "r", "e", "n", "s", "x", " ", "backspace", "left", "right"} {
		h.press(k)
		if h.m.mode != modeSit {
			t.Fatalf("key %q closed the picker (mode %v)", k, h.m.mode)
		}
	}
	h.wantCalls()
	if h.quitSent() {
		t.Error("Q quit from the picker")
	}
	if h.m.help || h.m.tab != tab {
		t.Errorf("a key reached the table: help %v, tab %v", h.m.help, h.m.tab)
	}
	// Ctrl+C still quits: it is not in a hand.
	h.press("ctrl+c")
	if !h.quitSent() {
		t.Error("Ctrl+C does not quit from the picker")
	}
}

// While the SitWith call runs, the picker takes no second pick.
func TestSitPickerBusy(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s")
	_, cmd := h.m.Update(keyMsg("1"))
	if !h.m.pick.busy {
		t.Fatal("the picker is not busy during the call")
	}
	if v := stripANSI(h.viewAt(80, 24)); !strings.Contains(v, "Sitting down…") {
		t.Errorf("the busy picker does not say so\n%s", v)
	}
	h.press("2", "enter")
	h.run(cmd)
	h.wantCalls("SitWith(-1,buyin)")
}

// An error from SitWith closes the picker and shows on the notice line.
func TestSitPickerError(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.sess.err = errors.New("the table is full")
	h.press("s", "2")
	h.wantCalls("SitWith(-1,bank)")
	if h.m.mode != modeTable || h.m.pick.busy {
		t.Errorf("after the error: mode %v, busy %v", h.m.mode, h.m.pick.busy)
	}
	for _, sz := range allSizes {
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		if !strings.Contains(v, "✗ the table is full") {
			t.Errorf("%v: the error is not on the notice line\n%s", sz, v)
		}
	}
	// S opens the picker again.
	h.sess.err = nil
	h.press("s", "1")
	h.wantCalls("SitWith(-1,bank)", "SitWith(-1,buyin)")
}

// A snapshot that seats the viewer closes the picker.
func TestSitPickerClosesWhenSeated(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s")
	h.snapshot(fixture(t, "alone").Snap)
	if h.m.mode != modeTable {
		t.Errorf("mode = %v after the viewer got a seat", h.m.mode)
	}
}

// A snapshot that empties the bank closes the picker, so that no
// "Whole bank 0" option can buy a full StartingStack. S then sits at once
// with a buy-in.
func TestSitPickerClosesWhenBankEmpties(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s")
	h.snapshot(watching(t, 0).Snap)
	if h.m.mode != modeTable {
		t.Fatalf("mode = %v after the bank became empty", h.m.mode)
	}
	for _, sz := range allSizes {
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		if strings.Contains(v, "Whole bank") {
			t.Errorf("%v: the picker still shows\n%s", sz, v)
		}
		if !strings.Contains(v, bankEmptyNotice) {
			t.Errorf("%v: the notice does not tell why the picker closed\n%s", sz, v)
		}
	}
	h.press("2")
	h.wantCalls()
	h.press("s")
	h.wantCalls("SitWith(-1,buyin)")
}

// A busy picker stays open when the bank empties, and the call result
// closes it.
func TestSitPickerBusyKeepsOpenWhenBankEmpties(t *testing.T) {
	h := newHarness(t, watching(t, 3450))
	h.press("s")
	_, cmd := h.m.Update(keyMsg("2"))
	h.snapshot(watching(t, 0).Snap)
	if h.m.mode != modeSit || !h.m.pick.busy {
		t.Errorf("mode %v, busy %v during the call", h.m.mode, h.m.pick.busy)
	}
	h.run(cmd)
	h.wantCalls("SitWith(-1,bank)")
	if h.m.mode != modeTable {
		t.Errorf("mode = %v after the call", h.m.mode)
	}
}

// The picker texts for each kind of bank.
func TestSitPickerTexts(t *testing.T) {
	cases := []struct {
		bank int64
		want []string
		not  []string
	}{
		{3450, []string{"1  Buy-in  2,000 · 1,450 stays in bank", "2  Whole bank  3,450"}, []string{"top-up"}},
		{2000, []string{"1  Buy-in  2,000 · 0 stays in bank", "2  Whole bank  2,000"}, []string{"top-up"}},
		{300, []string{"1  Buy-in  2,000 · 300 from bank + 1,700 top-up ₹850", "2  Whole bank  300"}, []string{"stays in bank"}},
		{1, []string{"1  Buy-in  2,000 · 1 from bank + 1,999 top-up ₹999.50", "2  Whole bank  1"}, nil},
	}
	for _, c := range cases {
		for _, sz := range allSizes {
			h := newHarness(t, watching(t, c.bank))
			h.press("s")
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			for _, w := range append(c.want, "SIT DOWN WITH", "1/2 pick", "Esc cancel") {
				if !strings.Contains(v, w) {
					t.Errorf("bank %d at %v: picker lacks %q\n%s", c.bank, sz, w, v)
				}
			}
			for _, w := range c.not {
				if strings.Contains(v, w) {
					t.Errorf("bank %d at %v: picker has %q\n%s", c.bank, sz, w, v)
				}
			}
		}
	}
}

// The top-up money uses ChipsPerUnit and the currency.
func TestTopUpMoney(t *testing.T) {
	cases := []struct {
		perUnit   int64
		currency  string
		bank      int64
		stack     int64
		want      string
		buyInText string
	}{
		{2, "₹", 300, 1000, "300 from bank + 700 top-up ₹350", "300 from bank + 700 top-up ₹350"},
		{3, "₹", 1, 1000, "1 from bank + 999 top-up ₹333", "1 from bank + 999 top-up ₹333"},
		{1, "$", 500, 200000, "500 from bank + 199,500 top-up $199,500", ""},
		{2, "₹", 1450, 1000, "", "450 stays in bank"},
	}
	for _, c := range cases {
		m := newModelWith(nopSession{}, testRenderer(), 80, 24, nil)
		m.snap.Config.ChipsPerUnit, m.snap.Config.Currency = c.perUnit, c.currency
		if c.want != "" {
			if got := m.topUpText(c.bank, c.stack); got != c.want {
				t.Errorf("topUpText(%d, %d) = %q, want %q", c.bank, c.stack, got, c.want)
			}
		}
		if c.buyInText != "" {
			if got := m.buyInDetail(c.bank, c.stack); got != c.buyInText {
				t.Errorf("buyInDetail(%d, %d) = %q, want %q", c.bank, c.stack, got, c.buyInText)
			}
		}
	}
}

// The "You are watching" line names the bank, or the buy-in when the
// bank is empty.
func TestWatchingLine(t *testing.T) {
	for _, sz := range allSizes {
		v := stripANSI(Render(watching(t, 3450), testRenderer(), sz[0], sz[1]))
		if !strings.Contains(v, "S  Sit down (bank 3,450)") || strings.Contains(v, "you bring") {
			t.Errorf("%v: watching line with a bank\n%s", sz, v)
		}
		v = stripANSI(Render(watching(t, 0), testRenderer(), sz[0], sz[1]))
		if !strings.Contains(v, "Sit down (you buy in for 2,000)") {
			t.Errorf("%v: watching line without a bank\n%s", sz, v)
		}
	}
}

// The busted line says where the rebuy chips come from.
func TestBustRebuyHint(t *testing.T) {
	cases := []struct {
		bank int64
		want string
		not  string
	}{
		{5000, "Buy back in for 2,000 from bank", "top-up"},
		{2000, "Buy back in for 2,000 from bank", "top-up"},
		{300, "Buy back in for 2,000 (300 from bank + 1,700 top-up ₹850)", "2,000 from bank"},
		{0, "Buy back in for 2,000", "from bank"},
	}
	for _, c := range cases {
		f := edited(t, "busted with a bank that covers the rebuy", func(s *server.Snapshot) { s.Me.Bank = c.bank })
		for _, sz := range allSizes {
			v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
			if !strings.Contains(v, c.want) {
				t.Errorf("bank %d at %v: lacks %q\n%s", c.bank, sz, c.want, v)
			}
			if strings.Contains(v, c.not) {
				t.Errorf("bank %d at %v: has %q\n%s", c.bank, sz, c.not, v)
			}
		}
		h := newHarness(t, f)
		h.press("b")
		h.wantCalls("Rebuy")
	}
}

// Every picker and bust state fits, and the whole picker box shows.
func TestBatch5StatesFit(t *testing.T) {
	sizes := append(allSizes, [2]int{90, 26}, [2]int{60, 18}, [2]int{50, 16}, [2]int{30, 8})
	for _, bank := range []int64{1, 300, 2000, 3450, 123456789} {
		for _, sz := range sizes {
			h := newHarness(t, watching(t, bank))
			h.press("s")
			view := h.viewAt(sz[0], sz[1])
			name := fmt.Sprintf("picker bank %d", bank)
			wantFits(t, name, view, sz[0], sz[1])
			if sz[0] < 60 {
				continue
			}
			v := stripANSI(view)
			for _, w := range []string{"╭─ SIT DOWN WITH", "Whole bank  " + chips(bank), "Esc cancel", "╰──"} {
				if !strings.Contains(v, w) {
					t.Errorf("%s at %v: lacks %q\n%s", name, sz, w, v)
				}
			}
			if strings.Contains(v, "…") && strings.Contains(v, "Buy-in  2,000 ·") {
				// The detail must not be cut inside the box.
				for _, l := range strings.Split(v, "\n") {
					if strings.Contains(l, "Buy-in") && strings.Contains(l, "…") {
						t.Errorf("%s at %v: the buy-in row is cut: %q", name, sz, l)
					}
				}
			}
		}
	}
	for _, name := range []string{"busted with a bank that covers", "busted with a bank that covers part", "watching with a bank"} {
		for _, sz := range sizes {
			wantFits(t, name, Render(fixture(t, name), testRenderer(), sz[0], sz[1]), sz[0], sz[1])
		}
	}
}

// A long top-up detail moves under the label when the box is narrow.
func TestSitPickerSplitsLongDetail(t *testing.T) {
	f := edited(t, "watching with a bank", func(s *server.Snapshot) {
		s.Me.Bank = 1
		s.Config.StartingStack = 123456789
	})
	h := newHarness(t, f)
	h.press("s")
	v := stripANSI(h.viewAt(60, 18))
	if !strings.Contains(v, "1 from bank + 123,456,788 top-up") {
		t.Errorf("the long detail is cut\n%s", v)
	}
}

// The help and the README describe the picker and the rebuy rule.
func TestPickerDocumented(t *testing.T) {
	for _, sz := range allSizes {
		h := newHarness(t, watching(t, 3450))
		h.press("?")
		view := h.viewAt(sz[0], sz[1])
		wantFits(t, "help", view, sz[0], sz[1])
		v := stripANSI(view)
		for _, w := range []string{"1 buy-in or 2 whole bank", "buy back in (bank first)"} {
			if !strings.Contains(v, w) {
				t.Errorf("help at %v lacks %q\n%s", sz, w, v)
			}
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Whole bank", "top-up", "Buy-in"} {
		if !strings.Contains(string(readme), w) {
			t.Errorf("README does not name %q", w)
		}
	}
}
