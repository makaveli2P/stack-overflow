package engine

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// Batch-6 tests: the paused run-out (Config.StepRunout).

// newRunoutTable is newTestTable with Config.StepRunout set to step.
func newRunoutTable(t *testing.T, step bool, sb, bb int64, seats int, stacks []int64, decks ...*cards.Deck) *Table {
	t.Helper()
	next := 0
	cfg := Config{Seats: seats, SmallBlind: sb, BigBlind: bb, StepRunout: step}
	tb, err := NewTable(cfg, func() *cards.Deck {
		if next < len(decks) {
			next++
			return decks[next-1]
		}
		return cards.NewDeckFrom(cards.FullDeck())
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range stacks {
		if s > 0 {
			must(t, tb.Sit(i, idOf(i), names[i], s))
		}
	}
	return tb
}

// tableChips returns the chips behind of every seated player plus the chips
// in the pots and the street bets.
func tableChips(tb *Table) int64 {
	v := tb.View("")
	var sum int64
	for _, s := range v.Seats {
		sum += s.Stack
	}
	if v.InHand {
		sum += v.TotalPot
	}
	return sum
}

// dealOut deals every street of a paused run-out and checks the board size
// and the EvStreet event after each deal. It drops the events before the
// first deal and returns the events of the deals.
func dealOut(t *testing.T, tb *Table) []Event {
	t.Helper()
	tb.DrainEvents()
	var evs []Event
	for range 3 {
		if !tb.RunoutPending() {
			break
		}
		before := len(tb.View("").Board)
		must(t, tb.DealRunout())
		got := tb.DrainEvents()
		evs = append(evs, got...)
		want := 3
		if before > 0 {
			want = before + 1
		}
		if b := tb.View("").Board; len(b) != want {
			t.Fatalf("board after DealRunout = %d cards, want %d", len(b), want)
		}
		if got[0].Kind != EvStreet {
			t.Fatalf("first event after DealRunout = %+v, want EvStreet", got[0])
		}
	}
	if tb.RunoutPending() || tb.InHand() {
		t.Fatal("the run-out did not end the hand")
	}
	return evs
}

// runoutCase is one hand that ends in a run-out (or not, for the river).
type runoutCase struct {
	name    string
	stacks  []int64
	holes   map[int]string
	board   string
	play    func(t *testing.T, tb *Table)
	from    int   // Result.RunoutFrom
	live    []int // live seats at the run-out, in showdown order
	noPause bool  // the last betting ends on the river
}

var runoutCases = []runoutCase{
	{
		name:   "preflop heads-up",
		stacks: []int64{1000, 300},
		holes:  map[int]string{0: "As Ad", 1: "Kc Kd"},
		board:  "2h 7s 9c Th Ks",
		play: func(t *testing.T, tb *Table) {
			act(t, tb, 0, AllIn) // the button posts the small blind and acts first
			act(t, tb, 1, Call)
		},
		from: 0,
		live: []int{1, 0},
	},
	{
		name:   "preflop three-way side pots",
		stacks: []int64{100, 300, 1000},
		holes:  map[int]string{0: "As Ad", 1: "Kc Kd", 2: "Qs Qh"},
		board:  "2h 7s 9c Th 3c",
		play: func(t *testing.T, tb *Table) {
			// Button Alice (0), SB Bob (1), BB Carol (2).
			act(t, tb, 0, AllIn)
			act(t, tb, 1, AllIn)
			act(t, tb, 2, Call)
		},
		from: 0,
		live: []int{1, 2, 0},
	},
	{
		name:   "flop",
		stacks: []int64{500, 500},
		holes:  map[int]string{0: "As Kd", 1: "Qc Qd"},
		board:  "2h 7s Kc 4d Ah",
		play: func(t *testing.T, tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			act(t, tb, 1, AllIn) // flop: the big blind acts first
			act(t, tb, 0, Call)
		},
		from: 3,
		live: []int{1, 0},
	},
	{
		name:   "turn",
		stacks: []int64{500, 800},
		holes:  map[int]string{0: "As Kd", 1: "Qc Qd"},
		board:  "2h 7s Kc 4d Qh",
		play: func(t *testing.T, tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			act(t, tb, 1, Check)
			act(t, tb, 0, Check)
			act(t, tb, 1, Bet, 100) // turn
			act(t, tb, 0, AllIn)
			act(t, tb, 1, Call)
		},
		from: 4,
		live: []int{1, 0},
	},
	{
		name:   "river",
		stacks: []int64{500, 500},
		holes:  map[int]string{0: "As Kd", 1: "Qc Qd"},
		board:  "2h 7s Kc 4d Qh",
		play: func(t *testing.T, tb *Table) {
			act(t, tb, 0, Call)
			act(t, tb, 1, Check)
			for range 2 { // flop and turn
				act(t, tb, 1, Check)
				act(t, tb, 0, Check)
			}
			act(t, tb, 1, AllIn) // river
			act(t, tb, 0, Call)
		},
		from:    5,
		noPause: true,
	},
}

func (c runoutCase) deck(t *testing.T) *cards.Deck {
	dealt := []int{}
	for i, s := range c.stacks {
		if s > 0 {
			dealt = append(dealt, i)
		}
	}
	return stackDeck(t, 0, dealt, c.holes, c.board)
}

func (c runoutCase) total() int64 {
	var sum int64
	for _, s := range c.stacks {
		sum += s
	}
	return sum
}

func TestRunoutSameResult(t *testing.T) {
	for _, c := range runoutCases {
		t.Run(c.name, func(t *testing.T) {
			// The old path: the whole board at once.
			old := newRunoutTable(t, false, 10, 20, len(c.stacks), c.stacks, c.deck(t))
			must(t, old.StartHand())
			c.play(t, old)
			oldEvents := old.DrainEvents()
			want := lastResult(t, old)

			tb := newRunoutTable(t, true, 10, 20, len(c.stacks), c.stacks, c.deck(t))
			must(t, tb.StartHand())
			c.play(t, tb)
			evs := tb.DrainEvents()
			if c.noPause {
				if tb.RunoutPending() || tb.InHand() {
					t.Fatal("an all-in on the river paused the hand")
				}
				mustFail(t, tb.DealRunout(), ErrNotNow)
			} else {
				if !tb.RunoutPending() || !tb.InHand() {
					t.Fatalf("pending = %v, in hand = %v; want a paused run-out", tb.RunoutPending(), tb.InHand())
				}
				wantToAct(t, tb, -1)
				v := tb.View("")
				if !v.Runout || len(v.Board) != c.from || v.ToAct != -1 || v.Legal != nil {
					t.Fatalf("view: runout %v, board %v, toAct %d, legal %v", v.Runout, v.Board, v.ToAct, v.Legal)
				}
				if got := tableChips(tb); got != c.total() {
					t.Fatalf("chips during the run-out = %d, want %d", got, c.total())
				}
				// One short EvShow per live player, in showdown order.
				var shows []string
				for _, e := range evs {
					if e.Kind == EvShow {
						shows = append(shows, e.Text)
					}
				}
				var wantShows []string
				for _, s := range c.live {
					hole := cards.MustParseMany(c.holes[s])
					wantShows = append(wantShows, names[s]+" shows "+cardsText(hole))
				}
				if !slices.Equal(shows, wantShows) {
					t.Fatalf("show lines = %q, want %q", shows, wantShows)
				}
				evs = append(evs, dealOut(t, tb)...)
			}
			got := lastResult(t, tb)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result with StepRunout:\n%+v\nwant\n%+v", got, want)
			}
			if got.RunoutFrom != c.from {
				t.Fatalf("RunoutFrom = %d, want %d", got.RunoutFrom, c.from)
			}
			if !slices.Equal(got.Board, cards.MustParseMany(c.board)) {
				t.Fatalf("board = %v, want %s", got.Board, c.board)
			}
			// Each live player shows once in the whole hand.
			count := map[string]int{}
			for _, e := range evs {
				if e.Kind == EvShow {
					count[e.PlayerID]++
				}
			}
			for _, s := range got.Shown {
				if count[s.PlayerID] != 1 {
					t.Fatalf("%s shows %d times, want 1\nevents:\n  %s", s.Name, count[s.PlayerID],
						strings.Join(eventTexts(evs), "\n  "))
				}
			}
			// Apart from the show lines, the log is the same.
			noShow := func(evs []Event) []string {
				var out []string
				for _, e := range evs {
					if e.Kind != EvShow {
						out = append(out, e.Text)
					}
				}
				return out
			}
			if a, b := noShow(evs), noShow(oldEvents); !slices.Equal(a, b) {
				t.Fatalf("log with StepRunout:\n  %s\nwant\n  %s", strings.Join(a, "\n  "), strings.Join(b, "\n  "))
			}
			wantSum := c.total()
			var sum int64
			for i, s := range c.stacks {
				if s > 0 {
					st, _ := tb.Stack(idOf(i))
					sum += st
				}
			}
			if sum != wantSum {
				t.Fatalf("chips after the hand = %d, want %d", sum, wantSum)
			}
		})
	}
}

func TestRunoutThreeWaySidePots(t *testing.T) {
	c := runoutCases[1]
	tb := newRunoutTable(t, true, 10, 20, 3, c.stacks, c.deck(t))
	must(t, tb.StartHand())
	c.play(t, tb)
	v := tb.View("")
	// The street bets are in the pots: 3 x 100 and 2 x 200.
	if len(v.Pots) != 2 || v.Pots[0].Amount != 300 || v.Pots[1].Amount != 400 || v.TotalPot != 700 {
		t.Fatalf("pots = %+v, total %d", v.Pots, v.TotalPot)
	}
	dealOut(t, tb)
	// Alice (AA) wins the main pot, Bob (KK) the side pot; Carol gets 700 back.
	wantStacks(t, tb, map[int]int64{0: 300, 1: 400, 2: 700})
}

func TestRunoutViewCards(t *testing.T) {
	// Button Alice (0), SB Bob (1), BB Carol (2). Alice folds; Bob and
	// Carol go all-in.
	tb := newRunoutTable(t, true, 10, 20, 4, []int64{1000, 500, 500})
	must(t, tb.StartHand())
	for _, s := range tb.View("").Seats {
		if s.Cards != nil {
			t.Fatalf("a spectator sees %s's cards before the run-out", s.Name)
		}
	}
	if tb.View("").Runout {
		t.Fatal("View.Runout before the run-out")
	}
	act(t, tb, 0, Fold)
	act(t, tb, 1, AllIn)
	act(t, tb, 2, Call)
	for _, viewer := range []string{"", idOf(0), idOf(1), "nobody"} {
		v := tb.View(viewer)
		if !v.Runout || v.Legal != nil {
			t.Fatalf("viewer %q: Runout %v, Legal %v", viewer, v.Runout, v.Legal)
		}
		for _, s := range []int{1, 2} {
			if len(v.Seats[s].Cards) != 2 || !v.Seats[s].HasCards {
				t.Fatalf("viewer %q does not see %s's cards", viewer, names[s])
			}
		}
		if viewer != idOf(0) && v.Seats[0].Cards != nil {
			t.Fatalf("viewer %q sees the folded cards", viewer)
		}
	}
	if len(tb.View(idOf(0)).Seats[0].Cards) != 2 {
		t.Fatal("the folded player does not see the own cards")
	}
	dealOut(t, tb)
	if v := tb.View(""); v.Runout || len(v.Seats[1].Cards) != 2 || len(v.Seats[2].Cards) != 2 {
		t.Fatalf("after the hand: Runout %v, cards %v %v", v.Runout, v.Seats[1].Cards, v.Seats[2].Cards)
	}
}

func TestRunoutRefusesActions(t *testing.T) {
	tb := newRunoutTable(t, true, 10, 20, 2, []int64{1000, 300})
	mustFail(t, tb.DealRunout(), ErrNotNow) // before the first hand
	must(t, tb.StartHand())
	mustFail(t, tb.DealRunout(), ErrNotNow) // during the betting
	act(t, tb, 0, AllIn)
	act(t, tb, 1, Call)
	tb.DrainEvents()
	for _, id := range []string{idOf(0), idOf(1)} {
		for _, a := range []Action{{Type: Fold}, {Type: Check}, {Type: Call}, {Type: AllIn}} {
			mustFail(t, tb.Act(id, a), ErrNotYourTurn)
		}
		if _, ok := tb.LegalFor(id); ok {
			t.Fatalf("LegalFor(%s) ok during the run-out", id)
		}
		mustFail(t, tb.Act(id, tb.DefaultAction(id)), ErrNotYourTurn)
		mustFail(t, tb.AddChips(id, 100), ErrHandRunning)
		mustFail(t, tb.ShowCards(id, true, true), ErrNotNow)
	}
	mustFail(t, tb.StartHand(), ErrHandRunning)
	mustFail(t, tb.RabbitHunt(), ErrNotNow)
	if tb.CanStartHand() {
		t.Fatal("CanStartHand during the run-out")
	}
	must(t, tb.SetSittingOut(idOf(1), true))
	must(t, tb.SetSittingOut(idOf(1), false))
	must(t, tb.Rename(idOf(1), "Robert"))
	if evs := tb.DrainEvents(); len(evs) != 3 {
		t.Fatalf("events = %q, want 3 seat lines", eventTexts(evs))
	}
	if !tb.RunoutPending() {
		t.Fatal("the run-out stopped")
	}
	dealOut(t, tb)
	mustFail(t, tb.DealRunout(), ErrNotNow) // after the hand
	if got := tableChips(tb); got != 1300 {
		t.Fatalf("chips = %d, want 1300", got)
	}
	must(t, tb.StartHand())
}

func TestRunoutStandHeadsUp(t *testing.T) {
	c := runoutCases[0]
	tb := newRunoutTable(t, true, 10, 20, 2, c.stacks, c.deck(t))
	must(t, tb.StartHand())
	c.play(t, tb)
	tb.DrainEvents()
	chips, err := tb.Stand(idOf(1))
	must(t, err)
	if chips != 0 {
		t.Fatalf("Bob stands with %d, want 0", chips)
	}
	if tb.InHand() || tb.RunoutPending() {
		t.Fatal("the hand did not end when only one live player was left")
	}
	mustFail(t, tb.DealRunout(), ErrNotNow)
	res := lastResult(t, tb)
	if !res.NoShowdown || res.RunoutFrom != 0 || len(res.Board) != 0 {
		t.Fatalf("result: NoShowdown %v, RunoutFrom %d, board %v", res.NoShowdown, res.RunoutFrom, res.Board)
	}
	wantStacks(t, tb, map[int]int64{0: 1300})
	var net int64
	for _, p := range res.Players {
		net += p.Won - p.Committed
	}
	if net != 0 {
		t.Fatalf("sum of Won - Committed = %d", net)
	}
	// Alice's cards were face up; they stay visible.
	if len(tb.View("").Seats[0].Cards) != 2 {
		t.Fatal("the tabled cards are hidden after the hand")
	}
	wantEvents(t, tb.DrainEvents(), "Bob folds", "Bob stands up with 0", "Alice wins 600 from the pot", "Hand #1 ends")
}

func TestRunoutStandThreeWay(t *testing.T) {
	for _, leaver := range []int{0, 2} { // an all-in player and the deep stack
		t.Run(names[leaver], func(t *testing.T) {
			c := runoutCases[1]
			tb := newRunoutTable(t, true, 10, 20, 3, c.stacks, c.deck(t))
			must(t, tb.StartHand())
			c.play(t, tb)
			must(t, tb.DealRunout()) // the flop
			tb.DrainEvents()
			chips, err := tb.Stand(idOf(leaver))
			must(t, err)
			if !tb.RunoutPending() || !tb.InHand() {
				t.Fatal("the run-out stopped with two live players")
			}
			v := tb.View("")
			if len(v.Board) != 3 || v.TotalPot != 700 {
				t.Fatalf("board %v, pot %d", v.Board, v.TotalPot)
			}
			for _, e := range tb.DrainEvents() {
				if e.Kind == EvShow || e.Kind == EvStreet || e.Kind == EvReturn {
					t.Fatalf("unexpected event after the stand: %q", e.Text)
				}
			}
			dealOut(t, tb)
			res := lastResult(t, tb)
			if res.RunoutFrom != 0 || res.NoShowdown || len(res.Shown) != 2 {
				t.Fatalf("result: RunoutFrom %d, NoShowdown %v, shown %d", res.RunoutFrom, res.NoShowdown, len(res.Shown))
			}
			var sum int64 = chips
			for i := range 3 {
				if st, ok := tb.Stack(idOf(i)); ok {
					sum += st
				}
			}
			if sum != c.total() {
				t.Fatalf("chips = %d, want %d", sum, c.total())
			}
			var net int64
			for _, p := range res.Players {
				net += p.Won - p.Committed
			}
			if net != 0 || len(res.Players) != 3 {
				t.Fatalf("players %d, sum of Won - Committed = %d", len(res.Players), net)
			}
		})
	}
}

func TestRunoutStandLastTwoInTurn(t *testing.T) {
	// Three-way: two players stand one after the other during the run-out.
	c := runoutCases[1]
	tb := newRunoutTable(t, true, 10, 20, 3, c.stacks, c.deck(t))
	must(t, tb.StartHand())
	c.play(t, tb)
	a, err := tb.Stand(idOf(2))
	must(t, err)
	b, err := tb.Stand(idOf(1))
	must(t, err)
	if tb.InHand() {
		t.Fatal("the hand did not end")
	}
	st, _ := tb.Stack(idOf(0))
	if a+b+st != c.total() || st != 700 {
		t.Fatalf("Alice %d, stood with %d and %d", st, a, b)
	}
	if res := lastResult(t, tb); !res.NoShowdown || res.RunoutFrom != 0 {
		t.Fatalf("result: NoShowdown %v, RunoutFrom %d", res.NoShowdown, res.RunoutFrom)
	}
}

// TestRunoutRandomLockstep plays random hands at two tables with the same
// decks and the same decisions, one with StepRunout and one without. The
// results and the stacks must be the same after every hand.
func TestRunoutRandomLockstep(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 6))
	pauses := 0
	for game := range 40 {
		seats := 2 + rng.IntN(5)
		stacks := make([]int64, seats)
		for i := range stacks {
			stacks[i] = 40 + rng.Int64N(400)
		}
		var decks []*cards.Deck
		var decks2 []*cards.Deck
		for range 30 {
			d := cards.FullDeck()
			rng.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
			decks = append(decks, cards.NewDeckFrom(slices.Clone(d)))
			decks2 = append(decks2, cards.NewDeckFrom(d))
		}
		old := newRunoutTable(t, false, 5, 10, seats, stacks, decks...)
		tb := newRunoutTable(t, true, 5, 10, seats, stacks, decks2...)
		for hand := 0; hand < 30 && old.CanStartHand(); hand++ {
			must(t, old.StartHand())
			must(t, tb.StartHand())
			for old.InHand() {
				id, ok := old.ToAct()
				if !ok {
					t.Fatalf("game %d: hand runs and nobody acts", game)
				}
				l, _ := old.LegalFor(id)
				a := randomAction(rng, l)
				must(t, old.Act(id, a))
				must(t, tb.Act(id, a))
			}
			if tb.RunoutPending() {
				pauses++
				for tb.RunoutPending() {
					must(t, tb.DealRunout())
				}
			}
			if tb.InHand() {
				t.Fatalf("game %d: the step table still runs a hand", game)
			}
			if !reflect.DeepEqual(old.View("").Result, tb.View("").Result) {
				t.Fatalf("game %d hand %d: results differ", game, hand)
			}
			for i := range seats {
				a, _ := old.Stack(idOf(i))
				b, _ := tb.Stack(idOf(i))
				if a != b {
					t.Fatalf("game %d hand %d: %s stack %d, want %d", game, hand, names[i], b, a)
				}
			}
		}
	}
	if pauses < 20 {
		t.Fatalf("only %d run-outs in the random hands", pauses)
	}
}

