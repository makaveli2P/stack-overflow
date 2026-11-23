package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Batch 3: the Fun tab (everyone) and the Host tab (only Me.Host).

// minRateSeconds is the seated time under which the win rate per hour
// shows "—": a few lucky minutes give silly hourly numbers.
const minRateSeconds = 10 * 60

// ---- formatting ----

// pctNum is the whole percentage of n/d, rounded half up. d must be > 0.
func pctNum(n, d int) int { return (200*n + d) / (2 * d) }

// equityPct formats an equity (0..1) as a whole percentage: 0.912 -> "91%".
func equityPct(e float64) string {
	return fmt.Sprintf("%d%%", int(math.Round(e*100)))
}

// bb100 formats big blinds won per 100 hands with one decimal and a sign:
// "+12.5", "-3.0", "0.0". It is "—" with no hands.
func bb100(netBB float64, hands int) string {
	if hands <= 0 {
		return "—"
	}
	v := math.Round(1000*netBB/float64(hands)) / 10
	if v == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%+.1f", v)
}

// aggression formats the aggression factor, bets and raises per call:
// "2.5"; "∞" when there are bets but no calls; "—" when both are 0.
func aggression(betsRaises, calls int) string {
	switch {
	case calls > 0:
		return fmt.Sprintf("%.1f", float64(betsRaises)/float64(calls))
	case betsRaises > 0:
		return "∞"
	}
	return "—"
}

// perHour formats the net since batch 3 (HostStats.NetChips) as money
// per hour seated. exact keeps
// the hundredths ("+₹45.25"); else the amount is rounded to whole units
// ("+₹45"). With no currency it is chips per hour. ok is false (and the
// text "—") under minRateSeconds of seated time.
func perHour(net int64, cpu int64, secs int64, cur string, exact bool) (text string, sign int, ok bool) {
	if secs < minRateSeconds {
		return "—", 0, false
	}
	cpu = max(cpu, 1)
	if cur == "" {
		v := int64(math.Round(float64(net) * 3600 / float64(secs)))
		return signed(v), signOf(v), true
	}
	hundredths := float64(net) * 100 / float64(cpu) * 3600 / float64(secs)
	a := server.Money(math.Round(hundredths))
	if !exact {
		a = server.Money(math.Round(hundredths/100) * 100)
	}
	return signedMoney(a, cur), signOf(int64(a)), true
}

func signOf(n int64) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// duration formats seated seconds: "45m", "3h 20m".
func duration(secs int64) string {
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh %dm", mins/60, mins%60)
}

// handCodeName names a starting-hand code: "AKs" -> "Ace-King suited",
// "QJo" -> "Queen-Jack offsuit", "77" -> "Pocket Sevens". It returns ""
// for a code it does not know.
func handCodeName(code string) string {
	rank := func(b byte) cards.Rank {
		switch b {
		case 'A':
			return cards.Ace
		case 'K':
			return cards.King
		case 'Q':
			return cards.Queen
		case 'J':
			return cards.Jack
		case 'T':
			return cards.Ten
		}
		if b >= '2' && b <= '9' {
			return cards.Rank(b - '0')
		}
		return 0
	}
	if len(code) < 2 || len(code) > 3 {
		return ""
	}
	a, b := rank(code[0]), rank(code[1])
	if a == 0 || b == 0 {
		return ""
	}
	if len(code) == 2 {
		if a != b {
			return ""
		}
		return "Pocket " + a.Plural()
	}
	switch code[2] {
	case 's':
		return a.Name() + "-" + b.Name() + " suited"
	case 'o':
		return a.Name() + "-" + b.Name() + " offsuit"
	}
	return ""
}

// myStats returns the viewer's own row.
func (m *model) myStats() (server.PlayerStats, bool) {
	for _, p := range m.snap.Stats {
		if p.Name == m.snap.Me.Name {
			return p, true
		}
	}
	return server.PlayerStats{}, false
}

// ---- Fun tab ----

