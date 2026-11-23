package server

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 3 stats. The derivation tests use hand-built Results, so they do
// not depend on the engine.

const (
	pre   = engine.Preflop
	flop  = engine.Flop
	turn  = engine.Turn
	river = engine.River
)

// rec builds one ActionRecord. raise is the Raise flag; rb is RaisesBefore.
func rec(p string, s engine.Street, t engine.ActionType, raise bool, rb int) engine.ActionRecord {
	return engine.ActionRecord{PlayerID: p, Street: s, Type: t, Raise: raise, RaisesBefore: rb}
}

// zeroSummaries gives a summary with no chips for each id.
func zeroSummaries(ids ...string) []engine.PlayerSummary {
	out := make([]engine.PlayerSummary, len(ids))
	for i, id := range ids {
		out[i] = engine.PlayerSummary{PlayerID: id}
	}
	return out
}

func hostOf(t *testing.T, f handFacts, id string) HostStats {
	t.Helper()
	pf := f.Players[id]
	if pf == nil {
		t.Fatalf("no facts for %q", id)
	}
	return pf.Host
}

// preflopPart keeps only the preflop counts of h.
func preflopPart(h HostStats) HostStats {
	return HostStats{
		ThreeBet: h.ThreeBet, ThreeBetChances: h.ThreeBetChances,
		FoldTo3Bet: h.FoldTo3Bet, FoldTo3BetChances: h.FoldTo3BetChances,
		Steal: h.Steal, StealChances: h.StealChances,
		Limp: h.Limp, LimpChances: h.LimpChances,
	}
}

// postflopPart keeps only the postflop counts of h.
func postflopPart(h HostStats) HostStats {
	return HostStats{
		CBet: h.CBet, CBetChances: h.CBetChances,
		FoldToCBet: h.FoldToCBet, FoldToCBetChances: h.FoldToCBetChances,
		CheckRaise: h.CheckRaise, CheckRaiseChances: h.CheckRaiseChances,
		PostflopBetsRaises: h.PostflopBetsRaises, PostflopCalls: h.PostflopCalls,
	}
}

func TestPreflopHostStats(t *testing.T) {
	// Order starts at the small blind: S = small blind, B = big blind,
	// U = under the gun, C = cutoff, D = button.
	tests := []struct {
		name    string
		order   []string
		actions []engine.ActionRecord
		want    map[string]HostStats
	}{
		{
			name:  "cutoff open is a steal; callers and folders had a 3-bet chance",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Raise, true, 0),
				rec("D", pre, engine.Fold, false, 1),
				rec("S", pre, engine.Fold, false, 1),
				rec("B", pre, engine.Call, false, 1),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, StealChances: 1, Steal: 1},
				"D": {ThreeBetChances: 1},
				"S": {ThreeBetChances: 1},
				"B": {ThreeBetChances: 1},
			},
		},
		{
			name:  "button steal, small blind 3-bets, button folds to the 3-bet",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Fold, false, 0),
				rec("D", pre, engine.Raise, true, 0),
				rec("S", pre, engine.Raise, true, 1),
				rec("B", pre, engine.Fold, false, 2),
				rec("D", pre, engine.Fold, false, 2),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, StealChances: 1},
				"D": {LimpChances: 1, StealChances: 1, Steal: 1, FoldTo3BetChances: 1, FoldTo3Bet: 1},
				"S": {ThreeBetChances: 1, ThreeBet: 1},
				"B": {},
			},
		},
		{
			name:  "opener calls the 3-bet: a fold-to-3-bet chance without a fold",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Raise, true, 0),
				rec("D", pre, engine.Call, false, 1),
				rec("S", pre, engine.AllIn, true, 1),
				rec("B", pre, engine.Fold, false, 2),
				rec("C", pre, engine.Call, false, 2),
				rec("D", pre, engine.Call, false, 2),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, StealChances: 1, Steal: 1, FoldTo3BetChances: 1},
				"D": {ThreeBetChances: 1},
				"S": {ThreeBetChances: 1, ThreeBet: 1},
				"B": {},
			},
		},
		{
			name:  "a limper before the button: no steal chance for the button",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Call, false, 0),
				rec("D", pre, engine.Raise, true, 0),
				rec("S", pre, engine.Fold, false, 1),
				rec("B", pre, engine.Fold, false, 1),
				rec("C", pre, engine.Fold, false, 1),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, Limp: 1, StealChances: 1, ThreeBetChances: 1},
				"D": {LimpChances: 1},
				"S": {ThreeBetChances: 1},
				"B": {ThreeBetChances: 1},
			},
		},
		{
			name:  "heads-up small blind completes: a limp and a steal chance; big blind has neither",
			order: []string{"S", "B"},
			actions: []engine.ActionRecord{
				rec("S", pre, engine.Call, false, 0),
				rec("B", pre, engine.Check, false, 0),
			},
			want: map[string]HostStats{
				"S": {LimpChances: 1, Limp: 1, StealChances: 1},
				"B": {},
			},
		},
		{
			name:  "3-handed: button and small blind can steal, there is no cutoff",
			order: []string{"S", "B", "D"},
			actions: []engine.ActionRecord{
				rec("D", pre, engine.Fold, false, 0),
				rec("S", pre, engine.Raise, true, 0),
				rec("B", pre, engine.Raise, true, 1),
				rec("S", pre, engine.Fold, false, 2),
			},
			want: map[string]HostStats{
				"D": {LimpChances: 1, StealChances: 1},
				"S": {LimpChances: 1, StealChances: 1, Steal: 1, FoldTo3BetChances: 1, FoldTo3Bet: 1},
				"B": {ThreeBetChances: 1, ThreeBet: 1},
			},
		},
		{
			name:  "under the gun open is not a steal",
			order: []string{"S", "B", "U", "C", "D"},
			actions: []engine.ActionRecord{
				rec("U", pre, engine.Raise, true, 0),
				rec("C", pre, engine.Fold, false, 1),
				rec("D", pre, engine.Fold, false, 1),
				rec("S", pre, engine.Fold, false, 1),
				rec("B", pre, engine.Fold, false, 1),
			},
			want: map[string]HostStats{
				"U": {LimpChances: 1},
				"C": {ThreeBetChances: 1},
				"D": {ThreeBetChances: 1},
				"S": {ThreeBetChances: 1},
				"B": {ThreeBetChances: 1},
			},
		},
		{
			name:  "the 3-bet chance counts once per hand: the first decision",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Raise, true, 0),
				rec("D", pre, engine.Call, false, 1),
				rec("S", pre, engine.AllIn, false, 1), // all-in for less: not a raise
				rec("B", pre, engine.Fold, false, 1),
				rec("D", pre, engine.Raise, true, 1),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, StealChances: 1, Steal: 1},
				"D": {ThreeBetChances: 1},
				"S": {ThreeBetChances: 1},
				"B": {ThreeBetChances: 1},
			},
		},
		{
			name:  "everyone folds to the big blind: only limp and steal chances",
			order: []string{"S", "B", "C", "D"},
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Fold, false, 0),
				rec("D", pre, engine.Fold, false, 0),
				rec("S", pre, engine.Fold, false, 0),
			},
			want: map[string]HostStats{
				"C": {LimpChances: 1, StealChances: 1},
				"D": {LimpChances: 1, StealChances: 1},
				"S": {LimpChances: 1, StealChances: 1},
				"B": {},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &engine.Result{Order: tc.order, Actions: tc.actions, Players: zeroSummaries(tc.order...), BigBlind: 20}
			f := deriveHand(res, nil)
			for id, want := range tc.want {
				if got := preflopPart(hostOf(t, f, id)); got != want {
					t.Errorf("%s:\n got %+v\nwant %+v", id, got, want)
				}
			}
		})
	}
}

