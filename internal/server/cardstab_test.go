package server

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// TestCardStability drives random hands with 3 to 6 players and random
// events through the room. It checks every snapshot that the room sends to
// every session (pushHook):
//
//   - In one HandNumber, the hole cards of each player never change.
//   - In one HandNumber, the board only grows, for each viewer and for all
//     viewers together.
//   - The cards that a seat shows are the cards of that seat's player.
//   - A viewer sees the hole cards of another player only in a run-out, or
//     after a showdown or a show.
func TestCardStability(t *testing.T) {
	seeds := 40
	ops := 1500
	if testing.Short() {
		seeds, ops = 6, 400
	}
	// CARDSTAB_SEEDS=n runs n seeds, for a long run by hand.
	if n, err := strconv.Atoi(os.Getenv("CARDSTAB_SEEDS")); err == nil && n > 0 {
		seeds = n
	}
	total := map[string]int{}
	for seed := range uint64(seeds) {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			ok := runStability(t, seed, ops)
			for k, v := range ok {
				total[k] += v
			}
		})
	}
	t.Logf("totals: %v", total)
}

// prodConfig is the default config of the real server (batch 7 flags).
func prodConfig() Config {
	return Config{
		Seats:         9,
		SmallBlind:    5,
		BigBlind:      10,
		StartingStack: 1000,
		TurnTimeout:   60 * time.Second,
		NextHandDelay: 10 * time.Second,
		RunoutDelay:   3 * time.Second,
		Extensions:    2,
		ExtensionTime: 30 * time.Second,
	}
}

type holeRec struct {
	seat  int
	cards []cards.Card
}

type stab struct {
	t     *testing.T
	rng   *rand.Rand
	cfg   Config
	h     *harness
	live  map[string]*Session // by identity
	ids   []string
	fresh int
	ok    map[string]int

	mu        sync.Mutex
	errs      []string
	op        string
	holes     map[int]map[string]holeRec // hand -> player id -> cards
	boards    map[int][]cards.Card       // hand -> longest board seen
	lastHand  map[*Session]int
	lastBoard map[*Session][]cards.Card
	own       map[string]map[int][]cards.Card // viewer key -> hand -> own cards
	snaps     int
	seen      map[string]int // snapshot facts, by kind
}

func runStability(t *testing.T, seed uint64, ops int) map[string]int {
	rng := rand.New(rand.NewPCG(seed, 7))
	cfg := prodConfig()
	// Odd seeds run as the real server does now: the room waits for a
	// seated player to press G (Session.Start).
	cfg.ManualStart = seed%2 == 1
	c := &stab{
		t: t, rng: rng, cfg: cfg,
		live:      map[string]*Session{},
		ok:        map[string]int{},
		holes:     map[int]map[string]holeRec{},
		boards:    map[int][]cards.Card{},
		lastHand:  map[*Session]int{},
		lastBoard: map[*Session][]cards.Card{},
		own:       map[string]map[int][]cards.Card{},
		seen:      map[string]int{},
	}
	pushHook = c.check
	oldPanicLog := panicLog
	panicLog = func(string) {}
	t.Cleanup(func() { pushHook, panicLog = nil, oldPanicLog })
	c.h = newHarnessWith(t, cfg, options{newDeck: randomDeck(rng.Uint64())})
	for _, login := range []string{"pa", "pb"} {
		must(t, c.h.r.CreatePINAccount(login, "4826"))
	}
	n := 3 + rng.IntN(4)
	all := []string{"key:k0", "key:k1", "key:k2", "key:k3", "pin:pa", "pin:pb"}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	c.ids = all[:n]
	for _, id := range c.ids {
		s, err := c.h.r.ConnectWith(id, id[4:], ConnectOptions{})
		must(t, err)
		c.live[id] = s
		c.setOp("first sit " + id)
		must(t, s.SitWith(-1, SitChoice(rng.IntN(2))))
	}
	for i := range ops {
		op := c.step()
		c.setOp(fmt.Sprintf("after op %d (%s)", i, op))
		c.mu.Lock()
		errs := c.errs
		c.mu.Unlock()
		if len(errs) > 0 {
			t.Fatalf("seed %d, op %d (%s):\n%s", seed, i, op, strings.Join(errs[:min(len(errs), 10)], "\n"))
		}
	}
	c.h.inRun(func(r *Room) { c.ok["hands"] = r.table.HandNumber() })
	if c.ok["hands"] == 0 || (cfg.ManualStart && c.ok["start"] == 0) {
		t.Errorf("seed %d dealt %d hands after %d starts: the test checks nothing", seed, c.ok["hands"], c.ok["start"])
	}
	c.ok["snapshots"] = c.snaps
	for k, v := range c.seen {
		c.ok[k] = v
	}
	return c.ok
}

