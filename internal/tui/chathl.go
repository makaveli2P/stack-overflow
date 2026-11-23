package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// A chat message from another player is highlighted for chatHLFor after
// this model first sees it (batch 11). The model uses its own clock, not
// ChatLine.At, so a clock difference between the server and the client
// does not matter. See DESIGN.md, "Batch 11: highlight new chat messages".
const (
	chatHLFor  = 5 * time.Second
	chatMarker = "▌"
)

// chatHL is the highlight state of the chat.
type chatHL struct {
	// until has one item for each line of snap.Chat, in the same order:
	// the end of the highlight of that line, or zero for no highlight.
	// It is nil when no line has a highlight.
	until []time.Time
	// tickAt is the highlight end that the pending tick is for. It is zero
	// when no tick is pending.
	tickAt time.Time
	gen    int // changes at each new tick, so an old tick does nothing
}

// chatHLMsg is the tick that ends a highlight. gen is the chatHL.gen that
// sent it.
type chatHLMsg struct{ gen int }

// sameChat reports whether a and b are the same chat message.
func sameChat(a, b server.ChatLine) bool {
	return a.At.Equal(b.At) && a.Name == b.Name && a.Text == b.Text && a.Shot == b.Shot
}

// chatOverlap returns the number of lines at the start of cur that are
// the same as the lines at the end of prev. The server keeps only the
// newest lines, so cur is the end of prev followed by the new lines.
func chatOverlap(prev, cur []server.ChatLine) int {
	if len(prev) == 0 {
		return 0
	}
	last := prev[len(prev)-1]
	for k := min(len(prev), len(cur)); k > 0; k-- {
		if !sameChat(cur[k-1], last) {
			continue
		}
		off := len(prev) - k
		match := true
		for i := 0; i < k-1; i++ {
			if !sameChat(cur[i], prev[off+i]) {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return 0
}

// followChat moves the highlights from prev, the chat of the old
// snapshot, to m.snap.Chat, and gives a highlight to each new message
// from another player. The messages of the first snapshot are the
// baseline and get no highlight. It returns the newest new line from
// another player that shares a screenshot (batch 14), and false when no
// such line came.
func (m *model) followChat(prev []server.ChatLine, first bool) (shared server.ChatLine, ok bool) {
	cur := m.snap.Chat
	old := m.hl.until
	m.hl.until = nil
	if first {
		return server.ChatLine{}, false
	}
	now := m.clock()
	set := func(i int, t time.Time) {
		if m.hl.until == nil {
			m.hl.until = make([]time.Time, len(cur))
		}
		m.hl.until[i] = t
	}
	k := chatOverlap(prev, cur)
	if len(old) == len(prev) {
		drop := len(prev) - k
		for i := 0; i < k; i++ {
			if t := old[drop+i]; t.After(now) {
				set(i, t)
			}
		}
	}
	for i := k; i < len(cur); i++ {
		c := cur[i]
		if c.Name == m.snap.Me.Name {
			continue
		}
		if k == 0 && len(prev) > 0 && !c.At.After(prev[len(prev)-1].At) {
			// No line is the same as before: only the lines after the
			// newest old line are new.
			continue
		}
		set(i, now.Add(chatHLFor))
		if c.Shot != "" {
			shared, ok = c, true
		}
	}
	return shared, ok
}

// chatLit reports whether chat line i has a highlight now.
func (m *model) chatLit(i int, now time.Time) bool {
	return i < len(m.hl.until) && m.hl.until[i].After(now)
}

// chatLitAny reports whether a chat line has a highlight now.
func (m *model) chatLitAny() bool {
	now := m.clock()
	for i := range m.hl.until {
		if m.chatLit(i, now) {
			return true
		}
	}
	return false
}

// chatHLTick starts the tick for the next highlight end. It starts no
// tick when no highlight is on, when a pending tick comes in time, or
// while the panic screen is up: the exit from the panic screen calls it
// again.
func (m *model) chatHLTick() tea.Cmd {
	if m.panic.on {
		return nil
	}
	now := m.clock()
	var next time.Time
	for _, t := range m.hl.until {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	if next.IsZero() || (!m.hl.tickAt.IsZero() && !m.hl.tickAt.After(next)) {
		return nil
	}
	m.hl.gen++
	m.hl.tickAt = next
	gen := m.hl.gen
	return tea.Tick(next.Sub(now), func(time.Time) tea.Msg { return chatHLMsg{gen} })
}

// chatHLDone handles the tick: it removes the highlights that ended and
// starts the tick for the next end.
func (m *model) chatHLDone(msg chatHLMsg) tea.Cmd {
	if msg.gen != m.hl.gen {
		return nil
	}
	m.hl.tickAt = time.Time{}
	now := m.clock()
	on := false
	for i, t := range m.hl.until {
		if t.After(now) {
			on = true
		} else {
			m.hl.until[i] = time.Time{}
		}
	}
	if !on {
		m.hl.until = nil
	}
	return m.chatHLTick()
}