func TestPostflopHostStats(t *testing.T) {
	// D raises preflop and the blinds call, so D is the c-bettor.
	preRaise := []engine.ActionRecord{
		rec("C", pre, engine.Fold, false, 0),
		rec("D", pre, engine.Raise, true, 0),
		rec("S", pre, engine.Call, false, 1),
		rec("B", pre, engine.Call, false, 1),
	}
	with := func(post ...engine.ActionRecord) []engine.ActionRecord {
		return append(append([]engine.ActionRecord(nil), preRaise...), post...)
	}
	tests := []struct {
		name    string
		actions []engine.ActionRecord
		want    map[string]HostStats
	}{
		{
			name: "checked to the raiser, c-bet, both fold",
			actions: with(
				rec("S", flop, engine.Check, false, 0),
				rec("B", flop, engine.Check, false, 0),
				rec("D", flop, engine.Bet, true, 0),
				rec("S", flop, engine.Fold, false, 1),
				rec("B", flop, engine.Fold, false, 1),
			),
			want: map[string]HostStats{
				"D": {CBetChances: 1, CBet: 1, PostflopBetsRaises: 1},
				"S": {FoldToCBetChances: 1, FoldToCBet: 1, CheckRaiseChances: 1},
				"B": {FoldToCBetChances: 1, FoldToCBet: 1, CheckRaiseChances: 1},
			},
		},
		{
			name: "donk bet: no c-bet chance and no fold-to-c-bet chance",
			actions: with(
				rec("S", flop, engine.Bet, true, 0),
				rec("B", flop, engine.Fold, false, 1),
				rec("D", flop, engine.Raise, true, 1),
				rec("S", flop, engine.Fold, false, 2),
			),
			want: map[string]HostStats{
				"D": {PostflopBetsRaises: 1},
				"S": {PostflopBetsRaises: 1},
				"B": {},
			},
		},
		{
			name: "check after a check: a c-bet chance without a c-bet",
			actions: with(
				rec("S", flop, engine.Check, false, 0),
				rec("B", flop, engine.Check, false, 0),
				rec("D", flop, engine.Check, false, 0),
			),
			want: map[string]HostStats{
				"D": {CBetChances: 1},
				"S": {},
				"B": {},
			},
		},
		{
			name: "check-raise after the c-bet: the late player faced a raise, not the c-bet",
			actions: with(
				rec("S", flop, engine.Check, false, 0),
				rec("B", flop, engine.Check, false, 0),
				rec("D", flop, engine.Bet, true, 0),
				rec("S", flop, engine.Raise, true, 1),
				rec("B", flop, engine.Fold, false, 2),
				rec("D", flop, engine.Call, false, 2),
			),
			want: map[string]HostStats{
				"D": {CBetChances: 1, CBet: 1, PostflopBetsRaises: 1, PostflopCalls: 1},
				"S": {FoldToCBetChances: 1, CheckRaiseChances: 1, CheckRaise: 1, PostflopBetsRaises: 1},
				"B": {CheckRaiseChances: 1},
			},
		},
		{
			name: "turn check-raise; a check that nobody bets into is no chance; all-in call and all-in raise",
			actions: with(
				rec("S", flop, engine.Fold, false, 0),
				rec("B", flop, engine.Check, false, 0),
				rec("D", flop, engine.Check, false, 0),
				rec("B", turn, engine.Check, false, 0),
				rec("D", turn, engine.Bet, true, 0),
				rec("B", turn, engine.AllIn, true, 1),
				rec("D", turn, engine.AllIn, false, 2),
			),
			want: map[string]HostStats{
				"D": {CBetChances: 1, PostflopBetsRaises: 1, PostflopCalls: 1},
				"S": {},
				"B": {CheckRaiseChances: 1, CheckRaise: 1, PostflopBetsRaises: 1},
			},
		},
		{
			name: "river: check, bet, call is a check-raise chance without a raise",
			actions: with(
				rec("S", flop, engine.Fold, false, 0),
				rec("B", flop, engine.Bet, true, 0),
				rec("D", flop, engine.Call, false, 1),
				rec("B", turn, engine.Check, false, 0),
				rec("D", turn, engine.Check, false, 0),
				rec("B", river, engine.Check, false, 0),
				rec("D", river, engine.Bet, true, 0),
				rec("B", river, engine.Call, false, 1),
			),
			want: map[string]HostStats{
				"D": {PostflopBetsRaises: 1, PostflopCalls: 1},
				"S": {},
				"B": {PostflopBetsRaises: 1, PostflopCalls: 1, CheckRaiseChances: 1},
			},
		},
		{
			name: "no preflop raise: no c-bet",
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Call, false, 0),
				rec("D", pre, engine.Call, false, 0),
				rec("S", pre, engine.Call, false, 0),
				rec("B", pre, engine.Check, false, 0),
				rec("S", flop, engine.Check, false, 0),
				rec("B", flop, engine.Check, false, 0),
				rec("C", flop, engine.Bet, true, 0),
				rec("D", flop, engine.Fold, false, 1),
				rec("S", flop, engine.Fold, false, 1),
				rec("B", flop, engine.Fold, false, 1),
			},
			want: map[string]HostStats{
				"C": {PostflopBetsRaises: 1},
				"D": {},
				"S": {CheckRaiseChances: 1},
				"B": {CheckRaiseChances: 1},
			},
		},
		{
			name: "the preflop raiser is all-in: no flop decision, no chance",
			actions: []engine.ActionRecord{
				rec("C", pre, engine.Fold, false, 0),
				rec("D", pre, engine.AllIn, true, 0),
				rec("S", pre, engine.Call, false, 1),
				rec("B", pre, engine.Call, false, 1),
				rec("S", flop, engine.Bet, true, 0),
				rec("B", flop, engine.Fold, false, 1),
			},
			want: map[string]HostStats{
				"D": {},
				"S": {PostflopBetsRaises: 1},
				"B": {},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			order := []string{"S", "B", "C", "D"}
			res := &engine.Result{Order: order, Actions: tc.actions, Players: zeroSummaries(order...), BigBlind: 20}
			f := deriveHand(res, nil)
			for id, want := range tc.want {
				if got := postflopPart(hostOf(t, f, id)); got != want {
					t.Errorf("%s:\n got %+v\nwant %+v", id, got, want)
				}
			}
		})
	}
}

