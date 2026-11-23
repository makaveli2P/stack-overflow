package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// The tests in this file guard DESIGN.md "Card stability" on the TUI side:
// the drawn cards are the snapshot cards, at every seat rotation, and the
// end of a hand and the next deal are clear.

// stabNames are the players of the rotation fixture. No name is part of
// another name or of other text on the screen.
var stabNames = [9]string{"Ana", "Ben", "Cleo", "Dov", "Eli", "Fox", "Gia", "Hugo", "Ivo"}

// stabHoles are 9 pairs of distinct hole cards; stabBoard is a distinct
// board.
var (
	stabHoles = [9]string{"As Kd", "Qh Jc", "Ts 9d", "8h 7c", "6s 5d", "4h 3c", "2s Ad", "Kh Qc", "Js Th"}
	stabBoard = parseCards("9h 8c 7s 6d 5h")
)

// stabSnap is a live river hand with 9 players. Each seat holds its own
// cards face up, so that every seat box draws cards. mySeat -1 is a
// spectator.
func stabSnap(mySeat int) server.Snapshot {
	seats := make([]engine.SeatView, 9)
	for i := range seats {
		sv := dealt(player(i, stabNames[i], 1000+int64(i)), 0, 100, "")
		sv.Cards = parseCards(stabHoles[i])
		seats[i] = sv
	}
	me := "Watcher"
	if mySeat >= 0 {
		me = stabNames[mySeat]
	}
	s := baseSnap(me, seats, mySeat)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.River, 0, 1, 2
	t.Board = slices.Clone(stabBoard)
	t.TotalPot = 900
	s.Log = []string{"Hand #42 — Ana has the button"}
	s.Chat = nil
	return s
}

// cardRE finds the text of one card on the screen, for example "10♠".
var cardRE = regexp.MustCompile(`(10|[2-9JQKA])[♣♦♥♠]`)

// screenLines draws f at w x h and returns the lines without styles, as
// runes. The fixtures use only narrow runes in the table area, so a rune
// index is a column.
func screenLines(t *testing.T, s server.Snapshot, w, h int) [][]rune {
	t.Helper()
	v := stripANSI(Render(Fixture{Snap: s}, testRenderer(), w, h))
	var out [][]rune
	for _, l := range strings.Split(v, "\n") {
		out = append(out, []rune(l))
	}
	return out
}

func runeAt(l []rune, i int) rune {
	if i < 0 || i >= len(l) {
		return 0
	}
	return l[i]
}

// seatBoxText finds the box of the seat that shows name, and returns the
// text inside the box. It returns false when no box shows the name.
func seatBoxText(lines [][]rune, name string) (string, bool) {
	nr := []rune(name)
	for li := 1; li < len(lines); li++ {
		l := lines[li]
		for c := 0; c+len(nr) <= len(l); c++ {
			if string(l[c:c+len(nr)]) != name || unicodeLetter(runeAt(l, c+len(nr))) || unicodeLetter(runeAt(l, c-1)) {
				continue
			}
			lb := c - 1
			for lb >= 0 && l[lb] == ' ' {
				lb--
			}
			if lb < 0 || l[lb] != '│' {
				continue // the name in other text, not in a seat box
			}
			rb := c + len(nr)
			for rb < len(l) && l[rb] != '│' {
				rb++
			}
			if rb >= len(l) {
				continue
			}
			var inner []string
			for j := li; j < len(lines) && runeAt(lines[j], lb) == '│'; j++ {
				if rb < len(lines[j]) {
					inner = append(inner, string(lines[j][lb+1:rb]))
				}
			}
			return strings.Join(inner, "\n"), true
		}
	}
	return "", false
}

func unicodeLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// cardTexts returns the card texts in s, in reading order.
func cardTexts(s string) []string { return cardRE.FindAllString(s, -1) }

// texts returns the card texts of cs.
func texts(cs []cards.Card) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = cardText(c)
	}
	return out
}

// stabSizes are the sizes of the rotation test: three in full mode, two
// in compact mode.
var stabSizes = [][2]int{{120, 30}, {162, 51}, {188, 55}, {80, 24}, {99, 40}}

