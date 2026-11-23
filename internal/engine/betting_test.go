package engine

import (
	"testing"
)

func TestMinRaiseSizes(t *testing.T) {
	tb := newTestTable(t, 10, 20, 4, []int64{1000, 1000, 1000, 1000})
	must(t, tb.StartHand()) // button 0, SB 1, BB 2, UTG 3
	if l := legal(t, tb, 3); l.MinTo != 40 || l.MaxTo != 1000 || !l.Raise || l.Bet || l.CallAmount != 20 {
		t.Fatalf("UTG legal = %+v", l)
	}
	mustFail(t, tb.Act(idOf(3), Action{Type: Raise, Amount: 39}), ErrIllegal)
	act(t, tb, 3, Raise, 60) // raises by 40
	if l := legal(t, tb, 0); l.MinTo != 100 {
		t.Fatalf("after raise to 60: MinTo = %d, want 100", l.MinTo)
	}
	mustFail(t, tb.Act(idOf(0), Action{Type: Raise, Amount: 99}), ErrIllegal)
	act(t, tb, 0, Raise, 150) // a raise of the raise, by 90
	if l := legal(t, tb, 1); l.MinTo != 240 || l.CallAmount != 140 {
		t.Fatalf("after raise to 150: legal = %+v, want MinTo 240, call 140", l)
	}
	if v := tb.View(""); v.MinRaiseTo != 240 || v.CurrentBet != 150 {
		t.Fatalf("view MinRaiseTo %d CurrentBet %d", v.MinRaiseTo, v.CurrentBet)
	}
	mustFail(t, tb.Act(idOf(1), Action{Type: Raise, Amount: 1001}), ErrIllegal)
	act(t, tb, 1, Fold)
	act(t, tb, 2, Fold)
	act(t, tb, 3, Call)
	// Postflop: the minimum bet is the big blind.
	wantToAct(t, tb, 3)
	l := legal(t, tb, 3)
	if !l.Bet || l.Raise || l.MinTo != 20 || l.MaxTo != 850 || !l.Check {
		t.Fatalf("flop legal = %+v", l)
	}
	mustFail(t, tb.Act(idOf(3), Action{Type: Raise, Amount: 40}), ErrIllegal)
	mustFail(t, tb.Act(idOf(3), Action{Type: Bet, Amount: 19}), ErrIllegal)
	act(t, tb, 3, Bet, 30)
	if l := legal(t, tb, 0); l.MinTo != 60 {
		t.Fatalf("after bet 30: MinTo = %d, want 60", l.MinTo)
	}
	mustFail(t, tb.Act(idOf(0), Action{Type: Bet, Amount: 60}), ErrIllegal)
	act(t, tb, 0, Raise, 60)
}

func TestIncompleteAllInDoesNotReopen(t *testing.T) {
	// Button 0 (Alice) acts first preflop 3-handed. Carol in the BB has 130.
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 130})
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 100) // full raise by 80
	act(t, tb, 1, Call)
	if l := legal(t, tb, 2); !l.Raise || l.MinTo != 130 || !l.AllIn || l.AllInTo != 130 {
		t.Fatalf("Carol legal = %+v; want an all-in raise to 130", l)
	}
	act(t, tb, 2, AllIn) // raises by 30 only: incomplete
	for _, seat := range []int{0, 1} {
		wantToAct(t, tb, seat)
		l := legal(t, tb, seat)
		if l.Raise || l.Bet || l.AllIn || !l.Call || l.CallAmount != 30 || l.MinTo != 0 {
			t.Fatalf("%s legal = %+v; want call or fold only", names[seat], l)
		}
		mustFail(t, tb.Act(idOf(seat), Action{Type: Raise, Amount: 300}), ErrIllegal)
		mustFail(t, tb.Act(idOf(seat), Action{Type: AllIn}), ErrIllegal)
		act(t, tb, seat, Call)
	}
	if v := tb.View(""); v.Street != Flop || v.TotalPot != 390 {
		t.Fatalf("street %v pot %d, want flop 390", v.Street, v.TotalPot)
	}
}

func TestIncompleteAllInStillAllowsPlayersWhoHaveNotActed(t *testing.T) {
	// UTG raises, the button goes all-in for less than a full raise. The
	// blinds did not act yet, so they may raise; the min raise stays +80.
	tb := newTestTable(t, 10, 20, 4, []int64{130, 1000, 1000, 1000})
	must(t, tb.StartHand()) // button 0, SB 1, BB 2, UTG 3
	act(t, tb, 3, Raise, 100)
	act(t, tb, 0, AllIn) // to 130
	if l := legal(t, tb, 1); !l.Raise || l.MinTo != 210 {
		t.Fatalf("SB legal = %+v, want raise from 210", l)
	}
	act(t, tb, 1, Call)
	act(t, tb, 2, Call)
	if l := legal(t, tb, 3); l.Raise || l.AllIn || l.CallAmount != 30 {
		t.Fatalf("UTG legal = %+v, want call or fold only", l)
	}
}

