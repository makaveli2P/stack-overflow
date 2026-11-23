package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"golang.org/x/crypto/bcrypt"
)

// Room tests drive real hands through the real engine. A fake clock and a
// manual tick make them fast and deterministic.

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	t      *testing.T
	r      *Room
	clock  *fakeClock
	cancel context.CancelFunc
	done   chan struct{}
	last   map[*Session]Snapshot
}

func testConfig() Config {
	return Config{
		Seats:         6,
		SmallBlind:    10,
		BigBlind:      20,
		StartingStack: 1000,
		TurnTimeout:   30 * time.Second,
		NextHandDelay: 3 * time.Second,
	}
}

// stackedDeck deals the cards in first, then the rest of the 52 cards in a
// fixed order.
func stackedDeck(first string) func() *cards.Deck {
	head := cards.MustParseMany(first)
	all := slices.Clone(head)
	for s := cards.Clubs; s <= cards.Spades; s++ {
		for rk := cards.Two; rk <= cards.Ace; rk++ {
			c := cards.Card{Rank: rk, Suit: s}
			if !slices.Contains(head, c) {
				all = append(all, c)
			}
		}
	}
	return func() *cards.Deck { return cards.NewDeckFrom(slices.Clone(all)) }
}

// The first four cards make one pocket pair of aces and one hand of ace-two
// for every way that the engine can deal them to two players. The next cards
// make no straight, flush or pair on the board, with or without burn cards.
const bustDeck = "As Ad Ac 2h Kd 9c 7h 4s Jd 8c Qd Ts 6c 5s"

func newHarness(t *testing.T, cfg Config, deck func() *cards.Deck) *harness {
	t.Helper()
	return newHarnessWith(t, cfg, options{newDeck: deck})
}

// newHarnessWith is newHarness with more options. The clock and ticker of
// opt are replaced; a zero pinCost becomes bcrypt.MinCost to keep tests fast.
func newHarnessWith(t *testing.T, cfg Config, opt options) *harness {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
	opt.now = clock.now
	opt.ticker = func() (<-chan time.Time, func()) { return nil, func() {} }
	if opt.pinCost == 0 {
		opt.pinCost = bcrypt.MinCost
	}
	r, err := newRoomWith(cfg, opt)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, r: r, clock: clock, done: make(chan struct{}), last: map[*Session]Snapshot{}}
	var ctx context.Context
	ctx, h.cancel = context.WithCancel(context.Background())
	go func() {
		r.Run(ctx)
		close(h.done)
	}()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
}

// advance moves the clock and runs one tick on the Run goroutine.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	h.clock.add(d)
	if err := h.r.do(func() error { h.r.tick(); return nil }); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) connect(key, name string) *Session {
	h.t.Helper()
	s, err := h.r.Connect(key, name)
	if err != nil {
		h.t.Fatalf("Connect(%q, %q): %v", key, name, err)
	}
	return s
}

