package engine

import (
	"testing"
)

func TestSitErrors(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000})
	mustFail(t, tb.Sit(3, "x", "X", 100), ErrBadSeat)
	mustFail(t, tb.Sit(-1, "x", "X", 100), ErrBadSeat)
	mustFail(t, tb.Sit(0, "x", "X", 100), ErrSeatTaken)
	mustFail(t, tb.Sit(1, "alice", "Alice", 100), ErrAlreadySeated)
	mustFail(t, tb.Sit(1, "x", "X", 0), ErrNoChips)
	mustFail(t, tb.Sit(1, "", "X", 100), ErrIllegal)
	if _, err := tb.Stand("x"); err != ErrNotSeated {
		t.Fatalf("Stand unknown = %v", err)
	}
	mustFail(t, tb.SetSittingOut("x", true), ErrNotSeated)
	mustFail(t, tb.AddChips("x", 10), ErrNotSeated)
	mustFail(t, tb.AddChips("alice", 0), ErrNoChips)
	mustFail(t, tb.StartHand(), ErrNotEnough)
	if tb.CanStartHand() {
		t.Fatal("CanStartHand with one player")
	}
}

func TestStandPlayerToAct(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	wantToAct(t, tb, 0)
	chips, err := tb.Stand(idOf(0))
	must(t, err)
	if chips != 1000 {
		t.Fatalf("Stand returned %d, want 1000", chips)
	}
	wantToAct(t, tb, 1)
	if v := tb.View(""); !v.Seats[0].Empty || v.TotalPot != 30 {
		t.Fatalf("seat 0 %+v, pot %d", v.Seats[0], v.TotalPot)
	}
	act(t, tb, 1, Call)
	act(t, tb, 2, Check)
	wantToAct(t, tb, 1)
}

func TestStandPlayerNotToAct(t *testing.T) {
	// Carol (BB) leaves while Alice must act. Her 20 stays in the pot.
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	chips, err := tb.Stand(idOf(2))
	must(t, err)
	if chips != 980 {
		t.Fatalf("Stand returned %d, want 980", chips)
	}
	wantEvents(t, tb.DrainEvents(), "Carol folds", "Carol stands up with 980")
	wantToAct(t, tb, 0)
	if v := tb.View(""); v.TotalPot != 30 || v.CurrentBet != 20 {
		t.Fatalf("pot %d bet %d, want 30 and 20", v.TotalPot, v.CurrentBet)
	}
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	if v := tb.View(""); v.Street != Flop || v.TotalPot != 60 || len(v.Pots) != 1 {
		t.Fatalf("street %v pot %d pots %+v", v.Street, v.TotalPot, v.Pots)
	}
	// Someone sits in Carol's old seat during the hand; the dead chips stay.
	must(t, tb.Sit(2, "zed", "Zed", 500))
	if v := tb.View(""); v.TotalPot != 60 || v.Seats[2].InHand || v.Seats[2].Committed != 0 {
		t.Fatalf("after Zed sits: pot %d seat %+v", v.TotalPot, v.Seats[2])
	}
	checkDown(t, tb)
	a, _ := tb.Stack(idOf(0))
	b, _ := tb.Stack(idOf(1))
	if a+b != 2020 {
		t.Fatalf("stacks %d + %d, want 2020", a, b)
	}
}

func TestStandWithBetOnStreet(t *testing.T) {
	// Bob bets on the flop, then leaves before Alice acts. His bet is dead
	// money in the pot, and Alice wins it without a call.
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	act(t, tb, 2, Check)
	act(t, tb, 1, Bet, 100)
	act(t, tb, 2, Fold)
	_, err := tb.Stand(idOf(1))
	must(t, err)
	if tb.InHand() {
		t.Fatal("hand should end: one live player")
	}
	wantStacks(t, tb, map[int]int64{0: 1140, 2: 980})
}

func TestStandLeavesOnePlayer(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	wantToAct(t, tb, 0)
	_, err := tb.Stand(idOf(1)) // the BB leaves out of turn
	must(t, err)
	if tb.InHand() {
		t.Fatal("hand still running")
	}
	wantToAct(t, tb, -1)
	wantStacks(t, tb, map[int]int64{0: 1020})
	evs := tb.DrainEvents()
	wantEvents(t, evs, "Bob folds", "Bob stands up with 980", "Alice wins 30 from the pot", "Hand #1 ends")
	for _, e := range evs {
		if e.Kind == EvReturn {
			t.Fatalf("no uncalled bet expected: %q", e.Text)
		}
	}
	if tb.CanStartHand() {
		t.Fatal("CanStartHand with one player")
	}
}