func (c *stab) setOp(op string) {
	c.mu.Lock()
	c.op = op
	c.mu.Unlock()
}

func (c *stab) fail(format string, args ...any) {
	c.errs = append(c.errs, c.op+": "+fmt.Sprintf(format, args...))
}

// check runs on the Run goroutine for every snapshot.
func (c *stab) check(s *Session, snap Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snaps++
	r := s.room
	v := snap.Table
	n := v.HandNumber
	who := fmt.Sprintf("viewer %s (%s)", s.key, s.name)

	if prev, ok := c.lastHand[s]; ok && n < prev {
		c.fail("%s: hand number went down %d -> %d", who, prev, n)
	}
	if prev, ok := c.lastHand[s]; ok && prev == n {
		if old := c.lastBoard[s]; len(v.Board) < len(old) || !slices.Equal(v.Board[:len(old)], old) {
			c.fail("%s: board in hand %d changed %v -> %v", who, n, old, v.Board)
		}
	}
	c.lastHand[s] = n
	c.lastBoard[s] = slices.Clone(v.Board)

	// One board for each hand, for all viewers.
	g := c.boards[n]
	short, long := g, v.Board
	if len(short) > len(long) {
		short, long = long, short
	}
	if !slices.Equal(long[:len(short)], short) {
		c.fail("%s: hand %d board %v does not match board %v of another viewer", who, n, v.Board, g)
	}
	if len(v.Board) > len(g) {
		c.boards[n] = slices.Clone(v.Board)
	}

	if snap.Waiting && v.InHand {
		c.fail("%s: the room waits for a start during hand %d", who, n)
	}
	if snap.Waiting && !c.cfg.ManualStart {
		c.fail("%s: Waiting without ManualStart", who)
	}

	if v.Result != nil {
		if v.Result.HandNumber != n {
			c.fail("%s: Result.HandNumber %d, View.HandNumber %d", who, v.Result.HandNumber, n)
		}
		if !v.InHand && !slices.Equal(v.Result.Board, v.Board) {
			c.fail("%s: Result.Board %v, Board %v", who, v.Result.Board, v.Board)
		}
	}

	// The truth: each seated player's cards as that player sees them.
	if c.holes[n] == nil {
		c.holes[n] = map[string]holeRec{}
	}
	for i, sv := range r.table.View("").Seats {
		if sv.Empty {
			continue
		}
		ov := r.table.View(sv.PlayerID)
		oc := ov.Seats[i].Cards
		if oc == nil {
			continue
		}
		rec, seen := c.holes[n][sv.PlayerID]
		switch {
		case !seen:
			c.holes[n][sv.PlayerID] = holeRec{seat: i, cards: slices.Clone(oc)}
		case rec.seat != i || !slices.Equal(rec.cards, oc):
			c.fail("hand %d: player %s had %v at seat %d, now %v at seat %d", n, sv.PlayerID, rec.cards, rec.seat, oc, i)
		}
	}

	// The viewer's own seat as the engine gives it to the account key.
	if tv := r.table.View(s.key); tv.MySeat != v.MySeat {
		c.fail("%s: MySeat %d, engine MySeat %d", who, v.MySeat, tv.MySeat)
	} else if tv.MySeat >= 0 && !slices.Equal(tv.Seats[tv.MySeat].Cards, v.Seats[v.MySeat].Cards) {
		c.fail("%s: own cards %v, engine %v", who, v.Seats[v.MySeat].Cards, tv.Seats[tv.MySeat].Cards)
	}

	mine := 0
	for i, sv := range v.Seats {
		if sv.Index != i {
			c.fail("%s: seat %d has Index %d", who, i, sv.Index)
		}
		if !sv.Empty && sv.PlayerID == s.key {
			mine++
			if v.MySeat != i {
				c.fail("%s: own player at seat %d, MySeat %d", who, i, v.MySeat)
			}
		}
		if sv.Cards == nil {
			continue
		}
		if len(sv.Cards) != 2 {
			c.fail("%s: seat %d shows %d cards", who, i, len(sv.Cards))
		}
		rec, seen := c.holes[n][sv.PlayerID]
		if !seen || rec.seat != i || !slices.Equal(rec.cards, sv.Cards) {
			c.fail("%s: hand %d seat %d (%s) shows %v; that player holds %+v", who, n, i, sv.PlayerID, sv.Cards, rec)
		}
		if sv.PlayerID == s.key {
			c.seen["own cards"]++
			if c.own[s.key] == nil {
				c.own[s.key] = map[int][]cards.Card{}
			}
			if old, ok := c.own[s.key][n]; ok && !slices.Equal(old, sv.Cards) {
				c.fail("%s: own cards in hand %d changed %v -> %v", who, n, old, sv.Cards)
			}
			c.own[s.key][n] = slices.Clone(sv.Cards)
			continue
		}
		switch {
		case v.InHand:
			c.seen["other cards: run-out"]++
		default:
			c.seen["other cards: after the hand"]++
		}
		if !c.mayShow(v, sv) {
			c.fail("%s: sees the cards %v of %s at seat %d (InHand %v Runout %v Street %v)", who, sv.Cards, sv.PlayerID, i, v.InHand, v.Runout, v.Street)
		}
	}
	if mine == 0 && v.MySeat != -1 {
		c.fail("%s: MySeat %d but no own seat", who, v.MySeat)
	}
	if mine > 1 {
		c.fail("%s: %d seats hold the viewer", who, mine)
	}
	if snap.Me.Seat != v.MySeat {
		c.fail("%s: Me.Seat %d, MySeat %d", who, snap.Me.Seat, v.MySeat)
	}
	c.checkShowAtEnd(r, who, snap)
}

