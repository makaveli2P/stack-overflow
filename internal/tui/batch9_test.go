package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch9Sizes are the terminal sizes of the batch 9 checks.
var batch9Sizes = [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 30}, {188, 55}}

// hintsAt returns the key hints line of the view at w x h.
func hintsAt(t *testing.T, view string, w, h int) string {
	t.Helper()
	n := 1
	if w >= fullW && h >= fullH {
		n = 2
	}
	return footerLine(t, view, w, h, n)
}

// ---- during the hand ----

// A folded player stores a show with 1, 2 or 3. The room decides; the TUI
// only sends the key's cards, also when the same key cancels.
func TestShowAtEndKeys(t *testing.T) {
	for _, name := range []string{"folded in the hand, may store", "show queued during the hand, both"} {
		for key, want := range map[string]string{"1": "ShowCards(true,false)", "2": "ShowCards(false,true)", "3": "ShowCards(true,true)"} {
			h := newHarness(t, fixture(t, name))
			h.press(key)
			h.wantCalls(want)
		}
	}
}

// A live player, a spectator and a player who sat down during the hand
// send nothing with 1, 2 or 3 during the hand, and see no "1 2 3" hint.
func TestShowAtEndKeysOnlyWhenFolded(t *testing.T) {
	notDealt := edited(t, "folded in the hand, may store", func(s *server.Snapshot) {
		me := &s.Table.Seats[2]
		me.InHand, me.Folded, me.Cards, me.LastAction = false, false, nil, ""
	})
	for _, f := range []Fixture{fixture(t, "6 seats, flop, waiting"), fixture(t, "spectator"), notDealt} {
		h := newHarness(t, f)
		h.press("1", "2", "3")
		h.wantCalls()
		for _, sz := range batch9Sizes {
			if got := hintsAt(t, h.viewAt(sz[0], sz[1]), sz[0], sz[1]); strings.Contains(got, "1 2 3") {
				t.Errorf("%s at %v: hints %q offer 1 2 3", f.Name, sz, got)
			}
		}
	}
}

// The status line names the stored cards and the key that cancels.
func TestShowAtEndStatusLine(t *testing.T) {
	cases := []struct {
		name, long, short string
	}{
		{"queued during the hand, both", "You will show A♠ K♦ when the hand ends · press 3 again to cancel", "You will show A♠ K♦ at the end · 3 again cancels"},
		{"queued during the hand, left", "You will show A♠ when the hand ends · press 1 again to cancel", "You will show A♠ at the end · 1 again cancels"},
		{"queued during the hand, right", "You will show K♦ when the hand ends · press 2 again to cancel", "You will show K♦ at the end · 2 again cancels"},
	}
	for _, c := range cases {
		f := fixture(t, c.name)
		for _, sz := range batch9Sizes {
			got := footerLine(t, Render(f, testRenderer(), sz[0], sz[1]), sz[0], sz[1], 0)
			want := c.long
			if len([]rune(c.long)) > sz[0] {
				want = c.short
			}
			if !strings.HasPrefix(got, want) {
				t.Errorf("%s at %v: status line %q, want %q", c.name, sz, got, want)
			}
		}
	}
	// No choice: the line says that the viewer folded, and no "will show".
	f := fixture(t, "folded in the hand, may store")
	for _, sz := range batch9Sizes {
		v := Render(f, testRenderer(), sz[0], sz[1])
		if got := footerLine(t, v, sz[0], sz[1], 0); !strings.HasPrefix(got, "You folded.") || strings.Contains(got, "will show") {
			t.Errorf("at %v: status line %q", sz, got)
		}
	}
}

// The footer offers 1 2 3 during the hand only after a fold.
func TestShowAtEndHint(t *testing.T) {
	for _, name := range []string{"folded in the hand, may store", "show queued during the hand, both"} {
		f := fixture(t, name)
		for _, sz := range batch9Sizes {
			if got := hintsAt(t, Render(f, testRenderer(), sz[0], sz[1]), sz[0], sz[1]); !strings.Contains(got, "1 2 3 Show") {
				t.Errorf("%s at %v: hints %q lack 1 2 3 Show", name, sz, got)
			}
		}
	}
}

