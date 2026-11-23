package engine

import (
	"slices"
	"testing"
)

func TestThreeWayAllInSidePots(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "As Ad", // Alice: best hand, shortest stack
		1: "Ks Kd", // Bob: second
		2: "Qs Qd", // Carol: worst, deepest
	}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{100, 300, 500}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn)
	act(t, tb, 1, AllIn)
	// Both other players are all-in, so nobody can call a raise: Carol may
	// only call or fold.
	act(t, tb, 2, Call)
	if tb.InHand() {
		t.Fatal("hand still running after everyone is all-in")
	}
	wantStacks(t, tb, map[int]int64{0: 300, 1: 400, 2: 200})
	evs := tb.DrainEvents()
	wantEvents(t, evs,
		"Alice is all-in for 100",
		"Bob is all-in for 300",
		"Carol calls 280",
		"Flop: 2♣ 7♦ 9♥",
		"Turn: 2♣ 7♦ 9♥ [J♣]",
		"River: 2♣ 7♦ 9♥ J♣ [3♠]",
		"Bob shows K♠ K♦ — Pair of Kings",
		"Carol shows Q♠ Q♦ — Pair of Queens",
		"Alice shows A♠ A♦ — Pair of Aces",
		"Alice wins 300 from the main pot with Pair of Aces",
		"Bob wins 400 from side pot 1 with Pair of Kings",
		"Hand #1 ends",
	)
	res := tb.View("").Result
	want := []Winning{
		{Seat: 0, PlayerID: "alice", Name: "Alice", Amount: 300, PotIndex: 0, Hand: "Pair of Aces"},
		{Seat: 1, PlayerID: "bob", Name: "Bob", Amount: 400, PotIndex: 1, Hand: "Pair of Kings"},
	}
	if !slices.Equal(res.Winnings, want) {
		t.Fatalf("winnings = %+v, want %+v", res.Winnings, want)
	}
	if len(res.Shown) != 3 || len(res.Board) != 5 {
		t.Fatalf("result = %+v", res)
	}
}

func TestSidePotGoesToShortStackWhenBest(t *testing.T) {
	// Carol (short) wins the main pot; Alice beats Bob for the side pot.
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "Ks Kd", 1: "Qs Qd", 2: "As Ad",
	}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 50}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 50)
	act(t, tb, 1, Call)
	act(t, tb, 2, AllIn) // calls 30 more, all-in
	// Flop: Alice and Bob still bet into a side pot.
	act(t, tb, 1, Bet, 100)
	act(t, tb, 0, Call)
	checkDown(t, tb)
	wantStacks(t, tb, map[int]int64{0: 1050, 1: 850, 2: 150})
}

func TestSplitPotOddChip(t *testing.T) {
	// The board is a broadway straight: Alice and Carol split 25. Bob folded
	// his small blind. The odd chip goes to the first winner after the
	// button: Carol (seat 2) before Alice (seat 0).
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "2c 3d", 1: "4h 5s", 2: "2d 3c",
	}, "Ac Kd Qh Js Tc")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Fold)
	act(t, tb, 2, Check)
	checkDown(t, tb)
	wantStacks(t, tb, map[int]int64{0: 1002, 1: 995, 2: 1003})
	res := tb.View("").Result
	if len(res.Winnings) != 2 || res.Winnings[0].Seat != 2 || res.Winnings[0].Amount != 13 ||
		res.Winnings[1].Seat != 0 || res.Winnings[1].Amount != 12 {
		t.Fatalf("winnings = %+v", res.Winnings)
	}
	wantEvents(t, tb.DrainEvents(),
		"Carol wins 13 from the pot with Straight, Ace high",
		"Alice wins 12 from the pot with Straight, Ace high")
}

