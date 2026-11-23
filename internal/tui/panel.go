package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// panelView draws the log, chat or players tab in a w x h block. Full mode
// draws a border; compact mode draws a rule with the tab names.
func (m *model) panelView(w, h int, boxed bool) string {
	if boxed {
		inner, innerH := w-4, h-2
		body := m.tabBar(inner) + "\n" + m.st.faint.Render(strings.Repeat("─", inner)) + "\n" +
			m.panelBody(inner, innerH-2)
		return m.st.panel.Width(w - 2).Height(innerH).Render(clip(body, inner, innerH))
	}
	bar := m.tabBar(w - 4)
	rule := m.st.faint.Render("── ") + bar + " " + m.st.faint.Render(strings.Repeat("─", max(w-lipgloss.Width(bar)-4, 0)))
	return clip(rule+"\n"+m.panelBody(w, h-1), w, h)
}

func (m *model) tabBar(w int) string {
	names := [numTabs]string{"Chat", "Log", "Players"}
	parts := make([]string, numTabs)
	for i, n := range names {
		t := panelTab(i)
		switch {
		case t == m.tab:
			parts[i] = m.st.tabOn.Render(n)
		case t == tabChat && m.chatLitAny():
			// A new message is highlighted: the tab name is too.
			parts[i] = m.st.tabLit.Render(n)
			if m.st.noColor {
				parts[i] = bold(n)
			}
			if m.unreadChat() {
				parts[i] += m.st.tabUnread.Render("•")
			}
		case t == tabChat && m.unreadChat():
			parts[i] = m.st.tabUnread.Render(n + "•")
		default:
			parts[i] = m.st.tabOff.Render(n)
		}
	}
	return fit(strings.Join(parts, m.st.faint.Render("  ")), w)
}

func (m *model) panelBody(w, h int) string {
	if h <= 0 {
		return ""
	}
	var lines []string
	switch m.tab {
	case tabLog:
		lines = m.logLines(w)
	case tabChat:
		lines = m.chatLines(w)
	case tabPlayers:
		lines = m.playerLines(w)
		if len(lines) > h {
			lines = lines[:h]
		}
		return strings.Join(lines, "\n")
	}
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	return strings.Join(lines, "\n")
}

func (m *model) logLines(w int) []string {
	if len(m.snap.Log) == 0 {
		return []string{m.st.faint.Render("Nothing has happened yet.")}
	}
	var out []string
	for _, l := range m.snap.Log {
		if div, rest, ok := handDivider(l); ok {
			// A new deal: a rule line, then the rest of the line.
			out = append(out, m.st.logHdr.Render(fit(div, w)))
			l = rest
		}
		base := m.logStyle(l)
		for _, wl := range wrapLines(l, w, 2) {
			out = append(out, m.colorSuits(wl, base))
		}
	}
	return out
}

// handDivider splits the engine's first line of a hand,
// "Hand #12 — Alice has the button", into the divider "── Hand #12 ──"
// and the rest, "Alice has the button". ok is false for other lines.
func handDivider(l string) (div, rest string, ok bool) {
	head, rest, found := strings.Cut(l, " — ")
	num, isHand := strings.CutPrefix(head, "Hand #")
	if !found || !isHand || num == "" || strings.Trim(num, "0123456789") != "" {
		return "", "", false
	}
	return "── " + head + " ──", rest, true
}

func (m *model) logStyle(l string) lipgloss.Style {
	switch {
	case strings.HasPrefix(l, "Hand #"):
		return m.st.logHdr
	case strings.HasPrefix(l, "Flop") || strings.HasPrefix(l, "Turn") || strings.HasPrefix(l, "River"):
		return m.st.logStreet
	case strings.Contains(l, " wins "):
		return m.st.logWin
	case strings.Contains(l, "busted") || strings.Contains(l, "all-in"):
		return m.st.bet
	}
	return m.st.dim
}

// colorSuits draws ♥ and ♦ in red and the rest of the line in base.
func (m *model) colorSuits(line string, base lipgloss.Style) string {
	var b, run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			b.WriteString(base.Render(run.String()))
			run.Reset()
		}
	}
	for _, r := range line {
		if r == '♥' || r == '♦' {
			flush()
			b.WriteString(m.st.redSuit.Render(string(r)))
			continue
		}
		run.WriteRune(r)
	}
	flush()
	return b.String()
}

// chatShotText is the chat text of a shared screenshot. The server sends
// the same words in ChatLine.Text.
const chatShotText = "shared a screenshot"

// shotLinkTooLong replaces a link that the chat panel cannot show in
// full (batch 15). The notice line has the link. shotLinkTooLongShort is
// the form for a panel that is narrower than shotLinkTooLong.
const (
	shotLinkTooLong      = "(link too long for this panel)"
	shotLinkTooLongShort = "(link too long)"
)

