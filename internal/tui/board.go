package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// boardBox draws the felt in the middle: street, board, pots and results.
func (m *model) boardBox(w int, full bool) string {
	inner := w - 4
	if inner < 10 {
		return ""
	}
	var lines []string
	if full {
		lines = append(lines, m.boardTitle(true, inner))
		lines = append(lines, strings.Split(m.boardCards(inner), "\n")...)
		lines = append(lines, m.boardInfo(2, inner)...)
	} else {
		lines = append(lines, m.boardTitle(false, inner), m.boardCards(0))
		lines = append(lines, m.boardInfo(1, inner)...)
	}
	// The viewer's private "would have had" line goes under the rabbit
	// label. The felt is too narrow for both on one line.
	if l := m.wouldLine(inner); l != "" {
		lines = append(lines, l)
	}
	for i, l := range lines {
		lines[i] = lipgloss.PlaceHorizontal(inner, lipgloss.Center, fit(l, inner))
	}
	return m.st.felt.Width(w - 2).Render(strings.Join(lines, "\n"))
}

// rabbitLabel marks the rabbit-hunt cards on the felt: nobody dealt them.
const rabbitLabel = "rabbit · not dealt"

// runoutTitle replaces the street name while a paused run-out deals the
// board.
const runoutTitle = "ALL-IN · RUN-OUT"

// boardTitle is the first line of the felt, w cells wide. During a run-out
// it names the run-out instead of the street; when the compact title does
// not fit, the hand number leaves first.
func (m *model) boardTitle(full bool, w int) string {
	t := m.snap.Table
	st := m.st
	hand := st.dim.Render(fmt.Sprintf("Hand #%d", t.HandNumber))
	switch {
	case t.InHand:
		street := st.bold.Render(strings.ToUpper(t.Street.String()))
		if t.Runout {
			street = st.allIn.Render(runoutTitle)
		}
		sep := st.dim.Render(" · ")
		if full {
			return street + sep + hand
		}
		pot := st.gold.Render("Pot " + chips(t.TotalPot))
		if title := street + sep + hand + sep + pot; lipgloss.Width(title) <= w {
			return title
		}
		return street + sep + pot
	case t.Result != nil:
		// The countdown to the next deal always shows with a result, so
		// that nobody takes the next hand's cards for this hand's cards.
		head, sep := st.bold.Render("RESULT"), st.dim.Render(" · ")
		if m.snap.NextHandAt.IsZero() {
			return head + sep + hand
		}
		next := st.dim.Render("next hand in ") + st.gold.Render(m.untilText(m.snap.NextHandAt))
		short := head + st.dim.Render(fmt.Sprintf(" #%d", t.HandNumber))
		for _, title := range []string{head + sep + hand + sep + next, short + sep + next, head + sep + next} {
			if lipgloss.Width(title) <= w {
				return title
			}
		}
		return next
	case !m.snap.NextHandAt.IsZero():
		return st.gold.Render("Next hand in " + m.untilText(m.snap.NextHandAt))
	}
	return st.dim.Render("Texas Hold'em · No Limit")
}

// boardCards draws the five board places. With inner > 0 it uses card
// boxes when they fit; else it draws inline chips.
func (m *model) boardCards(inner int) string {
	st := m.st
	board := m.snap.Table.Board
	hl := m.highlights()
	rabbit := m.rabbit()
	if inner >= 25 {
		boxes := make([]string, 5)
		for i := range boxes {
			switch j := i - len(board); {
			case j < 0:
				boxes[i] = st.cardBox(board[i], hl[board[i]])
			case j < len(rabbit):
				boxes[i] = st.rabbitBox(rabbit[j], hl[rabbit[j]])
			default:
				boxes[i] = st.slotBox()
			}
		}
		gap := 0
		if inner >= 29 {
			gap = 1
		}
		return boxRow(boxes, gap)
	}
	parts := make([]string, 5)
	for i := range parts {
		switch j := i - len(board); {
		case j < 0:
			parts[i] = st.chipHL(board[i], false, hl[board[i]])
		case j < len(rabbit):
			parts[i] = st.rabbitChipHL(rabbit[j], false, hl[rabbit[j]])
		default:
			parts[i] = st.faint.Render(" ·· ")
		}
	}
	row := strings.Join(parts, " ")
	if inner > 0 {
		return "\n" + row + "\n"
	}
	return row
}

