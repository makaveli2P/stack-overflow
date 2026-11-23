package engine

import (
	"fmt"
	"slices"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// findEvent returns the first event with this text.
func findEvent(t *testing.T, evs []Event, text string) Event {
	t.Helper()
	for _, e := range evs {
		if e.Text == text {
			return e
		}
	}
	t.Fatalf("no event %q in:\n  %v", text, eventTexts(evs))
	return Event{}
}

func summaryOf(t *testing.T, res *Result, seat int) PlayerSummary {
	t.Helper()
	for _, s := range res.Players {
		if s.Seat == seat {
			return s
		}
	}
	t.Fatalf("no summary for seat %d: %+v", seat, res.Players)
	return PlayerSummary{}
}

func wantNet(t *testing.T, res *Result) {
	t.Helper()
	var net int64
	for _, s := range res.Players {
		net += s.Won - s.Committed
	}
	if net != 0 {
		t.Fatalf("sum of Won - Committed = %d: %+v", net, res.Players)
	}
}

func TestFirstHand(t *testing.T) {
	for _, c := range []struct{ first, want int }{{0, 1}, {1, 1}, {-3, 1}, {42, 42}} {
		tb, err := NewTable(Config{Seats: 2, SmallBlind: 5, BigBlind: 10, FirstHand: c.first}, nil)
		must(t, err)
		if tb.HandNumber() != c.want-1 {
			t.Fatalf("FirstHand %d: HandNumber() = %d before the first hand, want %d", c.first, tb.HandNumber(), c.want-1)
		}
		must(t, tb.Sit(0, "alice", "Alice", 1000))
		must(t, tb.Sit(1, "bob", "Bob", 1000))
		tb.DrainEvents()
		must(t, tb.StartHand())
		if tb.HandNumber() != c.want || tb.View("").HandNumber != c.want {
			t.Fatalf("FirstHand %d: hand number %d, view %d, want %d", c.first, tb.HandNumber(), tb.View("").HandNumber, c.want)
		}
		foldToEnd(t, tb)
		evs := tb.DrainEvents()
		wantEvents(t, evs, fmt.Sprintf("Hand #%d — Alice has the button", c.want), fmt.Sprintf("Hand #%d ends", c.want))
		if r := tb.View("").Result; r.HandNumber != c.want || evs[0].HandNumber != c.want {
			t.Fatalf("FirstHand %d: result hand %d, event hand %d", c.first, r.HandNumber, evs[0].HandNumber)
		}
		must(t, tb.StartHand())
		if tb.HandNumber() != c.want+1 {
			t.Fatalf("FirstHand %d: second hand is %d", c.first, tb.HandNumber())
		}
	}
}

func TestEventStreetAndAction(t *testing.T) {
	tb := newTestTable(t, 5, 10, 4, []int64{1000, 1000, 1000})
	evs := tb.DrainEvents()
	if e := findEvent(t, evs, "Alice sits down with 1000"); e.Street != Waiting || e.Action != 0 {
		t.Fatalf("sit event %+v", e)
	}
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 30)
	act(t, tb, 1, Call)
	act(t, tb, 2, Fold)
	act(t, tb, 1, Check) // flop
	act(t, tb, 0, Bet, 50)
	act(t, tb, 1, Call)
	act(t, tb, 1, Check) // turn
	act(t, tb, 0, Check)
	must(t, tb.Sit(3, "dave", "Dave", 500)) // during the hand, after the river card
	act(t, tb, 1, AllIn)                    // river
	act(t, tb, 0, Call)
	wantToAct(t, tb, -1)
	evs = tb.DrainEvents()
	want := []struct {
		text   string
		kind   EventKind
		street Street
		action ActionType
	}{
		{"Hand #1 — Alice has the button", EvHandStart, Preflop, 0},
		{"Bob posts small blind 5", EvBlind, Preflop, 0},
		{"Carol posts big blind 10", EvBlind, Preflop, 0},
		{"Alice raises to 30", EvAction, Preflop, Raise},
		{"Bob calls 25", EvAction, Preflop, Call},
		{"Carol folds", EvAction, Preflop, Fold},
		{"Bob checks", EvAction, Flop, Check},
		{"Alice bets 50", EvAction, Flop, Bet},
		{"Bob calls 50", EvAction, Flop, Call},
		{"Dave sits down with 500", EvSeat, River, 0},
		{"Bob is all-in for 920", EvAction, River, AllIn},
		{"Alice is all-in for 920", EvAction, River, AllIn},
		{"Hand #1 ends", EvHandEnd, Showdown, 0},
	}
	for _, w := range want {
		e := findEvent(t, evs, w.text)
		if e.Kind != w.kind || e.Street != w.street || e.Action != w.action {
			t.Errorf("%q: kind %d street %v action %v, want %d %v %v", w.text, e.Kind, e.Street, e.Action, w.kind, w.street, w.action)
		}
	}
	for _, e := range evs {
		var bad bool
		switch e.Kind {
		case EvStreet:
			bad = e.Street < Flop || e.Street > River
		case EvShow, EvWin:
			bad = e.Street != Showdown
		}
		if bad || e.Street == Waiting {
			t.Errorf("event %q has street %v", e.Text, e.Street)
		}
	}

	// A fold win settles on the street where the last fold happened.
	must(t, tb.StartHand())
	checkUntil(t, tb, Flop)
	foldToEnd(t, tb)
	for _, e := range tb.DrainEvents() {
		if (e.Kind == EvWin || e.Kind == EvHandEnd) && e.Street != Flop {
			t.Errorf("fold win: %q on %v, want Flop", e.Text, e.Street)
		}
	}
}

