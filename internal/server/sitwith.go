package server

import (
	"errors"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

var errBadSitChoice = errors.New("unknown sit choice")

// sitWith takes a seat with the amount that c picks.
func (s *Session) sitWith(seat int, c SitChoice) error {
	if c != SitWholeBank && c != SitBuyIn {
		return errBadSitChoice
	}
	return s.call(func(r *Room) error { return r.handleSit(s, seat, c) })
}

// chipPlan is the source of the chips that a player brings to the table:
// fromBank chips come out of the bank and bought chips are a purchase.
type chipPlan struct {
	fromBank int64
	bought   int64
}

// chips returns the chips that the plan puts on the table.
func (p chipPlan) chips() int64 { return p.fromBank + p.bought }

// planSit returns the chips for a sit with choice c, a bank of bank and a
// starting stack of stack.
func planSit(c SitChoice, bank, stack int64) chipPlan {
	bank = max(bank, 0)
	if c == SitWholeBank && bank > 0 {
		return chipPlan{fromBank: bank}
	}
	if c == SitWholeBank {
		return chipPlan{bought: stack}
	}
	return planStack(bank, stack)
}

// planStack returns the chips for exactly stack chips: the bank first, then
// a purchase of the rest. SitBuyIn and Rebuy use it.
func planStack(bank, stack int64) chipPlan {
	from := min(max(bank, 0), stack)
	return chipPlan{fromBank: from, bought: stack - from}
}

// take moves the chips of p out of the bank of a and records the purchase.
// A purchase of more than zero chips counts as one buy-in.
func (a *account) take(p chipPlan) {
	a.Bank -= p.fromBank
	if p.bought > 0 {
		a.buyIn(p.bought)
	}
}

// bankOf returns the bank of key; 0 when the account does not exist.
func (r *Room) bankOf(key string) int64 {
	if a := r.st.Accounts[key]; a != nil {
		return a.Bank
	}
	return 0
}

func (r *Room) handleSit(s *Session, seat int, c SitChoice) error {
	if r.seated(s.key) {
		return engine.ErrAlreadySeated
	}
	if seat < 0 {
		if seat = r.freeSeat(); seat < 0 {
			return ErrTableFull
		}
	}
	plan := planSit(c, r.bankOf(s.key), r.cfg.StartingStack)
	if err := r.table.Sit(seat, s.key, s.name, plan.chips()); err != nil {
		return err
	}
	r.account(s.key, s.name).take(plan)
	r.save()
	r.settle()
	return nil
}

func (r *Room) handleRebuy(s *Session) error {
	stack, ok := r.table.Stack(s.key)
	if !ok {
		return engine.ErrNotSeated
	}
	if stack > 0 {
		return ErrNotBusted
	}
	plan := planStack(r.bankOf(s.key), r.cfg.StartingStack)
	if err := r.table.AddChips(s.key, plan.chips()); err != nil {
		return err
	}
	r.account(s.key, s.name).take(plan)
	r.save()
	r.settle()
	return nil
}