// checkShowAtEnd checks the batch 9 rules in one snapshot: Me.ShowAtEnd is
// set only for a folded player of the live hand, and while a hand runs no
// log line of that hand names a hole card of a folded player.
func (c *stab) checkShowAtEnd(r *Room, who string, snap Snapshot) {
	v := snap.Table
	if snap.Me.ShowAtEnd != [2]bool{} {
		c.seen["show at end stored"]++
		if !v.InHand || v.MySeat < 0 || !v.Seats[v.MySeat].InHand || !v.Seats[v.MySeat].Folded {
			c.fail("%s: Me.ShowAtEnd %v outside a folded hand (InHand %v, seat %d)", who, snap.Me.ShowAtEnd, v.InHand, v.MySeat)
		}
	}
	if !v.InHand {
		return
	}
	start := -1
	prefix := fmt.Sprintf("Hand #%d — ", v.HandNumber)
	for i, l := range snap.Log {
		if strings.HasPrefix(l, prefix) {
			start = i
		}
	}
	if start < 0 {
		return
	}
	for i, sv := range r.table.View("").Seats {
		if sv.Empty || !sv.InHand || !sv.Folded {
			continue
		}
		for _, card := range r.table.View(sv.PlayerID).Seats[i].Cards {
			text := card.Rank.String() + card.Suit.Symbol()
			for _, line := range snap.Log[start:] {
				if strings.Contains(line, text) {
					c.fail("%s: the log of hand %d names %s, a card of the folded player %s: %q", who, v.HandNumber, text, sv.PlayerID, line)
				}
			}
		}
	}
}

func mustIdent(raw string) identity {
	id, err := parseIdentity(raw)
	if err != nil {
		panic(err)
	}
	return id
}

// mayShow reports whether another viewer may see the cards of sv.
func (c *stab) mayShow(v engine.View, sv engine.SeatView) bool {
	if v.InHand {
		return v.Runout && !sv.Folded
	}
	res := v.Result
	if res == nil {
		return false
	}
	for _, sh := range res.Shown {
		if sh.PlayerID == sv.PlayerID && sh.Seat == sv.Index && slices.Equal(sh.Cards, sv.Cards) {
			return true
		}
	}
	for _, sh := range res.Revealed {
		if sh.PlayerID == sv.PlayerID && sh.Seat == sv.Index && slices.Equal(sh.Cards, sv.Cards) {
			return true
		}
	}
	return false
}