func (m *model) funCols() []statCol {
	st := m.st
	none := func() string { return st.faint.Render("—") }
	return []statCol{
		m.playerCol(),
		{head: "Bad beats", w: 9, cell: func(p server.PlayerStats, _ int) string {
			if p.BadBeats == 0 {
				return none()
			}
			return st.text.Render(fmt.Sprint(p.BadBeats)) + st.dim.Render(" ("+equityPct(p.WorstBeat)+")")
		}},
		{head: "Luckiest river", w: 14, cell: func(p server.PlayerStats, _ int) string {
			if p.LuckiestRiver <= 0 {
				return none()
			}
			s := st.win.Render("+" + chips(p.LuckiestRiver))
			if p.RiverCard.Rank >= cards.Two {
				s += st.dim.Render(" on ") + st.chip(p.RiverCard, true)
			}
			return s
		}},
		{head: "First win", w: 9, cell: func(p server.PlayerStats, _ int) string {
			if p.FirstWinAfter <= 0 {
				return st.faint.Render("not yet")
			}
			return st.text.Render(fmt.Sprintf("hand %d", p.FirstWinAfter))
		}},
		{head: "Nemesis", w: 14, grow: 6, left: true, cell: func(p server.PlayerStats, w int) string {
			if p.Nemesis == "" || p.NemesisChips <= 0 {
				return none()
			}
			c := chips(p.NemesisChips)
			return st.text.Render(fit(p.Nemesis, max(w-len(c)-1, 3))) + " " + st.err.Render(c)
		}},
	}
}

func (m *model) funView(w, h int) string {
	st := m.st
	legend := m.onlineLegend() + st.faint.Render("  · Enter explains each number")
	return m.rowsView(m.funCols(), w, h, []string{m.favouriteLine(w - 2), legend})
}

// favouriteLine is the "Your favourite hand" line under the Fun table. It
// reads only the viewer's own row: the server fills FavouriteHand only
// there, and the TUI never shows another row's value.
func (m *model) favouriteLine(w int) string {
	st := m.st
	head := st.dim.Render("Your favourite hand  ")
	p, ok := m.myStats()
	if !ok || p.FavouriteHand == "" {
		return head + st.faint.Render("none yet: play a hand by choice first")
	}
	s := head + st.gold.Render(p.FavouriteHand)
	if name := handCodeName(p.FavouriteHand); name != "" {
		s += st.text.Render(" " + name)
	}
	times := "times"
	if p.FavouriteCount == 1 {
		times = "time"
	}
	s += st.dim.Render(fmt.Sprintf(" · played %d %s", p.FavouriteCount, times))
	if tail := st.faint.Render(" · only you see this"); ansi.StringWidth(s+tail) <= w {
		s += tail
	}
	return s
}

