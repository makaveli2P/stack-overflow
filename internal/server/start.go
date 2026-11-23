package server

import "time"

// startDelayCap is the longest time from a Start to the first hand.
const startDelayCap = 3 * time.Second

func (s *Session) start() error {
	return s.call(func(r *Room) error { return r.handleStart(s) })
}

// handleStart ends the Waiting state (Config.ManualStart). The first hand
// starts after NextHandDelay, capped at startDelayCap. Waiting is only true
// between hands, so no hand is live here.
func (r *Room) handleStart(s *Session) error {
	if !r.waiting {
		return nil // The game runs already, or ManualStart is off.
	}
	if !r.seated(s.key) {
		return ErrStartNotSeated
	}
	if !r.table.CanStartHand() {
		return ErrStartNeedsTwo
	}
	r.waiting = false
	r.startedBy = s.name
	r.nextHandAt = r.now().Add(min(r.cfg.NextHandDelay, startDelayCap))
	r.logf("%s started the game", s.name)
	r.settle()
	return nil
}

// stopForStart puts the room back in the Waiting state when ManualStart is
// on. scheduleNextHand calls it between hands when fewer than 2 players can
// play. The players then press G again when they are ready.
func (r *Room) stopForStart() {
	if !r.cfg.ManualStart {
		return
	}
	r.waiting = true
	r.startedBy = ""
}