// checkUntil checks or calls until the hand reaches street s.
func checkUntil(t *testing.T, tb *Table, s Street) {
	t.Helper()
	for tb.hand.street < s {
		id, ok := tb.ToAct()
		if !ok {
			t.Fatalf("the hand ended before %v", s)
		}
		l, _ := tb.LegalFor(id)
		a := Action{Type: Check}
		if !l.Check {
			a.Type = Call
		}
		must(t, tb.Act(id, a))
	}
}

func TestSummaryWalk(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	foldToEnd(t, tb) // Alice and Bob fold; Carol (big blind) wins
	res := tb.View("").Result
	if !res.NoShowdown || !res.CanRabbit || len(res.Players) != 3 {
		t.Fatalf("result %+v", res)
	}
	wantNet(t, res)
	want := []PlayerSummary{
		{PlayerID: "alice", Name: "Alice", Seat: 0},
		{PlayerID: "bob", Name: "Bob", Seat: 1, Committed: 5},
		{PlayerID: "carol", Name: "Carol", Seat: 2, Committed: 5, Won: 10},
	}
	if !slices.Equal(res.Players, want) {
		t.Fatalf("summaries\n got %+v\nwant %+v", res.Players, want)
	}
}

func TestSummaryPreflopFlags(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Call)  // Alice limps
	act(t, tb, 1, Call)  // Bob completes
	act(t, tb, 2, Check) // Carol checks the option
	act(t, tb, 1, Bet, 10)
	act(t, tb, 2, Raise, 30) // a raise after the flop is not PFR
	act(t, tb, 0, Fold)
	act(t, tb, 1, Fold)
	res := tb.View("").Result
	wantNet(t, res)
	want := map[int]PlayerSummary{
		0: {PlayerID: "alice", Name: "Alice", Seat: 0, Committed: 10, VPIP: true, SawFlop: true},
		1: {PlayerID: "bob", Name: "Bob", Seat: 1, Committed: 20, VPIP: true, SawFlop: true},
		2: {PlayerID: "carol", Name: "Carol", Seat: 2, Committed: 20, Won: 50, SawFlop: true},
	}
	for seat, w := range want {
		if got := summaryOf(t, res, seat); got != w {
			t.Errorf("seat %d\n got %+v\nwant %+v", seat, got, w)
		}
	}
	if !res.NoShowdown || !res.CanRabbit {
		t.Fatalf("NoShowdown %v CanRabbit %v", res.NoShowdown, res.CanRabbit)
	}
}

