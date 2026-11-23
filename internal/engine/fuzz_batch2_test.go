package engine

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// The batch-2 part of the random test: player summaries, event streets and
// action types, rabbit hunts, winner shows and renames.

// checkSummaries tests Result.Players and the event log of the hand that
// just ended. evs are the events of that hand.
func (f *fuzzer) checkSummaries(h *hand, res *Result, evs []Event) {
	t := f.t
	t.Helper()
	// Find the expected facts from the event log alone.
	vpip, pfr, allIn, foldedPre := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	cur := f.tb.cfg.BigBlind // the current bet preflop after the blinds
	for _, e := range evs {
		f.checkHandEvent(e, res)
		if (e.Kind == EvBlind || e.Kind == EvAction) && strings.Contains(e.Text, "all-in") {
			allIn[e.PlayerID] = true
		}
		if e.Kind != EvAction || e.Street != Preflop {
			continue
		}
		switch e.Action {
		case Fold:
			foldedPre[e.PlayerID] = true
		case Call:
			vpip[e.PlayerID] = true
		case Bet, Raise:
			vpip[e.PlayerID], pfr[e.PlayerID] = true, true
			cur = max(cur, e.Amount)
		case AllIn: // Amount is the street total
			vpip[e.PlayerID] = true
			if e.Amount > cur {
				pfr[e.PlayerID] = true
			}
			cur = max(cur, e.Amount)
		}
	}

	if res.NoShowdown != (len(res.Shown) == 0) {
		t.Fatalf("NoShowdown %v with %d shown hands", res.NoShowdown, len(res.Shown))
	}
	if res.CanRabbit != (res.NoShowdown && len(res.Board) < 5) || len(res.Rabbit) != 0 || len(res.Revealed) != 0 {
		t.Fatalf("new result: CanRabbit %v, board %v, rabbit %v, revealed %v", res.CanRabbit, res.Board, res.Rabbit, res.Revealed)
	}
	won := map[int]int64{}
	for _, w := range res.Winnings {
		won[w.Seat] += w.Amount
	}
	shown := map[int]bool{}
	for _, s := range res.Shown {
		shown[s.Seat] = true
	}
	var net int64
	got := map[int]bool{}
	anyVPIP := false
	for _, s := range res.Players {
		p := h.players[s.Seat]
		if p == nil || p.id != s.PlayerID || p.name != s.Name || got[s.Seat] {
			t.Fatalf("summary %+v does not match a dealt player", s)
		}
		got[s.Seat] = true
		net += s.Won - s.Committed
		anyVPIP = anyVPIP || s.VPIP
		if s.Committed != p.committed || s.Won != won[s.Seat] {
			t.Fatalf("summary %+v: committed %d, won %d", s, p.committed, won[s.Seat])
		}
		// The chips must move as the summary says.
		end, ok := f.standChips[p.id]
		if f.tb.seats[p.seat] == p {
			end, ok = p.stack, true
		}
		if !ok || f.startStack[p.id]-s.Committed+s.Won != end {
			t.Fatalf("summary %+v: start %d, end %d (%v)", s, f.startStack[p.id], end, ok)
		}
		wantSaw := len(res.Board) >= 3 && !foldedPre[p.id]
		bad := s.VPIP != vpip[p.id] || s.PFR != pfr[p.id] || (s.PFR && !s.VPIP) ||
			s.SawFlop != wantSaw || s.AllIn != allIn[p.id] ||
			s.WentToShowdown != shown[s.Seat] ||
			s.WonAtShowdown != (s.WentToShowdown && s.Won > 0) ||
			(s.WonAtShowdown && !s.WentToShowdown) ||
			(s.Hand != nil) != s.WentToShowdown
		if bad {
			t.Fatalf("summary %+v\nwant vpip %v pfr %v saw %v allin %v shown %v\nevents:\n  %s",
				s, vpip[p.id], pfr[p.id], wantSaw, allIn[p.id], shown[s.Seat], strings.Join(eventTexts(evs), "\n  "))
		}
		if s.Hand != nil {
			v, five := cards.Best(append(slices.Clone(p.hole), res.Board...))
			if v.Compare(*s.Hand) != 0 || five != s.HandCards {
				t.Fatalf("summary hand %v %v, want %v %v", *s.Hand, s.HandCards, v, five)
			}
		}
	}
	if len(got) != h.count(dealt) {
		t.Fatalf("%d summaries for %d dealt players", len(got), h.count(dealt))
	}
	if net != 0 {
		t.Fatalf("summaries: sum of Won - Committed = %d", net)
	}
	// A walk: the big blind wins and nobody put in chips by choice.
	if res.NoShowdown && len(res.Winnings) > 0 && res.Winnings[0].Seat == h.bb && len(vpip) == 0 {
		if anyVPIP {
			t.Fatalf("walk with VPIP: %+v", res.Players)
		}
		f.seen.walks++
	}
}