// equityReady reports whether cards.Equity is implemented (the cards
// package may still have a stub that panics).
func equityReady() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	cards.Equity([][]cards.Card{cards.MustParseMany("As Ad"), cards.MustParseMany("Ks Kd")}, cards.MustParseMany("2c 3d 4h 9s"), 100)
	return true
}

// showdown builds a showdown Result of two players a and b who each put in
// 500. aWon and bWon are their wins from the main pot.
func showdown(aHole, bHole, board string, runoutFrom int, aWon, bWon int64) *engine.Result {
	res := &engine.Result{
		Board:      cards.MustParseMany(board),
		BigBlind:   20,
		RunoutFrom: runoutFrom,
		Order:      []string{"a", "b"},
		Shown: []engine.Shown{
			{PlayerID: "a", Cards: cards.MustParseMany(aHole)},
			{PlayerID: "b", Cards: cards.MustParseMany(bHole)},
		},
		Players: []engine.PlayerSummary{
			{PlayerID: "a", Committed: 500, Won: aWon, AllIn: true, SawFlop: true, WentToShowdown: true, WonAtShowdown: aWon > 0},
			{PlayerID: "b", Committed: 500, Won: bWon, AllIn: true, SawFlop: true, WentToShowdown: true, WonAtShowdown: bWon > 0},
		},
	}
	if aWon > 0 {
		res.Winnings = append(res.Winnings, engine.Winning{PlayerID: "a", Amount: aWon})
	}
	if bWon > 0 {
		res.Winnings = append(res.Winnings, engine.Winning{PlayerID: "b", Amount: bWon})
	}
	return res
}

