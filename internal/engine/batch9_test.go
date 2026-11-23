package engine

import (
	"reflect"
	"slices"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// Batch 9: a player who folded can show one or both hole cards after the
// hand. Never during the hand.

var batch9Holes = map[int]string{0: "Jh Jd", 1: "Qh Qc", 2: "As Kd"}

// wantHidden fails when a viewer other than the owner sees a card of seat.
func wantHidden(t *testing.T, tb *Table, seat int, ids ...string) {
	t.Helper()
	for _, id := range append(ids, "") {
		if id == idOf(seat) {
			continue
		}
		v := tb.View(id)
		if s := v.Seats[seat]; s.Cards != nil {
			t.Fatalf("viewer %q sees the cards of %s: %+v", id, names[seat], s)
		}
		if r := v.Result; r != nil {
			for _, sh := range append(slices.Clone(r.Shown), r.Revealed...) {
				if sh.PlayerID == idOf(seat) {
					t.Fatalf("viewer %q finds %s in the result: %+v", id, names[seat], sh)
				}
			}
		}
	}
}

// TestShowFoldedAfterNoShowdown: two players fold, the third wins. Both the
// winner and a folded player show; each has one entry in Revealed, in the
// order of the first show, and later cards join that entry.
func TestShowFoldedAfterNoShowdown(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, batch9Holes, "")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold) // Alice folds first
	mustFail(t, tb.ShowCards("alice", true, true), ErrNotNow)
	wantHidden(t, tb, 0, "bob", "carol")
	foldToEnd(t, tb) // Bob folds, Carol wins
	res := tb.View("").Result
	if !res.NoShowdown || res.Winnings[0].PlayerID != "carol" {
		t.Fatalf("result %+v", res)
	}
	players := tb.View("").Result.Players
	tb.DrainEvents()
	wantHidden(t, tb, 0, "bob", "carol")
	wantHidden(t, tb, 1, "alice", "carol")
	mustFail(t, tb.ShowCards("bob", false, false), ErrNotNow)

	// Bob shows the left card.
	must(t, tb.ShowCards("bob", true, false))
	evs := tb.DrainEvents()
	if e := findEvent(t, evs, "Bob shows Q♥"); e.Kind != EvReveal || e.Seat != 1 || e.PlayerID != "bob" || e.Street != Waiting {
		t.Fatalf("reveal event %+v", e)
	}
	for _, id := range []string{"", "alice", "carol"} {
		if s := tb.View(id).Seats[1]; s.Cards != nil || !s.HasCards {
			t.Fatalf("viewer %q after one card of Bob: %+v", id, s)
		}
	}
	// Carol, the winner, shows the right card. Bob keeps the first entry.
	must(t, tb.ShowCards("carol", false, true))
	wantEvents(t, tb.DrainEvents(), "Carol shows K♦")
	// Bob adds the right card: his entry grows, the order stays.
	must(t, tb.ShowCards("bob", false, true))
	wantEvents(t, tb.DrainEvents(), "Bob shows Q♣")
	want := []Shown{
		{Seat: 1, PlayerID: "bob", Name: "Bob", Cards: cards.MustParseMany("Qh Qc")},
		{Seat: 2, PlayerID: "carol", Name: "Carol", Cards: cards.MustParseMany("Kd")},
	}
	if got := tb.View("").Result.Revealed; !reflect.DeepEqual(got, want) {
		t.Fatalf("Revealed = %+v, want %+v", got, want)
	}
	for _, id := range []string{"", "alice", "bob", "carol"} {
		if s := tb.View(id).Seats[1]; !slices.Equal(s.Cards, cards.MustParseMany("Qh Qc")) || s.HasCards {
			t.Fatalf("viewer %q after both cards of Bob: %+v", id, s)
		}
	}
	// The same cards again change nothing.
	must(t, tb.ShowCards("bob", true, true))
	if evs := tb.DrainEvents(); len(evs) != 0 {
		t.Fatalf("a repeated show made events: %v", eventTexts(evs))
	}
	// Alice never showed: her cards stay hidden.
	wantHidden(t, tb, 0, "bob", "carol")
	// A show is not an action: the stats facts and the stacks do not change.
	if got := tb.View("").Result; !reflect.DeepEqual(got.Players, players) || len(got.Shown) != 0 {
		t.Fatalf("a show changed the result: %+v", got)
	}
	wantStacks(t, tb, map[int]int64{0: 1000, 1: 995, 2: 1005})

	// The next deal clears every show.
	must(t, tb.StartHand())
	if v := tb.View(""); v.Result != nil || v.Seats[1].Cards != nil {
		t.Fatalf("the show stays after StartHand: %+v", v.Seats[1])
	}
	mustFail(t, tb.ShowCards("bob", true, true), ErrNotNow)
}

