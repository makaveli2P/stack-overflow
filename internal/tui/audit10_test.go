package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 10 audit: between hands, a new player in the seat of the last
// winner must not show the old "wins N" or the old showdown hand.
func TestNewOccupantShowsNoOldWin(t *testing.T) {
	base := fixture(t, "9 seats, showdown result").Snap
	base.Table.Seats = slices.Clone(base.Table.Seats)
	r := *base.Table.Result
	r.Winnings = append(slices.Clone(r.Winnings),
		engine.Winning{Seat: 6, PlayerID: "key-Chen", Name: "Chen", Amount: 260, PotIndex: 2, Hand: "Three of a Kind, Eights"})
	r.Shown = slices.Clone(r.Shown)
	for i := range r.Shown {
		r.Shown[i].PlayerID = "key-" + r.Shown[i].Name
	}
	base.Table.Result = &r

	live := base
	live.Table.Seats = slices.Clone(base.Table.Seats)
	live.Table.InHand, live.Table.Street, live.Table.Result = true, engine.River, nil

	nina := base
	nina.Table.Seats = slices.Clone(base.Table.Seats)
	nina.Table.Seats[6] = player(6, "Nina", 1000)

	type step struct {
		name string
		live bool // the model saw the live hand first
	}
	for _, st := range []step{{"with the live hand", true}, {"result only", false}} {
		first := base
		if st.live {
			first = live
		}
		h := newHarness(t, Fixture{Name: st.name, Snap: first})
		h.snapshot(base)
		if si := h.m.seatInfo(6); si.won != 260 || si.hand != "Three of a Kind, Eights" {
			t.Fatalf("%s: Chen's seat: won %d, hand %q", st.name, si.won, si.hand)
		}
		h.snapshot(nina)
		si := h.m.seatInfo(6)
		if si.won != 0 || si.hand != "" {
			t.Errorf("%s: Nina's seat shows won %d, hand %q from Chen's hand", st.name, si.won, si.hand)
		}
		v := stripANSI(h.viewAt(188, 55))
		lines := strings.Split(v, "\n")
		at, col := -1, -1
		for i, l := range lines {
			if c := strings.Index(l, "│ Nina"); c >= 0 {
				at, col = i, len([]rune(l[:c]))+1
				break
			}
		}
		if at < 0 || at+3 >= len(lines) {
			t.Fatalf("%s: no seat of Nina\n%s", st.name, v)
		}
		for _, l := range lines[at : at+4] {
			if cell := seatCell(l, col); strings.Contains(cell, "wins") || strings.Contains(cell, "Eights") {
				t.Errorf("%s: Nina's seat shows %q\n%s", st.name, cell, v)
			}
		}
		// The winner's own seat keeps the win.
		if si := h.m.seatInfo(0); si.won != 3360 {
			t.Errorf("%s: Devon's seat: won %d, want 3360", st.name, si.won)
		}
	}
}
