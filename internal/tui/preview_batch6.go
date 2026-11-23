package tui

import (
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch6Fixtures are the screens of the batch 6 slow run-out: 0, 3 and 4
// board cards, heads-up and with three players (one folded).
func batch6Fixtures() []Fixture {
	return []Fixture{
		fxRunoutHeadsUp(0), fxRunoutHeadsUp(3), fxRunoutHeadsUp(4),
		fxRunoutThree(0), fxRunoutThree(3), fxRunoutThree(4),
		fxRunoutFoldedMe(), fxRunoutSpectator(),
	}
}

// runoutBoard is the board of every run-out fixture, in deal order.
const runoutBoard = "Kc 7h 2d 9s Qh"

// runoutStreet returns the street that matches n board cards.
func runoutStreet(n int) engine.Street {
	switch n {
	case 0:
		return engine.Preflop
	case 3:
		return engine.Flop
	case 4:
		return engine.Turn
	}
	return engine.River
}

// runoutLog returns the log lines of a run-out: the all-in, the call, the
// shows, and one line for each street that is out.
func runoutLog(lines []string, n int) []string {
	streets := []struct {
		at   int
		line string
	}{
		{3, "Flop: K♣ 7♥ 2♦"},
		{4, "Turn: K♣ 7♥ 2♦ [9♠]"},
	}
	for _, s := range streets {
		if n >= s.at {
			lines = append(lines, s.line)
		}
	}
	return lines
}

// runoutSnap sets the table fields that every run-out fixture shares.
func runoutSnap(s *server.Snapshot, n int, pot int64, button, sb, bb int) {
	t := &s.Table
	t.InHand, t.Runout, t.Street = true, true, runoutStreet(n)
	t.Button, t.SmallBlindSeat, t.BigBlindSeat = button, sb, bb
	t.Board = parseCards(runoutBoard)[:n]
	t.Pots = []engine.Pot{{Amount: pot}}
	t.TotalPot, t.CurrentBet, t.ToAct = pot, 0, -1
}

// fxRunoutHeadsUp is a heads-up all-in preflop, paused with n board cards
// out. Both hands are face up.
func fxRunoutHeadsUp(n int) Fixture {
	me := dealt(player(0, "Devon", 0), 0, 1600, "")
	me.AllIn, me.Cards = true, parseCards("Ah Kh")
	opp := dealt(player(1, "Priya", 0), 0, 1600, "")
	opp.AllIn, opp.Cards = true, parseCards("Qs Qd")
	if n == 0 {
		me.LastAction, opp.LastAction = "All-in 1,600", "Call 1,580"
	}
	s := baseSnap("Devon", table(2, me, opp), 0)
	runoutSnap(&s, n, 3200, 0, 0, 1)
	s.Log = runoutLog(append(s.Log, "Devon posts small blind 10", "Priya posts big blind 20",
		"Devon is all-in for 1,600", "Priya calls 1,580", "Devon shows A♥ K♥", "Priya shows Q♠ Q♦"), n)
	return Fixture{Name: runoutName("heads-up", n), Snap: s}
}

// threeSeats returns a 6-seat table with Devon, Priya and Sam dealt in.
// Sam folded; Devon and Priya are all-in.
func threeSeats() []engine.SeatView {
	me := dealt(player(0, "Devon", 0), 0, 1460, "")
	me.AllIn, me.Cards = true, parseCards("Js Ts")
	opp := dealt(player(2, "Priya", 540), 0, 1460, "")
	opp.Cards = parseCards("8h 8c")
	sam := dealt(player(4, "Sam", 1940), 0, 60, "")
	sam.Folded, sam.HasCards = true, false
	return table(6, me, opp, sam)
}

// fxRunoutThree is a three-way hand where one player folded and the other
// two are all-in, paused with n board cards out.
func fxRunoutThree(n int) Fixture {
	seats := threeSeats()
	s := baseSnap("Devon", seats, 0)
	runoutSnap(&s, n, 2980, 4, 0, 2)
	s.Log = runoutLog(append(s.Log, "Devon posts small blind 10", "Priya posts big blind 20",
		"Sam raises to 60", "Devon is all-in for 1,460", "Priya calls 1,440", "Sam folds",
		"Devon shows J♠ 10♠", "Priya shows 8♥ 8♣"), n)
	return Fixture{Name: runoutName("3 players, one folded,", n), Snap: s}
}

// fxRunoutFoldedMe is the three-way run-out when the viewer is the
// player who folded: the viewer sees both live hands face up.
func fxRunoutFoldedMe() Fixture {
	seats := threeSeats()
	seats[0], seats[4] = seats[4], seats[0]
	seats[0].Index, seats[4].Index = 0, 4
	seats[0].Name, seats[0].PlayerID = "Devon", "key-Devon"
	seats[4].Name, seats[4].PlayerID = "Sam", "key-Sam"
	seats[0].Cards = parseCards("5c 4d") // the engine keeps them; they are dead
	s := baseSnap("Devon", seats, 0)
	runoutSnap(&s, 4, 2980, 0, 2, 4)
	s.Log = runoutLog(append(s.Log, "Priya posts small blind 10", "Sam posts big blind 20",
		"Devon raises to 60", "Priya is all-in for 1,460", "Sam calls 1,440", "Devon folds",
		"Priya shows 8♥ 8♣", "Sam shows J♠ 10♠"), 4)
	return Fixture{Name: "run-out, I folded, 3 players, 4 board cards", Snap: s}
}

// fxRunoutSpectator is the heads-up run-out on the flop, seen by a
// spectator.
func fxRunoutSpectator() Fixture {
	f := fxRunoutHeadsUp(3)
	s := f.Snap
	s.Table.Seats = append([]engine.SeatView(nil), s.Table.Seats...)
	s.Table.Seats[0].Name, s.Table.Seats[0].PlayerID = "Nadia", "key-Nadia"
	s.Table.MySeat = -1
	s.Me = server.Me{Key: "key-Omar", Name: "Omar", Seat: -1, Bank: 2000}
	s.Spectators = []string{"Omar"}
	s.Log = append([]string(nil), s.Log...)
	for i := len(s.Log) - 6; i < len(s.Log); i++ {
		s.Log[i] = strings.ReplaceAll(s.Log[i], "Devon", "Nadia")
	}
	s.Leaderboard = append([]server.Standing(nil), s.Leaderboard...)
	s.Leaderboard[0].Name = "Nadia"
	return Fixture{Name: "run-out, spectator, heads-up, 3 board cards", Snap: s}
}

func runoutName(who string, n int) string {
	return "run-out, " + who + " " + string(rune('0'+n)) + " board cards"
}
