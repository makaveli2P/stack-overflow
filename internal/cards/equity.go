package cards

import (
	"fmt"
	"math/rand/v2"
)

// CONTRACT: Equity is fixed; the server uses it for bad-beat stats.

// Equity returns each hand's share of the pot when the rest of the board is
// dealt from the cards not in holes or board: a win counts 1, an n-way split
// counts 1/n. holes has 2+ hands of 2 cards; board has 0, 3, 4 or 5 cards.
// It enumerates every run-out when 2 or fewer cards are to come, and
// otherwise uses samples random run-outs (math/rand/v2 seeded from the
// cards, so the result is deterministic for the same input).
func Equity(holes [][]Card, board []Card, samples int) []float64 {
	return equity(holes, board, samples)
}

// defaultSamples is the number of random run-outs when the caller asks for
// samples <= 0 and more than 2 cards are to come.
const defaultSamples = 20000

// equity panics when a hand does not have 2 cards, when the board does not
// have 0, 3, 4 or 5 cards, or when a card is invalid or used twice. With one
// hand it returns [1]; with no hands it returns an empty slice.
func equity(holes [][]Card, board []Card, samples int) []float64 {
	out := make([]float64, len(holes))
	if len(holes) < 2 {
		if len(holes) == 1 {
			out[0] = 1
		}
		return out
	}
	rest := checkEquityInput(holes, board)
	r := runout{holes: holes, share: out}
	r.fixed = len(board)
	copy(r.board[:], board)
	toCome := 5 - len(board)

	var total float64
	switch {
	case toCome == 0:
		r.score()
		total = 1
	case toCome <= 2:
		total = r.enumerate(rest, toCome)
	default:
		if samples <= 0 {
			samples = defaultSamples
		}
		r.sample(rest, toCome, samples, seedFrom(holes, board))
		total = float64(samples)
	}
	for i := range out {
		out[i] /= total
	}
	return out
}

// checkEquityInput validates the input and returns the cards that are
// still in the deck, in FullDeck order.
func checkEquityInput(holes [][]Card, board []Card) []Card {
	switch len(board) {
	case 0, 3, 4, 5:
	default:
		panic(fmt.Sprintf("cards: Equity needs a board of 0, 3, 4 or 5 cards, not %d", len(board)))
	}
	var used [64]bool
	mark := func(c Card) {
		if !c.Rank.valid() || c.Suit > Spades {
			panic(fmt.Sprintf("cards: Equity got an invalid card %v", c))
		}
		i := cardIndex(c)
		if used[i] {
			panic(fmt.Sprintf("cards: Equity got the card %v twice", c))
		}
		used[i] = true
	}
	for i, h := range holes {
		if len(h) != 2 {
			panic(fmt.Sprintf("cards: Equity hand %d has %d cards, not 2", i, len(h)))
		}
		mark(h[0])
		mark(h[1])
	}
	for _, c := range board {
		mark(c)
	}
	if 2*len(holes)+5 > 52 {
		panic(fmt.Sprintf("cards: Equity cannot deal a board to %d hands", len(holes)))
	}
	rest := make([]Card, 0, 52)
	for _, c := range FullDeck() {
		if !used[cardIndex(c)] {
			rest = append(rest, c)
		}
	}
	return rest
}

func cardIndex(c Card) int { return int(c.Suit)*16 + int(c.Rank) }

// runout scores complete boards for a fixed set of hands.
type runout struct {
	holes [][]Card
	share []float64 // sum of the shares won, by hand
	board [5]Card   // the first `fixed` cards are the known board
	fixed int
	seven [7]Card
	best  []int // scratch: indexes of the hands with the best value
}

// score adds the shares of one complete board (r.board) to r.share.
func (r *runout) score() {
	copy(r.seven[2:], r.board[:])
	var top HandValue
	r.best = r.best[:0]
	for i, h := range r.holes {
		r.seven[0], r.seven[1] = h[0], h[1]
		v, _ := Best(r.seven[:])
		switch c := v.Compare(top); {
		case i == 0 || c > 0:
			top = v
			r.best = append(r.best[:0], i)
		case c == 0:
			r.best = append(r.best, i)
		}
	}
	part := 1 / float64(len(r.best))
	for _, i := range r.best {
		r.share[i] += part
	}
}

// enumerate scores every run-out of n (1 or 2) cards and returns their count.
func (r *runout) enumerate(rest []Card, n int) float64 {
	count := 0
	for i := range rest {
		r.board[r.fixed] = rest[i]
		if n == 1 {
			r.score()
			count++
			continue
		}
		for j := i + 1; j < len(rest); j++ {
			r.board[r.fixed+1] = rest[j]
			r.score()
			count++
		}
	}
	return float64(count)
}

// sample scores `samples` random run-outs of n cards. Each run-out is a
// partial Fisher-Yates shuffle of rest, so every n-card subset has the same
// chance whatever order an earlier sample left rest in.
func (r *runout) sample(rest []Card, n, samples int, seed [2]uint64) {
	rng := rand.New(rand.NewPCG(seed[0], seed[1]))
	deck := append([]Card(nil), rest...)
	for range samples {
		for k := range n {
			j := k + rng.IntN(len(deck)-k)
			deck[k], deck[j] = deck[j], deck[k]
			r.board[r.fixed+k] = deck[k]
		}
		r.score()
	}
}

// seedFrom hashes the cards (FNV-1a, 64 bit) into a PCG seed, so the same
// hands and board always give the same samples.
func seedFrom(holes [][]Card, board []Card) [2]uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	add := func(c Card) {
		h ^= uint64(cardIndex(c)) + 1
		h *= prime
	}
	for _, hand := range holes {
		add(hand[0])
		add(hand[1])
		h ^= 0xFF // hand separator
		h *= prime
	}
	for _, c := range board {
		add(c)
	}
	return [2]uint64{h, h*prime ^ 0x9E3779B97F4A7C15}
}
