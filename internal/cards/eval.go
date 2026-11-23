package cards

import "math/bits"

// CONTRACT FILE: the exported names and signatures in this file are fixed.

// Category is the class of a five-card poker hand, from low to high.
type Category uint8

const (
	HighCard Category = iota
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush // a royal flush is an ace-high straight flush
)

var categoryNames = [...]string{
	"High Card", "One Pair", "Two Pair", "Three of a Kind", "Straight",
	"Flush", "Full House", "Four of a Kind", "Straight Flush",
}

// String returns "High Card", "One Pair", ... "Straight Flush".
func (c Category) String() string {
	if int(c) >= len(categoryNames) {
		return "?"
	}
	return categoryNames[c]
}

// HandValue is the strength of the best five-card hand.
// Two HandValues with the same Category compare by Kickers, left to right.
// Kickers lists the ranks that matter for the tie-break, most important first.
// Examples:
//
//	Full house KKK77      -> Kickers [K, 7]
//	Two pair QQ55A        -> Kickers [Q, 5, A]
//	Straight 5-4-3-2-A    -> Kickers [5]   (the wheel is five-high)
//	Flush A-J-9-4-2       -> Kickers [A, J, 9, 4, 2]
//
// Unused slots are zero.
type HandValue struct {
	Category Category
	Kickers  [5]Rank
}

// Compare returns +1 if h beats o, -1 if o beats h, and 0 for a split.
func (h HandValue) Compare(o HandValue) int {
	if h.Category != o.Category {
		return sign(int(h.Category) - int(o.Category))
	}
	for i := range h.Kickers {
		if h.Kickers[i] != o.Kickers[i] {
			return sign(int(h.Kickers[i]) - int(o.Kickers[i]))
		}
	}
	return 0
}

func sign(d int) int {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}

// String returns a human description, for example:
//
//	"Royal Flush", "Straight Flush, King high", "Four of a Kind, Nines",
//	"Full House, Kings over Sevens", "Flush, Ace high", "Straight, Five high",
//	"Three of a Kind, Fours", "Two Pair, Queens and Fives", "Pair of Jacks",
//	"High Card, Ace".
func (h HandValue) String() string {
	k := h.Kickers
	switch h.Category {
	case StraightFlush:
		if k[0] == Ace {
			return "Royal Flush"
		}
		return "Straight Flush, " + k[0].Name() + " high"
	case FourOfAKind:
		return "Four of a Kind, " + k[0].Plural()
	case FullHouse:
		return "Full House, " + k[0].Plural() + " over " + k[1].Plural()
	case Flush:
		return "Flush, " + k[0].Name() + " high"
	case Straight:
		return "Straight, " + k[0].Name() + " high"
	case ThreeOfAKind:
		return "Three of a Kind, " + k[0].Plural()
	case TwoPair:
		return "Two Pair, " + k[0].Plural() + " and " + k[1].Plural()
	case OnePair:
		return "Pair of " + k[0].Plural()
	case HighCard:
		return "High Card, " + k[0].Name()
	}
	return "?"
}

// rankMask has bit r set for each rank r that is present (bits 2..14).
type rankMask uint16

const (
	aceBit   = rankMask(1) << Ace
	wheelLow = rankMask(1<<Two | 1<<Three | 1<<Four | 1<<Five)
)

// highest returns the highest rank in m. m must not be empty.
func (m rankMask) highest() Rank { return Rank(15 - bits.LeadingZeros16(uint16(m))) }

// topStraight returns the high rank of the best straight in m, or 0.
// The ace also counts low, so A-2-3-4-5 is a straight with high rank Five.
func (m rankMask) topStraight() Rank {
	for hi := Ace; hi >= Six; hi-- {
		run := rankMask(0x1F) << (hi - 4)
		if m&run == run {
			return hi
		}
	}
	if m&(aceBit|wheelLow) == aceBit|wheelLow {
		return Five
	}
	return 0
}

// straightRanks returns the five ranks of the straight with high rank hi,
// high to low. The wheel ends with the ace.
func straightRanks(hi Rank) [5]Rank {
	var out [5]Rank
	for i := range out {
		out[i] = hi - Rank(i)
	}
	if hi == Five {
		out[4] = Ace
	}
	return out
}

// counted holds the facts about a set of cards that the evaluator needs.
type counted struct {
	count  [15]uint8   // number of cards of each rank
	bySuit [4]rankMask // ranks present in each suit
	nSuit  [4]uint8    // number of cards in each suit
	all    rankMask    // ranks present in any suit
	groups [5]rankMask // groups[n] = ranks that occur exactly n times
}

func countCards(cs []Card) counted {
	var c counted
	for _, card := range cs {
		c.count[card.Rank]++
		c.bySuit[card.Suit] |= 1 << card.Rank
		c.nSuit[card.Suit]++
	}
	for r := Two; r <= Ace; r++ {
		if n := c.count[r]; n > 0 {
			c.all |= 1 << r
			c.groups[n] |= 1 << r
		}
	}
	return c
}