// checkSeats checks that each seat box draws exactly want[i], in any
// order, and no other card. It returns the number of cards found in seat
// boxes.
func checkSeats(t *testing.T, label string, lines [][]rune, s server.Snapshot, want func(i int) []string) int {
	t.Helper()
	n := 0
	for i, sv := range s.Table.Seats {
		box, ok := seatBoxText(lines, sv.Name)
		if !ok {
			t.Errorf("%s: no box for seat %d (%s)", label, i, sv.Name)
			continue
		}
		got := cardTexts(box)
		exp := want(i)
		slices.Sort(got)
		exp = slices.Sorted(slices.Values(exp))
		if !slices.Equal(got, exp) {
			t.Errorf("%s: seat %d (%s) draws %v, want %v\n%s", label, i, sv.Name, got, exp, box)
		}
		n += len(got)
	}
	return n
}

// boardRow returns the card texts of the board row: the first line, from
// the top, that holds a board card.
func boardRow(lines [][]rune, board []cards.Card) []string {
	first := cardText(board[0])
	for _, l := range lines {
		if found := cardTexts(string(l)); slices.Contains(found, first) {
			var out []string
			for _, c := range found {
				if slices.Contains(texts(board), c) {
					out = append(out, c)
				}
			}
			return out
		}
	}
	return nil
}

// TestDrawnCardsMatchSnapshot draws the 9-seat table from every seat
// (and as a spectator), at full and compact sizes. Each seat box must
// draw the cards of that seat's SeatView and no other cards, the
// viewer's box must draw Seats[MySeat].Cards, and the board row must draw
// Board in order.
func TestDrawnCardsMatchSnapshot(t *testing.T) {
	for my := -1; my < 9; my++ {
		for _, sz := range stabSizes {
			s := stabSnap(my)
			label := fmt.Sprintf("MySeat %d at %dx%d", my, sz[0], sz[1])
			lines := screenLines(t, s, sz[0], sz[1])
			n := checkSeats(t, label, lines, s, func(i int) []string { return texts(s.Table.Seats[i].Cards) })
			if n != 18 {
				t.Errorf("%s: %d cards in seat boxes, want 18", label, n)
			}
			if got := boardRow(lines, s.Table.Board); !slices.Equal(got, texts(s.Table.Board)) {
				t.Errorf("%s: board row %v, want %v", label, got, texts(s.Table.Board))
			}
			all := 0
			for _, l := range lines {
				all += len(cardTexts(string(l)))
			}
			if all != 18+5 {
				t.Errorf("%s: %d cards on the screen, want 23", label, all)
			}
		}
	}
}

// During a paused run-out the engine leaves HasCards false for the other
// live seats, and the TUI draws their face-up cards. A folded seat draws
// no cards, also when the snapshot holds some.
func TestDrawnCardsRunout(t *testing.T) {
	for my := -1; my < 9; my++ {
		for _, sz := range stabSizes {
			s := stabSnap(my)
			t2 := &s.Table
			t2.Runout, t2.Street = true, engine.Turn
			t2.Board = t2.Board[:4]
			folded := (max(my, 0) + 4) % 9
			for i := range t2.Seats {
				sv := &t2.Seats[i]
				if i != my {
					sv.HasCards = false
				}
				if i == folded {
					sv.Folded, sv.HasCards = true, false
				}
			}
			label := fmt.Sprintf("run-out, MySeat %d at %dx%d", my, sz[0], sz[1])
			lines := screenLines(t, s, sz[0], sz[1])
			checkSeats(t, label, lines, s, func(i int) []string {
				if i == folded {
					return nil
				}
				return texts(s.Table.Seats[i].Cards)
			})
			if got := boardRow(lines, t2.Board); !slices.Equal(got, texts(t2.Board)) {
				t.Errorf("%s: board row %v, want %v", label, got, texts(t2.Board))
			}
		}
	}
}

