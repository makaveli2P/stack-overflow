package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// fuzzer plays random legal actions and checks the table invariants after
// every step.
type fuzzer struct {
	t      *testing.T
	rng    *rand.Rand
	tb     *Table
	total  int64 // chips that must be on the table: stacks + pots
	nextID int
	seen   *coverage

	// Batch-2 bookkeeping for the hand in progress or the last hand.
	deck       []cards.Card     // the card order of the last deck that the table took
	startStack map[string]int64 // stacks before StartHand, by player id
	standChips map[string]int64 // chips that Stand gave back during the hand
}

// coverage counts the rare situations, so the test can prove that the
// random play reaches them.
type coverage struct {
	showdowns, foldWins, sidePots, splits, runOuts int
	standsInHand, sitsInHand, incomplete, illegal  int
	walks, rabbits, reveals, bothReveals, renames  int
	badRabbits, badReveals                         int
	foldReveals                                    int // batch 9: shows by folded players

	// Batch 3: run-outs by RunoutFrom, preflop re-raises, and all-ins for
	// less than a call.
	runoutPre, runoutFlop, runoutTurn int
	threeBets, fourBets, shortAllIns  int
}

func TestRandomHands(t *testing.T) {
	hands := 5000
	if testing.Short() {
		hands = 500
	}
	rng := rand.New(rand.NewPCG(11, 12))
	seen := &coverage{}
	played := 0
	for played < hands {
		played += playSession(t, rng, seen, hands-played)
	}
	t.Logf("%d hands: %+v", played, *seen)
	if seen.showdowns == 0 || seen.foldWins == 0 || seen.sidePots == 0 || seen.splits == 0 ||
		seen.runOuts == 0 || seen.standsInHand == 0 || seen.sitsInHand == 0 ||
		seen.incomplete == 0 || seen.illegal == 0 || seen.walks == 0 || seen.rabbits == 0 ||
		seen.reveals == 0 || seen.bothReveals == 0 || seen.renames == 0 ||
		seen.badRabbits == 0 || seen.badReveals == 0 || seen.foldReveals == 0 ||
		seen.runoutPre == 0 || seen.runoutFlop == 0 || seen.runoutTurn == 0 ||
		seen.threeBets == 0 || seen.fourBets == 0 || seen.shortAllIns == 0 {
		t.Fatalf("the random hands missed a case: %+v", *seen)
	}
}

// playSession plays up to max hands at one random table and returns the
// number of hands played.
func playSession(t *testing.T, rng *rand.Rand, seen *coverage, max int) int {
	seats := 2 + rng.IntN(9)
	sb := 1 + rng.Int64N(25)
	bb := sb + rng.Int64N(2*sb+1)
	firstHand := rng.IntN(1000) // 0 and 1 both mean 1
	firstNum := firstHand       // the number of the first hand
	if firstNum < 1 {
		firstNum = 1
	}
	f := &fuzzer{t: t, rng: rng, seen: seen}
	tb, err := NewTable(Config{Seats: seats, SmallBlind: sb, BigBlind: bb, FirstHand: firstHand}, func() *cards.Deck {
		cs := cards.FullDeck()
		rng.Shuffle(len(cs), func(i, j int) { cs[i], cs[j] = cs[j], cs[i] })
		f.deck = slices.Clone(cs)
		return cards.NewDeckFrom(cs)
	})
	must(t, err)
	f.tb = tb
	if want := firstNum - 1; tb.HandNumber() != want {
		t.Fatalf("FirstHand %d: HandNumber() = %d before the first hand, want %d", firstHand, tb.HandNumber(), want)
	}
	for seat := range 2 + rng.IntN(seats-1) {
		f.sit(seat)
	}
	played := 0
	for ; played < max && played < 40; played++ {
		f.betweenHands()
		f.checkBetweenEvents(tb.DrainEvents())
		if !tb.CanStartHand() {
			break
		}
		before := tb.HandNumber()
		f.startStack = map[string]int64{}
		f.standChips = map[string]int64{}
		for _, p := range tb.seats {
			if p != nil {
				f.startStack[p.id] = p.stack
			}
		}
		must(t, tb.StartHand())
		if tb.HandNumber() != before+1 || tb.HandNumber() < 1 || (played == 0 && tb.HandNumber() != firstNum) {
			t.Fatalf("HandNumber() = %d after StartHand, before %d, FirstHand %d", tb.HandNumber(), before, firstHand)
		}
		f.check()
		f.playHand()
		f.checkResult()
	}
	return played
}

