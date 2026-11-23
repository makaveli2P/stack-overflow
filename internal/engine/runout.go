package engine

// This file holds the paused run-out (Config.StepRunout). When the betting
// ends with two or more live players and fewer than 5 board cards, the
// engine stops before it deals the rest of the board. Every live hand is
// face up, and the caller deals each street with DealRunout.

// runoutPending reports whether a paused run-out waits for DealRunout.
func (t *Table) runoutPending() bool {
	h := t.running()
	return h != nil && h.runout
}

// startRunout pauses the hand at the start of a run-out. The caller sets
// toAct to -1, returns the uncalled bet and sets runoutFrom first. The
// street bets go into the pots, and every live player shows the hole cards
// in showdown order. The final showdown does not show these hands again.
func (t *Table) startRunout(h *hand) {
	h.runout = true
	h.currentBet = 0
	for _, p := range h.players {
		if p != nil {
			p.bet = 0
		}
	}
	// After the run-out, the showdown starts at the first live seat after
	// the button (dealStreet clears the aggressor), so the order here is
	// the same as the order of Result.Shown.
	start := h.next(h.button, (*player).live)
	for _, p := range h.clockwise(start-1, (*player).live) {
		h.tabled[p.seat] = true
		t.emit(EvShow, p.seat, p.id, 0, "%s shows %s", p.name, cardsText(p.hole))
	}
}

// dealRunout deals the next street of a paused run-out. After the river it
// ends the hand.
func (t *Table) dealRunout() error {
	h := t.running()
	if h == nil || !h.runout {
		return ErrNotNow
	}
	t.dealStreet(h)
	if len(h.board) == 5 {
		h.runout = false
		t.finish()
	}
	return nil
}

// runoutFold moves a paused run-out on after a live player left it. When
// only one live player is left, the hand ends and that player wins. Else
// the run-out stays paused.
func (t *Table) runoutFold(h *hand) {
	if h.count((*player).live) > 1 {
		return
	}
	h.runout = false
	t.returnUncalled(h)
	t.finish()
}
