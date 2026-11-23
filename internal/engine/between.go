package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// This file holds the calls that work on the last finished hand (rabbit
// hunt, shows after the hand) and the small batch-2 table calls.

// lastHand returns the last finished hand and its result. It returns nil
// while a hand runs or before the first hand.
func (t *Table) lastHand() (*hand, *Result) {
	h := t.hand
	if h == nil || h.running || h.result == nil {
		return nil, nil
	}
	return h, h.result
}

// rabbitCards returns how many cards the deck must still hold to deal the
// rest of a board of n cards, burns included.
func rabbitCards(n int) int {
	switch {
	case n == 0:
		return 4 + 2 + 2 // burn + flop, burn + turn, burn + river
	case n <= 3:
		return 2 + 2
	case n == 4:
		return 2
	}
	return 0
}

// rabbitHunt deals the rest of the board from the deck of the last hand.
// The deck is in the state that the hand left it in, and the deal burns one
// card before each street, as dealStreet does. So the rabbit cards are the
// cards that the real deal would have put on the board.
func (t *Table) rabbitHunt() error {
	h, res := t.lastHand()
	if res == nil || !res.CanRabbit {
		return ErrNotNow
	}
	var groups []string
	for n := len(h.board); n < 5; {
		h.deck.Draw() // burn
		k := 1
		if n == 0 {
			k = 3
		}
		street := make([]cards.Card, k)
		for i := range street {
			street[i] = h.deck.Draw()
		}
		res.Rabbit = append(res.Rabbit, street...)
		groups = append(groups, "["+cardsText(street)+"]")
		n += k
	}
	res.CanRabbit = false
	t.emit(EvRabbit, -1, "", 0, "Rabbit hunt: %s", strings.Join(groups, " "))
	return nil
}

// showCards adds hole cards of a player of the last hand to
// Result.Revealed. Two kinds of player may show: the NoShowdown winner (the
// only live player of that hand) and a player who folded in that hand. The
// player must still sit in the same seat: a player who stood up, or who sat
// down again, is not the player of that hand. A hand shown at showdown or at
// the start of a run-out is shown already, so the call returns ErrNotNow for
// it. A call that asks only for cards already shown changes nothing and
// returns nil.
//
// Each player has at most one entry in Result.Revealed, in the order of the
// first show. A later call adds the other card to that entry; the entry
// lists the shown cards in deal order.
func (t *Table) showCards(id string, left, right bool) error {
	h, res := t.lastHand()
	if res == nil || (!left && !right) {
		return ErrNotNow
	}
	p := t.find(id)
	if p == nil || h.players[p.seat] != p || len(p.hole) != 2 || h.shown[p.seat] || h.tabled[p.seat] {
		return ErrNotNow
	}
	winner := res.NoShowdown && p.live()
	if !winner && !p.folded {
		return ErrNotNow
	}
	var added []cards.Card
	for i, want := range [2]bool{left, right} {
		if want && !p.revealed[i] {
			p.revealed[i] = true
			added = append(added, p.hole[i])
		}
	}
	if len(added) == 0 {
		return nil
	}
	shown := Shown{Seat: p.seat, PlayerID: p.id, Name: p.name}
	for i := range 2 {
		if p.revealed[i] {
			shown.Cards = append(shown.Cards, p.hole[i])
		}
	}
	if i := slices.IndexFunc(res.Revealed, func(s Shown) bool { return s.PlayerID == p.id }); i >= 0 {
		res.Revealed[i] = shown
	} else {
		res.Revealed = append(res.Revealed, shown)
	}
	t.emit(EvReveal, p.seat, p.id, 0, "%s shows %s", p.name, cardsText(added))
	return nil
}

// rename changes the display name of a seated player. Later events and the
// next Result use the new name.
func (t *Table) rename(id, name string) error {
	p := t.find(id)
	switch {
	case p == nil:
		return ErrNotSeated
	case name == "":
		return fmt.Errorf("%w: empty name", ErrIllegal)
	case name == p.name:
		return nil
	}
	old := p.name
	p.name = name
	t.emit(EvSeat, p.seat, p.id, 0, "%s is now %s", old, name)
	return nil
}

// handNumber returns the number of the last hand dealt. Before the first
// hand it is Config.FirstHand-1 (0 when FirstHand is 0 or 1).
func (t *Table) handNumber() int { return t.handNum }
