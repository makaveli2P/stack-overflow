package server

// Batch 3 stats: the fun stats (everyone), the favourite hand (the owner
// only) and the host stats (the host only). See DESIGN.md "Batch 3".
//
// deriveHand is a pure function: one engine.Result in, the per-player
// deltas of that hand out. applyHand adds the deltas to the accounts.

import (
	"cmp"
	"math/bits"
	"slices"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

const (
	badBeatEquity  = 0.70  // the least equity for a bad beat
	badBeatSamples = 20000 // samples when more than 2 board cards are to come
)

// funStats are the lifetime fun stats of one account. They count only the
// hands from batch 3 onward.
type funStats struct {
	Hands         int      `json:"hands"` // hands dealt in since batch 3
	BadBeats      int      `json:"bad_beats"`
	WorstBeat     float64  `json:"worst_beat"`
	WorstBeatWith []string `json:"worst_beat_with,omitempty"` // short card forms
	LuckiestRiver int64    `json:"luckiest_river"`
	RiverCard     string   `json:"river_card,omitempty"` // short card form
	FirstWinAfter int      `json:"first_win_after"`
	// LostTo gives the chips that this account lost to each other account
	// (flow(this → id)), by account id.
	LostTo map[string]int64 `json:"lost_to,omitempty"`
	// Starting counts the canonical starting hands ("AKs") that the player
	// played voluntarily (VPIP).
	Starting map[string]int `json:"starting,omitempty"`
}

// handFacts is what one finished hand adds to the stats.
type handFacts struct {
	Players map[string]*playerFacts // by player (account) id
	// Flows[l][w] is the chips that loser l lost to winner w in this hand.
	Flows map[string]map[string]int64
}

// playerFacts is the part of one player in one finished hand.
type playerFacts struct {
	// Host has the counts of this hand only. SeatedSeconds and the streak
	// fields stay zero; BiggestLoss is the loss of this hand.
	Host HostStats
	Won  bool // Won > 0

	BadBeat    bool
	BeatEquity float64
	BeatWith   []cards.Card

	RiverWon  int64 // > 0 when the river made this player a main-pot winner
	RiverCard cards.Card

	Starting string // canonical starting hand; "" when not VPIP or unknown
}

// deriveHand computes the batch 3 stats of one hand. holes gives the hole
// cards of the players as the room saw them at the deal (each player's own
// cards); only the favourite hand reads it. Every other stat uses only
// public facts: the actions, the summaries and the cards shown at showdown.
func deriveHand(res *engine.Result, holes map[string][]cards.Card) handFacts {
	f := handFacts{
		Players: make(map[string]*playerFacts, len(res.Players)),
		Flows:   make(map[string]map[string]int64),
	}
	for _, p := range res.Players {
		if f.Players[p.PlayerID] != nil {
			continue
		}
		pf := &playerFacts{Won: p.Won > 0}
		pf.Host.NetChips = p.Won - p.Committed
		if res.BigBlind > 0 {
			pf.Host.NetBB = float64(p.Won-p.Committed) / float64(res.BigBlind)
		}
		pf.Host.BiggestLoss = max(0, p.Committed-p.Won)
		if p.AllIn && p.WentToShowdown {
			pf.Host.AllInShowdowns = 1
			if p.WonAtShowdown {
				pf.Host.AllInShowdownsWon = 1
			}
		}
		if h := holes[p.PlayerID]; p.VPIP && len(h) == 2 {
			pf.Starting = startingHand(h[0], h[1])
		}
		f.Players[p.PlayerID] = pf
	}
	get := func(id string) *HostStats {
		if pf := f.Players[id]; pf != nil {
			return &pf.Host
		}
		return &HostStats{} // not dealt in by the summary: count nothing
	}
	preflopStats(res, get)
	postflopStats(res, get)
	showdownStats(res, f.Players)
	f.Flows = nemesisFlows(res.Players)
	return f
}

// positions returns the small blind, big blind, button and cutoff ids of
// a hand from Result.Order. The cutoff is "" with fewer than 4 players.
func positions(order []string) (sb, bb, btn, co string) {
	n := len(order)
	if n < 2 {
		return
	}
	sb, bb = order[0], order[1]
	if n == 2 {
		return sb, bb, order[0], ""
	}
	btn = order[n-1]
	if n >= 4 {
		co = order[n-2]
	}
	return
}

// streetActions returns the actions of one street, in order.
func streetActions(res *engine.Result, s engine.Street) []engine.ActionRecord {
	var out []engine.ActionRecord
	for _, a := range res.Actions {
		if a.Street == s {
			out = append(out, a)
		}
	}
	return out
}

// preflopStats counts 3-bet, fold to 3-bet, steal and limp.
func preflopStats(res *engine.Result, get func(string) *HostStats) {
	pre := streetActions(res, engine.Preflop)
	sb, bb, btn, co := positions(res.Order)
	acted := make(map[string]bool)    // the first preflop decision is done
	threeBet := make(map[string]bool) // the 3-bet chance is counted
	opener, openAt := "", -1          // the first preflop raise
	onlyFolds := true                 // every decision so far was a fold
	for i, a := range pre {
		p := a.PlayerID
		h := get(p)
		if !acted[p] {
			acted[p] = true
			if a.RaisesBefore == 0 {
				if p != bb {
					h.LimpChances++
					if a.Type == engine.Call {
						h.Limp++
					}
				}
				if onlyFolds && p != "" && (p == co || p == btn || p == sb) {
					h.StealChances++
					if a.Raise {
						h.Steal++
					}
				}
			}
		}
		if a.RaisesBefore == 1 && opener != "" && opener != p && !threeBet[p] {
			threeBet[p] = true
			h.ThreeBetChances++
			if a.Raise {
				h.ThreeBet++
			}
		}
		if a.Type != engine.Fold {
			onlyFolds = false
		}
		if a.Raise && opener == "" {
			opener, openAt = p, i
		}
	}
	if openAt < 0 {
		return
	}
	for _, a := range pre[openAt+1:] {
		if a.PlayerID != opener {
			continue
		}
		if a.RaisesBefore >= 2 {
			h := get(opener)
			h.FoldTo3BetChances++
			if a.Type == engine.Fold {
				h.FoldTo3Bet++
			}
		}
		break
	}
}

// lastPreflopRaiser returns the id of the player who made the last preflop
// bet or raise, or "".
func lastPreflopRaiser(res *engine.Result) string {
	last := ""
	for _, a := range res.Actions {
		if a.Street == engine.Preflop && a.Raise {
			last = a.PlayerID
		}
	}
	return last
}

// postflopStats counts c-bet, fold to c-bet, check-raise and the AF parts.
func postflopStats(res *engine.Result, get func(string) *HostStats) {
	for _, a := range res.Actions {
		if a.Street < engine.Flop || a.Street > engine.River {
			continue
		}
		switch {
		case a.Raise:
			get(a.PlayerID).PostflopBetsRaises++
		case a.Type == engine.Call || a.Type == engine.AllIn:
			get(a.PlayerID).PostflopCalls++
		}
	}

	flop := streetActions(res, engine.Flop)
	if pfr := lastPreflopRaiser(res); pfr != "" {
		cbetAt := -1
		for i, a := range flop {
			if a.PlayerID != pfr {
				continue
			}
			// Checked to: no flop decision before this one bet. (A fold
			// when a check was open is no bet either.)
			if !slices.ContainsFunc(flop[:i], func(b engine.ActionRecord) bool { return b.Raise }) {
				h := get(pfr)
				h.CBetChances++
				if a.Raise {
					h.CBet++
					cbetAt = i
				}
			}
			break
		}
		if cbetAt >= 0 {
			done := map[string]bool{pfr: true}
			raised := false // a raise came after the c-bet
			for _, a := range flop[cbetAt+1:] {
				if !done[a.PlayerID] {
					done[a.PlayerID] = true
					if !raised {
						h := get(a.PlayerID)
						h.FoldToCBetChances++
						if a.Type == engine.Fold {
							h.FoldToCBet++
						}
					}
				}
				if a.Raise {
					raised = true
				}
			}
		}
	}

	for s := engine.Flop; s <= engine.River; s++ {
		checked := make(map[string]bool)  // checked on this street
		facing := make(map[string]bool)   // another player bet after the check
		resolved := make(map[string]bool) // the next decision after the check is counted
		for _, a := range streetActions(res, s) {
			p := a.PlayerID
			switch {
			case checked[p] && !resolved[p]:
				resolved[p] = true
				if facing[p] {
					h := get(p)
					h.CheckRaiseChances++
					if a.Raise {
						h.CheckRaise++
					}
				}
			case a.Type == engine.Check && !checked[p]:
				checked[p] = true
			}
			if a.Raise {
				for q := range checked {
					if q != p && !resolved[q] {
						facing[q] = true
					}
				}
			}
		}
	}
}

// showdownStats finds the bad beats and the lucky rivers. It uses only the
// cards shown at showdown.
func showdownStats(res *engine.Result, players map[string]*playerFacts) {
	if res.NoShowdown || len(res.Board) != 5 {
		return
	}
	var ids []string
	var holes [][]cards.Card
	for _, s := range res.Shown {
		if len(s.Cards) != 2 || slices.Contains(ids, s.PlayerID) {
			continue
		}
		ids = append(ids, s.PlayerID)
		holes = append(holes, s.Cards)
	}
	if len(ids) < 2 {
		return
	}
	mainWinners := make(map[string]bool)
	for _, w := range res.Winnings {
		if w.PotIndex == 0 {
			mainWinners[w.PlayerID] = true
		}
	}
	if len(mainWinners) == 0 {
		return
	}
	won := make(map[string]int64)
	anyAllIn := false
	for _, p := range res.Players {
		won[p.PlayerID] += p.Won
		anyAllIn = anyAllIn || p.AllIn
	}

	// Bad beats: the main-pot contenders are the live players, and at a
	// run-out every live hand is shown.
	if rf := res.RunoutFrom; anyAllIn && (rf == 0 || rf == 3 || rf == 4) {
		if eq := safeEquity(holes, res.Board[:rf]); len(eq) == len(ids) {
			for i, id := range ids {
				pf := players[id]
				if pf == nil || eq[i] < badBeatEquity {
					continue
				}
				if len(mainWinners) == 1 && mainWinners[id] {
					continue // won the whole main pot
				}
				pf.BadBeat = true
				pf.BeatEquity = eq[i]
				pf.BeatWith = slices.Clone(holes[i])
			}
		}
	}

	// Lucky rivers: a main-pot winner who was behind another contender on
	// the turn.
	turn := make([]cards.HandValue, len(ids))
	for i := range ids {
		turn[i], _ = cards.Best(append(slices.Clone(holes[i]), res.Board[:4]...))
	}
	for i, id := range ids {
		pf := players[id]
		if pf == nil || !mainWinners[id] || won[id] <= 0 {
			continue
		}
		for j := range ids {
			if j != i && turn[j].Compare(turn[i]) > 0 {
				pf.RiverWon = won[id]
				pf.RiverCard = res.Board[4]
				break
			}
		}
	}
}

// safeEquity calls cards.Equity. A panic gives nil: a stat must never stop
// the room.
func safeEquity(holes [][]cards.Card, board []cards.Card) (eq []float64) {
	defer func() {
		if recover() != nil {
			eq = nil
		}
	}()
	return cards.Equity(holes, board, badBeatSamples)
}

// nemesisFlows splits each loser's loss over the winners in proportion to
// their wins: flow(L→W) = |netL| × netW / (sum of positive nets), rounded
// to the nearest chip.
func nemesisFlows(ps []engine.PlayerSummary) map[string]map[string]int64 {
	net := make(map[string]int64)
	var order []string
	for _, p := range ps {
		if _, ok := net[p.PlayerID]; !ok {
			order = append(order, p.PlayerID)
		}
		net[p.PlayerID] += p.Won - p.Committed
	}
	var sumPos int64
	for _, id := range order {
		if net[id] > 0 {
			sumPos += net[id]
		}
	}
	flows := make(map[string]map[string]int64)
	if sumPos == 0 {
		return flows
	}
	for _, l := range order {
		if net[l] >= 0 {
			continue
		}
		for _, w := range order {
			if net[w] <= 0 {
				continue
			}
			if c := mulDivRound(-net[l], net[w], sumPos); c > 0 {
				if flows[l] == nil {
					flows[l] = make(map[string]int64)
				}
				flows[l][w] += c
			}
		}
	}
	return flows
}

// mulDivRound returns a×b/c rounded half up, for a, b >= 0, c > 0 and b <= c,
// without overflow.
func mulDivRound(a, b, c int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	// 2ab + c, then divide by 2c.
	hi = hi<<1 | lo>>63
	lo <<= 1
	var carry uint64
	lo, carry = bits.Add64(lo, uint64(c), 0)
	hi += carry
	q, _ := bits.Div64(hi, lo, 2*uint64(c))
	return int64(q)
}

// startingHand returns the canonical form of two hole cards: "AKs", "AKo"
// or "77", higher rank first.
func startingHand(a, b cards.Card) string {
	if a.Rank < b.Rank {
		a, b = b, a
	}
	switch {
	case a.Rank == b.Rank:
		return a.Rank.String() + b.Rank.String()
	case a.Suit == b.Suit:
		return a.Rank.String() + b.Rank.String() + "s"
	default:
		return a.Rank.String() + b.Rank.String() + "o"
	}
}

// startingRank orders canonical starting hands by rank: the higher first
// card, then the higher second card, then suited before offsuit.
func startingRank(h string) (hi, lo, suited int) {
	if len(h) < 2 {
		return
	}
	r1, _ := cards.Parse(h[:1] + "c")
	r2, _ := cards.Parse(h[1:2] + "c")
	if len(h) == 3 && h[2] == 's' {
		suited = 1
	}
	return int(r1.Rank), int(r2.Rank), suited
}

// stronger reports whether starting hand a ranks above b.
func stronger(a, b string) bool {
	ah, al, as := startingRank(a)
	bh, bl, bs := startingRank(b)
	return cmp.Or(cmp.Compare(ah, bh), cmp.Compare(al, bl), cmp.Compare(as, bs)) > 0
}

// favourite returns the most played starting hand and its count. A tie
// goes to the stronger hand by rank order.
func favourite(counts map[string]int) (string, int) {
	best, n := "", 0
	for h, c := range counts {
		if c > n || c == n && c > 0 && stronger(h, best) {
			best, n = h, c
		}
	}
	return best, n
}

// applyHand adds the facts of one hand to the accounts. Ids without an
// account are skipped.
func (st *state) applyHand(f handFacts) {
	for id, pf := range f.Players {
		a := st.Accounts[id]
		if a == nil {
			continue
		}
		x, h := &a.Fun, &a.Host
		x.Hands++
		if pf.Won && x.FirstWinAfter == 0 {
			x.FirstWinAfter = x.Hands
		}
		if pf.BadBeat {
			x.BadBeats++
			if pf.BeatEquity > x.WorstBeat {
				x.WorstBeat = pf.BeatEquity
				x.WorstBeatWith = cardStrings(pf.BeatWith)
			}
		}
		if pf.RiverWon > x.LuckiestRiver {
			x.LuckiestRiver = pf.RiverWon
			x.RiverCard = pf.RiverCard.String()
		}
		if pf.Starting != "" {
			if x.Starting == nil {
				x.Starting = make(map[string]int)
			}
			x.Starting[pf.Starting]++
		}
		h.add(pf.Host)
		if pf.Won {
			h.CurrentWinStreak++
			h.LongestWinStreak = max(h.LongestWinStreak, h.CurrentWinStreak)
		} else {
			h.CurrentWinStreak = 0
		}
	}
	for l, row := range f.Flows {
		a := st.Accounts[l]
		if a == nil {
			continue
		}
		for w, c := range row {
			if st.Accounts[w] == nil || w == l {
				continue
			}
			if a.Fun.LostTo == nil {
				a.Fun.LostTo = make(map[string]int64)
			}
			a.Fun.LostTo[w] += c
		}
	}
}

// add adds the counts of o to h: sums, and the max for BiggestLoss and
// the streaks. SeatedSeconds is summed.
func (h *HostStats) add(o HostStats) {
	h.ThreeBet += o.ThreeBet
	h.ThreeBetChances += o.ThreeBetChances
	h.FoldTo3Bet += o.FoldTo3Bet
	h.FoldTo3BetChances += o.FoldTo3BetChances
	h.Steal += o.Steal
	h.StealChances += o.StealChances
	h.Limp += o.Limp
	h.LimpChances += o.LimpChances
	h.CBet += o.CBet
	h.CBetChances += o.CBetChances
	h.FoldToCBet += o.FoldToCBet
	h.FoldToCBetChances += o.FoldToCBetChances
	h.CheckRaise += o.CheckRaise
	h.CheckRaiseChances += o.CheckRaiseChances
	h.PostflopBetsRaises += o.PostflopBetsRaises
	h.PostflopCalls += o.PostflopCalls
	h.Hands += o.Hands
	h.NetBB += o.NetBB
	h.NetChips += o.NetChips
	h.SeatedSeconds += o.SeatedSeconds
	h.BiggestLoss = max(h.BiggestLoss, o.BiggestLoss)
	h.LongestWinStreak = max(h.LongestWinStreak, o.LongestWinStreak)
	h.AllInShowdowns += o.AllInShowdowns
	h.AllInShowdownsWon += o.AllInShowdownsWon
}

// merge adds the fun stats of o to x (sums; worst beat, luckiest river =
// max; first win = the earlier). LostTo is merged by state.merge.
func (x *funStats) merge(o funStats) {
	x.Hands += o.Hands
	x.BadBeats += o.BadBeats
	if o.WorstBeat > x.WorstBeat {
		x.WorstBeat = o.WorstBeat
		x.WorstBeatWith = slices.Clone(o.WorstBeatWith)
	}
	if o.LuckiestRiver > x.LuckiestRiver {
		x.LuckiestRiver = o.LuckiestRiver
		x.RiverCard = o.RiverCard
	}
	if o.FirstWinAfter > 0 && (x.FirstWinAfter == 0 || o.FirstWinAfter < x.FirstWinAfter) {
		x.FirstWinAfter = o.FirstWinAfter
	}
	for h, c := range o.Starting {
		if x.Starting == nil {
			x.Starting = make(map[string]int)
		}
		x.Starting[h] += c
	}
}

// mergeFlows moves the nemesis flows of the account fromID into toID: the
// row (what from lost) and the column (what others lost to from).
func (st *state) mergeFlows(fromID, toID string) {
	src, dst := st.Accounts[fromID], st.Accounts[toID]
	for w, c := range src.Fun.LostTo {
		if w == toID || w == fromID {
			continue
		}
		if dst.Fun.LostTo == nil {
			dst.Fun.LostTo = make(map[string]int64)
		}
		dst.Fun.LostTo[w] += c
	}
	src.Fun.LostTo = nil
	for id, a := range st.Accounts {
		c, ok := a.Fun.LostTo[fromID]
		if !ok {
			continue
		}
		delete(a.Fun.LostTo, fromID)
		if id == toID || id == fromID {
			continue
		}
		if a.Fun.LostTo == nil {
			a.Fun.LostTo = make(map[string]int64)
		}
		a.Fun.LostTo[toID] += c
	}
	delete(dst.Fun.LostTo, toID)
	if len(dst.Fun.LostTo) == 0 {
		dst.Fun.LostTo = nil
	}
}

// nemesis returns the account that took the most net chips from id:
// the y with the largest flow(id→y) − flow(y→id), when it is more than 0.
// A tie goes to the name that sorts first.
func (st *state) nemesis(id string) (string, int64) {
	me := st.Accounts[id]
	if me == nil {
		return "", 0
	}
	best, bestNet := "", int64(0)
	for _, y := range st.ids() {
		if y == id {
			continue
		}
		other := st.Accounts[y]
		n := me.Fun.LostTo[y] - other.Fun.LostTo[id]
		if n > bestNet || n == bestNet && n > 0 && other.Name < best {
			best, bestNet = other.Name, n
		}
	}
	return best, bestNet
}

// funRow fills the fun stats (everyone may see them) of row from account id.
func (st *state) funRow(row *PlayerStats, id string) {
	x := st.Accounts[id].Fun
	row.BadBeats = x.BadBeats
	row.WorstBeat = x.WorstBeat
	row.WorstBeatWith = parseCards(x.WorstBeatWith)
	row.LuckiestRiver = x.LuckiestRiver
	if x.LuckiestRiver > 0 {
		if c, err := cards.Parse(x.RiverCard); err == nil {
			row.RiverCard = c
		}
	}
	row.FirstWinAfter = x.FirstWinAfter
	row.Nemesis, row.NemesisChips = st.nemesis(id)
}

// hostStats returns the host stats of account id. Hands comes from
// Fun.Hands: both count the hands dealt in from batch 3 onward, so
// NetBB/Hands and NetChips/SeatedSeconds use counters of one era. The
// lifetime PlayerStats.Hands and LifetimeNet also count older hands.
func (st *state) hostStats(id string) HostStats {
	if a := st.Accounts[id]; a != nil {
		h := a.Host
		h.Hands = a.Fun.Hands
		return h
	}
	return HostStats{}
}

func cardStrings(cs []cards.Card) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}