// funCard explains every fun number of one player.
func (m *model) funCard(p server.PlayerStats, bw, h int) string {
	st := m.st
	pad := strings.Repeat(" ", 16)
	kv := func(k, v string) string { return st.gold.Render(padRight(k, 16)) + v }
	none := func(text string) string { return st.faint.Render(text) }
	name := p.Name
	// The card is never wider than 72 cells. An explanation that does not
	// fit goes on to more lines, when the card has the height for them;
	// else it gets cut.
	textW := max(bw-4-16, 1)
	build := func(wrap bool) []string {
		why := func(text string) string {
			texts := []string{text}
			if wrap {
				texts = wrapLines(text, textW, 0)
			}
			for i, t := range texts {
				texts[i] = pad + st.dim.Render(t)
			}
			return strings.Join(texts, "\n")
		}

		lines := []string{""}
		if p.BadBeats == 0 {
			lines = append(lines, kv("Bad beats", st.bold.Render("0")))
		} else {
			lines = append(lines, kv("Bad beats", st.bold.Render(fmt.Sprint(p.BadBeats))+
				st.dim.Render(" · worst at ")+st.bold.Render(equityPct(p.WorstBeat))))
			if len(p.WorstBeatWith) > 0 {
				lines = append(lines, pad+st.dim.Render("with ")+st.chipRow(p.WorstBeatWith, true, nil))
			}
		}
		lines = append(lines, why("lost or split a main pot at 70% or more to win it"), "")

		if p.LuckiestRiver <= 0 {
			lines = append(lines, kv("Luckiest river", none("none yet")))
		} else {
			v := st.win.Render("+" + chips(p.LuckiestRiver))
			if p.RiverCard.Rank >= cards.Two {
				v += st.dim.Render(" on the ") + st.chip(p.RiverCard, true)
			}
			lines = append(lines, kv("Luckiest river", v))
		}
		lines = append(lines, why("biggest pot won on the river from behind on the turn"), "")

		if p.FirstWinAfter <= 0 {
			lines = append(lines, kv("First win", none("no pot won yet")))
		} else {
			lines = append(lines, kv("First win", st.bold.Render(fmt.Sprintf("hand %d", p.FirstWinAfter))))
		}
		lines = append(lines, why("hands dealt in up to the first pot won"), "")

		if p.Nemesis == "" || p.NemesisChips <= 0 {
			lines = append(lines, kv("Nemesis", none("none yet")))
			lines = append(lines, why("who won the most net chips from "+name))
		} else {
			lines = append(lines, kv("Nemesis", st.bold.Render(p.Nemesis)+st.dim.Render(" · ")+st.err.Render(chips(p.NemesisChips))+st.dim.Render(" chips")))
			lines = append(lines, why(fmt.Sprintf("%s won %s net chips from %s", p.Nemesis, chips(p.NemesisChips), name)))
		}

		// The favourite hand is private: only on the viewer's own card, and
		// only from the viewer's own row.
		if p.Name == m.snap.Me.Name {
			lines = append(lines, "")
			if p.FavouriteHand == "" {
				lines = append(lines, kv("Favourite hand", none("none yet")))
			} else {
				v := st.bold.Render(p.FavouriteHand)
				if n := handCodeName(p.FavouriteHand); n != "" {
					v += st.text.Render(" " + n)
				}
				lines = append(lines, kv("Favourite hand", v+st.dim.Render(fmt.Sprintf(" · %d×", p.FavouriteCount))))
			}
			lines = append(lines, why("played most often by choice · only you see this"))
		}
		return squeeze(strings.Split(strings.Join(lines, "\n"), "\n"), h-2)
	}
	lines := build(true)
	if len(lines) > h-2 {
		lines = build(false)
	}
	return m.titledBox(strings.ToUpper(name)+" · FUN", lines, bw, colFelt)
}

// ---- Host tab ----

