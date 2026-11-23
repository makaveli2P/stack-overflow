package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Dashboard tabs, in the order the tab line shows them.
const (
	dashStats = iota
	dashFun
	dashSettle
	dashHost // only when Me.Host
)

// dashTabNames are the tab titles, by tab.
var dashTabNames = [...]string{dashStats: "Stats", dashFun: "Fun", dashSettle: "Settle up", dashHost: "Host"}

// dashTabList returns the tabs the viewer sees. Only the host sees Host.
func (m *model) dashTabList() []int {
	if m.snap.Me.Host {
		return []int{dashStats, dashFun, dashSettle, dashHost}
	}
	return []int{dashStats, dashFun, dashSettle}
}

// rowTab reports whether a tab is a table of players with a row cursor.
func rowTab(tab int) bool { return tab != dashSettle }

// nextTab moves step tabs along the tab line and wraps at the ends.
func (m *model) nextTab(step int) {
	tabs := m.dashTabList()
	at := 0
	for i, t := range tabs {
		if t == m.dash.tab {
			at = i
		}
	}
	m.dash.tab = tabs[(at+step+len(tabs))%len(tabs)]
	m.dash.scroll = 0
}

// keepHostTab closes the Host tab when the viewer is not the host.
func (m *model) keepHostTab() {
	if m.dash.tab == dashHost && !m.snap.Me.Host {
		m.dash.tab, m.dash.detail = dashStats, false
	}
}

// dashState is the state of the D dashboard.
type dashState struct {
	tab     int
	row     int    // cursor in the stats table
	sel     string // name of the player under the cursor, to follow across re-sorts
	top     int    // first stats row on screen
	detail  bool   // the detail card of the cursor row is open
	scroll  int    // first line of the settle tab on screen
	confirm bool   // the admin typed M: ask for "yes"
	yes     textInput
	err     string
	busy    bool // the MarkSettled call runs
}

// clamp keeps the cursor inside a table of n rows.
func (d *dashState) clamp(n int) {
	d.row = min(d.row, max(n-1, 0))
	if n == 0 {
		d.detail = false
	}
}

// follow moves the cursor to the row of the selected player in new stats.
// The server sorts the stats by period net, so the rows change order
// after a hand. When the selected player is gone, the detail card closes.
func (d *dashState) follow(stats []server.PlayerStats) {
	if d.sel != "" {
		found := false
		for i, p := range stats {
			if p.Name == d.sel {
				d.row, found = i, true
				break
			}
		}
		if !found {
			d.detail = false
		}
	}
	d.clamp(len(stats))
	d.pick(stats)
}

// pick stores the name of the player under the cursor.
func (d *dashState) pick(stats []server.PlayerStats) {
	d.sel = ""
	if d.row >= 0 && d.row < len(stats) {
		d.sel = stats[d.row].Name
	}
}

func (m *model) openDash() {
	m.mode = modeDash
	d := &m.dash
	d.detail, d.confirm, d.scroll, d.err = false, false, 0, ""
	for i, p := range m.snap.Stats {
		if p.Name == m.snap.Me.Name {
			d.row = i
		}
	}
	d.clamp(len(m.snap.Stats))
	d.pick(m.snap.Stats)
}

func (m *model) dashKey(k tea.KeyMsg) tea.Cmd {
	d := &m.dash
	if d.confirm {
		return m.settleConfirmKey(k)
	}
	key := k.String()
	if len([]rune(key)) == 1 {
		key = strings.ToLower(key)
	}
	n := len(m.snap.Stats)
	switch key {
	case "d", "q":
		m.mode = modeTable
	case "esc":
		if d.detail {
			d.detail = false
		} else {
			m.mode = modeTable
		}
	case "tab", "right":
		if !d.detail {
			m.nextTab(1)
		}
	case "shift+tab", "left":
		if !d.detail {
			m.nextTab(-1)
		}
	case "up":
		if rowTab(d.tab) {
			d.row = max(d.row-1, 0)
		} else {
			d.scroll = max(d.scroll-1, 0)
		}
	case "down":
		if rowTab(d.tab) {
			d.row = min(d.row+1, max(n-1, 0))
		} else {
			d.scroll++
		}
	case "enter":
		if rowTab(d.tab) && n > 0 {
			d.detail = !d.detail
		}
	case "m":
		switch {
		case d.tab != dashSettle:
		case !m.snap.Me.Admin:
			m.flash(server.ErrNotAdmin.Error()+".", true)
		default:
			d.confirm, d.err = true, ""
			d.yes = textInput{limit: 8}
		}
	}
	d.pick(m.snap.Stats)
	return nil
}

