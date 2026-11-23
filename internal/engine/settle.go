package engine

import (
	"slices"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// finish ends the hand: it shows the live hands when two or more remain,
// pays every pot and marks busted players. The caller returns the uncalled
// bet first.
func (t *Table) finish() {
	h := t.hand
	h.toAct = -1
	pots := buildPots(h.players, func(p *player) int64 { return p.committed })
	res := &Result{
		HandNumber: t.handNum,
		Board:      slices.Clone(h.board),
		Actions:    slices.Clone(h.actions),
		Order:      slices.Clone(h.order),
		BigBlind:   h.bigBlind,
		RunoutFrom: h.runoutFrom,
	}
	live := h.clockwise(h.button, (*player).live)
	if len(live) == 1 {
		res.NoShowdown = true
		res.CanRabbit = len(h.board) < 5 && h.deck.Len() >= rabbitCards(len(h.board))
		// A run-out that a stand ended: the winner's hand is face up since
		// the start of the run-out. Both cards count as shown, so ShowCards
		// and the TUI do not offer to show them again.
		if w := live[0]; h.tabled[w.seat] {
			w.revealed = [2]bool{true, true}
			res.Revealed = []Shown{{Seat: w.seat, PlayerID: w.id, Name: w.name, Cards: slices.Clone(w.hole)}}
		}
		for i, pot := range pots {
			t.award(res, i, len(pots), live, pot.Amount, nil)
		}
	} else {
		// The events of the settlement carry the street Showdown.
		h.street = Showdown
		values := t.showdown(h, res)
		for i, pot := range pots {
			t.award(res, i, len(pots), bestOf(h, pot.Eligible, values), pot.Amount, values)
		}
	}
	for _, p := range h.players {
		if p != nil {
			p.bet = 0
			res.Players = append(res.Players, p.summary(res.NoShowdown))
		}
	}
	h.result = res
	for _, p := range h.players {
		if p != nil && t.seats[p.seat] == p && p.stack == 0 {
			t.emit(EvBust, p.seat, p.id, 0, "%s is busted", p.name)
		}
	}
	t.emit(EvHandEnd, -1, "", 0, "Hand #%d ends", t.handNum)
	// The hand stops running only now, so that the bust and hand-end events
	// carry the street on which the hand ended.
	h.running = false
}

// summary returns p's part in the hand that just ended.
func (p *player) summary(noShowdown bool) PlayerSummary {
	s := PlayerSummary{
		PlayerID:       p.id,
		Name:           p.name,
		Seat:           p.seat,
		Committed:      p.committed,
		Won:            p.won,
		VPIP:           p.vpip,
		PFR:            p.pfr,
		SawFlop:        p.sawFlop,
		WentToShowdown: p.wentShowdown,
		WonAtShowdown:  !noShowdown && p.wentShowdown && p.won > 0,
		AllIn:          p.wasAllIn,
	}
	if p.wentShowdown {
		v := p.shownValue
		s.Hand = &v
		s.HandCards = p.shownBest
	}
	return s
}

// returnUncalled gives back the part of the highest commitment that no
// other player matched. Folded chips count as matched.
func (t *Table) returnUncalled(h *hand) {
	var top *player
	var high, second int64
	for _, p := range h.players {
		switch {
		case p == nil:
		case p.committed > high:
			top, high, second = p, p.committed, high
		case p.committed > second:
			second = p.committed
		}
	}
	if top == nil || !top.live() || high <= second {
		return
	}
	back := high - second
	top.committed -= back
	top.bet = max(0, top.bet-back)
	top.stack += back
	t.emit(EvReturn, top.seat, top.id, back, "Uncalled %d returned to %s", back, top.name)
}

// buildPots splits the chips into a main pot and side pots, level by level.
// The levels are the distinct amounts that live players put in. Folded chips
// fill the levels too, but folded players are not eligible. Folded chips
// above the top live level go into the last pot.
func buildPots(players []*player, put func(*player) int64) []Pot {
	var levels []int64
	var liveSeats []int
	for _, p := range players {
		if p != nil && p.live() {
			liveSeats = append(liveSeats, p.seat)
			if c := put(p); c > 0 && !slices.Contains(levels, c) {
				levels = append(levels, c)
			}
		}
	}
	slices.Sort(levels)
	var pots []Pot
	var prev int64
	for _, lv := range levels {
		pot := Pot{}
		for _, p := range players {
			if p == nil {
				continue
			}
			c := put(p)
			pot.Amount += max(0, min(c, lv)-prev)
			if p.live() && c >= lv {
				pot.Eligible = append(pot.Eligible, p.seat)
			}
		}
		pots = append(pots, pot)
		prev = lv
	}
	var extra int64
	for _, p := range players {
		if p != nil {
			extra += max(0, put(p)-prev)
		}
	}
	if extra > 0 {
		if len(pots) == 0 {
			pots = append(pots, Pot{Eligible: liveSeats})
		}
		pots[len(pots)-1].Amount += extra
	}
	return pots
}

// showdown shows every live hand, in order: the last river aggressor first,
// else the first live seat after the button, then clockwise.
func (t *Table) showdown(h *hand, res *Result) map[int]cards.HandValue {
	start := h.aggressor
	if start < 0 || !h.players[start].live() {
		start = h.next(h.button, (*player).live)
	}
	values := map[int]cards.HandValue{}
	for _, p := range h.clockwise(start-1, (*player).live) {
		v, five := cards.Best(append(slices.Clone(p.hole), h.board...))
		values[p.seat] = v
		h.shown[p.seat] = true
		p.wentShowdown, p.shownValue, p.shownBest = true, v, five
		res.Shown = append(res.Shown, Shown{
			Seat: p.seat, PlayerID: p.id, Name: p.name,
			Cards: slices.Clone(p.hole), Best: five, Hand: v.String(),
		})
		// A hand shown at the start of a paused run-out is not shown again.
		if !h.tabled[p.seat] {
			t.emit(EvShow, p.seat, p.id, 0, "%s shows %s — %s", p.name, cardsText(p.hole), v)
		}
	}
	return values
}

// bestOf returns the eligible players with the best hand, in odd-chip
// order: clockwise from the first seat after the button.
func bestOf(h *hand, eligible []int, values map[int]cards.HandValue) []*player {
	var best []*player
	for _, s := range eligible {
		p := h.players[s]
		switch {
		case len(best) == 0:
			best = []*player{p}
		case values[s].Compare(values[best[0].seat]) > 0:
			best = []*player{p}
		case values[s].Compare(values[best[0].seat]) == 0:
			best = append(best, p)
		}
	}
	return h.clockwiseOrder(best)
}

// clockwiseOrder sorts players by seat, starting after the button.
func (h *hand) clockwiseOrder(ps []*player) []*player {
	in := func(p *player) bool { return slices.Contains(ps, p) }
	return h.clockwise(h.button, in)
}

// award splits amount equally among winners. The odd chips go one at a time
// to the winners in the given order. values is nil when nobody showed.
func (t *Table) award(res *Result, potIndex, potCount int, winners []*player, amount int64, values map[int]cards.HandValue) {
	if amount <= 0 || len(winners) == 0 {
		return
	}
	share := amount / int64(len(winners))
	odd := amount % int64(len(winners))
	for i, p := range winners {
		won := share
		if int64(i) < odd {
			won++
		}
		p.stack += won
		p.won += won
		w := Winning{Seat: p.seat, PlayerID: p.id, Name: p.name, Amount: won, PotIndex: potIndex}
		if values != nil {
			w.Hand = values[p.seat].String()
		}
		res.Winnings = append(res.Winnings, w)
		t.emit(EvWin, p.seat, p.id, won, "%s", winText(w, potCount))
	}
}
