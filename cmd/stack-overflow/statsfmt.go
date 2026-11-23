package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// minRateSeconds is the seated time that the money-per-hour rate needs.
// Under it, the rate shows "—".
const minRateSeconds = 10 * 60

// writeFunStats prints the Fun table. It does not print the favourite
// hand: that stat is private to each player, also for the host.
func writeFunStats(w io.Writer, stats []server.PlayerStats) {
	fmt.Fprintln(w, "Fun (all players see these)")
	t := &textTable{
		head:  []string{"Player", "Bad beats", "Worst beat", "Luckiest river", "First win", "Nemesis"},
		right: []bool{false, true, false, false, false, false},
	}
	for _, p := range stats {
		t.add(p.Name, strconv.Itoa(p.BadBeats), worstBeat(p), luckiestRiver(p), firstWin(p), nemesis(p))
	}
	t.write(w, "  ")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Bad beats: main pots lost (or split) after the player had 70% or more to win when the money went in.")
	fmt.Fprintln(w, "  Luckiest river: the most chips won in a hand where the river card changed a losing hand into the winner.")
	fmt.Fprintln(w, "  First win: the hand count at the first hand that the player won.")
	fmt.Fprintln(w, "  Nemesis: the opponent who took the most net chips from the player.")
}

// writeHostStats prints the Host table. Only the host sees these stats;
// the state file belongs to the host, so the command prints them.
func writeHostStats(w io.Writer, stats []server.PlayerStats, cur string, cpu int64) {
	fmt.Fprintln(w, "Host (only the host sees these)")
	t := &textTable{
		head: []string{"Player", "bb/100", rateHead(cur), "VPIP/PFR", "3-Bet", "AF", "C-Bet",
			"Fold to C-Bet", "Fold to 3-Bet", "Steal", "Limp", "Check-raise",
			"Biggest loss", "Win streak", "All-in win"},
		right: []bool{false, true, true, true, true, true, true, true, true, true, true, true, true, true, true},
	}
	for _, p := range stats {
		var h server.HostStats
		if p.Host != nil {
			h = *p.Host
		}
		t.add(p.Name,
			bbPer100(h.NetBB, h.Hands),
			moneyPerHour(h.NetChips, h.SeatedSeconds, cur, cpu),
			percentPair(p.VPIP, p.PFR, p.Hands),
			percent(h.ThreeBet, h.ThreeBetChances),
			aggression(h.PostflopBetsRaises, h.PostflopCalls),
			percent(h.CBet, h.CBetChances),
			percent(h.FoldToCBet, h.FoldToCBetChances),
			percent(h.FoldTo3Bet, h.FoldTo3BetChances),
			percent(h.Steal, h.StealChances),
			percent(h.Limp, h.LimpChances),
			percent(h.CheckRaise, h.CheckRaiseChances),
			formatChips(h.BiggestLoss),
			strconv.Itoa(h.LongestWinStreak),
			percent(h.AllInShowdownsWon, h.AllInShowdowns))
	}
	t.write(w, "  ")
	fmt.Fprintln(w)
	won := "money"
	if cur == "" {
		won = "chips"
	}
	fmt.Fprintln(w, "  bb/100: big blinds won for each 100 hands. "+rateHead(cur)+": "+won+" won since batch 3 for each hour in a seat (10 minutes or more).")
	fmt.Fprintln(w, "  3-Bet: re-raised a single raise before the flop. Fold to 3-Bet: folded after the player's first raise got a re-raise.")
	fmt.Fprintln(w, "  AF: bets and raises after the flop divided by calls after the flop.")
	fmt.Fprintln(w, "  C-Bet: the last raiser before the flop bet the flop. Fold to C-Bet: folded to that bet.")
	fmt.Fprintln(w, "  Steal: raised first from the cutoff, the button or the small blind. Limp: called first before the flop with no raise.")
	fmt.Fprintln(w, "  Check-raise: checked, then raised a bet on the same street.")
	fmt.Fprintln(w, "  Biggest loss: the most chips lost in one hand. Win streak: the most hands won one after the other.")
	fmt.Fprintln(w, "  All-in win: showdowns won after an all-in / all-in showdowns. A percentage shows \"—\" with no chances.")
}

