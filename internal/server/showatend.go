package server

import "github.com/makaveli2p/stack-overflow/internal/engine"

// Batch 9: a player who folds can show the hole cards, but only after the
// hand ends. During the hand the room only stores the choice (showAtEnd);
// endHand gives each stored choice to the engine before the first snapshot
// of the result.

// showCards is Session.ShowCards. During a hand it stores the choice of a
// folded player; between hands the engine shows the cards at once.
func (s *Session) showCards(left, right bool) error {
	return s.call(func(r *Room) error {
		if r.table.InHand() {
			if err := r.storeShow(s.key, left, right); err != nil {
				return err
			}
			r.settle()
			return nil
		}
		if err := r.table.ShowCards(s.key, left, right); err != nil {
			return err
		}
		r.holdForReveal()
		r.settle()
		return nil
	})
}

// storeShow stores, changes or cancels the show choice of key for the end
// of the live hand. Only a seated player who folded in this hand may store
// a choice; anyone else gets ErrNotNow. The same choice again, or no card,
// cancels the choice. Nothing becomes visible to other players.
func (r *Room) storeShow(key string, left, right bool) error {
	if !r.foldedInHand(key) {
		return engine.ErrNotNow
	}
	want := [2]bool{left, right}
	if want == [2]bool{} || r.showAtEnd[key] == want {
		delete(r.showAtEnd, key)
		return nil
	}
	r.showAtEnd[key] = want
	return nil
}

// foldedInHand reports whether key sits in a seat that was dealt into the
// live hand, and folded in it.
func (r *Room) foldedInHand(key string) bool {
	v := r.table.View(key)
	if !v.InHand || v.MySeat < 0 || v.MySeat >= len(v.Seats) {
		return false
	}
	sv := v.Seats[v.MySeat]
	return sv.InHand && sv.Folded && len(sv.Cards) == 2
}

// applyShows gives the stored choices to the engine, in seat order, right
// after a hand ended. It clears every choice. A choice that the engine
// refuses (for example, the player is no longer in that seat) is dropped.
// It returns true when a card was shown.
func (r *Room) applyShows() bool {
	if len(r.showAtEnd) == 0 {
		return false
	}
	r.drainEvents() // Keep the log in order: the result lines, then the shows.
	shown := false
	for _, sv := range r.table.View("").Seats {
		c, ok := r.showAtEnd[sv.PlayerID]
		if sv.Empty || !ok {
			continue
		}
		if r.table.ShowCards(sv.PlayerID, c[0], c[1]) == nil {
			shown = true
		}
	}
	clear(r.showAtEnd)
	r.drainEvents()
	return shown
}

// dropShow forgets the stored show choice of key (the player left the seat).
func (r *Room) dropShow(key string) { delete(r.showAtEnd, key) }
