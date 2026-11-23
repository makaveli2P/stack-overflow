package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// slots places ring positions around the table. Ring position 0 is the
// viewer at the bottom centre; the ring then goes clockwise: bottom left,
// left, the top row from left to right, right, bottom right. A value of -1
// means that the slot is not used.
type slots struct {
	top                     []int
	left, right             int
	bottomLeft, bottomRight int
}

func ringSlots(n int) slots {
	s := slots{left: -1, right: -1, bottomLeft: -1, bottomRight: -1}
	first, last := 1, n-1
	switch {
	case n <= 3:
	case n <= 6:
		s.left, s.right = 1, n-1
		first, last = 2, n-2
	default:
		s.bottomLeft, s.left, s.right, s.bottomRight = 1, 2, n-2, n-1
		first, last = 3, n-3
	}
	for k := first; k <= last; k++ {
		s.top = append(s.top, k)
	}
	return s
}

// seatInfo is everything needed to draw one seat.
type seatInfo struct {
	sv     engine.SeatView
	me     bool
	button bool
	sb, bb bool
	toAct  bool
	won    int64
	hand   string       // hand name shown at showdown
	away   bool         // seated, but the player is offline
	shown  []cards.Card // cards the player chose to show after the hand (Result.Revealed)
	folded bool         // the player folded in the last hand (shown cards get "folded · shown")
}

func (m *model) seatInfo(i int) seatInfo {
	t := m.snap.Table
	sv := t.Seats[i]
	si := seatInfo{sv: sv, me: i == t.MySeat}
	if sv.Empty {
		return si
	}
	if t.HandNumber > 0 && m.dealtIn(i) {
		si.button = i == t.Button
		si.sb = i == t.SmallBlindSeat
		si.bb = i == t.BigBlindSeat
	}
	si.toAct = t.InHand && i == t.ToAct
	if r := t.Result; r != nil && !t.InHand {
		// The result names seats of the last hand. Between hands a new
		// player can take a seat, so a win or a shown hand counts only for
		// the player who played it.
		played := m.dealtIn(i)
		for _, w := range r.Winnings {
			if w.Seat == i && played && samePlayer(w.PlayerID, sv.PlayerID) {
				si.won += w.Amount
			}
		}
		for _, sh := range r.Shown {
			if sh.Seat == i && played && samePlayer(sh.PlayerID, sv.PlayerID) {
				si.hand = sh.Hand
			}
		}
		si.shown = m.revealed(i)
		si.folded = m.foldedLast(sv.PlayerID) && m.dealtIn(i)
	}
	if st, ok := m.standing(sv.Name); ok && !st.Online {
		si.away = true
	}
	return si
}

// standing returns the leaderboard row for name. Two keys can share a name
// (for example one player with and without an SSH key), but only one of
// them can be online, so an online row wins.
func (m *model) standing(name string) (server.Standing, bool) {
	var found server.Standing
	ok := false
	for _, st := range m.snap.Leaderboard {
		if st.Name != name {
			continue
		}
		if st.Online {
			return st, true
		}
		found, ok = st, true
	}
	return found, ok
}

// tableView draws the seats around the board, w cells wide. With h > 0 it
// spreads the rows over h lines; with h == 0 it packs them.
func (m *model) tableView(w, h int, full bool) string {
	n := len(m.snap.Table.Seats)
	if n == 0 {
		return lipgloss.PlaceHorizontal(w, lipgloss.Center, m.st.dim.Render("No table yet."))
	}
	sl := ringSlots(n)
	base := max(m.snap.Table.MySeat, 0)
	seatW := seatWidth(sl, w, full)
	box := func(k int) string {
		if k < 0 {
			return strings.Repeat(" ", seatW)
		}
		return m.seatBox(m.seatInfo((base+k)%n), seatW, full)
	}

	// Side columns exist only when the ring has seats at the sides.
	sideW := 0
	if sl.left >= 0 {
		sideW = seatW + 1
	}
	midW := w - 2*sideW
	row := func(align lipgloss.Position, left, right int, centre string) string {
		parts := []string{lipgloss.PlaceHorizontal(midW, lipgloss.Center, centre)}
		if sideW > 0 {
			parts = []string{
				lipgloss.PlaceHorizontal(sideW, lipgloss.Left, box(left)),
				parts[0],
				lipgloss.PlaceHorizontal(sideW, lipgloss.Right, box(right)),
			}
		}
		return lipgloss.JoinHorizontal(align, parts...)
	}

	boardW := min(midW, 44)
	if full {
		boardW = min(midW, 46)
	}
	rows := []string{
		row(lipgloss.Center, sl.left, sl.right, m.boardBox(boardW, full)),
		row(lipgloss.Bottom, sl.bottomLeft, sl.bottomRight, m.viewerBox(base, seatW, midW, full)),
	}
	if len(sl.top) > 0 {
		rows = append([]string{topRow(sl.top, w, box)}, rows...)
	}
	return stackRows(rows, w, h)
}

