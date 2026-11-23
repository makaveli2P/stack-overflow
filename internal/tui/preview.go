package tui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Fixture is a hand-made screen state. Tests and cmd/tuipreview draw it.
type Fixture struct {
	Name string
	Snap server.Snapshot
	Keys []string // keys pressed after the snapshot arrives, e.g. "r", "3"
	// Panic shows the panic screen after the keys: 1 is the notice, 2 is
	// the decoy. Render sends a second snapshot with a higher PanicSeq.
	Panic int
	// newChat holds chat messages that come after the keys, in a second
	// snapshot. They are new on the screen, so they get the highlight.
	newChat []server.ChatLine
}

// Render draws a fixture at w x h with the renderer r.
func Render(f Fixture, r *lipgloss.Renderer, w, h int) string {
	m := newModelWith(nopSession{}, r, w, h, func() time.Time { return f.Snap.Now })
	m.Update(snapshotMsg{f.Snap})
	for _, k := range f.Keys {
		m.Update(keyMsg(k))
	}
	if len(f.newChat) > 0 {
		s := f.Snap
		s.Chat = slices.Concat(s.Chat, f.newChat)
		m.Update(snapshotMsg{s})
	}
	if f.Panic > 0 {
		s := f.Snap
		s.PanicSeq++
		m.Update(snapshotMsg{s})
		if f.Panic > 1 {
			m.Update(panicNoticeDoneMsg{m.panic.gen})
		}
	}
	return m.View()
}

// nopSession is a session that does nothing; previews use it.
type nopSession struct{}

func (nopSession) Updates() <-chan server.Snapshot     { return nil }
func (nopSession) Sit(int) error                       { return nil }
func (nopSession) SitWith(int, server.SitChoice) error { return nil }
func (nopSession) Stand() error                        { return nil }
func (nopSession) Act(engine.Action) error             { return nil }
func (nopSession) SitOut(bool) error                   { return nil }
func (nopSession) Rebuy() error                        { return nil }
func (nopSession) Chat(string) error                   { return nil }
func (nopSession) ShareShot(string) (bool, error)      { return false, nil }
func (nopSession) Close()                              {}
func (nopSession) SetName(string) error                { return nil }
func (nopSession) LinkPIN(string, string) error        { return nil }
func (nopSession) RabbitHunt() error                   { return nil }
func (nopSession) ShowCards(bool, bool) error          { return nil }
func (nopSession) SetAlerts(bool) error                { return nil }
func (nopSession) MarkSettled() error                  { return nil }
func (nopSession) Extend() error                       { return nil }
func (nopSession) Panic() error                        { return nil }
func (nopSession) Start() error                        { return nil }

