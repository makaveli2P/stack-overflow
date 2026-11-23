package server

import (
	"fmt"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// rvStep is one scripted decision: the player id that must act and what it
// does.
type rvStep struct {
	id string
	a  engine.Action
}

func rvCheck(id string) rvStep { return rvStep{id: id, a: engine.Action{Type: engine.Check}} }
func rvCall(id string) rvStep  { return rvStep{id: id, a: engine.Action{Type: engine.Call}} }
func rvAllIn(id string) rvStep { return rvStep{id: id, a: engine.Action{Type: engine.AllIn}} }

// rvTable seats p0..p(n-1) and starts one hand.
func rvTable(t *testing.T, sb, bb int64, deck string, stacks []int64) *engine.Table {
	t.Helper()
	tb, err := engine.NewTable(engine.Config{Seats: len(stacks), SmallBlind: sb, BigBlind: bb}, stackedDeck(deck))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range stacks {
		id := fmt.Sprintf("p%d", i)
		if err := tb.Sit(i, id, id, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := tb.StartHand(); err != nil {
		t.Fatal(err)
	}
	return tb
}

// When every other live player is all-in, nobody can call a raise, so the
// engine offers no raise (Legal.Raise is false). The host stats must not
// count a 3-bet (preflop), or a check-raise and AF bets (flop), in this spot.
func TestStatsNoRaiseAgainstLoneAllIn(t *testing.T) {
	// Preflop, heads-up: p0 (button/SB) shoves 300; p1 (BB, 1000) is the only
	// other player.
	tb := rvTable(t, 10, 20, "", []int64{300, 1000})
	if err := tb.Act("p0", engine.Action{Type: engine.AllIn}); err != nil {
		t.Fatal(err)
	}
	l, _ := tb.LegalFor("p1")
	if l.Raise {
		t.Errorf("preflop: p1 may raise an all-in with nobody left to call: %+v", l)
		if err := tb.Act("p1", engine.Action{Type: engine.Raise, Amount: 600}); err != nil {
			t.Fatal(err)
		}
	} else if err := tb.Act("p1", engine.Action{Type: engine.Call}); err != nil {
		t.Fatal(err)
	}
	res := tb.View("").Result
	if res == nil {
		t.Fatal("hand did not end")
	}
	h := deriveHand(res, nil).Players["p1"].Host
	if h.ThreeBet != 0 {
		t.Errorf("preflop: p1 counted a 3-bet against a lone all-in: ThreeBet %d/%d (actions %+v)", h.ThreeBet, h.ThreeBetChances, res.Actions)
	}

	// Flop, heads-up: p1 checks, p0 bets all-in 280, p1 "raises".
	tb = rvTable(t, 10, 20, "", []int64{300, 1000})
	for _, s := range []rvStep{rvCall("p0"), rvCheck("p1"), rvCheck("p1"), rvAllIn("p0")} {
		if err := tb.Act(s.id, s.a); err != nil {
			t.Fatal(err)
		}
	}
	l, _ = tb.LegalFor("p1")
	if l.Raise {
		t.Errorf("flop: p1 may raise an all-in with nobody left to call: %+v", l)
		if err := tb.Act("p1", engine.Action{Type: engine.Raise, Amount: 600}); err != nil {
			t.Fatal(err)
		}
	} else if err := tb.Act("p1", engine.Action{Type: engine.Call}); err != nil {
		t.Fatal(err)
	}
	res = tb.View("").Result
	if res == nil {
		t.Fatal("hand did not end")
	}
	h = deriveHand(res, nil).Players["p1"].Host
	if h.CheckRaise != 0 || h.PostflopBetsRaises != 0 {
		t.Errorf("flop: p1 counted a check-raise %d/%d and AF bets %d against a lone all-in",
			h.CheckRaise, h.CheckRaiseChances, h.PostflopBetsRaises)
	}
}
