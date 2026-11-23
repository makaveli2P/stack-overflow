package server

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// TestChipConservationRandom runs many random sessions and operations
// (connect, close, sit with each SitChoice, stand, act, timeouts, rebuy, sit out, SetName,
// MarkSettled, rabbit hunt, show cards, LinkPIN, alerts, restarts). After
// each operation the sum of PeriodNet over all accounts must be 0, the chips
// on the table plus the banks must equal the chips bought in this period,
// the money balances must sum to 0, and the transfers must settle them.
func TestChipConservationRandom(t *testing.T) {
	ops := 1000
	if testing.Short() {
		ops = 150
	}
	for seed := range uint64(4) {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			runChaos(t, seed, ops, 0)
		})
	}
}

// TestChipConservationRandomRunout is the same check with paused all-in
// run-outs: the random advances deal the streets.
func TestChipConservationRandomRunout(t *testing.T) {
	ops := 1000
	if testing.Short() {
		ops = 150
	}
	for seed := range uint64(2) {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			runChaos(t, seed+12, ops, 2*time.Second)
		})
	}
}

// randomDeck returns a deck func with its own generator. Only the Run
// goroutine calls it.
func randomDeck(seed uint64) func() *cards.Deck {
	rng := rand.New(rand.NewPCG(seed, 99))
	return func() *cards.Deck {
		all := cards.FullDeck()
		rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		return cards.NewDeckFrom(all)
	}
}

type chaos struct {
	t        *testing.T
	rng      *rand.Rand
	cfg      Config
	h        *harness
	live     map[string]*Session // by identity
	fresh    int
	ok       map[string]int // successful operations by name
	restarts int
}

func runChaos(t *testing.T, seed uint64, ops int, runout time.Duration) {
	cfg := testConfig()
	cfg.RunoutDelay = runout
	cfg.Seats = 4
	cfg.ChipsPerUnit = 3
	cfg.Currency = "₹"
	cfg.Admins = []string{"pin:pa"}
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	c := &chaos{t: t, rng: rand.New(rand.NewPCG(seed, 1)), cfg: cfg, live: map[string]*Session{}, ok: map[string]int{}}
	c.start()
	for _, login := range []string{"pa", "pb", "pc"} {
		must(t, c.h.r.CreatePINAccount(login, "4826"))
	}
	for i := range ops {
		op := c.step()
		c.check(fmt.Sprintf("op %d (%s)", i, op))
		if t.Failed() {
			t.FailNow()
		}
	}
	c.h.inRun(func(r *Room) { c.ok["hands"] = r.table.View("").HandNumber })
	c.h.stop()
	c.checkFile("end")
	t.Logf("successful operations: %v", c.ok)
	want := []string{"settle", "rabbit", "show", "link", "setname", "act"}
	if runout > 0 {
		want = append(want, "runout street")
	}
	for _, op := range want {
		if c.ok[op] == 0 && ops >= 500 {
			t.Errorf("no successful %s in %d operations", op, ops)
		}
	}
}

// done counts op when err is nil.
func (c *chaos) done(op string, err error) {
	if err == nil {
		c.ok[op]++
	}
}

func (c *chaos) start() {
	c.h = newHarnessWith(c.t, c.cfg, options{newDeck: randomDeck(c.rng.Uint64())})
	c.live = map[string]*Session{}
}

var chaosIdents = []string{"key:k0", "key:k1", "key:k2", "key:k3", "pin:pa", "pin:pb", "pin:pc", "user:legacy"}

var chaosNames = []string{"Boss", "boss", "Alice", "alice", "Zoë", "Иван", "bad@name", "", "Bob 2", "abcdefghijkl", "ALICE"}

func (c *chaos) pick() *Session {
	for id, s := range c.live {
		if s.closed.Load() { // The room closed it: same account connected again.
			delete(c.live, id)
		}
	}
	if len(c.live) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.live))
	for id := range c.live {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return c.live[ids[c.rng.IntN(len(ids))]]
}

