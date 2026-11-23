package server

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// giveBank sets the bank of s to bank, as if s bought those chips in one
// earlier buy-in and then stood up. The net of s stays at zero.
func (h *harness) giveBank(s *Session, bank int64) {
	h.t.Helper()
	h.inRun(func(r *Room) {
		a := r.account(s.key, s.name)
		a.Bank, a.BoughtChips = bank, bank
		a.BuyIns, a.TotalBuyIns = 0, 0
		if bank > 0 {
			a.BuyIns, a.TotalBuyIns = 1, 1
		}
		r.save()
		r.settle()
	})
}

// acct returns a copy of the account of s.
func (h *harness) acct(s *Session) account {
	h.t.Helper()
	var out account
	h.inRun(func(r *Room) { out = *r.st.Accounts[s.key] })
	return out
}

// netOf returns the leaderboard net of s, also when s sits at the table.
func (h *harness) netOf(s *Session) int64 {
	h.t.Helper()
	var net int64
	h.inRun(func(r *Room) { net = r.st.Accounts[s.key].net(r.tableChips()[s.key]) })
	return net
}

func TestSitBuyInBranches(t *testing.T) {
	cases := []struct {
		name       string
		bank       int64
		wantBank   int64
		wantBought int64 // BoughtChips after the sit
		wantBuyIns int   // BuyIns after the sit
	}{
		{"bank more than stack", 2450, 1450, 2450, 1},
		{"bank equals stack", 1000, 0, 1000, 1},
		{"bank less than stack", 300, 0, 1000, 2},
		{"bank zero", 0, 0, 1000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testConfig(), nil)
			alice := h.connect("key:alice", "alice")
			h.giveBank(alice, tc.bank)
			must(t, alice.SitWith(-1, SitBuyIn))
			snap := h.snap(alice)
			if got := seatOf(snap.Table, alice.key).Stack; got != 1000 {
				t.Errorf("stack = %d, want 1000", got)
			}
			if snap.Me.Bank != tc.wantBank {
				t.Errorf("Me.Bank = %d, want %d", snap.Me.Bank, tc.wantBank)
			}
			a := h.acct(alice)
			if a.Bank != tc.wantBank || a.BoughtChips != tc.wantBought ||
				a.BuyIns != tc.wantBuyIns || a.TotalBuyIns != tc.wantBuyIns {
				t.Errorf("account bank %d bought %d buy-ins %d/%d, want %d %d %d",
					a.Bank, a.BoughtChips, a.BuyIns, a.TotalBuyIns, tc.wantBank, tc.wantBought, tc.wantBuyIns)
			}
			st, _ := standing(snap, "alice")
			if st.Net != 0 || st.BuyIns != tc.wantBuyIns || !st.Playing {
				t.Errorf("standing = %+v, want net 0 and %d buy-ins", st, tc.wantBuyIns)
			}
			row, _ := statRow(snap, "alice")
			if row.BuyIns != tc.wantBuyIns || row.PeriodBuyIns != tc.wantBuyIns || row.PeriodNet != 0 {
				t.Errorf("stats row = %+v", row)
			}
		})
	}
}

func TestSitWholeBankUnchanged(t *testing.T) {
	for _, bank := range []int64{2450, 1000, 300, 0} {
		h := newHarness(t, testConfig(), nil)
		alice := h.connect("key:alice", "alice")
		bob := h.connect("key:bob", "bob")
		h.giveBank(alice, bank)
		h.giveBank(bob, bank)
		must(t, alice.Sit(-1))
		must(t, bob.SitWith(-1, SitWholeBank))
		wantStack, wantBuyIns := bank, 1
		if bank == 0 {
			wantStack = 1000
		}
		for _, p := range []*Session{alice, bob} {
			snap := h.snap(p)
			if got := seatOf(snap.Table, p.key).Stack; got != wantStack {
				t.Errorf("bank %d: %s stack = %d, want %d", bank, p.name, got, wantStack)
			}
			if snap.Me.Bank != 0 {
				t.Errorf("bank %d: %s Me.Bank = %d, want 0", bank, p.name, snap.Me.Bank)
			}
			if a := h.acct(p); a.BuyIns != wantBuyIns || a.BoughtChips != wantStack {
				t.Errorf("bank %d: %s buy-ins %d bought %d", bank, p.name, a.BuyIns, a.BoughtChips)
			}
			if net := h.netOf(p); net != 0 {
				t.Errorf("bank %d: %s net = %d", bank, p.name, net)
			}
		}
		h.stop()
	}
}