func TestBadBeatsAndLuckyRivers(t *testing.T) {
	if !equityReady() {
		t.Skip("cards.Equity is still a stub")
	}
	type want struct {
		badBeat  bool
		equity   float64
		riverWon int64
		river    string
	}
	tests := []struct {
		name string
		res  *engine.Result
		want map[string]want
	}{
		{
			// Turn: K♥ 7♦ 2♣ 9♠. Kings full of nothing: a has a set, b has
			// aces. b wins only with the two aces left: a has 42/44.
			name: "set loses to a two-outer on the river",
			res:  showdown("Kc Ks", "Ac Ad", "Kh 7d 2c 9s Ah", 4, 0, 1000),
			want: map[string]want{
				"a": {badBeat: true, equity: 42.0 / 44.0},
				"b": {riverWon: 1000, river: "Ah"},
			},
		},
		{
			// Turn: A♥ A♦ K♣ K♠. a has the queen kicker; b splits on a
			// queen (3), an ace (2) or a king (2): a has (37 + 7/2)/44.
			name: "split on the river: a bad beat for a, a lucky river for b",
			res:  showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 4, 500, 500),
			want: map[string]want{
				"a": {badBeat: true, equity: 40.5 / 44.0},
				"b": {riverWon: 500, river: "Qh"},
			},
		},
		{
			name: "split without a run-out: no bad beat, still a lucky river",
			res:  showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 5, 500, 500),
			want: map[string]want{
				"a": {},
				"b": {riverWon: 500, river: "Qh"},
			},
		},
		{
			name: "the favourite wins: nothing",
			res:  showdown("Kc Ks", "Ac Ad", "Kh 7d 2c 9s 3h", 4, 1000, 0),
			want: map[string]want{"a": {}, "b": {}},
		},
		{
			// Flop K♥ 7♦ 2♣: b needs runner-runner. The winner was ahead on
			// the turn, so the river did not save b.
			name: "the turn already decided it: no lucky river",
			res:  showdown("Kc Ks", "Ac Ad", "Kh 7d 2c As 3h", 3, 0, 1000),
			want: map[string]want{
				"a": {badBeat: true, equity: -1}, // equity checked below
				"b": {},
			},
		},
		{
			name: "no showdown: nothing",
			res: func() *engine.Result {
				r := showdown("Kc Ks", "Ac Ad", "Kh 7d 2c 9s Ah", 4, 0, 1000)
				r.NoShowdown = true
				return r
			}(),
			want: map[string]want{"a": {}, "b": {}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := deriveHand(tc.res, nil)
			for id, w := range tc.want {
				pf := f.Players[id]
				if pf.BadBeat != w.badBeat {
					t.Errorf("%s: bad beat %v, want %v", id, pf.BadBeat, w.badBeat)
				}
				if w.badBeat && w.equity >= 0 && math.Abs(pf.BeatEquity-w.equity) > 1e-9 {
					t.Errorf("%s: equity %.6f, want %.6f", id, pf.BeatEquity, w.equity)
				}
				if w.badBeat && (pf.BeatEquity < badBeatEquity || len(pf.BeatWith) != 2) {
					t.Errorf("%s: worst beat %.3f with %v", id, pf.BeatEquity, pf.BeatWith)
				}
				if pf.RiverWon != w.riverWon {
					t.Errorf("%s: river won %d, want %d", id, pf.RiverWon, w.riverWon)
				}
				if w.river != "" && pf.RiverCard.String() != w.river {
					t.Errorf("%s: river card %v, want %s", id, pf.RiverCard, w.river)
				}
			}
		})
	}
}

func TestLuckyRiverWithoutEquity(t *testing.T) {
	// No run-out, so Equity is not called; this test runs with a stub too.
	f := deriveHand(showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 5, 500, 500), nil)
	if f.Players["b"].RiverWon != 500 || f.Players["a"].RiverWon != 0 || f.Players["a"].BadBeat {
		t.Errorf("facts %+v %+v", *f.Players["a"], *f.Players["b"])
	}
}

// record adds one hand to st as the room does: the batch 2 and batch 3 stats.
func record(st *state, res *engine.Result) {
	st.recordHand(res)
	st.applyHand(deriveHand(res, nil))
}

func TestFunStatsAccumulate(t *testing.T) {
	st := newState(testTime)
	st.Accounts["a"] = &account{Name: "Ann"}
	st.Accounts["b"] = &account{Name: "Ben"}
	// b gets lucky twice; the bigger river stays.
	record(st, showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 5, 500, 500))
	record(st, showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qd", 5, 500, 500))
	big := showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qs", 5, 500, 500)
	big.Players[1].Won = 700
	record(st, big)
	record(st, showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 5, 500, 500))
	b := st.Accounts["b"].Fun
	if b.LuckiestRiver != 700 || b.RiverCard != "Qs" || b.Hands != 4 || b.FirstWinAfter != 1 {
		t.Errorf("fun %+v", b)
	}
	rows, _ := st.statsTable(nil, nil, nil)
	row, _ := statRow(Snapshot{Stats: rows}, "Ben")
	if row.LuckiestRiver != 700 || row.RiverCard.String() != "Qs" {
		t.Errorf("row %+v", row)
	}
}

func TestBadBeatAccumulates(t *testing.T) {
	if !equityReady() {
		t.Skip("cards.Equity is still a stub")
	}
	st := newState(testTime)
	st.Accounts["a"] = &account{Name: "Ann"}
	st.Accounts["b"] = &account{Name: "Ben"}
	record(st, showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 4, 500, 500))
	record(st, showdown("Kc Ks", "Ac Ad", "Kh 7d 2c 9s Ah", 4, 0, 1000))
	record(st, showdown("Qc Jc", "Td 2h", "Ah Ad Kc Ks Qh", 4, 500, 500))
	rows, _ := st.statsTable(nil, nil, nil)
	row, _ := statRow(Snapshot{Stats: rows}, "Ann")
	if row.BadBeats != 3 || math.Abs(row.WorstBeat-42.0/44.0) > 1e-9 ||
		len(row.WorstBeatWith) != 2 || row.WorstBeatWith[0].String() != "Kc" {
		t.Errorf("row %+v", row)
	}
}

