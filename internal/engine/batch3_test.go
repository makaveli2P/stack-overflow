package engine

import (
	"slices"
	"testing"
)

// Focused batch-3 tests: Result.Actions, Order, BigBlind and RunoutFrom.

// rec builds an ActionRecord for the player at seat.
func rec(seat int, st Street, typ ActionType, to, before int64, raise bool, raisesBefore int) ActionRecord {
	return ActionRecord{
		PlayerID: idOf(seat), Street: st, Type: typ, To: to,
		BetBefore: before, Raise: raise, RaisesBefore: raisesBefore,
	}
}

func lastResult(t *testing.T, tb *Table) *Result {
	t.Helper()
	if tb.InHand() {
		t.Fatal("the hand did not end")
	}
	res := tb.View("").Result
	if res == nil {
		t.Fatal("no result")
	}
	return res
}

func wantActions(t *testing.T, res *Result, want ...ActionRecord) {
	t.Helper()
	if !slices.Equal(res.Actions, want) {
		t.Fatalf("actions:\n got %+v\nwant %+v", res.Actions, want)
	}
}

func wantOrder(t *testing.T, res *Result, seats ...int) {
	t.Helper()
	var want []string
	for _, s := range seats {
		want = append(want, idOf(s))
	}
	if !slices.Equal(res.Order, want) {
		t.Fatalf("Order = %v, want %v", res.Order, want)
	}
}

// Four players: an open raise, a 3-bet, a fold, a 4-bet and a call; then a
// flop bet and call, and checks down.
func TestActionsOpenThreeBetFourBet(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2, 3},
		map[int]string{0: "Kc Kd", 1: "7c 2d", 2: "Ac Ad", 3: "Qc Qd"}, "2h 5s 9c Th 3s")
	tb := newTestTable(t, 5, 10, 4, []int64{1000, 1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	// Button Alice (0), SB Bob (1), BB Carol (2), UTG Dave (3).
	act(t, tb, 3, Raise, 30)  // open raise
	act(t, tb, 0, Raise, 90)  // 3-bet
	act(t, tb, 1, Fold)       // faces two raises
	act(t, tb, 2, Raise, 250) // 4-bet
	act(t, tb, 3, Call)
	act(t, tb, 0, Fold)
	// Flop: Carol is the first live seat after the button.
	act(t, tb, 2, Bet, 300)
	act(t, tb, 3, Call)
	checkDown(t, tb)
	res := lastResult(t, tb)
	wantActions(t, res,
		rec(3, Preflop, Raise, 30, 10, true, 0),
		rec(0, Preflop, Raise, 90, 30, true, 1),
		rec(1, Preflop, Fold, 0, 90, false, 2),
		rec(2, Preflop, Raise, 250, 90, true, 2),
		rec(3, Preflop, Call, 250, 250, false, 3),
		rec(0, Preflop, Fold, 0, 250, false, 3),
		rec(2, Flop, Bet, 300, 0, true, 0),
		rec(3, Flop, Call, 300, 300, false, 1),
		rec(2, Turn, Check, 0, 0, false, 0),
		rec(3, Turn, Check, 0, 0, false, 0),
		rec(2, River, Check, 0, 0, false, 0),
		rec(3, River, Check, 0, 0, false, 0),
	)
	wantOrder(t, res, 1, 2, 3, 0) // from the SB; the button is last
	if res.BigBlind != 10 || res.RunoutFrom != 5 || res.NoShowdown {
		t.Fatalf("BigBlind %d RunoutFrom %d NoShowdown %v, want 10 5 false", res.BigBlind, res.RunoutFrom, res.NoShowdown)
	}
}

// The big blind is not a raise: a limp is a call with no raise before it,
// and the BB's raise on the option is an open raise.
func TestActionsLimpAndOption(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	// Button Alice (0), SB Bob (1), BB Carol (2).
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	act(t, tb, 2, Raise, 40)
	act(t, tb, 0, Fold)
	act(t, tb, 1, Fold)
	res := lastResult(t, tb)
	wantActions(t, res,
		rec(0, Preflop, Call, 10, 10, false, 0),
		rec(1, Preflop, Call, 10, 10, false, 0),
		rec(2, Preflop, Raise, 40, 10, true, 0),
		rec(0, Preflop, Fold, 0, 40, false, 1),
		rec(1, Preflop, Fold, 0, 40, false, 1),
	)
	wantOrder(t, res, 1, 2, 0)
	if !res.NoShowdown || res.RunoutFrom != 5 {
		t.Fatalf("NoShowdown %v RunoutFrom %d, want true 5", res.NoShowdown, res.RunoutFrom)
	}
	// A walk: no decisions at all.
	must(t, tb.StartHand()) // button Bob, SB Carol, BB Alice
	act(t, tb, 1, Fold)
	act(t, tb, 2, Fold)
	res = lastResult(t, tb)
	wantActions(t, res, rec(1, Preflop, Fold, 0, 10, false, 0), rec(2, Preflop, Fold, 0, 10, false, 0))
	wantOrder(t, res, 2, 0, 1)
}

// Heads-up: the button posts the small blind and is first in Order.
func TestActionsHeadsUpOrder(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Check)
	act(t, tb, 1, Bet, 20) // flop: the big blind acts first
	act(t, tb, 0, Raise, 60)
	act(t, tb, 1, Fold)
	res := lastResult(t, tb)
	wantOrder(t, res, 0, 1)
	wantActions(t, res,
		rec(0, Preflop, Call, 20, 20, false, 0),
		rec(1, Preflop, Check, 0, 20, false, 0),
		rec(1, Flop, Bet, 20, 0, true, 0),
		rec(0, Flop, Raise, 60, 20, true, 1),
		rec(1, Flop, Fold, 0, 60, false, 2),
	)
	if res.BigBlind != 20 {
		t.Fatalf("BigBlind %d, want 20", res.BigBlind)
	}
	must(t, tb.StartHand()) // the button moves to Bob
	foldToEnd(t, tb)
	res = lastResult(t, tb)
	wantOrder(t, res, 1, 0)
	wantActions(t, res, rec(1, Preflop, Fold, 0, 20, false, 0))
}