func TestSitWithBadChoice(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	h.giveBank(alice, 500)
	for _, c := range []SitChoice{-1, 2, 99} {
		if err := alice.SitWith(-1, c); !errors.Is(err, errBadSitChoice) {
			t.Errorf("SitWith(%d) = %v, want errBadSitChoice", c, err)
		}
	}
	if snap := h.snap(alice); snap.Me.Seat != -1 || snap.Me.Bank != 500 {
		t.Errorf("after bad choice: Me = %+v", snap.Me)
	}
}

// bustOne plays one all-in hand with bustDeck and returns the busted player
// and the winner.
func bustOne(t *testing.T, h *harness, alice, bob *Session) (busted, winner *Session) {
	t.Helper()
	h.advance(h.r.cfg.NextHandDelay)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	snap := h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("all-in hand did not run out")
	}
	for _, p := range []*Session{alice, bob} {
		if seatOf(snap.Table, p.key).Stack == 0 {
			busted = p
		} else {
			winner = p
		}
	}
	if busted == nil {
		t.Fatal("nobody busted")
	}
	return busted, winner
}

func TestRebuyUsesBankFirst(t *testing.T) {
	cases := []struct {
		name       string
		bank       int64 // the bank of the busted player at the rebuy
		wantBank   int64
		boughtMore int64
		buyInsMore int
	}{
		{"bank covers", 2000, 1000, 0, 0},
		{"bank exactly covers", 1000, 0, 0, 0},
		{"bank covers a part", 300, 0, 700, 1},
		{"bank zero", 0, 0, 1000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testConfig(), stackedDeck(bustDeck))
			alice := h.connect("key:alice", "alice")
			bob := h.connect("key:bob", "bob")
			// Each player has a bank of 1000+bank, sits with SitBuyIn and
			// keeps tc.bank in the bank.
			h.giveBank(alice, 1000+tc.bank)
			h.giveBank(bob, 1000+tc.bank)
			must(t, alice.SitWith(-1, SitBuyIn))
			must(t, bob.SitWith(-1, SitBuyIn))
			busted, _ := bustOne(t, h, alice, bob)
			before := h.acct(busted)
			if before.Bank != tc.bank {
				t.Fatalf("bank before rebuy = %d, want %d", before.Bank, tc.bank)
			}
			netBefore := h.netOf(busted)
			must(t, busted.Rebuy())
			snap := h.snap(busted)
			if got := seatOf(snap.Table, busted.key).Stack; got != 1000 {
				t.Errorf("stack after rebuy = %d, want 1000", got)
			}
			after := h.acct(busted)
			if after.Bank != tc.wantBank || snap.Me.Bank != tc.wantBank {
				t.Errorf("bank after rebuy = %d (Me %d), want %d", after.Bank, snap.Me.Bank, tc.wantBank)
			}
			if got := after.BoughtChips - before.BoughtChips; got != tc.boughtMore {
				t.Errorf("bought %d more chips, want %d", got, tc.boughtMore)
			}
			if got := after.BuyIns - before.BuyIns; got != tc.buyInsMore {
				t.Errorf("BuyIns +%d, want +%d", got, tc.buyInsMore)
			}
			if got := after.TotalBuyIns - before.TotalBuyIns; got != tc.buyInsMore {
				t.Errorf("TotalBuyIns +%d, want +%d", got, tc.buyInsMore)
			}
			if got := h.netOf(busted); got != netBefore || got != -1000 {
				t.Errorf("net after rebuy = %d, before %d, want -1000", got, netBefore)
			}
			st, _ := standing(snap, busted.name)
			if st.BuyIns != before.BuyIns+tc.buyInsMore || st.Net != -1000 {
				t.Errorf("standing = %+v", st)
			}
			if snap.NextHandAt.IsZero() {
				t.Error("next hand not scheduled after rebuy")
			}
		})
	}
}

