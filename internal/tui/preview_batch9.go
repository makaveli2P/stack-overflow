package tui

import (
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch9Fixtures are the screens of batch 9: a folded player can show the
// hole cards, but only after the hand ends.
func batch9Fixtures() []Fixture {
	return []Fixture{
		fxFoldedInHand(), fxShowQueued([2]bool{true, true}), fxShowQueued([2]bool{true, false}),
		fxShowQueued([2]bool{false, true}), fxFoldedMayShow(), fxFoldedShownResult("Priya"),
		fxFoldedShownResult(""), fxOwnFoldedShown(),
	}
}

// foldedTurn is a 6-seat hand on the turn. Devon folded on the flop; Priya
// must act.
func foldedTurn() server.Snapshot {
	me := dealt(player(2, "Devon", 1400), 0, 60, "Fold")
	me.Folded, me.HasCards = true, false
	me.Cards = parseCards("As Kd")
	seats := table(6,
		dealt(player(0, "Priya", 2190), 0, 180, "Check"),
		func() engine.SeatView {
			s := dealt(player(1, "Sam", 980), 0, 20, "Fold")
			s.Folded, s.HasCards = true, false
			return s
		}(),
		me,
		dealt(player(3, "Marta", 1080), 120, 300, "Bet 120"),
		func() engine.SeatView { s := player(5, "Leo", 2000); s.SittingOut = true; return s }(),
	)
	s := baseSnap("Devon", seats, 2)
	withMoney(&s)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.Turn, 3, 0, 1
	t.Board = parseCards("Kc 7h Qh 2s")
	t.Pots = []engine.Pot{{Amount: 440, Eligible: []int{0, 3}}}
	t.CurrentBet, t.TotalPot = 120, 560
	clockOn(&s, 0, 14*time.Second)
	s.Log = append(s.Log, "Sam folds", "Flop: K♣ 7♥ Q♥", "Marta bets 60", "Devon folds", "Priya calls 60",
		"Turn: K♣ 7♥ Q♥ [2♠]", "Priya checks", "Marta bets 120")
	return s
}

// fxFoldedInHand: Devon folded and stored nothing. The footer offers 1 2 3.
func fxFoldedInHand() Fixture {
	return Fixture{Name: "folded in the hand, may store a show", Snap: foldedTurn()}
}

// fxShowQueued: Devon folded and stored a show for the end of the hand.
func fxShowQueued(c [2]bool) Fixture {
	s := foldedTurn()
	s.Me.ShowAtEnd = c
	name := map[[2]bool]string{{true, true}: "both", {true, false}: "left", {false, true}: "right"}[c]
	return Fixture{Name: "show queued during the hand, " + name, Snap: s}
}

// foldedResult is the end of a hand that Marta won when everyone folded on
// the turn. Devon folded and showed both cards (a stored show), Sam folded
// and showed one card, and Marta showed both cards. Priya folded and showed
// nothing. viewer "" is the spectator Omar.
func foldedResult(viewer string) server.Snapshot {
	priya := dealt(player(0, "Priya", 2190), 0, 180, "Fold")
	sam := dealt(player(1, "Sam", 960), 0, 40, "Fold")
	devon := dealt(player(2, "Devon", 1400), 0, 60, "Fold")
	marta := dealt(player(3, "Marta", 1580), 0, 300, "")
	for _, sv := range []*engine.SeatView{&priya, &sam, &devon, &marta} {
		sv.InHand, sv.Bet, sv.Committed = false, 0, 0
	}
	// The engine view after the hand: both cards shown are face up for
	// everyone; one card shown leaves a card back (HasCards).
	priya.HasCards, sam.HasCards, devon.HasCards = false, true, false
	devon.Cards = parseCards("As Kd")
	marta.Cards = parseCards("Jh Jd")
	mySeat := -1
	if viewer == "Priya" {
		priya.Cards = parseCards("9c 8c")
		mySeat = 0
	}
	seats := table(6, priya, sam, devon, marta,
		func() engine.SeatView { s := player(5, "Leo", 2000); s.SittingOut = true; return s }())
	me := viewer
	if me == "" {
		me = "Omar"
	}
	s := baseSnap(me, seats, mySeat)
	withMoney(&s)
	t := &s.Table
	t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = engine.Waiting, 3, 0, 1
	t.Board = parseCards("Kc 7h Qh 2s")
	t.Result = &engine.Result{HandNumber: 42, Board: t.Board, NoShowdown: true,
		Winnings: []engine.Winning{{Seat: 3, PlayerID: "key-Marta", Name: "Marta", Amount: 580}},
		Actions: []engine.ActionRecord{
			{PlayerID: "key-Sam", Street: engine.Preflop, Type: engine.Fold},
			{PlayerID: "key-Devon", Street: engine.Flop, Type: engine.Fold},
			{PlayerID: "key-Priya", Street: engine.Turn, Type: engine.Fold},
		},
		Revealed: []engine.Shown{
			{Seat: 2, PlayerID: "key-Devon", Name: "Devon", Cards: parseCards("As Kd")},
			{Seat: 1, PlayerID: "key-Sam", Name: "Sam", Cards: parseCards("Td")},
			{Seat: 3, PlayerID: "key-Marta", Name: "Marta", Cards: parseCards("Jh Jd")},
		},
	}
	s.NextHandAt = previewNow.Add(6 * time.Second)
	s.Log = append(s.Log, "Turn: K♣ 7♥ Q♥ [2♠]", "Marta bets 120", "Priya folds", "Marta wins 580 from the pot",
		"Hand #42 ends", "Devon shows A♠ K♦", "Sam shows T♦", "Marta shows J♥ J♦")
	if mySeat < 0 {
		s.Spectators = []string{"Omar"}
	}
	return s
}

// fxFoldedMayShow: Priya folded in the hand that just ended and showed
// nothing yet: the status line offers 1, 2 and 3.
func fxFoldedMayShow() Fixture {
	s := foldedResult("Priya")
	s.Table.Result.Revealed = nil
	for i := range s.Table.Seats {
		if i != 0 && i != 3 {
			s.Table.Seats[i].Cards, s.Table.Seats[i].HasCards = nil, false
		}
	}
	s.Table.Seats[3].Cards = nil // Marta did not show
	s.Log = s.Log[:len(s.Log)-3]
	return Fixture{Name: "folded after the hand, may show", Snap: s}
}

// fxFoldedShownResult: folded players' shown cards next to the winner's
// shown cards.
func fxFoldedShownResult(viewer string) Fixture {
	who := viewer
	if who == "" {
		who = "spectator"
	}
	return Fixture{Name: "result with folded players' shown cards and the winner's, " + who, Snap: foldedResult(viewer)}
}

// fxOwnFoldedShown: Priya folded and showed both cards after the hand. Her
// own seat draws them with the folded mark, and no show is offered.
func fxOwnFoldedShown() Fixture {
	s := foldedResult("Priya")
	s.Table.Result.Revealed = append(s.Table.Result.Revealed,
		engine.Shown{Seat: 0, PlayerID: "key-Priya", Name: "Priya", Cards: parseCards("9c 8c")})
	s.Log = append(s.Log, "Priya shows 9♣ 8♣")
	return Fixture{Name: "own folded hand shown after the hand", Snap: s}
}