func TestSummaryShowdownAllIn(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1}, map[int]string{0: "As Ad", 1: "Kc Kd"}, "2c 7h 9s Jd 3c")
	tb := newTestTable(t, 5, 10, 2, []int64{100, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, AllIn) // the button acts first heads-up
	act(t, tb, 1, Call)
	res := tb.View("").Result
	wantNet(t, res)
	if res.NoShowdown || res.CanRabbit {
		t.Fatalf("NoShowdown %v CanRabbit %v", res.NoShowdown, res.CanRabbit)
	}
	alice, bob := summaryOf(t, res, 0), summaryOf(t, res, 1)
	aceValue, aceFive := cards.Best(cards.MustParseMany("As Ad 2c 7h 9s Jd 3c"))
	kingValue, _ := cards.Best(cards.MustParseMany("Kc Kd 2c 7h 9s Jd 3c"))
	if !alice.VPIP || !alice.PFR || !alice.SawFlop || !alice.WentToShowdown || !alice.WonAtShowdown || !alice.AllIn ||
		alice.Committed != 100 || alice.Won != 200 || alice.Hand == nil || alice.Hand.Compare(aceValue) != 0 || alice.HandCards != aceFive {
		t.Errorf("alice %+v", alice)
	}
	if !bob.VPIP || bob.PFR || !bob.SawFlop || !bob.WentToShowdown || bob.WonAtShowdown || bob.AllIn ||
		bob.Committed != 100 || bob.Won != 0 || bob.Hand == nil || bob.Hand.Compare(kingValue) != 0 {
		t.Errorf("bob %+v", bob)
	}
	mustFail(t, tb.RabbitHunt(), ErrNotNow)
	mustFail(t, tb.ShowCards("alice", true, true), ErrNotNow)
}

func TestSummaryShortBlindAllIn(t *testing.T) {
	// Bob cannot cover the small blind: AllIn is true without any action.
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 3, 1000})
	must(t, tb.StartHand())
	checkDown(t, tb)
	res := tb.View("").Result
	wantNet(t, res)
	if bob := summaryOf(t, res, 1); !bob.AllIn || bob.VPIP || bob.Committed != 3 {
		t.Fatalf("bob %+v", bob)
	}
}

func TestSummaryStoodUp(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	act(t, tb, 0, Raise, 30)
	act(t, tb, 1, Call)
	act(t, tb, 2, Fold)
	act(t, tb, 1, Bet, 40)
	chips, err := tb.Stand("alice") // Alice leaves while she must act
	must(t, err)
	if chips != 970 || tb.InHand() {
		t.Fatalf("stand gave %d, in hand %v", chips, tb.InHand())
	}
	res := tb.View("").Result
	wantNet(t, res)
	want := map[int]PlayerSummary{
		0: {PlayerID: "alice", Name: "Alice", Seat: 0, Committed: 30, VPIP: true, PFR: true, SawFlop: true},
		1: {PlayerID: "bob", Name: "Bob", Seat: 1, Committed: 30, Won: 70, VPIP: true, SawFlop: true},
		2: {PlayerID: "carol", Name: "Carol", Seat: 2, Committed: 10},
	}
	for seat, w := range want {
		if got := summaryOf(t, res, seat); got != w {
			t.Errorf("seat %d\n got %+v\nwant %+v", seat, got, w)
		}
	}
}

func TestRabbitPreflop(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, nil, "Ks 7h 2d 9c Ah")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, deck)
	mustFail(t, tb.RabbitHunt(), ErrNotNow) // no hand yet
	must(t, tb.StartHand())
	mustFail(t, tb.RabbitHunt(), ErrNotNow) // a hand runs
	foldToEnd(t, tb)
	tb.DrainEvents()
	before := map[int]int64{0: 1000, 1: 995, 2: 1005}
	wantStacks(t, tb, before)

	must(t, tb.RabbitHunt())
	v := tb.View("")
	if want := cards.MustParseMany("Ks 7h 2d 9c Ah"); !slices.Equal(v.Result.Rabbit, want) {
		t.Fatalf("rabbit %v, want %v", v.Result.Rabbit, want)
	}
	if len(v.Board) != 0 || len(v.Result.Board) != 0 || v.Result.CanRabbit {
		t.Fatalf("board %v, result board %v, CanRabbit %v", v.Board, v.Result.Board, v.Result.CanRabbit)
	}
	evs := tb.DrainEvents()
	e := findEvent(t, evs, "Rabbit hunt: [K♠ 7♥ 2♦] [9♣] [A♥]")
	if e.Kind != EvRabbit || e.Street != Waiting || e.Seat != -1 || e.HandNumber != 1 || len(evs) != 1 {
		t.Fatalf("rabbit event %+v (%d events)", e, len(evs))
	}
	wantStacks(t, tb, before)
	mustFail(t, tb.RabbitHunt(), ErrNotNow) // only once

	must(t, tb.StartHand())
	if tb.View("").Result != nil {
		t.Fatal("the result stays after StartHand")
	}
}