// seatWidth picks the width of one seat box so that the top row and the
// board both fit in w cells.
func seatWidth(sl slots, w int, full bool) int {
	cols := max(len(sl.top), 3)
	maxSeat := 18
	if full {
		maxSeat = 22
	}
	seatW := min(maxSeat, (w-cols)/cols) // at least one cell between seats
	if sl.left >= 0 {
		boardMin := 28
		if full {
			boardMin = 37
		}
		seatW = min(seatW, (w-boardMin)/2-1)
	}
	return max(seatW, 12)
}

// topRow puts the top seats in equal columns, one seat centred in each.
func topRow(ring []int, w int, box func(int) string) string {
	n := len(ring)
	cols := make([]string, n)
	for i, k := range ring {
		cw := w*(i+1)/n - w*i/n // spread the spare cells over all columns
		cols[i] = lipgloss.PlaceHorizontal(cw, lipgloss.Center, box(k))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// viewerBox draws the bottom-centre seat: the viewer's own seat (large in
// full mode), or for a spectator the seat at ring position 0.
func (m *model) viewerBox(seat, seatW, midW int, full bool) string {
	si := m.seatInfo(seat)
	switch {
	case m.snap.Table.MySeat < 0:
		return m.seatBox(si, seatW, full)
	case full:
		return m.myBox(si, min(42, midW))
	}
	return m.seatBox(si, min(max(seatW+6, 30), midW), full)
}

// stackRows puts rows under each other. Spare lines up to h go between the
// rows (at most two each) and then above and below, so the block is centred.
func stackRows(rows []string, w, h int) string {
	used := 0
	for _, r := range rows {
		used += height(r)
	}
	gap := 0
	if h > used && len(rows) > 1 {
		gap = min((h-used)/(len(rows)+1), 2)
	}
	sep := strings.Repeat("\n", gap)
	block := strings.Join(rows, "\n"+sep)
	if h > height(block) {
		block = lipgloss.PlaceVertical(h, lipgloss.Center, block)
	}
	return lipgloss.PlaceHorizontal(w, lipgloss.Left, block)
}

// seatBorder picks the border colour that says the most about the seat.
func (m *model) seatBorder(si seatInfo) lipgloss.TerminalColor {
	switch {
	case si.toAct:
		return colGold
	case si.won > 0:
		return colWin
	case si.me:
		return colMe
	case si.sv.Folded || si.sv.SittingOut || si.sv.Busted || si.away:
		return colFaint
	}
	return colDim
}

// badges returns the button and blind marks of a seat. The tight form
// leaves out the padding, for narrow seats.
func (m *model) badges(si seatInfo, tight bool) string {
	st := m.st
	var out []string
	if si.button {
		d := " D "
		if tight {
			d = "D"
		}
		out = append(out, st.badgeD.Render(d))
	}
	if si.sb {
		out = append(out, st.badgeBlind.Render("SB"))
	}
	if si.bb {
		out = append(out, st.badgeBlind.Render("BB"))
	}
	return strings.Join(out, " ")
}

func (m *model) nameLine(si seatInfo, w int, suffix string) string {
	st := m.st
	badges := m.badges(si, false)
	if lipgloss.Width(si.sv.Name+suffix)+1+lipgloss.Width(badges) > w {
		badges = m.badges(si, true)
	}
	ns := st.bold
	switch {
	case si.toAct:
		ns = st.gold
	case si.me:
		ns = st.me
	case si.sv.Folded || si.sv.SittingOut || si.sv.Busted:
		ns = st.dim
	}
	name := ns.Render(si.sv.Name)
	if suffix != "" {
		name += st.dim.Render(suffix)
	}
	return spread(name, badges, w)
}

func (m *model) stackLine(si seatInfo, w int) string {
	st := m.st
	sv := si.sv
	bet := ""
	if sv.Bet > 0 {
		bet = st.bet.Render("● " + chips(sv.Bet))
	}
	stack := st.text.Render(chips(sv.Stack))
	switch {
	case sv.AllIn && sv.Stack == 0 && lipgloss.Width(bet)+7 <= w:
		stack = st.allIn.Render("ALL-IN")
	case sv.AllIn && sv.Stack == 0:
		stack = st.allIn.Render("0")
	case sv.Stack == 0:
		stack = st.faint.Render("0")
	}
	return spread(stack, bet, w)
}

// statusText says why a seat holds no cards, or "".
func (m *model) statusText(si seatInfo) string {
	st := m.st
	sv := si.sv
	switch {
	case sv.Folded:
		return st.faint.Render("folded")
	case sv.Busted:
		return st.err.Render("busted")
	case sv.SittingOut:
		return st.dim.Render("sitting out")
	case m.snap.Table.InHand && !sv.InHand:
		return st.faint.Render("next hand")
	}
	return ""
}

// foldedShown is the note on the seat of a player who folded and then
// showed cards after the hand.
const foldedShown = "folded · shown"

// seatCards draws the seat's cards inline, or "" when it holds none. A
// folded hand has no live cards (HasCards is false), also after the hand.
// During a run-out every live seat draws its face-up cards.
// Cards that a player showed after the hand are face up for everyone, with
// a "shown" mark; the mark of a folded player is "folded · shown". When the
// cards and the mark do not fit in w cells, the cards go tight, and then
// the folded mark gets shorter: in full mode the note line under the cards
// takes it (named is false), in compact mode it becomes "folded" and at
// last "shown". When no form with a mark fits, the compact seat draws the
// cards with no mark: the card back of a one-card show already says that
// the card was shown. named is true when the line names the fold.
//
// The viewer's own folded cards are face up but dim for the viewer only,
// while the viewer can still show them (canShow), so that the viewer
// knows which key shows which card.
func (m *model) seatCards(si seatInfo, full bool, w int) (line string, named bool) {
	type form struct {
		tight bool
		mark  string
		named bool
	}
	var forms []form
	switch {
	case len(si.shown) == 0:
		forms = []form{{!full, "", false}, {true, "", false}}
	case !si.folded:
		forms = []form{{!full, "shown", false}, {true, "shown", false}}
	case full:
		forms = []form{{false, foldedShown, true}, {true, foldedShown, true}, {false, "", false}, {true, "", false}}
	default:
		forms = []form{{true, foldedShown, true}, {true, "folded", true}, {true, "shown", false}, {true, "", false}}
	}
	for _, f := range forms {
		mark := ""
		if f.mark != "" {
			mark = " " + m.st.shown.Render(f.mark)
		}
		line, named = m.holeCardsAs(si, f.tight, mark), f.named
		if ansi.StringWidth(line) <= w {
			break
		}
	}
	return line, named
}

// holeCards is seatCards with tight cards, or with wide cards first when
// tight is false, without the named result.
func (m *model) holeCards(si seatInfo, tight bool, w int) string {
	line, _ := m.seatCards(si, !tight, w)
	return line
}

func (m *model) holeCardsAs(si seatInfo, tight bool, mark string) string {
	st := m.st
	sv := si.sv
	switch {
	case len(sv.Cards) == 2 && si.me && (sv.HasCards || len(si.shown) > 0) && m.inResult():
		// The viewer's cards of the hand that just ended stay dim until
		// the next deal, so that the new cards are clearly new.
		return st.oldChipRow(sv.Cards, tight, m.highlights()) + mark
	case len(sv.Cards) == 2 && (sv.HasCards || len(si.shown) > 0 || m.runoutLive(sv)):
		return st.chipRow(sv.Cards, tight, m.highlights()) + mark
	case m.ownFoldedFaces(si):
		return st.oldChipRow(sv.Cards, tight, m.rabbitMarks()) + mark
	case len(si.shown) > 0:
		row := st.chipRow(si.shown, tight, nil)
		if len(si.shown) < 2 {
			row += " " + st.backChip(tight)
		}
		return row + mark
	case !sv.HasCards:
		return ""
	}
	return st.backChip(tight) + " " + st.backChip(tight)
}

// ownFoldedFaces reports whether si is the viewer's own folded hand that
// the viewer can still show, during the hand or after it. Only the
// viewer's own snapshot holds these cards (SeatView.Cards of the own
// seat), and only the viewer's own seat draws them, face up but dim.
func (m *model) ownFoldedFaces(si seatInfo) bool {
	return si.me && len(si.sv.Cards) == 2 && !si.sv.HasCards && len(si.shown) == 0 && m.canShow()
}

// runoutLive reports whether sv is a live seat of a paused run-out. Its
// hole cards are face up for every viewer, so the seat draws
// SeatView.Cards also when the engine leaves HasCards false.
func (m *model) runoutLive(sv engine.SeatView) bool {
	t := m.snap.Table
	return t.InHand && t.Runout && sv.InHand && !sv.Folded
}

// revealed returns the cards that the player in seat i showed after the
// hand: the winner of a hand without a showdown, or a player who folded.
func (m *model) revealed(i int) []cards.Card {
	t := m.snap.Table
	if t.InHand || t.Result == nil || i < 0 || i >= len(t.Seats) {
		return nil
	}
	var out []cards.Card
	for _, sh := range t.Result.Revealed {
		if sh.Seat == i && samePlayer(sh.PlayerID, t.Seats[i].PlayerID) {
			out = append(out, sh.Cards...)
		}
	}
	return out
}

// samePlayer reports whether a result entry with the player id resultID
// belongs to the player seatID who sits in the seat now. An entry without
// an id (an old fixture) belongs to the seat.
func samePlayer(resultID, seatID string) bool {
	return resultID == "" || resultID == seatID
}

// note is the most useful last line for a seat: clock, win, hand or action.
func (m *model) note(si seatInfo, w int) string {
	st := m.st
	switch {
	case si.toAct:
		if bar := m.timerBar(w); bar != "" {
			return bar
		}
		return st.gold.Render("to act…")
	case si.won > 0:
		return st.win.Render("wins " + chips(si.won))
	case si.hand != "":
		return st.dim.Render(fitHand(si.hand, w))
	case si.away:
		return st.away.Render("away")
	case si.sv.LastAction != "" && !si.sv.Folded:
		return st.dim.Italic(true).Render(si.sv.LastAction)
	}
	return ""
}

// seatBox draws one seat, w cells wide. Full mode uses four lines inside
// the border, compact mode three.
func (m *model) seatBox(si seatInfo, w int, full bool) string {
	inner := w - 4
	lines := 3
	if full {
		lines = 4
	}
	if si.sv.Empty {
		body := []string{m.st.faint.Render(fmt.Sprintf("seat %d", si.sv.Index+1)), m.st.faint.Render("open")}
		return m.st.seatEmpty.Width(w - 2).Render(padLines(body, lines, inner))
	}
	suffix := ""
	if si.me {
		suffix = " (you)"
		if !full && m.snap.Table.MySeat >= 0 {
			lines = 4 // the hand name goes under the cards
		}
	}
	body := []string{m.nameLine(si, inner, suffix), m.stackLine(si, inner)}
	cardsPart, named := m.seatCards(si, full, inner)
	foldShown := si.folded && len(si.shown) > 0
	switch {
	case cardsPart != "":
	case si.me:
		cardsPart = m.myStatus(si)
	default:
		cardsPart = m.statusText(si)
	}
	if full {
		note := m.note(si, inner)
		if foldShown && !named {
			text := foldedShown
			if ansi.StringWidth(text) > inner {
				text = "folded"
			}
			note = m.st.shown.Render(text)
		}
		body = append(body, cardsPart, note)
	} else {
		third := cardsPart
		rest := inner - lipgloss.Width(third)
		if third != "" {
			rest--
		}
		var hint string
		if lines == 4 {
			hint = m.myHintLine(inner)
		}
		// After a showdown the note is the shown hand; the hint under the
		// cards already names it.
		dup := hint != "" && si.won == 0 && si.hand != ""
		// The mark after shown cards already says that the player folded.
		dup = dup || foldShown
		n := m.note(si, max(rest, 0))
		if n == "" && m.ownFoldedFaces(si) {
			// The dim cards of the own folded hand take the place of the
			// "folded" status: keep the word after the cards.
			n = m.myStatus(si)
		}
		if n != "" && rest >= 4 && !dup {
			third = strings.TrimSpace(third + " " + n)
		}
		body = append(body, third)
		if lines == 4 {
			body = append(body, hint)
		}
	}
	return m.st.seat.BorderForeground(m.seatBorder(si)).Width(w - 2).Render(padLines(body, lines, inner))
}

// padLines cuts each line to w cells and pads the list to n lines.
func padLines(lines []string, n, w int) string {
	out := make([]string, n)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		}
	}
	return strings.Join(out, "\n")
}