func TestPreflopRunoutBadBeat(t *testing.T) {
	if !equityReady() {
		t.Skip("cards.Equity is still a stub")
	}
	// Aces against seven-deuce, all-in preflop: about 88% (sampled).
	f := deriveHand(showdown("As Ah", "7c 2d", "7h 7s 2c Kd 4h", 0, 0, 1000), nil)
	a := f.Players["a"]
	if !a.BadBeat || a.BeatEquity < 0.85 || a.BeatEquity > 0.91 {
		t.Errorf("preflop bad beat %+v", *a)
	}
}

func TestNemesisFlows(t *testing.T) {
	ps := func(nets ...int64) []engine.PlayerSummary {
		out := make([]engine.PlayerSummary, len(nets))
		for i, n := range nets {
			out[i] = engine.PlayerSummary{PlayerID: string(rune('a' + i)), Committed: 1000, Won: 1000 + n}
		}
		return out
	}
	tests := []struct {
		name string
		nets []int64
		want map[string]map[string]int64
	}{
		{"heads-up", []int64{-300, 300}, map[string]map[string]int64{"a": {"b": 300}}},
		{"3-way, one winner", []int64{-300, 400, -100}, map[string]map[string]int64{"a": {"b": 300}, "c": {"b": 100}}},
		{"3-way, two winners", []int64{-300, 200, 100}, map[string]map[string]int64{"a": {"b": 200, "c": 100}}},
		{"3-way, one breaks even", []int64{-300, 300, 0}, map[string]map[string]int64{"a": {"b": 300}}},
		// 5 × 7/10 = 3.5 → 4 and 5 × 3/10 = 1.5 → 2.
		{"rounding", []int64{-5, -5, 7, 3}, map[string]map[string]int64{"a": {"c": 4, "d": 2}, "b": {"c": 4, "d": 2}}},
		{"nobody wins", []int64{0, 0}, map[string]map[string]int64{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveHand(&engine.Result{Players: ps(tc.nets...)}, nil).Flows
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("flows %v, want %v", got, tc.want)
			}
		})
	}
	if got := mulDivRound(math.MaxInt64/2, 3, 4); got != (math.MaxInt64/2/4)*3+(math.MaxInt64/2%4*3+2)/4 {
		t.Errorf("mulDivRound overflow: %d", got)
	}
}

func TestNemesisAcrossHandsAndMerge(t *testing.T) {
	st := newState(testTime)
	for _, id := range []string{"p1", "p2", "p3"} {
		st.Accounts[id] = &account{Name: "n" + id}
	}
	hand := func(nets map[string]int64) {
		var ps []engine.PlayerSummary
		for _, id := range []string{"p1", "p2", "p3"} {
			if n, ok := nets[id]; ok {
				ps = append(ps, engine.PlayerSummary{PlayerID: id, Committed: 1000, Won: 1000 + n})
			}
		}
		record(st, &engine.Result{Players: ps})
	}
	hand(map[string]int64{"p1": -300, "p2": 300})
	hand(map[string]int64{"p1": 100, "p2": -100})
	hand(map[string]int64{"p1": -60, "p2": 20, "p3": 40})
	// p1 → p2: 300 + 20, p2 → p1: 100, p1 → p3: 40.
	if n, c := st.nemesis("p1"); n != "np2" || c != 220 {
		t.Errorf("nemesis of p1 = %q %d", n, c)
	}
	if n, c := st.nemesis("p2"); n != "" || c != 0 {
		t.Errorf("nemesis of p2 = %q %d", n, c)
	}
	rows, _ := st.statsTable(nil, nil, nil)
	if row, _ := statRow(Snapshot{Stats: rows}, "np1"); row.Nemesis != "np2" || row.NemesisChips != 220 {
		t.Errorf("row %+v", row)
	}

	// Merge p2 into p3: p1 lost 320 to p2 and 40 to p3; p2 lost 100 to p1.
	if err := st.merge("np2", "np3"); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int64{
		"p1": {"p3": 360},
		"p3": {"p1": 100},
	}
	for id, w := range want {
		if got := st.Accounts[id].Fun.LostTo; !reflect.DeepEqual(got, w) {
			t.Errorf("%s lost to %v, want %v", id, got, w)
		}
	}
	if n, c := st.nemesis("p1"); n != "np3" || c != 260 {
		t.Errorf("nemesis of p1 after merge = %q %d", n, c)
	}
	// A merge into the winner removes the self flow.
	if err := st.merge("np1", "np3"); err != nil {
		t.Fatal(err)
	}
	if got := st.Accounts["p3"].Fun.LostTo; got != nil {
		t.Errorf("self flow kept: %v", got)
	}
}