// Best returns the value of the best five-card hand from 5, 6 or 7 cards,
// and the five cards that make it (ordered: the cards that make the category
// first, then kickers, high to low). It panics on fewer than 5 cards.
func Best(cs []Card) (HandValue, [5]Card) {
	if len(cs) < 5 {
		panic("cards: Best needs at least 5 cards")
	}
	c := countCards(cs)
	flush, flushFive, isFlush := c.flushHand()
	if isFlush && flush.Category == StraightFlush {
		return flush, flushFive
	}
	if q := c.groups[4]; q != 0 {
		return c.fourOfAKind(q.highest())
	}
	if v, five, ok := c.fullHouse(); ok {
		return v, five
	}
	if isFlush {
		return flush, flushFive
	}
	if hi := c.all.topStraight(); hi != 0 {
		return c.straight(hi)
	}
	if t := c.groups[3]; t != 0 {
		return c.sets(ThreeOfAKind, t.highest())
	}
	if p := c.groups[2]; p != 0 {
		hi := p.highest()
		if rest := p &^ (1 << hi); rest != 0 {
			return c.sets(TwoPair, hi, rest.highest())
		}
		return c.sets(OnePair, hi)
	}
	return c.sets(HighCard)
}

// flushHand returns the best straight flush or flush, if any.
func (c *counted) flushHand() (HandValue, [5]Card, bool) {
	for s := Clubs; s <= Spades; s++ {
		if c.nSuit[s] < 5 {
			continue
		}
		m := c.bySuit[s]
		if hi := m.topStraight(); hi != 0 {
			v := HandValue{Category: StraightFlush}
			v.Kickers[0] = hi
			return v, c.pickOneEach(straightRanks(hi), s), true
		}
		v := HandValue{Category: Flush}
		for i := range v.Kickers {
			v.Kickers[i] = m.highest()
			m &^= 1 << v.Kickers[i]
		}
		return v, c.pickOneEach(v.Kickers, s), true
	}
	return HandValue{}, [5]Card{}, false
}

func (c *counted) fourOfAKind(quad Rank) (HandValue, [5]Card) {
	kick := (c.all &^ (1 << quad)).highest()
	v := HandValue{Category: FourOfAKind, Kickers: [5]Rank{quad, kick}}
	var buf [5]Card
	out := c.take(buf[:0], quad, 4)
	c.take(out, kick, 1)
	return v, buf
}

// fullHouse uses the best trips, and the best other rank with two or more
// cards as the pair. That rank can be a second set of trips.
func (c *counted) fullHouse() (HandValue, [5]Card, bool) {
	t := c.groups[3]
	if t == 0 {
		return HandValue{}, [5]Card{}, false
	}
	trips := t.highest()
	pairs := (c.groups[2] | c.groups[3]) &^ (1 << trips)
	if pairs == 0 {
		return HandValue{}, [5]Card{}, false
	}
	pair := pairs.highest()
	v := HandValue{Category: FullHouse, Kickers: [5]Rank{trips, pair}}
	var buf [5]Card
	out := c.take(buf[:0], trips, 3)
	c.take(out, pair, 2)
	return v, buf, true
}

func (c *counted) straight(hi Rank) (HandValue, [5]Card) {
	v := HandValue{Category: Straight}
	v.Kickers[0] = hi
	return v, c.pickOneEach(straightRanks(hi), anySuit)
}

// setSize is the number of cards in each made set of a category.
func setSize(cat Category) int {
	switch cat {
	case OnePair, TwoPair:
		return 2
	case ThreeOfAKind:
		return 3
	}
	return 1
}

// sets builds a pair, two pair, trips or high-card hand. made lists the ranks
// of the sets that make the category, high first. The free slots get the
// highest other ranks as single kickers.
func (c *counted) sets(cat Category, made ...Rank) (HandValue, [5]Card) {
	size := setSize(cat)
	v := HandValue{Category: cat}
	var buf [5]Card
	out := buf[:0]
	rest := c.all
	k := 0
	for _, r := range made {
		v.Kickers[k] = r
		k++
		rest &^= 1 << r
		out = c.take(out, r, size)
	}
	for len(out) < 5 {
		r := rest.highest()
		rest &^= 1 << r
		v.Kickers[k] = r
		k++
		out = c.take(out, r, 1)
	}
	return v, buf
}

// anySuit tells pickOneEach that the suit is free.
const anySuit Suit = 255

// pickOneEach takes one card of each rank, in order. When suit is not
// anySuit, every card must have that suit.
func (c *counted) pickOneEach(ranks [5]Rank, suit Suit) [5]Card {
	var out [5]Card
	for i, r := range ranks {
		if suit != anySuit {
			out[i] = Card{Rank: r, Suit: suit}
			continue
		}
		c.take(out[i:i], r, 1)
	}
	return out
}

// take appends n cards of rank r to dst, higher suits first, so the result
// does not depend on the input order.
func (c *counted) take(dst []Card, r Rank, n int) []Card {
	for s := int(Spades); s >= int(Clubs) && n > 0; s-- {
		if c.bySuit[s]&(1<<r) != 0 {
			dst = append(dst, Card{Rank: r, Suit: Suit(s)})
			n--
		}
	}
	return dst
}