// myBox draws the viewer's own seat in full mode, with large cards and
// the name of the best hand under them.
func (m *model) myBox(si seatInfo, w int) string {
	st := m.st
	inner := w - 4
	sv := si.sv
	head := []string{m.nameLine(si, inner, " (you)"), m.stackLine(si, inner)}

	var cardCol string
	if folded := m.ownFoldedFaces(si); len(sv.Cards) == 2 && (sv.HasCards || len(si.shown) > 0 || folded) {
		hl := m.highlights()
		box := st.cardBox
		if m.inResult() || folded {
			box = st.oldCardBox // the last hand, or a folded hand: dim
		}
		if folded {
			hl = m.rabbitMarks() // no live hand: only a "would have had" mark
		}
		cardCol = boxRow([]string{box(sv.Cards[0], hl[sv.Cards[0]]), box(sv.Cards[1], hl[sv.Cards[1]])}, 1)
	} else {
		cardCol = boxRow([]string{st.slotBox(), st.slotBox()}, 1)
	}
	sideW := inner - lipgloss.Width(cardCol) - 2
	hint := m.handHint(inner)
	var side []string
	if si.toAct {
		side = []string{st.gold.Render("YOUR TURN"), m.timerBar(sideW)}
	} else {
		note := m.note(si, sideW)
		if si.won == 0 && si.hand != "" && hint != "" {
			// After a showdown the note is the shown hand, which the hint
			// under the cards already names.
			note = ""
		}
		for _, l := range []string{m.myStatus(si), note} {
			if l != "" && (len(side) == 0 || ansi.Strip(side[0]) != ansi.Strip(l)) {
				side = append(side, l)
			}
		}
	}
	switch {
	case len(si.shown) > 0 && si.folded:
		side = append(side, st.shown.Render(foldedShown))
	case len(si.shown) > 0:
		side = append(side, st.shown.Render("shown to everyone"))
	case m.canShowAtEnd() && m.snap.Me.ShowAtEnd != [2]bool{}:
		side = append(side, st.shown.Render("shows at the end"))
	}
	if l := m.handLabel(); l != "" && len(side) < 3 {
		side = append(side, l)
	}
	sideBlock := padLines(side, 3, sideW)
	cardsLine := lipgloss.JoinHorizontal(lipgloss.Top, cardCol, "  ", sideBlock)
	body := strings.Join(head, "\n") + "\n" + cardsLine + "\n" + hint
	return st.seat.BorderForeground(m.seatBorder(si)).Width(w - 2).Render(clip(body, inner, 6))
}