// randomAction picks a legal action, with many all-ins.
func randomAction(rng *rand.Rand, l Legal) Action {
	switch r := rng.IntN(10); {
	case r < 2 && l.Check:
		return Action{Type: Check}
	case r < 3:
		return Action{Type: Fold}
	case r < 6 && l.AllIn:
		return Action{Type: AllIn}
	case r < 8 && (l.Bet || l.Raise):
		typ := Raise
		if l.Bet {
			typ = Bet
		}
		return Action{Type: typ, Amount: l.MinTo + rng.Int64N(l.MaxTo-l.MinTo+1)}
	case l.Call:
		return Action{Type: Call}
	case l.Check:
		return Action{Type: Check}
	}
	return Action{Type: Fold}
}

// TestRunoutStandNoSecondShow: when a stand ends a paused run-out, the hand
// of the winner was face up since the start of the run-out. The result
// counts both cards as shown, and ShowCards cannot show them again.
func TestRunoutStandNoSecondShow(t *testing.T) {
	for _, deals := range []int{0, 1, 2} { // stand before the flop, the turn, the river
		c := runoutCases[0]
		tb := newRunoutTable(t, true, 10, 20, 2, c.stacks, c.deck(t))
		must(t, tb.StartHand())
		c.play(t, tb)
		for range deals {
			must(t, tb.DealRunout())
		}
		_, err := tb.Stand(idOf(1))
		must(t, err)
		tb.DrainEvents()
		res := lastResult(t, tb)
		if !res.NoShowdown {
			t.Fatalf("deals %d: NoShowdown false", deals)
		}
		want := []Shown{{Seat: 0, PlayerID: idOf(0), Name: names[0], Cards: cards.MustParseMany(c.holes[0])}}
		if !reflect.DeepEqual(res.Revealed, want) {
			t.Fatalf("deals %d: Revealed = %+v, want %+v", deals, res.Revealed, want)
		}
		for _, lr := range [][2]bool{{true, true}, {true, false}, {false, true}} {
			mustFail(t, tb.ShowCards(idOf(0), lr[0], lr[1]), ErrNotNow)
		}
		if evs := tb.DrainEvents(); len(evs) != 0 {
			t.Fatalf("deals %d: ShowCards emitted %+v", deals, evs)
		}
		if cs := tb.View("").Seats[0].Cards; len(cs) != 2 {
			t.Fatalf("deals %d: the tabled cards are hidden after the hand", deals)
		}
		// The rabbit hunt still works for the rest of the board.
		must(t, tb.RabbitHunt())
	}
}

// TestShowCardsWithoutRunoutUnchanged: a normal NoShowdown win (no run-out)
// leaves Revealed empty, and the winner can still show.
func TestShowCardsWithoutRunoutUnchanged(t *testing.T) {
	tb := newRunoutTable(t, true, 10, 20, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	res := lastResult(t, tb)
	if !res.NoShowdown || len(res.Revealed) != 0 {
		t.Fatalf("NoShowdown %v, Revealed %+v", res.NoShowdown, res.Revealed)
	}
	must(t, tb.ShowCards(idOf(1), true, true))
	if len(lastResult(t, tb).Revealed) != 1 {
		t.Fatal("ShowCards did not reveal the cards")
	}
}
