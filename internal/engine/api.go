// Package engine is a pure, single-threaded No-Limit Texas Hold'em table.
// It has no goroutines, no timers and no I/O. The server package owns the
// clock and calls these methods from one goroutine.
//
// CONTRACT FILE: the exported names and signatures in this file are fixed.
// Other packages depend on them. Implement the bodies (other files are fine);
// do not change the API. See DESIGN.md for the rules.
package engine

import (
	"errors"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

var (
	ErrBadConfig     = errors.New("bad table config")
	ErrSeatTaken     = errors.New("seat is taken")
	ErrBadSeat       = errors.New("no such seat")
	ErrAlreadySeated = errors.New("player is already seated")
	ErrNotSeated     = errors.New("player is not seated")
	ErrNoChips       = errors.New("stack must be more than zero")
	ErrHandRunning   = errors.New("a hand is in progress")
	ErrNoHand        = errors.New("no hand is in progress")
	ErrNotEnough     = errors.New("not enough players to start a hand")
	ErrNotYourTurn   = errors.New("it is not your turn")
	ErrIllegal       = errors.New("illegal action")
)

// Config sets the table limits.
type Config struct {
	Seats      int   // 2..10
	SmallBlind int64 // > 0
	BigBlind   int64 // >= SmallBlind
	// FirstHand is the number of the first hand this table deals (0 or 1 = 1).
	// The server passes the saved counter so hand numbers continue after a restart.
	FirstHand int
	// StepRunout pauses a run-out. When betting ends with two or more live
	// players and fewer than 5 board cards, the hand stays live (InHand is
	// true, ToAct is false, View.Runout is true) and the caller deals each
	// street with DealRunout. When false, the engine deals the whole board
	// at once (the old behaviour).
	StepRunout bool
}

// ActionType is what a player does on their turn.
type ActionType uint8

const (
	Fold  ActionType = iota
	Check            // only when there is nothing to call
	Call             // match the current bet (or all-in for less)
	Bet              // first bet on a street; Amount = street total ("bet 40")
	Raise            // raise a bet; Amount = street total ("raise to 120")
	AllIn            // put every chip in; the engine treats it as call, bet or raise
)

func (a ActionType) String() string {
	switch a {
	case Fold:
		return "Fold"
	case Check:
		return "Check"
	case Call:
		return "Call"
	case Bet:
		return "Bet"
	case Raise:
		return "Raise"
	case AllIn:
		return "All-in"
	}
	return "?"
}

// Action is one player decision. Amount is used only for Bet and Raise, and
// is the player's TOTAL contribution for this street after the action.
type Action struct {
	Type   ActionType
	Amount int64
}

// Street is the stage of the hand.
type Street uint8

const (
	Waiting Street = iota // no hand in progress
	Preflop
	Flop
	Turn
	River
	Showdown // only seen in events; a finished hand returns to Waiting
)

func (s Street) String() string {
	return [...]string{"Waiting", "Preflop", "Flop", "Turn", "River", "Showdown"}[s]
}

// Legal lists the actions open to the player who must act.
// All "To" amounts are the player's street total after the action.
type Legal struct {
	Fold       bool
	Check      bool
	Call       bool
	CallAmount int64 // chips to add for a call (capped at the stack)
	Bet        bool  // no bet yet on this street, stack > call, and another live player can still bet
	Raise      bool  // a bet exists, raising is open, stack > call, and another live player can still bet
	MinTo      int64 // smallest legal Bet/Raise total
	MaxTo      int64 // largest legal Bet/Raise total (= all-in total)
	AllIn      bool  // stack > 0 and (Bet or Raise, or the stack is no more than the call)
	AllInTo    int64 // street total if the player goes all-in
}

// Pot is one main pot or side pot. Eligible lists seat indexes.
type Pot struct {
	Amount   int64
	Eligible []int
}

// SeatView is one seat as one viewer sees it.
type SeatView struct {
	Index      int
	Empty      bool
	PlayerID   string
	Name       string
	Stack      int64 // chips behind (not yet in the pot)
	Bet        int64 // chips put in on this street
	Committed  int64 // chips put in during this hand (all streets)
	InHand     bool  // dealt into the current hand
	Folded     bool
	AllIn      bool
	SittingOut bool
	Busted     bool         // stack is zero and the player is not in a hand
	HasCards   bool         // holds live hole cards (draw card backs if Cards is nil)
	Cards      []cards.Card // len 2 when the viewer may see them, else nil
	LastAction string       // "Check", "Call 20", "Raise to 60", "Fold", "All-in 340"; cleared each street
}

// Winning is one award of chips from one pot.
type Winning struct {
	Seat     int
	PlayerID string
	Name     string
	Amount   int64
	PotIndex int    // 0 = main pot
	Hand     string // HandValue.String(); "" when everyone else folded
}

// Shown is one hand revealed at showdown.
type Shown struct {
	Seat     int
	PlayerID string
	Name     string
	Cards    []cards.Card
	Best     [5]cards.Card
	Hand     string
}

// Result describes the last finished hand.
type Result struct {
	HandNumber int
	Board      []cards.Card
	Winnings   []Winning
	Shown      []Shown

	// Players has one summary for each player dealt into the hand, also for
	// players who stood up during it. The server builds stats from it.
	Players []PlayerSummary
	// NoShowdown is true when the hand ended because everyone else folded.
	NoShowdown bool
	// CanRabbit is true when NoShowdown is true, the board has fewer than 5
	// cards, and nobody hunted yet. RabbitHunt is legal only then.
	CanRabbit bool
	// Rabbit holds the cards that the rabbit hunt revealed (the rest of the
	// board, in deal order, after the same burns). They are not part of Board.
	Rabbit []cards.Card
	// Revealed holds hole cards that players chose to show after the hand
	// (the NoShowdown winner, and folded players from batch 9). Cards has 1 or 2 entries; Best and Hand are empty. When a stand
	// ends a paused run-out, it holds both cards of the winner, because that
	// hand was face up since the start of the run-out.
	Revealed []Shown

	// Actions lists every player decision of the hand in order (blinds are
	// not included). The server derives positional and street stats from it.
	Actions []ActionRecord
	// Order lists the player ids dealt in, in preflop seat order starting at
	// the small blind (heads-up: the button/small blind first). Button is
	// Order[len(Order)-1] for 3+ players and Order[0] heads-up.
	Order    []string
	BigBlind int64 // the big blind of this hand
	// RunoutFrom is the number of board cards that were out when the last
	// betting closed with 2+ live players and at least one all-in, so the rest
	// of the board was dealt with no more betting. 5 when no run-out happened.
	RunoutFrom int
}

// ActionRecord is one decision in Result.Actions.
type ActionRecord struct {
	PlayerID string
	Street   Street     // Preflop..River
	Type     ActionType // as resolved by the engine: an AllIn is recorded as AllIn
	// To is the player's street total after the action (0 for Fold/Check).
	To int64
	// BetBefore is the highest street total before the action.
	BetBefore int64
	// Raise is true when the action raised the highest street total (a bet
	// counts as a raise of 0; an all-in for less than a call does not).
	Raise bool
	// RaisesBefore counts the bets and raises earlier on this street
	// (preflop: the big blind does not count, so an open raise has 0 before
	// it and a 3-bet has 1).
	RaisesBefore int
}

// PlayerSummary is one player's part in one finished hand.
// The sum of (Won - Committed) over all players of a hand is zero.
type PlayerSummary struct {
	PlayerID  string
	Name      string
	Seat      int
	Committed int64 // chips that went into the pots (after any uncalled return)
	Won       int64 // chips received from the pots
	// VPIP: the player put chips in preflop voluntarily (call, bet, raise or
	// all-in; posting a blind and checking the option are not voluntary).
	VPIP bool
	// PFR: the player bet or raised preflop (an all-in that raised counts).
	PFR            bool
	SawFlop        bool // still live when the flop was dealt
	WentToShowdown bool // hand was shown at showdown
	WonAtShowdown  bool // won chips at showdown
	AllIn          bool // was all-in at some point in the hand
	// Hand is the best five-card value shown at showdown; nil when the hand
	// was not shown at showdown.
	Hand      *cards.HandValue
	HandCards [5]cards.Card
}

// View is the whole table as one viewer sees it. It is a deep copy: the
// caller may keep it and read it from another goroutine.
type View struct {
	HandNumber     int
	Street         Street
	InHand         bool
	SmallBlind     int64
	BigBlind       int64
	Button         int // seat index, -1 before the first hand
	SmallBlindSeat int // -1 when none
	BigBlindSeat   int // -1 when none
	ToAct          int // seat index, -1 when nobody must act
	CurrentBet     int64
	MinRaiseTo     int64 // next legal raise total for the player to act (info only)
	Board          []cards.Card
	Pots           []Pot // pots from earlier streets; this street's bets are in SeatView.Bet
	TotalPot       int64 // sum of Pots + sum of all SeatView.Bet
	Seats          []SeatView
	MySeat         int    // viewer's seat, -1 when the viewer is not seated
	Legal          *Legal // non-nil only when the viewer must act now
	Result         *Result
	// Runout is true while a paused run-out waits for DealRunout
	// (Config.StepRunout). During a run-out every live player's hole cards
	// are face up for every viewer (SeatView.Cards).
	Runout bool
}

// EventKind classifies an Event.
type EventKind uint8

const (
	EvHandStart EventKind = iota
	EvBlind
	EvStreet // a new street was dealt; Text lists the board
	EvAction
	EvReturn // an uncalled bet returned
	EvShow
	EvWin
	EvBust
	EvHandEnd
	EvSeat   // sit, stand, sit out, chips added, rename
	EvRabbit // rabbit hunt; Text lists the cards, e.g. "Rabbit hunt (Bob): [9♣] [A♥]"
	EvReveal // the winner showed cards, e.g. "Alice shows A♠ —"
)

// Event is one line of table history.
type Event struct {
	Kind       EventKind
	HandNumber int
	Seat       int // -1 when no seat applies
	PlayerID   string
	Amount     int64
	Text       string // a complete human log line, e.g. "Alice raises to 60"
	// Street is the street on which the event happened (Waiting for seat
	// events between hands).
	Street Street
	// Action is set for EvAction events only.
	Action ActionType
}

// NewTable makes an empty table. newDeck returns a fresh deck for each hand;
// nil means cards.NewShuffledDeck.
func NewTable(cfg Config, newDeck func() *cards.Deck) (*Table, error) { return newTable(cfg, newDeck) }

// Sit puts a player in an empty seat with a stack. A player who sits during
// a hand is dealt in from the next hand.
func (t *Table) Sit(seat int, id, name string, stack int64) error {
	return t.sit(seat, id, name, stack)
}

// Stand removes a player and returns the chips behind. If the player holds a
// live hand, the engine folds the hand first (in turn or out of turn) and
// then advances the hand as needed. Chips already in the pot stay there.
func (t *Table) Stand(id string) (int64, error) { return t.stand(id) }

// SetSittingOut marks a player as sitting out (or back in) from the next hand.
func (t *Table) SetSittingOut(id string, out bool) error { return t.setSittingOut(id, out) }

// AddChips adds chips to a seated player's stack. It returns ErrHandRunning
// when the player is in a live hand.
func (t *Table) AddChips(id string, amount int64) error { return t.addChips(id, amount) }

// CanStartHand reports whether no hand runs and at least two seated players
// are not sitting out and have chips.
func (t *Table) CanStartHand() bool { return t.canStartHand() }

// StartHand moves the button, posts blinds and deals hole cards.
func (t *Table) StartHand() error { return t.startHand() }

// Act applies the decision of the player who must act. When the action
// closes the betting, the engine deals the next street, or runs out the
// board when no more betting is possible, and settles the hand. With
// Config.StepRunout the run-out pauses instead (see DealRunout), and Act
// returns ErrNotYourTurn until the hand ends.
func (t *Table) Act(id string, a Action) error { return t.act(id, a) }

// ToAct returns the id of the player who must act.
func (t *Table) ToAct() (string, bool) { return t.toAct() }

// LegalFor returns the legal actions for id; ok is false when id need not act.
func (t *Table) LegalFor(id string) (Legal, bool) { return t.legalFor(id) }

// DefaultAction is the timeout action: Check when legal, else Fold.
func (t *Table) DefaultAction(id string) Action { return t.defaultAction(id) }

// InHand reports whether a hand is in progress.
func (t *Table) InHand() bool { return t.inHand() }

// Stack returns the chips behind for a seated player.
func (t *Table) Stack(id string) (int64, bool) { return t.stack(id) }

// View returns the table as viewerID sees it ("" = spectator).
// Hole cards are visible to their owner, and to everyone for hands shown at
// showdown. After a hand ends, the board, shown cards and Result stay
// visible until the next StartHand.
func (t *Table) View(viewerID string) View { return t.view(viewerID) }

// DrainEvents returns and clears the events since the last call.
func (t *Table) DrainEvents() []Event { return t.drainEvents() }

// ErrNotNow: RabbitHunt or ShowCards is not legal now.
var ErrNotNow = errors.New("not possible now")

// RabbitHunt deals the rest of the board of the last hand from the same deck
// (with the normal burns) into Result.Rabbit. It is legal only between hands
// when Result.CanRabbit is true; else it returns ErrNotNow. It never changes
// chips. It sets CanRabbit to false.
func (t *Table) RabbitHunt() error { return t.rabbitHunt() }

// ShowCards lets a player of the last hand show one or both hole cards
// (left = first dealt card) after the hand ends. Legal only between hands
// (Result set, no hand live), at least one of left/right true, and only for:
//   - the winner of a NoShowdown hand, or
//   - a player who folded in the last hand and is still seated (batch 9).
//
// Else ErrNotNow. Never during a hand. A second call can add the other
// card. Each player has at most one entry in Result.Revealed (in the order
// of the first show); its Cards grow with later calls. Shown cards become
// visible in View to everyone, at that player's seat. A hand that was face
// up at the start of a run-out, or shown at showdown, cannot be shown again
// (ErrNotNow).
func (t *Table) ShowCards(id string, left, right bool) error { return t.showCards(id, left, right) }

// Rename changes a seated player's display name (also during a hand).
func (t *Table) Rename(id, name string) error { return t.rename(id, name) }

// HandNumber returns the number of the last hand dealt (0 before the first).
func (t *Table) HandNumber() int { return t.handNumber() }

// RunoutPending reports whether a paused run-out waits for DealRunout.
func (t *Table) RunoutPending() bool { return t.runoutPending() }

// DealRunout burns and deals the next street of a paused run-out (the flop,
// the turn or the river). After the river it ends the hand (showdown, pots
// awarded, Result set). It returns ErrNotNow when no run-out is pending.
func (t *Table) DealRunout() error { return t.dealRunout() }