// boardInfo returns up to n lines about pots, results or why no hand runs.
func (m *model) boardInfo(n, w int) []string {
	t := m.snap.Table
	st := m.st
	var out []string
	switch {
	case t.InHand:
		if n > 1 {
			out = append(out, st.gold.Render("POT "+chips(t.TotalPot)))
		}
		if len(t.Pots) > 1 {
			parts := make([]string, len(t.Pots))
			for i, p := range t.Pots {
				name := "main"
				if i > 0 {
					name = fmt.Sprintf("side %d", i)
				}
				parts[i] = name + " " + chips(p.Amount)
			}
			out = append(out, st.dim.Render(strings.Join(parts, " · ")))
		}
	case t.Result != nil:
		// The title holds the countdown to the next deal (boardTitle).
		out = m.resultLines(w)
		var extra string
		switch {
		case len(m.rabbit()) > 0:
			extra = st.rabbitEdge.Render("┆") + st.rabbitFace.Render(rabbitLabel) + st.rabbitEdge.Render("┆")
		case m.offerRabbit():
			extra = st.gold.Render("H") + st.dim.Render(" "+m.rabbitHintLabel())
		}
		room := n
		if extra != "" {
			room = max(n-1, 1)
		}
		if len(out) > room {
			out = []string{strings.Join(out, st.dim.Render(" · "))}
		}
		if extra != "" {
			out = append(out, extra)
		}
		if n == 1 && len(out) > 1 {
			out = []string{strings.Join(out, st.dim.Render(" · "))}
		}
	default:
		out = append(out, m.waitingText())
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out[:n]
}

// resultLines says who won what, one line per winner.
func (m *model) resultLines(width int) []string {
	st := m.st
	type win struct {
		name   string
		amount int64
		hand   string
	}
	var wins []win
	at := map[string]int{}
	for _, w := range m.snap.Table.Result.Winnings {
		i, ok := at[w.PlayerID]
		if !ok {
			i = len(wins)
			at[w.PlayerID] = i
			wins = append(wins, win{name: w.Name})
		}
		wins[i].amount += w.Amount
		if wins[i].hand == "" {
			wins[i].hand = w.Hand
		}
	}
	out := make([]string, len(wins))
	for i, w := range wins {
		line := st.win.Render(w.name + " wins " + chips(w.amount))
		if w.hand != "" {
			line += st.dim.Render(" · " + fitHand(w.hand, width-lipgloss.Width(line)-3))
		}
		out[i] = line
	}
	return out
}

// rabbit returns the cards of the last hand's rabbit hunt.
func (m *model) rabbit() []cards.Card {
	t := m.snap.Table
	if t.InHand || t.Result == nil {
		return nil
	}
	return t.Result.Rabbit
}

func (m *model) waitingText() string {
	ready := readyIn(m.snap)
	if !m.snap.NextHandAt.IsZero() {
		return m.st.dim.Render(fmt.Sprintf("%d players ready", ready))
	}
	if m.snap.Waiting && ready >= 2 {
		// The room waits for a start (G), not for players.
		if m.snap.Table.MySeat < 0 {
			return m.st.dim.Render(fmt.Sprintf("%d ready · not started", ready))
		}
		return m.st.dim.Render(fmt.Sprintf("%d ready · G to start", ready))
	}
	if m.snap.Table.MySeat < 0 {
		return m.st.dim.Render("Waiting for players · press S to sit")
	}
	return m.st.dim.Render(fmt.Sprintf("Waiting for players · %d of 2 ready", ready))
}
