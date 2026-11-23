package server

import (
	"errors"
	"strings"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

const (
	chatBurst  = 5
	chatWindow = 5 * time.Second
)

var errChatTooFast = errors.New("too many messages: wait a few seconds")

// dropSession removes s from the room and closes its Updates channel.
func (r *Room) dropSession(s *Session) {
	if r.sessions[s.key] == s {
		delete(r.sessions, s.key)
	}
	if !s.closed.Swap(true) {
		close(s.ch)
	}
}

// call runs fn on the Run goroutine while s is the live session for its key.
func (s *Session) call(fn func(r *Room) error) error {
	if s.closed.Load() {
		return ErrClosed
	}
	r := s.room
	return r.do(func() error {
		if r.sessions[s.key] != s {
			return ErrClosed
		}
		return fn(r)
	})
}

func (s *Session) updates() <-chan Snapshot { return s.ch }

func (s *Session) sit(seat int) error { return s.sitWith(seat, SitWholeBank) }

func (s *Session) stand() error {
	return s.call(func(r *Room) error { return r.handleStand(s) })
}

func (s *Session) act(a engine.Action) error {
	return s.call(func(r *Room) error {
		if err := r.table.Act(s.key, a); err != nil {
			return err
		}
		r.newTurn() // A new turn starts, also when the same player acts next.
		r.settle()
		return nil
	})
}

func (s *Session) sitOut(out bool) error {
	return s.call(func(r *Room) error {
		if err := r.table.SetSittingOut(s.key, out); err != nil {
			return err
		}
		r.settle()
		return nil
	})
}

func (s *Session) rebuy() error {
	return s.call(func(r *Room) error { return r.handleRebuy(s) })
}

func (s *Session) chat(text string) error {
	return s.call(func(r *Room) error { return r.handleChat(s, text) })
}

func (s *Session) close() {
	if s.closed.Load() {
		return
	}
	_ = s.room.do(func() error {
		s.room.handleClose(s)
		return nil
	})
}

func (r *Room) freeSeat() int {
	for _, sv := range r.table.View("").Seats {
		if sv.Empty {
			return sv.Index
		}
	}
	return -1
}

func (r *Room) handleStand(s *Session) error {
	if err := r.standUp(s.key, s.name); err != nil {
		return err
	}
	r.settle()
	return nil
}

// standUp removes key from the table, adds the chips to the bank and saves.
// The bank can hold chips already (a sit with SitBuyIn keeps the rest of the
// bank), so standUp adds and never sets the bank.
// The chips that key put in during the live hand stay in the pot. standUp
// records them in leftInHand, so that the saved bank refunds them when the
// server stops before the hand ends.
func (r *Room) standUp(key, name string) error {
	committed := r.committedInHand(key)
	chips, err := r.table.Stand(key)
	if err != nil {
		return err
	}
	r.dropShow(key)
	if committed > 0 && r.table.InHand() {
		r.leftInHand[key] += committed
	}
	r.account(key, name).Bank += chips
	r.save()
	return nil
}

// committedInHand returns the chips that key put in during the live hand.
func (r *Room) committedInHand(key string) int64 {
	view := r.table.View("")
	if !view.InHand {
		return 0
	}
	for _, sv := range view.Seats {
		if !sv.Empty && sv.PlayerID == key && sv.InHand {
			return sv.Committed
		}
	}
	return 0
}

func (r *Room) handleChat(s *Session, text string) error {
	text = cleanChat(text)
	if text == "" {
		return ErrEmptyChat
	}
	now := r.now()
	if !s.chatLimit.allow(now) {
		return errChatTooFast
	}
	r.chat.add(ChatLine{At: now, Name: s.name, Text: text})
	r.broadcast()
	return nil
}

func (r *Room) handleClose(s *Session) {
	if r.sessions[s.key] != s {
		return
	}
	r.dropSession(s)
	r.logf("%s left", s.name)
	if r.seated(s.key) {
		if r.table.InHand() {
			r.away[s.key] = true
		} else {
			_ = r.standUp(s.key, s.name)
		}
	}
	r.settle()
}

// Screenshot links in the chat (DESIGN.md "Batch 14" and "Batch 15").
const (
	shotText    = "shared a screenshot" // ChatLine.Text of a shared shot
	maxShotLink = 120                   // bytes
)

// waitingShot is a screenshot link that waits for the end of the live
// hand (batch 15). name is the name of the player at the press.
type waitingShot struct {
	key  string
	name string
	url  string
}

func (s *Session) shareShot(url string) (later bool, err error) {
	if !validShotLink(url) {
		return false, ErrBadShot
	}
	err = s.call(func(r *Room) error {
		var e error
		later, e = r.handleShareShot(s, url)
		return e
	})
	return later, err
}

// validShotLink reports whether url can go in the chat as a screenshot
// link: it starts with "http://", it has at most maxShotLink bytes, and it
// has only printable ASCII. So it has no space, no control character and
// no escape sequence, and the TUI can draw it as it is.
func validShotLink(url string) bool {
	if len(url) > maxShotLink || len(url) <= len("http://") || !strings.HasPrefix(url, "http://") {
		return false
	}
	for i := 0; i < len(url); i++ {
		if c := url[i]; c <= ' ' || c >= 0x7f {
			return false
		}
	}
	return true
}

// handleShareShot posts the chat line of a shared shot, or keeps it for
// the end of the live hand (later is true). The press uses the chat rate
// limit of s, so a shot counts as one message, also when it waits.
func (r *Room) handleShareShot(s *Session, url string) (later bool, err error) {
	// A link waits only when endHand runs for this hand: handLive is true
	// from the deal to endHand. An all-in run-out is still a live hand.
	live := r.handLive && r.table.InHand()
	if live && r.waitingShotsOf(s.key) >= MaxWaitingShots {
		return false, ErrShotsWaiting
	}
	now := r.now()
	if !s.chatLimit.allow(now) {
		return false, errChatTooFast
	}
	if live {
		r.waitingShots = append(r.waitingShots, waitingShot{key: s.key, name: s.name, url: url})
		return true, nil
	}
	r.chat.add(ChatLine{At: now, Name: s.name, Text: shotText, Shot: url})
	r.broadcast()
	return false, nil
}

// waitingShotsOf returns the number of links of key that wait for the end
// of the hand.
func (r *Room) waitingShotsOf(key string) int {
	n := 0
	for _, w := range r.waitingShots {
		if w.key == key {
			n++
		}
	}
	return n
}

// postWaitingShots posts the links that waited for the end of the hand,
// in the order in which they reached the server (the ShareShot calls).
// The TUI calls ShareShot when the picture is ready, so two players who
// press P very close together can come in the other order. The links of
// one player keep the order of the presses. They do not use the chat
// rate limit: the press did. endHand calls it, so the lines come with the result, before
// the next deal. The caller sends the snapshots.
func (r *Room) postWaitingShots() {
	if len(r.waitingShots) == 0 {
		return
	}
	now := r.now()
	for _, w := range r.waitingShots {
		r.chat.add(ChatLine{At: now, Name: w.name, Text: shotText, Shot: w.url})
	}
	clear(r.waitingShots)
	r.waitingShots = r.waitingShots[:0]
}
