package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch8Sizes are the terminal sizes of the batch 8 checks: full mode
// (hana plays at 120x30) and compact mode.
var batch8Sizes = [][2]int{{100, 30}, {120, 30}, {140, 40}, {188, 55}, {80, 24}, {60, 20}}

// footerLine returns line i of the footer (0 is the status line).
func footerLine(t *testing.T, view string, w, h, i int) string {
	t.Helper()
	lines := strings.Split(stripANSI(view), "\n")
	n := 2
	if w >= fullW && h >= fullH {
		n = 3
	}
	if len(lines) != h {
		t.Fatalf("view has %d lines, want %d", len(lines), h)
	}
	return lines[h-n+i]
}

// ---- start key (G) ----

func TestStartKeyCallsStart(t *testing.T) {
	for _, k := range []string{"g", "G"} {
		h := newHarness(t, fixture(t, "waiting to start, seated"))
		h.press(k)
		h.wantCalls("Start")
	}
}

// A spectator may press G too: the room answers ErrStartNotSeated.
func TestStartKeySpectatorGetsServerError(t *testing.T) {
	h := newHarness(t, fixture(t, "waiting to start, spectator"))
	h.sess.err = server.ErrStartNotSeated
	h.press("g")
	h.wantCalls("Start")
	for _, sz := range batch8Sizes {
		if got := footerLine(t, h.viewAt(sz[0], sz[1]), sz[0], sz[1], 0); !strings.Contains(got, server.ErrStartNotSeated.Error()) {
			t.Errorf("%v: status line = %q, want the ErrStartNotSeated text", sz, got)
		}
	}
}

// The error text of a failed start shows on the status line, and the
// status line goes back to the waiting text after the notice time.
func TestStartErrorOnStatusLine(t *testing.T) {
	h := newHarness(t, fixture(t, "waiting to start, seated"))
	h.sess.err = server.ErrStartNeedsTwo
	h.press("g")
	for _, sz := range batch8Sizes {
		got := footerLine(t, h.viewAt(sz[0], sz[1]), sz[0], sz[1], 0)
		if !strings.Contains(got, server.ErrStartNeedsTwo.Error()) {
			t.Errorf("%v: status line = %q, want the ErrStartNeedsTwo text", sz, got)
		}
	}
	if h.m.notice.text != "" {
		t.Errorf("the error shows twice: notice %q", h.m.notice.text)
	}
	h.now = h.now.Add(noticeFor + time.Second)
	h.m.Update(tickMsg(h.now))
	if got := footerLine(t, h.viewAt(100, 30), 100, 30, 0); !strings.Contains(got, "Waiting to start") {
		t.Errorf("after the notice time, status line = %q", got)
	}
}

// G types in every text input and does nothing in the dashboard.
func TestStartKeyNotInInputs(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		mode mode
	}{
		{"chat", []string{"t", "g", "G"}, modeChat},
		{"slash chat", []string{"/", "g"}, modeChat},
		{"rename", []string{"m", "r", "g"}, modeRename},
		{"dashboard", []string{"d", "g"}, modeDash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, fixture(t, "waiting to start, seated"))
			h.press(c.keys...)
			h.wantCalls()
			if h.m.mode != c.mode {
				t.Errorf("mode = %v, want %v", h.m.mode, c.mode)
			}
		})
	}
	h := newHarness(t, fixture(t, "waiting to start, seated"))
	h.press("t", "g", "g")
	if string(h.m.chatBuf) != "gg" {
		t.Errorf("chat buffer = %q, want gg", string(h.m.chatBuf))
	}
	h.press("enter")
	h.wantCalls("Chat(gg)")
}

// While the game runs, G sends nothing and says why.
func TestStartKeyWhileGameRuns(t *testing.T) {
	for _, name := range []string{"6 seats, flop", "started by Priya", "busted, between hands"} {
		h := newHarness(t, fixture(t, name))
		h.press("g")
		h.wantCalls()
		if h.m.notice.text != startedNotice || h.m.notice.err {
			t.Errorf("%s: notice = %+v", name, h.m.notice)
		}
	}
}