// worstBeat is "85% with A♠ A♥", or "—" when there is no bad beat.
func worstBeat(p server.PlayerStats) string {
	if p.BadBeats == 0 || p.WorstBeat <= 0 {
		return "—"
	}
	s := strconv.Itoa(int(math.Round(p.WorstBeat*100))) + "%"
	if c := cardList(p.WorstBeatWith); c != "" {
		s += " with " + c
	}
	return s
}

// luckiestRiver is "1,240 on 9♣", or "—" when there is none.
func luckiestRiver(p server.PlayerStats) string {
	if p.LuckiestRiver <= 0 {
		return "—"
	}
	s := formatChips(p.LuckiestRiver)
	if c := cardText(p.RiverCard); c != "" {
		s += " on " + c
	}
	return s
}

// firstWin is "hand 7", or "—" before the first win.
func firstWin(p server.PlayerStats) string {
	if p.FirstWinAfter <= 0 {
		return "—"
	}
	return "hand " + strconv.Itoa(p.FirstWinAfter)
}

// nemesis is "Bob took 340", or "—" when there is none.
func nemesis(p server.PlayerStats) string {
	if p.Nemesis == "" || p.NemesisChips <= 0 {
		return "—"
	}
	return p.Nemesis + " took " + formatChips(p.NemesisChips)
}

// cardText writes a card as the table shows it, for example "10♥" or "A♠".
// It returns "" for the zero card.
func cardText(c cards.Card) string {
	if c.Rank < cards.Two || c.Rank > cards.Ace {
		return ""
	}
	r := c.Rank.String()
	if c.Rank == cards.Ten {
		r = "10"
	}
	return r + c.Suit.Symbol()
}

// cardList writes cards with a space between them.
func cardList(cs []cards.Card) string {
	var parts []string
	for _, c := range cs {
		if s := cardText(c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// bbPer100 is 100 × netBB / hands with one decimal, or "—" without hands.
// It rounds half away from zero, as the Host tab of the TUI does, so that
// both show the same number: 2.25 bb in 100 hands is "+2.3".
func bbPer100(netBB float64, hands int) string {
	if hands <= 0 {
		return "—"
	}
	return signedDecimal(math.Round(1000*netBB/float64(hands)) / 10)
}

// rateHead is the head of the rate column: "₹/hour", or "chips/hour" when
// there is no currency.
func rateHead(cur string) string {
	if cur == "" {
		return "chips/hour"
	}
	return cur + "/hour"
}

// moneyPerHour is the net money since batch 3 (HostStats.NetChips) for
// each hour in a seat, or "—"
// under minRateSeconds. With no currency, it is the net chips for each
// hour. It scales the exact amount to one hour and then rounds one time,
// as the Host tab of the TUI does.
func moneyPerHour(netChips, seconds int64, cur string, cpu int64) string {
	if seconds < minRateSeconds {
		return "—"
	}
	if cur == "" {
		return signedChips(int64(math.Round(float64(netChips) * 3600 / float64(seconds))))
	}
	// The same order of operations as tui perHour, so that the float
	// result (and an exact half) is the same in both views.
	hundredths := float64(netChips) * 100 / float64(max(cpu, 1)) * 3600 / float64(seconds)
	return signedMoney(server.Money(math.Round(hundredths)), cur)
}

// aggression is the AF: bets and raises / calls with one decimal; "∞"
// when calls are 0 and bets are not; "—" when both are 0.
func aggression(bets, calls int) string {
	switch {
	case bets == 0 && calls == 0:
		return "—"
	case calls == 0:
		return "∞"
	}
	return strconv.FormatFloat(float64(bets)/float64(calls), 'f', 1, 64)
}

// percentPair is "30/12" (whole percentages of d), or "—" when d is 0.
func percentPair(a, b, d int) string {
	if d <= 0 {
		return "—"
	}
	return strings.TrimSuffix(percent(a, d), "%") + "/" + strings.TrimSuffix(percent(b, d), "%")
}

// signedDecimal writes f with one decimal and a "+" above zero. A value
// that rounds to zero is "0.0".
func signedDecimal(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	switch {
	case s == "-0.0" || s == "0.0":
		return "0.0"
	case f > 0:
		return "+" + s
	}
	return s
}
