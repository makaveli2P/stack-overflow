package cards

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func r5(rs ...Rank) [5]Rank {
	var out [5]Rank
	copy(out[:], rs)
	return out
}

func TestBestCategories(t *testing.T) {
	tests := []struct {
		name  string
		cards string
		cat   Category
		kick  [5]Rank
		five  string
		text  string
	}{
		{"royal flush", "As Ks Qs Js Ts 2d 3c", StraightFlush, r5(Ace),
			"As Ks Qs Js Ts", "Royal Flush"},
		{"straight flush", "9h 8h 7h 6h 5h Ah Kh", StraightFlush, r5(Nine),
			"9h 8h 7h 6h 5h", "Straight Flush, Nine high"},
		{"steel wheel", "Ad 2d 3d 4d 5d Kd Qc", StraightFlush, r5(Five),
			"5d 4d 3d 2d Ad", "Straight Flush, Five high"},
		{"quads", "9c 9d 9h 9s Ac Kd 2h", FourOfAKind, r5(Nine, Ace),
			"9s 9h 9d 9c Ac", "Four of a Kind, Nines"},
		{"quads beat board full house", "7c 7d 7h 7s Kc Kd Kh", FourOfAKind, r5(Seven, King),
			"7s 7h 7d 7c Kh", "Four of a Kind, Sevens"},
		{"full house", "Kc Kd Kh 7s 7c 2d 3h", FullHouse, r5(King, Seven),
			"Kh Kd Kc 7s 7c", "Full House, Kings over Sevens"},
		{"full house from two trips", "5c 5d 5h Qs Qc Qd 2h", FullHouse, r5(Queen, Five),
			"Qs Qd Qc 5h 5d", "Full House, Queens over Fives"},
		{"full house picks best pair", "5c 5d 5h Qs Qc 9d 9h", FullHouse, r5(Five, Queen),
			"5h 5d 5c Qs Qc", "Full House, Fives over Queens"},
		{"flush", "Ah Jh 9h 4h 2h Kd Kc", Flush, r5(Ace, Jack, Nine, Four, Two),
			"Ah Jh 9h 4h 2h", "Flush, Ace high"},
		{"six-card flush uses top five", "Ah Jh 9h 4h 3h 2h Kc", Flush, r5(Ace, Jack, Nine, Four, Three),
			"Ah Jh 9h 4h 3h", "Flush, Ace high"},
		{"flush beats straight", "6c 7c 8d 9c Tc 2c Ah", Flush, r5(Ten, Nine, Seven, Six, Two),
			"Tc 9c 7c 6c 2c", "Flush, Ten high"},
		{"straight", "9c Td Jh Qs Kc 2d 2h", Straight, r5(King),
			"Kc Qs Jh Td 9c", "Straight, King high"},
		{"wheel", "Ac 2d 3h 4s 5c Kd 9h", Straight, r5(Five),
			"5c 4s 3h 2d Ac", "Straight, Five high"},
		{"six-card straight uses top", "4c 5d 6h 7s 8c 9d Kh", Straight, r5(Nine),
			"9d 8c 7s 6h 5d", "Straight, Nine high"},
		{"wheel and six high", "Ac 2d 3h 4s 5c 6d Kh", Straight, r5(Six),
			"6d 5c 4s 3h 2d", "Straight, Six high"},
		{"straight with pair", "5c 6d 7h 8s 9c 9d 2h", Straight, r5(Nine),
			"9d 8s 7h 6d 5c", "Straight, Nine high"},
		{"broadway", "Ac Kd Qh Js Tc 3d 3h", Straight, r5(Ace),
			"Ac Kd Qh Js Tc", "Straight, Ace high"},
		{"no wrap-around straight", "Qc Kd Ah 2s 3c 7d 8h", HighCard, r5(Ace, King, Queen, Eight, Seven),
			"Ah Kd Qc 8h 7d", "High Card, Ace"},
		{"trips", "4c 4d 4h As Kc 9d 2h", ThreeOfAKind, r5(Four, Ace, King),
			"4h 4d 4c As Kc", "Three of a Kind, Fours"},
		{"two pair", "Qc Qd 5h 5s Ac 9d 2h", TwoPair, r5(Queen, Five, Ace),
			"Qd Qc 5s 5h Ac", "Two Pair, Queens and Fives"},
		{"three pairs: third pair is kicker", "Qc Qd 5h 5s 9c 9d 2h", TwoPair, r5(Queen, Nine, Five),
			"Qd Qc 9d 9c 5s", "Two Pair, Queens and Nines"},
		{"three pairs: single card beats third pair", "Qc Qd 5h 5s 3c 3d Kh", TwoPair, r5(Queen, Five, King),
			"Qd Qc 5s 5h Kh", "Two Pair, Queens and Fives"},
		{"pair", "Jc Jd 9h 7s 4c 3d 2h", OnePair, r5(Jack, Nine, Seven, Four),
			"Jd Jc 9h 7s 4c", "Pair of Jacks"},
		{"high card", "Ac Jd 9h 7s 4c 3d 2h", HighCard, r5(Ace, Jack, Nine, Seven, Four),
			"Ac Jd 9h 7s 4c", "High Card, Ace"},
		{"five cards", "6s 6h Kd Kc 2s", TwoPair, r5(King, Six, Two),
			"Kd Kc 6s 6h 2s", "Two Pair, Kings and Sixes"},
		{"six cards", "6s 6h 6d Kc 2s 3s", ThreeOfAKind, r5(Six, King, Three),
			"6s 6h 6d Kc 3s", "Three of a Kind, Sixes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, five := Best(MustParseMany(tt.cards))
			if v.Category != tt.cat || v.Kickers != tt.kick {
				t.Errorf("value = %v %v, want %v %v", v.Category, v.Kickers, tt.cat, tt.kick)
			}
			if got := handString(five[:]); got != tt.five {
				t.Errorf("five = %s, want %s", got, tt.five)
			}
			if got := v.String(); got != tt.text {
				t.Errorf("String() = %q, want %q", got, tt.text)
			}
		})
	}
}

