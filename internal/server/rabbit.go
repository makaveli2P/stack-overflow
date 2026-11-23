package server

import (
	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 10: each account can do Config.RabbitHunts rabbit hunts in one
// server run, and after a hunt each player who was dealt into the hand sees
// what the own hole cards would have made (Me.RabbitHand).

// rabbitsLeft returns the rabbit hunts that key can still do in this server
// run (Me.RabbitsLeft). It is 0 when Config.RabbitHunts is 0 (no limit).
func (r *Room) rabbitsLeft(key string) int {
	if r.cfg.RabbitHunts <= 0 {
		return 0
	}
	return max(0, r.cfg.RabbitHunts-r.rabbitUsed[key])
}

// rabbitHand returns what key would have had after the rabbit hunt of the
// last hand: the best five cards of the hole cards of key (Room.holes) with
// the dealt board plus the rabbit cards. view is the view of key. The value
// is zero when no hand result with rabbit cards is on the table, when key
// was not dealt into that hand, or when the kept hole cards belong to
// another hand. Only the snapshot of key may carry the value.
func (r *Room) rabbitHand(key string, view engine.View) RabbitHand {
	res := view.Result
	if view.InHand || res == nil || len(res.Rabbit) == 0 || res.HandNumber != r.holesHand {
		return RabbitHand{}
	}
	hole := r.holes[key]
	board := len(res.Board) + len(res.Rabbit)
	if len(hole) != 2 || board != 5 {
		return RabbitHand{}
	}
	all := make([]cards.Card, 0, 7)
	all = append(append(append(all, hole...), res.Board...), res.Rabbit...)
	hv, best := cards.Best(all)
	return RabbitHand{Category: hv.Category, Hand: hv.String(), Best: best[:]}
}
