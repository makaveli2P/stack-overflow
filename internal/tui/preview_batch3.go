package tui

import (
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// batch3Fixtures are the screens of the batch 3 stats: the Fun and Host tabs.
func batch3Fixtures() []Fixture {
	return []Fixture{fxDashFun(), fxFunCard(), fxFunCardOther(), fxDashHost(), fxHostCard(), fxHostHelp()}
}

// fun sets the fun stats of one row.
type fun struct {
	badBeats  int
	worst     float64
	with      string
	river     int64
	riverCard string
	firstWin  int
	nemesis   string
	nemChips  int64
}

func (f fun) apply(p *server.PlayerStats) {
	p.BadBeats, p.WorstBeat, p.WorstBeatWith = f.badBeats, f.worst, parseCards(f.with)
	p.LuckiestRiver, p.FirstWinAfter = f.river, f.firstWin
	if f.riverCard != "" {
		p.RiverCard = parseCards(f.riverCard)[0]
	}
	p.Nemesis, p.NemesisChips = f.nemesis, f.nemChips
}

// funSnap is the 8-player dashboard with fun stats. The viewer is Devon.
// As the server does, only the viewer's own row has a favourite hand.
func funSnap() server.Snapshot {
	s := dashSnap()
	rows := map[string]fun{
		"Priya":       {badBeats: 1, worst: 0.82, with: "Ah Ad", river: 2140, riverCard: "7c", firstWin: 2, nemesis: "Sam", nemChips: 310},
		"Devon":       {badBeats: 3, worst: 0.955, with: "Ks Kc", river: 860, riverCard: "Qh", firstWin: 5, nemesis: "Priya", nemChips: 1240},
		"Marta":       {firstWin: 1, nemesis: "Leo", nemChips: 95},
		"Bartholomew": {badBeats: 1, worst: 0.71, with: "Js Ts", firstWin: 9, nemesis: "Marta", nemChips: 12},
		"Chen":        {nemesis: "Bartholomew", nemChips: 1290}, // a long name: the cell cuts it when narrow
		"Leo":         {badBeats: 12, worst: 1, with: "Qs Qd", river: 12340, riverCard: "Th", firstWin: 14, nemesis: "Priya", nemChips: 2310},
		"Sam":         {badBeats: 2, worst: 0.88, with: "As Kd", river: 410, riverCard: "2d", firstWin: 3, nemesis: "Devon", nemChips: 640},
		"Kim":         {},
	}
	for i := range s.Stats {
		rows[s.Stats[i].Name].apply(&s.Stats[i])
	}
	s.Stats[1].FavouriteHand, s.Stats[1].FavouriteCount = "AJs", 14
	return s
}

// hostSnap is funSnap seen by the host, with the host stats of every row.
func hostSnap() server.Snapshot {
	s := funSnap()
	s.Me.Host, s.Me.Admin = true, true
	hs := map[string]server.HostStats{
		"Priya": {ThreeBet: 9, ThreeBetChances: 41, FoldTo3Bet: 3, FoldTo3BetChances: 8, Steal: 14, StealChances: 30,
			Limp: 6, LimpChances: 120, CBet: 21, CBetChances: 28, FoldToCBet: 7, FoldToCBetChances: 19,
			CheckRaise: 2, CheckRaiseChances: 11, PostflopBetsRaises: 88, PostflopCalls: 35, NetBB: 62,
			SeatedSeconds: 5*3600 + 20*60, BiggestLoss: 1180, LongestWinStreak: 4, CurrentWinStreak: 1,
			AllInShowdowns: 7, AllInShowdownsWon: 5},
		"Devon": {ThreeBet: 4, ThreeBetChances: 37, FoldTo3Bet: 2, FoldTo3BetChances: 3, Steal: 6, StealChances: 25,
			Limp: 18, LimpChances: 110, CBet: 11, CBetChances: 15, FoldToCBet: 9, FoldToCBetChances: 14,
			CheckRaise: 1, CheckRaiseChances: 9, PostflopBetsRaises: 40, PostflopCalls: 38, NetBB: -15,
			SeatedSeconds: 4*3600 + 5*60, BiggestLoss: 960, LongestWinStreak: 3, AllInShowdowns: 4, AllInShowdownsWon: 1},
		"Marta": {ThreeBet: 1, ThreeBetChances: 30, StealChances: 18, Limp: 30, LimpChances: 90, CBet: 4, CBetChances: 9,
			FoldToCBet: 8, FoldToCBetChances: 10, CheckRaiseChances: 4, PostflopBetsRaises: 12, PostflopCalls: 40,
			NetBB: 18, SeatedSeconds: 2*3600 + 40*60, BiggestLoss: 400, LongestWinStreak: 2, AllInShowdowns: 1, AllInShowdownsWon: 1},
		"Bartholomew": {ThreeBetChances: 9, Steal: 2, StealChances: 4, Limp: 10, LimpChances: 30,
			PostflopBetsRaises: 6, NetBB: 5, SeatedSeconds: 50 * 60, BiggestLoss: 220, LongestWinStreak: 1},
		"Chen": {LimpChances: 10, Limp: 7, PostflopCalls: 5, NetBB: -12, SeatedSeconds: 9 * 60, BiggestLoss: 160},
		"Leo": {ThreeBet: 22, ThreeBetChances: 44, FoldTo3Bet: 1, FoldTo3BetChances: 14, Steal: 25, StealChances: 31,
			Limp: 3, LimpChances: 140, CBet: 38, CBetChances: 40, FoldToCBet: 2, FoldToCBetChances: 20,
			CheckRaise: 5, CheckRaiseChances: 12, PostflopBetsRaises: 150, PostflopCalls: 30, NetBB: -95,
			SeatedSeconds: 6*3600 + 10*60, BiggestLoss: 2600, LongestWinStreak: 5, CurrentWinStreak: 2,
			AllInShowdowns: 21, AllInShowdownsWon: 8},
		"Sam": {ThreeBet: 6, ThreeBetChances: 36, FoldTo3Bet: 4, FoldTo3BetChances: 6, Steal: 9, StealChances: 27,
			Limp: 12, LimpChances: 105, CBet: 15, CBetChances: 22, FoldToCBet: 10, FoldToCBetChances: 17,
			CheckRaise: 3, CheckRaiseChances: 10, PostflopBetsRaises: 55, PostflopCalls: 41, NetBB: 40,
			SeatedSeconds: 4*3600 + 45*60, BiggestLoss: 1500, LongestWinStreak: 3, AllInShowdowns: 6, AllInShowdownsWon: 3},
		"Kim": {},
	}
	for i := range s.Stats {
		h := hs[s.Stats[i].Name]
		// Every hand of the fixture is from batch 3 onward.
		h.Hands, h.NetChips = s.Stats[i].Hands, s.Stats[i].LifetimeNet
		s.Stats[i].Host = &h
	}
	return s
}

func fxDashFun() Fixture {
	return Fixture{Name: "dashboard fun tab, 8 players", Snap: funSnap(), Keys: []string{"d", "tab"}}
}

func fxFunCard() Fixture {
	return Fixture{Name: "dashboard fun card, own row", Snap: funSnap(), Keys: []string{"d", "tab", "enter"}}
}

func fxFunCardOther() Fixture {
	return Fixture{Name: "dashboard fun card, another player", Snap: funSnap(), Keys: []string{"d", "tab", "down", "down", "down", "down", "enter"}}
}

func fxDashHost() Fixture {
	return Fixture{Name: "dashboard host tab, 8 players", Snap: hostSnap(), Keys: []string{"d", "shift+tab"}}
}

func fxHostCard() Fixture {
	return Fixture{Name: "dashboard host card", Snap: hostSnap(), Keys: []string{"d", "shift+tab", "up", "enter"}}
}

func fxHostHelp() Fixture {
	return Fixture{Name: "help as the host", Snap: hostSnap(), Keys: []string{"?"}}
}