func handString(cs []Card) string {
	s := ""
	for i, c := range cs {
		if i > 0 {
			s += " "
		}
		s += c.String()
	}
	return s
}

func TestCompareTies(t *testing.T) {
	tests := []struct {
		name string
		a, b string // seven cards each
		want int
	}{
		{"two pair: kicker decides", "Qc Qd 5h 5s Ac 9d 2h", "Qh Qs 5c 5d Kc 9c 2c", 1},
		{"two pair: second pair decides", "Qc Qd 6h 6s 2c 3d 4h", "Qh Qs 5c 5d Ac Kc 9c", 1},
		{"pair: third kicker decides", "Jc Jd Ah 8s 4c 3d 2h", "Jh Js Ac 8c 3c 2c 6d", -1},
		{"flush vs flush: fifth card decides", "Ah Jh 9h 4h 3h Kc Kd", "As Js 9s 4s 2s Kh Qd", 1},
		{"full house: trips decide first", "Kc Kd Kh 2s 2c 7d 8h", "Qc Qd Qh As Ac 7c 8c", 1},
		{"full house: pair decides", "Kc Kd Kh 3s 3c 7d 8h", "Ks Kd Kh 2s 2c 7c 8c", 1},
		{"quads: kicker decides", "9c 9d 9h 9s Ac 2d 3h", "9c 9d 9h 9s Kc 2d 3h", 1},
		{"wheel loses to six-high straight", "Ac 2d 3h 4s 5c Kd Kh", "2c 3d 4h 5s 6c Kd Kh", -1},
		{"steel wheel loses to six-high straight flush", "Ad 2d 3d 4d 5d 9c 9h", "2h 3h 4h 5h 6h 9c 9d", -1},
		{"board plays: split", "Ac Kc Qd Jh Td 2c 3d", "Ad Kd Qh Js Tc 4c 5d", 0},
		{"board flush plays: split", "Ah Kh 9h 7h 4h 2c 3d", "Ah Kh 9h 7h 4h 2d 3c", 0},
		{"straight: same high splits", "9c Td Jh Qs Kc 2d 2h", "9d Tc Jc Qc Kd 3d 3h", 0},
		{"high card: fifth card decides", "Ac Jd 9h 7s 4c 3d 2h", "As Js 9c 7c 3c 2d 5h", -1},
		{"trips: second kicker decides", "4c 4d 4h As Qc 9d 2h", "4c 4d 4h As Jc 9d 2h", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			va, _ := Best(MustParseMany(tt.a))
			vb, _ := Best(MustParseMany(tt.b))
			if got := va.Compare(vb); got != tt.want {
				t.Errorf("Compare(%v, %v) = %d, want %d", va, vb, got, tt.want)
			}
			if got := vb.Compare(va); got != -tt.want {
				t.Errorf("reverse Compare = %d, want %d", got, -tt.want)
			}
		})
	}
}

func TestCategoryOrder(t *testing.T) {
	hands := []string{
		"Ac Jd 9h 7s 4c", "2c 2d 3h 4s 5d", "2c 2d 3h 3s 4c", "2c 2d 2h 3s 4c",
		"2c 3d 4h 5s 6c", "2c 3c 4c 5c 7c", "2c 2d 2h 3s 3c", "2c 2d 2h 2s 3c",
		"2c 3c 4c 5c 6c",
	}
	for i := 1; i < len(hands); i++ {
		lo, _ := Best(MustParseMany(hands[i-1]))
		hi, _ := Best(MustParseMany(hands[i]))
		if hi.Compare(lo) != 1 || hi.Category != lo.Category+1 {
			t.Errorf("%v (%v) does not beat %v (%v)", hands[i], hi.Category, hands[i-1], lo.Category)
		}
	}
}

func TestCategoryStrings(t *testing.T) {
	want := []string{"High Card", "One Pair", "Two Pair", "Three of a Kind", "Straight",
		"Flush", "Full House", "Four of a Kind", "Straight Flush"}
	for i, w := range want {
		if got := Category(i).String(); got != w {
			t.Errorf("Category(%d) = %q, want %q", i, got, w)
		}
	}
}

