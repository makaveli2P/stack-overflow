package engine

import "testing"

// wantCallOnly checks that the player at seat may only call or fold: when
// every other live player is all-in, nobody can call a raise (NLHE).
func wantCallOnly(t *testing.T, tb *Table, seat int, call int64) {
	t.Helper()
	l := legal(t, tb, seat)
	if l.Bet || l.Raise || l.AllIn || l.MinTo != 0 || l.MaxTo != 0 || !l.Call || l.CallAmount != call {
		t.Fatalf("%s legal = %+v, want call %d or fold only", names[seat], l, call)
	}
	mustFail(t, tb.Act(idOf(seat), Action{Type: Raise, Amount: l.AllInTo}), ErrIllegal)
	mustFail(t, tb.Act(idOf(seat), Action{Type: Bet, Amount: l.AllInTo}), ErrIllegal)
	mustFail(t, tb.Act(idOf(seat), Action{Type: AllIn}), ErrIllegal)
}

// wantNoRaiseBy checks that the finished hand records no raise by id.
func wantNoRaiseBy(t *testing.T, tb *Table, id string) {
	t.Helper()
	res := tb.View("").Result
	if res == nil {
		t.Fatal("hand did not end")
	}
	for _, a := range res.Actions {
		if a.PlayerID == id && a.Raise {
			t.Errorf("%s recorded a raise against a lone all-in: %+v", id, a)
		}
	}
}

func TestNoRaiseAgainstLoneAllInPreflop(t *testing.T) {
	// Heads-up: button 0 posts the small blind and acts first.
	tb := newTestTable(t, 10, 20, 2, []int64{300, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn)
	wantCallOnly(t, tb, 1, 280)
	act(t, tb, 1, Call)
	wantNoRaiseBy(t, tb, idOf(1))
	for _, p := range tb.View("").Result.Players {
		if p.PlayerID == idOf(1) && p.PFR {
			t.Errorf("%s got PFR for a call of an all-in", names[1])
		}
	}
}

func TestNoRaiseAgainstLoneAllInPostflop(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{300, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Check)
	act(t, tb, 1, Check) // flop: the big blind acts first
	act(t, tb, 0, AllIn)
	wantCallOnly(t, tb, 1, 280)
	act(t, tb, 1, Call)
	wantNoRaiseBy(t, tb, idOf(1))
}

func TestNoRaiseAgainstSeveralAllIns(t *testing.T) {
	// Button 0, SB 1, BB 2, UTG 3. Two short stacks go all-in; the deep
	// stack behind them may only call or fold.
	tb := newTestTable(t, 10, 20, 4, []int64{2000, 1000, 2000, 100})
	must(t, tb.StartHand())
	act(t, tb, 3, AllIn)
	act(t, tb, 0, Fold)
	act(t, tb, 1, AllIn)
	wantCallOnly(t, tb, 2, 980) // Carol has 1980 behind
	act(t, tb, 2, Call)
	wantNoRaiseBy(t, tb, idOf(2))
}

func TestRaiseStillOpenWithAnotherDeepStack(t *testing.T) {
	// One short stack is all-in, but a second deep stack can still call a
	// raise, so raising stays open.
	tb := newTestTable(t, 10, 20, 3, []int64{100, 1000, 1000}) // button 0, SB 1, BB 2
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn)
	if l := legal(t, tb, 1); !l.Raise || !l.AllIn || l.MinTo != 180 || l.MaxTo != 1000 {
		t.Fatalf("SB legal = %+v, want a raise from 180 to 1000", l)
	}
}

func TestShortAllInBetMinRaise(t *testing.T) {
	// A short all-in bet is less than a full bet. Bob can still call a
	// raise, so Alice may raise, and the next raise is to 15 + 20.
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 35}) // button 0, SB 1, BB 2
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	act(t, tb, 2, Check)
	act(t, tb, 1, Check) // flop: the small blind acts first
	act(t, tb, 2, Bet, 15)
	if l := legal(t, tb, 0); !l.Raise || l.MinTo != 35 || l.CallAmount != 15 {
		t.Fatalf("Alice legal = %+v", l)
	}
}
