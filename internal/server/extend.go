package server

import (
	"strconv"
	"time"
)

func (s *Session) extend() error {
	return s.call(func(r *Room) error { return r.handleExtend(s) })
}

// handleExtend adds ExtensionTime to the deadline of the running turn clock
// of s and uses one extension of the account. The count belongs to the
// account key, so a reconnect does not reset it.
func (r *Room) handleExtend(s *Session) error {
	if r.cfg.Extensions <= 0 {
		return ErrNoExtensions
	}
	if r.clockFor != s.key || r.deadline.IsZero() || !r.now().Before(r.deadline) {
		return ErrCannotExtend
	}
	if id, ok := r.table.ToAct(); !ok || id != s.key {
		return ErrCannotExtend
	}
	left := r.extensionsLeft(s.key)
	if left <= 0 {
		return ErrNoExtensions
	}
	r.extUsed[s.key]++
	r.turnExt++
	r.deadline = r.deadline.Add(r.cfg.ExtensionTime)
	r.clockSeq++ // A new deadline: the "hurry" alert can fire again.
	r.logf("%s adds %s (%d left)", s.name, seconds(r.cfg.ExtensionTime), left-1)
	r.settle()
	return nil
}

// extensionsLeft returns the time extensions that key can still use in this
// server run (Me.ExtensionsLeft).
func (r *Room) extensionsLeft(key string) int {
	if r.cfg.Extensions <= 0 {
		return 0
	}
	return max(0, r.cfg.Extensions-r.extUsed[key])
}

// seconds formats d as seconds for the log: "30s" for whole seconds, else
// the shortest decimal form, e.g. "1.5s".
func seconds(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}