func TestWaitingStatusLines(t *testing.T) {
	cases := []struct{ fixture, want string }{
		{"waiting to start, seated", "Waiting to start · 3 seated · press G when everyone is in"},
		{"waiting to start, spectator", "Sit down with S. A seated player starts with G."},
		{"started by Priya", "Priya started the game · first hand in 3s"},
	}
	for _, c := range cases {
		for _, sz := range batch8Sizes {
			v := Render(fixture(t, c.fixture), testRenderer(), sz[0], sz[1])
			if got := footerLine(t, v, sz[0], sz[1], 0); !strings.Contains(got, c.want) {
				t.Errorf("%s at %v: status line = %q, want %q", c.fixture, sz, got, c.want)
			}
		}
	}
}

// The footer and the help show G only when it is useful: the table waits
// and the viewer is seated.
func TestStartHintOnlyWhenUseful(t *testing.T) {
	cases := []struct {
		fixture string
		want    bool
	}{
		{"waiting to start, seated", true},
		{"waiting to start, spectator", false},
		{"started by Priya", false},
		{"6 seats, flop", false},
		{"busted, between hands", false},
	}
	for _, c := range cases {
		for _, sz := range batch8Sizes {
			v := Render(fixture(t, c.fixture), testRenderer(), sz[0], sz[1])
			hints := footerLine(t, v, sz[0], sz[1], -1+map[bool]int{true: 3, false: 2}[sz[0] >= fullW && sz[1] >= fullH])
			if got := strings.Contains(hints, "G start"); got != c.want {
				t.Errorf("%s at %v: hints %q, G shown = %v, want %v", c.fixture, sz, hints, got, c.want)
			}
		}
		h := newHarness(t, fixture(t, c.fixture))
		h.press("?")
		help := stripANSI(h.viewAt(100, 30))
		if got := strings.Contains(help, "start the game"); got != c.want {
			t.Errorf("%s: help names G = %v, want %v", c.fixture, got, c.want)
		}
	}
}

// The help with G (and with E off while the table waits) still fits in
// 80x24, and names I.
func TestBatch8HelpFits(t *testing.T) {
	for _, name := range []string{"waiting to start, seated", "waiting to start, spectator", "heads-up"} {
		for _, sz := range append(batch8Sizes, [2]int{80, 24}) {
			h := newHarness(t, fixture(t, name))
			h.press("?")
			view := h.viewAt(sz[0], sz[1])
			wantFits(t, name+" help", view, sz[0], sz[1])
			v := stripANSI(view)
			if sz[1] < 24 {
				continue
			}
			for _, want := range []string{"Keys", "Q / Ctrl+C", "hand rankings", "╰"} {
				if !strings.Contains(v, want) {
					t.Errorf("%s help at %v lacks %q\n%s", name, sz, want, v)
				}
			}
		}
	}
}

func TestWaitingFeltText(t *testing.T) {
	cases := []struct{ fixture, want string }{
		{"waiting to start, seated", "3 ready · G to start"},
		{"waiting to start, spectator", "3 ready · not started"},
	}
	for _, c := range cases {
		v := stripANSI(Render(fixture(t, c.fixture), testRenderer(), 140, 40))
		if !strings.Contains(v, c.want) {
			t.Errorf("%s: felt lacks %q\n%s", c.fixture, c.want, v)
		}
		if strings.Contains(v, "of 2 ready") {
			t.Errorf("%s: felt says 'of 2 ready' with 3 players\n%s", c.fixture, v)
		}
	}
}

// ---- hand rankings box ----

