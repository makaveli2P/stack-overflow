package tui

import (
	"slices"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch10Fixtures are the screens of batch 10: a limit of rabbit hunts for
// each player, and the private "you would have had" line after a hunt.
func batch10Fixtures() []Fixture {
	return []Fixture{fxRabbitLeft(2), fxRabbitLeft(0), fxRabbitFolded(), fxRabbitWinner(), fxRabbitNine()}
}

// rabbitHandOf computes Me.RabbitHand as the server does: the best five
// cards of hole with the board and the rabbit cards.
func rabbitHandOf(hole, board, rabbit []cards.Card) server.RabbitHand {
	hv, best := cards.Best(slices.Concat(hole, board, rabbit))
	return server.RabbitHand{Category: hv.Category, Hand: hv.String(), Best: best[:]}
}

// hunted is the hand of foldedOut after Devon folded on the flop and
// hunted the rabbit: 9♥ 2♠. viewer is "Devon" or "Marta". Devon held
// A♥ 4♥ and would have had a flush; Marta held 9♠ 9♦.
func hunted(viewer string) server.Snapshot {
	s := foldedOut(viewer)
	s.Config.RabbitHunts = 3
	s.Me.RabbitsLeft = 3
	t := &s.Table
	t.Result.Actions = []engine.ActionRecord{{PlayerID: "key-Devon", Street: engine.Flop, Type: engine.Fold}}
	t.Result.CanRabbit = false
	t.Result.Rabbit = parseCards("9h 2s")
	var hole []cards.Card
	switch viewer {
	case "Devon":
		// The owner's view after a fold: the own cards, no live hand.
		hole = parseCards("Ah 4h")
		t.Seats[2].Cards, t.Seats[2].HasCards = hole, false
		s.Me.RabbitsLeft = 2
	case "Marta":
		hole = parseCards("9s 9d")
		t.Seats[3].Cards = hole
	}
	s.Me.RabbitHand = rabbitHandOf(hole, t.Board, t.Result.Rabbit)
	s.Log = append(s.Log, "Rabbit hunt (Devon): [9♥] [2♠]")
	return s
}

// fxRabbitLeft: the winner may hunt the rabbit, with left hunts left. With
// 0 left, the hint goes away.
func fxRabbitLeft(left int) Fixture {
	s := foldedOut("Marta")
	s.Config.RabbitHunts = 3
	s.Me.RabbitsLeft = left
	name := "rabbit hunt offered, 2 left"
	if left == 0 {
		name = "rabbit hunt not offered, 0 left"
	}
	return Fixture{Name: name, Snap: s}
}

// fxRabbitFolded: Devon folded, hunted, and sees what he would have had.
func fxRabbitFolded() Fixture {
	return Fixture{Name: "rabbit hunt, would have had, folded player", Snap: hunted("Devon")}
}

// fxRabbitWinner: Marta won without a showdown and sees what her cards
// would have made with the rabbit cards.
func fxRabbitWinner() Fixture {
	return Fixture{Name: "rabbit hunt, would have had, winner", Snap: hunted("Marta")}
}

// fxRabbitNine: the folded player's view at a full table of 9 seats. The
// felt has one more line; the table must still fit.
func fxRabbitNine() Fixture {
	s := hunted("Devon")
	for i, name := range []string{"Ana", "Bo", "Cy"} {
		s.Table.Seats = append(s.Table.Seats, player(6+i, name, 1000))
		s.Leaderboard = append(s.Leaderboard, server.Standing{Name: name, Online: true, Playing: true})
	}
	s.Config.Seats = len(s.Table.Seats)
	return Fixture{Name: "rabbit hunt, would have had, 9 seats", Snap: s}
}