func TestStartingHands(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"As", "Kd", "AKo"},
		{"Kd", "As", "AKo"},
		{"Ah", "Kh", "AKs"},
		{"7c", "7d", "77"},
		{"9h", "Th", "T9s"},
		{"2c", "Ac", "A2s"},
		{"3d", "2s", "32o"},
	}
	for _, tc := range tests {
		a, b := cards.MustParseMany(tc.a)[0], cards.MustParseMany(tc.b)[0]
		if got := startingHand(a, b); got != tc.want {
			t.Errorf("startingHand(%s, %s) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
	favs := []struct {
		counts map[string]int
		want   string
		n      int
	}{
		{map[string]int{"77": 2, "A2o": 2}, "A2o", 2},
		{map[string]int{"AKo": 2, "AKs": 2}, "AKs", 2},
		{map[string]int{"AA": 1, "KK": 3}, "KK", 3},
		{map[string]int{"AA": 2, "AKs": 2}, "AA", 2},
		{map[string]int{"T9s": 1, "98s": 1, "T8s": 1}, "T9s", 1},
		{nil, "", 0},
	}
	for _, tc := range favs {
		if h, n := favourite(tc.counts); h != tc.want || n != tc.n {
			t.Errorf("favourite(%v) = %q %d, want %q %d", tc.counts, h, n, tc.want, tc.n)
		}
	}
	// Only VPIP hands count.
	res := &engine.Result{Players: []engine.PlayerSummary{{PlayerID: "a", VPIP: true}, {PlayerID: "b"}}}
	holes := map[string][]cards.Card{"a": cards.MustParseMany("Ts 9s"), "b": cards.MustParseMany("Ac Ad")}
	f := deriveHand(res, holes)
	if f.Players["a"].Starting != "T9s" || f.Players["b"].Starting != "" {
		t.Errorf("starting %q %q", f.Players["a"].Starting, f.Players["b"].Starting)
	}
}

func TestStreaksNetBBAndAllIns(t *testing.T) {
	st := newState(testTime)
	st.Accounts["a"] = &account{Name: "Ann"}
	type hand struct {
		committed, won int64
		allIn, sd      bool
	}
	hands := []hand{
		{40, 0, false, false},
		{100, 160, true, true},
		{20, 40, false, false},
		{100, 0, true, true},
		{10, 20, false, false},
	}
	for _, h := range hands {
		p := engine.PlayerSummary{PlayerID: "a", Committed: h.committed, Won: h.won, AllIn: h.allIn, WentToShowdown: h.sd, WonAtShowdown: h.sd && h.won > 0}
		st.applyHand(deriveHand(&engine.Result{Players: []engine.PlayerSummary{p}, BigBlind: 20}, nil))
	}
	a := st.Accounts["a"]
	h := a.Host
	// Nets: -40 +60 +20 -100 +10 = -50 chips = -2.5 big blinds.
	if math.Abs(h.NetBB-(-2.5)) > 1e-9 || h.BiggestLoss != 100 || h.LongestWinStreak != 2 || h.CurrentWinStreak != 1 ||
		h.AllInShowdowns != 2 || h.AllInShowdownsWon != 1 {
		t.Errorf("host %+v", h)
	}
	if a.Fun.FirstWinAfter != 2 || a.Fun.Hands != 5 {
		t.Errorf("fun %+v", a.Fun)
	}
	// A loss resets the current streak; the first win stays.
	st.applyHand(deriveHand(&engine.Result{Players: []engine.PlayerSummary{{PlayerID: "a", Committed: 10}}, BigBlind: 20}, nil))
	if a.Host.CurrentWinStreak != 0 || a.Host.LongestWinStreak != 2 || a.Fun.FirstWinAfter != 2 {
		t.Errorf("after a loss %+v %+v", a.Host, a.Fun)
	}
}

func TestDeriveHandIgnoresUnknownPlayers(t *testing.T) {
	res := &engine.Result{
		Order:   []string{"a", "b"},
		Players: zeroSummaries("a"),
		Actions: []engine.ActionRecord{rec("ghost", pre, engine.Raise, true, 0), rec("a", pre, engine.Fold, false, 1)},
	}
	f := deriveHand(res, nil)
	if len(f.Players) != 1 || f.Players["a"].Host.ThreeBetChances != 1 {
		t.Errorf("facts %+v", f.Players)
	}
	st := newState(testTime)
	st.applyHand(f) // no accounts: no panic
}

func TestMarkSettledKeepsBatch3Stats(t *testing.T) {
	st := newState(testTime)
	a := &account{Name: "Ann", Bank: 500, BoughtChips: 1000,
		Fun:  funStats{Hands: 3, BadBeats: 1, LostTo: map[string]int64{"p9": 5}, Starting: map[string]int{"AA": 2}},
		Host: HostStats{ThreeBet: 1, ThreeBetChances: 2, SeatedSeconds: 60, NetBB: 1.5}}
	st.Accounts["p1"] = a
	fun, host := a.Fun, a.Host
	st.closePeriod(testTime, 2, "₹")
	if !reflect.DeepEqual(a.Fun, fun) || a.Host != host {
		t.Errorf("after settle %+v %+v", a.Fun, a.Host)
	}
}

func TestMergeBatch3Stats(t *testing.T) {
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "Ann",
		Fun:  funStats{Hands: 3, BadBeats: 1, WorstBeat: 0.8, WorstBeatWith: []string{"As", "Ad"}, LuckiestRiver: 100, RiverCard: "2c", FirstWinAfter: 3, Starting: map[string]int{"AA": 2}},
		Host: HostStats{ThreeBet: 1, ThreeBetChances: 2, BiggestLoss: 50, LongestWinStreak: 4, CurrentWinStreak: 1, SeatedSeconds: 60, NetBB: 1.5}}
	st.Accounts["p2"] = &account{Name: "Ben",
		Fun:  funStats{Hands: 2, BadBeats: 2, WorstBeat: 0.9, WorstBeatWith: []string{"Ks", "Kd"}, LuckiestRiver: 50, RiverCard: "3c", FirstWinAfter: 1, Starting: map[string]int{"AA": 1, "KK": 1}},
		Host: HostStats{ThreeBet: 2, ThreeBetChances: 3, BiggestLoss: 80, LongestWinStreak: 2, CurrentWinStreak: 2, SeatedSeconds: 30, NetBB: -0.5}}
	if err := st.merge("Ben", "Ann"); err != nil {
		t.Fatal(err)
	}
	a := st.Accounts["p1"]
	wantFun := funStats{Hands: 5, BadBeats: 3, WorstBeat: 0.9, WorstBeatWith: []string{"Ks", "Kd"}, LuckiestRiver: 100, RiverCard: "2c", FirstWinAfter: 1, Starting: map[string]int{"AA": 3, "KK": 1}}
	wantHost := HostStats{ThreeBet: 3, ThreeBetChances: 5, BiggestLoss: 80, LongestWinStreak: 4, CurrentWinStreak: 2, SeatedSeconds: 90, NetBB: 1}
	if !reflect.DeepEqual(a.Fun, wantFun) || a.Host != wantHost {
		t.Errorf("merged\n%+v\n%+v", a.Fun, a.Host)
	}
}

func TestStateV3RoundTripAndV2Migration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	in := newState(testTime)
	in.Accounts["p1"] = &account{Name: "Ann",
		Fun:  funStats{Hands: 3, BadBeats: 1, WorstBeat: 0.75, WorstBeatWith: []string{"As", "Ad"}, LuckiestRiver: 10, RiverCard: "Qh", FirstWinAfter: 2, LostTo: map[string]int64{"p2": 40}, Starting: map[string]int{"AKs": 1}},
		Host: HostStats{CBet: 1, CBetChances: 2, NetBB: -3.25, SeatedSeconds: 600, LongestWinStreak: 3}}
	if err := saveState(path, in); err != nil {
		t.Fatal(err)
	}
	got, migrated, err := loadState(path, testTime)
	if err != nil || migrated || !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip %v %v\n got %+v\nwant %+v", err, migrated, got.Accounts["p1"], in.Accounts["p1"])
	}

	// A version 2 file reads with the new stats at zero, the room backs it
	// up and writes version 3.
	dir := t.TempDir()
	cfg := testConfig()
	cfg.StatePath = filepath.Join(dir, "state.json")
	v2 := `{"version":2,"next_id":2,"hand_number":7,"period":1,"accounts":{"p1":{"name":"Ann","named":true,"keys":["SHA256:ann"],"bank":300,"bought_chips":1000,"buy_ins":1,"stats":{"hands":7,"hands_won":2}}}}`
	if err := os.WriteFile(cfg.StatePath, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, cfg, nil)
	if data, err := os.ReadFile(cfg.StatePath + ".v2.bak"); err != nil || string(data) != v2 {
		t.Errorf("v2 backup: %v", err)
	}
	if data, _ := os.ReadFile(cfg.StatePath); !strings.Contains(string(data), `"version": 3`) {
		t.Errorf("not saved as v3:\n%s", data)
	}
	h.inRun(func(r *Room) {
		a := r.st.Accounts["p1"]
		if a == nil || a.Stats.Hands != 7 || a.Bank != 300 || a.Host != (HostStats{}) || !reflect.DeepEqual(a.Fun, funStats{}) {
			t.Errorf("migrated %+v", a)
		}
	})
}