// resultSnap is the 9-seat table after a hand that everyone folded to
// winner. The board has three cards; rabbit holds the rabbit-hunt cards.
// The winner revealed both cards; the viewer keeps the own cards.
func resultSnap(my, winner int, rabbit []cards.Card) server.Snapshot {
	s := stabSnap(my)
	t := &s.Table
	t.InHand, t.Street = false, engine.Waiting
	t.Board = t.Board[:3]
	for i := range t.Seats {
		sv := &t.Seats[i]
		sv.InHand, sv.Folded, sv.LastAction = false, false, ""
		if i != my {
			sv.Cards, sv.HasCards = nil, false
		}
	}
	t.Result = &engine.Result{HandNumber: 42, Board: t.Board, NoShowdown: true, Rabbit: rabbit,
		Winnings: []engine.Winning{{Seat: winner, PlayerID: "key-" + stabNames[winner], Name: stabNames[winner], Amount: 900}},
		Revealed: []engine.Shown{{Seat: winner, PlayerID: "key-" + stabNames[winner], Name: stabNames[winner], Cards: parseCards(stabHoles[winner])}},
	}
	s.NextHandAt = previewNow.Add(6 * time.Second)
	return s
}

// After a hand: the winner's shown cards draw on the winner's seat only,
// the viewer keeps the own cards, and rabbit-hunt cards draw only in the
// rabbit style (a dashed edge) with the label "rabbit · not dealt".
func TestDrawnCardsResultAndRabbit(t *testing.T) {
	rabbit := stabBoard[3:]
	for my := -1; my < 9; my++ {
		winner := (max(my, 0) + 3) % 9
		for _, sz := range stabSizes {
			s := resultSnap(my, winner, rabbit)
			label := fmt.Sprintf("result, MySeat %d, winner %d at %dx%d", my, winner, sz[0], sz[1])
			lines := screenLines(t, s, sz[0], sz[1])
			checkSeats(t, label, lines, s, func(i int) []string {
				if i == winner || i == my {
					return texts(parseCards(stabHoles[i]))
				}
				return nil
			})
			if got := boardRow(lines, s.Table.Board); !slices.Equal(got, texts(s.Table.Board)) {
				t.Errorf("%s: board row %v, want %v", label, got, texts(s.Table.Board))
			}
			checkRabbit(t, label, lines, rabbit)
		}
	}
}

// checkRabbit checks that each rabbit card shows once, right after a
// dashed edge, and that the felt says that nobody dealt it.
func checkRabbit(t *testing.T, label string, lines [][]rune, rabbit []cards.Card) {
	t.Helper()
	screen := ""
	for _, l := range lines {
		screen += string(l) + "\n"
	}
	for _, c := range texts(rabbit) {
		if n := strings.Count(screen, c); n != 1 {
			t.Errorf("%s: rabbit card %s shows %d times, want 1", label, c, n)
		}
		if !strings.Contains(screen, "┆"+c) {
			t.Errorf("%s: rabbit card %s has no dashed edge", label, c)
		}
	}
	if !strings.Contains(screen, rabbitLabel) {
		t.Errorf("%s: no %q label\n%s", label, rabbitLabel, screen)
	}
}

// msgsOf runs cmd and returns its messages, with batches expanded.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, msgsOf(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// clears reports whether cmd clears the screen.
func clears(cmd tea.Cmd) bool {
	want := fmt.Sprintf("%T", tea.ClearScreen())
	for _, m := range msgsOf(cmd) {
		if fmt.Sprintf("%T", m) == want {
			return true
		}
	}
	return false
}