// hostCols are the Host tab columns, by priority for narrow screens.
func (m *model) hostCols() []statCol {
	st := m.st
	cur, cpu := m.currency(), m.perUnit()
	dash := st.faint.Render("—")
	// hs wraps a cell that needs the host stats: "—" when they are missing.
	hs := func(f func(p server.PlayerStats, h *server.HostStats) string) func(server.PlayerStats, int) string {
		return func(p server.PlayerStats, _ int) string {
			if p.Host == nil {
				return dash
			}
			return f(p, p.Host)
		}
	}
	ratio := func(n, d int) string {
		if d <= 0 {
			return dash
		}
		return st.text.Render(pct(n, d))
	}
	signStyle := func(sign int, text string) string {
		return m.netStyle(int64(sign)).Render(text)
	}
	rateHead := cur + "/hour"
	if cur == "" {
		rateHead = "chips/h"
	}
	// widest is the width of the widest text of f over all rows, at least
	// minW: a value such as bb/100 after one hand ("-10000.0") or a big
	// hourly rate must not be cut.
	widest := func(minW int, f func(p server.PlayerStats, h *server.HostStats) string) int {
		for _, p := range m.snap.Stats {
			if p.Host != nil {
				minW = max(minW, ansi.StringWidth(f(p, p.Host)))
			}
		}
		return minW
	}
	bbText := func(p server.PlayerStats, h *server.HostStats) string { return bb100(h.NetBB, h.Hands) }
	rateText := func(p server.PlayerStats, h *server.HostStats) string {
		v, _, _ := perHour(h.NetChips, cpu, h.SeatedSeconds, cur, false)
		return v
	}
	lossCell := func(_ server.PlayerStats, h *server.HostStats) string { return lossText(h.BiggestLoss) }
	return []statCol{
		m.playerCol(),
		{head: "bb/100", w: widest(7, bbText), cell: hs(func(p server.PlayerStats, h *server.HostStats) string {
			v := bbText(p, h)
			if v == "—" {
				return dash
			}
			return signStyle(signOf(int64(math.Round(10*h.NetBB))), v)
		})},
		{head: rateHead, w: widest(max(8, ansi.StringWidth(rateHead)), rateText), cell: hs(func(p server.PlayerStats, h *server.HostStats) string {
			v, sign, ok := perHour(h.NetChips, cpu, h.SeatedSeconds, cur, false)
			if !ok {
				return dash
			}
			return signStyle(sign, v)
		})},
		{head: "VPIP/PFR", w: 8, cell: func(p server.PlayerStats, _ int) string {
			if p.Hands <= 0 {
				return dash
			}
			return st.text.Render(fmt.Sprintf("%d/%d", pctNum(p.VPIP, p.Hands), pctNum(p.PFR, p.Hands)))
		}},
		{head: "3-Bet", w: 5, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.ThreeBet, h.ThreeBetChances)
		})},
		{head: "AF", w: 4, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			v := aggression(h.PostflopBetsRaises, h.PostflopCalls)
			if v == "—" {
				return dash
			}
			return st.text.Render(v)
		})},
		{head: "C-Bet", w: 5, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.CBet, h.CBetChances)
		})},
		{head: "Fold CB", w: 7, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.FoldToCBet, h.FoldToCBetChances)
		})},
		{head: "Fold 3B", w: 7, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.FoldTo3Bet, h.FoldTo3BetChances)
		})},
		{head: "Steal", w: 5, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.Steal, h.StealChances)
		})},
		{head: "Limp", w: 4, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.Limp, h.LimpChances)
		})},
		{head: "Chk-raise", w: 9, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.CheckRaise, h.CheckRaiseChances)
		})},
		{head: "Big loss", w: widest(8, lossCell), cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			if h.BiggestLoss <= 0 {
				return dash
			}
			return st.err.Render("-" + chips(h.BiggestLoss))
		})},
		{head: "Streak", w: 6, cell: hs(func(p server.PlayerStats, h *server.HostStats) string {
			if p.Hands <= 0 {
				return dash
			}
			return st.text.Render(fmt.Sprint(h.LongestWinStreak))
		})},
		{head: "AI win", w: 6, cell: hs(func(_ server.PlayerStats, h *server.HostStats) string {
			return ratio(h.AllInShowdownsWon, h.AllInShowdowns)
		})},
	}
}

func (m *model) hostView(w, h int) string {
	st := m.st
	legend := m.onlineLegend() + st.faint.Render("  · only the host sees this tab · Enter explains")
	return m.rowsView(m.hostCols(), w, h, []string{legend})
}