// snap returns the newest snapshot for s. The room sends before it replies to
// a call, so after a call returns the snapshot waits in the channel.
func (h *harness) snap(s *Session) Snapshot {
	h.t.Helper()
	select {
	case got, ok := <-s.Updates():
		if !ok {
			h.t.Fatal("Updates closed")
		}
		h.last[s] = got
		return got
	default:
	}
	got, ok := h.last[s]
	if !ok {
		h.t.Fatal("no snapshot")
	}
	return got
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// startHeadsUp seats alice and bob and starts the first hand.
func startHeadsUp(t *testing.T, h *harness) (alice, bob *Session) {
	t.Helper()
	alice = h.connect("key:alice", "alice")
	bob = h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(h.r.cfg.NextHandDelay)
	if !h.snap(alice).Table.InHand {
		t.Fatal("hand did not start")
	}
	return alice, bob
}

// toAct returns the session that must act, and the other one.
func (h *harness) toAct(players ...*Session) (actor, other *Session) {
	h.t.Helper()
	v := h.snap(players[0]).Table
	if v.ToAct < 0 {
		h.t.Fatal("nobody must act")
	}
	id := v.Seats[v.ToAct].PlayerID
	for _, p := range players {
		if p.key == id {
			actor = p
		} else {
			other = p
		}
	}
	if actor == nil {
		h.t.Fatalf("player to act %q has no session", id)
	}
	return actor, other
}

// playPassive checks or calls for whoever must act until the hand ends.
func (h *harness) playPassive(players ...*Session) {
	h.t.Helper()
	for range 50 {
		if !h.snap(players[0]).Table.InHand {
			return
		}
		actor, _ := h.toAct(players...)
		legal := h.snap(actor).Table.Legal
		if legal == nil {
			h.t.Fatal("actor has no Legal")
		}
		a := engine.Action{Type: engine.Call}
		if legal.Check {
			a.Type = engine.Check
		}
		must(h.t, actor.Act(a))
	}
	h.t.Fatal("hand did not end")
}

func seatOf(v engine.View, key string) *engine.SeatView {
	for i := range v.Seats {
		if v.Seats[i].PlayerID == key {
			return &v.Seats[i]
		}
	}
	return nil
}

func standing(snap Snapshot, name string) (Standing, bool) {
	for _, st := range snap.Leaderboard {
		if st.Name == name {
			return st, true
		}
	}
	return Standing{}, false
}

func logHas(snap Snapshot, sub string) bool {
	return slices.ContainsFunc(snap.Log, func(l string) bool { return strings.Contains(l, sub) })
}

func tableChips(v engine.View) int64 {
	total := v.TotalPot
	for _, sv := range v.Seats {
		total += sv.Stack
	}
	return total
}

func TestConnectAndSpectators(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "  alice\x1b[31m ")
	snap := h.snap(alice)
	if snap.Me.Name != "alice" || snap.Me.Seat != -1 || snap.Me.Key != "key:alice" {
		t.Errorf("Me = %+v", snap.Me)
	}
	bob := h.connect("key:bob", "bob")
	if got := h.snap(alice).Spectators; !slices.Equal(got, []string{"alice", "bob"}) {
		t.Errorf("spectators = %v", got)
	}
	if _, err := h.r.Connect("key:other", "ALICE"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("same name, other key: err = %v, want ErrNameInUse", err)
	}
	must(t, bob.Sit(-1))
	if got := h.snap(alice).Spectators; !slices.Equal(got, []string{"alice"}) {
		t.Errorf("spectators after sit = %v", got)
	}
	if !logHas(h.snap(alice), "bob joined") {
		t.Error("log has no join line")
	}
	empty := h.connect("", "")
	if snap := h.snap(empty); snap.Me.Name != "player" || snap.Me.Key != "user:player" {
		t.Errorf("empty name: Me = %+v", snap.Me)
	}
}

func TestSitErrors(t *testing.T) {
	cfg := testConfig()
	cfg.Seats = 2
	h := newHarness(t, cfg, nil)
	a := h.connect("ka", "a")
	b := h.connect("kb", "b")
	c := h.connect("kc", "c")
	must(t, a.Sit(1))
	if err := a.Sit(-1); !errors.Is(err, engine.ErrAlreadySeated) {
		t.Errorf("sit twice: %v", err)
	}
	if err := b.Sit(1); !errors.Is(err, engine.ErrSeatTaken) {
		t.Errorf("taken seat: %v", err)
	}
	must(t, b.Sit(-1))
	if err := c.Sit(-1); !errors.Is(err, ErrTableFull) {
		t.Errorf("full table: %v", err)
	}
	if err := c.Stand(); !errors.Is(err, engine.ErrNotSeated) {
		t.Errorf("stand without seat: %v", err)
	}
	if err := c.Rebuy(); !errors.Is(err, engine.ErrNotSeated) {
		t.Errorf("rebuy without seat: %v", err)
	}
	if err := a.Rebuy(); !errors.Is(err, ErrNotBusted) {
		t.Errorf("rebuy with chips: %v", err)
	}
}

