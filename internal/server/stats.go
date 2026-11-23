package server

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// recordHand adds the stats of one finished hand to the accounts.
func (st *state) recordHand(res *engine.Result) {
	for _, p := range res.Players {
		a := st.Accounts[p.PlayerID]
		if a == nil {
			continue
		}
		s := &a.Stats
		s.Hands++
		a.PeriodHands++
		if p.Won > 0 {
			s.HandsWon++
		}
		if p.VPIP {
			s.VPIP++
		}
		if p.PFR {
			s.PFR++
		}
		if p.SawFlop {
			s.SawFlop++
		}
		if p.WentToShowdown {
			s.Showdowns++
		}
		if p.WonAtShowdown {
			s.ShowdownsWon++
		}
		if p.AllIn {
			s.AllIns++
		}
		s.BiggestPot = max(s.BiggestPot, p.Won)
		if p.Hand != nil && (s.Best == nil || p.Hand.Compare(s.Best.Value) > 0) {
			b := &bestHand{Value: *p.Hand}
			for _, c := range p.HandCards {
				b.Cards = append(b.Cards, c.String())
			}
			s.Best = b
		}
	}
}

// parseCards reads short card forms; it skips forms that do not parse.
func parseCards(short []string) []cards.Card {
	var out []cards.Card
	for _, s := range short {
		if c, err := cards.Parse(s); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// statsRows builds the stats table. onTable gives the chips on the table of
// each account (see Room.tableChips); online and playing mark the rows.
// Rows: every account with hands, chips in this period, or online.
func (st *state) statsRows(onTable map[string]int64, online, playing map[string]bool) []PlayerStats {
	rows, _ := st.statsTable(onTable, online, playing)
	return rows
}

// statsTable is statsRows plus the account id of each row (ids[i] belongs
// to rows[i]). The rows have the fun stats, but no host stats and no
// favourite hand: those depend on the viewer.
func (st *state) statsTable(onTable map[string]int64, online, playing map[string]bool) ([]PlayerStats, []string) {
	type idRow struct {
		id  string
		row PlayerStats
	}
	var list []idRow
	for _, id := range st.ids() {
		a := st.Accounts[id]
		net := a.net(onTable[id])
		if a.Stats.Hands == 0 && !online[id] && net == 0 && a.BuyIns == 0 {
			continue
		}
		row := PlayerStats{
			Name:         a.Name,
			Online:       online[id],
			Playing:      playing[id],
			Hands:        a.Stats.Hands,
			HandsWon:     a.Stats.HandsWon,
			VPIP:         a.Stats.VPIP,
			PFR:          a.Stats.PFR,
			SawFlop:      a.Stats.SawFlop,
			Showdowns:    a.Stats.Showdowns,
			ShowdownsWon: a.Stats.ShowdownsWon,
			AllIns:       a.Stats.AllIns,
			BiggestPot:   a.Stats.BiggestPot,
			LifetimeNet:  a.SettledNet + net,
			BuyIns:       a.TotalBuyIns,
			PeriodNet:    net,
			PeriodBuyIns: a.BuyIns,
			PeriodHands:  a.PeriodHands,
		}
		if b := a.Stats.Best; b != nil {
			row.BestHand = b.Value.String()
			row.BestCards = parseCards(b.Cards)
		}
		st.funRow(&row, id)
		list = append(list, idRow{id, row})
	}
	slices.SortFunc(list, func(a, b idRow) int {
		if c := cmp.Compare(b.row.PeriodNet, a.row.PeriodNet); c != 0 {
			return c
		}
		return strings.Compare(a.row.Name, b.row.Name)
	})
	rows := make([]PlayerStats, len(list))
	ids := make([]string, len(list))
	for i, x := range list {
		rows[i], ids[i] = x.row, x.id
	}
	return rows, ids
}

// settlement builds the settle-up view of the current period.
func (st *state) settlement(onTable map[string]int64, cpu int64, currency string) Settlement {
	if cpu <= 0 {
		cpu = 1
	}
	var nets []Balance
	for _, id := range st.ids() {
		a := st.Accounts[id]
		if net := a.net(onTable[id]); net != 0 {
			nets = append(nets, Balance{Name: a.Name, Chips: net})
		}
	}
	balances := moneyBalances(nets, cpu)
	s := Settlement{
		Period:       st.Period,
		Since:        st.Since,
		ChipsPerUnit: cpu,
		Currency:     currency,
		Balances:     balances,
		Transfers:    greedyTransfers(balances),
	}
	for i := 0; i < len(st.History) && i < shownHistory; i++ {
		s.History = append(s.History, clonePast(st.History[i]))
	}
	return s
}

// chipsToMoney converts chips to hundredths of a currency unit, rounding
// half away from zero.
func chipsToMoney(chips, cpu int64) Money {
	if cpu <= 0 {
		cpu = 1
	}
	num := chips * 100
	q, rem := num/cpu, num%cpu
	if rem < 0 {
		rem = -rem
	}
	if 2*rem >= cpu {
		if num < 0 {
			q--
		} else {
			q++
		}
	}
	return Money(q)
}

// moneyBalances converts the chip nets to money. Rounding can leave the sum
// a few hundredths off the exact total; moneyBalances then moves one
// hundredth at a time to or from the largest balances, so that the money
// sums to the money of the chip total: exactly zero when the chips balance.
// Chips that do not balance (for example a hand-edited or lossy version 1
// chip bank) are not spread over the players: that would change honest
// balances by large amounts. The transfers then settle only part of the
// balances. The result is sorted by Money, high to low; zero balances are
// left out.
func moneyBalances(nets []Balance, cpu int64) []Balance {
	out := make([]Balance, 0, len(nets))
	var sum Money
	var chips int64
	for _, b := range nets {
		b.Money = chipsToMoney(b.Chips, cpu)
		sum += b.Money
		chips += b.Chips
		out = append(out, b)
	}
	sum -= chipsToMoney(chips, cpu) // the rounding remainder only
	if sum != 0 && len(out) > 0 {
		order := make([]int, len(out))
		for i := range order {
			order[i] = i
		}
		slices.SortStableFunc(order, func(i, j int) int {
			if c := cmp.Compare(absMoney(out[j].Money), absMoney(out[i].Money)); c != 0 {
				return c
			}
			return strings.Compare(out[i].Name, out[j].Name)
		})
		step := Money(-1)
		if sum < 0 {
			step = 1
		}
		for k := 0; sum != 0; k++ {
			out[order[k%len(order)]].Money += step
			sum += step
		}
	}
	out = slices.DeleteFunc(out, func(b Balance) bool { return b.Chips == 0 && b.Money == 0 })
	slices.SortStableFunc(out, func(a, b Balance) int {
		if c := cmp.Compare(b.Money, a.Money); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func absMoney(m Money) Money {
	if m < 0 {
		return -m
	}
	return m
}

// greedyTransfers settles the balances: it matches the largest debtor with
// the largest creditor again and again, and the debtor pays the smaller of
// the two amounts. Each payment clears at least one player, so there are at
// most n-1 payments. Ties go by name.
func greedyTransfers(balances []Balance) []Transfer {
	type party struct {
		name string
		left Money
	}
	var debtors, creditors []party
	for _, b := range balances {
		switch {
		case b.Money < 0:
			debtors = append(debtors, party{b.Name, -b.Money})
		case b.Money > 0:
			creditors = append(creditors, party{b.Name, b.Money})
		}
	}
	largest := func(ps []party) int {
		best := -1
		for i, p := range ps {
			if p.left <= 0 {
				continue
			}
			if best < 0 || p.left > ps[best].left || p.left == ps[best].left && p.name < ps[best].name {
				best = i
			}
		}
		return best
	}
	var out []Transfer
	for {
		d, c := largest(debtors), largest(creditors)
		if d < 0 || c < 0 {
			return out
		}
		pay := min(debtors[d].left, creditors[c].left)
		out = append(out, Transfer{From: debtors[d].name, To: creditors[c].name, Amount: pay})
		debtors[d].left -= pay
		creditors[c].left -= pay
	}
}

// closePeriod records the current period in History and starts the next
// one: every bank and period counter goes to zero. The caller stands every
// player up first.
func (st *state) closePeriod(now time.Time, cpu int64, currency string) PastPeriod {
	s := st.settlement(nil, cpu, currency)
	past := PastPeriod{
		Period:       st.Period,
		Since:        st.Since,
		SettledAt:    now,
		Transfers:    s.Transfers,
		Currency:     currency,
		ChipsPerUnit: s.ChipsPerUnit,
	}
	byName := make(map[string]string, len(st.Accounts))
	for _, id := range st.ids() {
		byName[st.Accounts[id].Name] = id // names are unique
	}
	ids := make([]transferIDs, len(past.Transfers))
	for i, t := range past.Transfers {
		ids[i] = transferIDs{From: byName[t.From], To: byName[t.To]}
	}
	st.History = append([]PastPeriod{clonePast(past)}, st.History...)
	if len(st.History) > maxHistory {
		st.History = st.History[:maxHistory]
	}
	if st.HistoryIDs == nil {
		st.HistoryIDs = make(map[int][]transferIDs)
	}
	st.HistoryIDs[past.Period] = ids
	kept := make(map[int]bool, len(st.History))
	for _, p := range st.History {
		kept[p.Period] = true
	}
	for p := range st.HistoryIDs {
		if !kept[p] {
			delete(st.HistoryIDs, p)
		}
	}
	for _, a := range st.Accounts {
		a.SettledNet += a.net(0)
		a.Bank, a.BuyIns, a.BoughtChips, a.PeriodHands = 0, 0, 0, 0
	}
	st.Period++
	st.Since = now
	return past
}

func clonePast(p PastPeriod) PastPeriod {
	p.Transfers = slices.Clone(p.Transfers)
	return p
}

func cloneSettlement(s Settlement) Settlement {
	s.Balances = slices.Clone(s.Balances)
	s.Transfers = slices.Clone(s.Transfers)
	s.History = slices.Clone(s.History)
	for i := range s.History {
		s.History[i] = clonePast(s.History[i])
	}
	return s
}

func cloneStats(rows []PlayerStats) []PlayerStats {
	out := slices.Clone(rows)
	for i := range out {
		out[i].BestCards = slices.Clone(out[i].BestCards)
		out[i].WorstBeatWith = slices.Clone(out[i].WorstBeatWith)
		if h := out[i].Host; h != nil {
			c := *h
			out[i].Host = &c
		}
	}
	return out
}
