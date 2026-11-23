package server

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// hurryBefore is how long before the deadline the "hurry" alert fires.
const hurryBefore = 5 * time.Second

// broadcast sends a personal snapshot to every session.
func (r *Room) broadcast() {
	if len(r.sessions) == 0 {
		return
	}
	onTable := r.tableChips()
	online := make(map[string]bool, len(r.sessions))
	for key := range r.sessions {
		online[key] = true
	}
	playing := make(map[string]bool)
	for _, sv := range r.table.View("").Seats {
		if !sv.Empty {
			playing[sv.PlayerID] = true
		}
	}
	now := r.now()
	rows, ids := r.st.statsTable(onTable, online, playing)
	var hostRows []HostStats // made once, when a host is online
	base := Snapshot{
		Log:         r.log.items(),
		Chat:        r.chat.items(),
		Spectators:  r.spectators(),
		Leaderboard: r.leaderboard(),
		Deadline:    r.deadline,
		ClockTotal:  r.clockTotal(),
		NextHandAt:  r.nextHandAt,
		Now:         now,
		Config:      r.cfg,
		Stats:       rows,
		Settle:      r.st.settlement(onTable, r.cfg.ChipsPerUnit, r.cfg.Currency),
		PanicSeq:    r.panicSeq,
		Waiting:     r.waiting,
		StartedBy:   r.startedBy,
	}
	for _, s := range r.sessions {
		snap := base
		// Each session gets its own slices, so that one TUI cannot change
		// the data of another.
		snap.Log = slices.Clone(base.Log)
		snap.Chat = slices.Clone(base.Chat)
		snap.Spectators = slices.Clone(base.Spectators)
		snap.Leaderboard = slices.Clone(base.Leaderboard)
		snap.Config.Admins = slices.Clone(base.Config.Admins)
		snap.Stats = cloneStats(base.Stats)
		if s.host && hostRows == nil {
			hostRows = make([]HostStats, len(ids))
			for i, id := range ids {
				hostRows[i] = r.st.hostStats(id)
			}
		}
		r.viewerStats(snap.Stats, ids, s, hostRows)
		snap.Settle = cloneSettlement(base.Settle)
		snap.Table = r.table.View(s.key)
		snap.Me = Me{
			Key:     s.ident,
			Name:    s.name,
			Seat:    snap.Table.MySeat,
			CanLink: r.canLink(s),
			Admin:   r.isAdmin(s),
			Keyless: s.keyless,
			Host:    s.host,
			Alerts:  true,
		}
		snap.Me.ExtensionsLeft = r.extensionsLeft(s.key)
		snap.Me.RabbitsLeft = r.rabbitsLeft(s.key)
		snap.Me.RabbitHand = r.rabbitHand(s.key, snap.Table) // only the owner's snapshot has it
		snap.Me.ShowAtEnd = r.showAtEnd[s.key]               // only the owner's snapshot has it
		if a := r.st.Accounts[s.key]; a != nil {
			snap.Me.Bank = a.Bank
			snap.Me.NeedsName = !a.Named
			snap.Me.Alerts = !a.AlertsOff
		}
		snap.Alert = r.alertFor(s, now)
		s.push(snap)
	}
}

// viewerStats adds the stats that depend on the viewer s to its own copy of
// the rows: the host stats in every row when s is the host, and the
// favourite hand in the row of s only (also for the host, no other row).
func (r *Room) viewerStats(rows []PlayerStats, ids []string, s *Session, hostRows []HostStats) {
	for i, id := range ids {
		if s.host {
			h := hostRows[i]
			rows[i].Host = &h
		}
		if id == s.key {
			if a := r.st.Accounts[id]; a != nil {
				rows[i].FavouriteHand, rows[i].FavouriteCount = favourite(a.Fun.Starting)
			}
		}
	}
}

// alertFor returns the one-shot cue for s: "turn" once for each new turn
// of s, then "hurry" once for each deadline when hurryBefore or less
// remains on its clock. A time extension makes a new deadline, so "hurry"
// can fire again.
func (r *Room) alertFor(s *Session, now time.Time) string {
	if r.turnFor == "" || r.turnFor != s.key {
		return ""
	}
	if s.turnAlert != r.turnSeq {
		s.turnAlert = r.turnSeq
		return "turn"
	}
	if r.clockFor == s.key && !r.deadline.IsZero() && r.deadline.Sub(now) <= hurryBefore && s.hurryAlert != r.clockSeq {
		s.hurryAlert = r.clockSeq
		return "hurry"
	}
	return ""
}

// pushHook sees every snapshot that the room sends, on the Run goroutine.
// It is nil in production. Tests set it to check each snapshot.
var pushHook func(s *Session, snap Snapshot)

// push puts snap in the channel. An older snapshot that the client did not
// read yet is dropped; its alert moves to snap when snap has none, so that
// no cue is lost. Only the Run goroutine sends, so the send cannot block.
func (s *Session) push(snap Snapshot) {
	if pushHook != nil {
		pushHook(s, snap)
	}
	select {
	case old := <-s.ch:
		if snap.Alert == "" {
			snap.Alert = old.Alert
		}
	default:
	}
	select {
	case s.ch <- snap:
	default:
	}
}

// spectators returns the sorted names of connected players without a seat.
func (r *Room) spectators() []string {
	var names []string
	for key, s := range r.sessions {
		if !r.seated(key) {
			names = append(names, s.name)
		}
	}
	slices.Sort(names)
	return names
}

// leaderboard returns one row for each account that bought chips in this
// period or is online, best Net first. Net changes only when a hand ends,
// not with each bet (see tableChips).
func (r *Room) leaderboard() []Standing {
	onTable := r.tableChips()
	rows := make([]Standing, 0, len(r.st.Accounts))
	for key, a := range r.st.Accounts {
		_, seated := r.table.Stack(key)
		_, online := r.sessions[key]
		if !online && !seated && a.BuyIns == 0 && a.net(onTable[key]) == 0 {
			continue
		}
		rows = append(rows, Standing{
			Name:    a.Name,
			Net:     a.net(onTable[key]),
			BuyIns:  a.BuyIns,
			Online:  online,
			Playing: seated,
		})
	}
	slices.SortFunc(rows, func(a, b Standing) int {
		if c := cmp.Compare(b.Net, a.Net); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return rows
}