// handSteps returns the snapshots of one hand for the viewer in seat my:
// preflop, flop, turn, river, the result after everyone folded to
// winner, and the result with rabbit-hunt cards.
func handSteps(my, winner, hand int, holes [9]string) []server.Snapshot {
	board := parseCards("9h 8c 7s 6d 5h")
	base := stabSnap(my)
	base.Table.HandNumber = hand
	for i := range base.Table.Seats {
		sv := &base.Table.Seats[i]
		sv.Cards = nil
		if i == my {
			sv.Cards = parseCards(holes[i])
		}
	}
	base.Log = []string{fmt.Sprintf("Hand #%d — %s has the button", hand, stabNames[0])}
	var out []server.Snapshot
	for _, n := range []int{0, 3, 4, 5} {
		s := base
		s.Table.Seats = slices.Clone(base.Table.Seats)
		s.Table.Board = slices.Clone(board[:n])
		s.Table.Street = []engine.Street{engine.Preflop, engine.Preflop, engine.Preflop, engine.Flop, engine.Turn, engine.River}[n]
		out = append(out, s)
	}
	res := resultSnap(my, winner, nil)
	res.Table.HandNumber, res.Table.Result.HandNumber = hand, hand
	res.Table.Result.CanRabbit = true
	if my >= 0 {
		res.Table.Seats[my].Cards = parseCards(holes[my])
	}
	res.Log = append(slices.Clone(base.Log), fmt.Sprintf("%s wins 900", stabNames[winner]))
	out = append(out, res)
	hunt := res
	hunt.Table.Result = new(engine.Result)
	*hunt.Table.Result = *res.Table.Result
	hunt.Table.Result.CanRabbit = false
	hunt.Table.Result.Rabbit = slices.Clone(board[3:])
	hunt.Log = append(slices.Clone(res.Log), fmt.Sprintf("Rabbit hunt (%s): [%s] [%s]", stabNames[(winner+1)%9], cardText(board[3]), cardText(board[4])))
	return append(out, hunt)
}

// ownCards returns the card texts in the viewer's seat box.
func ownCards(t *testing.T, h *harness, w, ht int) []string {
	t.Helper()
	var lines [][]rune
	for _, l := range strings.Split(stripANSI(h.viewAt(w, ht)), "\n") {
		lines = append(lines, []rune(l))
	}
	box, ok := seatBoxText(lines, h.m.snap.Me.Name)
	if !ok {
		t.Fatalf("no box for the viewer %s at %dx%d", h.m.snap.Me.Name, w, ht)
	}
	return cardTexts(box)
}

// TestDealSequence feeds the snapshots of one hand and then the next hand.
// Within one hand number the viewer's drawn cards never change. During the
// result they are dim and say "last hand". The next deal clears the
// screen, shows a short notice and puts "── Hand #N ──" in the log.
func TestDealSequence(t *testing.T) {
	const my, winner = 2, 5
	next := stabHoles
	next[my] = "Ah Kh"
	for _, sz := range [][2]int{{120, 30}, {162, 51}, {80, 24}} {
		w, ht := sz[0], sz[1]
		steps := handSteps(my, winner, 42, stabHoles)
		h := newHarness(t, Fixture{Snap: steps[0]})
		want := texts(parseCards(stabHoles[my]))
		for i, s := range steps {
			cmd := h.m.applySnapshot(s)
			if i > 0 && clears(cmd) {
				t.Errorf("%dx%d step %d: the screen clears inside one hand", w, ht, i)
			}
			got := ownCards(t, h, w, ht)
			if !slices.Equal(got, want) {
				t.Fatalf("%dx%d step %d: own cards %v, want %v", w, ht, i, got, want)
			}
			raw := h.viewAt(w, ht)
			view := stripANSI(raw)
			result := s.Table.Result != nil && !s.Table.InHand
			if result != strings.Contains(view, "last hand") {
				t.Errorf("%dx%d step %d (result %v): \"last hand\" label wrong\n%s", w, ht, i, result, view)
			}
			if !result && w >= fullW && !strings.Contains(view, "Hand #42") {
				t.Errorf("%dx%d step %d: no hand number near the cards\n%s", w, ht, i, view)
			}
			if dim := ownDim(h, parseCards(stabHoles[my]), raw); dim != result {
				t.Errorf("%dx%d step %d: own cards dim %v, want %v", w, ht, i, dim, result)
			}
			if result && !strings.Contains(view, "next hand in") {
				t.Errorf("%dx%d step %d: no countdown to the next hand\n%s", w, ht, i, view)
			}
		}
		h.m.tab = tabLog
		if v := stripANSI(h.viewAt(w, ht)); !strings.Contains(v, "Rabbit hunt ("+stabNames[winner+1]+")") {
			t.Errorf("%dx%d: the log does not name the rabbit hunter\n%s", w, ht, v)
		}
		h.m.tab = tabChat

		// The next deal.
		deal := handSteps(my, winner, 43, next)[0]
		deal.Log = append(slices.Clone(steps[len(steps)-1].Log), deal.Log...)
		if !clears(h.m.applySnapshot(deal)) {
			t.Errorf("%dx%d: a new hand number does not clear the screen", w, ht)
		}
		if got, want := ownCards(t, h, w, ht), texts(parseCards(next[my])); !slices.Equal(got, want) {
			t.Errorf("%dx%d: new hand cards %v, want %v", w, ht, got, want)
		}
		v := stripANSI(h.viewAt(w, ht))
		if !strings.Contains(v, "Hand #43 · new cards") {
			t.Errorf("%dx%d: no new-cards notice\n%s", w, ht, v)
		}
		if strings.Contains(v, "last hand") || strings.Contains(v, cardText(stabBoard[0])) {
			t.Errorf("%dx%d: the old hand shows after the deal\n%s", w, ht, v)
		}
		h.now = h.now.Add(newDealFor + time.Second)
		h.m.Update(tickMsg(h.now))
		if v := stripANSI(h.viewAt(w, ht)); strings.Contains(v, "new cards") {
			t.Errorf("%dx%d: the new-cards notice stays\n%s", w, ht, v)
		}
		h.m.tab = tabLog
		v = stripANSI(h.viewAt(w, ht))
		for _, d := range []string{"── Hand #42 ──", "── Hand #43 ──"} {
			if !strings.Contains(v, d) && (ht >= 30 || strings.Contains(d, "43")) {
				t.Errorf("%dx%d: no log divider %q\n%s", w, ht, d, v)
			}
		}
	}
}