// handLabel names the hand of the viewer's cards: "Hand #12" in a live
// hand, and "last hand · #12" while the result of that hand is on screen.
// It is "" when the viewer holds no cards.
func (m *model) handLabel() string {
	t := m.snap.Table
	me, ok := m.mySeat()
	if !ok || len(me.Cards) != 2 || !me.HasCards || t.HandNumber <= 0 {
		return ""
	}
	switch {
	case t.InHand:
		return m.st.dim.Render(fmt.Sprintf("Hand #%d", t.HandNumber))
	case m.inResult():
		return m.st.faint.Render(fmt.Sprintf("last hand · #%d", t.HandNumber))
	}
	return ""
}

// myHintLine is the line under the viewer's cards in compact mode: the
// best-hand hint, with "last hand" in front of it while the result of
// that hand is on screen.
func (m *model) myHintLine(w int) string {
	if !m.inResult() {
		return m.handHint(w)
	}
	lead := m.st.faint.Render("last hand · ")
	if hint := m.handHint(w - lipgloss.Width(lead)); hint != "" {
		return lead + hint
	}
	return ""
}

// myStatus explains the viewer's seat when it holds no cards.
func (m *model) myStatus(si seatInfo) string {
	st := m.st
	sv := si.sv
	switch {
	case sv.Folded:
		return st.faint.Render("folded")
	case sv.Busted:
		return st.err.Render("busted · B to rebuy")
	case sv.SittingOut:
		return st.dim.Render("sitting out · O to play")
	case !sv.InHand && m.inResult():
		// Between hands the engine clears InHand and Folded on every
		// seat. The note under the cards already says "wins N" or "Fold".
		return ""
	case !sv.InHand:
		return st.dim.Render("waiting for a hand")
	}
	return ""
}