func TestOddChipOrderWrapsAroundButton(t *testing.T) {
	// Hand 2: button 1 (Bob), SB 2 (Carol), BB 0 (Alice). Carol folds her
	// small blind; Alice and Bob split 25. The first seat after the button
	// is Carol's (folded), then Alice's, so Alice gets the odd chip.
	first := stackDeck(t, 0, []int{0, 1, 2}, nil, "")
	second := stackDeck(t, 1, []int{0, 1, 2}, map[int]string{
		0: "2d 3c", 1: "2c 3d", 2: "4h 5s",
	}, "Ac Kd Qh Js Tc")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, first, second)
	must(t, tb.StartHand())
	foldToEnd(t, tb) // Carol wins the blinds
	wantStacks(t, tb, map[int]int64{0: 1000, 1: 995, 2: 1005})
	must(t, tb.StartHand())
	act(t, tb, 1, Call)
	act(t, tb, 2, Fold)
	act(t, tb, 0, Check)
	checkDown(t, tb)
	wantStacks(t, tb, map[int]int64{0: 1003, 1: 997, 2: 1000})
}

func TestEveryoneFoldsReturnsUncalled(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	tb.DrainEvents()
	act(t, tb, 0, Raise, 60)
	act(t, tb, 1, Fold)
	act(t, tb, 2, Fold)
	wantStacks(t, tb, map[int]int64{0: 1030, 1: 990, 2: 980})
	evs := tb.DrainEvents()
	want := []string{
		"Alice raises to 60",
		"Bob folds",
		"Carol folds",
		"Uncalled 40 returned to Alice",
		"Alice wins 50 from the pot",
		"Hand #1 ends",
	}
	if got := eventTexts(evs); !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	res := tb.View("").Result
	if len(res.Shown) != 0 || len(res.Winnings) != 1 || res.Winnings[0].Hand != "" {
		t.Fatalf("result = %+v", res)
	}
	for _, e := range evs {
		if e.Kind == EvShow || e.Kind == EvStreet {
			t.Fatalf("unexpected event %+v", e)
		}
	}
}

func TestAllInRunOutDealsEveryStreet(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1}, map[int]string{0: "Ah Kh", 1: "7c 7d"}, "Qh Jh 2c 3d Th")
	tb := newTestTable(t, 10, 20, 2, []int64{500, 800}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn)
	act(t, tb, 1, Call)
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	evs := tb.DrainEvents()
	wantEvents(t, evs, "Flop: Q♥ J♥ 2♣", "Turn: Q♥ J♥ 2♣ [3♦]", "River: Q♥ J♥ 2♣ 3♦ [T♥]",
		"Bob shows 7♣ 7♦ — Pair of Sevens", "Alice shows A♥ K♥ — Royal Flush",
		"Alice wins 1000 from the pot with Royal Flush")
	wantStacks(t, tb, map[int]int64{0: 1000, 1: 300})
}

func TestRunOutAfterFlopAllIn(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{500, 800})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Check)
	act(t, tb, 1, Bet, 100)
	act(t, tb, 0, AllIn)
	act(t, tb, 1, Call)
	if tb.InHand() || len(tb.View("").Board) != 5 {
		t.Fatal("hand did not run out to the river")
	}
}

func TestShortBigBlind(t *testing.T) {
	// Carol has 15 for a 20 big blind. The call is still 20 and the min
	// raise is to 40. Carol can win only the main pot (3 x 15).
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "Ks Kd", 1: "Qs Qd", 2: "As Ad",
	}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 15}, deck)
	must(t, tb.StartHand())
	wantEvents(t, tb.DrainEvents(), "Bob posts small blind 10", "Carol posts big blind 15 and is all-in")
	l := legal(t, tb, 0)
	if l.CallAmount != 20 || l.MinTo != 40 {
		t.Fatalf("legal = %+v, want call 20, min raise to 40", l)
	}
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	if v := tb.View(""); v.Street != Flop || len(v.Pots) != 2 || v.Pots[0].Amount != 45 || v.Pots[1].Amount != 10 {
		t.Fatalf("flop view: street %v pots %+v", v.Street, v.Pots)
	}
	checkDown(t, tb)
	wantStacks(t, tb, map[int]int64{0: 990, 1: 980, 2: 45})
}