func TestStandAddsStackToBank(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	h.giveBank(alice, 2450)
	h.giveBank(bob, 2450)
	must(t, alice.SitWith(-1, SitBuyIn))
	must(t, bob.SitWith(-1, SitBuyIn))

	// A stand right away gives the whole bank back.
	must(t, alice.Stand())
	if got := h.snap(alice).Me.Bank; got != 2450 {
		t.Fatalf("bank after sit and stand = %d, want 2450", got)
	}
	must(t, alice.SitWith(-1, SitBuyIn))

	// Play one hand: the player to act folds.
	h.advance(h.r.cfg.NextHandDelay)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	snap := h.snap(alice)
	stacks := map[*Session]int64{}
	for _, p := range []*Session{alice, bob} {
		stacks[p] = seatOf(snap.Table, p.key).Stack
	}
	for _, p := range []*Session{alice, bob} {
		netBefore := h.netOf(p)
		must(t, p.Stand())
		got := h.snap(p).Me.Bank
		if got != 1450+stacks[p] {
			t.Errorf("%s bank after stand = %d, want %d", p.name, got, 1450+stacks[p])
		}
		if net := h.netOf(p); net != netBefore {
			t.Errorf("%s net after stand = %d, want %d", p.name, net, netBefore)
		}
	}
	if total := h.acct(alice).Bank + h.acct(bob).Bank; total != 4900 {
		t.Errorf("banks sum to %d, want 4900", total)
	}
}

func TestDisconnectStandAddsStackToBank(t *testing.T) {
	t.Run("between hands", func(t *testing.T) {
		h := newHarness(t, testConfig(), nil)
		alice := h.connect("key:alice", "alice")
		h.giveBank(alice, 2450)
		must(t, alice.SitWith(-1, SitBuyIn))
		alice.Close()
		back := h.connect("key:alice", "alice")
		snap := h.snap(back)
		if snap.Me.Seat != -1 || snap.Me.Bank != 2450 {
			t.Errorf("after disconnect: Me = %+v, want no seat and bank 2450", snap.Me)
		}
	})
	t.Run("after the hand", func(t *testing.T) {
		cfg := testConfig()
		cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
		h := newHarness(t, cfg, stackedDeck(bustDeck))
		alice := h.connect("key:alice", "alice")
		bob := h.connect("key:bob", "bob")
		h.giveBank(alice, 2450)
		h.giveBank(bob, 2450)
		must(t, alice.SitWith(-1, SitBuyIn))
		must(t, bob.SitWith(-1, SitBuyIn))
		h.advance(cfg.NextHandDelay)
		actor, leaver := h.toAct(alice, bob)
		leaver.Close()
		h.playPassive(actor)
		snap := h.snap(actor)
		if seatOf(snap.Table, leaver.key) != nil {
			t.Fatal("leaver still seated after the hand")
		}
		actorStack := seatOf(snap.Table, actor.key).Stack
		want := 4900 - 1450 - actorStack
		if got := h.acct(leaver).Bank; got != want {
			t.Errorf("leaver bank = %d, want %d", got, want)
		}
		st, _ := standing(snap, leaver.name)
		if st.Net != want-2450 {
			t.Errorf("leaver net = %d, want %d", st.Net, want-2450)
		}
		saved, err := loadLedger(cfg.StatePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := saved[leaver.key].Bank + saved[actor.key].Bank; got != 4900 {
			t.Errorf("saved banks sum to %d, want 4900", got)
		}
	})
}

func TestSaveRestartKeepsBankAndStack(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	h.giveBank(alice, 2450) // SitBuyIn keeps 1450 in the bank.
	h.giveBank(bob, 300)    // SitBuyIn buys a top-up of 700.
	must(t, alice.SitWith(-1, SitBuyIn))
	must(t, bob.SitWith(-1, SitBuyIn))

	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved[alice.key].Bank; got != 2450 {
		t.Errorf("saved alice bank = %d, want 2450 (1450 bank + 1000 table)", got)
	}
	if got := saved[bob.key]; got.Bank != 1000 || got.BoughtChips != 1000 || got.BuyIns != 2 {
		t.Errorf("saved bob = bank %d bought %d buy-ins %d", got.Bank, got.BoughtChips, got.BuyIns)
	}

	// Play one hand to the end, then start the next and stop mid-hand.
	h.advance(cfg.NextHandDelay)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	stacks := map[string]int64{}
	for _, p := range []*Session{alice, bob} {
		stacks[p.key] = seatOf(h.snap(alice).Table, p.key).Stack
	}
	h.advance(cfg.NextHandDelay)
	actor, _ = h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Raise, Amount: 100}))
	h.stop()

	saved, err = loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	wantBank := map[string]int64{alice.key: 1450 + stacks[alice.key], bob.key: stacks[bob.key]}
	for _, p := range []*Session{alice, bob} {
		if got := saved[p.key].Bank; got != wantBank[p.key] {
			t.Errorf("saved %s bank = %d, want %d", p.name, got, wantBank[p.key])
		}
	}

	h2 := newHarness(t, cfg, nil)
	a2 := h2.connect(alice.ident, "alice")
	b2 := h2.connect(bob.ident, "bob")
	for _, p := range []*Session{a2, b2} {
		snap := h2.snap(p)
		if snap.Me.Bank != wantBank[p.key] {
			t.Errorf("restored %s bank = %d, want %d", p.name, snap.Me.Bank, wantBank[p.key])
		}
	}
	snap := h2.snap(a2)
	if st, _ := standing(snap, "alice"); st.Net != wantBank[alice.key]-2450 || st.BuyIns != 1 {
		t.Errorf("restored alice standing = %+v", st)
	}
	if st, _ := standing(snap, "bob"); st.Net != wantBank[bob.key]-1000 || st.BuyIns != 2 {
		t.Errorf("restored bob standing = %+v", st)
	}
}