func TestHandStartsAfterDelayAndHidesCards(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	if got := h.snap(alice).NextHandAt; !got.IsZero() {
		t.Fatalf("one player: NextHandAt = %v, want zero", got)
	}
	must(t, bob.Sit(-1))
	start := h.clock.now()
	if got := h.snap(alice).NextHandAt; !got.Equal(start.Add(3 * time.Second)) {
		t.Fatalf("NextHandAt = %v, want %v", got, start.Add(3*time.Second))
	}

	h.advance(time.Second)
	snap := h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("hand started before the delay")
	}
	if !snap.Now.Equal(start.Add(time.Second)) {
		t.Errorf("per-second snapshot: Now = %v", snap.Now)
	}
	h.advance(2 * time.Second)
	snap = h.snap(alice)
	if !snap.Table.InHand || !snap.NextHandAt.IsZero() {
		t.Fatalf("after delay: InHand %v NextHandAt %v", snap.Table.InHand, snap.NextHandAt)
	}
	if want := h.clock.now().Add(30 * time.Second); !snap.Deadline.Equal(want) {
		t.Errorf("Deadline = %v, want %v", snap.Deadline, want)
	}

	for _, p := range []*Session{alice, bob} {
		v := h.snap(p).Table
		for _, sv := range v.Seats {
			if sv.Empty {
				continue
			}
			if sv.PlayerID == p.key && len(sv.Cards) != 2 {
				t.Errorf("%s cannot see own cards", p.name)
			}
			if sv.PlayerID != p.key && (sv.Cards != nil || !sv.HasCards) {
				t.Errorf("%s sees opponent cards %v (HasCards %v)", p.name, sv.Cards, sv.HasCards)
			}
		}
	}
}

func TestActFoldAndLedger(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state", "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)

	actor, other := h.toAct(alice, bob)
	if err := other.Act(engine.Action{Type: engine.Check}); !errors.Is(err, engine.ErrNotYourTurn) {
		t.Errorf("out of turn: %v", err)
	}
	must(t, actor.Act(engine.Action{Type: engine.Fold}))

	snap := h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("hand still runs after fold")
	}
	if got := tableChips(snap.Table); got != 2000 {
		t.Errorf("chips on table = %d, want 2000", got)
	}
	if snap.NextHandAt.IsZero() {
		t.Error("next hand not scheduled")
	}
	var sum int64
	for _, st := range snap.Leaderboard {
		sum += st.Net
		if !st.Playing || !st.Online || st.BuyIns != 1 {
			t.Errorf("standing %+v", st)
		}
	}
	if sum != 0 {
		t.Errorf("sum of Net = %d, want 0", sum)
	}
	if snap.Leaderboard[0].Net < snap.Leaderboard[1].Net {
		t.Errorf("leaderboard not sorted: %+v", snap.Leaderboard)
	}

	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Session{alice, bob} {
		stack := seatOf(snap.Table, p.key).Stack
		if a := saved[p.key]; a == nil || a.Bank != stack || a.BoughtChips != 1000 {
			t.Errorf("saved %s = %+v, want bank %d", p.name, a, stack)
		}
	}
}

func TestTimeoutChecksOrFolds(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)

	// Preflop the small blind calls; the big blind then times out and checks.
	sb, bb := h.toAct(alice, bob)
	must(t, sb.Act(engine.Action{Type: engine.Call}))
	if actor, _ := h.toAct(alice, bob); actor != bb {
		t.Fatal("big blind does not have the option")
	}
	h.advance(29 * time.Second)
	if h.snap(alice).Table.Street != engine.Preflop {
		t.Fatal("clock fired early")
	}
	h.advance(time.Second)
	snap := h.snap(alice)
	if snap.Table.Street != engine.Flop {
		t.Fatalf("after timeout: street %v, want Flop (check)", snap.Table.Street)
	}
	if !logHas(snap, "bob timed out") && !logHas(snap, "alice timed out") {
		t.Error("log has no timeout line")
	}
	if want := h.clock.now().Add(30 * time.Second); !snap.Deadline.Equal(want) {
		t.Errorf("new street: Deadline = %v, want %v", snap.Deadline, want)
	}
	if st := seatOf(snap.Table, bb.key); !st.SittingOut || !st.InHand {
		t.Errorf("after the timeout the player must sit out from the next hand: %+v", st)
	}

	// On the flop, one player bets; the other times out and folds.
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Bet, Amount: 40}))
	h.advance(30 * time.Second)
	snap = h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("hand runs after the timeout fold")
	}
	if st := seatOf(snap.Table, other.key); !st.Folded && st.LastAction != "Fold" && !logHas(snap, other.name+" folds") {
		t.Errorf("%s did not fold: %+v", other.name, st)
	}
}

func TestNetDoesNotMoveDuringHand(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Raise, Amount: 100}))
	snap := h.snap(alice)
	for _, st := range snap.Leaderboard {
		if st.Net != 0 {
			t.Errorf("during the hand %s has Net %d, want 0", st.Name, st.Net)
		}
	}
}

