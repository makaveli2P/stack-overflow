package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Full mode needs this much room; smaller terminals get compact mode.
const (
	fullW = 100
	fullH = 30
)

func (m *model) View() string {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		return ""
	}
	var out string
	switch {
	case m.panic.on:
		out = m.panicView(w, h)
	case m.gone:
		out = m.message(w, h, "Disconnected", "The room closed this session. Goodbye!")
	case !m.haveSnap:
		out = m.message(w, h, "STACK OVERFLOW", "Connecting to the table…")
	default:
		out = m.screen(w, h)
	}
	return clip(out, w, h)
}

func (m *model) message(w, h int, title, text string) string {
	box := m.st.felt.Padding(1, 3).Render(m.st.bold.Render(title) + "\n\n" + m.st.dim.Render(text))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func (m *model) screen(w, h int) string {
	switch {
	case m.inWelcome():
		return m.welcomeScreen(w, h)
	case m.mode == modeDash:
		return m.dashScreen(w, h)
	}
	full := w >= fullW && h >= fullH
	footer := m.footer(w, full)
	bodyH := max(h-1-len(footer), 1)

	var body string
	switch {
	case m.mode == modeProfile || m.mode == modeRename || m.mode == modeLink:
		body = m.profileView(w, bodyH)
	case m.help:
		body = m.helpView(w, bodyH)
	case full:
		pw := min(max(w*3/10, 30), 46)
		tw := w - pw - 1
		table := clip(m.tableView(tw, bodyH, true), tw, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.PlaceHorizontal(tw, lipgloss.Left, table), " ", m.rightColumn(pw, bodyH))
	default:
		table := m.tableView(w, 0, false)
		if rest := bodyH - height(table); rest >= 3 {
			body = table + "\n" + m.panelView(w, rest, false)
		} else {
			body = table
		}
	}
	if m.mode == modeSit {
		// The picker covers the middle of the table; in full mode the
		// panel stays free.
		areaW := w
		if full {
			areaW = w - min(max(w*3/10, 30), 46) - 1
		}
		body = overlay(body, clip(m.pickerBox(areaW), areaW, bodyH), areaW, bodyH)
	}
	return m.header(w) + "\n" + fixHeight(body, bodyH) + "\n" + strings.Join(footer, "\n")
}

// fixHeight cuts or pads a block to exactly h lines.
func fixHeight(block string, h int) string {
	lines := strings.Split(block, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

func (m *model) header(w int) string {
	st := m.st
	t := m.snap.Table
	seated, playing := 0, 0
	for _, s := range t.Seats {
		if !s.Empty {
			seated++
			if s.InHand {
				playing++
			}
		}
	}
	info := fmt.Sprintf("Blinds %s/%s · %d/%d seated", chips(t.SmallBlind), chips(t.BigBlind), seated, len(t.Seats))
	if n := len(m.snap.Spectators); n > 0 {
		info += fmt.Sprintf(" · %d watching", n)
	}
	left := st.title.Render("♠ STACK OVERFLOW") + " " + st.dim.Render(info)
	me := m.snap.Me
	// The bank is the viewer's own; a shared frame leaves it out.
	money := ""
	if !m.public {
		money = st.dim.Render(" · bank " + chips(me.Bank))
	}
	if s, ok := m.standing(me.Name); ok {
		net := st.dim
		if s.Net > 0 {
			net = st.win
		} else if s.Net < 0 {
			net = st.err
		}
		money = st.dim.Render(" · net ") + net.Render(signed(s.Net))
	}
	right := st.me.Render(me.Name) + money + st.dim.Render(" · ") + st.gold.Render("?") + st.dim.Render(" help")
	return spread(left, right, w)
}

// footer returns the lines under the table: the action or status line,
// in full mode an extra line, and the key hints (or a notice).
func (m *model) footer(w int, full bool) []string {
	main, extra := m.mainLine(w, full)
	hints := m.hintsLine(w)
	if m.notice.text != "" {
		n := m.noticeLine(w)
		if full && extra == "" {
			extra = n
		} else {
			hints = n
		}
	}
	if full {
		return []string{fit(main, w), fit(extra, w), fit(hints, w)}
	}
	return []string{fit(main, w), fit(hints, w)}
}

func (m *model) noticeLine(w int) string {
	if m.notice.url != "" {
		return m.linkNoticeLine(w)
	}
	if m.notice.err {
		return fit(m.st.err.Render("✗ "+m.notice.text), w)
	}
	return fit(m.st.info.Render("› "+m.notice.text), w)
}

func (m *model) btn(key, label string) string {
	return m.st.key.Render(key) + " " + m.st.keyLabel.Render(label)
}

// mainLine returns the action/status line and, for full mode, a second line.
func (m *model) mainLine(w int, full bool) (string, string) {
	st := m.st
	switch m.mode {
	case modeChat:
		return m.chatInput(w), ""
	case modeConfirm:
		return st.gold.Render(m.ask.text) + "   " + m.btn("Y", "yes") + "  " + m.btn("N", "no"), ""
	case modeRaise:
		return m.raiseLine(w, full)
	case modeSit:
		me := m.snap.Me
		return st.gold.Render("Sit down with") + " " + m.btn("1", "Buy-in "+chips(m.snap.Config.StartingStack)) +
			"  " + m.btn("2", "Whole bank "+chips(me.Bank)), ""
	}
	if m.snap.Table.Legal != nil {
		return m.actionBar(w), m.potOdds()
	}
	return m.statusLine(w), ""
}

func (m *model) chatInput(w int) string {
	label := m.st.gold.Render("Say ›") + " "
	room := w - lipgloss.Width(label) - 2
	text := string(m.chatBuf)
	if over := ansi.StringWidth(text) + 2 - room; over > 0 {
		text = ansi.TruncateLeft(text, over, "…")
	}
	field := m.st.inputBox.Render(padRight(text+"▏", room))
	return label + field
}

func (m *model) actionBar(w int) string {
	st := m.st
	l := m.snap.Table.Legal
	me, _ := m.mySeat()
	parts := []string{st.gold.Render("YOUR TURN")}
	if m.acting {
		parts = append(parts, st.dim.Render("sending…"))
	} else {
		if l.Fold {
			parts = append(parts, m.btn("F", "Fold"))
		}
		if l.Check {
			parts = append(parts, m.btn("C", "Check"))
		} else if l.Call {
			label := "Call " + chips(l.CallAmount)
			if l.CallAmount >= me.Stack {
				label += " (all-in)"
			}
			parts = append(parts, m.btn("C", label))
		}
		if l.Bet {
			parts = append(parts, m.btn("B", "Bet"))
		} else if l.Raise {
			parts = append(parts, m.btn("R", "Raise"))
		}
		// When a call already takes the whole stack, the call is the all-in.
		if l.AllIn && (!l.Call || l.CallAmount < me.Stack) {
			label := "All-in " + chips(me.Stack)
			if m.allInArmed() {
				parts = append(parts, st.key.Background(colErr).Render("A")+" "+st.allIn.Render(label+" · press A again"))
			} else {
				parts = append(parts, m.btn("A", label))
			}
		}
	}
	// The E hint must not push the line past w. Try the full hint, then
	// narrower gaps, then the hint without the count. The acting seat
	// also shows the clock, so the timer bar here leaves first.
	if labels := m.extendLabels(); labels != nil && !m.acting {
		for _, label := range labels {
			for _, sep := range []string{"   ", "  "} {
				left := strings.Join(append(parts[:len(parts):len(parts)], m.btn("E", label)), sep)
				if lipgloss.Width(left) <= w {
					return m.withTimer(left, w)
				}
			}
		}
	}
	return m.withTimer(strings.Join(parts, "   "), w)
}

// withTimer adds the timer bar on the right of the action bar when it fits.
func (m *model) withTimer(left string, w int) string {
	barW := min(24, w-lipgloss.Width(left)-3)
	if bar := m.timerBar(barW); bar != "" {
		return spread(left, bar, w)
	}
	return left
}

// potOdds shows the price of a call, for full mode. The odds count only the
// chips that the viewer can win: a bet larger than the viewer's whole hand
// total goes to a side pot that the viewer cannot win.
func (m *model) potOdds() string {
	t := m.snap.Table
	l := t.Legal
	me, ok := m.mySeat()
	if l == nil || !ok || !l.Call || l.CallAmount <= 0 {
		return ""
	}
	limit := me.Committed + me.Stack
	winnable := t.TotalPot
	for _, s := range t.Seats {
		winnable -= max(s.Committed-limit, 0)
	}
	pot := t.TotalPot
	// Truncate, so that 49.5% shows as 49%. A price under 1% shows as
	// "<1%": "0%" would read as a free call.
	odds := fmt.Sprintf("%d%%", 100*l.CallAmount/(winnable+l.CallAmount))
	if 100*l.CallAmount < winnable+l.CallAmount {
		odds = "<1%"
	}
	return m.st.dim.Render(fmt.Sprintf("Pot %s · to call %s · pot odds %s", chips(pot), chips(l.CallAmount), odds))
}

func (m *model) statusLine(w int) string {
	st := m.st
	t := m.snap.Table
	me, seated := m.mySeat()
	runout := t.InHand && t.Runout
	if m.startErr.text != "" && !t.InHand {
		return st.err.Render("✗ " + m.startErr.text)
	}
	switch {
	case m.snap.Waiting && !seated && !t.InHand:
		return st.text.Render("Sit down with ") + st.gold.Render("S") + st.text.Render(". A seated player starts with ") +
			st.gold.Render("G") + st.text.Render(".")
	case !seated && !runout:
		line := st.text.Render("You are watching. ") + m.btn("S", "Sit down")
		if m.public {
			// Both texts tell the viewer's bank, also "you buy in for"
			// (an empty bank).
			return line
		}
		buy := "bank " + chips(m.snap.Me.Bank)
		if m.snap.Me.Bank <= 0 {
			buy = "you buy in for " + chips(m.snap.Config.StartingStack)
		}
		return line + st.dim.Render(" ("+buy+")")
	case me.Busted && !me.InHand:
		label, detail := m.rebuyText()
		s := m.btn("B", label)
		if detail != "" {
			s += st.dim.Render(" (" + detail + ")")
		}
		// A long top-up text pushes out the lead text first.
		if lipgloss.Width(s)+lipgloss.Width("You are out of chips. ") > w {
			return s
		}
		return st.err.Render("You are out of chips. ") + s
	case m.showPrompt() != "":
		// The show prompt comes before the sit-out and waiting lines: it
		// is the only line that says which key shows which card.
		s := m.showPrompt()
		tail := ""
		switch {
		case me.SittingOut:
			tail = " · you are sitting out"
		case m.snap.Waiting && readyIn(m.snap) < 2:
			tail = " · waiting for players"
		case m.snap.Waiting:
			tail = " · waiting to start"
		case !m.snap.NextHandAt.IsZero():
			return s + st.dim.Render("   next hand in ") + st.gold.Render(m.untilText(m.snap.NextHandAt))
		}
		if tail != "" && ansi.StringWidth(s)+ansi.StringWidth(tail) <= w {
			s += st.dim.Render(tail)
		}
		return s
	case me.SittingOut && !me.InHand:
		return st.dim.Render("You are sitting out. ") + m.btn("O", "Play again")
	case m.snap.Waiting && !t.InHand && readyIn(m.snap) < 2:
		return st.gold.Render("Waiting for players") + st.dim.Render(fmt.Sprintf(" · %d of 2 ready", readyIn(m.snap)))
	case m.snap.Waiting && !t.InHand:
		return st.gold.Render("Waiting to start") + st.dim.Render(fmt.Sprintf(" · %d seated · press ", seatedIn(m.snap))) +
			st.gold.Render("G") + st.dim.Render(" when everyone is in")
	case t.InHand && seated && me.InHand && me.Folded && m.snap.Me.ShowAtEnd != [2]bool{}:
		return m.showAtEndLine(w)
	case runout:
		s := st.allIn.Render("All-in.") + st.dim.Render(" Dealing the board…")
		if seated && me.Folded {
			s = st.faint.Render("You folded. ") + s
		}
		return s
	case t.InHand && t.ToAct >= 0 && t.ToAct < len(t.Seats):
		who := t.Seats[t.ToAct].Name
		s := st.dim.Render("Waiting for ") + st.gold.Render(who)
		if left, ok := m.turnLeft(); ok {
			s += st.dim.Render(fmt.Sprintf(" · %ds", seconds(left)))
		}
		if me.Folded {
			s = st.faint.Render("You folded. ") + s
		}
		return s
	case t.InHand:
		return st.dim.Render("Dealing…")
	case m.snap.StartedBy != "" && !m.snap.NextHandAt.IsZero():
		return st.gold.Render(m.snap.StartedBy) + st.dim.Render(" started the game · first hand in ") +
			st.gold.Render(m.untilText(m.snap.NextHandAt))
	case !m.snap.NextHandAt.IsZero():
		return st.dim.Render("Next hand in ") + st.gold.Render(m.untilText(m.snap.NextHandAt))
	}
	return st.dim.Render("Waiting for more players to sit down.")
}

// showPrompt offers the winner of a no-showdown hand, or a player who
// folded, to show cards after the hand. Each key names its card, because a
// show cannot be undone.
func (m *model) showPrompt() string {
	left, right := m.showable()
	if !left && !right {
		return ""
	}
	me, _ := m.mySeat() // showable: the seat holds 2 cards
	st := m.st
	lead := st.win.Render("You win.")
	if !m.wonNoShowdown() {
		lead = st.dim.Render("You folded.")
	}
	s := lead + st.text.Render(" Show your cards? ")
	if left {
		s += m.btn("1", cardText(me.Cards[0])) + "  "
	}
	if right {
		s += m.btn("2", cardText(me.Cards[1])) + "  "
	}
	if left && right {
		s += m.btn("3", "both")
	}
	return strings.TrimRight(s, " ")
}

// showAtEndLine says which cards the viewer will show when the hand ends
// (Me.ShowAtEnd), and which key cancels the choice. A narrow line gets a
// shorter text.
func (m *model) showAtEndLine(w int) string {
	st := m.st
	c := m.snap.Me.ShowAtEnd
	me, _ := m.mySeat()
	what := "your cards"
	if len(me.Cards) == 2 {
		var cs []string
		for i, on := range c {
			if on {
				cs = append(cs, cardText(me.Cards[i]))
			}
		}
		what = strings.Join(cs, " ")
	}
	key := "3"
	switch c {
	case [2]bool{true, false}:
		key = "1"
	case [2]bool{false, true}:
		key = "2"
	}
	type form struct{ lead, mid, tail string }
	forms := []form{
		{"You will show " + what + " when the hand ends · press ", key, " again to cancel"},
		{"You will show " + what + " at the end · ", key, " again cancels"},
		{"Show " + what + " at the end · ", key, " cancels"},
	}
	f := forms[len(forms)-1]
	for _, try := range forms {
		if ansi.StringWidth(try.lead+try.mid+try.tail) <= w {
			f = try
			break
		}
	}
	return st.text.Render(f.lead) + st.gold.Render(f.mid) + st.text.Render(f.tail)
}

func (m *model) raiseLine(w int, full bool) (string, string) {
	st := m.st
	rm, ok := m.raiseMath()
	if !ok {
		return "", ""
	}
	verb := "Raise to"
	if rm.legal.Bet {
		verb = "Bet"
	}
	amt := m.raise.amount
	shown := chips(amt)
	if m.raise.typing {
		shown += "▏"
		if amt == 0 {
			shown = "▏"
		}
	}
	fieldFmt := " %11s "
	if !full {
		fieldFmt = " %7s "
	}
	field := st.inputBox.Bold(true).Render(fmt.Sprintf(fieldFmt, shown))
	var check string
	switch {
	case amt < rm.legal.MinTo:
		check = st.err.Render("min " + chips(rm.legal.MinTo))
	case amt > rm.legal.MaxTo:
		check = st.allIn.Render("ALL-IN") + st.err.Render(" (max "+chips(rm.legal.MaxTo)+")")
	case amt >= rm.legal.MaxTo || amt >= rm.legal.AllInTo:
		check = st.allIn.Render("ALL-IN")
	case full:
		check = st.dim.Render(fmt.Sprintf("+%s chips · %s–%s", chips(amt-rm.myBet), chips(rm.legal.MinTo), chips(rm.legal.MaxTo)))
	}
	line := st.gold.Render(verb) + " " + field + "  " + check
	if !full {
		return strings.TrimRight(line, " ") + "   " + m.presetText(rm), ""
	}
	send := "raise"
	if rm.legal.Bet {
		send = "bet"
	}
	return line + "   " + m.btn("Enter", send) + "  " + m.btn("Esc", "close"), m.raiseSlider(rm, w)
}

// raiseSlider draws the amount between MinTo and MaxTo, plus the presets.
func (m *model) raiseSlider(rm raiseMath, w int) string {
	st := m.st
	lo, hi := rm.legal.MinTo, rm.legal.MaxTo
	sw := 24
	pos := 0
	if hi > lo {
		pos = int(float64(rm.clamp(m.raise.amount)-lo) / float64(hi-lo) * float64(sw-1))
	}
	bar := st.gold.Render(strings.Repeat("━", pos)) + st.gold.Render("●") + st.faint.Render(strings.Repeat("━", sw-1-pos))
	slider := st.dim.Render(chips(lo)+" ") + bar + st.dim.Render(" "+chips(hi))
	return fit(slider+"    "+m.presetText(rm), w)
}

// presetText lists the amounts behind keys h, t, p and a.
func (m *model) presetText(rm raiseMath) string {
	st := m.st
	keys := []string{"H", "T", "P"}
	names := []string{"½ pot", "⅔ pot", "pot"}
	var pre []string
	for i, p := range presets {
		pre = append(pre, st.gold.Render(keys[i])+" "+st.dim.Render(names[i]+" "+chips(rm.potTo(p))))
	}
	pre = append(pre, st.gold.Render("A")+" "+st.dim.Render("all-in"))
	return strings.Join(pre, "  ")
}

// hint is one key hint.
type hint struct{ key, label string }

// rankedHint is a hint for the footer. Hints with a higher drop value
// leave the line first when it is too narrow; 0 never leaves.
type rankedHint struct {
	key, label string
	drop       int
}

// fitHints joins hints after prefix, and leaves out the most droppable
// hints until the line fits in w cells.
func (m *model) fitHints(prefix string, rs []rankedHint, w int) string {
	for {
		hs := make([]hint, len(rs))
		worst := -1
		for i, r := range rs {
			hs[i] = hint{r.key, r.label}
			if r.drop > 0 && (worst < 0 || r.drop >= rs[worst].drop) {
				worst = i
			}
		}
		line := prefix + m.hintText(hs)
		if ansi.StringWidth(line) <= w || worst < 0 {
			return fit(line, w)
		}
		rs = append(rs[:worst:worst], rs[worst+1:]...)
	}
}

func (m *model) hintsLine(w int) string {
	var hs []rankedHint
	prefix := ""
	switch m.mode {
	case modeRaise:
		bb := chips(max(m.snap.Table.BigBlind, 1))
		call := "call"
		if l := m.snap.Table.Legal; l != nil && l.Check {
			call = "check"
		}
		hs = []rankedHint{{"0-9", "type", 0}, {"←/→", "±" + bb, 3}, {"↑/↓", "pot steps", 4},
			{"C", call, 1}, {"F", "fold", 1}, {"Enter", "send", 0}, {"Esc", "close", 0}}
		if labels := m.extendLabels(); labels != nil {
			hs = append(hs, rankedHint{"E", labels[0], 5})
		}
	case modeChat:
		if m.snap.Table.Legal != nil {
			// The chat input replaces the action bar. Tell the viewer that
			// it is their turn, because compact mode shows no other text.
			hs = []rankedHint{{"Esc", "act", 0}, {"Enter", "send", 0}}
			prefix = m.turnCue() + m.st.faint.Render(" · ")
			break
		}
		hs = []rankedHint{{"Enter", "send", 0}, {"Esc", "cancel", 0}, {"Ctrl+U", "clear", 1}}
	case modeConfirm:
		hs = []rankedHint{{"Y", "yes", 0}, {"any key", "no", 0}}
	case modeProfile, modeRename, modeLink:
		hs = []rankedHint{{"Esc", "close", 0}}
	case modeSit:
		hs = []rankedHint{{"1/2", "pick", 0}, {"↑/↓", "move", 2}, {"Enter", "sit", 0}, {"Esc", "cancel", 0}}
	default:
		me, seated := m.mySeat()
		switch {
		case !seated:
			hs = append(hs, rankedHint{"S", "sit", 0})
		case me.Busted:
			hs = append(hs, rankedHint{"B", "buy back in", 0}, rankedHint{"L", "leave seat", 0})
		default:
			out := "sit out"
			if me.SittingOut {
				out = "back in"
			}
			hs = append(hs, rankedHint{"O", out, 0}, rankedHint{"L", "leave seat", 0})
		}
		if m.offerRabbit() {
			hs = append(hs, rankedHint{"H", m.rabbitHintLabel(), 0})
		}
		if m.canShow() {
			hs = append(hs, rankedHint{"1 2 3", "Show", 1})
		}
		if m.canStart() {
			hs = append([]rankedHint{{"G", "start", 0}}, hs...)
		}
		hs = append(hs, rankedHint{"D", "stats", 2}, rankedHint{"T", "chat", 3}, rankedHint{"Tab", "panel", 5},
			rankedHint{"M", "profile", 4}, rankedHint{"?", "help", 0}, rankedHint{"Q", "quit", 1})
	}
	return m.fitHints(prefix, hs, w)
}

// turnCue is a short "it is your turn" line: the price to call and the
// time left.
func (m *model) turnCue() string {
	st := m.st
	l := m.snap.Table.Legal
	s := st.gold.Render("YOUR TURN")
	switch {
	case l == nil:
		return s
	case l.Check:
		s += st.dim.Render(" · check")
	case l.Call:
		s += st.dim.Render(" · call " + chips(l.CallAmount))
	}
	if left, ok := m.turnLeft(); ok {
		s += st.dim.Render(fmt.Sprintf(" · %ds", seconds(left)))
	}
	return s
}

func (m *model) helpView(w, h int) string {
	dashHelp := "stats · fun · settle up (Tab, Enter)"
	if m.snap.Me.Host {
		dashHelp = "stats · fun · settle up · host (Tab, Enter)"
	}
	rows := []hint{
		{"F", "fold"},
		{"C / K", "check or call"},
		{"R / B", "raise or bet: opens the amount editor"},
		{"A A", "all-in (press twice)"},
	}
	// While the table waits to start, no clock runs: G takes the place of
	// E, so that the list still fits in 24 lines.
	switch c := m.snap.Config; {
	case m.canStart():
		rows = append(rows, hint{"G", "start the game, when everyone is in"})
	case m.snap.Waiting:
		// A spectator cannot start, and no clock runs: no G and no E.
	case c.Extensions > 0 && c.ExtensionTime > 0:
		rows = append(rows, hint{"E", fmt.Sprintf("add %s to your clock, on your turn (max %d)", durationText(c.ExtensionTime), c.Extensions)})
	}
	rows = append(rows, []hint{
		{"", ""},
		{"0-9 ⌫", "type the amount"},
		{"H T P A", "½ pot · ⅔ pot · pot · all-in"},
		{"← → ↑ ↓", "one big blind · next pot step"},
		{"Enter / Esc", "send / close the editor"},
		{"", ""},
		{"S / L", "sit down: 1 buy-in or 2 whole bank / leave seat"},
		{"O / B", "sit out or play again / buy back in (bank first)"},
		{"H", "rabbit hunt, after everyone folded"},
		{"1 2 3", "show left / right / both cards, after a win or a fold"},
		{"", ""},
		{"D", dashHelp},
		{"M N I P", "profile · alerts on/off · hand rankings · screenshot"},
		{"T / Tab", "chat (or /) · switch log / chat / players"},
		{"Cmd+⌫/^H", "panic: hide the game on every screen"},
		{"Q / Ctrl+C", "quit (? or Esc closes this help)"},
	}...)
	box := m.helpBox(rows, helpRoomy)
	for _, l := range []helpLayout{helpCompact, helpOneLine} {
		if height(box) <= h {
			break
		}
		box = m.helpBox(rows, l)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, clip(box, w, h))
}

// helpLayout says how much space the help box uses around the key list.
type helpLayout int

const (
	helpRoomy   helpLayout = iota // blank lines and vertical padding
	helpCompact                   // no blank lines, no vertical padding
	helpOneLine                   // compact, with the title and the logo on one line
)

// helpBox draws the key list. The compact layouts leave out the blank
// lines and the vertical padding, and at last put the title and the logo
// on one line, so that the list fits in 24 terminal lines.
func (m *model) helpBox(rows []hint, layout helpLayout) string {
	st := m.st
	roomy := layout == helpRoomy
	var lines []string
	for _, r := range rows {
		switch {
		case r.key != "":
			lines = append(lines, st.gold.Render(padRight(r.key, 12))+st.text.Render(r.label))
		case roomy:
			lines = append(lines, "")
		}
	}
	w := lipgloss.Width(strings.Join(lines, "\n"))
	logo := lipgloss.PlaceHorizontal(w, lipgloss.Center, m.logo())
	title := logo + "\n" + st.bold.Render("Keys") + "\n"
	if gap := w - lipgloss.Width("Keys"+m.logo()); layout == helpOneLine && gap >= 2 {
		title = st.bold.Render("Keys") + strings.Repeat(" ", gap) + m.logo() + "\n"
	}
	pad := 0
	if roomy {
		title = logo + "\n\n" + st.bold.Render("Keys") + "\n\n"
		pad = 1
	}
	return st.felt.Padding(pad, 3).Render(title + strings.Join(lines, "\n"))
}
