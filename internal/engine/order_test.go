package engine

import (
	"testing"
)

func TestNewTableConfig(t *testing.T) {
	bad := []Config{
		{Seats: 1, SmallBlind: 1, BigBlind: 2},
		{Seats: 11, SmallBlind: 1, BigBlind: 2},
		{Seats: 6, SmallBlind: 0, BigBlind: 2},
		{Seats: 6, SmallBlind: 5, BigBlind: 2},
	}
	for _, cfg := range bad {
		if _, err := NewTable(cfg, nil); err == nil {
			t.Errorf("NewTable(%+v) did not fail", cfg)
		}
	}
	tb, err := NewTable(Config{Seats: 2, SmallBlind: 5, BigBlind: 5}, nil)
	must(t, err)
	if v := tb.View(""); v.Button != -1 || len(v.Seats) != 2 || v.ToAct != -1 || v.MySeat != -1 {
		t.Fatalf("empty table view = %+v", v)
	}
}

func TestHeadsUpOrder(t *testing.T) {
	tb := newTestTable(t, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	v := tb.View("")
	// The button posts the small blind and acts first preflop.
	if v.Button != 0 || v.SmallBlindSeat != 0 || v.BigBlindSeat != 1 {
		t.Fatalf("button %d sb %d bb %d, want 0 0 1", v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
	if v.Seats[0].Bet != 10 || v.Seats[1].Bet != 20 {
		t.Fatalf("blinds %d/%d, want 10/20", v.Seats[0].Bet, v.Seats[1].Bet)
	}
	wantToAct(t, tb, 0)
	act(t, tb, 0, Call)
	// The big blind has the option.
	wantToAct(t, tb, 1)
	if l := legal(t, tb, 1); !l.Check || !l.Raise || l.MinTo != 40 {
		t.Fatalf("BB option = %+v", l)
	}
	act(t, tb, 1, Check)
	// After the flop the big blind acts first.
	for _, street := range []Street{Flop, Turn, River} {
		if got := tb.View("").Street; got != street {
			t.Fatalf("street = %v, want %v", got, street)
		}
		wantToAct(t, tb, 1)
		act(t, tb, 1, Check)
		wantToAct(t, tb, 0)
		act(t, tb, 0, Check)
	}
	if tb.InHand() {
		t.Fatal("hand did not end after the river")
	}

	// The button moves; the new button posts the small blind and acts first.
	must(t, tb.StartHand())
	v = tb.View("")
	if v.Button != 1 || v.SmallBlindSeat != 1 || v.BigBlindSeat != 0 {
		t.Fatalf("hand 2: button %d sb %d bb %d, want 1 1 0", v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
	wantToAct(t, tb, 1)
}

func TestThreeHandedOrder(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	v := tb.View("")
	if v.Button != 0 || v.SmallBlindSeat != 1 || v.BigBlindSeat != 2 {
		t.Fatalf("button %d sb %d bb %d, want 0 1 2", v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
	wantToAct(t, tb, 0)
	act(t, tb, 0, Call)
	wantToAct(t, tb, 1)
	if l := legal(t, tb, 1); l.CallAmount != 10 {
		t.Fatalf("SB call amount = %d, want 10", l.CallAmount)
	}
	act(t, tb, 1, Call)
	wantToAct(t, tb, 2)
	act(t, tb, 2, Check)
	// Postflop: the first live seat after the button.
	wantToAct(t, tb, 1)
	act(t, tb, 1, Check)
	wantToAct(t, tb, 2)
	act(t, tb, 2, Check)
	wantToAct(t, tb, 0)
	act(t, tb, 0, Check)
	if tb.View("").Street != Turn {
		t.Fatal("turn not dealt")
	}
}

func TestSixHandedOrder(t *testing.T) {
	tb := newTestTable(t, 10, 20, 6, []int64{1000, 1000, 1000, 1000, 1000, 1000})
	must(t, tb.StartHand())
	for _, seat := range []int{3, 4, 5, 0, 1} {
		wantToAct(t, tb, seat)
		act(t, tb, seat, Call)
	}
	wantToAct(t, tb, 2)
	act(t, tb, 2, Check)
	for _, seat := range []int{1, 2, 3, 4, 5, 0} {
		wantToAct(t, tb, seat)
		act(t, tb, seat, Check)
	}
	// A bet on the turn: the others act after the bettor, in order.
	act(t, tb, 1, Check)
	act(t, tb, 2, Bet, 50)
	for _, seat := range []int{3, 4, 5, 0, 1} {
		wantToAct(t, tb, seat)
		act(t, tb, seat, Fold)
	}
	wantToAct(t, tb, -1)
	if tb.InHand() {
		t.Fatal("hand did not end when everyone folded")
	}
}

func TestPostflopSkipsFoldedAndAllIn(t *testing.T) {
	// Seat 1 (SB) folds, seat 2 is all-in: postflop seat 3 acts first.
	tb := newTestTable(t, 10, 20, 4, []int64{1000, 1000, 100, 1000})
	must(t, tb.StartHand())
	act(t, tb, 3, Call)
	act(t, tb, 0, Call)
	act(t, tb, 1, Fold)
	act(t, tb, 2, AllIn)
	act(t, tb, 3, Call)
	act(t, tb, 0, Call)
	wantToAct(t, tb, 3)
}

func TestButtonMovement(t *testing.T) {
	tb := newTestTable(t, 10, 20, 5, []int64{1000, 1000, 1000, 1000})
	buttons := []int{}
	for range 5 {
		must(t, tb.StartHand())
		buttons = append(buttons, tb.View("").Button)
		foldToEnd(t, tb)
		if len(buttons) == 2 {
			// Seat 2 leaves between hands; the button skips the empty seat.
			if _, err := tb.Stand(idOf(2)); err != nil {
				t.Fatal(err)
			}
		}
		if len(buttons) == 3 {
			must(t, tb.SetSittingOut(idOf(0), true))
		}
	}
	want := []int{0, 1, 3, 1, 3}
	for i := range want {
		if buttons[i] != want[i] {
			t.Fatalf("buttons = %v, want %v", buttons, want)
		}
	}
}

func TestButtonWhenButtonPlayerLeaves(t *testing.T) {
	tb := newTestTable(t, 10, 20, 4, []int64{1000, 1000, 1000, 1000})
	must(t, tb.StartHand())
	foldToEnd(t, tb)
	must(t, tb.StartHand()) // button 1
	foldToEnd(t, tb)
	if _, err := tb.Stand(idOf(1)); err != nil {
		t.Fatal(err)
	}
	must(t, tb.StartHand())
	if v := tb.View(""); v.Button != 2 || v.SmallBlindSeat != 3 || v.BigBlindSeat != 0 {
		t.Fatalf("button %d sb %d bb %d, want 2 3 0", v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
}

func TestHeadsUpAfterPlayerLeaves(t *testing.T) {
	tb := newTestTable(t, 10, 20, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand()) // button 0
	foldToEnd(t, tb)
	if _, err := tb.Stand(idOf(0)); err != nil {
		t.Fatal(err)
	}
	must(t, tb.StartHand()) // button moves to 1; heads-up: button is SB
	v := tb.View("")
	if v.Button != 1 || v.SmallBlindSeat != 1 || v.BigBlindSeat != 2 {
		t.Fatalf("button %d sb %d bb %d, want 1 1 2", v.Button, v.SmallBlindSeat, v.BigBlindSeat)
	}
	wantToAct(t, tb, 1)
}