func TestHeadsUpShortSmallBlindRunsOut(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{5, 1000})
	must(t, tb.StartHand())
	if tb.InHand() {
		t.Fatal("hand should run out at once: nobody can bet")
	}
	evs := tb.DrainEvents()
	wantEvents(t, evs, "Alice posts small blind 5 and is all-in", "Bob posts big blind 20",
		"Uncalled 15 returned to Bob")
	a, _ := tb.Stack(idOf(0))
	b, _ := tb.Stack(idOf(1))
	if a+b != 1005 || (a != 0 && a != 10 && a != 5) {
		t.Fatalf("stacks %d + %d", a, b)
	}
}

func TestHeadsUpShortBigBlind(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 15})
	must(t, tb.StartHand())
	// Bob is all-in, so nobody can call a raise: Alice may only call or fold.
	l := legal(t, tb, 0)
	if l.CallAmount != 10 || l.Raise || l.AllIn || l.MinTo != 0 {
		t.Fatalf("legal = %+v", l)
	}
	act(t, tb, 0, Call)
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	wantEvents(t, tb.DrainEvents(), "Uncalled 5 returned to Alice")
}

// The big blind is all-in for 5, less than the 10 small blind. The small
// blind covers the whole stack of the big blind, so no betting is possible.
// The hand must run out at once and return the 5 uncalled chips. Before the
// fix, the engine asked the small blind to call the full big blind, and
// DefaultAction (timeout or disconnect) folded it, so the big blind won 5
// chips that it never matched.
func TestHeadsUpBigBlindAllInForLessThanSmallBlindRunsOut(t *testing.T) {
	// Alice: button + small blind, 1000. Bob: big blind, 5. Bob holds aces.
	deck := stackDeck(t, 0, []int{0, 1}, map[int]string{0: "Ks Kd", 1: "As Ad"}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 5}, deck)
	must(t, tb.StartHand())
	if id, ok := tb.ToAct(); ok {
		l, _ := tb.LegalFor(id)
		t.Errorf("%s must act although the only opponent is all-in for less than her blind: %+v", id, l)
		must(t, tb.Act(id, tb.DefaultAction(id)))
	}
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	wantEvents(t, tb.DrainEvents(), "Uncalled 5 returned to Alice")
	// 5 of Alice's 10 come back; Bob wins 5 + 5.
	wantStacks(t, tb, map[int]int64{0: 995, 1: 10})
}

// The same case three-handed: the button folds, and the small blind faces a
// big blind that is all-in for less than the small blind.
func TestThreeHandedBigBlindAllInForLessThanSmallBlindRunsOut(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{1: "Ks Kd", 2: "As Ad"}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 5}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	if id, ok := tb.ToAct(); ok {
		t.Errorf("%s must act although the only opponent is all-in for less", id)
		must(t, tb.Act(id, tb.DefaultAction(id)))
	}
	wantStacks(t, tb, map[int]int64{0: 1000, 1: 995, 2: 10})
}

// The big blind is all-in for exactly the small blind. The small blind
// matches it, so the hand runs out and nothing comes back.
func TestBigBlindAllInForSmallBlindAmountRunsOut(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 10})
	must(t, tb.StartHand())
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	for _, e := range tb.DrainEvents() {
		if e.Kind == EvReturn {
			t.Fatalf("unexpected return: %s", e.Text)
		}
	}
}

// A bettor stands out of turn. The lone remaining bettor already matches the
// all-in player, so the hand runs out. The lone bettor must not call the
// dead bet, and DefaultAction must not fold her hand.
func TestStandingBettorDoesNotForceDeadCall(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "Ks Kd", 1: "7c 2d", 2: "As Ad",
	}, "2c 7d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 50}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 200)
	act(t, tb, 1, Call)
	act(t, tb, 2, AllIn)
	act(t, tb, 1, Bet, 100)
	wantToAct(t, tb, 0)
	chips, err := tb.Stand(idOf(1))
	must(t, err)
	if chips != 700 {
		t.Fatalf("Bob stands with %d, want 700", chips)
	}
	if id, ok := tb.ToAct(); ok {
		l, _ := tb.LegalFor(id)
		t.Errorf("%s must call a dead bet against an all-in player: %+v", id, l)
		must(t, tb.Act(id, tb.DefaultAction(id)))
	}
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	// Main pot 150 to Carol (aces). Side pot 400 (Alice 150, Bob 250 dead)
	// to Alice.
	wantStacks(t, tb, map[int]int64{0: 1200, 2: 150})
}

