package tui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Batch 10: a limit of rabbit hunts for each player, and the private line
// "You would have had: …" after a hunt.

// offerRabbit reports whether the hints offer H: the viewer can hunt now
// and has a hunt left. With no hunt left, H still sends the hunt, and the
// server error goes to the status line. A shared frame (m.public) never
// offers H: the offer and its count tell the viewer's hunts left.
func (m *model) offerRabbit() bool {
	if m.public || !m.canRabbit() {
		return false
	}
	return m.snap.Config.RabbitHunts <= 0 || m.snap.Me.RabbitsLeft > 0
}

// rabbitHintLabel is the label of the H hint: "rabbit hunt (2 left)" with
// a limit, "rabbit hunt" without one.
func (m *model) rabbitHintLabel() string {
	if m.snap.Config.RabbitHunts > 0 {
		return fmt.Sprintf("rabbit hunt (%d left)", m.snap.Me.RabbitsLeft)
	}
	return "rabbit hunt"
}

// rabbitHand returns the viewer's own "would have had" hand while the
// rabbit cards of the last hand are on screen. Only the viewer's own
// snapshot holds it (Me.RabbitHand).
func (m *model) rabbitHand() (server.RabbitHand, bool) {
	rh := m.snap.Me.RabbitHand
	if rh.Hand == "" || len(m.rabbit()) == 0 {
		return server.RabbitHand{}, false
	}
	return rh, true
}

// rabbitMarks marks the 5 cards of the viewer's "would have had" hand. It
// is empty when there is no such hand on screen.
func (m *model) rabbitMarks() map[cards.Card]highlight {
	out := map[cards.Card]highlight{}
	if rh, ok := m.rabbitHand(); ok {
		for _, c := range rh.Best {
			out[c] = hlHint
		}
	}
	return out
}

// rabbitRow returns the row of the rankings box for rh: 0 is Royal flush,
// 9 is High card.
func rabbitRow(rh server.RabbitHand) int {
	return categoryRow(rh.Category, rh.Hand == "Royal Flush")
}

// wouldLine is the viewer's private line under the rabbit label, w cells
// wide: "You would have had: Flush, Ace high". Shorter forms come first
// when it does not fit. It is "" when there is no such hand.
func (m *model) wouldLine(w int) string {
	rh, ok := m.rabbitHand()
	if !ok {
		return ""
	}
	st := m.st
	short := rankRows[rabbitRow(rh)].you
	for _, f := range [][2]string{
		{"You would have had: ", rh.Hand},
		{"Would have had: ", rh.Hand},
		{"You would have had: ", short},
		{"Would have: ", short},
	} {
		if ansi.StringWidth(f[0]+f[1]) <= w {
			return st.rabbitFace.Render(f[0]) + st.bold.Render(f[1])
		}
	}
	return fit(st.rabbitFace.Render("Would have: ")+st.bold.Render(short), w)
}