// TestNetSameForEachChoice plays the same hand once for each SitChoice. The
// net, the buy-in money and the settle-up balances must not depend on the
// choice.
func TestNetSameForEachChoice(t *testing.T) {
	type result struct {
		nets     map[string]int64
		balances []Balance
	}
	run := func(c SitChoice, bank int64) result {
		cfg := testConfig()
		cfg.ChipsPerUnit = 2
		h := newHarness(t, cfg, stackedDeck(bustDeck))
		defer h.stop()
		alice := h.connect("key:alice", "alice")
		bob := h.connect("key:bob", "bob")
		h.giveBank(alice, bank)
		must(t, alice.SitWith(-1, c))
		must(t, bob.Sit(-1))
		h.advance(cfg.NextHandDelay)
		actor, _ := h.toAct(alice, bob)
		must(t, actor.Act(engine.Action{Type: engine.Fold}))
		res := result{nets: map[string]int64{}}
		seated := h.snap(alice)
		for _, p := range []*Session{alice, bob} {
			res.nets[p.name] = h.netOf(p)
		}
		must(t, alice.Stand())
		stood := h.snap(alice)
		for _, p := range []*Session{alice, bob} {
			if got := h.netOf(p); got != res.nets[p.name] {
				t.Errorf("choice %d bank %d: %s net %d after stand, %d before", c, bank, p.name, got, res.nets[p.name])
			}
		}
		res.balances = seated.Settle.Balances
		if !balancesEqual(seated.Settle.Balances, stood.Settle.Balances) {
			t.Errorf("choice %d bank %d: balances changed at stand: %+v -> %+v",
				c, bank, seated.Settle.Balances, stood.Settle.Balances)
		}
		return res
	}
	for _, bank := range []int64{2450, 1000, 300, 0} {
		whole := run(SitWholeBank, bank)
		buyIn := run(SitBuyIn, bank)
		for name, net := range whole.nets {
			if buyIn.nets[name] != net {
				t.Errorf("bank %d: %s net %d with SitBuyIn, %d with SitWholeBank", bank, name, buyIn.nets[name], net)
			}
		}
		if !balancesEqual(whole.balances, buyIn.balances) {
			t.Errorf("bank %d: balances %+v with SitBuyIn, %+v with SitWholeBank", bank, buyIn.balances, whole.balances)
		}
		if len(whole.balances) == 0 {
			t.Errorf("bank %d: no balances after a hand", bank)
		}
	}
}

func balancesEqual(a, b []Balance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLeaderboardBuyInsCount(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	// A first buy-in from an empty bank.
	must(t, alice.SitWith(-1, SitBuyIn))
	must(t, alice.Stand())
	// A sit from a full bank buys nothing.
	must(t, alice.SitWith(-1, SitBuyIn))
	must(t, alice.Stand())
	must(t, alice.Sit(-1))
	must(t, alice.Stand())
	// A top-up counts as one buy-in.
	h.inRun(func(r *Room) { r.st.Accounts[alice.key].Bank = 400 })
	must(t, alice.SitWith(-1, SitBuyIn))
	snap := h.snap(alice)
	st, _ := standing(snap, "alice")
	if st.BuyIns != 2 {
		t.Errorf("leaderboard BuyIns = %d, want 2", st.BuyIns)
	}
	row, _ := statRow(snap, "alice")
	if row.BuyIns != 2 || row.PeriodBuyIns != 2 {
		t.Errorf("stats BuyIns = %d, PeriodBuyIns = %d, want 2", row.BuyIns, row.PeriodBuyIns)
	}
	if a := h.acct(alice); a.BoughtChips != 1600 {
		t.Errorf("BoughtChips = %d, want 1600 (1000 + 600 top-up)", a.BoughtChips)
	}
}