// ownDim reports whether the view draws the viewer's cards in the dim
// style of the last hand.
func ownDim(h *harness, own []cards.Card, raw string) bool {
	st := h.m.st
	for _, c := range own {
		box := cardText(c)
		if len([]rune(box)) < 3 {
			box += " "
		}
		forms := []string{box, " " + cardText(c) + " ", cardText(c)} // box, chip, tight chip
		if !slices.ContainsFunc(forms, func(f string) bool { return strings.Contains(raw, st.faint.Render(f)) }) {
			return false
		}
	}
	return true
}

// A spectator and a seat that holds no cards get no notice at a deal, but
// the screen still clears.
func TestDealNoticeOnlyWithNewCards(t *testing.T) {
	for _, my := range []int{-1, 2} {
		steps := handSteps(my, 5, 42, stabHoles)
		h := newHarness(t, Fixture{Snap: steps[len(steps)-1]})
		deal := handSteps(my, 5, 43, stabHoles)[0]
		if my >= 0 {
			deal.Table.Seats[my].Cards, deal.Table.Seats[my].HasCards = nil, false
		}
		if !clears(h.m.applySnapshot(deal)) {
			t.Errorf("MySeat %d: no clear at the deal", my)
		}
		if h.m.notice.text != "" {
			t.Errorf("MySeat %d: notice %q, want none", my, h.m.notice.text)
		}
	}
	// An error notice is not replaced.
	steps := handSteps(2, 5, 42, stabHoles)
	h := newHarness(t, Fixture{Snap: steps[len(steps)-1]})
	h.m.flash("it is not your turn", true)
	h.m.applySnapshot(handSteps(2, 5, 43, stabHoles)[0])
	if h.m.notice.text != "it is not your turn" {
		t.Errorf("notice %q, want the error notice", h.m.notice.text)
	}
}

// The countdown to the next deal shows on the felt in every result: one
// winner with the rabbit hunt open, two winners, and a hunted rabbit.
func TestResultKeepsCountdown(t *testing.T) {
	one := resultSnap(2, 5, nil)
	one.Table.Result.CanRabbit = true
	two := resultSnap(2, 5, nil)
	two.Table.Result.NoShowdown = false
	two.Table.Result.Revealed = nil
	two.Table.Result.Winnings = append(two.Table.Result.Winnings,
		engine.Winning{Seat: 6, PlayerID: "key-Gia", Name: "Gia", Amount: 450, Hand: "Straight, Nine high"})
	two.Table.Result.Winnings[0].Hand = "Straight, Nine high"
	hunted := resultSnap(2, 5, stabBoard[3:])
	for name, s := range map[string]server.Snapshot{"one winner, H": one, "two winners": two, "rabbit": hunted} {
		for _, sz := range append(stabSizes, [2]int{100, 30}, [2]int{60, 20}) {
			lines := screenLines(t, s, sz[0], sz[1])
			found := false
			for _, l := range lines {
				if strings.Contains(string(l), "RESULT") || strings.Contains(string(l), "next hand in") {
					found = found || strings.Contains(string(l), "next hand in")
				}
			}
			if !found {
				t.Errorf("%s at %dx%d: the felt has no \"next hand in\"", name, sz[0], sz[1])
			}
		}
	}
}

