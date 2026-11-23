package tui

import (
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch2Fixtures are the screens of the batch 2 features.
func batch2Fixtures() []Fixture {
	return []Fixture{
		fxDashStats(), dashDetail(), fxDashSettle(), dashSettleConfirm(),
		welcome(), welcomeKeyless(), linkForm(), profileBox(), profileRename(),
		rabbitCards(), revealedCards(), winnerCanShow(),
		hintTurn(), hintRiver(),
		editorPreset("h"), editorPreset("t"), editorPreset("a"),
	}
}

// money settings of the batch 2 defaults: 2 chips are ₹1.
func withMoney(s *server.Snapshot) {
	s.Config.ChipsPerUnit, s.Config.Currency = 2, "₹"
	s.Me.Alerts = true
}

func statsRow(name string, online bool, periodNet, lifetime int64, hands, won, vpip, pfr, flop, sd, sdWon, allIns, buyIns int, biggest int64, best string, bestCards string) server.PlayerStats {
	return server.PlayerStats{
		Name: name, Online: online, Playing: online,
		Hands: hands, HandsWon: won, VPIP: vpip, PFR: pfr, SawFlop: flop, Showdowns: sd, ShowdownsWon: sdWon,
		AllIns: allIns, BiggestPot: biggest, BestHand: best, BestCards: parseCards(bestCards),
		LifetimeNet: lifetime, BuyIns: buyIns, PeriodNet: periodNet, PeriodBuyIns: 1 + buyIns/3, PeriodHands: hands / 3,
	}
}

// dashSnap is a 6-seat table with eight players in the stats.
func dashSnap() server.Snapshot {
	s := sixFlopWaiting().Snap
	withMoney(&s)
	s.Stats = []server.PlayerStats{
		statsRow("Priya", true, 1240, 3400, 182, 41, 56, 33, 61, 22, 12, 9, 7, 4210, "Full House, Kings over Sevens", "Ks Kh Kd 7c 7h"),
		statsRow("Devon", true, 380, -150, 164, 33, 41, 22, 52, 18, 10, 6, 5, 2160, "Straight Flush, Queen high", "Qs Js Ts 9s 8s"),
		statsRow("Marta", true, 75, 610, 120, 25, 30, 12, 40, 11, 5, 3, 3, 1480, "Flush, Ace high", "Ah Jh 9h 4h 2h"),
		statsRow("Bartholomew", false, 0, 260, 37, 6, 15, 4, 12, 3, 1, 1, 1, 610, "Two Pair, Queens and Fives", "Qc Qd 5s 5h Ad"),
		statsRow("Chen", false, -310, -310, 12, 1, 7, 2, 4, 0, 0, 2, 2, 140, "", ""),
		statsRow("Leo", true, -420, -1900, 201, 30, 99, 61, 80, 30, 9, 21, 12, 3900, "Three of a Kind, Eights", "8h 8c 8d Ks 2c"),
		statsRow("Sam", true, -700, 1220, 158, 29, 47, 25, 55, 20, 11, 8, 6, 2950, "Straight, Ten high", "Ts 9d 8c 7h 6s"),
		statsRow("Kim", false, -265, -265, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, "", ""),
	}
	since := previewNow.Add(-26 * time.Hour)
	s.Settle = server.Settlement{
		Period: 3, Since: since, ChipsPerUnit: 2, Currency: "₹",
		Balances: []server.Balance{
			{Name: "Priya", Chips: 1240, Money: 62000},
			{Name: "Devon", Chips: 380, Money: 19000},
			{Name: "Marta", Chips: 75, Money: 3750},
			{Name: "Kim", Chips: -265, Money: -13250},
			{Name: "Chen", Chips: -310, Money: -15500},
			{Name: "Leo", Chips: -420, Money: -21000},
			{Name: "Sam", Chips: -700, Money: -35000},
		},
		Transfers: []server.Transfer{
			{From: "Sam", To: "Priya", Amount: 35000},
			{From: "Leo", To: "Priya", Amount: 21000},
			{From: "Chen", To: "Devon", Amount: 15500},
			{From: "Kim", To: "Priya", Amount: 6000},
			{From: "Kim", To: "Devon", Amount: 3500},
			{From: "Kim", To: "Marta", Amount: 3750},
		},
		History: []server.PastPeriod{
			{Period: 2, Since: since.Add(-7 * 24 * time.Hour), SettledAt: since, Currency: "₹",
				Transfers: []server.Transfer{{From: "Leo", To: "Sam", Amount: 125050}, {From: "Devon", To: "Sam", Amount: 26025}}},
			{Period: 1, Since: since.Add(-14 * 24 * time.Hour), SettledAt: since.Add(-7 * 24 * time.Hour), Currency: "₹"},
		},
	}
	return s
}

func fxDashStats() Fixture {
	return Fixture{Name: "dashboard stats tab, 8 players", Snap: dashSnap(), Keys: []string{"d"}}
}

func dashDetail() Fixture {
	return Fixture{Name: "dashboard detail card", Snap: dashSnap(), Keys: []string{"d", "up", "enter"}}
}

func fxDashSettle() Fixture {
	return Fixture{Name: "dashboard settle up, transfers and history", Snap: dashSnap(), Keys: []string{"d", "tab", "tab"}}
}

func dashSettleConfirm() Fixture {
	s := dashSnap()
	s.Me.Admin = true
	return Fixture{Name: "dashboard settle up, admin types yes", Snap: s, Keys: []string{"d", "tab", "tab", "m", "y", "e"}}
}

// newcomer is the snapshot of a brand-new account at a 6-seat table.
func newcomer(name string) server.Snapshot {
	s := spectator().Snap
	withMoney(&s)
	s.Me = server.Me{Key: "key:SHA256:abc", Name: name, Seat: -1, NeedsName: true, CanLink: true, Alerts: true}
	return s
}

func welcome() Fixture {
	return Fixture{Name: "welcome screen, new SSH key", Snap: newcomer("devon")}
}

func welcomeKeyless() Fixture {
	s := newcomer("nadia")
	s.Me.Key, s.Me.CanLink, s.Me.Keyless = "pin:nadia", false, true
	return Fixture{Name: "welcome screen, PIN login", Snap: s, Keys: []string{"backspace", "backspace", "backspace", "backspace", "backspace", "Nadia K"}}
}

func linkForm() Fixture {
	return Fixture{Name: "link form from the welcome screen", Snap: newcomer("devon"),
		Keys: []string{"tab", "devon", "enter", "12"}}
}

func profileBox() Fixture {
	s := sixFlopWaiting().Snap
	withMoney(&s)
	s.Me.Admin = true
	s.Me.Bank = 500
	return Fixture{Name: "profile box (M)", Snap: s, Keys: []string{"m"}}
}

func profileRename() Fixture {
	s := spectator().Snap
	withMoney(&s)
	s.Me.Key, s.Me.Keyless = "pin:omar", true
	return Fixture{Name: "profile box rename, PIN login", Snap: s, Keys: []string{"m", "r", "backspace", "backspace", "ar S."}}
}

// foldedOut is a 6-seat hand that ended on the flop when everyone folded
// to Marta.
func foldedOut(viewer string) server.Snapshot {
	me := dealt(player(2, "Devon", 1400), 0, 0, "")
	me.Cards = parseCards("Qs Qd")
	marta := dealt(player(3, "Marta", 1460), 0, 0, "")
	priya := player(0, "Priya", 2250)
	sam := player(1, "Sam", 960)
	seats := table(6, priya, sam, me, marta, func() engine.SeatView { s := player(5, "Leo", 2000); s.SittingOut = true; return s }())
	for _, i := range []int{0, 1, 2} {
		seats[i].HasCards, seats[i].Cards = false, nil
	}
	mySeat := 2
	if viewer == "Marta" {
		seats[3].Cards = parseCards("As 4d")
		mySeat = 3
	}
	s := baseSnap(viewer, seats, mySeat)
	withMoney(&s)
	t := &s.Table
	t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = engine.Waiting, 3, 0, 1
	t.Board = parseCards("Kc 7h Qh")
	t.Result = &engine.Result{HandNumber: 42, Board: t.Board, NoShowdown: true, CanRabbit: true,
		Winnings: []engine.Winning{{Seat: 3, PlayerID: "key-Marta", Name: "Marta", Amount: 260}}}
	s.NextHandAt = previewNow.Add(6 * time.Second)
	s.Log = append(s.Log, "Devon folds", "Marta wins 260 from the main pot")
	return s
}

func rabbitCards() Fixture {
	s := foldedOut("Devon")
	s.Table.Seats[2].HasCards = false
	s.Table.Result.CanRabbit = false
	s.Table.Result.Rabbit = parseCards("Qc 2s")
	s.Log = append(s.Log, "Rabbit hunt (Devon): [Q♣] [2♠]")
	return Fixture{Name: "rabbit hunt cards on the board", Snap: s}
}

func revealedCards() Fixture {
	s := foldedOut("Devon")
	s.Table.Seats[2].HasCards = false
	s.Table.Result.Revealed = []engine.Shown{{Seat: 3, PlayerID: "key-Marta", Name: "Marta", Cards: parseCards("As")}}
	s.Log = append(s.Log, "Marta shows A♠")
	return Fixture{Name: "winner showed one card", Snap: s}
}

func winnerCanShow() Fixture {
	return Fixture{Name: "winner may show cards and hunt the rabbit", Snap: foldedOut("Marta")}
}

func hintTurn() Fixture {
	me := dealt(player(0, "Devon", 1720), 0, 280, "Check")
	me.Cards = parseCards("Kh 7c")
	opp := dealt(player(1, "Priya", 1340), 0, 280, "")
	s := baseSnap("Devon", table(2, me, opp), 0)
	withMoney(&s)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.Turn, 1, 1, 0
	t.Board = parseCards("Ks 7d 2c 9h")
	t.Pots = []engine.Pot{{Amount: 560}}
	t.TotalPot = 560
	clockOn(&s, 1, 18*time.Second)
	return Fixture{Name: "hand hint on the turn, two pair", Snap: s}
}

func hintRiver() Fixture {
	f := hintTurn()
	s := f.Snap
	s.Table.Seats = append([]engine.SeatView(nil), s.Table.Seats...)
	s.Table.Seats[0].Cards = parseCards("Ah 5h")
	s.Table.Seats[0].LastAction = ""
	s.Table.Board = parseCards("Kh 7d 2h 9h Jc")
	s.Table.Street = engine.River
	s.Table.Legal = &engine.Legal{Fold: true, Check: true, Bet: true, MinTo: 20, MaxTo: 1720, AllIn: true, AllInTo: 1720}
	clockOn(&s, 0, 25*time.Second)
	return Fixture{Name: "hand hint on the river, flush, my turn", Snap: s}
}

func editorPreset(key string) Fixture {
	f := headsUpPreflop()
	withMoney(&f.Snap)
	names := map[string]string{"h": "half pot", "t": "two thirds pot", "a": "all-in"}
	f.Name = "raise editor, " + key + " preset (" + names[key] + ")"
	f.Keys = []string{"r", key}
	return f
}
