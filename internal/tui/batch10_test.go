package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch10Sizes are the terminal sizes of the batch 10 checks.
var batch10Sizes = [][2]int{{60, 20}, {80, 24}, {120, 30}, {188, 55}}

// ---- the limit ----

// The H hint names the hunts left, in the footer and on the felt.
func TestRabbitHintCount(t *testing.T) {
	f := fixture(t, "rabbit hunt offered, 2 left")
	for _, sz := range batch10Sizes {
		h := newHarness(t, f)
		view := h.viewAt(sz[0], sz[1])
		if hints := hintsAt(t, view, sz[0], sz[1]); !strings.Contains(hints, "H rabbit hunt (2 left)") {
			t.Errorf("%v: hints %q", sz, hints)
		}
		if sz[0] >= fullW && strings.Count(stripANSI(view), "H rabbit hunt (2 left)") != 2 {
			t.Errorf("%v: the felt does not offer H with the count\n%s", sz, stripANSI(view))
		}
	}
}

// With no limit (Config.RabbitHunts 0), the hint has no count.
func TestRabbitHintNoLimit(t *testing.T) {
	h := newHarness(t, fixture(t, "winner may show cards and hunt the rabbit"))
	view := h.viewAt(120, 30)
	hints := hintsAt(t, view, 120, 30)
	if !strings.Contains(hints, "H rabbit hunt ·") || strings.Contains(stripANSI(view), "left)") {
		t.Errorf("hints %q\n%s", hints, stripANSI(view))
	}
}

// With 0 hunts left, no hint offers H. H still sends the hunt, and the
// server error shows in the status line.
func TestRabbitNoneLeft(t *testing.T) {
	f := fixture(t, "rabbit hunt not offered, 0 left")
	for _, sz := range batch10Sizes {
		h := newHarness(t, f)
		if v := stripANSI(h.viewAt(sz[0], sz[1])); strings.Contains(v, "rabbit hunt") || strings.Contains(v, "H rabb") {
			t.Errorf("%v: H is offered with 0 left\n%s", sz, v)
		}
	}
	h := newHarness(t, f)
	h.sess.err = server.ErrNoRabbits
	h.press("h")
	h.wantCalls("RabbitHunt")
	v := h.viewAt(120, 30)
	if line := footerLine(t, v, 120, 30, 1); !strings.Contains(line, "no rabbit hunts left") {
		t.Errorf("status %q, want the server error", line)
	}
}

// ---- "you would have had" ----

// wouldFixtures are the batch 10 fixtures with a "would have had" line,
// with the expected hand.
var wouldFixtures = map[string]string{
	"would have had, folded player": "Flush, Ace high",
	"would have had, winner":        "Three of a Kind, Nines",
	"would have had, 9 seats":       "Flush, Ace high",
}

// The private line sits right under the rabbit label (full mode), or right
// under the board line (compact mode).
func TestWouldHaveLine(t *testing.T) {
	for name, hand := range wouldFixtures {
		f := fixture(t, name)
		for _, sz := range batch10Sizes {
			h := newHarness(t, f)
			lines := strings.Split(stripANSI(h.viewAt(sz[0], sz[1])), "\n")
			label := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "┆rabbi") })
			if label < 0 || label+1 >= len(lines) {
				t.Fatalf("%s %v: no rabbit label\n%s", name, sz, strings.Join(lines, "\n"))
			}
			next := lines[label+1]
			if !strings.Contains(next, "ould have") {
				t.Errorf("%s %v: the line under the rabbit label is %q", name, sz, next)
			}
			if sz[0] >= 80 && !strings.Contains(next, hand) && !strings.Contains(next, rankRows[0].you) {
				// At 80 and wider, the felt has room for the full hand name,
				// except for the longest names in full mode.
				if sz == [2]int{80, 24} || sz == [2]int{188, 55} {
					t.Errorf("%s %v: %q does not name %q", name, sz, next, hand)
				}
			}
		}
	}
	h := newHarness(t, fixture(t, "would have had, folded player"))
	for _, sz := range [][2]int{{80, 24}, {188, 55}} {
		if v := stripANSI(h.viewAt(sz[0], sz[1])); !strings.Contains(v, "You would have had: Flush, Ace high") {
			t.Errorf("%v: no full line\n%s", sz, v)
		}
	}
}

// wouldLine falls back to shorter forms in a narrow felt.
func TestWouldLineForms(t *testing.T) {
	h := newHarness(t, fixture(t, "would have had, winner"))
	for w, want := range map[int]string{
		60: "You would have had: Three of a Kind, Nines",
		38: "Would have had: Three of a Kind, Nines",
		35: "You would have had: Three of a Kind",
		27: "Would have: Three of a Kind",
	} {
		if got := stripANSI(h.m.wouldLine(w)); got != want {
			t.Errorf("wouldLine(%d) = %q, want %q", w, got, want)
		}
	}
	if got := stripANSI(h.m.wouldLine(20)); len([]rune(got)) > 20 || !strings.HasPrefix(got, "Would have") {
		t.Errorf("wouldLine(20) = %q", got)
	}
}