func TestDisconnectMidHandStandsAfterHand(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)

	// The player who does not act now leaves; the hand goes on.
	actor, leaver := h.toAct(alice, bob)
	leaver.Close()
	if _, ok := <-leaver.Updates(); ok {
		// Drain the last snapshot, then the channel must close.
		if _, ok := <-leaver.Updates(); ok {
			t.Fatal("Updates still open after Close")
		}
	}
	if err := leaver.Act(engine.Action{Type: engine.Fold}); !errors.Is(err, ErrClosed) {
		t.Errorf("act after close: %v", err)
	}
	snap := h.snap(actor)
	if !snap.Table.InHand || seatOf(snap.Table, leaver.key) == nil {
		t.Fatal("leaver lost the seat during the hand")
	}

	// The actor calls; the leaver's turn then acts at once (check), and so on.
	h.playPassive(actor)
	snap = h.snap(actor)
	if seatOf(snap.Table, leaver.key) != nil {
		t.Fatal("leaver still seated after the hand")
	}
	st, ok := standing(snap, leaver.name)
	if !ok || st.Playing || st.Online {
		t.Errorf("leaver standing = %+v", st)
	}
	stack := seatOf(snap.Table, actor.key).Stack
	if st.Net != -(stack - 1000) {
		t.Errorf("leaver net %d, actor stack %d", st.Net, stack)
	}
	if !snap.NextHandAt.IsZero() {
		t.Error("next hand scheduled with one player")
	}
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved[leaver.key].Bank + saved[actor.key].Bank; got != 2000 {
		t.Errorf("saved banks sum to %d, want 2000", got)
	}
}

func TestDisconnectOnTurnActsAtOnce(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	actor.Close() // Preflop small blind: the default action is a fold.
	snap := h.snap(other)
	if snap.Table.InHand {
		t.Fatal("hand still runs; the disconnected player did not act")
	}
	if seatOf(snap.Table, actor.key) != nil {
		t.Error("disconnected player still seated")
	}
	if st, _ := standing(snap, actor.name); st.Net != -10 {
		t.Errorf("small blind net = %d, want -10", st.Net)
	}
}

func TestDisconnectBetweenHandsStandsAtOnce(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	bob.Close()
	snap := h.snap(alice)
	if seatOf(snap.Table, bob.key) != nil {
		t.Error("bob still seated")
	}
	if !snap.NextHandAt.IsZero() {
		t.Error("next hand still scheduled")
	}
	if !logHas(snap, "bob left") {
		t.Error("log has no leave line")
	}
	bob = h.connect("key:bob", "bob")
	if got := h.snap(bob).Me.Bank; got != 1000 {
		t.Errorf("bob bank = %d, want 1000", got)
	}
	must(t, bob.Sit(-1))
	if st, _ := standing(h.snap(bob), "bob"); st.BuyIns != 1 {
		t.Errorf("sit with a bank counted a buy-in: %+v", st)
	}
}

func TestReconnectKeepsSeat(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, leaver := h.toAct(alice, bob)
	leaver.Close()

	back := h.connect(leaver.ident, leaver.name)
	snap := h.snap(back)
	if snap.Me.Seat < 0 || !snap.Table.InHand {
		t.Fatalf("reconnect lost the seat: Me %+v", snap.Me)
	}
	if !logHas(snap, leaver.name+" is back") {
		t.Error("log has no return line")
	}
	h.playPassive(actor, back)
	if h.snap(back).Me.Seat < 0 {
		t.Error("player stood up at hand end after reconnect")
	}
}

func TestSameKeyKicksOldSession(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	old := h.connect("key:alice", "alice")
	must(t, old.Sit(2))
	h.connect("key:bob", "bob")

	fresh := h.connect("key:alice", "alice")
	for range old.Updates() {
	}
	if err := old.Chat("hi"); !errors.Is(err, ErrClosed) {
		t.Errorf("old session chat: %v", err)
	}
	old.Close() // Must not touch the new session.
	snap := h.snap(fresh)
	if snap.Me.Seat != 2 {
		t.Errorf("new session seat = %d, want 2", snap.Me.Seat)
	}
	must(t, fresh.Chat("still here"))
	if st, _ := standing(h.snap(fresh), "alice"); !st.Online || !st.Playing {
		t.Errorf("alice standing = %+v", st)
	}
}

