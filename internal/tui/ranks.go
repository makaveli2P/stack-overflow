package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// The hand rankings box sits under the right panel in full mode. It lists
// the 10 poker hands from best to worst and marks the viewer's hand now.

// minPanelH is the smallest height of the right panel when the box shows.
const minPanelH = 8

// rankRow is one hand of the rankings box.
type rankRow struct {
	name    string // the name in the list
	you     string // the name on the "You:" line
	example string // a short example, rank letters only (T = ten)
}

// rankRows are the 10 hands, best first (DESIGN.md "Hand rankings box").
var rankRows = [10]rankRow{
	{"Royal flush", "Royal Flush", "A K Q J T, one suit"},
	{"Straight flush", "Straight Flush", "9 8 7 6 5, one suit"},
	{"Four of a kind", "Four of a Kind", "Q Q Q Q"},
	{"Full house", "Full House", "K K K 7 7"},
	{"Flush", "Flush", "any 5, one suit"},
	{"Straight", "Straight", "8 7 6 5 4"},
	{"Three of a kind", "Three of a Kind", "8 8 8"},
	{"Two pair", "Two Pair", "J J 4 4"},
	{"Pair", "Pair", "A A"},
	{"High card", "High Card", "none of the above"},
}

// rankRowOf returns the row of a hand: 0 is Royal flush, 9 is High card.
func rankRowOf(h myHand) int {
	cat := h.category()
	return categoryRow(cat, cat == cards.StraightFlush && h.hv.Kickers[0] == cards.Ace)
}

// categoryRow returns the row of a hand category: 0 is Royal flush (royal
// is true), 9 is High card.
func categoryRow(cat cards.Category, royal bool) int {
	switch {
	case royal:
		return 0
	case cat > cards.StraightFlush:
		return 1
	}
	return int(cards.StraightFlush-cat) + 1
}

// myRankRow returns the row of the viewer's hand now, or -1 when the
// viewer holds no live hole cards. It uses the same hand as the best-hand
// hint (myHandNow), and only the viewer's own cards.
func (m *model) myRankRow() int {
	h, ok := m.myHandNow()
	if !ok || !m.snap.Table.InHand {
		return -1
	}
	return rankRowOf(h)
}

// ranksLayout says where the box puts the examples.
type ranksLayout int

const (
	ranksInline  ranksLayout = iota // each example on the line of its hand
	ranksStacked                    // each example on a line under its hand
	ranksPlain                      // no examples
)

// rankLead is the width of the mark and the number before each name.
const rankLead = 4 // "▶10 "

// ranksInlineFits reports whether each example fits on the line of its
// hand in inner cells.
func ranksInlineFits(inner int) bool {
	for _, r := range rankRows {
		if rankLead+ansi.StringWidth(r.name)+1+ansi.StringWidth(r.example) > inner {
			return false
		}
	}
	return true
}

// ranksLayouts returns the layouts to try for inner cells, most detail
// first.
func ranksLayouts(inner int) []ranksLayout {
	if ranksInlineFits(inner) {
		return []ranksLayout{ranksInline, ranksPlain}
	}
	return []ranksLayout{ranksStacked, ranksPlain}
}

// rankingsBox draws the box w cells wide and at most room lines high. It
// drops the examples first, and returns "" when the box does not fit or
// the viewer hid it with I.
func (m *model) rankingsBox(w, room int) string {
	inner := w - 4
	if m.hideRanks || inner < 20 {
		return ""
	}
	for _, l := range ranksLayouts(inner) {
		lines := m.ranksLines(inner, l)
		if len(lines)+2 <= room {
			return m.st.panel.Width(w - 2).Render(clip(strings.Join(lines, "\n"), inner, len(lines)))
		}
	}
	return ""
}

// ranksLines returns the lines inside the box border.
func (m *model) ranksLines(inner int, l ranksLayout) []string {
	st := m.st
	mine := m.myRankRow()
	would := -1 // the row of the "would have had" hand, after a rabbit hunt
	if rh, ok := m.rabbitHand(); ok && mine < 0 {
		would = rabbitRow(rh)
	}
	you := st.faint.Render("Best hand first")
	switch {
	case mine >= 0:
		you = st.gold.Render("You: " + rankRows[mine].you)
	case would >= 0:
		you = st.rabbitFace.Render("Would have: " + rankRows[would].you)
	}
	lines := []string{st.bold.Render("HAND RANKINGS"), you}
	for i, r := range rankRows {
		lead := " " + st.faint.Render(fmt.Sprintf("%2d", i+1)) + " "
		name := st.text.Render(r.name)
		ex := st.dim.Render(r.example)
		switch i {
		case mine:
			lead = st.gold.Render(fmt.Sprintf("▶%2d ", i+1))
			name = st.gold.Render(r.name)
			ex = st.gold.Render(r.example)
		case would:
			// A hollow marker: a hand that the viewer did not hold.
			lead = st.rabbitFace.Render(fmt.Sprintf("▷%2d ", i+1))
			name = st.rabbitFace.Render(r.name)
			ex = st.rabbitFace.Render(r.example)
		}
		switch l {
		case ranksInline:
			lines = append(lines, spread(lead+name, ex, inner))
		case ranksStacked:
			lines = append(lines, lead+name, strings.Repeat(" ", rankLead)+ex)
		default:
			lines = append(lines, lead+name)
		}
	}
	return lines
}

// rightColumn draws the right side of full mode, w x h: the panel, and
// under it the rankings box when there is room for both.
func (m *model) rightColumn(w, h int) string {
	box := m.rankingsBox(w, h-minPanelH)
	if box == "" {
		return m.panelView(w, h, true)
	}
	return m.panelView(w, h-height(box), true) + "\n" + box
}