func (m *model) settleConfirmKey(k tea.KeyMsg) tea.Cmd {
	d := &m.dash
	if d.busy {
		// Only Esc works while the call runs: it hides the
		// confirmation, and callDone shows the result.
		if k.String() == "esc" {
			d.confirm, d.err = false, ""
		}
		return nil
	}
	switch k.String() {
	case "esc":
		d.confirm, d.err = false, ""
	case "enter":
		if !strings.EqualFold(strings.TrimSpace(d.yes.String()), "yes") {
			d.err = "Type yes to confirm, or press Esc."
			return nil
		}
		d.busy, d.err = true, ""
		s := m.sess
		ok := fmt.Sprintf("Period %d is settled. Every bank starts at zero.", m.snap.Settle.Period)
		return callOK(callSettle, ok, s.MarkSettled)
	default:
		if d.yes.key(k) {
			d.err = ""
		}
	}
	return nil
}

// ---- drawing ----

func (m *model) dashScreen(w, h int) string {
	m.keepHostTab()
	bodyH := max(h-2, 1)
	var body string
	inner := bodyH - 1
	detail := m.dash.detail && len(m.snap.Stats) > 0
	switch {
	case m.dash.tab == dashSettle:
		body = m.settleView(w, inner)
	case detail && m.dash.tab == dashFun:
		body = m.placeBox(m.funCard(m.snap.Stats[m.dash.row], min(w, 72), inner), w, inner)
	case detail && m.dash.tab == dashHost:
		body = m.placeBox(m.hostCard(m.snap.Stats[m.dash.row], min(w, 76), inner), w, inner)
	case detail:
		body = m.placeBox(m.detailCard(m.snap.Stats[m.dash.row], min(w, 68), inner), w, inner)
	case m.dash.tab == dashFun:
		body = m.funView(w, inner)
	case m.dash.tab == dashHost:
		body = m.hostView(w, inner)
	default:
		body = m.statsView(w, inner)
	}
	return m.header(w) + "\n" + m.dashTabs(w) + "\n" + fixHeight(body, inner) + "\n" + m.dashFooter(w)
}

func (m *model) dashTabs(w int) string {
	st := m.st
	tabs := m.dashTabList()
	parts := make([]string, len(tabs))
	for i, t := range tabs {
		if t == m.dash.tab {
			parts[i] = st.tabOn.Render(dashTabNames[t])
		} else {
			parts[i] = st.tabOff.Render(dashTabNames[t])
		}
	}
	left := st.faint.Render("── ") + strings.Join(parts, st.faint.Render("  ")) + " "
	right := ""
	if s := m.snap.Settle; s.Period > 0 {
		text := fmt.Sprintf(" Period %d", s.Period)
		if !s.Since.IsZero() {
			text += " · since " + s.Since.Local().Format("2 Jan 15:04")
		}
		right = st.dim.Render(text+" ") + st.faint.Render("──")
	}
	fill := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if fill < 2 {
		right, fill = "", w-ansi.StringWidth(left)
	}
	return fit(left+st.faint.Render(strings.Repeat("─", max(fill, 0)))+right, w)
}