func TestBestPanicsOnFewCards(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Best did not panic on 4 cards")
		}
	}()
	Best(MustParseMany("As Ks Qs Js"))
}

// refValue is a naive reference evaluator for exactly five cards. It sorts
// rank groups by (size, rank) and checks straights by the rank spread, which
// is a different method from Best.
func refValue(cs []Card) HandValue {
	counts := map[Rank]int{}
	flush := true
	for _, c := range cs {
		counts[c.Rank]++
		if c.Suit != cs[0].Suit {
			flush = false
		}
	}
	type group struct {
		r Rank
		n int
	}
	var groups []group
	for r, n := range counts {
		groups = append(groups, group{r, n})
	}
	slices.SortFunc(groups, func(a, b group) int {
		if a.n != b.n {
			return b.n - a.n
		}
		return int(b.r) - int(a.r)
	})
	var v HandValue
	for i, g := range groups {
		v.Kickers[i] = g.r
	}
	straightHigh := Rank(0)
	if len(groups) == 5 {
		switch {
		case groups[0].r-groups[4].r == 4:
			straightHigh = groups[0].r
		case groups[0].r == Ace && groups[1].r == Five:
			straightHigh = Five
		}
	}
	switch {
	case straightHigh != 0 && flush:
		return HandValue{Category: StraightFlush, Kickers: r5(straightHigh)}
	case groups[0].n == 4:
		v.Category = FourOfAKind
	case groups[0].n == 3 && groups[1].n == 2:
		v.Category = FullHouse
	case flush:
		v.Category = Flush
	case straightHigh != 0:
		return HandValue{Category: Straight, Kickers: r5(straightHigh)}
	case groups[0].n == 3:
		v.Category = ThreeOfAKind
	case groups[0].n == 2 && groups[1].n == 2:
		v.Category = TwoPair
	case groups[0].n == 2:
		v.Category = OnePair
	default:
		v.Category = HighCard
	}
	return v
}

// refBest tries all five-card subsets with refValue.
func refBest(cs []Card) HandValue {
	var best HandValue
	first := true
	sub := make([]Card, 5)
	var rec func(start, k int)
	rec = func(start, k int) {
		if k == 5 {
			v := refValue(sub)
			if first || v.Compare(best) > 0 {
				best, first = v, false
			}
			return
		}
		for i := start; i <= len(cs)-(5-k); i++ {
			sub[k] = cs[i]
			rec(i+1, k+1)
		}
	}
	rec(0, 0)
	return best
}

func dealRandom(rng *rand.Rand, n int) []Card {
	deck := FullDeck()
	rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	return deck[:n]
}

func TestBestMatchesReference(t *testing.T) {
	n := 200_000
	if testing.Short() {
		n = 20_000
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var cats [9]int
	for i := range n {
		size := 7
		if i%10 == 0 {
			size = 5 + i/10%3
		}
		cs := dealRandom(rng, size)
		v, five := Best(cs)
		cats[v.Category]++
		if want := refBest(cs); v != want {
			t.Fatalf("Best(%s) = %v %v, reference = %v %v", handString(cs), v.Category, v.Kickers, want.Category, want.Kickers)
		}
		if again, _ := Best(five[:]); again != v {
			t.Fatalf("Best(%s): five %s re-evaluate to %v, want %v", handString(cs), handString(five[:]), again, v)
		}
		for _, c := range five {
			if !slices.Contains(cs, c) {
				t.Fatalf("Best(%s) returned card %v that is not in the input", handString(cs), c)
			}
		}
		if hasDuplicate(five[:]) {
			t.Fatalf("Best(%s) returned duplicate cards %s", handString(cs), handString(five[:]))
		}
	}
	for c, k := range cats {
		if k == 0 && Category(c) != StraightFlush {
			t.Errorf("the random hands never made a %v", Category(c))
		}
	}
}

func hasDuplicate(cs []Card) bool {
	seen := map[Card]bool{}
	for _, c := range cs {
		if seen[c] {
			return true
		}
		seen[c] = true
	}
	return false
}

// TestBestOrderInvariant checks that the input order does not change the result.
func TestBestOrderInvariant(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 5000 {
		cs := dealRandom(rng, 7)
		v1, f1 := Best(cs)
		rng.Shuffle(len(cs), func(i, j int) { cs[i], cs[j] = cs[j], cs[i] })
		v2, f2 := Best(cs)
		if v1 != v2 || f1 != f2 {
			t.Fatalf("order changed the result: %v %v vs %v %v", v1, f1, v2, f2)
		}
	}
}

func BenchmarkBest7(b *testing.B) {
	rng := rand.New(rand.NewPCG(5, 6))
	hands := make([][]Card, 1024)
	for i := range hands {
		hands[i] = dealRandom(rng, 7)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		Best(hands[i&1023])
		i++
	}
}