func TestTwoIncompleteAllInsReopen(t *testing.T) {
	// Dave (UTG) raises to 100 (+80). Alice all-in 150 (+50), Bob all-in
	// 200 (+50). Together +100 >= 80, so Dave may raise again.
	tb := newTestTable(t, 10, 20, 4, []int64{150, 200, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 3, Raise, 100)
	act(t, tb, 0, AllIn)
	act(t, tb, 1, AllIn)
	act(t, tb, 2, Call)
	wantToAct(t, tb, 3)
	l := legal(t, tb, 3)
	if !l.Raise || l.MinTo != 280 || !l.AllIn || l.CallAmount != 100 {
		t.Fatalf("Dave legal = %+v; want raise from 280", l)
	}
	act(t, tb, 3, Raise, 280)
	// Carol now faces a full raise (+80), so raising is open for her.
	if l := legal(t, tb, 2); !l.Raise || l.MinTo != 360 {
		t.Fatalf("Carol legal = %+v", l)
	}
}

func TestIncompleteRaiseAfterBetAndCheck(t *testing.T) {
	// Flop: Bob checks, Carol bets 100, Alice is all-in for 150 (+50).
	// The bet grew by 150 since Bob checked, so Bob may raise. It grew by
	// only 50 since Carol bet, so Carol may only call or fold.
	tb := newTestTable(t, 10, 20, 3, []int64{170, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	act(t, tb, 2, Check)
	act(t, tb, 1, Check)
	act(t, tb, 2, Bet, 100)
	act(t, tb, 0, AllIn)
	if l := legal(t, tb, 1); !l.Raise || l.MinTo != 250 {
		t.Fatalf("Bob legal = %+v", l)
	}
	act(t, tb, 1, Call)
	if l := legal(t, tb, 2); l.Raise || !l.Call || l.CallAmount != 50 {
		t.Fatalf("Carol legal = %+v; want call or fold only", l)
	}
}

func TestBigBlindOption(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	wantToAct(t, tb, 2)
	l := legal(t, tb, 2)
	if !l.Check || l.Call || !l.Raise || l.MinTo != 40 {
		t.Fatalf("BB legal = %+v", l)
	}
	act(t, tb, 2, Raise, 60)
	wantToAct(t, tb, 0)
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	if v := tb.View(""); v.Street != Flop || v.TotalPot != 180 {
		t.Fatalf("street %v pot %d, want flop 180", v.Street, v.TotalPot)
	}
}

func TestActErrors(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	mustFail(t, tb.Act(idOf(0), Action{Type: Check}), ErrNoHand)
	must(t, tb.StartHand())
	mustFail(t, tb.StartHand(), ErrHandRunning)
	mustFail(t, tb.Act("nobody", Action{Type: Fold}), ErrNotSeated)
	mustFail(t, tb.Act(idOf(1), Action{Type: Fold}), ErrNotYourTurn)
	mustFail(t, tb.Act(idOf(0), Action{Type: Check}), ErrIllegal)
	mustFail(t, tb.Act(idOf(0), Action{Type: Bet, Amount: 40}), ErrIllegal)
	mustFail(t, tb.Act(idOf(0), Action{Type: ActionType(99)}), ErrIllegal)
	if _, ok := tb.LegalFor(idOf(1)); ok {
		t.Fatal("LegalFor a player not to act returned ok")
	}
	if a := tb.DefaultAction(idOf(0)); a.Type != Fold {
		t.Fatalf("DefaultAction facing a bet = %v, want Fold", a.Type)
	}
	act(t, tb, 0, Call)
	act(t, tb, 1, Call)
	if a := tb.DefaultAction(idOf(2)); a.Type != Check {
		t.Fatalf("DefaultAction for BB option = %v, want Check", a.Type)
	}
	mustFail(t, tb.Act(idOf(2), Action{Type: Call}), ErrIllegal)
	act(t, tb, 2, Check)
	mustFail(t, tb.Act(idOf(1), Action{Type: Call}), ErrIllegal)
	mustFail(t, tb.AddChips(idOf(1), 100), ErrHandRunning)
}

func TestFoldWhenCheckIsLegal(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Fold) // legal, though a check was possible
	wantStacks(t, tb, map[int]int64{0: 1020, 1: 980})
}

func TestShortStackBetIsAllInOnly(t *testing.T) {
	// Bob has 15 behind on the flop, less than the big blind.
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 35})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)
	act(t, tb, 1, Check)
	l := legal(t, tb, 1)
	if !l.Bet || l.MinTo != 15 || l.MaxTo != 15 || !l.AllIn {
		t.Fatalf("short legal = %+v", l)
	}
	mustFail(t, tb.Act(idOf(1), Action{Type: Bet, Amount: 10}), ErrIllegal)
	act(t, tb, 1, Bet, 15)
	// Bob is all-in, so nobody can call a raise: Alice may only call or fold.
	// TestShortAllInBetMinRaise checks the min-raise after a short bet.
	if l := legal(t, tb, 0); l.Raise || l.AllIn || l.CallAmount != 15 {
		t.Fatalf("Alice legal = %+v", l)
	}
}
