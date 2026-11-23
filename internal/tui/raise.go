package tui

import (
	"slices"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// maxTyped stops typed amounts from overflowing int64.
const maxTyped = 1e11

// raiseEditor holds the bet or raise amount that the player builds.
type raiseEditor struct {
	amount int64 // street total; can be out of range while the player types
	typing bool  // the last key was a digit, so the next digit appends
}

// potShare is a fraction of the pot: num/den.
type potShare struct{ num, den int64 }

// presets are the amounts behind keys h, t and p; key a is all-in.
var presets = []potShare{{1, 2}, {2, 3}, {1, 1}}

// steps are the pot fractions that ↑ and ↓ move between.
var steps = []potShare{{1, 4}, {1, 3}, {1, 2}, {2, 3}, {3, 4}, {1, 1}, {3, 2}, {2, 1}}

// raiseMath holds the numbers a pot-sized bet needs.
type raiseMath struct {
	legal  engine.Legal
	myBet  int64 // viewer's chips in on this street
	pot    int64 // total pot, all bets included
	bigBet int64 // one big blind, the step for ← and →
}

func (r raiseMath) clamp(v int64) int64 {
	return min(max(v, r.legal.MinTo), r.legal.MaxTo)
}

// potTo returns the street total for a bet of share × pot. The pot counts
// the call first, so "pot" means a standard pot-sized raise: the current
// bet plus the pot after calling.
func (r raiseMath) potTo(p potShare) int64 {
	call := r.legal.CallAmount
	return r.clamp(r.myBet + call + (r.pot+call)*p.num/p.den)
}

// stepTargets lists the distinct amounts that ↑ and ↓ visit, low to high.
func (r raiseMath) stepTargets() []int64 {
	out := []int64{r.legal.MinTo, r.legal.MaxTo}
	for _, p := range steps {
		out = append(out, r.potTo(p))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (r raiseMath) stepUp(v int64) int64 {
	for _, t := range r.stepTargets() {
		if t > v {
			return t
		}
	}
	return r.legal.MaxTo
}

func (r raiseMath) stepDown(v int64) int64 {
	ts := r.stepTargets()
	for i := len(ts) - 1; i >= 0; i-- {
		if ts[i] < v {
			return ts[i]
		}
	}
	return r.legal.MinTo
}

// action turns a street total into the engine action to send.
func (r raiseMath) action(v int64) engine.Action {
	v = r.clamp(v)
	switch {
	case v >= r.legal.AllInTo:
		return engine.Action{Type: engine.AllIn}
	case r.legal.Bet:
		return engine.Action{Type: engine.Bet, Amount: v}
	default:
		return engine.Action{Type: engine.Raise, Amount: v}
	}
}

func (e *raiseEditor) clampTo(l engine.Legal) {
	if !e.typing {
		e.amount = min(max(e.amount, l.MinTo), l.MaxTo)
	}
}

// digit appends a typed digit, or starts a new number after other keys.
func (e *raiseEditor) digit(d int64) {
	if !e.typing {
		e.amount, e.typing = 0, true
	}
	if e.amount < maxTyped {
		e.amount = e.amount*10 + d
	}
}

// backspace deletes the last digit, also of a prefilled amount.
func (e *raiseEditor) backspace() {
	e.amount, e.typing = e.amount/10, true
}

// set stores an amount that a non-digit key chose.
func (e *raiseEditor) set(v int64) { e.amount, e.typing = v, false }

// math returns the numbers for the current snapshot, and false when the
// viewer may not bet or raise now.
func (m *model) raiseMath() (raiseMath, bool) {
	l := m.snap.Table.Legal
	if l == nil || (!l.Bet && !l.Raise) {
		return raiseMath{}, false
	}
	me, _ := m.mySeat()
	bb := max(m.snap.Table.BigBlind, 1)
	return raiseMath{legal: *l, myBet: me.Bet, pot: m.snap.Table.TotalPot, bigBet: bb}, true
}