func TestRebuyWhenBusted(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))

	snap := h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("all-in hand did not run out")
	}
	var busted, winner *Session
	for _, p := range []*Session{alice, bob} {
		if seatOf(snap.Table, p.key).Stack == 0 {
			busted = p
		} else {
			winner = p
		}
	}
	if busted == nil {
		t.Fatalf("nobody busted; the stacked deck gave a split: %+v", snap.Table.Result)
	}
	if !snap.NextHandAt.IsZero() {
		t.Error("next hand scheduled with a busted player")
	}
	if err := winner.Rebuy(); !errors.Is(err, ErrNotBusted) {
		t.Errorf("winner rebuy: %v", err)
	}
	must(t, busted.Rebuy())
	snap = h.snap(busted)
	if got := seatOf(snap.Table, busted.key).Stack; got != 1000 {
		t.Errorf("stack after rebuy = %d", got)
	}
	st, _ := standing(snap, busted.name)
	if st.BuyIns != 2 || st.Net != -1000 {
		t.Errorf("busted standing = %+v, want 2 buy-ins and net -1000", st)
	}
	if snap.NextHandAt.IsZero() {
		t.Error("next hand not scheduled after rebuy")
	}
	if snap.Leaderboard[0].Name != winner.name || snap.Leaderboard[0].Net != 1000 {
		t.Errorf("leaderboard = %+v", snap.Leaderboard)
	}
}

func TestSitOutSkipsHands(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	must(t, bob.SitOut(true))
	if !h.snap(alice).NextHandAt.IsZero() {
		t.Error("next hand scheduled while bob sits out")
	}
	must(t, bob.SitOut(false))
	if h.snap(alice).NextHandAt.IsZero() {
		t.Error("next hand not scheduled after bob comes back")
	}
}

func TestSaveAndRestore(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	stacks := map[string]int64{}
	for _, p := range []*Session{alice, bob} {
		stacks[p.key] = seatOf(h.snap(alice).Table, p.key).Stack
	}
	h.stop()

	for range alice.Updates() {
	}
	if err := alice.Chat("hi"); !errors.Is(err, ErrClosed) {
		t.Errorf("chat after room stop: %v", err)
	}
	if _, err := h.r.Connect("key:carol", "carol"); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("connect after room stop: %v", err)
	}

	h2 := newHarness(t, cfg, nil)
	a2 := h2.connect(alice.ident, "alice")
	snap := h2.snap(a2)
	if snap.Me.Bank != stacks[alice.key] {
		t.Errorf("restored bank = %d, want %d", snap.Me.Bank, stacks[alice.key])
	}
	if st, _ := standing(snap, "alice"); st.BuyIns != 1 || st.Net != stacks[alice.key]-1000 {
		t.Errorf("restored standing = %+v", st)
	}
	must(t, a2.Sit(-1))
	snap = h2.snap(a2)
	if got := seatOf(snap.Table, alice.key).Stack; got != stacks[alice.key] {
		t.Errorf("stack after sit = %d", got)
	}
	if snap.Me.Bank != 0 {
		t.Errorf("bank after sit = %d, want 0", snap.Me.Bank)
	}
}

func TestShutdownMidHandRefundsHand(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Raise, Amount: 100}))
	h.stop()
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Session{alice, bob} {
		if got := saved[p.key].Bank; got != 1000 {
			t.Errorf("%s bank = %d, want 1000 (hand cancelled)", p.name, got)
		}
	}
}

func TestChatRoom(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	if err := alice.Chat(" \x1b[2J\x00 "); !errors.Is(err, ErrEmptyChat) {
		t.Errorf("empty chat: %v", err)
	}
	for i := range 5 {
		must(t, alice.Chat(fmt.Sprintf("\x1b[31mmsg %d\x1b[0m", i)))
	}
	if err := alice.Chat("one more"); !errors.Is(err, errChatTooFast) {
		t.Errorf("sixth message: %v", err)
	}
	must(t, bob.Chat("bob has his own limit"))
	h.clock.add(5 * time.Second)
	must(t, alice.Chat("later"))

	chat := h.snap(bob).Chat
	if len(chat) != 7 {
		t.Fatalf("chat has %d lines, want 7", len(chat))
	}
	if chat[0].Text != "msg 0" || chat[0].Name != "alice" {
		t.Errorf("first line = %+v", chat[0])
	}
	if chat[6].Text != "later" || !chat[6].At.Equal(h.clock.now()) {
		t.Errorf("last line = %+v", chat[6])
	}
}