// inResult reports whether the result of a hand that the viewer was dealt
// into is on screen.
func (m *model) inResult() bool {
	t := m.snap.Table
	return !t.InHand && t.Result != nil && t.MySeat >= 0 && t.MySeat < len(t.Seats) && m.dealtIn(t.MySeat)
}

// myHand is the viewer's own hand now. The best-hand hint and the hand
// rankings box both read it, so the two always agree.
type myHand struct {
	hole [2]cards.Card
	made bool            // the flop is out: hv and best hold the best hand
	hv   cards.HandValue // the best five-card hand, when made
	best [5]cards.Card   // the five cards of that hand, when made
}

// category returns the class of the hand: the best five-card hand once
// the flop is out, else One Pair for a pocket pair and High Card for
// other hole cards.
func (h myHand) category() cards.Category {
	switch {
	case h.made:
		return h.hv.Category
	case h.hole[0].Rank == h.hole[1].Rank:
		return cards.OnePair
	}
	return cards.HighCard
}

// myHandNow returns the viewer's hand, when the viewer holds live cards:
// before the flop, or with three or more board cards. It reads only the
// viewer's own seat, never another player's cards.
func (m *model) myHandNow() (myHand, bool) {
	me, ok := m.mySeat()
	board := m.snap.Table.Board
	if !ok || len(me.Cards) != 2 || !me.HasCards || me.Folded || (len(board) > 0 && len(board) < 3) {
		return myHand{}, false
	}
	h := myHand{hole: [2]cards.Card{me.Cards[0], me.Cards[1]}}
	if len(board) >= 3 {
		h.hv, h.best = cards.Best(append(append([]cards.Card{}, me.Cards...), board...))
		h.made = true
	}
	return h, true
}

