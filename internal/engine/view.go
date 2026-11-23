package engine

import "slices"

func (t *Table) view(viewerID string) View {
	v := View{
		HandNumber:     t.handNum,
		SmallBlind:     t.cfg.SmallBlind,
		BigBlind:       t.cfg.BigBlind,
		Button:         t.button,
		SmallBlindSeat: -1,
		BigBlindSeat:   -1,
		ToAct:          -1,
		MySeat:         -1,
		Seats:          make([]SeatView, len(t.seats)),
	}
	h := t.hand
	if h != nil {
		v.SmallBlindSeat, v.BigBlindSeat = h.sb, h.bb
		v.Board = slices.Clone(h.board)
		v.Result = h.result.clone()
	}
	for i, p := range t.seats {
		v.Seats[i] = t.seatView(i, p, viewerID)
		if p != nil && viewerID != "" && p.id == viewerID {
			v.MySeat = i
		}
	}
	if h := t.running(); h != nil {
		t.fillRunning(&v, h)
	}
	return v
}

// fillRunning adds the betting state of the hand in progress.
func (t *Table) fillRunning(v *View, h *hand) {
	v.InHand = true
	v.Runout = h.runout
	v.Street = h.street
	v.ToAct = h.toAct
	v.CurrentBet = h.currentBet
	// A player who left the table is not in any SeatView, so that player's
	// street bet counts as already in the pots.
	v.Pots = buildPots(h.players, func(p *player) int64 {
		if t.seats[p.seat] == p {
			return p.committed - p.bet
		}
		return p.committed
	})
	for _, pot := range v.Pots {
		v.TotalPot += pot.Amount
	}
	for _, s := range v.Seats {
		v.TotalPot += s.Bet
	}
	if h.toAct < 0 {
		return
	}
	actor := h.players[h.toAct]
	l := h.legal(actor)
	v.MinRaiseTo = l.MinTo
	if v.MySeat == h.toAct && t.seats[h.toAct] == actor {
		v.Legal = &l
	}
}

func (t *Table) seatView(i int, p *player, viewerID string) SeatView {
	sv := SeatView{Index: i, Empty: p == nil}
	if p == nil {
		return sv
	}
	sv.PlayerID, sv.Name = p.id, p.name
	sv.Stack, sv.SittingOut = p.stack, p.sittingOut
	h := t.hand
	dealtIn := h != nil && h.players[i] == p
	running := dealtIn && h.running
	sv.Busted = p.stack == 0 && !running
	if !dealtIn {
		return sv
	}
	sv.LastAction = p.lastAction
	own := viewerID != "" && viewerID == p.id
	if running {
		sv.InHand = true
		sv.Bet, sv.Committed = p.bet, p.committed
		sv.Folded = p.folded
		sv.AllIn = p.live() && p.stack == 0
		sv.HasCards = p.live()
		// During a paused run-out every live hand is face up.
		if own || (h.tabled[i] && p.live()) {
			sv.Cards = slices.Clone(p.hole)
		}
		return sv
	}
	// After the hand the NoShowdown winner and the players who folded can
	// show cards (ShowCards). When both cards are shown, everyone sees them
	// here. When only one card is shown, Cards stays nil for other viewers
	// (Cards is len 2 or nil) and HasCards is true: the TUI draws the shown
	// card from Result.Revealed and a card back for the other one. A
	// folded hand that nobody showed stays hidden from everyone but its
	// owner, also after the hand.
	both := p.revealed[0] && p.revealed[1]
	switch {
	case own || h.shown[i] || both || (h.tabled[i] && p.live()):
		sv.Cards = slices.Clone(p.hole)
		sv.HasCards = p.live()
	case p.revealed[0] || p.revealed[1]:
		sv.HasCards = true
	}
	return sv
}

func (r *Result) clone() *Result {
	if r == nil {
		return nil
	}
	c := *r
	c.Board = slices.Clone(r.Board)
	c.Winnings = slices.Clone(r.Winnings)
	c.Shown = slices.Clone(r.Shown)
	for i := range c.Shown {
		c.Shown[i].Cards = slices.Clone(r.Shown[i].Cards)
	}
	c.Players = slices.Clone(r.Players)
	for i := range c.Players {
		if hv := r.Players[i].Hand; hv != nil {
			v := *hv
			c.Players[i].Hand = &v
		}
	}
	c.Rabbit = slices.Clone(r.Rabbit)
	c.Actions = slices.Clone(r.Actions)
	c.Order = slices.Clone(r.Order)
	c.Revealed = slices.Clone(r.Revealed)
	for i := range c.Revealed {
		c.Revealed[i].Cards = slices.Clone(r.Revealed[i].Cards)
	}
	return &c
}