func (m *model) dashFooter(w int) string {
	if m.snap.Table.Legal != nil {
		return fit(m.turnCue()+m.st.faint.Render(" · ")+m.hintText([]hint{{"D", "back to the table"}}), w)
	}
	if m.notice.text != "" {
		return m.noticeLine(w)
	}
	d := m.dash
	var hs []hint
	switch {
	case d.confirm:
		hs = []hint{{"Enter", "confirm"}, {"Esc", "cancel"}}
	case d.tab == dashSettle:
		hs = []hint{{"Tab", "next tab"}, {"↑/↓", "scroll"}}
		if m.snap.Me.Admin {
			hs = append(hs, hint{"M", "mark settled"})
		}
		hs = append(hs, hint{"D/Esc", "close"})
	case d.detail:
		hs = []hint{{"↑/↓", "next player"}, {"Esc", "back"}, {"D", "close"}}
	default:
		hs = []hint{{"Tab", "next tab"}, {"↑/↓", "select"}, {"Enter", "details"}, {"D/Esc", "close"}}
	}
	return fit(m.hintText(hs), w)
}

// pct formats n/d as a whole percentage, or "—" when d is zero.
func pct(n, d int) string {
	if d <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", (200*n+d)/(2*d))
}

// netStyle colours a result: green above zero, red below.
func (m *model) netStyle(n int64) lipgloss.Style {
	switch {
	case n > 0:
		return m.st.win
	case n < 0:
		return m.st.err
	}
	return m.st.dim
}

// statCol is one column of the stats table.
type statCol struct {
	head string
	w    int // width in cells
	grow int // extra cells the column may take
	left bool
	cell func(p server.PlayerStats, w int) string
}

func (m *model) statCols() []statCol {
	st := m.st
	cur, cpu := m.currency(), m.perUnit()
	num := func(n int) string { return st.text.Render(fmt.Sprint(n)) }
	cols := []statCol{
		m.playerCol(),
		{head: "Net", w: 8, cell: func(p server.PlayerStats, _ int) string {
			return m.netStyle(p.PeriodNet).Render(signed(p.PeriodNet))
		}},
		{head: cur, w: 10, cell: func(p server.PlayerStats, _ int) string {
			a := m.periodMoney(p, cpu)
			return m.netStyle(int64(a)).Render(signedMoney(a, cur))
		}},
		{head: "Hands", w: 5, cell: func(p server.PlayerStats, _ int) string { return num(p.Hands) }},
		{head: "Won", w: 4, cell: func(p server.PlayerStats, _ int) string { return num(p.HandsWon) }},
		{head: "VPIP", w: 4, cell: func(p server.PlayerStats, _ int) string { return st.text.Render(pct(p.VPIP, p.Hands)) }},
		{head: "PFR", w: 4, cell: func(p server.PlayerStats, _ int) string { return st.text.Render(pct(p.PFR, p.Hands)) }},
		{head: "WTSD", w: 4, cell: func(p server.PlayerStats, _ int) string { return st.text.Render(pct(p.Showdowns, p.SawFlop)) }},
		{head: "W$SD", w: 4, cell: func(p server.PlayerStats, _ int) string {
			return st.text.Render(pct(p.ShowdownsWon, p.Showdowns))
		}},
		{head: "Biggest", w: 7, cell: func(p server.PlayerStats, _ int) string {
			if p.BiggestPot == 0 {
				return st.faint.Render("—")
			}
			return st.text.Render(chips(p.BiggestPot))
		}},
		{head: "Best hand", w: 10, grow: 20, left: true, cell: func(p server.PlayerStats, w int) string {
			if p.BestHand == "" {
				return st.faint.Render("—")
			}
			return st.text.Render(fitHand(p.BestHand, w))
		}},
		{head: "All-ins", w: 7, cell: func(p server.PlayerStats, _ int) string { return num(p.AllIns) }},
		{head: "Buy-ins", w: 7, cell: func(p server.PlayerStats, _ int) string { return num(p.BuyIns) }},
		{head: "Lifetime", w: 9, cell: func(p server.PlayerStats, _ int) string {
			return m.netStyle(p.LifetimeNet).Render(signed(p.LifetimeNet))
		}},
	}
	if cur == "" {
		cols = append(cols[:2], cols[3:]...)
	}
	return cols
}

