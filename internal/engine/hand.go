package engine

import (
	"fmt"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// hand is the state of one deal.
type hand struct {
	running  bool
	deck     *cards.Deck
	players  []*player // by seat; nil when the seat is not dealt in
	button   int
	sb, bb   int
	street   Street
	board    []cards.Card
	bigBlind int64

	currentBet int64 // the highest street bet
	minRaise   int64 // size of the last full bet or raise on this street
	toAct      int   // seat, -1 when nobody must act
	aggressor  int   // seat of the last bet or raise on this street, -1

	shown  []bool // by seat: hole cards shown at showdown
	result *Result

	// Batch-3 facts for the Result.
	actions    []ActionRecord // every decision, in order
	order      []string       // player ids from the small blind, preflop seat order
	runoutFrom int            // board cards out when a run-out started; 5 when none

	// Batch-6 paused run-out (Config.StepRunout).
	runout bool   // a run-out waits for DealRunout
	tabled []bool // by seat: hole cards shown face up at the start of a run-out
}

// dealt matches every player in the hand.
func dealt(*player) bool { return true }

// next returns the first dealt seat after from (clockwise) whose player
// matches ok, or -1.
func (h *hand) next(from int, ok func(*player) bool) int {
	return nextIndex(len(h.players), from, func(i int) bool {
		return h.players[i] != nil && ok(h.players[i])
	})
}

// clockwise returns the dealt seats that match ok, in order, starting with
// the first seat after from.
func (h *hand) clockwise(from int, ok func(*player) bool) []*player {
	var out []*player
	n := len(h.players)
	for k := 1; k <= n; k++ {
		if p := h.players[(from+k)%n]; p != nil && ok(p) {
			out = append(out, p)
		}
	}
	return out
}

func (h *hand) count(ok func(*player) bool) int {
	n := 0
	for _, p := range h.players {
		if p != nil && ok(p) {
			n++
		}
	}
	return n
}

// needsToAct reports whether p still owes a decision on this street.
func (h *hand) needsToAct(p *player) bool {
	return p.canBet() && (!p.acted || p.bet < h.currentBet)
}

// raiseOpen reports whether p may raise. A player who did not act yet on
// this street may always raise. A player who acted may raise again only when
// the bet grew by at least one full raise since then. One incomplete all-in
// raise does not do that, but several of them together can (TDA rule).
func (h *hand) raiseOpen(p *player) bool {
	return !p.acted || h.currentBet-p.actedAt >= h.minRaise
}

// roundOver reports whether the betting on this street is complete.
func (h *hand) roundOver() bool {
	var last *player
	n := 0
	for _, p := range h.players {
		if p != nil && p.canBet() {
			last = p
			n++
		}
	}
	// With nobody to bet against, a matched bet ends the round at once. The
	// lone bettor must match only the live players (who are all-in). The
	// current bet can be higher than every live bet: a big blind that is
	// all-in for less than the full blind, or a bettor who folded.
	if n == 0 || (n == 1 && last.bet >= h.highestLiveBet(last)) {
		return true
	}
	return h.count(h.needsToAct) == 0
}

// highestLiveBet returns the highest street bet of a live player other
// than p.
func (h *hand) highestLiveBet(p *player) int64 {
	var high int64
	for _, o := range h.players {
		if o != nil && o != p && o.live() {
			high = max(high, o.bet)
		}
	}
	return high
}

// legal lists the actions for p.
//
// Bet and Raise are offered when the stack is more than the call, raising
// is open for p, and another live player can still put chips in. When every
// other live player is all-in, nobody can call a raise, so p may only call
// or fold (NLHE). MinTo is the full minimum (current bet + last full raise),
// capped at the all-in total: a short stack can bet or raise only all-in.
// When Bet and Raise are both false, MinTo and MaxTo are 0. When raising is
// closed, AllIn is offered only when it is no more than a call (stack <=
// amount to call), so Legal and Act always agree.
func (h *hand) legal(p *player) Legal {
	toCall := h.currentBet - p.bet
	l := Legal{
		Fold:       true,
		Check:      toCall == 0,
		Call:       toCall > 0,
		CallAmount: min(toCall, p.stack),
		AllInTo:    p.bet + p.stack,
	}
	canRaise := p.stack > toCall && h.raiseOpen(p) &&
		h.count(func(o *player) bool { return o != p && o.canBet() }) > 0
	if canRaise {
		l.MinTo = min(h.currentBet+h.minRaise, l.AllInTo)
		l.MaxTo = l.AllInTo
		l.Bet = h.currentBet == 0
		l.Raise = !l.Bet
	}
	l.AllIn = p.stack > 0 && (canRaise || p.stack <= toCall)
	return l
}

func (t *Table) startHand() error {
	if t.running() != nil {
		return ErrHandRunning
	}
	if t.eligibleCount() < 2 {
		return ErrNotEnough
	}
	n := len(t.seats)
	eligible := func(i int) bool { return t.seats[i].eligible() }
	// Before the first hand t.button is -1, so the search starts at seat 0.
	t.button = nextIndex(n, t.button, eligible)
	t.handNum++
	h := &hand{
		running:    true,
		deck:       t.newDeck(),
		players:    make([]*player, n),
		button:     t.button,
		street:     Preflop,
		bigBlind:   t.cfg.BigBlind,
		minRaise:   t.cfg.BigBlind,
		toAct:      -1,
		sb:         -1,
		bb:         -1,
		aggressor:  -1,
		shown:      make([]bool, n),
		tabled:     make([]bool, n),
		runoutFrom: 5,
	}
	for i, p := range t.seats {
		if eligible(i) {
			p.resetForHand()
			h.players[i] = p
		}
	}
	t.hand = h
	b := t.seats[t.button]
	t.emit(EvHandStart, h.button, b.id, 0, "Hand #%d — %s has the button", t.handNum, b.name)
	t.postBlinds(h)
	for _, p := range h.clockwise(h.sb-1, dealt) {
		h.order = append(h.order, p.id)
	}
	t.dealHoleCards(h)
	t.proceed(h.bb)
	return nil
}

func (t *Table) postBlinds(h *hand) {
	if h.count(dealt) == 2 {
		h.sb = h.button
	} else {
		h.sb = h.next(h.button, dealt)
	}
	h.bb = h.next(h.sb, dealt)
	t.post(h.players[h.sb], t.cfg.SmallBlind, "small blind", "SB")
	t.post(h.players[h.bb], t.cfg.BigBlind, "big blind", "BB")
	// A short big blind does not lower the price: the call is the full blind.
	h.currentBet = t.cfg.BigBlind
}

func (t *Table) post(p *player, blind int64, name, short string) {
	amount := min(blind, p.stack)
	p.putIn(amount)
	p.lastAction = fmt.Sprintf("%s %d", short, amount)
	if p.stack == 0 {
		t.emit(EvBlind, p.seat, p.id, amount, "%s posts %s %d and is all-in", p.name, name, amount)
		return
	}
	t.emit(EvBlind, p.seat, p.id, amount, "%s posts %s %d", p.name, name, amount)
}

func (p *player) putIn(amount int64) {
	p.stack -= amount
	p.bet += amount
	p.committed += amount
	if p.stack == 0 {
		p.wasAllIn = true
	}
}

// dealHoleCards deals one card at a time, clockwise from the seat left of
// the button, in two rounds.
func (t *Table) dealHoleCards(h *hand) {
	order := h.clockwise(h.button, dealt)
	for range 2 {
		for _, p := range order {
			p.hole = append(p.hole, h.deck.Draw())
		}
	}
}

func (t *Table) act(id string, a Action) error {
	if t.running() == nil {
		return ErrNoHand
	}
	if t.find(id) == nil {
		return ErrNotSeated
	}
	h, p, ok := t.actor(id)
	if !ok {
		return ErrNotYourTurn
	}
	l := h.legal(p)
	switch a.Type {
	case Fold:
		t.fold(p)
	case Check:
		if !l.Check {
			return fmt.Errorf("%w: cannot check, %d to call", ErrIllegal, l.CallAmount)
		}
		t.check(h, p)
	case Call:
		if !l.Call {
			return fmt.Errorf("%w: nothing to call", ErrIllegal)
		}
		t.putTo(h, p, p.bet+l.CallAmount)
	case Bet, Raise:
		if err := checkSize(a, l); err != nil {
			return err
		}
		t.putTo(h, p, a.Amount)
	case AllIn:
		if !l.AllIn {
			return fmt.Errorf("%w: raising is closed; you may only call or fold", ErrIllegal)
		}
		t.putTo(h, p, l.AllInTo)
	default:
		return fmt.Errorf("%w: unknown action %d", ErrIllegal, a.Type)
	}
	t.proceed(p.seat)
	return nil
}

func checkSize(a Action, l Legal) error {
	switch {
	case a.Type == Bet && !l.Bet && l.Raise:
		return fmt.Errorf("%w: there is a bet already; raise instead", ErrIllegal)
	case a.Type == Raise && !l.Raise && l.Bet:
		return fmt.Errorf("%w: there is no bet to raise; bet instead", ErrIllegal)
	case !l.Bet && !l.Raise:
		return fmt.Errorf("%w: you may not %s now", ErrIllegal, a.Type)
	case a.Amount < l.MinTo || a.Amount > l.MaxTo:
		return fmt.Errorf("%w: %s to %d; it must be from %d to %d", ErrIllegal, a.Type, a.Amount, l.MinTo, l.MaxTo)
	}
	return nil
}

func (t *Table) fold(p *player) {
	t.hand.record(p, Fold)
	p.folded = true
	p.acted = true
	p.lastAction = "Fold"
	t.emitAction(p, Fold, 0, "%s folds", p.name)
}

func (t *Table) check(h *hand, p *player) {
	h.record(p, Check)
	p.acted = true
	p.actedAt = h.currentBet
	p.lastAction = "Check"
	t.emitAction(p, Check, 0, "%s checks", p.name)
}

// putTo makes p's street total equal to `to`. It is a call when `to` does
// not pass the current bet, else a bet or a raise. A raise that is smaller
// than the last full raise (only possible all-in) does not change minRaise.
func (t *Table) putTo(h *hand, p *player, to int64) {
	added := to - p.bet
	before := h.currentBet
	p.putIn(added)
	// Every putTo puts chips in by choice (blinds use post). Preflop that
	// is VPIP; a total above the current bet is also PFR.
	if h.street == Preflop && added > 0 {
		p.vpip = true
		p.pfr = p.pfr || to > before
	}
	if to > before {
		if inc := to - before; inc >= h.minRaise {
			h.minRaise = inc
		}
		h.currentBet = to
		h.aggressor = p.seat
	}
	p.acted = true
	p.actedAt = h.currentBet

	typ := Raise
	switch {
	case p.stack == 0:
		typ = AllIn
	case to <= before:
		typ = Call
	case before == 0:
		typ = Bet
	}
	h.recordTo(p, typ, to, before)

	switch typ {
	case AllIn:
		p.lastAction = fmt.Sprintf("All-in %d", to)
		t.emitAction(p, AllIn, to, "%s is all-in for %d", p.name, to)
	case Call:
		p.lastAction = fmt.Sprintf("Call %d", added)
		t.emitAction(p, Call, added, "%s calls %d", p.name, added)
	case Bet:
		p.lastAction = fmt.Sprintf("Bet %d", to)
		t.emitAction(p, Bet, to, "%s bets %d", p.name, to)
	default:
		p.lastAction = fmt.Sprintf("Raise to %d", to)
		t.emitAction(p, Raise, to, "%s raises to %d", p.name, to)
	}
}

// proceed moves the hand on after the player at seat `from` acted (or
// left). It ends the hand, ends the street, or finds the next player.
func (t *Table) proceed(from int) {
	h := t.hand
	if h.runout {
		// Nobody acts during a paused run-out; only a player who leaves
		// can change it.
		t.runoutFold(h)
		return
	}
	switch {
	case h.count((*player).live) == 1:
		t.returnUncalled(h)
		t.finish()
	case !h.roundOver():
		h.toAct = h.next(from, h.needsToAct)
	case h.street == River || h.count((*player).canBet) <= 1:
		// No more betting: return the uncalled bet, then run out the board.
		h.toAct = -1
		t.returnUncalled(h)
		// Two or more live players and at most one who can bet: the others
		// are all-in, so the rest of the board is a run-out.
		if len(h.board) < 5 {
			h.runoutFrom = len(h.board)
			if t.cfg.StepRunout {
				// The caller deals each street with DealRunout.
				t.startRunout(h)
				return
			}
		}
		for len(h.board) < 5 {
			t.dealStreet(h)
		}
		t.finish()
	default:
		t.dealStreet(h)
		h.toAct = h.next(h.button, h.needsToAct)
	}
}

// afterOutOfTurnFold moves the hand on after the player at seat left it.
func (t *Table) afterOutOfTurnFold(seat int) {
	h := t.hand
	if h.toAct == seat {
		t.proceed(seat)
		return
	}
	// Search again from the player to act, so that player stays first.
	t.proceed((h.toAct - 1 + len(h.players)) % len(h.players))
}

// dealStreet burns one card and deals the next street.
func (t *Table) dealStreet(h *hand) {
	h.deck.Draw()
	n := 1
	if h.street == Preflop {
		n = 3
	}
	for range n {
		h.board = append(h.board, h.deck.Draw())
	}
	h.street++
	if h.street == Flop {
		for _, p := range h.players {
			if p != nil && p.live() {
				p.sawFlop = true
			}
		}
	}
	h.currentBet = 0
	h.minRaise = h.bigBlind
	h.aggressor = -1
	for _, p := range h.players {
		if p != nil {
			p.bet, p.acted, p.actedAt = 0, false, 0
			p.lastAction = ""
		}
	}
	t.emit(EvStreet, -1, "", 0, "%s: %s", h.street, boardText(h.board))
}

// record adds a Fold or Check decision of p to the action log. It runs
// before the decision changes the state.
func (h *hand) record(p *player, typ ActionType) {
	h.recordTo(p, typ, 0, h.currentBet)
}

// recordTo adds a decision of p to the action log. to is p's street total
// after the decision (0 for Fold and Check) and before is the highest street
// total before it.
func (h *hand) recordTo(p *player, typ ActionType, to, before int64) {
	raises := 0
	for _, a := range h.actions {
		if a.Street == h.street && a.Raise {
			raises++
		}
	}
	h.actions = append(h.actions, ActionRecord{
		PlayerID:     p.id,
		Street:       h.street,
		Type:         typ,
		To:           to,
		BetBefore:    before,
		Raise:        to > before,
		RaisesBefore: raises,
	})
}
