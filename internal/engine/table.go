package engine

import (
	"fmt"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// Table is one poker table. It is not safe for concurrent use.
type Table struct {
	cfg     Config
	newDeck func() *cards.Deck
	seats   []*player // nil is an empty seat
	button  int       // -1 before the first hand
	handNum int
	hand    *hand // the current hand, or the last hand when none runs
	events  []Event
}

// player is one seated person. The hand fields are valid only while the
// player is in t.hand.players.
type player struct {
	id, name   string
	seat       int
	stack      int64 // chips behind
	sittingOut bool

	hole       []cards.Card
	bet        int64 // chips put in on this street
	committed  int64 // chips put in during this hand
	folded     bool
	acted      bool  // acted on this street
	actedAt    int64 // the current bet when the player last acted
	lastAction string

	// Facts for the PlayerSummary of this hand.
	won          int64 // chips received from the pots
	vpip, pfr    bool
	sawFlop      bool
	wentShowdown bool
	wasAllIn     bool
	shownValue   cards.HandValue // valid when wentShowdown
	shownBest    [5]cards.Card   // valid when wentShowdown
	revealed     [2]bool         // hole cards shown after the hand (ShowCards)
}

func (p *player) resetForHand() {
	p.hole = nil
	p.bet, p.committed = 0, 0
	p.folded, p.acted = false, false
	p.actedAt = 0
	p.lastAction = ""
	p.won = 0
	p.vpip, p.pfr, p.sawFlop, p.wentShowdown, p.wasAllIn = false, false, false, false, false
	p.shownValue, p.shownBest = cards.HandValue{}, [5]cards.Card{}
	p.revealed = [2]bool{}
}

// live reports whether the player still contests the pot.
func (p *player) live() bool { return !p.folded }

// canBet reports whether the player can still put chips in.
func (p *player) canBet() bool { return !p.folded && p.stack > 0 }

func (p *player) eligible() bool { return p != nil && !p.sittingOut && p.stack > 0 }

func newTable(cfg Config, newDeck func() *cards.Deck) (*Table, error) {
	switch {
	case cfg.Seats < 2 || cfg.Seats > 10:
		return nil, fmt.Errorf("%w: seats must be 2 to 10, not %d", ErrBadConfig, cfg.Seats)
	case cfg.SmallBlind <= 0:
		return nil, fmt.Errorf("%w: small blind must be more than zero", ErrBadConfig)
	case cfg.BigBlind < cfg.SmallBlind:
		return nil, fmt.Errorf("%w: big blind must be at least the small blind", ErrBadConfig)
	}
	if newDeck == nil {
		newDeck = cards.NewShuffledDeck
	}
	// handNum is the number of the last hand dealt. With FirstHand = n, the
	// table acts as if hand n-1 was the last one, so HandNumber() returns
	// n-1 before the first hand and the server can save it unchanged.
	return &Table{
		cfg:     cfg,
		newDeck: newDeck,
		seats:   make([]*player, cfg.Seats),
		button:  -1,
		handNum: max(cfg.FirstHand, 1) - 1,
	}, nil
}

func (t *Table) find(id string) *player {
	for _, p := range t.seats {
		if p != nil && p.id == id {
			return p
		}
	}
	return nil
}

// running returns the hand in progress, or nil.
func (t *Table) running() *hand {
	if t.hand != nil && t.hand.running {
		return t.hand
	}
	return nil
}

// inLiveHand reports whether p holds live cards in the hand in progress.
func (t *Table) inLiveHand(p *player) bool {
	h := t.running()
	return h != nil && h.players[p.seat] == p && p.live()
}

func (t *Table) sit(seat int, id, name string, stack int64) error {
	switch {
	case seat < 0 || seat >= len(t.seats):
		return ErrBadSeat
	case t.seats[seat] != nil:
		return ErrSeatTaken
	case id == "":
		return fmt.Errorf("%w: empty player id", ErrIllegal)
	case t.find(id) != nil:
		return ErrAlreadySeated
	case stack <= 0:
		return ErrNoChips
	}
	t.seats[seat] = &player{id: id, name: name, seat: seat, stack: stack}
	t.emit(EvSeat, seat, id, stack, "%s sits down with %d", name, stack)
	return nil
}

func (t *Table) stand(id string) (int64, error) {
	p := t.find(id)
	if p == nil {
		return 0, ErrNotSeated
	}
	wasLive := t.inLiveHand(p)
	if wasLive {
		t.fold(p)
	}
	chips := p.stack
	p.stack = 0
	t.seats[p.seat] = nil
	t.emit(EvSeat, p.seat, id, chips, "%s stands up with %d", p.name, chips)
	if wasLive {
		t.afterOutOfTurnFold(p.seat)
	}
	return chips, nil
}

func (t *Table) setSittingOut(id string, out bool) error {
	p := t.find(id)
	if p == nil {
		return ErrNotSeated
	}
	if p.sittingOut == out {
		return nil
	}
	p.sittingOut = out
	if out {
		t.emit(EvSeat, p.seat, id, 0, "%s sits out", p.name)
	} else {
		t.emit(EvSeat, p.seat, id, 0, "%s is back in", p.name)
	}
	return nil
}

func (t *Table) addChips(id string, amount int64) error {
	p := t.find(id)
	switch {
	case p == nil:
		return ErrNotSeated
	case amount <= 0:
		return ErrNoChips
	case t.inLiveHand(p):
		return ErrHandRunning
	}
	p.stack += amount
	t.emit(EvSeat, p.seat, id, amount, "%s adds %d chips", p.name, amount)
	return nil
}

func (t *Table) eligibleCount() int {
	n := 0
	for _, p := range t.seats {
		if p.eligible() {
			n++
		}
	}
	return n
}

func (t *Table) canStartHand() bool { return t.running() == nil && t.eligibleCount() >= 2 }

func (t *Table) inHand() bool { return t.running() != nil }

func (t *Table) toAct() (string, bool) {
	h := t.running()
	if h == nil || h.toAct < 0 {
		return "", false
	}
	return h.players[h.toAct].id, true
}

// actor returns the player with this id when that player must act now.
func (t *Table) actor(id string) (*hand, *player, bool) {
	h := t.running()
	if h == nil || h.toAct < 0 {
		return nil, nil, false
	}
	p := h.players[h.toAct]
	if p.id != id || t.seats[p.seat] != p {
		return nil, nil, false
	}
	return h, p, true
}

func (t *Table) legalFor(id string) (Legal, bool) {
	h, p, ok := t.actor(id)
	if !ok {
		return Legal{}, false
	}
	return h.legal(p), true
}

func (t *Table) defaultAction(id string) Action {
	if l, ok := t.legalFor(id); ok && l.Check {
		return Action{Type: Check}
	}
	return Action{Type: Fold}
}

func (t *Table) stack(id string) (int64, bool) {
	p := t.find(id)
	if p == nil {
		return 0, false
	}
	return p.stack, true
}

func (t *Table) emit(kind EventKind, seat int, id string, amount int64, format string, args ...any) {
	t.events = append(t.events, Event{
		Kind:       kind,
		HandNumber: t.handNum,
		Seat:       seat,
		PlayerID:   id,
		Amount:     amount,
		Text:       fmt.Sprintf(format, args...),
		Street:     t.eventStreet(),
	})
}

// emitAction adds an EvAction event for p with the action type typ.
func (t *Table) emitAction(p *player, typ ActionType, amount int64, format string, args ...any) {
	t.emit(EvAction, p.seat, p.id, amount, format, args...)
	t.events[len(t.events)-1].Action = typ
}

// eventStreet is the street of the hand in progress, or Waiting between
// hands. During the settlement of a hand with a showdown it is Showdown.
func (t *Table) eventStreet() Street {
	if h := t.running(); h != nil {
		return h.street
	}
	return Waiting
}

func (t *Table) drainEvents() []Event {
	ev := t.events
	t.events = nil
	return ev
}

// nextIndex returns the first index after from, clockwise over n seats,
// for which ok is true. The search ends at from itself. It returns -1 when
// no index matches.
func nextIndex(n, from int, ok func(i int) bool) int {
	for k := 1; k <= n; k++ {
		i := ((from+k)%n + n) % n
		if ok(i) {
			return i
		}
	}
	return -1
}