// pickCols keeps the columns that fit in w, in priority order, then gives
// the spare cells to the columns that can grow.
func pickCols(all []statCol, w int) []statCol {
	var out []statCol
	used := 0
	for i, c := range all {
		need := c.w
		if i > 0 {
			need += 2
		}
		if used+need > w {
			break
		}
		used += need
		out = append(out, c)
	}
	spare := w - used
	for i := range out {
		add := min(spare, out[i].grow)
		out[i].w += add
		spare -= add
	}
	return out
}

func padLeft(s string, w int) string {
	s = fit(s, w)
	return strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + s
}

// playerCol is the first column of every player table: online dot and
// name, with a mark on the viewer's row.
func (m *model) playerCol() statCol {
	st := m.st
	return statCol{head: "Player", w: 4 + maxNameRunes, left: true, cell: func(p server.PlayerStats, w int) string {
		dot := st.faint.Render("○")
		if p.Online {
			dot = st.win.Render("●")
		}
		if p.Name == m.snap.Me.Name {
			return dot + " " + st.me.Render(fit(p.Name, w-4)) + st.me.Render(" ◂")
		}
		return dot + " " + st.text.Render(fit(p.Name, w-2))
	}}
}

// onlineLegend explains the marks of the player column.
func (m *model) onlineLegend() string {
	st := m.st
	return st.win.Render("●") + st.faint.Render(" online  ○ offline  ") + st.me.Render("◂ you")
}

func (m *model) statsView(w, h int) string {
	legend := m.onlineLegend() + m.st.faint.Render("  · Net = this period, the rest = all time")
	return m.rowsView(m.statCols(), w, h, []string{legend})
}

// rowsView draws a table of players in w x h with the row cursor. foot
// holds the lines under the table; the last one is the legend, which
// also tells which rows are on screen when they do not all fit.
func (m *model) rowsView(all []statCol, w, h int, foot []string) string {
	st := m.st
	stats := m.snap.Stats
	if len(stats) == 0 {
		return "\n" + st.dim.Render("  No hands played yet. The stats start with the first hand.")
	}
	cols := pickCols(all, w-4)
	line := func(cells []string) string {
		var b strings.Builder
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			if cols[i].left {
				b.WriteString(padRight(c, cols[i].w))
			} else {
				b.WriteString(padLeft(c, cols[i].w))
			}
		}
		return b.String()
	}
	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = st.dim.Render(c.head)
	}
	heads[0] = st.dim.Render("  " + cols[0].head)
	out := []string{"  " + line(heads)}

	rowsH := max(h-2-len(foot), 1)
	d := &m.dash
	d.top = min(max(d.top, d.row-rowsH+1), d.row)
	d.top = max(min(d.top, len(stats)-rowsH), 0)
	for i := d.top; i < min(d.top+rowsH, len(stats)); i++ {
		p := stats[i]
		cells := make([]string, len(cols))
		for j, c := range cols {
			cells[j] = c.cell(p, c.w)
		}
		mark := "  "
		if i == d.row {
			mark = st.cursor.Render("▸ ")
		}
		out = append(out, mark+line(cells))
	}
	out = append(out, "")
	for i, l := range foot {
		if i == len(foot)-1 && len(stats) > rowsH {
			l = st.dim.Render(fmt.Sprintf("%d–%d of %d · ", d.top+1, min(d.top+rowsH, len(stats)), len(stats))) + l
		}
		out = append(out, "  "+l)
	}
	return clip(strings.Join(out, "\n"), w, h)
}

// periodMoney returns the money value of a player's period net. It is the
// player's settle balance, which takes the rounding remainder, so that the
// stats agree with the Settle tab and the transfers. A player with no
// balance gets the plain rounded value.
func (m *model) periodMoney(p server.PlayerStats, cpu int64) server.Money {
	for _, b := range m.snap.Settle.Balances {
		if b.Name == p.Name {
			return b.Money
		}
	}
	return toMoney(p.PeriodNet, cpu)
}

