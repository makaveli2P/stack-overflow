package engine

import (
	"fmt"
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// cardsText writes cards with suit symbols, for example "A♠ K♦".
func cardsText(cs []cards.Card) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.Rank.String() + c.Suit.Symbol()
	}
	return strings.Join(parts, " ")
}

// boardText writes the board. On the turn and the river the new card is in
// brackets: "K♠ 7♥ 2♦ [9♣]".
func boardText(board []cards.Card) string {
	if len(board) <= 3 {
		return cardsText(board)
	}
	last := len(board) - 1
	return cardsText(board[:last]) + " [" + cardsText(board[last:]) + "]"
}

// potName names a pot for the log.
func potName(index, count int) string {
	switch {
	case count == 1:
		return "the pot"
	case index == 0:
		return "the main pot"
	}
	return fmt.Sprintf("side pot %d", index)
}

func winText(w Winning, potCount int) string {
	s := fmt.Sprintf("%s wins %d from %s", w.Name, w.Amount, potName(w.PotIndex, potCount))
	if w.Hand != "" {
		s += " with " + w.Hand
	}
	return s
}