func TestChatKeepsLast100(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	for i := range 130 {
		h.clock.add(time.Second) // Stay below the rate limit.
		must(t, alice.Chat(fmt.Sprint(i)))
	}
	chat := h.snap(alice).Chat
	if len(chat) != 100 || chat[0].Text != "30" || chat[99].Text != "129" {
		t.Errorf("chat len %d, first %q, last %q", len(chat), chat[0].Text, chat[len(chat)-1].Text)
	}
}

func TestUpdatesKeepNewest(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	for i := range 3 {
		must(t, bob.Chat(fmt.Sprint(i)))
	}
	if n := len(alice.Updates()); n != 1 {
		t.Fatalf("buffer holds %d snapshots, want 1", n)
	}
	chat := (<-alice.Updates()).Chat
	if len(chat) != 3 {
		t.Errorf("newest snapshot has %d chat lines, want 3", len(chat))
	}
}

func TestSnapshotsAreIndependent(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Chat("hello"))
	sa, sb := h.snap(alice), h.snap(bob)
	sa.Chat[0].Text = "changed"
	sa.Log[0] = "changed"
	if sb.Chat[0].Text != "hello" || sb.Log[0] == "changed" {
		t.Error("snapshots share slices")
	}
}

// TestConcurrentCallsAndStop runs many callers while the room stops. It must
// not deadlock and must pass the race detector.
func TestConcurrentCallsAndStop(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			s, err := h.r.Connect(fmt.Sprintf("k%d", i), fmt.Sprintf("p%d", i))
			if err != nil {
				return
			}
			go func() {
				for range s.Updates() {
				}
			}()
			for j := range 20 {
				_ = s.Chat(fmt.Sprint(j))
				_ = s.Sit(-1)
				_ = s.SitOut(j%2 == 0)
				_ = s.Stand()
			}
			s.Close()
		})
	}
	h.stop()
	wg.Wait()
	if _, err := h.r.Connect("late", "late"); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("connect after stop: %v", err)
	}
}

