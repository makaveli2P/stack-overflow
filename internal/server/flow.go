package server

import (
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// settle makes all automatic moves that the current state needs (hand end,
// actions for disconnected players, clocks, next-hand timer), then sends a
// snapshot to every session.
func (r *Room) settle() {
	for r.step() {
	}
	r.accrueSeated()
	r.drainEvents()
	r.broadcast()
}

// step makes at most one automatic action. It returns true when the table
// changed and step must run again.
func (r *Room) step() bool {
	r.drainEvents()
	if r.handLive && !r.table.InHand() {
		r.endHand()
	}
	if !r.table.InHand() {
		r.runoutAt = time.Time{}
		r.setClock("", false)
		r.setTurn("", false)
		r.scheduleNextHand()
		if r.showHold {
			r.showHold = false
			r.holdForReveal()
		}
		return false
	}
	if r.table.RunoutPending() {
		// Nobody acts during a run-out: no turn clock, no turn alerts. The
		// tick deals the next street at runoutAt.
		r.setClock("", false)
		r.setTurn("", false)
		if r.runoutAt.IsZero() {
			r.runoutAt = r.now().Add(r.cfg.RunoutDelay)
		}
		return false
	}
	r.runoutAt = time.Time{}
	id, ok := r.table.ToAct()
	if ok && r.away[id] {
		return r.actDefault(id)
	}
	r.setClock(id, ok)
	r.setTurn(id, ok)
	return false
}

// setTurn starts a new turn (for the "turn" and "hurry" alerts) when the
// player to act changed. newTurn forces a new turn for the same player.
func (r *Room) setTurn(id string, ok bool) {
	if !ok {
		r.turnFor = ""
		return
	}
	if id != r.turnFor {
		r.turnFor = id
		r.turnSeq++
	}
}

// newTurn makes the next step start a new clock and a new turn, also when
// the same player must act again.
func (r *Room) newTurn() {
	r.clockFor = ""
	r.turnFor = ""
}

func (r *Room) drainEvents() {
	for _, ev := range r.table.DrainEvents() {
		r.log.add(ev.Text)
	}
}

// endHand records the stats of the hand, shows the stored cards of folded
// players, posts the screenshot links that waited for the end of the hand,
// stands up the disconnected players and saves the bank. The engine
// awarded the pot, so the chips of players who left during the hand are no
// longer theirs. Every way that a hand ends comes here: step calls it when
// the table has no hand any more, before the next deal.
func (r *Room) endHand() {
	r.handLive = false
	clear(r.leftInHand)
	// The result is in the log now (step drained the events first), so
	// the shared links come with it.
	r.postWaitingShots()
	// handLive was true, so this hand ended now: record its stats once.
	if res := r.table.View("").Result; res != nil {
		r.st.recordHand(res)
		r.st.applyHand(deriveHand(res, r.holes))
	}
	// The hole cards stay until the next deal (dealtHoles): a rabbit hunt
	// of this hand reads them for Me.RabbitHand.
	// The stored shows of folded players come before the stand-ups of the
	// disconnected players: a disconnect keeps the choice until now.
	if r.applyShows() {
		r.showHold = true
	}
	for key := range r.away {
		name := key
		if a := r.st.Accounts[key]; a != nil {
			name = a.Name
		}
		_ = r.standUp(key, name)
		delete(r.away, key)
	}
	r.save()
}

func (r *Room) startHand() {
	if err := r.table.StartHand(); err != nil {
		return
	}
	r.handLive = true
	r.startedBy = "" // The first hand after a Start is dealt.
	r.newTurn()
	clear(r.showAtEnd)
	r.showHold = false
	clear(r.leftInHand)
	r.dealtHoles()
	// Save the new hand number now. A crash during this hand then cannot
	// make the next server run deal this hand number a second time.
	r.save()
}

// dealtHoles keeps the hole cards of each player dealt in, as the owner
// sees them, for the favourite hand stat and Me.RabbitHand. It replaces
// the cards of the last hand.
func (r *Room) dealtHoles() {
	clear(r.holes)
	r.holesHand = r.table.View("").HandNumber
	for _, sv := range r.table.View("").Seats {
		if sv.Empty || !sv.InHand {
			continue
		}
		own := r.table.View(sv.PlayerID)
		if sv.Index < len(own.Seats) {
			if cs := own.Seats[sv.Index].Cards; len(cs) == 2 {
				r.holes[sv.PlayerID] = cs
			}
		}
	}
}

// accrueSeated adds the time since the last call to SeatedSeconds of each
// key that was seated and not sitting out, and starts the count for keys
// that sat down or came back. The room clock gives the time. Whole seconds
// move to the account; the rest stays in seatedAt.
func (r *Room) accrueSeated() {
	now := r.now()
	active := make(map[string]bool)
	for _, sv := range r.table.View("").Seats {
		if !sv.Empty && !sv.SittingOut {
			active[sv.PlayerID] = true
		}
	}
	for key, since := range r.seatedAt {
		if secs := int64(now.Sub(since) / time.Second); secs > 0 {
			if a := r.st.Accounts[key]; a != nil {
				a.Host.SeatedSeconds += secs
			}
			since = since.Add(time.Duration(secs) * time.Second)
			r.seatedAt[key] = since
		}
		if !active[key] {
			delete(r.seatedAt, key)
		}
	}
	for key := range active {
		if _, ok := r.seatedAt[key]; !ok {
			r.seatedAt[key] = now
		}
	}
}

// actDefault plays the timeout action for id. It returns false when the
// engine refuses both the default action and a fold.
func (r *Room) actDefault(id string) bool {
	a := r.table.DefaultAction(id)
	if err := r.table.Act(id, a); err != nil {
		if a.Type == engine.Fold || r.table.Act(id, engine.Action{Type: engine.Fold}) != nil {
			return false
		}
	}
	r.newTurn()
	return true
}

// setClock starts the turn clock for id when the turn changed. ok is false
// when nobody must act.
func (r *Room) setClock(id string, ok bool) {
	if !ok || r.cfg.TurnTimeout <= 0 {
		r.stopClock()
		return
	}
	if id != r.clockFor {
		r.clockFor = id
		r.deadline = r.now().Add(r.cfg.TurnTimeout)
		r.turnExt = 0
		r.clockSeq++
	}
}

// stopClock stops the turn clock. The extensions of the turn end with it.
func (r *Room) stopClock() {
	r.clockFor, r.deadline, r.turnExt = "", time.Time{}, 0
}

// clockTotal is the full length of the running turn clock (Snapshot.ClockTotal).
func (r *Room) clockTotal() time.Duration {
	if r.deadline.IsZero() {
		return 0
	}
	return r.cfg.TurnTimeout + time.Duration(r.turnExt)*r.cfg.ExtensionTime
}

// scheduleNextHand sets the next-hand timer between hands. With
// Config.ManualStart it schedules no hand while the room waits for a Start,
// and it puts the room back in the Waiting state when fewer than 2 players
// can play.
func (r *Room) scheduleNextHand() {
	if !r.table.CanStartHand() {
		r.nextHandAt = time.Time{}
		r.stopForStart()
		return
	}
	if r.waiting {
		r.nextHandAt = time.Time{} // Only a Start schedules the first hand.
		return
	}
	if r.nextHandAt.IsZero() {
		r.nextHandAt = r.now().Add(r.cfg.NextHandDelay)
	}
}

// revealHold is the least time that rabbit cards or shown cards stay on
// screen before the next hand starts.
const revealHold = 4 * time.Second

// holdForReveal moves the next-hand start later, when necessary, so that
// everyone sees cards that were just revealed between hands.
func (r *Room) holdForReveal() {
	if r.nextHandAt.IsZero() {
		return
	}
	if min := r.now().Add(revealHold); r.nextHandAt.Before(min) {
		r.nextHandAt = min
	}
}

// tick runs once each second. It fires the turn clock, the run-out timer
// and the next-hand timer, and sends fresh snapshots while a timer runs.
func (r *Room) tick() {
	if r.deadline.IsZero() && r.nextHandAt.IsZero() && r.runoutAt.IsZero() {
		return
	}
	now := r.now()
	if !r.deadline.IsZero() && !now.Before(r.deadline) {
		r.timeout()
	}
	if !r.runoutAt.IsZero() && !now.Before(r.runoutAt) {
		r.dealRunout()
	}
	if !r.nextHandAt.IsZero() && !now.Before(r.nextHandAt) {
		r.nextHandAt = time.Time{}
		if r.table.CanStartHand() {
			r.startHand()
		}
	}
	r.settle()
}

// dealRunout deals the next street of a paused run-out. The settle that
// follows logs the street, sets the timer for the next street, or runs the
// normal end of the hand after the river.
func (r *Room) dealRunout() {
	r.runoutAt = time.Time{}
	r.drainEvents() // Keep the log in order: earlier events, then the street.
	// An error means that no run-out waits now. The timer stays clear, and
	// the next step sets it again only while a run-out waits.
	_ = r.table.DealRunout()
}

// timeout plays the default action for the player whose clock ran out. The
// player then sits out from the next hand, so that an absent player does not
// make the table wait in each hand. The player presses O to play again.
func (r *Room) timeout() {
	id := r.clockFor
	r.stopClock()
	if cur, ok := r.table.ToAct(); !ok || cur != id {
		return
	}
	r.drainEvents() // Keep the log in order: earlier events, then the timeout line.
	if a := r.st.Accounts[id]; a != nil {
		r.logf("%s timed out", a.Name)
	}
	r.actDefault(id)
	_ = r.table.SetSittingOut(id, true)
}