func TestRabbitLaterStreets(t *testing.T) {
	board := "Ks 7h 2d 9c Ah"
	tb := newTestTable(t, 5, 10, 2, []int64{1000, 1000},
		stackDeck(t, 0, []int{0, 1}, nil, board), // hand 1: fold on the flop
		stackDeck(t, 1, []int{0, 1}, nil, board), // hand 2: fold on the turn
		stackDeck(t, 0, []int{0, 1}, nil, board), // hand 3: fold on the river
	)
	for _, c := range []struct {
		street Street
		rabbit string
		text   string
	}{
		{Flop, "9c Ah", "Rabbit hunt: [9♣] [A♥]"},
		{Turn, "Ah", "Rabbit hunt: [A♥]"},
		{River, "", ""},
	} {
		must(t, tb.StartHand())
		checkUntil(t, tb, c.street)
		id, _ := tb.ToAct()
		must(t, tb.Act(id, Action{Type: Bet, Amount: 10}))
		foldToEnd(t, tb)
		tb.DrainEvents()
		res := tb.View("").Result
		if c.rabbit == "" {
			if res.CanRabbit || !res.NoShowdown {
				t.Fatalf("river fold: CanRabbit %v NoShowdown %v", res.CanRabbit, res.NoShowdown)
			}
			mustFail(t, tb.RabbitHunt(), ErrNotNow)
			continue
		}
		must(t, tb.RabbitHunt())
		got := tb.View("").Result
		if want := cards.MustParseMany(c.rabbit); !slices.Equal(got.Rabbit, want) || !slices.Equal(append(got.Board, got.Rabbit...), cards.MustParseMany(board)) {
			t.Fatalf("%v: board %v rabbit %v, want rabbit %v", c.street, got.Board, got.Rabbit, want)
		}
		wantEvents(t, tb.DrainEvents(), c.text)
	}
}

func TestShowCards(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, map[int]string{1: "Qh Qc", 2: "As Kd"}, "")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, deck)
	mustFail(t, tb.ShowCards("carol", true, true), ErrNotNow) // no hand yet
	must(t, tb.StartHand())
	mustFail(t, tb.ShowCards("carol", true, true), ErrNotNow) // a hand runs
	foldToEnd(t, tb)                                          // Carol wins
	tb.DrainEvents()
	// Bob folded, so since batch 9 he may show too; see batch9_test.go.
	mustFail(t, tb.ShowCards("nobody", true, true), ErrNotNow)
	mustFail(t, tb.ShowCards("carol", false, false), ErrNotNow)

	// Carol shows the right card only.
	must(t, tb.ShowCards("carol", false, true))
	v := tb.View("bob")
	if r := v.Result.Revealed; len(r) != 1 || r[0].Seat != 2 || r[0].PlayerID != "carol" || r[0].Name != "Carol" ||
		!slices.Equal(r[0].Cards, cards.MustParseMany("Kd")) {
		t.Fatalf("revealed %+v", r)
	}
	for _, id := range []string{"", "alice", "bob"} {
		if s := tb.View(id).Seats[2]; s.Cards != nil || !s.HasCards {
			t.Fatalf("viewer %q after one card: %+v", id, s)
		}
	}
	if s := tb.View("carol").Seats[2]; !slices.Equal(s.Cards, cards.MustParseMany("As Kd")) {
		t.Fatalf("Carol sees %v", s.Cards)
	}
	if s := tb.View("bob").Seats[1]; !slices.Equal(s.Cards, cards.MustParseMany("Qh Qc")) || tb.View("carol").Seats[1].Cards != nil {
		t.Fatal("the show changed another seat")
	}
	evs := tb.DrainEvents()
	if e := findEvent(t, evs, "Carol shows K♦"); e.Kind != EvReveal || e.Seat != 2 || e.PlayerID != "carol" || e.Street != Waiting {
		t.Fatalf("reveal event %+v", e)
	}

	// The same card again changes nothing.
	must(t, tb.ShowCards("carol", false, true))
	if evs := tb.DrainEvents(); len(evs) != 0 {
		t.Fatalf("a repeated show made events: %v", eventTexts(evs))
	}

	// Then the left card: both show, in deal order, to everyone.
	must(t, tb.ShowCards("carol", true, true))
	wantEvents(t, tb.DrainEvents(), "Carol shows A♠")
	both := cards.MustParseMany("As Kd")
	if r := tb.View("").Result.Revealed; len(r) != 1 || !slices.Equal(r[0].Cards, both) {
		t.Fatalf("revealed %+v", r)
	}
	for _, id := range []string{"", "alice", "bob", "carol"} {
		if s := tb.View(id).Seats[2]; !slices.Equal(s.Cards, both) || !s.HasCards {
			t.Fatalf("viewer %q after both cards: %+v", id, s)
		}
	}
	wantStacks(t, tb, map[int]int64{0: 1000, 1: 995, 2: 1005})

	must(t, tb.StartHand())
	if v := tb.View("bob"); v.Result != nil || v.Seats[2].Cards != nil {
		t.Fatalf("the show stays after StartHand: %+v", v.Seats[2])
	}
}

