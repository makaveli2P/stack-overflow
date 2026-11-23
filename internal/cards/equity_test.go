package cards

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func hands(hs ...string) [][]Card {
	out := make([][]Card, len(hs))
	for i, h := range hs {
		out[i] = MustParseMany(h)
	}
	return out
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func wantEquity(t *testing.T, got, want []float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("equity %v, want %v", got, want)
	}
	var sum float64
	for i := range got {
		sum += got[i]
		if !near(got[i], want[i], tol) {
			t.Fatalf("equity %v, want %v (within %v)", got, want, tol)
		}
	}
	if !near(sum, 1, 1e-9) {
		t.Fatalf("equity %v sums to %v, want 1", got, sum)
	}
}

// refEquity enumerates every run-out with plain loops and Best. It is the
// reference for the exact cases.
func refEquity(holes [][]Card, board []Card) []float64 {
	used := map[Card]bool{}
	for _, h := range holes {
		used[h[0]], used[h[1]] = true, true
	}
	for _, c := range board {
		used[c] = true
	}
	var rest []Card
	for _, c := range FullDeck() {
		if !used[c] {
			rest = append(rest, c)
		}
	}
	share := make([]float64, len(holes))
	boards := 0
	var deal func(b []Card, start int)
	deal = func(b []Card, start int) {
		if len(b) == 5 {
			boards++
			vals := make([]HandValue, len(holes))
			for i, h := range holes {
				vals[i], _ = Best(append(slices.Clone(h), b...))
			}
			top := slices.MaxFunc(vals, HandValue.Compare)
			var winners []int
			for i, v := range vals {
				if v.Compare(top) == 0 {
					winners = append(winners, i)
				}
			}
			for _, i := range winners {
				share[i] += 1 / float64(len(winners))
			}
			return
		}
		for i := start; i < len(rest); i++ {
			deal(append(slices.Clone(b), rest[i]), i+1)
		}
	}
	deal(slices.Clone(board), 0)
	for i := range share {
		share[i] /= float64(boards)
	}
	return share
}

// Exact preflop values below come from a full enumeration of all
// C(48,5) or C(46,5) boards (run once; too slow for every test run).
func TestEquityPreflop(t *testing.T) {
	cases := []struct {
		name  string
		holes [][]Card
		want  []float64
	}{
		{"AA vs KK", hands("Ac Ad", "Kh Ks"), []float64{0.81255, 0.18745}},
		{"AKo vs QQ", hands("As Kd", "Qh Qc"), []float64{0.42835, 0.57165}},
		{"AA vs KK vs QQ", hands("Ah As", "Kd Kc", "Qh Qc"), []float64{0.66868, 0.18515, 0.14617}},
	}
	for _, c := range cases {
		got := Equity(c.holes, nil, 20000)
		wantEquity(t, got, c.want, 0.01)
	}
	// The well-known rounded numbers.
	wantEquity(t, Equity(hands("Ac Ad", "Kh Ks"), nil, 20000), []float64{0.82, 0.18}, 0.01)
	wantEquity(t, Equity(hands("As Kd", "Qh Qc"), nil, 20000), []float64{0.43, 0.57}, 0.01)
}

func TestEquityDeterministic(t *testing.T) {
	h := hands("Ac Ad", "Kh Ks")
	a := Equity(h, nil, 5000)
	b := Equity(h, nil, 5000)
	if !slices.Equal(a, b) {
		t.Fatalf("two calls gave %v and %v", a, b)
	}
	// samples <= 0 uses the default and still works.
	wantEquity(t, Equity(h, nil, 0), []float64{0.81255, 0.18745}, 0.01)
}