// detailCard draws every number of one player, with the best hand.
func (m *model) detailCard(p server.PlayerStats, bw, h int) string {
	st := m.st
	cur, cpu := m.currency(), m.perUnit()
	kv := func(k, v string) string { return st.dim.Render(padRight(k, 13)) + v }
	sep := st.faint.Render(" · ")
	n := func(v int) string { return st.text.Render(fmt.Sprint(v)) }

	net := m.netStyle(p.PeriodNet).Render(signed(p.PeriodNet)) + st.dim.Render(" chips")
	if cur != "" {
		a := m.periodMoney(p, cpu)
		net += sep + m.netStyle(int64(a)).Render(signedMoney(a, cur))
	}
	biggest := st.faint.Render("—")
	if p.BiggestPot > 0 {
		biggest = st.text.Render(chips(p.BiggestPot))
	}
	lines := []string{
		"",
		kv("Net", net+st.dim.Render(" this period")),
		kv("Lifetime net", m.netStyle(p.LifetimeNet).Render(signed(p.LifetimeNet))+st.dim.Render(" chips")),
		kv("Hands", n(p.Hands)+st.dim.Render(" dealt")+sep+n(p.HandsWon)+st.dim.Render(" won")+sep+n(p.PeriodHands)+st.dim.Render(" this period")),
		kv("Buy-ins", n(p.PeriodBuyIns)+st.dim.Render(" this period")+sep+n(p.BuyIns)+st.dim.Render(" in all")),
		kv("Biggest pot", biggest+sep+st.dim.Render("all-ins ")+n(p.AllIns)),
		"",
	}
	stat := func(label string, a, b int, text string) string {
		frac := ""
		if b > 0 {
			frac = fmt.Sprintf("%d/%d", a, b)
		}
		return st.gold.Render(padRight(label, 6)) + st.bold.Render(padLeft(pct(a, b), 4)) + " " +
			st.faint.Render(padRight(frac, 9)) + st.dim.Render(text)
	}
	lines = append(lines,
		stat("VPIP", p.VPIP, p.Hands, "put chips in preflop by choice"),
		stat("PFR", p.PFR, p.Hands, "bet or raised preflop"),
		stat("WTSD", p.Showdowns, p.SawFlop, "went to showdown after the flop"),
		stat("W$SD", p.ShowdownsWon, p.Showdowns, "won chips at showdown"),
		"",
	)
	if p.BestHand == "" {
		lines = append(lines, kv("Best hand", st.faint.Render("none shown at a showdown yet")))
	} else {
		lines = append(lines, kv("Best hand", st.bold.Render(p.BestHand)))
		if len(p.BestCards) > 0 {
			boxes := make([]string, len(p.BestCards))
			for i, c := range p.BestCards {
				boxes[i] = st.trophyBox(c)
			}
			for _, l := range strings.Split(boxRow(boxes, 1), "\n") {
				lines = append(lines, strings.Repeat(" ", 13)+l)
			}
		}
	}
	title := strings.ToUpper(p.Name)
	if p.Online {
		title += " · online"
	}
	return m.titledBox(title, squeeze(lines, h-2), bw, colFelt)
}

// ---- settle up ----

func (m *model) settleView(w, h int) string {
	st := m.st
	foot := m.settleFoot(w)
	room := max(h-len(foot)-1, 1)

	var lines []string
	bal, xfer := m.balanceLines(), m.transferLines()
	if w >= 96 {
		lw := 50
		left := append([]string{""}, bal...)
		right := append([]string{""}, xfer...)
		for i := 0; i < max(len(left), len(right)); i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, "  "+padRight(l, lw)+"  "+r)
		}
	} else {
		lines = append(lines, "")
		for _, l := range append(append(bal, ""), xfer...) {
			lines = append(lines, "  "+l)
		}
	}
	d := &m.dash
	d.scroll = max(min(d.scroll, len(lines)-room), 0)
	view := lines[d.scroll:min(d.scroll+room, len(lines))]
	for len(view) < room {
		view = append(view, "")
	}
	if d.scroll > 0 {
		view[0] = "  " + st.faint.Render("↑ more")
	}
	if d.scroll+room < len(lines) && room > 1 {
		view[room-1] = "  " + st.faint.Render("↓ more")
	}
	return clip(strings.Join(append(append(view, ""), foot...), "\n"), w, h)
}