// myBest returns the viewer's best five-card hand, when the viewer holds
// live cards and the board has three or more cards.
func (m *model) myBest() (cards.HandValue, [5]cards.Card, bool) {
	h, ok := m.myHandNow()
	if !ok || !h.made {
		return cards.HandValue{}, [5]cards.Card{}, false
	}
	return h.hv, h.best, true
}

// handHint names the viewer's hand for the line under the viewer's cards:
// the best hand once the flop is out, a short name for the hole cards
// before it, or "" when the viewer holds no live cards.
func (m *model) handHint(w int) string {
	h, ok := m.myHandNow()
	switch {
	case !ok:
		return ""
	case h.made:
		return m.st.bold.Render(fitHand(h.hv.String(), w))
	}
	return m.st.dim.Render(fitHand(startingHand(h.hole[0], h.hole[1]), w))
}

// startingHand names two hole cards: "Pocket Kings", "Ace-King suited".
func startingHand(a, b cards.Card) string {
	if a.Rank == b.Rank {
		return "Pocket " + a.Rank.Plural()
	}
	if b.Rank > a.Rank {
		a, b = b, a
	}
	kind := " offsuit"
	if a.Suit == b.Suit {
		kind = " suited"
	}
	return a.Rank.Name() + "-" + b.Rank.Name() + kind
}