func TestShowCardsWinnerLeft(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	must(t, tb.StartHand())
	foldToEnd(t, tb)
	_, err := tb.Stand("carol")
	must(t, err)
	mustFail(t, tb.ShowCards("carol", true, false), ErrNotNow)
	must(t, tb.Sit(2, "carol", "Carol", 50)) // back, but in a new seat session
	mustFail(t, tb.ShowCards("carol", true, false), ErrNotNow)
}

func TestRename(t *testing.T) {
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000})
	tb.DrainEvents()
	must(t, tb.Rename("alice", "Ally"))
	evs := tb.DrainEvents()
	if len(evs) != 1 || evs[0].Text != "Alice is now Ally" || evs[0].Kind != EvSeat || evs[0].Seat != 0 || evs[0].Street != Waiting {
		t.Fatalf("rename events %+v", evs)
	}
	must(t, tb.Rename("alice", "Ally")) // no change, no event
	if evs := tb.DrainEvents(); len(evs) != 0 {
		t.Fatalf("a rename to the same name made events: %v", eventTexts(evs))
	}
	mustFail(t, tb.Rename("nobody", "X"), ErrNotSeated)
	mustFail(t, tb.Rename("bob", ""), ErrIllegal)

	must(t, tb.StartHand())
	must(t, tb.Rename("bob", "Robert")) // during the hand
	foldToEnd(t, tb)
	evs = tb.DrainEvents()
	if e := findEvent(t, evs, "Bob is now Robert"); e.Street != Preflop {
		t.Fatalf("rename during the hand: %+v", e)
	}
	wantEvents(t, evs, "Ally folds", "Robert folds", "Carol wins 10 from the pot")
	if v := tb.View(""); v.Seats[0].Name != "Ally" || v.Seats[1].Name != "Robert" || summaryOf(t, v.Result, 1).Name != "Robert" {
		t.Fatalf("names %q %q", v.Seats[0].Name, v.Seats[1].Name)
	}
}

func TestResultCopyBatch2(t *testing.T) {
	// A showdown result: Players[i].Hand must not share memory.
	tb := newTestTable(t, 5, 10, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	checkDown(t, tb)
	r := tb.View("").Result
	r.Players[0].Hand.Category = cards.StraightFlush
	r.Players[0].Name = "X"
	if r2 := tb.View("").Result; r2.Players[0].Hand.Category == cards.StraightFlush || r2.Players[0].Name == "X" {
		t.Fatal("Result.Players shares memory with the table")
	}

	// A fold win with a rabbit and a show.
	must(t, tb.StartHand())
	foldToEnd(t, tb)
	must(t, tb.RabbitHunt())
	winner := tb.View("").Result.Winnings[0].PlayerID
	must(t, tb.ShowCards(winner, true, false))
	r = tb.View("").Result
	r.Rabbit[0].Rank = 0
	r.Revealed[0].Cards[0].Rank = 0
	r2 := tb.View("").Result
	if r2.Rabbit[0].Rank == 0 || r2.Revealed[0].Cards[0].Rank == 0 {
		t.Fatal("Result.Rabbit or Result.Revealed shares memory with the table")
	}
}