// actionWords maps each action type to the words of its log line.
var actionWords = map[ActionType]string{
	Fold: " folds", Check: " checks", Call: " calls ", Bet: " bets ", Raise: " raises to ", AllIn: " is all-in for ",
}

// checkHandEvent tests the Street and Action fields of one event of a hand.
func (f *fuzzer) checkHandEvent(e Event, res *Result) {
	t := f.t
	t.Helper()
	inBetting := e.Street >= Preflop && e.Street <= River
	bad := e.HandNumber != res.HandNumber || e.Street == Waiting || (e.Kind != EvAction && e.Action != 0)
	switch e.Kind {
	case EvHandStart, EvBlind:
		bad = bad || e.Street != Preflop
	case EvStreet:
		bad = bad || e.Street < Flop || e.Street > River || !strings.HasPrefix(e.Text, e.Street.String()+": ")
	case EvAction:
		bad = bad || !inBetting || !strings.Contains(e.Text, actionWords[e.Action])
	case EvShow:
		bad = bad || e.Street != Showdown
	case EvWin, EvBust, EvHandEnd:
		bad = bad || (res.NoShowdown && !inBetting) || (!res.NoShowdown && e.Street != Showdown)
	case EvReturn, EvSeat:
		bad = bad || !inBetting
	default:
		bad = true
	}
	if bad {
		t.Fatalf("bad hand event %+v (no showdown %v)", e, res.NoShowdown)
	}
}

// checkBetweenEvents tests the events made between two hands.
func (f *fuzzer) checkBetweenEvents(evs []Event) {
	for _, e := range evs {
		ok := e.Kind == EvSeat || e.Kind == EvRabbit || e.Kind == EvReveal
		if !ok || e.Street != Waiting || e.Action != 0 || e.HandNumber != f.tb.HandNumber() {
			f.t.Fatalf("bad event between hands: %+v", e)
		}
	}
}

// tryAfterHand tries a rabbit hunt or a show at random. Both must fail
// while a hand runs.
func (f *fuzzer) tryAfterHand() {
	switch f.rng.IntN(4) {
	case 0:
		f.tryRabbit()
	case 1, 2:
		f.tryShow()
	}
}

// lastResult returns the result of the last hand, or nil while a hand
// runs or before the first hand.
func (f *fuzzer) lastResult() *Result {
	if h := f.tb.hand; h != nil && !h.running {
		return h.result
	}
	return nil
}

func (f *fuzzer) stackSum() int64 {
	var sum int64
	for _, p := range f.tb.seats {
		if p != nil {
			sum += p.stack
		}
	}
	return sum
}

// allViews returns the view of the spectator and of each seated player.
func (f *fuzzer) allViews() []View {
	vs := []View{f.tb.View("")}
	for _, p := range f.tb.seats {
		if p != nil {
			vs = append(vs, f.tb.View(p.id))
		}
	}
	return vs
}

// wantNotNow runs call, which must fail with ErrNotNow and change nothing.
func (f *fuzzer) wantNotNow(what string, call func() error) {
	f.t.Helper()
	before := f.allViews()
	n := len(f.tb.events)
	if err := call(); !errors.Is(err, ErrNotNow) {
		f.t.Fatalf("%s = %v, want ErrNotNow", what, err)
	}
	if !reflect.DeepEqual(before, f.allViews()) || len(f.tb.events) != n {
		f.t.Fatalf("a failed %s changed the table", what)
	}
}

func (f *fuzzer) tryRabbit() {
	tb := f.tb
	res := f.lastResult()
	if res == nil || !res.CanRabbit {
		f.wantNotNow("RabbitHunt", tb.RabbitHunt)
		f.seen.badRabbits++
		return
	}
	h := tb.hand
	stacks := f.stackSum()
	board := slices.Clone(res.Board)
	must(f.t, tb.RabbitHunt())
	// The real deal: two hole cards each, then burn + 3, burn + 1, burn + 1.
	n, d := h.count(dealt), f.deck
	full := []cards.Card{d[2*n+1], d[2*n+2], d[2*n+3], d[2*n+5], d[2*n+7]}
	v := tb.View("")
	got := append(slices.Clone(v.Result.Board), v.Result.Rabbit...)
	if !slices.Equal(got, full) || !slices.Equal(v.Board, board) || !slices.Equal(v.Result.Board, board) ||
		v.Result.CanRabbit || f.stackSum() != stacks {
		f.t.Fatalf("rabbit: board %v + rabbit %v, want %v; CanRabbit %v", v.Result.Board, v.Result.Rabbit, full, v.Result.CanRabbit)
	}
	if e := tb.events[len(tb.events)-1]; e.Kind != EvRabbit || e.Street != Waiting || !strings.HasPrefix(e.Text, "Rabbit hunt: [") {
		f.t.Fatalf("rabbit event %+v", e)
	}
	f.wantNotNow("second RabbitHunt", tb.RabbitHunt)
	f.seen.rabbits++
	f.check()
}