// The other players' seats never draw the folded player's cards during
// the hand: only the owner's snapshot holds them, and the TUI draws a
// folded seat with no cards.
func TestShowAtEndOwnSeatOnly(t *testing.T) {
	f := fixture(t, "show queued during the hand, both")
	for _, sz := range batch9Sizes {
		v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
		if strings.Contains(v, "folded · shown") {
			t.Errorf("at %v: a seat says folded · shown during the hand", sz)
		}
	}
}

// ---- after the hand ----

// A folded player gets the show prompt after the hand, and 1, 2 and 3 show.
func TestFoldedShowsAfterHand(t *testing.T) {
	for key, want := range map[string]string{"1": "ShowCards(true,false)", "2": "ShowCards(false,true)", "3": "ShowCards(true,true)"} {
		h := newHarness(t, fixture(t, "folded after the hand, may show"))
		h.press(key)
		h.wantCalls(want)
	}
	f := fixture(t, "folded after the hand, may show")
	for _, sz := range batch9Sizes {
		v := Render(f, testRenderer(), sz[0], sz[1])
		if got := footerLine(t, v, sz[0], sz[1], 0); !strings.HasPrefix(got, "You folded. Show your cards?") {
			t.Errorf("at %v: status line %q", sz, got)
		}
		if got := hintsAt(t, v, sz[0], sz[1]); !strings.Contains(got, "1 2 3 Show") {
			t.Errorf("at %v: hints %q", sz, got)
		}
	}
	// One card shown: 3 shows only the other one, and 1 does nothing.
	h := newHarness(t, edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		r := *s.Table.Result
		r.Revealed = []engine.Shown{{Seat: 0, PlayerID: "key-Priya", Name: "Priya", Cards: parseCards("9c")}}
		s.Table.Result = &r
	}))
	h.press("1")
	h.wantCalls()
	h.press("3")
	h.wantCalls("ShowCards(false,true)")
}

// After all cards are shown, or for a player who did not fold and did not
// win without a showdown, there is no prompt and no hint.
func TestFoldedShowNotOffered(t *testing.T) {
	notFolded := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		r := *s.Table.Result
		r.Actions = r.Actions[:2] // Priya's fold is not there
		s.Table.Result = &r
	})
	standUp := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		s.Table.Seats[0].Cards = nil // a new seat: the engine gives no old cards
	})
	for _, f := range []Fixture{fixture(t, "own folded hand shown"), notFolded, standUp} {
		h := newHarness(t, f)
		h.press("1", "2", "3")
		h.wantCalls()
		for _, sz := range batch9Sizes {
			v := h.viewAt(sz[0], sz[1])
			if got := footerLine(t, v, sz[0], sz[1], 0); strings.Contains(got, "Show your cards?") {
				t.Errorf("%s at %v: status line %q", f.Name, sz, got)
			}
			if got := hintsAt(t, v, sz[0], sz[1]); strings.Contains(got, "1 2 3") {
				t.Errorf("%s at %v: hints %q", f.Name, sz, got)
			}
		}
	}
	// The winner of a hand without a showdown still gets the old prompt.
	f := fixture(t, "winner may show")
	for _, sz := range batch9Sizes {
		v := Render(f, testRenderer(), sz[0], sz[1])
		if got := footerLine(t, v, sz[0], sz[1], 0); !strings.HasPrefix(got, "You win. Show your cards?") {
			t.Errorf("winner at %v: status line %q", sz, got)
		}
	}
}