// boxLines returns the stripped lines inside the rankings box of a full
// mode view: from "HAND RANKINGS" to the bottom border.
func boxLines(view string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(stripANSI(view), "\n") {
		l = strings.TrimRight(l, " ")
		if strings.Contains(l, "│ HAND RANKINGS") {
			in = true
		}
		if !in {
			continue
		}
		if strings.HasSuffix(l, "╯") {
			break
		}
		right := strings.LastIndex(l, "│")
		left := strings.LastIndex(l[:right], "│")
		out = append(out, strings.TrimSpace(l[left+len("│"):right]))
	}
	return out
}

// The box lists the 10 hands exactly as DESIGN.md does, best first.
func TestRankingsRows(t *testing.T) {
	want := []struct{ name, example string }{
		{"Royal flush", "A K Q J T, one suit"},
		{"Straight flush", "9 8 7 6 5, one suit"},
		{"Four of a kind", "Q Q Q Q"},
		{"Full house", "K K K 7 7"},
		{"Flush", "any 5, one suit"},
		{"Straight", "8 7 6 5 4"},
		{"Three of a kind", "8 8 8"},
		{"Two pair", "J J 4 4"},
		{"Pair", "A A"},
		{"High card", "none of the above"},
	}
	m := newHarness(t, fixture(t, "rankings box, spectator")).m
	for _, l := range []ranksLayout{ranksInline, ranksStacked, ranksPlain} {
		lines := m.ranksLines(42, l)
		for i := range lines {
			lines[i] = stripANSI(lines[i])
		}
		if lines[0] != "HAND RANKINGS" {
			t.Errorf("layout %d: title %q", l, lines[0])
		}
		rows := lines[2:]
		step := 1
		if l == ranksStacked {
			step = 2
		}
		if len(rows) != 10*step {
			t.Fatalf("layout %d: %d row lines, want %d", l, len(rows), 10*step)
		}
		for i, w := range want {
			row := rows[i*step]
			head := fmt.Sprintf(" %2d %s", i+1, w.name)
			if !strings.HasPrefix(row, head) {
				t.Errorf("layout %d row %d = %q, want prefix %q", l, i+1, row, head)
			}
			var ex string
			switch l {
			case ranksInline:
				ex = strings.TrimSpace(strings.TrimPrefix(row, head))
			case ranksStacked:
				ex = strings.TrimSpace(rows[i*step+1])
			}
			if l != ranksPlain && ex != w.example {
				t.Errorf("layout %d row %d example = %q, want %q", l, i+1, ex, w.example)
			}
			if strings.ContainsAny(row, "♠♥♦♣") {
				t.Errorf("row %d has a suit symbol: %q", i+1, row)
			}
		}
	}
}

// liveHand returns a heads-up snapshot where the viewer holds hole and
// the board holds board.
func liveHand(hole, board []cards.Card) server.Snapshot {
	s := ranksHand("2c 3d", "")
	s.Table.Seats = append([]engine.SeatView(nil), s.Table.Seats...)
	s.Table.Seats[0].Cards = hole
	s.Table.Board = board
	return s
}

// hintRow returns the rankings row that a hint text names.
func hintRow(t *testing.T, hint string, preflop bool) int {
	t.Helper()
	if preflop {
		if strings.HasPrefix(hint, "Pocket ") {
			return 8
		}
		return 9
	}
	prefixes := []string{"Royal Flush", "Straight Flush", "Four of a Kind", "Full House", "Flush",
		"Straight", "Three of a Kind", "Two Pair", "Pair of", "High Card"}
	for i, p := range prefixes {
		if strings.HasPrefix(hint, p) {
			return i
		}
	}
	t.Fatalf("hint %q names no hand", hint)
	return -1
}