// An all-in for less than a call is not a raise; an incomplete all-in
// raise is a raise (it grows the highest street total). Both end in a
// preflop run-out.
func TestActionsAllInForLess(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2},
		map[int]string{0: "Ac Ad", 1: "7c 2d", 2: "Kc Kd"}, "2h 5s 9c Th 3s")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 25}, deck)
	must(t, tb.StartHand())
	// Button Alice (0), SB Bob (1), BB Carol (2) with 15 behind.
	act(t, tb, 0, Raise, 100)
	act(t, tb, 1, Fold)
	act(t, tb, 2, AllIn) // 25 total: less than the 100 to call
	res := lastResult(t, tb)
	wantActions(t, res,
		rec(0, Preflop, Raise, 100, 10, true, 0),
		rec(1, Preflop, Fold, 0, 100, false, 1),
		rec(2, Preflop, AllIn, 25, 100, false, 1),
	)
	if res.RunoutFrom != 0 || len(res.Board) != 5 {
		t.Fatalf("RunoutFrom %d board %v, want 0 and a full board", res.RunoutFrom, res.Board)
	}

	// Incomplete raise: Alice raises to 100, Carol is all-in for 130 (a
	// raise of 30, less than the full 90), Alice may only call.
	deck = stackDeck(t, 1, []int{0, 1, 2},
		map[int]string{0: "Ac Ad", 1: "7c 2d", 2: "Kc Kd"}, "2h 5s 9c Th 3s")
	tb = newTestTable(t, 5, 10, 3, []int64{1000, 1000, 125}, deck, deck)
	must(t, tb.StartHand()) // button Alice
	foldToEnd(t, tb)        // Carol wins the small blind: 130
	must(t, tb.StartHand()) // button Bob (1), SB Carol (2), BB Alice (0)
	act(t, tb, 1, Raise, 100)
	act(t, tb, 2, AllIn) // 130
	act(t, tb, 0, Fold)
	if l := legal(t, tb, 1); l.Raise || !l.Call {
		t.Fatalf("after an incomplete raise Bob may raise: %+v", l)
	}
	act(t, tb, 1, Call)
	res = lastResult(t, tb)
	wantActions(t, res,
		rec(1, Preflop, Raise, 100, 10, true, 0),
		rec(2, Preflop, AllIn, 130, 100, true, 1),
		rec(0, Preflop, Fold, 0, 130, false, 2),
		rec(1, Preflop, Call, 130, 130, false, 2),
	)
	wantOrder(t, res, 2, 0, 1)
	if res.RunoutFrom != 0 {
		t.Fatalf("RunoutFrom %d, want 0", res.RunoutFrom)
	}
}