// keyMsg builds the key message for a key name such as "enter" or "r".
func keyMsg(k string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"left": tea.KeyLeft, "right": tea.KeyRight, "up": tea.KeyUp, "down": tea.KeyDown,
		"backspace": tea.KeyBackspace, "ctrl+c": tea.KeyCtrlC, "ctrl+h": tea.KeyCtrlH, "f20": tea.KeyF20, "ctrl+u": tea.KeyCtrlU, " ": tea.KeySpace,
	}
	if t, ok := named[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// previewNow is the fixed server time of every fixture.
var previewNow = time.Date(2026, 11, 17, 14, 30, 0, 0, time.UTC)

// parseCards reads cards such as "As Kh Td 2c" without the cards package parser.
func parseCards(s string) []cards.Card {
	ranks := map[byte]cards.Rank{'2': 2, '3': 3, '4': 4, '5': 5, '6': 6, '7': 7, '8': 8, '9': 9,
		'T': cards.Ten, 'J': cards.Jack, 'Q': cards.Queen, 'K': cards.King, 'A': cards.Ace}
	suits := map[byte]cards.Suit{'c': cards.Clubs, 'd': cards.Diamonds, 'h': cards.Hearts, 's': cards.Spades}
	var out []cards.Card
	for _, f := range strings.Fields(s) {
		out = append(out, cards.Card{Rank: ranks[f[0]], Suit: suits[f[1]]})
	}
	return out
}

func player(i int, name string, stack int64) engine.SeatView {
	return engine.SeatView{Index: i, PlayerID: "key-" + name, Name: name, Stack: stack}
}

func dealt(sv engine.SeatView, bet, committed int64, action string) engine.SeatView {
	sv.InHand, sv.HasCards, sv.Bet, sv.Committed, sv.LastAction = true, true, bet, committed, action
	return sv
}

// table fills empty seats up to n.
func table(n int, seated ...engine.SeatView) []engine.SeatView {
	seats := make([]engine.SeatView, n)
	for i := range seats {
		seats[i] = engine.SeatView{Index: i, Empty: true}
	}
	for _, s := range seated {
		seats[s.Index] = s
	}
	return seats
}

func baseSnap(me string, seats []engine.SeatView, mySeat int) server.Snapshot {
	cfg := server.Config{Seats: len(seats), SmallBlind: 10, BigBlind: 20, StartingStack: 2000,
		TurnTimeout: 30 * time.Second, NextHandDelay: 5 * time.Second, Extensions: 3, ExtensionTime: 30 * time.Second}
	var board []server.Standing
	nets := []int64{1240, 380, 0, -150, -420, -2000, 75, 610, -310}
	for i, s := range seats {
		if s.Empty {
			continue
		}
		board = append(board, server.Standing{Name: s.Name, Net: nets[i%len(nets)], BuyIns: 1 + i%3, Online: true, Playing: true})
	}
	bank := int64(0)
	if mySeat < 0 {
		bank = 2000
	}
	return server.Snapshot{
		Table: engine.View{HandNumber: 42, SmallBlind: 10, BigBlind: 20, Button: -1,
			SmallBlindSeat: -1, BigBlindSeat: -1, ToAct: -1, Seats: seats, MySeat: mySeat},
		Log: []string{
			"Hand #41 — Devon has the button", "Priya posts small blind 10", "Sam posts big blind 20",
			"Flop: K♠ 7♥ 2♦", "Priya bets 40", "Sam raises to 120", "Priya calls 80",
			"Turn: K♠ 7♥ 2♦ [9♣]", "Priya checks", "Sam bets 200", "Priya folds",
			"Uncalled 200 returned to Sam", "Sam wins 280 from the main pot", "Hand #42 — Priya has the button",
		},
		Chat: []server.ChatLine{
			{At: previewNow.Add(-5 * time.Minute), Name: "Priya", Text: "who ordered pizza?"},
			{At: previewNow.Add(-3 * time.Minute), Name: "Sam", Text: "nice hand, that river was brutal"},
			{At: previewNow.Add(-1 * time.Minute), Name: "Marta", Text: "brb coffee ☕"},
		},
		Leaderboard: board,
		Me:          server.Me{Key: "key-" + me, Name: me, Seat: mySeat, Bank: bank, ExtensionsLeft: cfg.Extensions},
		Now:         previewNow,
		Config:      cfg,
	}
}

func clockOn(s *server.Snapshot, seat int, left time.Duration) {
	s.Table.ToAct = seat
	s.Deadline = previewNow.Add(left)
}

// Fixtures returns the preview screens, from heads-up to a full table.
func Fixtures() []Fixture {
	return append([]Fixture{
		headsUpPreflop(), sixFlopWaiting(), nineRiverRaise(), nineShowdown(),
		spectator(), busted(), nineFlopBet(), chatOpen(), emptyRoom(),
	}, slices.Concat(batch2Fixtures(), batch3Fixtures(), batch4Fixtures(), batch5Fixtures(), batch6Fixtures(), batch7Fixtures(), batch8Fixtures(), batch9Fixtures(), batch10Fixtures(), batch11Fixtures())...)
}

func headsUpPreflop() Fixture {
	me := dealt(player(0, "Devon", 1990), 10, 10, "")
	me.Cards = parseCards("Ah Kh")
	opp := dealt(player(1, "Priya", 1580), 20, 20, "")
	s := baseSnap("Devon", table(2, me, opp), 0)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.Preflop, 0, 0, 1
	t.CurrentBet, t.MinRaiseTo, t.TotalPot = 20, 40, 30
	t.Legal = &engine.Legal{Fold: true, Call: true, CallAmount: 10, Raise: true, MinTo: 40, MaxTo: 2000, AllIn: true, AllInTo: 2000}
	clockOn(&s, 0, 22*time.Second)
	return Fixture{Name: "heads-up preflop, my turn", Snap: s}
}

func sixFlopWaiting() Fixture {
	me := dealt(player(2, "Devon", 1460), 0, 60, "Call 60")
	me.Cards = parseCards("Qs Qd")
	seats := table(6,
		dealt(player(0, "Priya", 2310), 0, 60, "Check"),
		func() engine.SeatView {
			s := dealt(player(1, "Sam", 980), 0, 20, "Fold")
			s.Folded, s.HasCards = true, false
			return s
		}(),
		me,
		dealt(player(3, "Marta", 1200), 80, 140, "Bet 80"),
		func() engine.SeatView { s := player(5, "Leo", 2000); s.SittingOut = true; return s }(),
	)
	s := baseSnap("Devon", seats, 2)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.Flop, 3, 0, 1
	t.Board = parseCards("Kc 7h Qh")
	t.Pots = []engine.Pot{{Amount: 200, Eligible: []int{0, 2, 3}}}
	t.CurrentBet, t.TotalPot = 80, 280
	clockOn(&s, 0, 9*time.Second)
	return Fixture{Name: "6 seats, flop, waiting for another player", Snap: s}
}

func nineSeats() []engine.SeatView {
	allIn := dealt(player(6, "Chen", 0), 340, 540, "All-in 340")
	allIn.AllIn = true
	return table(9,
		dealt(player(0, "Devon", 4250), 0, 400, "Call 200"),
		dealt(player(1, "Priya", 3120), 600, 1000, "Raise to 600"),
		func() engine.SeatView {
			s := dealt(player(2, "Sam", 870), 0, 20, "Fold")
			s.Folded, s.HasCards = true, false
			return s
		}(),
		player(3, "Marta", 1500),
		func() engine.SeatView { s := player(4, "Leo", 0); s.Busted = true; return s }(),
		func() engine.SeatView { s := player(5, "Aiko", 2000); s.SittingOut = true; return s }(),
		allIn,
		dealt(player(7, "Bartholomew", 4400), 0, 400, "Call 200"),
	)
}

func nineRiverRaise() Fixture {
	seats := nineSeats()
	seats[0].Cards = parseCards("Js Ts")
	s := baseSnap("Devon", seats, 0)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.River, 7, 0, 1
	t.Board = parseCards("9s 8s 2d Kh Qs")
	t.Pots = []engine.Pot{{Amount: 820, Eligible: []int{0, 1, 6, 7}}, {Amount: 600, Eligible: []int{0, 1, 7}}}
	t.CurrentBet, t.TotalPot = 600, 820+600+600+340
	t.Legal = &engine.Legal{Fold: true, Call: true, CallAmount: 600, Raise: true, MinTo: 1200, MaxTo: 4250, AllIn: true, AllInTo: 4250}
	s.Leaderboard[1].Online = false
	clockOn(&s, 0, 6*time.Second)
	return Fixture{Name: "9 seats, river, raise editor (pot preset)", Snap: s, Keys: []string{"r", "p"}}
}

func nineShowdown() Fixture {
	seats := nineSeats()
	for i := range seats {
		seats[i].Bet, seats[i].LastAction = 0, ""
	}
	seats[0].Cards = parseCards("Js Ts")
	seats[1].Cards = parseCards("Ad Kd")
	seats[6].Cards = parseCards("8h 8c")
	seats[7].Cards = parseCards("Qc Qd")
	s := baseSnap("Devon", seats, 0)
	t := &s.Table
	t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = engine.Waiting, 7, 0, 1
	t.Board = parseCards("9s 8s 2d Kh Qs")
	t.Result = &engine.Result{HandNumber: 42, Board: t.Board,
		Winnings: []engine.Winning{
			{Seat: 0, PlayerID: "key-Devon", Name: "Devon", Amount: 2160, PotIndex: 0, Hand: "Straight Flush, Queen high"},
			{Seat: 0, PlayerID: "key-Devon", Name: "Devon", Amount: 1200, PotIndex: 1, Hand: "Straight Flush, Queen high"},
		},
		Shown: []engine.Shown{
			{Seat: 0, Name: "Devon", Cards: seats[0].Cards, Best: [5]cards.Card(parseCards("Qs Js Ts 9s 8s")), Hand: "Straight Flush, Queen high"},
			{Seat: 1, Name: "Priya", Cards: seats[1].Cards, Hand: "Pair of Kings"},
			{Seat: 6, Name: "Chen", Cards: seats[6].Cards, Hand: "Three of a Kind, Eights"},
			{Seat: 7, Name: "Bartholomew", Cards: seats[7].Cards, Hand: "Three of a Kind, Queens"},
		}}
	s.NextHandAt = previewNow.Add(4 * time.Second)
	s.Log = append(s.Log, "Devon shows J♠ 10♠ — Straight Flush, Queen high", "Devon wins 2,160 from the main pot with Straight Flush, Queen high")
	return Fixture{Name: "9 seats, showdown result", Snap: s}
}

func spectator() Fixture {
	f := sixFlopWaiting()
	s := f.Snap
	s.Table.Seats = append([]engine.SeatView(nil), s.Table.Seats...)
	s.Table.Seats[2] = dealt(player(2, "Nadia", 1460), 0, 60, "Call 60")
	s.Table.MySeat = -1
	s.Me = server.Me{Key: "key-Omar", Name: "Omar", Seat: -1, Bank: 0}
	s.Spectators = []string{"Omar", "Kim"}
	s.Leaderboard = append(s.Leaderboard, server.Standing{Name: "Omar", Online: true}, server.Standing{Name: "Kim", Net: -200, BuyIns: 1, Online: true})
	return Fixture{Name: "spectator, 6 seats, flop", Snap: s}
}

func busted() Fixture {
	me := player(1, "Devon", 0)
	me.Busted = true
	s := baseSnap("Devon", table(6, player(0, "Priya", 3400), me, player(3, "Sam", 600)), 1)
	s.Table.Button = 0
	s.NextHandAt = previewNow.Add(3 * time.Second)
	return Fixture{Name: "busted, between hands", Snap: s}
}

func nineFlopBet() Fixture {
	seats := table(9)
	names := []string{"Devon", "Priya", "Sam", "Marta", "Leo", "Aiko", "Chen", "Bartholomew", "Kim"}
	for i, n := range names {
		seats[i] = dealt(player(i, n, int64(1500+i*137)), 0, 40, "Check")
	}
	seats[0].Cards = parseCards("Ac 5c")
	seats[0].LastAction = ""
	s := baseSnap("Devon", seats, 0)
	t := &s.Table
	t.InHand, t.Street, t.Button, t.SmallBlindSeat, t.BigBlindSeat = true, engine.Flop, 8, 0, 1
	t.Board = parseCards("Tc 4c Kd")
	t.Pots = []engine.Pot{{Amount: 360}}
	t.TotalPot = 360
	t.Legal = &engine.Legal{Fold: true, Check: true, Bet: true, MinTo: 20, MaxTo: 1500, AllIn: true, AllInTo: 1500}
	clockOn(&s, 0, 27*time.Second)
	return Fixture{Name: "9 seats full, flop, my turn to bet", Snap: s}
}

func chatOpen() Fixture {
	f := sixFlopWaiting()
	f.Name = "chat input open on the chat tab"
	f.Keys = []string{"t", "g", "g", " ", "w", "p"} // Chat is the default tab
	return f
}

func emptyRoom() Fixture {
	s := baseSnap("Devon", table(9, player(0, "Devon", 2000)), 0)
	s.Log = nil
	s.Chat = nil
	s.Table.HandNumber = 0
	return Fixture{Name: "first player alone at a 9-seat table", Snap: s}
}