// A player who stands up during a hand leaves the committed chips in the
// pot. When the server stops before the hand ends, the saved bank must give
// back the chips that each player put in (README: "it cancels the hand and
// gives back the chips that each player put in"). The sum of Net over the
// saved ledger must stay 0, also in the save that standUp makes at once.
func TestStandMidHandThenShutdownRefundsCommittedChips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bank.json")
	cfg := Config{Seats: 6, SmallBlind: 10, BigBlind: 20, StartingStack: 1000,
		TurnTimeout: 30 * time.Second, NextHandDelay: time.Second, StatePath: path}
	clock := &fakeClock{t: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
	r, err := newRoomWith(cfg, options{now: clock.now,
		ticker: func() (<-chan time.Time, func()) { return nil, func() {} }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	byKey := map[string]*Session{}
	var first *Session
	for _, n := range []string{"a", "b", "c"} {
		s, err := r.Connect("key:"+n, n)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Sit(-1); err != nil {
			t.Fatal(err)
		}
		byKey[s.key] = s
		if first == nil {
			first = s
		}
	}
	clock.add(2 * time.Second)
	_ = r.do(func() error { r.tick(); return nil })

	latest := func(s *Session) Snapshot {
		var snap Snapshot
		for {
			select {
			case snap = <-s.Updates():
				continue
			default:
			}
			return snap
		}
	}
	savedNet := func(when string) {
		t.Helper()
		led, err := loadLedger(path)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for k, a := range led {
			t.Logf("%s: %s bank=%d bought=%d net=%d", when, k, a.Bank, a.BoughtChips, a.net(0))
			sum += a.net(0)
		}
		if sum != 0 {
			t.Fatalf("%s: sum of Net in the saved bank = %d, want 0 (chips lost)", when, sum)
		}
	}
	v := latest(first).Table
	if !v.InHand {
		t.Fatal("hand did not start")
	}
	// The small blind or the big blind (not the actor) has chips in the pot.
	// That player stands; two players stay in the hand.
	var leaver *Session
	for _, sv := range v.Seats {
		if !sv.Empty && sv.Committed > 0 && sv.Index != v.ToAct {
			leaver = byKey[sv.PlayerID]
			break
		}
	}
	if leaver == nil {
		t.Fatal("no player with chips in the pot")
	}
	if err := leaver.Stand(); err != nil {
		t.Fatal(err)
	}
	var stillInHand bool
	var boardSum int64
	_ = r.do(func() error {
		stillInHand = r.table.InHand()
		for _, row := range r.leaderboard() {
			boardSum += row.Net
		}
		return nil
	})
	if !stillInHand {
		t.Fatal("hand ended; test needs a running hand")
	}
	if boardSum != 0 {
		t.Errorf("sum of leaderboard Net during the hand = %d, want 0", boardSum)
	}
	savedNet("after stand") // The state on disk if the server crashes now.

	cancel() // The server stops during the hand.
	<-done
	savedNet("after shutdown")
}

// When the hand ends after a player stood up, the pot goes to the winner and
// the leaver gets no refund: the sum of Net stays 0.
func TestStandMidHandThenHandEndsKeepsNetZero(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	a := h.connect("key:a", "a")
	b := h.connect("key:b", "b")
	c := h.connect("key:c", "c")
	for _, s := range []*Session{a, b, c} {
		must(t, s.Sit(-1))
	}
	h.advance(h.r.cfg.NextHandDelay)
	v := h.snap(a).Table
	if !v.InHand {
		t.Fatal("hand did not start")
	}
	byKey := map[string]*Session{a.key: a, b.key: b, c.key: c}
	var leaver *Session
	for _, sv := range v.Seats {
		if !sv.Empty && sv.Committed > 0 && sv.Index != v.ToAct {
			leaver = byKey[sv.PlayerID]
			break
		}
	}
	if leaver == nil {
		t.Fatal("no player with chips in the pot")
	}
	must(t, leaver.Stand())
	var rest []*Session
	for _, s := range []*Session{a, b, c} {
		if s != leaver {
			rest = append(rest, s)
		}
	}
	h.playPassive(rest...)
	var sum int64
	var left int
	for _, row := range h.snap(rest[0]).Leaderboard {
		sum += row.Net
	}
	_ = h.r.do(func() error { left = len(h.r.leftInHand); return nil })
	if sum != 0 {
		t.Errorf("sum of Net after the hand = %d, want 0", sum)
	}
	if left != 0 {
		t.Errorf("leftInHand has %d entries after the hand, want 0", left)
	}
}

// Connect with the same key and a new name while seated. The seat must show
// the same name as the session. Two different keys must never show the same
// visible name at the same time.
func TestReconnectWithNewNameWhileSeatedKeepsOneName(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	a := h.connect("key:1", "alice")
	must(t, a.Sit(-1))
	b := h.connect("key:1", "bob")
	snap := h.snap(b)
	if got := snap.Table.Seats[snap.Me.Seat].Name; got != snap.Me.Name {
		t.Errorf("seat name %q, session name %q", got, snap.Me.Name)
	}
	for _, row := range snap.Leaderboard {
		if row.Playing && row.Name != snap.Me.Name {
			t.Errorf("leaderboard name %q, session name %q", row.Name, snap.Me.Name)
		}
	}
	if _, err := h.r.Connect("key:2", "alice"); err == nil {
		snap = h.snap(b)
		if got := snap.Table.Seats[snap.Me.Seat].Name; strings.EqualFold(got, "alice") {
			t.Errorf("key:2 joined as alice while the seat of key:1 still shows %q", got)
		}
	}
}

// A seated player whose session closed during a hand keeps the seat and its
// name. A different key must not join under that name.
func TestNameOfAwaySeatIsInUse(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	a := h.connect("key:a", "alice")
	b := h.connect("key:b", "bob")
	c := h.connect("key:c", "carol")
	must(t, a.Sit(-1))
	must(t, b.Sit(-1))
	must(t, c.Sit(-1))
	h.advance(h.r.cfg.NextHandDelay)
	if !h.snap(b).Table.InHand {
		t.Fatal("hand did not start")
	}
	// With three players, the hand continues after the away player folds.
	a.Close()
	if v := h.snap(b).Table; !v.InHand || v.Seats[0].PlayerID != a.key {
		t.Fatal("test needs a running hand with the away seat")
	}
	if _, err := h.r.Connect("key:other", "Alice"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("name of an away seat: err = %v, want ErrNameInUse", err)
	}
}