// mayShow reports whether p may show cards of the last hand: p is the
// player who was dealt in at that seat, the hand was not shown at showdown
// or at the start of a run-out, and p is the NoShowdown winner or folded
// (a Fold in Result.Actions).
func (f *fuzzer) mayShow(res *Result, p *player) bool {
	h := f.tb.hand
	if res == nil || h == nil || h.players[p.seat] != p || h.tabled[p.seat] {
		return false
	}
	for _, s := range res.Shown {
		if s.PlayerID == p.id {
			return false
		}
	}
	if res.NoShowdown && len(res.Winnings) > 0 && res.Winnings[0].PlayerID == p.id && res.Winnings[0].Seat == p.seat {
		return true
	}
	return slices.ContainsFunc(res.Actions, func(a ActionRecord) bool { return a.PlayerID == p.id && a.Type == Fold })
}

func (f *fuzzer) tryShow() {
	tb := f.tb
	res := f.lastResult()
	var can []*player
	for _, p := range tb.seats {
		if p != nil && f.mayShow(res, p) {
			can = append(can, p)
		}
	}
	p := f.randomPlayer()
	if len(can) > 0 && f.rng.IntN(3) > 0 {
		p = can[f.rng.IntN(len(can))]
	}
	if p == nil {
		return
	}
	left, right := f.rng.IntN(2) == 0, f.rng.IntN(2) == 0
	if !slices.Contains(can, p) || (!left && !right) {
		f.wantNotNow(fmt.Sprintf("ShowCards(%s, %v, %v)", p.id, left, right), func() error {
			return tb.ShowCards(p.id, left, right)
		})
		f.seen.badReveals++
		return
	}
	// The cards to show after this call: the cards shown before, and the new ones.
	before := slices.Clone(res.Revealed)
	at := slices.IndexFunc(before, func(s Shown) bool { return s.PlayerID == p.id })
	want := [2]bool{left, right}
	if at >= 0 {
		for _, c := range before[at].Cards {
			want[slices.Index(p.hole, c)] = true
		}
	}
	var wantCards []cards.Card
	for i := range 2 {
		if want[i] {
			wantCards = append(wantCards, p.hole[i])
		}
	}
	added := len(wantCards)
	if at >= 0 {
		added -= len(before[at].Cards)
	}
	stacks, n := f.stackSum(), len(tb.events)
	must(f.t, tb.ShowCards(p.id, left, right))
	v := tb.View("")
	r := v.Result.Revealed
	// One entry per player, in the order of the first show. The other
	// entries do not change.
	wantLen, idx := len(before), at
	if at < 0 {
		wantLen, idx = len(before)+1, len(before)
	}
	if len(r) != wantLen || r[idx].Seat != p.seat || r[idx].PlayerID != p.id || !slices.Equal(r[idx].Cards, wantCards) || f.stackSum() != stacks {
		f.t.Fatalf("revealed %+v, want %v from seat %d at %d", r, wantCards, p.seat, idx)
	}
	for i, s := range before {
		if i != idx && (r[i].PlayerID != s.PlayerID || !slices.Equal(r[i].Cards, s.Cards)) {
			f.t.Fatalf("the show of %s changed entry %d: %+v, was %+v", p.id, i, r[i], s)
		}
	}
	switch {
	case added == 0 && len(tb.events) != n:
		f.t.Fatal("a show of cards already shown made an event")
	case added > 0 && (len(tb.events) != n+1 || tb.events[n].Kind != EvReveal || !strings.HasPrefix(tb.events[n].Text, p.name+" shows ")):
		f.t.Fatalf("show events: %v", eventTexts(tb.events[n:]))
	}
	both := want[0] && want[1]
	for _, view := range f.allViews() {
		sv := view.Seats[p.seat]
		mine := view.MySeat == p.seat
		if (mine || both) && !slices.Equal(sv.Cards, p.hole) || !mine && !both && (sv.Cards != nil || !sv.HasCards) {
			f.t.Fatalf("seat view %+v after show %v (viewer seat %d)", sv, wantCards, view.MySeat)
		}
	}
	f.seen.reveals++
	if both {
		f.seen.bothReveals++
	}
	if p.folded {
		f.seen.foldReveals++
	}
	f.check()
}

func countCards(ss []Shown) int {
	n := 0
	for _, s := range ss {
		n += len(s.Cards)
	}
	return n
}

// renameRandom renames a random seated player, and tries to rename a
// player who is not seated.
func (f *fuzzer) renameRandom() {
	mustFail(f.t, f.tb.Rename("nobody", "X"), ErrNotSeated)
	p := f.randomPlayer()
	if p == nil {
		return
	}
	old := p.name
	name := fmt.Sprintf("R%s.%d", p.id, f.seen.renames)
	must(f.t, f.tb.Rename(p.id, name))
	if e := f.tb.events[len(f.tb.events)-1]; e.Kind != EvSeat || e.Text != old+" is now "+name || p.name != name {
		f.t.Fatalf("rename event %+v", e)
	}
	f.seen.renames++
}