func TestStandAllInPlayer(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{100, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn)
	chips, err := tb.Stand(idOf(0))
	must(t, err)
	if chips != 0 {
		t.Fatalf("Stand returned %d", chips)
	}
	wantToAct(t, tb, 1)
	act(t, tb, 1, Fold)
	if tb.InHand() {
		t.Fatal("hand still running")
	}
	wantStacks(t, tb, map[int]int64{1: 990, 2: 1110})
}

func TestStandAfterFoldDoesNotMoveAction(t *testing.T) {
	tb := newTestTable(t, 10, 20, 4, []int64{1000, 1000, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 3, Fold)
	wantToAct(t, tb, 0)
	_, err := tb.Stand(idOf(3))
	must(t, err)
	wantToAct(t, tb, 0)
	wantEvents(t, tb.DrainEvents(), "Dave folds", "Dave stands up with 1000")
}

func TestSitDuringHand(t *testing.T) {
	tb := newTestTable(t, 10, 20, 4, []int64{1000, 1000})
	must(t, tb.StartHand())
	must(t, tb.Sit(3, idOf(3), names[3], 500))
	v := tb.View(idOf(3))
	if v.MySeat != 3 || v.Seats[3].InHand || v.Seats[3].HasCards || v.Legal != nil {
		t.Fatalf("new player view = %+v", v.Seats[3])
	}
	if _, ok := tb.LegalFor(idOf(3)); ok {
		t.Fatal("new player may act")
	}
	mustFail(t, tb.Act(idOf(3), Action{Type: Fold}), ErrNotYourTurn)
	must(t, tb.AddChips(idOf(3), 100)) // not in the live hand
	foldToEnd(t, tb)
	must(t, tb.StartHand())
	v = tb.View("")
	if !v.Seats[3].InHand || v.Button != 1 || v.SmallBlindSeat != 3 || v.BigBlindSeat != 0 {
		t.Fatalf("hand 2: seat 3 %+v button %d sb %d bb %d", v.Seats[3], v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
}

func TestBustedPlayersAreSkipped(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "As Ad", 1: "Ks Kd", 2: "7c 2d",
	}, "Ac 8d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 100}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 100)
	act(t, tb, 1, Fold)
	act(t, tb, 2, AllIn)
	if tb.InHand() {
		t.Fatal("hand did not run out")
	}
	wantEvents(t, tb.DrainEvents(), "Carol is busted", "Hand #1 ends")
	v := tb.View("")
	if !v.Seats[2].Busted || v.Seats[2].Stack != 0 || v.Seats[2].InHand {
		t.Fatalf("Carol = %+v", v.Seats[2])
	}
	must(t, tb.StartHand())
	v = tb.View("")
	if v.Seats[2].InHand || v.Button != 1 || v.SmallBlindSeat != 1 || v.BigBlindSeat != 0 {
		t.Fatalf("hand 2: Carol %+v button %d sb %d bb %d", v.Seats[2], v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
	foldToEnd(t, tb)
	// Chips bring Carol back from the next hand.
	must(t, tb.AddChips(idOf(2), 500))
	if v := tb.View(""); v.Seats[2].Busted {
		t.Fatal("Carol still busted after AddChips")
	}
	must(t, tb.StartHand())
	if !tb.View("").Seats[2].InHand {
		t.Fatal("Carol not dealt in after AddChips")
	}
}

func TestSittingOut(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.SetSittingOut(idOf(1), true))
	must(t, tb.SetSittingOut(idOf(1), true)) // no change, no error
	must(t, tb.StartHand())
	v := tb.View("")
	if v.Seats[1].InHand || !v.Seats[1].SittingOut || v.SmallBlindSeat != 0 || v.BigBlindSeat != 2 {
		t.Fatalf("view = %+v", v)
	}
	// Sitting out during the hand takes effect from the next hand.
	must(t, tb.SetSittingOut(idOf(2), true))
	if !tb.View("").Seats[2].InHand {
		t.Fatal("Carol left the hand when she sat out")
	}
	foldToEnd(t, tb)
	if tb.CanStartHand() {
		t.Fatal("CanStartHand with one active player")
	}
	must(t, tb.SetSittingOut(idOf(1), false))
	if !tb.CanStartHand() {
		t.Fatal("CanStartHand false")
	}
	wantEvents(t, tb.DrainEvents(), "Bob sits out", "Carol sits out", "Bob is back in")
}

func TestViewHidesHoleCards(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{
		0: "As Ad", 1: "Ks Kd", 2: "7c 2d",
	}, "Ac 8d 9h Jc 3s")
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	for seat := range 3 {
		v := tb.View(idOf(seat))
		if v.MySeat != seat {
			t.Fatalf("MySeat = %d, want %d", v.MySeat, seat)
		}
		for i, s := range v.Seats {
			if !s.HasCards {
				t.Fatalf("seat %d HasCards false", i)
			}
			if (i == seat) != (len(s.Cards) == 2) {
				t.Fatalf("viewer %d sees seat %d cards %v", seat, i, s.Cards)
			}
		}
	}
	for _, s := range tb.View("").Seats {
		if s.Cards != nil {
			t.Fatalf("spectator sees %v", s.Cards)
		}
	}
	if v := tb.View(idOf(0)); v.Legal == nil || tb.View(idOf(1)).Legal != nil {
		t.Fatal("Legal must be set only for the player to act")
	}

	// Bob folds; Alice and Carol go to showdown. Everyone sees the shown
	// hands, but not Bob's folded cards.
	act(t, tb, 0, Call)
	act(t, tb, 1, Fold)
	if v := tb.View(idOf(1)); len(v.Seats[1].Cards) != 2 || v.Seats[1].HasCards {
		t.Fatalf("folded owner: %+v", v.Seats[1])
	}
	checkDown(t, tb)
	for _, viewer := range []string{"", idOf(0), idOf(1), idOf(2)} {
		v := tb.View(viewer)
		if len(v.Seats[0].Cards) != 2 || len(v.Seats[2].Cards) != 2 {
			t.Fatalf("viewer %q cannot see the shown hands", viewer)
		}
		if viewer != idOf(1) && v.Seats[1].Cards != nil {
			t.Fatalf("viewer %q sees Bob's folded cards", viewer)
		}
		if len(v.Board) != 5 || v.Result == nil || v.InHand || v.Street != Waiting {
			t.Fatalf("after hand: %+v", v)
		}
	}
}

func TestViewAfterFoldWinHidesWinnerCards(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	if v := tb.View(idOf(0)); v.Seats[1].Cards != nil {
		t.Fatal("Alice sees the winner's cards")
	}
	if v := tb.View(idOf(1)); len(v.Seats[1].Cards) != 2 {
		t.Fatal("Bob does not see his own cards after the hand")
	}
}

func TestViewIsDeepCopy(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 100})
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 200)
	act(t, tb, 1, Call)
	act(t, tb, 2, AllIn)
	v := tb.View(idOf(1)) // Bob acts first on the flop

	v.Board[0].Rank = 0
	v.Seats[1].Cards[0].Rank = 0
	v.Pots[0].Eligible[0] = 99
	v.Legal.MinTo = -1
	w := tb.View(idOf(1))
	if w.Board[0].Rank == 0 || w.Seats[1].Cards[0].Rank == 0 || w.Pots[0].Eligible[0] == 99 || w.Legal.MinTo == -1 {
		t.Fatal("View shares memory with the table")
	}
	checkDown(t, tb)
	r := tb.View("")
	r.Result.Shown[0].Cards[0].Rank = 0
	r.Result.Winnings[0].Amount = -5
	r.Result.Board[0].Rank = 0
	r2 := tb.View("").Result
	if r2.Shown[0].Cards[0].Rank == 0 || r2.Winnings[0].Amount == -5 || r2.Board[0].Rank == 0 {
		t.Fatal("Result shares memory with the table")
	}
}

func TestResultClearedAtNextHand(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	checkDown(t, tb)
	if tb.View("").Result == nil {
		t.Fatal("no result")
	}
	must(t, tb.StartHand())
	if v := tb.View(""); v.Result != nil || len(v.Board) != 0 || v.HandNumber != 2 {
		t.Fatalf("hand 2 view: result %v board %v", v.Result, v.Board)
	}
}