func (m *model) balanceLines() []string {
	st := m.st
	s := m.snap.Settle
	cur := s.Currency
	rate := fmt.Sprintf("%d chips = %s", m.perUnit(), money(100, cur))
	if m.perUnit() == 1 {
		rate = "1 chip = " + money(100, cur)
	}
	out := []string{st.logHdr.Render("BALANCES") + st.dim.Render("  "+rate)}
	if len(s.Balances) == 0 {
		return append(out, st.dim.Render("Everyone is even. Nothing to settle."))
	}
	for _, b := range s.Balances {
		ns := st.text
		if b.Name == m.snap.Me.Name {
			ns = st.me
		}
		verb := "receives"
		if b.Money < 0 {
			verb = "pays"
		}
		ms := m.netStyle(int64(b.Money))
		out = append(out, ns.Render(padRight(b.Name, maxNameRunes))+" "+
			ms.Render(padLeft(signedMoney(b.Money, cur), 10))+"  "+
			st.dim.Render(padLeft(signed(b.Chips), 7)+" chips  ")+ms.Render(verb))
	}
	return out
}

func (m *model) transferLines() []string {
	st := m.st
	s := m.snap.Settle
	name := func(n string) string {
		if n == m.snap.Me.Name {
			return st.me.Render(n)
		}
		return st.bold.Render(n)
	}
	pay := func(t server.Transfer, verb, cur string) string {
		return name(t.From) + st.dim.Render(" "+verb+" ") + name(t.To) + " " + st.gold.Render(money(t.Amount, cur))
	}
	out := []string{st.logHdr.Render("TRANSFERS")}
	if len(s.Transfers) == 0 {
		out = append(out, st.dim.Render("No payments needed."))
	}
	for _, t := range s.Transfers {
		out = append(out, pay(t, "pays", s.Currency))
	}
	out = append(out, "", st.logHdr.Render("HISTORY"))
	if len(s.History) == 0 {
		return append(out, st.dim.Render("No period is settled yet."))
	}
	for i, p := range s.History {
		if i > 0 {
			out = append(out, "")
		}
		head := fmt.Sprintf("Period %d", p.Period)
		if !p.Since.IsZero() {
			head += " · " + p.Since.Local().Format("2 Jan")
		}
		if !p.SettledAt.IsZero() {
			head += " – " + p.SettledAt.Local().Format("2 Jan 15:04")
		}
		out = append(out, st.dim.Render(head))
		if len(p.Transfers) == 0 {
			out = append(out, st.faint.Render("  nothing to pay"))
		}
		cur := p.Currency
		if cur == "" {
			cur = s.Currency
		}
		for _, t := range p.Transfers {
			out = append(out, "  "+pay(t, "paid", cur))
		}
	}
	return out
}

// settleFoot is the fixed part under the settle tab: the admin button or
// the typed confirmation.
func (m *model) settleFoot(w int) []string {
	st := m.st
	d := m.dash
	switch {
	case d.confirm:
		ask := fmt.Sprintf("Settle period %d? This stands everyone up and sets every bank to zero.", m.snap.Settle.Period)
		line := st.gold.Render("Type yes: ") + m.field(d.yes, 8, !d.busy)
		switch {
		case d.busy:
			line += st.dim.Render("  settling…")
		case d.err != "":
			line += "  " + st.err.Render("✗ "+d.err)
		}
		return []string{"  " + st.err.Render(fit(ask, w-2)), "  " + line}
	case m.snap.Me.Admin:
		return []string{"  " + m.btn("M", "Mark everything settled") + st.dim.Render("  after the payments are made")}
	}
	return []string{"  " + st.faint.Render("Only the host can mark the period settled.")}
}
