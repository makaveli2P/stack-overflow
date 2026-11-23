package engine

import (
	"slices"
	"strings"
)

// The batch-3 part of the random test: Result.Actions, Order, BigBlind and
// RunoutFrom. Every expected value comes from the event log alone.

// checkActions tests the batch-3 fields of the result of the hand that
// just ended. evs are the events of that hand.
func (f *fuzzer) checkActions(h *hand, res *Result, evs []Event) {
	t := f.t
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf(format+"\nactions %+v\nevents:\n  %s", append(args, res.Actions, strings.Join(eventTexts(evs), "\n  "))...)
	}
	bb := f.tb.cfg.BigBlind
	if res.BigBlind != bb {
		fail("BigBlind %d, want %d", res.BigBlind, bb)
	}

	// Order: the dealt seats in seat order, from the small blind (the
	// first blind event). The button is last, or first heads-up.
	var button string
	var blinds []string
	for _, e := range evs {
		switch e.Kind {
		case EvHandStart:
			button = e.PlayerID
		case EvBlind:
			blinds = append(blinds, e.PlayerID)
		}
	}
	seats := slices.Clone(res.Players)
	slices.SortFunc(seats, func(a, b PlayerSummary) int { return a.Seat - b.Seat })
	sbAt := slices.IndexFunc(seats, func(s PlayerSummary) bool { return s.PlayerID == blinds[0] })
	var want []string
	for i := range seats {
		want = append(want, seats[(sbAt+i)%len(seats)].PlayerID)
	}
	btnAt := len(want) - 1
	if len(want) == 2 {
		btnAt = 0
	}
	if len(blinds) != 2 || !slices.Equal(res.Order, want) || want[btnAt] != button || want[1] != blinds[1] {
		fail("Order %v, want %v (button %s, blinds %v)", res.Order, want, button, blinds)
	}

	// Actions: one record per EvAction event, with the street totals
	// replayed from the blind, action and street events.
	street, high, raises := Preflop, bb, 0
	bets := map[string]int64{}
	board, boardAtLast := 0, 0 // board cards now, and at the last blind or action
	k := 0
	vpip, pfr := map[string]bool{}, map[string]bool{}
	for _, e := range evs {
		switch e.Kind {
		case EvBlind:
			bets[e.PlayerID] += e.Amount
			boardAtLast = board
		case EvStreet:
			street, high, raises = e.Street, 0, 0
			clear(bets)
			board = map[Street]int{Flop: 3, Turn: 4, River: 5}[e.Street]
		case EvAction:
			if k >= len(res.Actions) {
				fail("%d actions, more action events", len(res.Actions))
			}
			a := res.Actions[k]
			k++
			var to int64
			switch e.Action {
			case Fold, Check:
			case Call:
				to = bets[e.PlayerID] + e.Amount
			default: // Bet, Raise and AllIn events carry the street total
				to = e.Amount
			}
			if to > 0 {
				bets[e.PlayerID] = to
			}
			wantRec := ActionRecord{
				PlayerID: e.PlayerID, Street: street, Type: e.Action, To: to,
				BetBefore: high, Raise: to > high, RaisesBefore: raises,
			}
			if a != wantRec || e.Street != street {
				fail("action %d = %+v, want %+v (event %+v)", k-1, a, wantRec, e)
			}
			if !slices.Contains(res.Order, a.PlayerID) {
				fail("action by %s, who is not in Order %v", a.PlayerID, res.Order)
			}
			if a.Raise {
				raises++
				high = to
			}
			if street == Preflop && to > 0 {
				vpip[a.PlayerID] = true
				pfr[a.PlayerID] = pfr[a.PlayerID] || a.Raise
			}
			f.countAction(a)
			boardAtLast = board
		}
	}
	if k != len(res.Actions) {
		fail("%d actions, %d action events", len(res.Actions), k)
	}
	for _, s := range res.Players {
		if s.VPIP != vpip[s.PlayerID] || s.PFR != pfr[s.PlayerID] {
			fail("summary %+v: from actions VPIP %v PFR %v", s, vpip[s.PlayerID], pfr[s.PlayerID])
		}
	}

	// RunoutFrom: a showdown whose last betting came before the river.
	wantRunout := 5
	if !res.NoShowdown && boardAtLast < 5 {
		wantRunout = boardAtLast
	}
	if res.RunoutFrom != wantRunout {
		fail("RunoutFrom %d, want %d (no showdown %v)", res.RunoutFrom, wantRunout, res.NoShowdown)
	}
	switch res.RunoutFrom {
	case 0:
		f.seen.runoutPre++
	case 3:
		f.seen.runoutFlop++
	case 4:
		f.seen.runoutTurn++
	}

	// The View must hold a deep copy of the batch-3 fields.
	v := f.tb.View("")
	if !slices.Equal(v.Result.Actions, res.Actions) || !slices.Equal(v.Result.Order, res.Order) ||
		v.Result.BigBlind != res.BigBlind || v.Result.RunoutFrom != res.RunoutFrom {
		fail("view result %+v differs", v.Result)
	}
	if len(v.Result.Actions) > 0 {
		v.Result.Actions[0].To = -1
		v.Result.Order[0] = "changed"
		if h.result.Actions[0].To == -1 || h.result.Order[0] == "changed" {
			fail("the view shares the action log with the table")
		}
	}
}

// countAction counts the rare decisions, so the test proves it reaches them.
func (f *fuzzer) countAction(a ActionRecord) {
	switch {
	case a.Street == Preflop && a.Raise && a.RaisesBefore == 1:
		f.seen.threeBets++
	case a.Street == Preflop && a.Raise && a.RaisesBefore >= 2:
		f.seen.fourBets++
	case a.Type == AllIn && !a.Raise && a.To < a.BetBefore:
		f.seen.shortAllIns++
	}
}