func (c *stab) pick() *Session {
	var ids []string
	for id, s := range c.live {
		if s.closed.Load() {
			delete(c.live, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return c.live[ids[c.rng.IntN(len(ids))]]
}

func (c *stab) done(op string, err error) {
	if err == nil {
		c.ok[op]++
	}
}

// step runs one random operation and returns its name.
func (c *stab) step() string {
	r := c.h.r
	var live, canStart, runout, waiting bool
	c.h.inRun(func(r *Room) {
		live, canStart, runout, waiting = r.table.InHand(), r.table.CanStartHand(), r.table.RunoutPending(), r.waiting
	})
	c.setOp("op")
	if waiting && canStart && c.rng.IntN(100) < 40 {
		// G: a start by any player. A spectator gets ErrStartNotSeated.
		if s := c.pick(); s != nil {
			err := s.Start()
			c.done("start", err)
			return "start"
		}
	}
	if runout && c.rng.IntN(100) < 40 {
		c.h.advance(c.cfg.RunoutDelay)
		c.done("runout street", nil)
		return "runout street"
	}
	if live && c.rng.IntN(100) < 10 {
		if op := c.storeShow(); op != "" {
			return op
		}
	}
	if live && c.rng.IntN(100) < 75 {
		return c.act()
	}
	if !live && canStart && c.rng.IntN(100) < 50 {
		c.h.advance(c.cfg.NextHandDelay)
		return "next hand"
	}
	s := c.pick()
	switch n := c.rng.IntN(100); {
	case n < 5 || s == nil:
		// A connect: a reconnect after a close, or a second connection
		// with the same identity while the first is open.
		id := c.ids[c.rng.IntN(len(c.ids))]
		_, open := c.live[id]
		var mid bool
		c.h.inRun(func(r *Room) {
			key, _ := r.findAccount(mustIdent(id))
			mid = r.table.InHand() && r.committedInHand(key) > 0
		})
		ns, err := r.ConnectWith(id, id[4:], ConnectOptions{Addr: "10.0.0.9:1"})
		if err == nil && mid {
			c.done("connect while dealt in", nil)
		}
		if err == nil {
			c.live[id] = ns
			if open {
				c.done("second connect", nil)
				return "second connect " + id
			}
			c.done("reconnect", nil)
		}
		return "connect " + id
	case n < 9:
		s.Close()
		delete(c.live, s.ident)
		c.done("close", nil)
		return "close"
	case n < 22:
		seat := c.rng.IntN(c.cfg.Seats+1) - 1
		c.done("sit", s.SitWith(seat, SitChoice(c.rng.IntN(2))))
		return "sit"
	case n < 27:
		var mid bool
		c.h.inRun(func(r *Room) { mid = r.table.InHand() })
		err := s.Stand()
		if mid {
			c.done("stand mid-hand", err)
		} else {
			c.done("stand", err)
		}
		return "stand"
	case n < 30:
		c.done("sitout", s.SitOut(c.rng.IntN(3) == 0))
		return "sitout"
	case n < 40:
		return c.act()
	case n < 52:
		d := []time.Duration{time.Second, 3 * time.Second, 10 * time.Second, 61 * time.Second}[c.rng.IntN(4)]
		c.h.advance(d)
		return fmt.Sprint("advance ", d)
	case n < 57:
		names := []string{"Hana", "Wendy", "Devon", "Zoë", "Ace", "Boss"}
		c.done("rename", s.SetName(names[c.rng.IntN(len(names))]))
		return "rename"
	case n < 63:
		c.done("rabbit", s.RabbitHunt())
		return "rabbit"
	case n < 70:
		if op := c.storeShow(); op != "" {
			return op
		}
		var w *Session
		c.h.inRun(func(r *Room) {
			if res := r.table.View("").Result; res != nil && len(res.Winnings) > 0 && !r.table.InHand() {
				w = r.sessions[res.Winnings[0].PlayerID]
			}
		})
		if w == nil {
			w = s
		}
		c.done("show", w.ShowCards(c.rng.IntN(2) == 0, c.rng.IntN(3) > 0))
		return "show"
	case n < 74:
		c.done("extend", s.Extend())
		return "extend"
	case n < 77:
		c.done("panic", s.Panic())
		return "panic"
	case n < 80:
		c.done("rebuy", s.Rebuy())
		return "rebuy"
	case n < 81:
		// G at any time: a no-op while the game runs.
		c.done("start any time", s.Start())
		return "start any time"
	case n < 82:
		// LinkPIN: a new key takes over a PIN account, also a seated one.
		c.fresh++
		id := fmt.Sprintf("key:fresh%d", c.fresh)
		ns, err := r.ConnectWith(id, "laptop", ConnectOptions{Host: true})
		if err != nil {
			return "link connect"
		}
		login := []string{"pa", "pb"}[c.rng.IntN(2)]
		if err := ns.LinkPIN(login, "4826"); err != nil {
			ns.Close()
			return "link failed"
		}
		c.done("link", nil)
		if !slices.Contains(c.ids, id) {
			c.ids = append(c.ids, id)
		}
		c.live[id] = ns
		return "link " + login
	default:
		_ = s.Chat("hi")
		return "chat"
	}
}

// storeShow lets a random folded player of the live hand store, change or
// cancel a show for the end of the hand (batch 9). It returns "" when no
// hand runs or nobody folded.
func (c *stab) storeShow() string {
	var folded []string
	var w *Session
	c.h.inRun(func(r *Room) {
		if !r.table.InHand() {
			return
		}
		for key := range r.sessions {
			if r.foldedInHand(key) {
				folded = append(folded, key)
			}
		}
		slices.Sort(folded)
		if len(folded) > 0 {
			w = r.sessions[folded[c.rng.IntN(len(folded))]]
		}
	})
	if w == nil {
		return ""
	}
	c.done("show at end", w.ShowCards(c.rng.IntN(2) == 0, c.rng.IntN(3) > 0))
	return "show at end"
}

// act plays a random legal action for the player to act, when that
// player has a live session.
func (c *stab) act() string {
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
		choices = append(choices, engine.Action{Type: engine.Call}, engine.Action{Type: engine.Call}, engine.Action{Type: engine.Call})
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
