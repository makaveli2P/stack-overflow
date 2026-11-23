package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// ---- batch 6: slow run-out ----

// runoutFixtures are the names of every batch 6 run-out fixture.
var runoutFixtures = []string{
	"run-out, heads-up 0 board cards",
	"run-out, heads-up 3 board cards",
	"run-out, heads-up 4 board cards",
	"run-out, 3 players, one folded, 0 board cards",
	"run-out, 3 players, one folded, 3 board cards",
	"run-out, 3 players, one folded, 4 board cards",
	"run-out, I folded, 3 players, 4 board cards",
	"run-out, spectator, heads-up, 3 board cards",
}

// The board header names the run-out instead of the street, at every size.
func TestRunoutHeader(t *testing.T) {
	for _, name := range runoutFixtures {
		for _, sz := range append(allSizes, [2]int{60, 18}) {
			h := newHarness(t, fixture(t, name))
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			if !strings.Contains(v, "ALL-IN · RUN-OUT") {
				t.Errorf("%s at %dx%d: no run-out header\n%s", name, sz[0], sz[1], v)
			}
			for _, street := range []string{"PREFLOP", "FLOP ·", "TURN ·", "RIVER ·"} {
				if strings.Contains(v, street) {
					t.Errorf("%s at %dx%d: header names the street %q\n%s", name, sz[0], sz[1], street, v)
				}
			}
		}
	}
}

// Without a run-out the header still names the street.
func TestStreetHeaderWithoutRunout(t *testing.T) {
	f := edited(t, "run-out, heads-up 3 board cards", func(s *server.Snapshot) { s.Table.Runout = false })
	for _, sz := range allSizes {
		v := stripANSI(newHarness(t, f).viewAt(sz[0], sz[1]))
		if !strings.Contains(v, "FLOP · Hand #42") || strings.Contains(v, "RUN-OUT") {
			t.Errorf("at %dx%d: want the street header\n%s", sz[0], sz[1], v)
		}
	}
}

// The status line says that the board is being dealt; no action bar shows.
func TestRunoutStatusLine(t *testing.T) {
	for _, name := range runoutFixtures {
		for _, sz := range allSizes {
			v := stripANSI(newHarness(t, fixture(t, name)).viewAt(sz[0], sz[1]))
			if !strings.Contains(v, "All-in. Dealing the board…") {
				t.Errorf("%s at %dx%d: no run-out status line\n%s", name, sz[0], sz[1], v)
			}
			for _, bad := range []string{"YOUR TURN", "Waiting for", "F Fold", "C Call", "C Check", "R Raise"} {
				if strings.Contains(v, bad) {
					t.Errorf("%s at %dx%d: shows %q during a run-out", name, sz[0], sz[1], bad)
				}
			}
		}
	}
	v := stripANSI(newHarness(t, fixture(t, "run-out, I folded")).viewAt(80, 24))
	if !strings.Contains(v, "You folded. All-in. Dealing the board…") {
		t.Errorf("a folded viewer gets no fold cue\n%s", v)
	}
}

// Every live seat draws its face-up cards; a folded seat draws none. Only
// the viewer's own folded seat draws its cards (dim, batch 9), because the
// viewer can still choose to show them at the end.
func TestRunoutFaceUpCards(t *testing.T) {
	for _, name := range runoutFixtures {
		h := newHarness(t, fixture(t, name))
		for i, sv := range h.m.snap.Table.Seats {
			if sv.Empty || !sv.InHand {
				continue
			}
			got := stripANSI(h.m.holeCards(h.m.seatInfo(i), true, 40))
			want := ""
			if !sv.Folded || i == h.m.snap.Table.MySeat {
				want = cardText(sv.Cards[0]) + " " + cardText(sv.Cards[1])
			}
			if got != want {
				t.Errorf("%s: seat %d (%s) draws %q, want %q", name, i, sv.Name, got, want)
			}
		}
	}
}

// The live hands show on the seats in the drawn screen, also with the log
// out of view (the log holds the same cards in its show lines).
func TestRunoutFaceUpCardsOnScreen(t *testing.T) {
	cases := []struct {
		fixture string
		want    []string
		not     []string
	}{
		{"run-out, heads-up 0 board cards", []string{"Q♠", "Q♦", "A♥", "K♥"}, nil},
		{"run-out, 3 players, one folded, 3 board cards", []string{"8♥", "8♣", "J♠", "10♠"}, nil},
		{"run-out, I folded", []string{"8♥", "8♣", "J♠", "10♠", "5♣", "4♦"}, nil}, // the viewer's own folded cards
		{"run-out, spectator", []string{"Q♠", "Q♦", "A♥", "K♥"}, []string{"░░"}},
	}
	for _, c := range cases {
		for _, sz := range allSizes {
			h := newHarness(t, fixture(t, c.fixture))
			h.press("tab") // the chat tab: no show lines on screen
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			for _, w := range c.want {
				if !strings.Contains(v, w) {
					t.Errorf("%s at %dx%d: %q is not on screen\n%s", c.fixture, sz[0], sz[1], w, v)
				}
			}
			for _, n := range append(c.not, "░░") {
				if strings.Contains(v, n) {
					t.Errorf("%s at %dx%d: %q is on screen\n%s", c.fixture, sz[0], sz[1], n, v)
				}
			}
		}
	}
}