// Run-outs from each street; a river all-in and a fold are not run-outs.
func TestRunoutFrom(t *testing.T) {
	holes := map[int]string{0: "Ac Ad", 1: "Kc Kd"}
	play := func(stacks []int64, moves func(tb *Table)) *Result {
		t.Helper()
		tb := newTestTable(t, 5, 10, 2, stacks, stackDeck(t, 0, []int{0, 1}, holes, "2h 5s 9c Th 3s"))
		must(t, tb.StartHand())
		moves(tb)
		return lastResult(t, tb)
	}
	cases := []struct {
		name   string
		stacks []int64
		moves  func(tb *Table)
		want   int
		noShow bool
	}{
		{"preflop all-in and call", []int64{500, 500}, func(tb *Table) {
			act(t, tb, 0, AllIn)
			act(t, tb, 1, Call)
		}, 0, false},
		{"both all-in on the blinds", []int64{5, 8}, func(*Table) {}, 0, false},
		{"flop all-in and call", []int64{500, 500}, func(tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			act(t, tb, 1, AllIn)
			act(t, tb, 0, Call)
		}, 3, false},
		{"turn all-in and call", []int64{500, 500}, func(tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			act(t, tb, 1, Check)
			act(t, tb, 0, Check)
			act(t, tb, 1, AllIn)
			act(t, tb, 0, Call)
		}, 4, false},
		{"river all-in and call", []int64{500, 500}, func(tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			for range 2 {
				act(t, tb, 1, Check)
				act(t, tb, 0, Check)
			}
			act(t, tb, 1, AllIn)
			act(t, tb, 0, Call)
		}, 5, false},
		{"flop all-in and fold", []int64{500, 500}, func(tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			act(t, tb, 1, AllIn)
			act(t, tb, 0, Fold)
		}, 5, true},
	}
	for _, c := range cases {
		res := play(c.stacks, c.moves)
		if res.RunoutFrom != c.want || res.NoShowdown != c.noShow {
			t.Errorf("%s: RunoutFrom %d NoShowdown %v, want %d %v", c.name, res.RunoutFrom, res.NoShowdown, c.want, c.noShow)
		}
	}
	// The short stack is all-in on the flop. Nobody else can bet, so the
	// other player may only call, and the turn and river run out.
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 60},
		stackDeck(t, 0, []int{0, 1, 2}, map[int]string{0: "Ac Ad", 1: "Kc Kd", 2: "Qc Qd"}, "2h 5s 9c Th 3s"))
	must(t, tb.StartHand()) // button Alice, SB Bob, BB Carol
	act(t, tb, 0, Call)
	act(t, tb, 1, Fold)
	act(t, tb, 2, Check)
	act(t, tb, 2, AllIn) // 50 more on the flop
	mustFail(t, tb.Act(idOf(0), Action{Type: Raise, Amount: 200}), ErrIllegal)
	act(t, tb, 0, Call)
	res := lastResult(t, tb)
	if res.RunoutFrom != 3 {
		t.Fatalf("RunoutFrom %d, want 3", res.RunoutFrom)
	}
	wantActions(t, res,
		rec(0, Preflop, Call, 10, 10, false, 0),
		rec(1, Preflop, Fold, 0, 10, false, 0),
		rec(2, Preflop, Check, 0, 10, false, 0),
		rec(2, Flop, AllIn, 50, 0, true, 0),
		rec(0, Flop, Call, 50, 50, false, 1),
	)
}

// A player who stands during a hand folds; the fold is in the log, and
// the player stays in Order.
func TestActionsStandFold(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand()) // button Alice, SB Bob, BB Carol
	act(t, tb, 0, Raise, 30)
	if _, err := tb.Stand(idOf(2)); err != nil { // Carol leaves out of turn
		t.Fatal(err)
	}
	act(t, tb, 1, Fold)
	res := lastResult(t, tb)
	wantActions(t, res,
		rec(0, Preflop, Raise, 30, 10, true, 0),
		rec(2, Preflop, Fold, 0, 30, false, 1),
		rec(1, Preflop, Fold, 0, 30, false, 1),
	)
	wantOrder(t, res, 1, 2, 0)
}