// The marked row always names the same hand as the best-hand hint, for
// many random hole cards and boards at each street.
func TestRankingsMarkMatchesHint(t *testing.T) {
	rng := rand.New(rand.NewPCG(8, 2026))
	deck := cards.FullDeck()
	seen := map[int]bool{}
	for n := 0; n < 3000; n++ {
		rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
		for _, nb := range []int{0, 3, 4, 5} {
			hole := append([]cards.Card(nil), deck[:2]...)
			board := append([]cards.Card(nil), deck[2:2+nb]...)
			h := newHarness(t, Fixture{Snap: liveHand(hole, board)})
			hint := stripANSI(h.m.handHint(60))
			row := h.m.myRankRow()
			if want := hintRow(t, hint, nb == 0); row != want {
				t.Fatalf("hole %v board %v: hint %q, marked row %d, want %d", hole, board, hint, row+1, want+1)
			}
			if nb > 0 {
				hv, _ := cards.Best(append(append([]cards.Card(nil), hole...), board...))
				want := int(cards.StraightFlush-hv.Category) + 1
				if hv.Category == cards.StraightFlush && hv.Kickers[0] == cards.Ace {
					want = 0
				}
				if row != want {
					t.Fatalf("hole %v board %v: row %d, evaluator says %d", hole, board, row+1, want+1)
				}
			}
			seen[row] = true
			if n%200 == 0 {
				// The drawn box agrees with the row: one mark, on that row,
				// and the "You:" line names it.
				lines := h.m.ranksLines(42, ranksInline)
				marks := 0
				for i, l := range lines[2:] {
					if strings.Contains(stripANSI(l), "▶") {
						marks++
						if i != row {
							t.Fatalf("mark on row %d, want %d", i+1, row+1)
						}
					}
				}
				if marks != 1 || stripANSI(lines[1]) != "You: "+rankRows[row].you {
					t.Fatalf("marks %d, you line %q", marks, stripANSI(lines[1]))
				}
			}
		}
	}
	for _, r := range []int{3, 4, 5, 6, 7, 8, 9} {
		if !seen[r] {
			t.Errorf("no random hand hit row %d; the test does not cover it", r+1)
		}
	}
}

// Rare rows: royal flush, straight flush and four of a kind.
func TestRankingsMarkRareHands(t *testing.T) {
	cases := []struct {
		hole, board string
		row         int
		you         string
	}{
		{"Ah Kh", "Qh Jh Th 2c 3d", 0, "Royal Flush"},
		{"9s 8s", "7s 6s 5s Kd 2c", 1, "Straight Flush"},
		{"Qs Qd", "Qh Qc 2s", 2, "Four of a Kind"},
		{"Ks Kd", "Kh 7c 7s", 3, "Full House"},
		{"Ks Kd", "", 8, "Pair"},
		{"Ks 2d", "", 9, "High Card"},
	}
	for _, c := range cases {
		h := newHarness(t, Fixture{Snap: liveHand(parseCards(c.hole), parseCards(c.board))})
		if got := h.m.myRankRow(); got != c.row {
			t.Errorf("%s | %s: row %d, want %d", c.hole, c.board, got+1, c.row+1)
		}
		if v := stripANSI(h.viewAt(140, 40)); !strings.Contains(v, "You: "+c.you+" ") {
			t.Errorf("%s | %s: no 'You: %s' line\n%s", c.hole, c.board, c.you, v)
		}
	}
}