// seedStats gives each account some fun, favourite and host stats.
func seedStats(r *Room) {
	for _, id := range r.st.ids() {
		a := r.st.Accounts[id]
		a.Stats.Hands = 5
		a.Fun.Starting = map[string]int{"AKs": 2, "77": 1}
		a.Fun.BadBeats = 1
		a.Host.ThreeBet, a.Host.ThreeBetChances = 1, 3
	}
	r.broadcast()
}

func TestStatsPrivacy(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	host := h.connectWith("key:SHA256:host", "hosty", ConnectOptions{Host: true})
	ann := h.connectWith("key:SHA256:ann", "ann", ConnectOptions{})
	ben := h.connectWith("key:SHA256:ben", "ben", ConnectOptions{Admin: true})
	h.inRun(seedStats)

	check := func(s *Session, wantHost bool) {
		t.Helper()
		snap := h.snap(s)
		if snap.Me.Host != wantHost {
			t.Errorf("%s: Me.Host = %v", s.name, snap.Me.Host)
		}
		if len(snap.Stats) != 3 {
			t.Fatalf("%s: rows %d", s.name, len(snap.Stats))
		}
		for _, row := range snap.Stats {
			if (row.Host != nil) != wantHost {
				t.Errorf("%s sees Host of %s: %v", s.name, row.Name, row.Host)
			}
			if row.Host != nil && (row.Host.ThreeBet != 1 || row.Host.ThreeBetChances != 3) {
				t.Errorf("%s: host row %+v", s.name, *row.Host)
			}
			own := row.Name == s.name
			if own && (row.FavouriteHand != "AKs" || row.FavouriteCount != 2) {
				t.Errorf("%s: own favourite %q %d", s.name, row.FavouriteHand, row.FavouriteCount)
			}
			if !own && (row.FavouriteHand != "" || row.FavouriteCount != 0) {
				t.Errorf("%s sees the favourite hand of %s", s.name, row.Name)
			}
			if row.BadBeats != 1 {
				t.Errorf("%s: fun stats of %s missing", s.name, row.Name)
			}
		}
	}
	check(host, true)
	check(ann, false)
	check(ben, false)
	if !h.snap(host).Me.Admin || h.snap(ann).Me.Admin || !h.snap(ben).Me.Admin {
		t.Error("admin flags: a host is an admin")
	}
	must(t, host.MarkSettled())

	// Each snapshot gets its own copies.
	orig := []PlayerStats{{Host: &HostStats{ThreeBet: 1}, WorstBeatWith: cards.MustParseMany("As Ad")}}
	cp := cloneStats(orig)
	cp[0].Host.ThreeBet = 99
	cp[0].WorstBeatWith[0] = cards.Card{}
	if orig[0].Host.ThreeBet != 1 || orig[0].WorstBeatWith[0].String() != "As" {
		t.Error("cloneStats shares data")
	}

	h.stop()
	rows, _, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("ReadStats rows %d", len(rows))
	}
	for _, row := range rows {
		if row.Host == nil || row.Host.ThreeBetChances != 3 {
			t.Errorf("ReadStats host of %s: %v", row.Name, row.Host)
		}
		if row.FavouriteHand != "" || row.FavouriteCount != 0 {
			t.Errorf("ReadStats shows the favourite hand of %s", row.Name)
		}
	}
}