// TestShowFoldedAfterShowdown: a folded player can show after a hand with a
// showdown. The players who showed at showdown cannot show again, and a
// player who sat down during the hand cannot show.
func TestShowFoldedAfterShowdown(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, batch9Holes, "2c 7h 9s 3d 4c")
	tb := newTestTable(t, 5, 10, 4, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	must(t, tb.Sit(3, "dave", "Dave", 500)) // not dealt in
	checkDown(t, tb)
	res := tb.View("").Result
	if res.NoShowdown || len(res.Shown) != 2 {
		t.Fatalf("result %+v", res)
	}
	tb.DrainEvents()
	for _, id := range []string{"bob", "carol", "dave", "nobody"} {
		for _, lr := range [][2]bool{{true, true}, {true, false}, {false, true}} {
			mustFail(t, tb.ShowCards(id, lr[0], lr[1]), ErrNotNow)
		}
	}
	wantHidden(t, tb, 0, "bob", "carol", "dave")
	must(t, tb.ShowCards("alice", true, true))
	wantEvents(t, tb.DrainEvents(), "Alice shows J♥ J♦")
	want := []Shown{{Seat: 0, PlayerID: "alice", Name: "Alice", Cards: cards.MustParseMany("Jh Jd")}}
	if got := tb.View("").Result; !reflect.DeepEqual(got.Revealed, want) || len(got.Shown) != 2 {
		t.Fatalf("Revealed = %+v, Shown = %+v", got.Revealed, got.Shown)
	}
	for _, id := range []string{"", "bob", "carol", "dave"} {
		if s := tb.View(id).Seats[0]; !slices.Equal(s.Cards, cards.MustParseMany("Jh Jd")) {
			t.Fatalf("viewer %q: %+v", id, s)
		}
	}
	if evs := tb.DrainEvents(); len(evs) != 0 {
		t.Fatalf("extra events %v", eventTexts(evs))
	}
}