// No mark and no "You:" line without live hole cards of the viewer's own.
func TestRankingsNoMarkWithoutLiveCards(t *testing.T) {
	cases := map[string]Fixture{
		"spectator":         fixture(t, "rankings box, spectator"),
		"waiting":           fixture(t, "waiting to start, seated"),
		"busted":            fixture(t, "busted, between hands"),
		"showdown result":   fixture(t, "9 seats, showdown result"),
		"folded":            edited(t, "rankings box, river, two pair", func(s *server.Snapshot) { s.Table.Seats[0].Folded = true }),
		"no cards":          edited(t, "rankings box, river, two pair", func(s *server.Snapshot) { s.Table.Seats[0].Cards = nil }),
		"hand over":         edited(t, "rankings box, river, two pair", func(s *server.Snapshot) { s.Table.InHand, s.Table.Legal = false, nil }),
		"run-out folded me": fixture(t, "run-out, I folded"),
	}
	// A spectator next to face-up cards of other players: no mark.
	cases["spectator, cards face up"] = edited(t, "rankings box, spectator", func(s *server.Snapshot) {
		for i := range s.Table.Seats {
			if !s.Table.Seats[i].Empty {
				s.Table.Seats[i].Cards = parseCards("Ah Ad")
			}
		}
	})
	for name, f := range cases {
		h := newHarness(t, f)
		if r := h.m.myRankRow(); r != -1 {
			t.Errorf("%s: marked row %d", name, r+1)
		}
		v := strings.Join(boxLines(h.viewAt(140, 40)), "\n")
		if !strings.Contains(v, "HAND RANKINGS") {
			t.Errorf("%s: no box at 140x40", name)
		}
		if strings.Contains(v, "▶") || strings.Contains(v, "You:") {
			t.Errorf("%s: box has a mark or a You line\n%s", name, v)
		}
	}
}

// The box never reads another player's cards: the viewer's row stays the
// same when the other seats hold any cards.
func TestRankingsIgnoresOtherCards(t *testing.T) {
	f := fixture(t, "rankings box, river, two pair")
	base := newHarness(t, f).m.myRankRow()
	g := edited(t, "rankings box, river, two pair", func(s *server.Snapshot) { s.Table.Seats[1].Cards = parseCards("Ah Qh") })
	if got := newHarness(t, g).m.myRankRow(); got != base || base != 7 {
		t.Errorf("row %d with Priya's cards, %d without; want 8", got+1, base+1)
	}
}

// The full-mode layout: exact line counts, nothing wider than the
// terminal, the panel at 8 lines or more, and examples only with room.
func TestRankingsLayout(t *testing.T) {
	cases := []struct {
		w, h     int
		examples bool
	}{
		{100, 30, false}, {120, 30, false}, {140, 40, true}, {188, 55, true}, {100, 40, true}, {120, 34, false},
	}
	for _, name := range []string{"rankings box, river, two pair", "rankings box, preflop, Pair", "rankings box, spectator", "waiting to start, seated"} {
		for _, c := range cases {
			view := Render(fixture(t, name), testRenderer(), c.w, c.h)
			wantFits(t, name, view, c.w, c.h)
			if n := len(strings.Split(view, "\n")); n != c.h {
				t.Errorf("%s at %dx%d: %d lines", name, c.w, c.h, n)
			}
			v := stripANSI(view)
			if !strings.Contains(v, "HAND RANKINGS") {
				t.Errorf("%s at %dx%d: no box\n%s", name, c.w, c.h, v)
			}
			if got := strings.Contains(v, "none of the above"); got != c.examples {
				t.Errorf("%s at %dx%d: examples shown = %v, want %v\n%s", name, c.w, c.h, got, c.examples, v)
			}
		}
	}
	// Compact mode has no box; the hint under the cards stays.
	for _, sz := range [][2]int{{80, 24}, {60, 20}, {99, 40}, {140, 29}} {
		view := Render(fixture(t, "rankings box, river, two pair"), testRenderer(), sz[0], sz[1])
		wantFits(t, "compact", view, sz[0], sz[1])
		v := stripANSI(view)
		if strings.Contains(v, "HAND RANKINGS") {
			t.Errorf("%v: compact mode draws the box", sz)
		}
		if !strings.Contains(v, "Two Pair") && !strings.Contains(v, "2 pair") {
			t.Errorf("%v: compact mode lost the hint\n%s", sz, v)
		}
	}
}

