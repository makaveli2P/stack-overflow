// Package cards holds playing cards, a deck, and the poker hand evaluator.
//
// CONTRACT FILE: the exported names and signatures in this file are fixed.
// Other packages depend on them. Implement the bodies; do not change the API.
package cards

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
)

// Suit is a card suit.
type Suit uint8

const (
	Clubs Suit = iota
	Diamonds
	Hearts
	Spades
)

// Rank is a card rank. Two = 2 ... Ace = 14.
type Rank uint8

const (
	Two   Rank = 2
	Three Rank = 3
	Four  Rank = 4
	Five  Rank = 5
	Six   Rank = 6
	Seven Rank = 7
	Eight Rank = 8
	Nine  Rank = 9
	Ten   Rank = 10
	Jack  Rank = 11
	Queen Rank = 12
	King  Rank = 13
	Ace   Rank = 14
)

// Card is one playing card.
type Card struct {
	Rank Rank
	Suit Suit
}

const (
	rankLetters = "??23456789TJQKA"
	suitLetters = "cdhs"
)

var (
	suitSymbols = [...]string{"♣", "♦", "♥", "♠"}
	rankNames   = [...]string{
		"?", "?", "Two", "Three", "Four", "Five", "Six", "Seven",
		"Eight", "Nine", "Ten", "Jack", "Queen", "King", "Ace",
	}
)

// String returns the short form, for example "As", "Td", "2c".
func (c Card) String() string { return c.Rank.String() + c.Suit.letter() }

// Symbol returns the suit symbol: ♣ ♦ ♥ ♠.
func (s Suit) Symbol() string {
	if int(s) >= len(suitSymbols) {
		return "?"
	}
	return suitSymbols[s]
}

func (s Suit) letter() string {
	if int(s) >= len(suitLetters) {
		return "?"
	}
	return suitLetters[s : s+1]
}

func (r Rank) valid() bool { return r >= Two && r <= Ace }

// String returns the rank letter: 2..9, T, J, Q, K, A.
func (r Rank) String() string {
	if !r.valid() {
		return "?"
	}
	return rankLetters[r : r+1]
}

// Name returns the rank word, singular: "Ace", "King", "Ten", "Two".
func (r Rank) Name() string {
	if !r.valid() {
		return "?"
	}
	return rankNames[r]
}

// Plural returns the rank word, plural: "Aces", "Sixes", "Twos".
func (r Rank) Plural() string {
	if r == Six {
		return "Sixes"
	}
	return r.Name() + "s"
}

// Parse reads the short form ("As", "td", "10h" is also accepted).
// The suit can also be a symbol, for example "A♠".
func Parse(s string) (Card, error) {
	in := strings.TrimSpace(s)
	rankPart, suitPart := in, ""
	switch {
	case strings.HasPrefix(in, "10"):
		rankPart, suitPart = "T", in[2:]
	case len(in) >= 2:
		rankPart, suitPart = in[:1], in[1:]
	}
	r, okRank := parseRank(rankPart)
	st, okSuit := parseSuit(suitPart)
	if !okRank || !okSuit {
		return Card{}, fmt.Errorf("cards: bad card %q", s)
	}
	return Card{Rank: r, Suit: st}, nil
}

func parseRank(s string) (Rank, bool) {
	if len(s) != 1 {
		return 0, false
	}
	i := strings.IndexByte(rankLetters[2:], upper(s[0]))
	return Rank(i + 2), i >= 0
}

func parseSuit(s string) (Suit, bool) {
	if len(s) == 1 {
		i := strings.IndexByte(suitLetters, lower(s[0]))
		return Suit(i), i >= 0
	}
	for i, sym := range suitSymbols {
		if s == sym {
			return Suit(i), true
		}
	}
	return 0, false
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b - 'A' + 'a'
	}
	return b
}

// MustParseMany reads a space-separated list such as "As Kd 7c". It panics on error.
// Tests use it.
func MustParseMany(s string) []Card {
	fields := strings.Fields(s)
	out := make([]Card, 0, len(fields))
	for _, f := range fields {
		c, err := Parse(f)
		if err != nil {
			panic(err)
		}
		out = append(out, c)
	}
	return out
}

// Deck is an ordered stack of cards. Draw takes from the top.
type Deck struct {
	cards []Card
}

// FullDeck returns the 52 cards in a fixed order: clubs to spades, Two to Ace.
func FullDeck() []Card {
	out := make([]Card, 0, 52)
	for s := Clubs; s <= Spades; s++ {
		for r := Two; r <= Ace; r++ {
			out = append(out, Card{Rank: r, Suit: s})
		}
	}
	return out
}

// NewShuffledDeck returns all 52 cards in a random order.
// It MUST use crypto/rand (Fisher-Yates with unbiased crypto/rand indices).
func NewShuffledDeck() *Deck {
	cs := FullDeck()
	for i := len(cs) - 1; i > 0; i-- {
		j := randIndex(uint32(i + 1))
		cs[i], cs[j] = cs[j], cs[i]
	}
	return &Deck{cards: cs}
}

// randIndex returns a uniform random number in [0, n) from crypto/rand.
// It rejects the values above the largest multiple of n, so no index has
// more chance than another.
func randIndex(n uint32) uint32 {
	limit := (1<<32 - 1) - (1<<32)%uint64(n)
	var buf [4]byte
	for {
		if _, err := rand.Read(buf[:]); err != nil {
			panic(fmt.Sprintf("cards: crypto/rand failed: %v", err))
		}
		v := binary.LittleEndian.Uint32(buf[:])
		if uint64(v) <= limit {
			return v % n
		}
	}
}

// NewDeckFrom returns a deck that deals the given cards in the given order.
// Tests use it to stack the deck.
func NewDeckFrom(cs []Card) *Deck {
	return &Deck{cards: append([]Card(nil), cs...)}
}

// Draw removes and returns the top card. It panics if the deck is empty.
func (d *Deck) Draw() Card {
	if len(d.cards) == 0 {
		panic("cards: draw from an empty deck")
	}
	c := d.cards[0]
	d.cards = d.cards[1:]
	return c
}

// Len returns the number of cards left.
func (d *Deck) Len() int { return len(d.cards) }