func TestEquitySetVsFlushDraw(t *testing.T) {
	set, draw := "7c 7d", "Ah Qh"
	// Turn, one card to come: the draw wins with the 7 hearts that do not
	// pair the board (4h 5h 6h 8h 9h Th Jh). Kh and 3h fill the set up.
	got := Equity(hands(set, draw), MustParseMany("7h 2h Ks 3c"), 0)
	wantEquity(t, got, []float64{37.0 / 44, 7.0 / 44}, 1e-12)

	// Flop, two cards to come: compare with the plain enumeration.
	board := MustParseMany("7h 2h Ks")
	got = Equity(hands(set, draw), board, 0)
	wantEquity(t, got, refEquity(hands(set, draw), board), 1e-12)
	if got[0] < 0.70 || got[0] > 0.80 {
		t.Fatalf("set vs flush draw on the flop = %v, want the set near 0.75", got)
	}
	// The sample count does not matter for an exact case.
	if again := Equity(hands(set, draw), board, 7); !slices.Equal(again, got) {
		t.Fatalf("exact equity changed with samples: %v vs %v", again, got)
	}
}

func TestEquitySplits(t *testing.T) {
	// A royal flush on the board: everybody plays the board.
	got := Equity(hands("2c 3d", "4h 5c"), MustParseMany("As Ks Qs Js Ts"), 0)
	wantEquity(t, got, []float64{0.5, 0.5}, 0)
	// The same ranks and no spade in either hand: every run-out splits.
	got = Equity(hands("Ac Kd", "Ad Kc"), MustParseMany("2s 7s 9s"), 0)
	wantEquity(t, got, []float64{0.5, 0.5}, 1e-12)
	// Three-way split on the board.
	got = Equity(hands("2c 3d", "4h 5c", "6d 7c"), MustParseMany("As Ks Qs Js Ts"), 0)
	wantEquity(t, got, []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}, 1e-12)
}

func TestEquityRiverAndThreeWay(t *testing.T) {
	// Complete board: one winner takes all.
	got := Equity(hands("Ah Kh", "Qc Qd", "2s 3s"), MustParseMany("Qh 7h 2h 9c 4d"), 0)
	wantEquity(t, got, []float64{1, 0, 0}, 0)
	// Three-way on the flop and the turn: exact, against the reference.
	for _, b := range []string{"Kh 9h 2c", "Kh 9h 2c 5d"} {
		h := hands("Ah Kd", "9c 9d", "Qh Jh")
		board := MustParseMany(b)
		wantEquity(t, Equity(h, board, 0), refEquity(h, board), 1e-12)
	}
}

func TestEquityEdgeInputs(t *testing.T) {
	if got := Equity(hands("Ac Ad"), nil, 100); !slices.Equal(got, []float64{1}) {
		t.Fatalf("one hand = %v, want [1]", got)
	}
	if got := Equity(nil, nil, 100); len(got) != 0 {
		t.Fatalf("no hands = %v, want []", got)
	}
	bad := []struct {
		holes [][]Card
		board string
		want  string
	}{
		{hands("Ac Ad", "Ac Kd"), "", "twice"},
		{hands("Ac Ad", "Kd Kc"), "Ac 2c 3c", "twice"},
		{hands("Ac Ad", "Kd"), "", "has 1 cards"},
		{hands("Ac Ad", "Kd Kc"), "2c 3c", "board of 0, 3, 4 or 5"},
		{[][]Card{{{Rank: 1, Suit: Clubs}, {Rank: Two, Suit: Clubs}}, MustParseMany("Kd Kc")}, "", "invalid card"},
	}
	for _, b := range bad {
		func() {
			defer func() {
				r := recover()
				if s, _ := r.(string); !strings.Contains(s, b.want) {
					t.Errorf("Equity(%v, %q) panic = %v, want %q", b.holes, b.board, r, b.want)
				}
			}()
			Equity(b.holes, MustParseMany(b.board), 100)
		}()
	}
}

func BenchmarkEquityPreflopHeadsUp(b *testing.B) {
	h := hands("As Kd", "Qh Qc")
	for b.Loop() {
		Equity(h, nil, 20000)
	}
}

func BenchmarkEquityFlopExact(b *testing.B) {
	h := hands("7c 7d", "Ah Qh")
	board := MustParseMany("7h 2h Ks")
	for b.Loop() {
		Equity(h, board, 0)
	}
}