// step runs one random operation and returns its name.
func (c *chaos) step() string {
	r := c.h.r
	s := c.pick()
	var live, canStart, runout bool
	c.h.inRun(func(r *Room) {
		live, canStart, runout = r.table.InHand(), r.table.CanStartHand(), r.table.RunoutPending()
	})
	if runout && c.rng.IntN(100) < 50 {
		// Only run-out rooms come here, so the other rooms keep their
		// random sequence.
		c.h.advance(c.cfg.RunoutDelay)
		c.done("runout street", nil)
		return "runout street"
	}
	if live && c.rng.IntN(100) < 60 {
		return c.act()
	}
	if canStart && c.rng.IntN(100) < 40 {
		c.h.advance(c.cfg.NextHandDelay)
		return "next hand"
	}
	switch n := c.rng.IntN(100); {
	case n < 10 || s == nil:
		ident := chaosIdents[c.rng.IntN(len(chaosIdents))]
		var ns *Session
		var err error
		if ident == "user:legacy" {
			ns, err = r.Connect(ident, "legacy")
		} else {
			ns, err = r.ConnectWith(ident, ident[4:], ConnectOptions{Admin: c.rng.IntN(4) == 0})
		}
		if err == nil {
			c.live[ident] = ns
		}
		return "connect " + ident
	case n < 15:
		s.Close()
		delete(c.live, s.ident)
		return "close"
	case n < 25:
		seat := c.rng.IntN(c.cfg.Seats+1) - 1
		// The seat picks the choice, so the random sequence stays as it was.
		if seat%2 == 0 {
			c.done("sit", s.Sit(seat))
			return "sit"
		}
		c.done("sit buy-in", s.SitWith(seat, SitBuyIn))
		return "sit buy-in"
	case n < 29:
		_ = s.Stand()
		return "stand"
	case n < 55:
		return c.act()
	case n < 65:
		d := []time.Duration{time.Second, 5 * time.Second, 31 * time.Second, c.cfg.NextHandDelay}[c.rng.IntN(4)]
		c.h.advance(d)
		return "advance"
	case n < 68:
		_ = s.Rebuy()
		return "rebuy"
	case n < 70:
		_ = s.SitOut(c.rng.IntN(4) == 0)
		return "sitout"
	case n < 75:
		c.done("setname", s.SetName(chaosNames[c.rng.IntN(len(chaosNames))]))
		return "setname"
	case n < 78:
		c.done("settle", s.MarkSettled())
		return "settle"
	case n < 83:
		c.done("rabbit", s.RabbitHunt())
		return "rabbit"
	case n < 88:
		if c.rng.IntN(2) == 0 {
			s = c.winner(s)
		}
		c.done("show", s.ShowCards(c.rng.IntN(2) == 0, c.rng.IntN(2) == 0))
		return "show"
	case n < 92:
		c.fresh++
		ident := fmt.Sprintf("key:fresh%d", c.fresh)
		ns, err := r.ConnectWith(ident, "laptop", ConnectOptions{})
		if err != nil {
			return "link connect"
		}
		c.live[ident] = ns
		login := []string{"pa", "pb", "pc"}[c.rng.IntN(3)]
		pin := []string{"4826", "0000"}[c.rng.IntN(2)]
		if err := ns.LinkPIN(login, pin); err == nil {
			c.done("link", nil)
			// The PIN session of that account (if any) closed.
			if old := c.live["pin:"+login]; old != nil && old.closed.Load() {
				delete(c.live, "pin:"+login)
			}
		}
		return "link"
	case n < 94:
		_ = s.SetAlerts(c.rng.IntN(2) == 0)
		return "alerts"
	case n < 99:
		_ = s.Chat("hello")
		return "chat"
	default:
		c.h.stop()
		c.checkFile("restart")
		c.restarts++
		c.start()
		return "restart"
	}
}

// winner returns the session of the winner of the last hand, or s.
func (c *chaos) winner(s *Session) *Session {
	c.h.inRun(func(r *Room) {
		if res := r.table.View("").Result; res != nil && len(res.Winnings) > 0 {
			if w := r.sessions[res.Winnings[0].PlayerID]; w != nil {
				s = w
			}
		}
	})
	return s
}

// act plays a random legal action for the player to act, when that
// player has a live session.
func (c *chaos) act() string {
	var actor *Session
	var legal engine.Legal
	c.h.inRun(func(r *Room) {
		id, ok := r.table.ToAct()
		if !ok {
			return
		}
		actor = r.sessions[id]
		legal, _ = r.table.LegalFor(id)
	})
	if actor == nil {
		return "act (none)"
	}
	var choices []engine.Action
	if legal.Fold {
		choices = append(choices, engine.Action{Type: engine.Fold})
	}
	if legal.Check {
		choices = append(choices, engine.Action{Type: engine.Check}, engine.Action{Type: engine.Check})
	}
	if legal.Call {
		choices = append(choices, engine.Action{Type: engine.Call}, engine.Action{Type: engine.Call})
	}
	if legal.Bet || legal.Raise {
		typ := engine.Bet
		if legal.Raise {
			typ = engine.Raise
		}
		amount := legal.MinTo
		if legal.MaxTo > legal.MinTo {
			amount += c.rng.Int64N(legal.MaxTo - legal.MinTo + 1)
		}
		choices = append(choices, engine.Action{Type: typ, Amount: amount})
	}
	if legal.AllIn {
		choices = append(choices, engine.Action{Type: engine.AllIn})
	}
	if len(choices) == 0 {
		return "act (no choice)"
	}
	a := choices[c.rng.IntN(len(choices))]
	if err := actor.Act(a); err != nil {
		c.t.Errorf("legal action %+v failed: %v", a, err)
	}
	c.done("act", nil)
	return "act " + a.Type.String()
}

// check verifies the invariants on the Run goroutine's state.
func (c *chaos) check(when string) {
	c.t.Helper()
	var (
		netSum, bought, physical, statSum int64
		set                               Settlement
	)
	c.h.inRun(func(r *Room) {
		onTable := r.tableChips()
		for id, a := range r.st.Accounts {
			netSum += a.net(onTable[id])
			bought += a.BoughtChips
			physical += a.Bank
		}
		v := r.table.View("")
		physical += v.TotalPot
		for _, sv := range v.Seats {
			physical += sv.Stack
		}
		for _, row := range r.st.statsRows(onTable, nil, nil) {
			statSum += row.PeriodNet
		}
		set = r.st.settlement(onTable, r.cfg.ChipsPerUnit, r.cfg.Currency)
	})
	if netSum != 0 || statSum != 0 {
		c.t.Fatalf("%s: sum of PeriodNet = %d (stats rows %d), want 0", when, netSum, statSum)
	}
	if physical != bought {
		c.t.Fatalf("%s: banks + table chips = %d, chips bought = %d", when, physical, bought)
	}
	checkSettles(c.t, set.Balances, set.Transfers)
	if c.t.Failed() {
		c.t.Fatalf("%s: settlement %+v", when, set)
	}
}

// checkFile verifies the invariants on the saved state file.
func (c *chaos) checkFile(when string) {
	c.t.Helper()
	rows, set, err := ReadStats(c.cfg)
	if err != nil {
		c.t.Fatal(err)
	}
	var sum int64
	for _, row := range rows {
		sum += row.PeriodNet
	}
	if sum != 0 {
		c.t.Fatalf("%s: saved PeriodNet sums to %d", when, sum)
	}
	checkSettles(c.t, set.Balances, set.Transfers)
}
