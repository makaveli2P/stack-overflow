package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// The TUI draws ranks and suits itself, so "10" shows as "10" and not "T".

func rankText(r cards.Rank) string {
	switch {
	case r >= cards.Two && r <= cards.Nine:
		return string(rune('0' + r))
	case r == cards.Ten:
		return "10"
	case r == cards.Jack:
		return "J"
	case r == cards.Queen:
		return "Q"
	case r == cards.King:
		return "K"
	case r == cards.Ace:
		return "A"
	}
	return "?"
}

func suitText(s cards.Suit) string {
	switch s {
	case cards.Clubs:
		return "♣"
	case cards.Diamonds:
		return "♦"
	case cards.Hearts:
		return "♥"
	case cards.Spades:
		return "♠"
	}
	return "?"
}

func isRed(s cards.Suit) bool { return s == cards.Hearts || s == cards.Diamonds }

func cardText(c cards.Card) string { return rankText(c.Rank) + suitText(c.Suit) }

func (s styles) faceStyle(c cards.Card) lipgloss.Style {
	if isRed(c.Suit) {
		return s.cardRed
	}
	return s.cardBlack
}

// highlight marks a card that is part of a hand.
type highlight int

const (
	hlNone highlight = iota
	hlWin            // part of a winning hand at showdown
	hlHint           // part of the viewer's best hand now
)

// chip draws one card inline, for example " A♠ ". tight drops the padding.
func (s styles) chip(c cards.Card, tight bool) string {
	return s.chipHL(c, tight, hlNone)
}

// chipHL draws one card inline. A highlighted card is underlined; the
// cards of a winning hand are also bold.
func (s styles) chipHL(c cards.Card, tight bool, hl highlight) string {
	t := cardText(c)
	if !tight {
		t = " " + t + " "
	}
	return s.markFace(s.faceStyle(c), hl).Render(t)
}

// markFace adds the highlight mark to a card face. The mark is on the face
// only, so the card edge never changes during a hand.
func (s styles) markFace(st lipgloss.Style, hl highlight) lipgloss.Style {
	switch hl {
	case hlHint:
		return st.Underline(true)
	case hlWin:
		return st.Underline(true).Bold(true)
	}
	return st
}

// oldChip draws one card of the viewer's last hand inline: dim, so that
// it does not look like a card of a live hand.
func (s styles) oldChip(c cards.Card, tight bool, hl highlight) string {
	t := cardText(c)
	if !tight {
		t = " " + t + " "
	}
	return s.markFace(s.faint, hl).Render(t)
}

// rabbitChip draws a rabbit-hunt card inline: dashed and dim, not a real
// card. The tight form keeps the dashed edge too, so that the colour is
// never the only mark. A rabbit chip is as wide as a normal padded chip.
func (s styles) rabbitChip(c cards.Card, tight bool) string {
	return s.rabbitChipHL(c, tight, hlNone)
}

// rabbitChipHL is rabbitChip with a highlight mark on the face.
func (s styles) rabbitChipHL(c cards.Card, tight bool, hl highlight) string {
	edge := s.rabbitEdge.Render("┆")
	return edge + s.markFace(s.rabbitFace, hl).Render(cardText(c)) + edge
}

// backChip draws one face-down card inline.
func (s styles) backChip(tight bool) string {
	if tight {
		return s.back.Render("░░")
	}
	return s.back.Render(" ░░ ")
}

// chipRow draws cards inline, separated by one space. Cards in hl get
// the highlight.
func (s styles) chipRow(cs []cards.Card, tight bool, hl map[cards.Card]highlight) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = s.chipHL(c, tight, hl[c])
	}
	return strings.Join(parts, " ")
}

// oldChipRow is chipRow for the viewer's cards of the last hand.
func (s styles) oldChipRow(cs []cards.Card, tight bool, hl map[cards.Card]highlight) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = s.oldChip(c, tight, hl[c])
	}
	return strings.Join(parts, " ")
}

// cardBox draws one card as a three-line box:
//
//	┌───┐
//	│A♠ │
//	└───┘
//
// The edge is the same for every card on the table, on every street. A
// highlighted card gets a mark on its face only (see markFace), so a card
// never seems to change while it is on the table.
func (s styles) cardBox(c cards.Card, hl highlight) string {
	return s.cardBoxAs(c, hl, s.cardEdge, s.faceStyle(c))
}

// oldCardBox draws one card of the viewer's last hand: a dim edge and a
// dim face.
func (s styles) oldCardBox(c cards.Card, hl highlight) string {
	return s.cardBoxAs(c, hl, s.slotEdge, s.faint)
}

func (s styles) cardBoxAs(c cards.Card, hl highlight, edge, face lipgloss.Style) string {
	text := cardText(c)
	if len([]rune(text)) < 3 {
		text += " "
	}
	return edge.Render("┌───┐") + "\n" +
		edge.Render("│") + s.markFace(face, hl).Render(text) + edge.Render("│") + "\n" +
		edge.Render("└───┘")
}

// trophyBox draws a card of a best hand on the stats dashboard: a bright,
// heavy edge. The table never uses it.
func (s styles) trophyBox(c cards.Card) string {
	text := padRight(cardText(c), 3)
	return s.cardEdgeWin.Render("┏━━━┓") + "\n" +
		s.cardEdgeWin.Render("┃") + s.faceStyle(c).Render(text) + s.cardEdgeWin.Render("┃") + "\n" +
		s.cardEdgeWin.Render("┗━━━┛")
}

// rabbitBox draws a rabbit-hunt card: a dashed box with a dim face, so
// that nobody takes it for a real board card. A highlight marks the face
// only (see markFace).
func (s styles) rabbitBox(c cards.Card, hl highlight) string {
	face := padRight(cardText(c), 3)
	return s.rabbitEdge.Render("╭┄┄┄╮") + "\n" +
		s.rabbitEdge.Render("┆") + s.markFace(s.rabbitFace, hl).Render(face) + s.rabbitEdge.Render("┆") + "\n" +
		s.rabbitEdge.Render("╰┄┄┄╯")
}

// slotBox draws an empty place for a card that is not dealt yet.
func (s styles) slotBox() string {
	return s.slotEdge.Render("╭┄┄┄╮\n┆   ┆\n╰┄┄┄╯")
}

// boxRow joins card boxes side by side.
func boxRow(boxes []string, gap int) string {
	if len(boxes) == 0 {
		return ""
	}
	parts := make([]string, 0, 2*len(boxes))
	sp := strings.Repeat(" ", gap)
	for i, b := range boxes {
		if i > 0 && gap > 0 {
			parts = append(parts, sp)
		}
		parts = append(parts, b)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