func TestFavouriteHandFromRealHand(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	for _, s := range []*Session{alice, bob} {
		snap := h.snap(s)
		if snap.Table.InHand {
			t.Fatal("hand did not end")
		}
		for _, row := range snap.Stats {
			own := row.Name == s.name
			switch {
			case own && (row.FavouriteCount != 1 || (row.FavouriteHand != "AA" && row.FavouriteHand != "A2o")):
				t.Errorf("%s: own favourite %q %d", s.name, row.FavouriteHand, row.FavouriteCount)
			case !own && row.FavouriteHand != "":
				t.Errorf("%s sees %s's favourite %q", s.name, row.Name, row.FavouriteHand)
			}
			if row.Host != nil {
				t.Errorf("%s sees host stats", s.name)
			}
		}
	}
	// Batch 10: the room keeps the hole cards of the last hand until the
	// next deal (for Me.RabbitHand), with the number of that hand.
	h.inRun(func(r *Room) {
		res := r.table.View("").Result
		if res == nil || r.holesHand != res.HandNumber || len(r.holes) != 2 {
			t.Errorf("holes after the hand: %v (hand %d)", r.holes, r.holesHand)
		}
	})
}

func TestSeatedSeconds(t *testing.T) {
	cfg := testConfig()
	cfg.TurnTimeout = 0
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(30 * time.Second)
	must(t, alice.SitOut(true))
	h.advance(60 * time.Second)
	must(t, alice.SitOut(false))
	h.advance(10*time.Second + 500*time.Millisecond)
	secs := func(s *Session) (n int64) {
		h.inRun(func(r *Room) { r.accrueSeated(); n = r.st.Accounts[s.key].Host.SeatedSeconds })
		return n
	}
	if a, b := secs(alice), secs(bob); a != 40 || b != 100 {
		t.Errorf("seated seconds: alice %d, bob %d", a, b)
	}
	h.advance(500 * time.Millisecond) // the half seconds add up
	if b := secs(bob); b != 101 {
		t.Errorf("bob %d", b)
	}
	// The saved file has the time too.
	key := bob.key
	h.advance(9 * time.Second)
	h.stop()
	st, _, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Accounts[key].Host.SeatedSeconds; got != 110 {
		t.Errorf("saved seated seconds %d", got)
	}
}

// A real 3-handed hand through the engine: the button opens and the blinds
// fold. The host sees the steal and the 3-bet chances; the others do not.
func TestHostStatsFromRealHand(t *testing.T) {
	cfg := testConfig()
	h := newHarness(t, cfg, nil)
	ann := h.connectWith("key:SHA256:ann", "ann", ConnectOptions{Host: true})
	ben := h.connect("key:ben", "ben")
	cat := h.connect("key:cat", "cat")
	players := []*Session{ann, ben, cat}
	for _, s := range players {
		must(t, s.Sit(-1))
	}
	h.advance(cfg.NextHandDelay)
	v := h.snap(ann).Table
	if !v.InHand {
		t.Fatal("hand did not start")
	}
	btn := v.Seats[v.Button].PlayerID
	actor, _ := h.toAct(players...)
	if actor.key != btn {
		t.Fatalf("button %s does not act first (actor %s)", btn, actor.key)
	}
	must(t, actor.Act(engine.Action{Type: engine.Raise, Amount: 60}))
	for range 2 {
		next, _ := h.toAct(players...)
		must(t, next.Act(engine.Action{Type: engine.Fold}))
	}
	snap := h.snap(ann)
	if snap.Table.InHand {
		t.Fatal("hand did not end")
	}
	for _, row := range snap.Stats {
		if row.Host == nil {
			t.Fatalf("host sees no host stats for %s", row.Name)
		}
		hs := *row.Host
		switch row.Name {
		case actor.name:
			if hs.Steal != 1 || hs.StealChances != 1 || hs.LimpChances != 1 || math.Abs(hs.NetBB-1.5) > 1e-9 ||
				hs.CurrentWinStreak != 1 || row.FirstWinAfter != 1 {
				t.Errorf("button %+v first win %d", hs, row.FirstWinAfter)
			}
		default:
			if hs.ThreeBetChances != 1 || hs.ThreeBet != 0 || hs.NetBB >= 0 || hs.BiggestLoss == 0 {
				t.Errorf("blind %s %+v", row.Name, hs)
			}
		}
	}
	for _, s := range []*Session{ben, cat} {
		for _, row := range h.snap(s).Stats {
			if row.Host != nil {
				t.Errorf("%s sees host stats", s.name)
			}
		}
	}
}