// The seat of a player who folded and showed draws the shown cards with
// the folded mark; the winner's seat keeps "shown".
func TestFoldedShownSeat(t *testing.T) {
	for _, name := range []string{"winner's, Priya", "winner's, spectator", "own folded hand shown"} {
		f := fixture(t, name)
		for _, sz := range batch9Sizes {
			v := stripANSI(Render(f, testRenderer(), sz[0], sz[1]))
			full := sz[0] >= fullW && sz[1] >= fullH
			for _, want := range []string{"A♠", "K♦", "J♥", "J♦", "10♦"} {
				if !strings.Contains(v, want) {
					t.Errorf("%s at %v: no %s\n%s", name, sz, want, v)
				}
			}
			// Devon's seat: the mark is on the cards line, or in full mode
			// on the note line under it. A narrow seat keeps "shown".
			mark := "folded"
			switch {
			case full && sz[0] >= 120:
				mark = "folded · shown"
			case sz[0] < 80:
				mark = "shown"
			}
			lines := strings.Split(v, "\n")
			at, col := -1, -1
			for i, l := range lines {
				if c := strings.Index(l, "│ Devon"); c >= 0 {
					at, col = i, len([]rune(l[:c]))+1
					break
				}
			}
			if at < 0 || at+3 >= len(lines) {
				t.Fatalf("%s at %v: no seat of Devon\n%s", name, sz, v)
			}
			cardsLine, note := seatCell(lines[at+2], col), seatCell(lines[at+3], col)
			if !strings.Contains(cardsLine, "A♠") || !strings.Contains(cardsLine+"\n"+note, mark) {
				t.Errorf("%s at %v: Devon's seat %q / %q, want A♠ and %q\n%s", name, sz, cardsLine, note, mark, v)
			}
			if !strings.Contains(v, "shown") {
				t.Errorf("%s at %v: the winner's seat has no shown mark\n%s", name, sz, v)
			}
			// Priya did not show: only her own screen draws her cards.
			if name == "winner's, spectator" && strings.Contains(v, "9♣") {
				t.Errorf("%s at %v: Priya's cards show, but she did not show them\n%s", name, sz, v)
			}
		}
	}
	// In the full layout the viewer's own box says that the folded hand
	// is shown.
	v := stripANSI(Render(fixture(t, "own folded hand shown"), testRenderer(), 120, 30))
	if !strings.Contains(v, "9♣") || strings.Count(v, "folded · shown") < 3 {
		t.Errorf("own box\n%s", v)
	}
}