// A live seat with cards draws them during a run-out also when the engine
// leaves HasCards false; a folded seat with cards stays dark.
func TestRunoutDrawsCardsWithoutHasCards(t *testing.T) {
	f := edited(t, "run-out, 3 players, one folded, 3 board cards", func(s *server.Snapshot) {
		s.Table.Seats[2].HasCards = false
		s.Table.Seats[4].Cards = parseCards("2c 2s")
	})
	h := newHarness(t, f)
	if got := stripANSI(h.m.holeCards(h.m.seatInfo(2), true, 40)); got != "8♥ 8♣" {
		t.Errorf("live seat draws %q, want 8♥ 8♣", got)
	}
	if got := stripANSI(h.m.holeCards(h.m.seatInfo(4), true, 40)); got != "" {
		t.Errorf("folded seat draws %q, want nothing", got)
	}
}

// The viewer's best-hand hint follows the board as it grows.
func TestRunoutHandHint(t *testing.T) {
	cases := []struct{ fixture, want string }{
		{"run-out, heads-up 0 board cards", "Ace-King suited"},
		{"run-out, heads-up 3 board cards", "Pair of Kings"},
		{"run-out, heads-up 4 board cards", "Pair of Kings"},
		{"run-out, 3 players, one folded, 4 board cards", "High Card, King"},
	}
	for _, c := range cases {
		for _, sz := range allSizes {
			v := stripANSI(newHarness(t, fixture(t, c.fixture)).viewAt(sz[0], sz[1]))
			if !strings.Contains(v, c.want) {
				t.Errorf("%s at %dx%d: no hint %q\n%s", c.fixture, sz[0], sz[1], c.want, v)
			}
		}
	}
}

// No action key works during a run-out. A key that acts gives a notice.
func TestRunoutKeysDoNothing(t *testing.T) {
	stale := func(s *server.Snapshot) {
		// A stale turn in the snapshot must not bring the keys back.
		s.Table.Legal = &engine.Legal{Fold: true, Call: true, CallAmount: 20, Raise: true, MinTo: 40, MaxTo: 2000, AllIn: true, AllInTo: 2000}
		s.Table.ToAct = s.Table.MySeat
		s.Deadline = previewNow.Add(20 * time.Second)
	}
	for _, name := range []string{"run-out, heads-up 3 board cards", "run-out, 3 players, one folded, 0 board cards", "run-out, I folded"} {
		for _, f := range []Fixture{fixture(t, name), edited(t, name, stale)} {
			for _, keys := range [][]string{{"c"}, {"C"}, {"k"}, {"f"}, {"r"}, {"b"}, {"a"}, {"a", "a"}, {"E"}, {"e"}, {"r", "enter"}} {
				h := newHarness(t, f)
				h.press(keys...)
				h.wantCalls()
				if h.m.mode != modeTable {
					t.Errorf("%s %v: mode = %v, want the table", name, keys, h.m.mode)
				}
				if h.m.notice.text != runoutNotice {
					t.Errorf("%s %v: notice = %q, want %q", name, keys, h.m.notice.text, runoutNotice)
				}
				v := stripANSI(h.viewAt(80, 24))
				if strings.Contains(v, "YOUR TURN") || strings.Contains(v, "E +30s") {
					t.Errorf("%s %v: turn cues show during a run-out\n%s", name, keys, v)
				}
			}
		}
	}
}

// The run-out turns off the window-title turn cue and any open editor.
func TestRunoutEndsTurnState(t *testing.T) {
	h := newHarness(t, fixture(t, "heads-up preflop, my turn"))
	h.press("r")
	if h.m.mode != modeRaise {
		t.Fatalf("mode = %v, want the editor", h.m.mode)
	}
	h.snapshot(fixture(t, "run-out, heads-up 0 board cards").Snap)
	if h.m.mode != modeTable || h.m.myTurn {
		t.Errorf("mode = %v, myTurn = %v after the run-out starts", h.m.mode, h.m.myTurn)
	}
}

// Every run-out screen fits, also with the help, the chat and the panel tabs.
func TestBatch6StatesFit(t *testing.T) {
	keys := [][]string{nil, {"tab"}, {"tab", "tab"}, {"t"}, {"?"}, {"c"}}
	for _, name := range runoutFixtures {
		for _, k := range keys {
			for _, sz := range append(allSizes, [2]int{60, 18}, [2]int{90, 26}, [2]int{30, 8}) {
				h := newHarness(t, fixture(t, name))
				h.press(k...)
				wantFits(t, fmt.Sprintf("%s %v", name, k), h.viewAt(sz[0], sz[1]), sz[0], sz[1])
			}
		}
	}
}

// A long compact title drops the hand number before it cuts the pot.
func TestRunoutCompactTitleKeepsPot(t *testing.T) {
	f := edited(t, "run-out, heads-up 3 board cards", func(s *server.Snapshot) {
		s.Table.HandNumber = 1234
		s.Table.TotalPot, s.Table.Pots = 123456, []engine.Pot{{Amount: 123456}}
	})
	v := stripANSI(newHarness(t, f).viewAt(80, 24))
	if !strings.Contains(v, "ALL-IN · RUN-OUT · Pot 123,456") {
		t.Errorf("compact title loses the pot\n%s", v)
	}
}

// A stand ended a paused run-out: the winner's hand was face up since the
// start of the run-out, and Result.Revealed holds both cards. The winner
// gets no show prompt, and 1, 2 and 3 send nothing.
func TestRunoutStandWinnerHasNoShowPrompt(t *testing.T) {
	f := edited(t, "winner may show", func(s *server.Snapshot) {
		r := *s.Table.Result
		r.Revealed = []engine.Shown{{Seat: 3, PlayerID: "key-Marta", Name: "Marta", Cards: s.Table.Seats[3].Cards}}
		s.Table.Result = &r
	})
	for _, sz := range allSizes {
		h := newHarness(t, f)
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		if strings.Contains(v, "Show your cards?") {
			t.Errorf("at %dx%d: the winner gets a show prompt for tabled cards\n%s", sz[0], sz[1], v)
		}
		h.press("1", "2", "3")
		h.wantCalls()
	}
}