// highlights marks the cards that make a hand: the five cards of each
// main-pot winner while a showdown result is on screen, else the five
// cards of the viewer's best hand.
func (m *model) highlights() map[cards.Card]highlight {
	out := map[cards.Card]highlight{}
	t := m.snap.Table
	if !t.InHand && t.Result != nil {
		winners := map[int]bool{}
		for _, w := range t.Result.Winnings {
			if w.PotIndex == 0 {
				winners[w.Seat] = true
			}
		}
		for _, sh := range t.Result.Shown {
			if winners[sh.Seat] {
				for _, c := range sh.Best {
					out[c] = hlWin
				}
			}
		}
		if len(out) > 0 {
			return out
		}
		// After a rabbit hunt: the viewer's "would have had" hand.
		if marks := m.rabbitMarks(); len(marks) > 0 {
			return marks
		}
	}
	// The hint marks a made hand in a live hand only; five high cards
	// are not worth a mark.
	if hv, best, ok := m.myBest(); ok && t.InHand {
		for _, c := range madeCards(hv.Category, best) {
			out[c] = hlHint
		}
	}
	return out
}

// madeCards returns the cards of best that make its category, the same
// category that the hand rankings box marks: the two cards of a Pair, the
// four of Two Pair, the three of Three of a Kind, the four of Four of a
// Kind, and all five for a Straight, a Flush, a Full House and better.
// Kickers and a High Card hand get no mark.
func madeCards(cat cards.Category, best [5]cards.Card) []cards.Card {
	switch cat {
	case cards.HighCard:
		return nil
	case cards.Straight, cards.Flush, cards.FullHouse, cards.StraightFlush:
		return best[:]
	}
	// One Pair, Two Pair, Three and Four of a Kind: the cards whose rank
	// shows more than once in the five.
	n := map[cards.Rank]int{}
	for _, c := range best {
		n[c.Rank]++
	}
	var out []cards.Card
	for _, c := range best {
		if n[c.Rank] > 1 {
			out = append(out, c)
		}
	}
	return out
}

// timerBar draws the turn clock as a bar plus seconds, w cells wide. It
// returns "" when w has no cell for the bar next to the seconds label.
func (m *model) timerBar(w int) string {
	left, ok := m.turnLeft()
	total := m.clockTotal()
	if !ok || total <= 0 || w < 6 {
		return ""
	}
	label := fmt.Sprintf(" %2ds", seconds(left))
	barW := w - len(label)
	if barW < 1 {
		// A long label (10000 s or more) leaves no cell for the bar.
		return ""
	}
	frac := float64(left) / float64(total)
	frac = min(max(frac, 0), 1)
	filled := min(max(int(frac*float64(barW)+0.5), 0), barW)
	st := m.st.win
	switch {
	case frac <= 0.25:
		st = m.st.err
	case frac <= 0.5:
		st = m.st.gold
	}
	return st.Render(strings.Repeat("━", filled)) + m.st.faint.Render(strings.Repeat("━", barW-filled)) + st.Render(label)
}

// untilText formats the time left before t on the server clock.
func (m *model) untilText(t time.Time) string {
	return fmt.Sprintf("%ds", seconds(t.Sub(m.serverNow())))
}

// shortNames maps the start of a hand name to a short form for small seats.
var shortNames = []struct{ prefix, short string }{
	{"Royal Flush", "Royal flush"},
	{"Straight Flush", "Str. flush"},
	{"Four of a Kind", "Quads"},
	{"Full House", "Full house"},
	{"Flush", "Flush"},
	{"Straight", "Straight"},
	{"Three of a Kind", "Trips"},
	{"Two Pair", "Two pair"},
	{"Pair", "Pair"},
	{"High Card", "High card"},
}

// fitHand shortens a hand name until it fits w cells:
// "Three of a Kind, Queens" -> "Trips, Queens" -> "Three of a Kind" ->
// "Trips".
func fitHand(name string, w int) string {
	if lipgloss.Width(name) <= w {
		return name
	}
	for _, s := range shortNames {
		rest, ok := strings.CutPrefix(name, s.prefix)
		if !ok {
			continue
		}
		if medium := s.short + rest; lipgloss.Width(medium) <= w {
			return medium
		}
		if rest != "" && lipgloss.Width(s.prefix) <= w {
			return s.prefix
		}
		return s.short
	}
	return name
}