func (m *model) chatLines(w int) []string {
	if len(m.snap.Chat) == 0 {
		return []string{m.st.faint.Render("No messages yet. Press T to chat.")}
	}
	showTime := w >= 40
	now := m.clock()
	var out []string
	for ci, c := range m.snap.Chat {
		stamp := ""
		if showTime {
			stamp = c.At.Local().Format("15:04") + " "
		}
		// A shared screenshot (batch 14) reads "<name> shared a
		// screenshot", with the link on the next line.
		sep, text := ": ", c.Text
		if c.Shot != "" {
			sep, text = " ", chatShotText
		}
		prefix := stamp + c.Name + sep
		stampSt, nameSt, sepSt, textSt, linkSt, dimSt := m.st.faint, m.st.nameStyle(c.Name), m.st.dim, m.st.text, m.st.info, m.st.dim
		lit := w >= 2 && m.chatLit(ci, now)
		tw := w
		if lit {
			// The marker takes one cell of each line.
			tw = w - 1
			// The time uses dim, not faint: faint is too weak on the
			// background, mainly with 16 colours.
			hl := m.st.chatHL
			stampSt, nameSt, sepSt, textSt, linkSt, dimSt = m.st.dim.Inherit(hl), nameSt.Inherit(hl), sepSt.Inherit(hl), textSt.Inherit(hl), linkSt.Inherit(hl), dimSt.Inherit(hl)
		}
		lines := wrapLines(prefix+text, tw, 2)
		link, tooLong := -1, false // the index of the link line
		if c.Shot != "" {
			// A cut link is a trap: the terminal still makes the cut text
			// a link, and it opens a wrong address. So the panel shows
			// the link only in full, with no indent (batch 15); else a
			// dim line says that it does not fit.
			link = len(lines)
			if lipgloss.Width(c.Shot) <= tw {
				lines = append(lines, c.Shot)
			} else {
				msg := shotLinkTooLong
				if lipgloss.Width(msg) > tw {
					msg = shotLinkTooLongShort
				}
				lines = append(lines, fit(msg, tw))
				tooLong = true
			}
		}
		for i, wl := range lines {
			lw := lipgloss.Width(wl)
			if lit && m.st.noColor {
				out = append(out, bold(chatMarker+wl))
				continue
			}
			switch {
			case i == link && tooLong:
				wl = dimSt.Render(wl)
			case i == link:
				wl = linkSt.Render(wl)
			case i == 0 && strings.HasPrefix(wl, prefix):
				wl = stampSt.Render(stamp) + nameSt.Render(c.Name) +
					sepSt.Render(sep) + textSt.Render(wl[len(prefix):])
			default:
				wl = textSt.Render(wl)
			}
			if lit {
				// The background fills the whole line.
				wl = m.st.chatMark.Render(chatMarker) + wl + m.st.chatHL.Render(strings.Repeat(" ", max(tw-lw, 0)))
			}
			out = append(out, wl)
		}
	}
	return out
}

func (m *model) playerLines(w int) []string {
	st := m.st
	wide := w >= 32
	nameW := w - 14
	if wide {
		nameW = w - 19
	}
	head := fmt.Sprintf("%-3s%-*s %8s", "#", nameW+2, "Player", "Net")
	if wide {
		head += fmt.Sprintf(" %4s", "Buys")
	}
	out := []string{st.faint.Render(head)}
	for i, s := range m.snap.Leaderboard {
		dot := st.faint.Render("○")
		switch {
		case s.Playing && s.Online:
			dot = st.win.Render("●")
		case s.Online:
			dot = st.gold.Render("●")
		}
		ns := st.text
		if s.Name == m.snap.Me.Name && s.Online {
			ns = st.me
		}
		net := st.dim
		if s.Net > 0 {
			net = st.win
		} else if s.Net < 0 {
			net = st.err
		}
		row := fmt.Sprintf("%-3d", i+1) + dot + " " + ns.Render(padRight(s.Name, nameW)) + " " +
			net.Render(fmt.Sprintf("%8s", signed(s.Net)))
		if wide {
			row += st.dim.Render(fmt.Sprintf(" %4d", s.BuyIns))
		}
		out = append(out, row)
	}
	out = append(out, "", st.faint.Render("● playing  ")+st.gold.Render("●")+st.faint.Render(" watching  ○ offline"))
	if len(m.snap.Spectators) > 0 {
		out = append(out, st.dim.Render("Watching: ")+st.text.Render(strings.Join(m.snap.Spectators, ", ")))
	}
	return out
}