// rightColumn always fills h lines. The box drops the examples, then
// hides, so that the panel keeps 8 lines or more.
func TestRightColumnShrinks(t *testing.T) {
	m := newHarness(t, fixture(t, "rankings box, river, two pair")).m
	for _, w := range []int{30, 36, 42, 46} {
		for h := 10; h <= 60; h++ {
			col := m.rightColumn(w, h)
			lines := strings.Split(col, "\n")
			if len(lines) != h {
				t.Fatalf("w %d h %d: %d lines", w, h, len(lines))
			}
			for i, l := range lines {
				if lw := len([]rune(stripANSI(l))); lw > w {
					t.Fatalf("w %d h %d: line %d is %d wide", w, h, i, lw)
				}
			}
			v := stripANSI(col)
			box := strings.Contains(v, "HAND RANKINGS")
			examples := strings.Contains(v, "none of the above")
			full := 14
			if !ranksInlineFits(w - 4) {
				full = 24
			}
			switch {
			case h-minPanelH >= full:
				if !box || !examples {
					t.Errorf("w %d h %d: want the box with examples", w, h)
				}
			case h-minPanelH >= 14:
				if !box || examples {
					t.Errorf("w %d h %d: want the box without examples", w, h)
				}
			default:
				if box {
					t.Errorf("w %d h %d: want no box", w, h)
				}
			}
			if box {
				// The panel ends at the line before the box top border.
				top := strings.Index(v, "│ HAND RANKINGS")
				panelLines := strings.Count(v[:top], "\n") - 1
				if panelLines < minPanelH {
					t.Errorf("w %d h %d: panel has %d lines", w, h, panelLines)
				}
			}
		}
	}
}

// I hides and shows the box for this session; the panel then takes the
// whole height. In a text input, I types.
func TestRankingsToggle(t *testing.T) {
	h := newHarness(t, fixture(t, "rankings box, river, two pair"))
	if !strings.Contains(stripANSI(h.viewAt(100, 30)), "HAND RANKINGS") {
		t.Fatal("the box must show by default")
	}
	h.press("i")
	v := stripANSI(h.viewAt(100, 30))
	if strings.Contains(v, "HAND RANKINGS") {
		t.Error("I must hide the box")
	}
	if !strings.Contains(h.m.notice.text, "Hand rankings off") {
		t.Errorf("notice = %q", h.m.notice.text)
	}
	if col := stripANSI(h.m.rightColumn(30, 26)); strings.Count(col, "╭") != 1 || strings.Count(col, "\n") != 25 {
		t.Errorf("hidden box: the panel must fill the 26 lines\n%s", col)
	}
	h.press("I")
	if !strings.Contains(stripANSI(h.viewAt(100, 30)), "HAND RANKINGS") {
		t.Error("a second I must show the box")
	}
	h.press("t", "i")
	if h.m.hideRanks || string(h.m.chatBuf) != "i" {
		t.Errorf("I in the chat input: hidden %v, buffer %q", h.m.hideRanks, string(h.m.chatBuf))
	}
	h.press("esc")
	h.m.width, h.m.height = 80, 24
	h.press("i")
	if !strings.Contains(h.m.notice.text, "100x30") {
		t.Errorf("compact notice = %q", h.m.notice.text)
	}
	h.wantCalls()
}

// The chat input works under the shorter panel, the panel shows the newest
// line, and the unread mark still shows on the Chat tab.
func TestChatWithRankingsBox(t *testing.T) {
	for _, sz := range [][2]int{{100, 30}, {120, 30}, {140, 40}, {188, 55}} {
		h := newHarness(t, fixture(t, "rankings box, river, two pair"))
		h.m.width, h.m.height = sz[0], sz[1]
		h.press("t", "h", "i", " ", "a", "l", "l")
		v := stripANSI(h.m.View())
		if !strings.Contains(v, "Say ›") || !strings.Contains(v, "hi all") || !strings.Contains(v, "HAND RANKINGS") {
			t.Errorf("%v: chat input or box missing\n%s", sz, v)
		}
		h.press("enter")
		h.wantCalls("Chat(hi all)")
		if !strings.Contains(v, "brb coffee") {
			t.Errorf("%v: the panel lost the newest chat line\n%s", sz, v)
		}
		// A new line on another tab marks the Chat tab.
		h.press("tab")
		s := h.m.snap
		s.Chat = append(append([]server.ChatLine(nil), s.Chat...), server.ChatLine{At: previewNow.Add(time.Second), Name: "Priya", Text: "new one"})
		h.m.Update(snapshotMsg{s})
		if v := stripANSI(h.m.View()); !strings.Contains(v, "Chat•") {
			t.Errorf("%v: no unread mark\n%s", sz, v)
		}
		h.press("shift+tab")
		if v := stripANSI(h.m.View()); !strings.Contains(v, "new one") {
			t.Errorf("%v: the short panel does not show the new line\n%s", sz, v)
		}
	}
}