// The 5 best cards are marked: on the board, on the rabbit cards and on
// the viewer's own cards. The other cards are not.
func TestWouldHaveMarks(t *testing.T) {
	h := newHarness(t, fixture(t, "would have had, folded player"))
	rh := h.m.snap.Me.RabbitHand
	marks := h.m.highlights()
	if got := slices.SortedFunc(maps.Keys(marks), cardOrder); !slices.Equal(got, slices.SortedFunc(slices.Values(rh.Best), cardOrder)) {
		t.Fatalf("marks %v, want %v", got, rh.Best)
	}
	view := h.viewAt(120, 30)
	st := h.m.st
	mid := func(box string) string { return strings.Split(box, "\n")[1] }
	c := func(s string) cards.Card { return parseCards(s)[0] }
	for _, want := range []string{
		mid(st.cardBox(c("Qh"), hlHint)), mid(st.cardBox(c("7h"), hlHint)),
		mid(st.rabbitBox(c("9h"), hlHint)), mid(st.cardBox(c("Kc"), hlNone)),
		mid(st.rabbitBox(c("2s"), hlNone)),
		mid(st.oldCardBox(c("Ah"), hlHint)), mid(st.oldCardBox(c("4h"), hlHint)),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks the card face %q", stripANSI(want))
		}
	}
	// Compact mode marks the inline cards too.
	view = h.viewAt(80, 24)
	for _, want := range []string{
		st.rabbitChipHL(c("9h"), false, hlHint), st.oldChip(c("Ah"), true, hlHint),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("compact view lacks the card %q", stripANSI(want))
		}
	}
}

func cardOrder(a, b cards.Card) int {
	if a.Suit != b.Suit {
		return int(a.Suit) - int(b.Suit)
	}
	return int(a.Rank) - int(b.Rank)
}

// The rankings box marks the row with a hollow marker and names it.
func TestWouldHaveRankingsBox(t *testing.T) {
	for name, c := range map[string][2]string{
		"would have had, folded player": {"▷ 5 Flush", "Would have: Flush"},
		"would have had, winner":        {"▷ 7 Three of a kind", "Would have: Three of a Kind"},
	} {
		h := newHarness(t, fixture(t, name))
		v := stripANSI(h.viewAt(120, 30))
		row, want := c[0], c[1]
		if !strings.Contains(v, row) || !strings.Contains(v, "│ "+want) || strings.Contains(v, "▶") {
			t.Errorf("%s: box lacks %q or %q\n%s", name, row, want, v)
		}
	}
	// No hunt, no mark.
	h := newHarness(t, fixture(t, "rabbit hunt offered, 2 left"))
	if v := stripANSI(h.viewAt(120, 30)); strings.Contains(v, "▷") || strings.Contains(v, "Would have") {
		t.Errorf("a mark without a hunt\n%s", v)
	}
}

// The royal flush has its own row.
func TestRabbitRowRoyal(t *testing.T) {
	for _, c := range []struct {
		cards string
		row   int
	}{
		{"As Ks Qs Js Ts 2c 3d", 0}, {"9s Ks Qs Js Ts 2c 3d", 1}, {"2c 2d 2h 2s 9c 8d 7h", 2},
		{"Ah Kh 9h 7h 4h 2c 3d", 4}, {"9s 9d 9h Kc Qh 2s 7h", 6}, {"Ac Kd 9h 7h 4s 2c 3d", 9},
	} {
		hv, best := cards.Best(cards.MustParseMany(c.cards))
		rh := server.RabbitHand{Category: hv.Category, Hand: hv.String(), Best: best[:]}
		if got := rabbitRow(rh); got != c.row {
			t.Errorf("%s (%s): row %d, want %d", c.cards, rh.Hand, got, c.row)
		}
	}
}

// The line and the marks go away at the next deal, also when a stale
// RabbitHand came with the snapshot: they need the rabbit cards on screen.
func TestWouldHaveGoneAtNextDeal(t *testing.T) {
	f := fixture(t, "would have had, folded player")
	h := newHarness(t, f)
	s := f.Snap
	s.Table.InHand, s.Table.Result = true, nil
	h.snapshot(s)
	for _, sz := range batch10Sizes {
		if v := stripANSI(h.viewAt(sz[0], sz[1])); strings.Contains(v, "ould have") || strings.Contains(v, "▷") {
			t.Errorf("%v: line after the next deal\n%s", sz, v)
		}
	}
	if _, ok := h.m.rabbitHand(); ok {
		t.Error("rabbitHand during a live hand")
	}
	// Another player's result without a RabbitHand for the viewer: nothing.
	f = fixture(t, "would have had, folded player")
	f.Snap.Me.RabbitHand = server.RabbitHand{}
	h = newHarness(t, f)
	if v := stripANSI(h.viewAt(120, 30)); strings.Contains(v, "ould have") {
		t.Errorf("line without a RabbitHand\n%s", v)
	}
}

// Every batch 10 screen fits in every size, with an exact line count, and
// the felt keeps its bottom edge.
func TestBatch10StatesFit(t *testing.T) {
	keys := [][]string{nil, {"?"}, {"tab"}, {"i"}}
	sizes := append(slices.Clone(batch10Sizes), [2]int{100, 30}, [2]int{140, 40}, [2]int{90, 26}, [2]int{30, 8})
	for _, f := range batch10Fixtures() {
		for _, k := range keys {
			for _, sz := range sizes {
				h := newHarness(t, f)
				h.press(k...)
				view := h.viewAt(sz[0], sz[1])
				name := fmt.Sprintf("%s %v", f.Name, k)
				wantFits(t, name, view, sz[0], sz[1])
				if n := len(strings.Split(view, "\n")); n != sz[1] {
					t.Errorf("%s at %v: %d lines", name, sz, n)
				}
				if k != nil || sz[0] < 60 {
					continue
				}
				v := stripANSI(view)
				if strings.Count(v, "(you)") != 1 {
					t.Errorf("%s at %v: the own seat is cut\n%s", name, sz, v)
				}
				if _, ok := wouldFixtures[strings.TrimPrefix(f.Name, "rabbit hunt, ")]; ok && !strings.Contains(v, "ould have") {
					t.Errorf("%s at %v: no would-have line\n%s", name, sz, v)
				}
			}
		}
	}
}
