package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

var names = []string{"Alice", "Bob", "Carol", "Dave", "Erin", "Frank", "Grace", "Heidi", "Ivan", "Judy"}

func idOf(seat int) string { return strings.ToLower(names[seat]) }

// newTestTable seats one player per stack, at seats 0, 1, 2, ... A stack of
// 0 leaves the seat empty. Every hand uses the next deck from decks; when
// the list is empty, it uses a fresh unshuffled deck.
func newTestTable(t *testing.T, sb, bb int64, seats int, stacks []int64, decks ...*cards.Deck) *Table {
	t.Helper()
	next := 0
	tb, err := NewTable(Config{Seats: seats, SmallBlind: sb, BigBlind: bb}, func() *cards.Deck {
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustFail(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// stackDeck builds a deck for one hand. button is the button seat and
// dealt lists the dealt seats. holes maps a seat to its two hole cards
// ("As Kd"); board lists up to five board cards. The deal order is the
// engine's: one card at a time clockwise from the seat left of the button,
// two rounds, then burn + 3, burn + 1, burn + 1. Free slots get unused cards.
func stackDeck(t *testing.T, button int, dealt []int, holes map[int]string, board string) *cards.Deck {
	t.Helper()
	used := map[cards.Card]bool{}
	hole := map[int][]cards.Card{}
	for s, h := range holes {
		hole[s] = cards.MustParseMany(h)
		for _, c := range hole[s] {
			used[c] = true
		}
	}
	boardCards := cards.MustParseMany(board)
	for _, c := range boardCards {
		used[c] = true
	}
	var free []cards.Card
	for _, c := range cards.FullDeck() {
		if !used[c] {
			free = append(free, c)
		}
	}
	take := func() cards.Card {
		c := free[0]
		free = free[1:]
		return c
	}
	order := slices.Clone(dealt)
	slices.Sort(order)
	if k := slices.IndexFunc(order, func(s int) bool { return s > button }); k > 0 {
		order = append(order[k:], order[:k]...)
	}
	var deck []cards.Card
	for round := range 2 {
		for _, s := range order {
			if h, ok := hole[s]; ok {
				deck = append(deck, h[round])
			} else {
				deck = append(deck, take())
			}
		}
	}
	for i := range 5 {
		if i == 0 || i >= 3 {
			deck = append(deck, take()) // burn
		}
		if i < len(boardCards) {
			deck = append(deck, boardCards[i])
		} else {
			deck = append(deck, take())
		}
	}
	deck = append(deck, free...)
	return cards.NewDeckFrom(deck)
}

// act makes the player at seat act and fails the test on error.
func act(t *testing.T, tb *Table, seat int, typ ActionType, amount ...int64) {
	t.Helper()
	a := Action{Type: typ}
	if len(amount) > 0 {
		a.Amount = amount[0]
	}
	if err := tb.Act(idOf(seat), a); err != nil {
		t.Fatalf("%s %v %v: %v", names[seat], typ, amount, err)
	}
}

func wantToAct(t *testing.T, tb *Table, seat int) {
	t.Helper()
	id, ok := tb.ToAct()
	if seat < 0 {
		if ok {
			t.Fatalf("ToAct = %q, want nobody", id)
		}
		return
	}
	if !ok || id != idOf(seat) {
		t.Fatalf("ToAct = %q (%v), want %q", id, ok, idOf(seat))
	}
}

func wantStacks(t *testing.T, tb *Table, want map[int]int64) {
	t.Helper()
	for seat, w := range want {
		got, ok := tb.Stack(idOf(seat))
		if !ok || got != w {
			t.Errorf("%s stack = %d (%v), want %d", names[seat], got, ok, w)
		}
	}
}

func legal(t *testing.T, tb *Table, seat int) Legal {
	t.Helper()
	l, ok := tb.LegalFor(idOf(seat))
	if !ok {
		t.Fatalf("LegalFor(%s): not to act", names[seat])
	}
	return l
}

func eventTexts(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Text
	}
	return out
}

// wantEvents checks that each wanted line appears in order among the events.
func wantEvents(t *testing.T, evs []Event, want ...string) {
	t.Helper()
	texts := eventTexts(evs)
	i := 0
	for _, line := range texts {
		if i < len(want) && line == want[i] {
			i++
		}
	}
	if i < len(want) {
		t.Fatalf("missing event %q\nevents:\n  %s", want[i], strings.Join(texts, "\n  "))
	}
}

// foldToEnd folds every player to act until the hand ends.
func foldToEnd(t *testing.T, tb *Table) {
	t.Helper()
	for range 100 {
		id, ok := tb.ToAct()
		if !ok {
			return
		}
		must(t, tb.Act(id, Action{Type: Fold}))
	}
	t.Fatal("the hand did not end")
}

// checkDown checks or calls for every player until the hand ends.
func checkDown(t *testing.T, tb *Table) {
	t.Helper()
	for range 100 {
		id, ok := tb.ToAct()
		if !ok {
			return
		}
		l, _ := tb.LegalFor(id)
		a := Action{Type: Check}
		if !l.Check {
			a.Type = Call
		}
		must(t, tb.Act(id, a))
	}
	t.Fatal("the hand did not end")
}