// TestShowFoldedGone: a folded player who stood up cannot show, also after
// a return to the same seat.
func TestShowFoldedGone(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, batch9Holes, "")
	tb := newTestTable(t, 5, 10, 3, []int64{1000, 1000, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	_, err := tb.Stand("alice") // during the hand
	must(t, err)
	foldToEnd(t, tb)
	mustFail(t, tb.ShowCards("alice", true, true), ErrNotNow)
	must(t, tb.Sit(0, "alice", "Alice", 1000))
	mustFail(t, tb.ShowCards("alice", true, true), ErrNotNow)
	// Bob folded and stood up after the hand.
	_, err = tb.Stand("bob")
	must(t, err)
	mustFail(t, tb.ShowCards("bob", true, false), ErrNotNow)
	must(t, tb.Sit(1, "bob", "Bob", 1000))
	mustFail(t, tb.ShowCards("bob", true, false), ErrNotNow)
	if r := tb.View("").Result.Revealed; len(r) != 0 {
		t.Fatalf("Revealed = %+v", r)
	}
	for _, id := range []string{"", "alice", "bob", "carol"} {
		v := tb.View(id)
		for _, s := range v.Seats[:2] {
			if s.Cards != nil || s.HasCards {
				t.Fatalf("viewer %q sees a card of a new seat: %+v", id, s)
			}
		}
	}
}

// TestShowFoldedRunout: during a paused run-out a folded player cannot
// show. After the river and the result, the folded player can; the tabled
// hands cannot show again.
func TestShowFoldedRunout(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, batch9Holes, "2c 7h 9s 3d 4c")
	tb := newRunoutTable(t, true, 5, 10, 3, []int64{1000, 100, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	act(t, tb, 1, AllIn)
	act(t, tb, 2, Call)
	if !tb.RunoutPending() {
		t.Fatal("no run-out")
	}
	tb.DrainEvents()
	for tb.RunoutPending() {
		for _, id := range []string{"alice", "bob", "carol"} {
			mustFail(t, tb.ShowCards(id, true, true), ErrNotNow)
		}
		if evs := tb.DrainEvents(); len(evs) != 0 {
			t.Fatalf("a show during the run-out made events: %v", eventTexts(evs))
		}
		wantHidden(t, tb, 0, "bob", "carol")
		must(t, tb.DealRunout())
		tb.DrainEvents()
	}
	if tb.InHand() || tb.View("").Result == nil {
		t.Fatal("the hand did not end")
	}
	wantHidden(t, tb, 0, "bob", "carol")
	for _, id := range []string{"bob", "carol"} {
		mustFail(t, tb.ShowCards(id, true, true), ErrNotNow)
	}
	must(t, tb.ShowCards("alice", false, true))
	wantEvents(t, tb.DrainEvents(), "Alice shows J♦")
	if r := tb.View("").Result.Revealed; len(r) != 1 || r[0].PlayerID != "alice" || !slices.Equal(r[0].Cards, cards.MustParseMany("Jd")) {
		t.Fatalf("Revealed = %+v", r)
	}
}

// TestShowFoldedAfterStandEndsRunout: a stand ends a paused run-out. The
// winner's entry is first (face up since the run-out start) and cannot
// change; a folded player's show goes after it.
func TestShowFoldedAfterStandEndsRunout(t *testing.T) {
	deck := stackDeck(t, 0, []int{0, 1, 2}, batch9Holes, "2c 7h 9s 3d 4c")
	tb := newRunoutTable(t, true, 5, 10, 3, []int64{1000, 100, 1000}, deck)
	must(t, tb.StartHand())
	act(t, tb, 0, Fold)
	act(t, tb, 1, AllIn)
	act(t, tb, 2, Call)
	_, err := tb.Stand("bob")
	must(t, err)
	res := tb.View("").Result
	if res == nil || !res.NoShowdown || len(res.Revealed) != 1 || res.Revealed[0].PlayerID != "carol" {
		t.Fatalf("result %+v", res)
	}
	mustFail(t, tb.ShowCards("carol", true, true), ErrNotNow)
	mustFail(t, tb.ShowCards("bob", true, true), ErrNotNow)
	must(t, tb.ShowCards("alice", true, false))
	got := tb.View("").Result.Revealed
	if len(got) != 2 || got[0].PlayerID != "carol" || len(got[0].Cards) != 2 || got[1].PlayerID != "alice" ||
		!slices.Equal(got[1].Cards, cards.MustParseMany("Jh")) {
		t.Fatalf("Revealed = %+v", got)
	}
}

// TestShowWinnerNoShowdownStillWorks: the old winner path, heads-up.
func TestShowWinnerNoShowdownStillWorks(t *testing.T) {
	tb := newTestTable(t, 5, 10, 2, []int64{1000, 1000})
	must(t, tb.StartHand())
	foldToEnd(t, tb)
	res := tb.View("").Result
	w := res.Winnings[0].PlayerID
	loser := "alice"
	if w == "alice" {
		loser = "bob"
	}
	must(t, tb.ShowCards(w, true, true))
	must(t, tb.ShowCards(loser, true, true)) // the folded player, since batch 9
	r := tb.View("").Result.Revealed
	if len(r) != 2 || r[0].PlayerID != w || r[1].PlayerID != loser || len(r[0].Cards) != 2 || len(r[1].Cards) != 2 {
		t.Fatalf("Revealed = %+v", r)
	}
}