func TestBothBlindsAllIn(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{10, 20})
	must(t, tb.StartHand())
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	wantEvents(t, tb.DrainEvents(), "Uncalled 10 returned to Bob")
}

func TestShowdownOrder(t *testing.T) {
	t.Run("river aggressor first", func(t *testing.T) {
		tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
		must(t, tb.StartHand())
		for tb.View("").Street != River {
			playStreet(t, tb)
		}
		act(t, tb, 1, Check)
		act(t, tb, 2, Bet, 50)
		act(t, tb, 0, Call)
		act(t, tb, 1, Call)
		shown := tb.View("").Result.Shown
		if shown[0].Seat != 2 || shown[1].Seat != 0 || shown[2].Seat != 1 {
			t.Fatalf("shown order = %d %d %d, want 2 0 1", shown[0].Seat, shown[1].Seat, shown[2].Seat)
		}
	})
	t.Run("no river bet: first after button", func(t *testing.T) {
		tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
		must(t, tb.StartHand())
		act(t, tb, 0, Raise, 100)
		act(t, tb, 1, Call)
		act(t, tb, 2, Call)
		checkDown(t, tb)
		shown := tb.View("").Result.Shown
		if shown[0].Seat != 1 || shown[1].Seat != 2 || shown[2].Seat != 0 {
			t.Fatalf("shown order = %d %d %d, want 1 2 0", shown[0].Seat, shown[1].Seat, shown[2].Seat)
		}
	})
}

// playStreet checks or calls until the street ends.
func playStreet(t *testing.T, tb *Table) {
	t.Helper()
	street := tb.View("").Street
	for tb.InHand() && tb.View("").Street == street {
		id, _ := tb.ToAct()
		l, _ := tb.LegalFor(id)
		a := Action{Type: Check}
		if !l.Check {
			a.Type = Call
		}
		must(t, tb.Act(id, a))
	}
}

func TestHandEventText(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "As Kd", 1: "7c 2d", 2: "Kh Qh",
	}, "Ks 7h 2s 9c 4d")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 60)
	act(t, tb, 1, Call)
	act(t, tb, 2, Fold)
	act(t, tb, 1, Check)
	act(t, tb, 0, Bet, 120)
	act(t, tb, 1, Call)
	act(t, tb, 1, Check)
	act(t, tb, 0, Check)
	act(t, tb, 1, Check)
	act(t, tb, 0, Check)
	want := []string{
		"Alice sits down with 1000",
		"Bob sits down with 1000",
		"Carol sits down with 1000",
		"Hand #1 — Alice has the button",
		"Bob posts small blind 10",
		"Carol posts big blind 20",
		"Alice raises to 60",
		"Bob calls 50",
		"Carol folds",
		"Flop: K♠ 7♥ 2♠",
		"Bob checks",
		"Alice bets 120",
		"Bob calls 120",
		"Turn: K♠ 7♥ 2♠ [9♣]",
		"Bob checks",
		"Alice checks",
		"River: K♠ 7♥ 2♠ 9♣ [4♦]",
		"Bob checks",
		"Alice checks",
		"Bob shows 7♣ 2♦ — Two Pair, Sevens and Twos",
		"Alice shows A♠ K♦ — Pair of Kings",
		"Bob wins 380 from the pot with Two Pair, Sevens and Twos",
		"Hand #1 ends",
	}
	if got := eventTexts(tb.DrainEvents()); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q\nwant:\n%q", got, want)
	}
	if evs := tb.DrainEvents(); len(evs) != 0 {
		t.Fatalf("second drain = %v", evs)
	}
}