// Every batch 8 screen fits in every size, with an exact line count.
func TestBatch8StatesFit(t *testing.T) {
	keys := [][]string{nil, {"g"}, {"i"}, {"t", "g"}, {"?"}, {"tab"}, {"tab", "tab"}}
	for _, f := range batch8Fixtures() {
		for _, k := range keys {
			for _, sz := range append(batch8Sizes, [2]int{90, 26}, [2]int{30, 8}) {
				h := newHarness(t, f)
				h.sess.err = server.ErrStartNeedsTwo
				h.press(k...)
				view := h.viewAt(sz[0], sz[1])
				name := fmt.Sprintf("%s %v", f.Name, k)
				wantFits(t, name, view, sz[0], sz[1])
				if n := len(strings.Split(view, "\n")); n != sz[1] {
					t.Errorf("%s at %v: %d lines", name, sz, n)
				}
			}
		}
	}
}

// A start error goes away when the viewer sits down or another player
// sits, because it no longer applies.
func TestStartErrorClearsWhenTableChanges(t *testing.T) {
	h := newHarness(t, fixture(t, "waiting to start, spectator"))
	h.sess.err = server.ErrStartNotSeated
	h.press("g")
	if h.m.startErr.text == "" {
		t.Fatal("precondition: no start error")
	}
	s := fixture(t, "waiting to start, seated").Snap
	h.m.Update(snapshotMsg{s})
	if got := footerLine(t, h.viewAt(100, 30), 100, 30, 0); !strings.Contains(got, "Waiting to start") {
		t.Errorf("after the viewer sat down, status line = %q", got)
	}
	// The same table again keeps a new error.
	h.press("g")
	h.m.Update(snapshotMsg{s})
	if h.m.startErr.text == "" {
		t.Error("a snapshot without a change cleared the start error")
	}
}

// With fewer than 2 players who can play, the table waits for players: G is
// not offered, and the status line says how many are ready.
func TestStartHintNeedsTwoReady(t *testing.T) {
	f := fixture(t, "waiting to start, seated")
	seats := append([]engine.SeatView(nil), f.Snap.Table.Seats...)
	me := f.Snap.Table.MySeat
	for i := range seats {
		if i != me && !seats[i].Empty {
			seats[i].SittingOut = true
		}
	}
	f.Snap.Table.Seats = seats
	if n := readyIn(f.Snap); n != 1 {
		t.Fatalf("ready = %d, want 1", n)
	}
	for _, sz := range batch8Sizes {
		v := Render(f, testRenderer(), sz[0], sz[1])
		full := sz[0] >= fullW && sz[1] >= fullH
		hints := footerLine(t, v, sz[0], sz[1], -1+map[bool]int{true: 3, false: 2}[full])
		if strings.Contains(hints, "G start") {
			t.Errorf("at %v: hints %q offer G with 1 ready", sz, hints)
		}
		if got := footerLine(t, v, sz[0], sz[1], 0); !strings.Contains(got, "Waiting for players · 1 of 2 ready") {
			t.Errorf("at %v: status line = %q", sz, got)
		}
	}
}