func (f *fuzzer) randomStack() int64 {
	bb := f.tb.cfg.BigBlind
	switch f.rng.IntN(4) {
	case 0:
		return 1 + f.rng.Int64N(2*bb) // short: less than or near the blinds
	default:
		return 1 + f.rng.Int64N(200*bb)
	}
}

func (f *fuzzer) sit(seat int) {
	f.nextID++
	id := fmt.Sprintf("p%d", f.nextID)
	stack := f.randomStack()
	must(f.t, f.tb.Sit(seat, id, "P"+id, stack))
	f.total += stack
}

func (f *fuzzer) emptySeat() int {
	var empty []int
	for i, p := range f.tb.seats {
		if p == nil {
			empty = append(empty, i)
		}
	}
	if len(empty) == 0 {
		return -1
	}
	return empty[f.rng.IntN(len(empty))]
}

func (f *fuzzer) randomPlayer() *player {
	var ps []*player
	for _, p := range f.tb.seats {
		if p != nil {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil
	}
	return ps[f.rng.IntN(len(ps))]
}

func (f *fuzzer) stand(p *player) {
	dealtIn := f.tb.running() != nil && f.tb.hand.players[p.seat] == p
	chips, err := f.tb.Stand(p.id)
	must(f.t, err)
	f.total -= chips
	if dealtIn {
		f.standChips[p.id] = chips
	}
}

// betweenHands rebuys busted players and changes seats at random. It also
// tries the rabbit hunt and the winner's show, before and after the seat
// changes.
func (f *fuzzer) betweenHands() {
	f.tryAfterHand()
	defer f.tryAfterHand()
	for _, p := range f.tb.seats {
		if p != nil && p.stack == 0 && f.rng.IntN(2) == 0 {
			amount := f.randomStack()
			must(f.t, f.tb.AddChips(p.id, amount))
			f.total += amount
		}
	}
	if p := f.randomPlayer(); p != nil && f.rng.IntN(8) == 0 {
		must(f.t, f.tb.SetSittingOut(p.id, !p.sittingOut))
	}
	if p := f.randomPlayer(); p != nil && f.rng.IntN(10) == 0 {
		f.stand(p)
	}
	for f.tb.eligibleCount() < 2 {
		s := f.emptySeat()
		if s < 0 {
			for _, p := range f.tb.seats {
				must(f.t, f.tb.SetSittingOut(p.id, false))
			}
			break
		}
		f.sit(s)
	}
	f.check()
}

func (f *fuzzer) playHand() {
	for steps := 0; f.tb.InHand(); steps++ {
		if steps > 1000 {
			f.t.Fatal("the hand did not end after 1000 steps")
		}
		switch r := f.rng.IntN(100); {
		case r < 2:
			if p := f.randomPlayer(); p != nil {
				f.stand(p)
				f.seen.standsInHand++
			}
		case r < 4:
			if s := f.emptySeat(); s >= 0 {
				f.sit(s)
				f.seen.sitsInHand++
			}
		case r < 10:
			f.tryIllegal()
			f.seen.illegal++
		case r < 11:
			f.tryAfterHand() // must fail: a hand runs
		case r < 12:
			f.renameRandom()
		default:
			f.actRandom()
		}
		f.check()
	}
}

func (f *fuzzer) actRandom() {
	h := f.tb.hand
	id, _ := f.tb.ToAct()
	before, minRaise := h.currentBet, h.minRaise
	must(f.t, f.tb.Act(id, f.randomAction(id)))
	if h.currentBet > before && h.currentBet-before < minRaise {
		f.seen.incomplete++
	}
}

func (f *fuzzer) randomAction(id string) Action {
	l, _ := f.tb.LegalFor(id)
	r := f.rng.IntN(100)
	switch {
	case r < 5:
		return f.tb.DefaultAction(id)
	case r < 20:
		return Action{Type: Fold}
	case r < 30 && l.AllIn:
		return Action{Type: AllIn}
	case r < 55 && (l.Bet || l.Raise):
		amount := l.MinTo
		if f.rng.IntN(2) == 0 {
			amount += f.rng.Int64N(l.MaxTo - l.MinTo + 1)
		}
		if l.Bet {
			return Action{Type: Bet, Amount: amount}
		}
		return Action{Type: Raise, Amount: amount}
	case l.Check:
		return Action{Type: Check}
	}
	return Action{Type: Call}
}

// tryIllegal sends an action that must fail, and checks that it changes
// nothing.
func (f *fuzzer) tryIllegal() {
	id, _ := f.tb.ToAct()
	l, _ := f.tb.LegalFor(id)
	var a Action
	want := ErrIllegal
	switch {
	case !l.Check:
		a = Action{Type: Check}
	case !l.Call:
		a = Action{Type: Call}
	case l.Bet:
		a = Action{Type: Bet, Amount: l.MaxTo + 1}
	default:
		a = Action{Type: Raise}
	}
	if f.rng.IntN(3) == 0 {
		if other := f.randomPlayer(); other != nil && other.id != id {
			id, a, want = other.id, Action{Type: Fold}, ErrNotYourTurn
		}
	}
	before := f.tb.View(id)
	nEvents := len(f.tb.events)
	if err := f.tb.Act(id, a); !errors.Is(err, want) {
		f.t.Fatalf("Act(%s, %+v) = %v, want %v (legal %+v)", id, a, err, want, l)
	}
	if after := f.tb.View(id); !reflect.DeepEqual(before, after) || len(f.tb.events) != nEvents {
		f.t.Fatalf("a failed Act changed the table")
	}
}

// check tests every invariant of the current state.
func (f *fuzzer) check() {
	f.t.Helper()
	tb := f.tb
	h := tb.running()
	var stacks, committed, behindBets int64
	for _, p := range tb.seats {
		if p != nil {
			if p.stack < 0 {
				f.t.Fatalf("negative stack: %+v", p)
			}
			stacks += p.stack
		}
	}
	if h != nil {
		for _, p := range h.players {
			if p == nil {
				continue
			}
			if p.bet < 0 || p.bet > p.committed {
				f.t.Fatalf("bad bet: %+v", p)
			}
			committed += p.committed
			if tb.seats[p.seat] == p {
				behindBets += p.bet
			}
		}
	}
	if stacks+committed != f.total {
		f.t.Fatalf("chips: stacks %d + committed %d != %d", stacks, committed, f.total)
	}
	v := tb.View("")
	var pots, seatBets int64
	for _, pot := range v.Pots {
		pots += pot.Amount
		if len(pot.Eligible) == 0 {
			f.t.Fatalf("pot with nobody eligible: %+v", v.Pots)
		}
		for _, s := range pot.Eligible {
			if h.players[s] == nil || h.players[s].folded {
				f.t.Fatalf("seat %d is eligible but not live", s)
			}
		}
	}
	for _, s := range v.Seats {
		seatBets += s.Bet
	}
	if seatBets != behindBets || pots+seatBets != committed || v.TotalPot != committed {
		f.t.Fatalf("pots %d + bets %d, total %d, committed %d", pots, seatBets, v.TotalPot, committed)
	}
	f.checkTurn(h)
	f.checkVisibility(h != nil)
}

func (f *fuzzer) checkTurn(h *hand) {
	tb := f.tb
	id, ok := tb.ToAct()
	if h == nil {
		if ok {
			f.t.Fatalf("ToAct = %q with no hand", id)
		}
		return
	}
	if !ok {
		f.t.Fatal("a running hand has nobody to act")
	}
	for _, p := range tb.seats {
		if p == nil {
			continue
		}
		l, ok := tb.LegalFor(p.id)
		v := tb.View(p.id)
		if (p.id == id) != ok || (p.id == id) != (v.Legal != nil) {
			f.t.Fatalf("LegalFor(%s) = %v, View.Legal %v, ToAct %s", p.id, ok, v.Legal, id)
		}
		if ok {
			f.checkLegal(h, p, l)
		}
	}
}

func (f *fuzzer) checkLegal(h *hand, p *player, l Legal) {
	bad := !l.Fold || l.Check == l.Call || l.CallAmount > p.stack || p.stack == 0 ||
		l.AllInTo != p.bet+p.stack || (l.Bet && l.Raise)
	if l.Bet || l.Raise {
		bad = bad || l.MinTo > l.MaxTo || l.MaxTo != l.AllInTo || l.MinTo <= h.currentBet || !l.AllIn
	} else {
		bad = bad || l.MinTo != 0 || l.MaxTo != 0
	}
	if l.AllIn && !l.Bet && !l.Raise {
		bad = bad || p.stack > l.CallAmount
	}
	if bad {
		f.t.Fatalf("bad Legal for %+v: %+v (bet %d)", p, l, h.currentBet)
	}
}

// checkVisibility makes sure no viewer sees hole cards that are not theirs,
// except hands shown at showdown after the hand.
func (f *fuzzer) checkVisibility(running bool) {
	viewers := []string{""}
	for _, p := range f.tb.seats {
		if p != nil {
			viewers = append(viewers, p.id)
		}
	}
	for _, id := range viewers {
		v := f.tb.View(id)
		shown := map[int]bool{}
		if v.Result != nil && !running {
			for _, s := range v.Result.Shown {
				shown[s.Seat] = true
			}
			for _, s := range v.Result.Revealed {
				shown[s.Seat] = len(s.Cards) == 2
			}
		}
		for i, s := range v.Seats {
			if s.Cards == nil {
				continue
			}
			if len(s.Cards) != 2 || (i != v.MySeat && !shown[i]) {
				f.t.Fatalf("viewer %q sees seat %d cards %v (running %v)", id, i, s.Cards, running)
			}
		}
	}
}

// checkResult tests the payouts of the hand that just ended.
func (f *fuzzer) checkResult() {
	h := f.tb.hand
	res := h.result
	if res == nil || h.running {
		f.t.Fatal("no result after the hand")
	}
	won := map[int]int64{}
	var paid, committed int64
	for _, w := range res.Winnings {
		won[w.Seat] += w.Amount
		paid += w.Amount
		if w.Amount <= 0 || h.players[w.Seat] == nil || h.players[w.Seat].folded {
			f.t.Fatalf("bad winning %+v", w)
		}
	}
	for _, p := range h.players {
		if p != nil {
			committed += p.committed
		}
	}
	if paid != committed {
		f.t.Fatalf("paid %d, committed %d", paid, committed)
	}
	// A player can win at most what each player put in, up to the
	// player's own commitment. Dead chips above every live commitment are
	// the only exception, and only the top live player can get them.
	for seat, amount := range won {
		me := h.players[seat].committed
		var limit, top int64
		for _, p := range h.players {
			if p == nil {
				continue
			}
			limit += min(p.committed, me)
			if p.live() {
				top = max(top, p.committed)
			}
		}
		if amount > limit && me != top {
			f.t.Fatalf("seat %d won %d, more than its limit %d", seat, amount, limit)
		}
	}
	if len(res.Shown) > 0 {
		f.checkShowdownWinner(res)
	}
	evs := f.tb.DrainEvents()
	f.checkSummaries(h, res, evs)
	f.checkActions(h, res, evs)
	f.count(h, res)
}

func (f *fuzzer) count(h *hand, res *Result) {
	if len(res.Shown) == 0 {
		f.seen.foldWins++
		return
	}
	f.seen.showdowns++
	if h.count((*player).canBet) <= 1 {
		f.seen.runOuts++
	}
	perPot := map[int]int{}
	for _, w := range res.Winnings {
		perPot[w.PotIndex]++
		if w.PotIndex > 0 {
			f.seen.sidePots++
		}
	}
	for _, n := range perPot {
		if n > 1 {
			f.seen.splits++
		}
	}
}

// checkShowdownWinner checks that the best shown hand wins chips.
func (f *fuzzer) checkShowdownWinner(res *Result) {
	if len(res.Board) != 5 {
		f.t.Fatalf("showdown with board %v", res.Board)
	}
	var best cards.HandValue
	for i, s := range res.Shown {
		v, _ := cards.Best(append(append([]cards.Card{}, s.Cards...), res.Board...))
		if i == 0 || v.Compare(best) > 0 {
			best = v
		}
	}
	for _, s := range res.Shown {
		v, _ := cards.Best(append(append([]cards.Card{}, s.Cards...), res.Board...))
		if v.Compare(best) != 0 {
			continue
		}
		found := false
		for _, w := range res.Winnings {
			found = found || w.Seat == s.Seat
		}
		if !found {
			f.t.Fatalf("best hand %v at seat %d won nothing: %+v", v, s.Seat, res.Winnings)
		}
	}
}