// seatCell returns the text of line inside the seat box whose inner text
// starts at rune column col: up to the next box border.
func seatCell(line string, col int) string {
	rs := []rune(line)
	if col >= len(rs) {
		return ""
	}
	rest := string(rs[col:])
	if i := strings.IndexAny(rest, "│┆"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// Every batch 9 screen fits in every size, with an exact line count.
func TestBatch9StatesFit(t *testing.T) {
	keys := [][]string{nil, {"1"}, {"3"}, {"?"}, {"tab"}}
	for _, f := range batch9Fixtures() {
		for _, k := range keys {
			for _, sz := range append(batch9Sizes, [2]int{140, 40}, [2]int{90, 26}, [2]int{30, 8}) {
				h := newHarness(t, f)
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

// The help names the folded show.
func TestHelpNamesFoldedShow(t *testing.T) {
	h := newHarness(t, fixture(t, "folded in the hand, may store"))
	h.press("?")
	if v := stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "after a win or a fold") {
		t.Errorf("help\n%s", v)
	}
}

// ---- review fixes ----

// ownSeatText returns the text of the viewer's own seat box: the line with
// "(you)" and the 5 lines under it, cut to the columns of the box.
func ownSeatText(t *testing.T, v string) string {
	t.Helper()
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		c := strings.Index(l, "(you)")
		if c < 1 {
			continue
		}
		top := []rune(lines[i-1])
		from := len([]rune(l[:strings.LastIndexAny(l[:c], "│┆")]))
		to := from + 1
		for to < len(top) && top[to] != '╮' {
			to++
		}
		var out []string
		for j := i; j < len(lines) && j <= i+5; j++ {
			rs := []rune(lines[j])
			out = append(out, string(rs[min(from, len(rs)):min(to+1, len(rs))]))
		}
		return strings.Join(out, "\n")
	}
	t.Fatalf("no own seat\n%s", v)
	return ""
}

// B9-UX-1: a folded player sees the own cards (dim) while a show is
// possible, during the hand and after it, so that the player knows which
// key shows which card. The prompt names the card of each key.
func TestFoldedOwnCardsFaceUp(t *testing.T) {
	cases := []struct {
		name string
		hole []string
	}{
		{"folded after the hand, may show", []string{"9♣", "8♣"}},
		{"folded in the hand, may store a show", []string{"A♠", "K♦"}},
		{"show queued during the hand, left", []string{"A♠", "K♦"}},
	}
	for _, c := range cases {
		for _, sz := range batch9Sizes {
			v := stripANSI(Render(fixture(t, c.name), testRenderer(), sz[0], sz[1]))
			seat := ownSeatText(t, v)
			for _, card := range c.hole {
				if !strings.Contains(seat, card) {
					t.Errorf("%s at %v: the own seat has no %s\n%s", c.name, sz, card, seat)
				}
			}
			if !strings.Contains(seat, "fold") && !strings.Contains(seat, "Fold") {
				t.Errorf("%s at %v: the own seat does not say that the player folded\n%s", c.name, sz, seat)
			}
		}
	}
	// After the hand, the prompt names the card of each key.
	f := fixture(t, "folded after the hand, may show")
	for _, sz := range batch9Sizes {
		got := strings.Join(strings.Fields(footerLine(t, Render(f, testRenderer(), sz[0], sz[1]), sz[0], sz[1], 0)), " ")
		if !strings.HasPrefix(got, "You folded. Show your cards? 1 9♣ 2 8♣ 3 both") {
			t.Errorf("at %v: status line %q", sz, got)
		}
	}
	// One card is shown: the prompt offers only the other card.
	one := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		r := *s.Table.Result
		r.Revealed = []engine.Shown{{Seat: 0, PlayerID: "key-Priya", Name: "Priya", Cards: parseCards("9c")}}
		s.Table.Result = &r
	})
	got := strings.Join(strings.Fields(footerLine(t, Render(one, testRenderer(), 120, 30), 120, 30, 0)), " ")
	if !strings.HasPrefix(got, "You folded. Show your cards? 2 8♣") || strings.Contains(got, "1 9♣") {
		t.Errorf("one card shown: status line %q", got)
	}
	// The winner of a hand without a showdown gets the card names too.
	w := strings.Join(strings.Fields(footerLine(t, Render(fixture(t, "winner may show"), testRenderer(), 120, 30), 120, 30, 0)), " ")
	if strings.Contains(w, "left") || strings.Contains(w, "right") || !strings.Contains(w, "3 both") {
		t.Errorf("winner: status line %q", w)
	}
}

// The core rule: the folded cards of a player never show on the screen of
// another viewer. The TUI draws the dim folded cards only in the viewer's
// own seat, also if a snapshot held the cards of another folded seat.
func TestFoldedCardsOnlyForOwner(t *testing.T) {
	cases := []struct {
		name  string
		seat  int // the folded seat that keeps its cards
		cards []string
	}{
		{"folded in the hand, may store a show", 2, []string{"A♠", "K♦"}},
		{"show queued during the hand, both", 2, []string{"A♠", "K♦"}},
		{"folded after the hand, may show", 0, []string{"9♣", "8♣"}},
	}
	for _, c := range cases {
		for _, viewer := range []int{-1, 3} {
			f := edited(t, c.name, func(s *server.Snapshot) {
				s.Table.MySeat = viewer
				s.Me.ShowAtEnd = [2]bool{}
				s.Log = nil
			})
			h := newHarness(t, f)
			h.press("1", "2", "3")
			if c.seat != 2 || viewer != 3 {
				// A spectator, and a live player, send nothing.
				h.wantCalls()
			}
			for _, sz := range append(batch9Sizes, [2]int{30, 8}, [2]int{140, 40}) {
				v := stripANSI(h.viewAt(sz[0], sz[1]))
				for _, card := range c.cards {
					if strings.Contains(v, card) {
						t.Errorf("%s, viewer seat %d at %v: %s is on screen\n%s", c.name, viewer, sz, card, v)
					}
				}
			}
		}
	}
}

// B9-UX-2: the show prompt comes before the sit-out and waiting lines, so
// that the footer hint "1 2 3 Show" always has a prompt that names the
// cards.
func TestShowPromptWhenSittingOutOrWaiting(t *testing.T) {
	sitOut := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		s.Table.Seats[0].SittingOut = true
	})
	waiting := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		s.Waiting, s.NextHandAt = true, time.Time{}
	})
	fewReady := edited(t, "folded after the hand, may show", func(s *server.Snapshot) {
		s.Waiting, s.NextHandAt = true, time.Time{}
		for _, i := range []int{1, 2, 3} {
			s.Table.Seats[i].SittingOut = true
		}
	})
	winnerOut := edited(t, "winner may show", func(s *server.Snapshot) {
		s.Table.Seats[s.Table.MySeat].SittingOut = true
	})
	cases := []struct {
		f          Fixture
		lead, tail string
	}{
		{sitOut, "You folded. Show your cards? 1 9♣ 2 8♣ 3 both", "you are sitting out"},
		{waiting, "You folded. Show your cards? 1 9♣ 2 8♣ 3 both", "waiting to start"},
		{fewReady, "You folded. Show your cards? 1 9♣ 2 8♣ 3 both", "waiting for players"},
		{winnerOut, "You win. Show your cards?", "you are sitting out"},
	}
	for _, c := range cases {
		for _, sz := range batch9Sizes {
			v := Render(c.f, testRenderer(), sz[0], sz[1])
			got := strings.Join(strings.Fields(footerLine(t, v, sz[0], sz[1], 0)), " ")
			if !strings.HasPrefix(got, c.lead) {
				t.Errorf("%s at %v: status line %q, want %q first", c.tail, sz, got, c.lead)
			}
			if sz[0] >= 100 && !strings.Contains(got, c.tail) {
				t.Errorf("%s at %v: status line %q has no %q", c.tail, sz, got, c.tail)
			}
			// A narrow footer can drop the hint (it has a low rank), but a
			// folded player's footer keeps it.
			if hints := hintsAt(t, v, sz[0], sz[1]); c.f.Name != winnerOut.Name && !strings.Contains(hints, "1 2 3 Show") {
				t.Errorf("%s at %v: hints %q", c.tail, sz, hints)
			}
		}
	}
	// After the show, the sit-out line comes back.
	shown := edited(t, "own folded hand shown", func(s *server.Snapshot) {
		s.Table.Seats[0].SittingOut = true
	})
	if got := footerLine(t, Render(shown, testRenderer(), 120, 30), 120, 30, 0); !strings.HasPrefix(got, "You are sitting out.") {
		t.Errorf("all shown: status line %q", got)
	}
}

// B9-UX-4: a compact seat with one shown card never cuts the mark to
// "sho…". When no mark fits, the seat draws only the cards.
func TestCompactOneCardShowNotCut(t *testing.T) {
	cut := regexp.MustCompile(`(fo|fol|fold|folde|folded|folded ·|folded · s\w*|sh|sho|show|shown)…`)
	for _, name := range []string{"winner's, Priya", "winner's, spectator"} {
		for _, sz := range append(batch9Sizes, [2]int{30, 8}, [2]int{90, 26}) {
			v := stripANSI(Render(fixture(t, name), testRenderer(), sz[0], sz[1]))
			if m := cut.FindString(v); m != "" {
				t.Errorf("%s at %v: a cut mark %q\n%s", name, sz, m, v)
			}
		}
		v := stripANSI(Render(fixture(t, name), testRenderer(), 60, 20))
		if !strings.Contains(v, "10♦ ░░") {
			t.Errorf("%s at 60x20: Sam's seat lacks 10♦ ░░\n%s", name, v)
		}
	}
}
