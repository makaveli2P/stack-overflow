package tui

import (
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch8Fixtures are the screens of the batch 8 start key and the hand
// rankings box. The box shows in full mode only. At 30 rows (or a narrow
// panel) it drops the examples; at 40 rows it shows them.
func batch8Fixtures() []Fixture {
	return []Fixture{
		fxWaitSeated(), fxWaitSpectator(), fxStartedBy(),
		fxRanksPreflop("Pair", "8s 8d"), fxRanksPreflop("High card", "Kd 9c"),
		fxRanksRiver("flush", "Ah 5h", "Kh 7d 2h 9h Jc"), fxRanksRiver("two pair", "Kd 9c", "Kh 7d 2h 9h Jc"),
		fxRanksSpectator(), fxRanksHidden(),
	}
}

// waitingRoom is a room that waits for a start: Devon, Priya and Sam sit,
// and no hand runs.
func waitingRoom() server.Snapshot {
	s := baseSnap("Devon", table(6, player(0, "Devon", 2000), player(2, "Priya", 2000), player(4, "Sam", 2000)), 0)
	s.Table.HandNumber = 0
	s.Log = []string{"Devon sits down with 2,000", "Priya sits down with 2,000", "Sam sits down with 2,000"}
	s.Waiting = true
	return s
}

// fxWaitSeated is a seated player while the table waits for G.
func fxWaitSeated() Fixture {
	return Fixture{Name: "waiting to start, seated", Snap: waitingRoom()}
}

// fxWaitSpectator is a spectator while the table waits for G.
func fxWaitSpectator() Fixture {
	s := waitingRoom()
	s.Table.MySeat = -1
	s.Me = server.Me{Key: "key-Omar", Name: "Omar", Seat: -1}
	s.Spectators = []string{"Omar"}
	return Fixture{Name: "waiting to start, spectator", Snap: s}
}

// fxStartedBy is the short time between a start and the first deal.
func fxStartedBy() Fixture {
	s := waitingRoom()
	s.Waiting = false
	s.StartedBy = "Priya"
	s.NextHandAt = previewNow.Add(3 * time.Second)
	s.Log = append(s.Log, "Priya started the game")
	return Fixture{Name: "started by Priya, first hand in 3s", Snap: s}
}

// ranksHand is a heads-up hand with Devon's hole cards and a board.
func ranksHand(hole, board string) server.Snapshot {
	me := dealt(player(0, "Devon", 1900), 0, 100, "")
	me.Cards = parseCards(hole)
	opp := dealt(player(1, "Priya", 1900), 0, 100, "Check")
	s := baseSnap("Devon", table(2, me, opp), 0)
	t := &s.Table
	t.InHand, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, 1, 1, 0
	t.Board = parseCards(board)
	t.Street = map[int]engine.Street{0: engine.Preflop, 3: engine.Flop, 4: engine.Turn, 5: engine.River}[len(t.Board)]
	t.Pots = []engine.Pot{{Amount: 200}}
	t.TotalPot = 200
	t.Legal = &engine.Legal{Fold: true, Check: true, Bet: true, MinTo: 20, MaxTo: 1900, AllIn: true, AllInTo: 1900}
	clockOn(&s, 0, 20*time.Second)
	return s
}

// fxRanksPreflop marks Pair or High card before the flop.
func fxRanksPreflop(row, hole string) Fixture {
	return Fixture{Name: "rankings box, preflop, " + row, Snap: ranksHand(hole, "")}
}

// fxRanksRiver marks the viewer's best hand on the river.
func fxRanksRiver(row, hole, board string) Fixture {
	return Fixture{Name: "rankings box, river, " + row, Snap: ranksHand(hole, board)}
}

// fxRanksSpectator is the box for a spectator: no mark and no "You:" line.
func fxRanksSpectator() Fixture {
	f := spectator()
	f.Name = "rankings box, spectator (no mark)"
	return f
}

// fxRanksHidden is the table after I hid the box: the panel is full height.
func fxRanksHidden() Fixture {
	f := fxRanksRiver("two pair", "Kd 9c", "Kh 7d 2h 9h Jc")
	f.Name = "rankings box hidden with I"
	f.Keys = []string{"i"}
	return f
}
