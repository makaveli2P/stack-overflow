package cards

import (
	"testing"
)

func TestCardStrings(t *testing.T) {
	tests := []struct {
		card   Card
		short  string
		symbol string
	}{
		{Card{Ace, Spades}, "As", "♠"},
		{Card{Ten, Diamonds}, "Td", "♦"},
		{Card{Two, Clubs}, "2c", "♣"},
		{Card{King, Hearts}, "Kh", "♥"},
	}
	for _, tt := range tests {
		if got := tt.card.String(); got != tt.short {
			t.Errorf("%v.String() = %q, want %q", tt.card, got, tt.short)
		}
		if got := tt.card.Suit.Symbol(); got != tt.symbol {
			t.Errorf("Symbol() = %q, want %q", got, tt.symbol)
		}
	}
}

func TestRankWords(t *testing.T) {
	tests := []struct {
		r            Rank
		name, plural string
	}{
		{Ace, "Ace", "Aces"},
		{King, "King", "Kings"},
		{Ten, "Ten", "Tens"},
		{Six, "Six", "Sixes"},
		{Two, "Two", "Twos"},
	}
	for _, tt := range tests {
		if got := tt.r.Name(); got != tt.name {
			t.Errorf("Name(%d) = %q, want %q", tt.r, got, tt.name)
		}
		if got := tt.r.Plural(); got != tt.plural {
			t.Errorf("Plural(%d) = %q, want %q", tt.r, got, tt.plural)
		}
	}
}

func TestParse(t *testing.T) {
	good := map[string]Card{
		"As":   {Ace, Spades},
		"as":   {Ace, Spades},
		"AS":   {Ace, Spades},
		"10h":  {Ten, Hearts},
		"Th":   {Ten, Hearts},
		"td":   {Ten, Diamonds},
		"2c":   {Two, Clubs},
		" 9d ": {Nine, Diamonds},
		"Q♥":   {Queen, Hearts},
		"10♣":  {Ten, Clubs},
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "A", "1s", "Ax", "11h", "Ass", "10", "♠A", "é♠"} {
		if c, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", in, c)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, c := range FullDeck() {
		got, err := Parse(c.String())
		if err != nil || got != c {
			t.Errorf("Parse(%q) = %v, %v", c.String(), got, err)
		}
	}
}

func TestMustParseMany(t *testing.T) {
	got := MustParseMany("As Kd  7c")
	want := []Card{{Ace, Spades}, {King, Diamonds}, {Seven, Clubs}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("MustParseMany did not panic on a bad card")
		}
	}()
	MustParseMany("As Zz")
}

func TestShuffledDeckHasEveryCardOnce(t *testing.T) {
	d := NewShuffledDeck()
	if d.Len() != 52 {
		t.Fatalf("Len = %d, want 52", d.Len())
	}
	seen := map[Card]bool{}
	for d.Len() > 0 {
		c := d.Draw()
		if seen[c] {
			t.Fatalf("card %v dealt twice", c)
		}
		seen[c] = true
	}
	if len(seen) != 52 {
		t.Fatalf("dealt %d distinct cards, want 52", len(seen))
	}
}

// TestShuffleIsUniform is a coarse check: each card lands on top about
// 1/52 of the time, and the decks differ.
func TestShuffleIsUniform(t *testing.T) {
	const n = 52 * 400
	top := map[Card]int{}
	for range n {
		top[NewShuffledDeck().Draw()]++
	}
	for _, c := range FullDeck() {
		if got := top[c]; got < 250 || got > 570 {
			t.Errorf("card %v was on top %d times out of %d; want about 400", c, got, n)
		}
	}
}

func TestRandIndexRange(t *testing.T) {
	for _, n := range []uint32{1, 2, 3, 7, 52} {
		for range 200 {
			if v := randIndex(n); v >= n {
				t.Fatalf("randIndex(%d) = %d", n, v)
			}
		}
	}
}

func TestDeckFrom(t *testing.T) {
	src := MustParseMany("As Kd 7c")
	d := NewDeckFrom(src)
	src[0] = Card{Two, Clubs} // the deck must keep its own copy
	for _, want := range MustParseMany("As Kd 7c") {
		if got := d.Draw(); got != want {
			t.Fatalf("Draw = %v, want %v", got, want)
		}
	}
	if d.Len() != 0 {
		t.Fatalf("Len = %d, want 0", d.Len())
	}
	defer func() {
		if recover() == nil {
			t.Error("Draw on an empty deck did not panic")
		}
	}()
	d.Draw()
}