// hostCard shows every host number of one player as "count/chances" with
// a one-line explanation.
func (m *model) hostCard(p server.PlayerStats, bw, h int) string {
	st := m.st
	cur, cpu := m.currency(), m.perUnit()
	title := strings.ToUpper(p.Name) + " · HOST STATS"
	hs := p.Host
	if hs == nil {
		return m.titledBox(title, []string{"", st.dim.Render("No host stats for this player."), ""}, bw, colGold)
	}
	type hostRow struct{ label, value, detail, text string }
	frac := func(n, d int) string {
		if d <= 0 {
			return "0/0"
		}
		return fmt.Sprintf("%d/%d", n, d)
	}
	ratio := func(label string, n, d int, text string) hostRow {
		return hostRow{label, pct(n, d), frac(n, d), text}
	}

	rateLabel := cur + "/hour"
	if cur == "" {
		rateLabel = "chips/hour"
	}
	rate, _, ok := perHour(hs.NetChips, cpu, hs.SeatedSeconds, cur, true)
	rateWhy := "net per hour seated"
	if !ok {
		rateWhy = "shows after 10 minutes seated"
	}
	streak := fmt.Sprintf("now %d", hs.CurrentWinStreak)

	// A zero hostRow is a blank line.
	rows := []hostRow{
		{},
		{"bb/100", bb100(hs.NetBB, hs.Hands), fmt.Sprintf("%+.0f bb", hs.NetBB), "big blinds won per 100 hands"},
		{rateLabel, rate, duration(hs.SeatedSeconds), rateWhy},
		{"AF", aggression(hs.PostflopBetsRaises, hs.PostflopCalls), fmt.Sprintf("%d/%d", hs.PostflopBetsRaises, hs.PostflopCalls),
			"postflop bets and raises per call"},
		{},
		ratio("3-Bet", hs.ThreeBet, hs.ThreeBetChances, "re-raised a single raise preflop"),
		ratio("Fold to 3-Bet", hs.FoldTo3Bet, hs.FoldTo3BetChances, "open-raised, then folded to a 3-bet"),
		ratio("Steal", hs.Steal, hs.StealChances, "raised first in from CO, BTN or SB"),
		ratio("Limp", hs.Limp, hs.LimpChances, "called first in, no raise before"),
		ratio("C-Bet", hs.CBet, hs.CBetChances, "bet the flop as last preflop raiser"),
		ratio("Fold to C-Bet", hs.FoldToCBet, hs.FoldToCBetChances, "folded to a flop continuation bet"),
		ratio("Check-raise", hs.CheckRaise, hs.CheckRaiseChances, "checked, then raised on that street"),
		ratio("All-in win", hs.AllInShowdownsWon, hs.AllInShowdowns, "won chips at an all-in showdown"),
		{},
		{"Biggest loss", lossText(hs.BiggestLoss), "", "most chips lost in one hand"},
		{"Win streak", fmt.Sprint(hs.LongestWinStreak), streak, "most hands won in a row"},
		{},
	}

	// The value and detail columns are as wide as their widest entry, so
	// that no number is cut (a short sample of seat time often gives a
	// rate of ₹1,000.00/hour or more). An explanation that then does not
	// fits goes on to more lines under its column, when the card has the
	// height for them; else the explanation gets cut.
	valueW, detailW := 9, 8
	for _, r := range rows {
		valueW = max(valueW, ansi.StringWidth(r.value))
		detailW = max(detailW, ansi.StringWidth(r.detail))
	}
	textX := 14 + valueW + 1 + detailW + 1
	textW := max(bw-4-textX, 1)
	build := func(wrap bool) []string {
		var lines []string
		for _, r := range rows {
			if r == (hostRow{}) {
				lines = append(lines, "")
				continue
			}
			head := st.gold.Render(padRight(r.label, 14)) + st.bold.Render(padLeft(r.value, valueW)) + " " +
				st.faint.Render(padRight(r.detail, detailW)) + " "
			texts := []string{r.text}
			if wrap {
				texts = wrapLines(r.text, textW, 0)
			}
			for i, t := range texts {
				if i > 0 {
					head = strings.Repeat(" ", textX)
				}
				lines = append(lines, head+st.dim.Render(t))
			}
		}
		lines = append(lines, st.faint.Render("count/chances · \"—\" means no chance yet · only the host sees this"))
		return squeeze(lines, h-2)
	}
	lines := build(true)
	if len(lines) > h-2 {
		lines = build(false)
	}
	return m.titledBox(title, lines, bw, colGold)
}

func lossText(n int64) string {
	if n <= 0 {
		return "—"
	}
	return "-" + chips(n)
}