// The hint marks only the cards that make the hand: for a Pair on the
// flop, the two paired cards. The card edge stays the same on every
// street and in the result.
func TestHintMarksOnlyTheHand(t *testing.T) {
	h := newHarness(t, Fixture{Snap: liveHand(parseCards("Kh 7c"), parseCards("Kd 2s 9c"))})
	hl := h.m.highlights()
	for _, c := range parseCards("Kh Kd") {
		if hl[c] != hlHint {
			t.Errorf("%s makes the pair but has no mark", cardText(c))
		}
	}
	for _, c := range parseCards("7c 2s 9c") {
		if hl[c] != hlNone {
			t.Errorf("%s is a kicker but has a mark", cardText(c))
		}
	}
	for _, s := range handSteps(2, 5, 42, [9]string{2: "Kh Kd"}) {
		h := newHarness(t, Fixture{Snap: s})
		for _, sz := range [][2]int{{120, 30}, {188, 55}} {
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			if strings.ContainsAny(v, "┏┃┗") {
				t.Errorf("%v at %dx%d: a heavy card edge\n%s", s.Table.Street, sz[0], sz[1], v)
			}
			if !strings.Contains(v, "┌───┐ ┌───┐") {
				t.Errorf("%v at %dx%d: own cards without the normal edge\n%s", s.Table.Street, sz[0], sz[1], v)
			}
		}
	}
}

// madeCards gives the cards of each category.
func TestMadeCards(t *testing.T) {
	cases := []struct{ hand, want string }{
		{"As Kd 9c 7h 2s", ""},
		{"Ks Kd 9c 7h 2s", "Ks Kd"},
		{"Ks Kd 7c 7h 2s", "Ks Kd 7c 7h"},
		{"Ks Kd Kc 7h 2s", "Ks Kd Kc"},
		{"9s 8d 7c 6h 5s", "9s 8d 7c 6h 5s"},
		{"As Js 9s 7s 2s", "As Js 9s 7s 2s"},
		{"Ks Kd Kc 7h 7s", "Ks Kd Kc 7h 7s"},
		{"Ks Kd Kc Kh 2s", "Ks Kd Kc Kh"},
		{"9s 8s 7s 6s 5s", "9s 8s 7s 6s 5s"},
	}
	for _, c := range cases {
		cs := parseCards(c.hand)
		hv, best := cards.Best(cs)
		got := texts(madeCards(hv.Category, best))
		want := texts(parseCards(c.want))
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s (%v): marks %v, want %v", c.hand, hv.Category, got, want)
		}
	}
}

// A rabbit chip keeps its dashed edge in the tight form too, so that the
// colour is never the only mark.
func TestRabbitChipEdge(t *testing.T) {
	st := newStyles(testRenderer())
	for _, tight := range []bool{false, true} {
		v := stripANSI(st.rabbitChip(parseCards("Qc")[0], tight))
		if v != "┆Q♣┆" {
			t.Errorf("tight %v: %q, want %q", tight, v, "┆Q♣┆")
		}
	}
}

// The log draws the first line of each hand as a divider.
func TestHandDivider(t *testing.T) {
	cases := []struct{ in, div, rest string }{
		{"Hand #12 — Alice has the button", "── Hand #12 ──", "Alice has the button"},
		{"Hand #12 ends", "", ""},
		{"Hand #x — Alice", "", ""},
		{"Bob: Hand #3 — no", "", ""},
	}
	for _, c := range cases {
		div, rest, ok := handDivider(c.in)
		if ok != (c.div != "") || div != c.div || rest != c.rest {
			t.Errorf("handDivider(%q) = %q, %q, %v", c.in, div, rest, ok)
		}
	}
}
