package server

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// splitDeck gives two players the same ranks for every way that the engine
// can deal the first four cards. The board (after the burns 4h, 5h and 6h)
// is a broadway straight with no flush, so the pot is split and nobody
// busts.
const splitDeck = "2c 2d 3c 3d 4h Th Js Qh 5h Ks 6h As"

func runoutConfig() Config {
	cfg := testConfig()
	cfg.RunoutDelay = 3 * time.Second
	return cfg
}

// allInHeadsUp starts a heads-up hand and puts both players all-in preflop.
func allInHeadsUp(t *testing.T, h *harness) (alice, bob *Session) {
	t.Helper()
	alice, bob = startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	return alice, bob
}

// handsOf returns the lifetime hand count of key.
func (h *harness) handsOf(key string) int {
	h.t.Helper()
	n := 0
	h.inRun(func(r *Room) {
		if a := r.st.Accounts[key]; a != nil {
			n = a.Stats.Hands
		}
	})
	return n
}

// checkPaused verifies the room state while a run-out waits.
func checkPaused(t *testing.T, snap Snapshot, board int) {
	t.Helper()
	v := snap.Table
	if !v.InHand || !v.Runout || v.ToAct >= 0 {
		t.Fatalf("InHand %v Runout %v ToAct %d, want a paused run-out", v.InHand, v.Runout, v.ToAct)
	}
	if len(v.Board) != board {
		t.Fatalf("board %v, want %d cards", v.Board, board)
	}
	if !snap.Deadline.IsZero() || snap.ClockTotal != 0 {
		t.Errorf("turn clock runs during the run-out: deadline %v total %v", snap.Deadline, snap.ClockTotal)
	}
	if !snap.NextHandAt.IsZero() {
		t.Errorf("next hand scheduled during the run-out: %v", snap.NextHandAt)
	}
	if snap.Alert != "" {
		t.Errorf("alert %q during the run-out", snap.Alert)
	}
}

func TestRunoutDealsStreetsOnTimer(t *testing.T) {
	h := newHarness(t, runoutConfig(), stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	spec := h.connect("key:carol", "carol")

	snap := h.snap(alice)
	checkPaused(t, snap, 0)
	if got := tableChips(snap.Table); got != 2000 {
		t.Fatalf("table chips %d, want 2000", got)
	}
	for _, p := range []*Session{alice, bob} {
		if sv := seatOf(h.snap(spec).Table, p.key); len(sv.Cards) != 2 {
			t.Errorf("spectator does not see the cards of %s: %v", p.name, sv.Cards)
		}
	}
	if !logHas(snap, "alice shows") || !logHas(snap, "bob shows") {
		t.Errorf("no show lines in the log: %q", snap.Log)
	}

	// The timer starts at the all-in. One second short: nothing is dealt.
	h.advance(2 * time.Second)
	checkPaused(t, h.snap(alice), 0)
	h.advance(time.Second)
	checkPaused(t, h.snap(alice), 3)
	if !logHas(h.snap(alice), "Flop") {
		t.Errorf("no flop line in the log: %q", h.snap(alice).Log)
	}
	h.advance(2 * time.Second)
	checkPaused(t, h.snap(alice), 3)
	h.advance(time.Second)
	checkPaused(t, h.snap(alice), 4)
	if n := h.handsOf(alice.key); n != 0 {
		t.Errorf("hand recorded before the river: %d", n)
	}
	h.advance(3 * time.Second)

	snap = h.snap(alice)
	v := snap.Table
	if v.InHand || v.Runout || len(v.Board) != 5 || v.Result == nil {
		t.Fatalf("hand did not end after the river: InHand %v Runout %v board %v", v.InHand, v.Runout, v.Board)
	}
	if v.Result.RunoutFrom != 0 {
		t.Errorf("RunoutFrom %d, want 0", v.Result.RunoutFrom)
	}
	for _, p := range []*Session{alice, bob} {
		if got := seatOf(v, p.key).Stack; got != 1000 {
			t.Errorf("%s stack %d after the split, want 1000", p.name, got)
		}
	}
	if want := h.clock.now().Add(h.r.cfg.NextHandDelay); !snap.NextHandAt.Equal(want) {
		t.Errorf("next hand at %v, want %v", snap.NextHandAt, want)
	}
	h.inRun(func(r *Room) {
		if !r.runoutAt.IsZero() || r.handLive {
			t.Errorf("runoutAt %v handLive %v after the hand", r.runoutAt, r.handLive)
		}
	})

	// Stats are recorded once, also with more ticks before the next hand.
	h.advance(time.Second)
	for _, p := range []*Session{alice, bob} {
		if n := h.handsOf(p.key); n != 1 {
			t.Errorf("%s hands = %d, want 1", p.name, n)
		}
	}
	h.advance(h.r.cfg.NextHandDelay)
	if !h.snap(alice).Table.InHand || h.snap(alice).Table.HandNumber != 2 {
		t.Fatal("next hand did not start")
	}
	for _, p := range []*Session{alice, bob} {
		if n := h.handsOf(p.key); n != 1 {
			t.Errorf("%s hands = %d after the next deal, want 1", p.name, n)
		}
	}
}

func TestRunoutZeroDelayIsInstant(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(splitDeck))
	alice, _ := allInHeadsUp(t, h)
	v := h.snap(alice).Table
	if v.InHand || v.Runout || len(v.Board) != 5 {
		t.Fatalf("InHand %v Runout %v board %v, want the board at once", v.InHand, v.Runout, v.Board)
	}
	if h.snap(alice).NextHandAt.IsZero() {
		t.Error("next hand not scheduled")
	}
}

func TestNewRoomRejectsNegativeRunoutDelay(t *testing.T) {
	cfg := testConfig()
	cfg.RunoutDelay = -time.Second
	if _, err := newRoom(cfg); !errors.Is(err, engine.ErrBadConfig) {
		t.Fatalf("RunoutDelay -1s: %v", err)
	}
}

func TestRunoutRefusesExtend(t *testing.T) {
	cfg := extConfig()
	cfg.RunoutDelay = 3 * time.Second
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	// Read the queued snapshots: an unread "turn" cue from the betting
	// moves to the next snapshot (Session.push).
	h.snap(alice)
	h.snap(bob)
	for _, p := range []*Session{alice, bob} {
		if err := p.Extend(); !errors.Is(err, ErrCannotExtend) {
			t.Errorf("%s Extend during the run-out: %v", p.name, err)
		}
		if err := p.Act(engine.Action{Type: engine.Check}); err == nil {
			t.Errorf("%s acts during the run-out", p.name)
		}
	}
	// A long wait never fires a turn clock or a "hurry" cue.
	h.advance(2 * time.Second)
	for _, p := range []*Session{alice, bob} {
		checkPaused(t, h.snap(p), 0)
	}
}

func TestRunoutDisconnect(t *testing.T) {
	cfg := runoutConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	bob.Close()
	snap := h.snap(alice)
	checkPaused(t, snap, 0)
	if seatOf(snap.Table, bob.key) == nil {
		t.Fatal("bob left the seat during the run-out")
	}
	for range 3 {
		h.advance(cfg.RunoutDelay)
	}
	snap = h.snap(alice)
	if snap.Table.InHand || len(snap.Table.Board) != 5 {
		t.Fatalf("run-out did not end: InHand %v board %v", snap.Table.InHand, snap.Table.Board)
	}
	if seatOf(snap.Table, bob.key) != nil {
		t.Error("bob still seated after the hand")
	}
	var bank int64
	h.inRun(func(r *Room) { bank = r.st.Accounts[bob.key].Bank })
	if alice := seatOf(snap.Table, alice.key).Stack; alice+bank != 2000 || bank != 1000 {
		t.Errorf("alice stack %d + bob bank %d, want 1000 + 1000", alice, bank)
	}
	if n := h.handsOf(bob.key); n != 1 {
		t.Errorf("bob hands = %d, want 1", n)
	}
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if saved[alice.key].Bank+saved[bob.key].Bank != 2000 {
		t.Errorf("saved banks %d + %d, want 2000", saved[alice.key].Bank, saved[bob.key].Bank)
	}
}

func TestRunoutStandEndsHand(t *testing.T) {
	h := newHarness(t, runoutConfig(), stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	h.advance(3 * time.Second) // the flop
	checkPaused(t, h.snap(alice), 3)
	must(t, alice.Stand())
	snap := h.snap(bob)
	if snap.Table.InHand || snap.Table.Runout {
		t.Fatalf("hand still live after a stand: InHand %v Runout %v", snap.Table.InHand, snap.Table.Runout)
	}
	if got := seatOf(snap.Table, bob.key).Stack; got != 2000 {
		t.Errorf("bob stack %d, want 2000", got)
	}
	var aliceBank int64
	h.inRun(func(r *Room) {
		aliceBank = r.st.Accounts[alice.key].Bank
		if !r.runoutAt.IsZero() {
			t.Errorf("runoutAt %v after the hand", r.runoutAt)
		}
		if len(r.leftInHand) != 0 {
			t.Errorf("leftInHand %v after the hand", r.leftInHand)
		}
	})
	if aliceBank != 0 {
		t.Errorf("alice bank %d, want 0", aliceBank)
	}
	for _, p := range []*Session{alice, bob} {
		if n := h.handsOf(p.key); n != 1 {
			t.Errorf("%s hands = %d, want 1", p.name, n)
		}
	}
	// The old timer does not deal into the ended hand.
	h.advance(3 * time.Second)
	if b := h.snap(bob).Table.Board; len(b) != 3 {
		t.Errorf("board %v after the stand, want the flop only", b)
	}
}

func TestShutdownDuringRunoutRefundsHand(t *testing.T) {
	cfg := runoutConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "bank.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := allInHeadsUp(t, h)
	h.advance(cfg.RunoutDelay)
	checkPaused(t, h.snap(alice), 3)

	// A save during the run-out counts the hand as not played.
	h.inRun(func(r *Room) { r.save() })
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Session{alice, bob} {
		if got := saved[p.key].Bank; got != 1000 {
			t.Errorf("%s saved bank %d during the run-out, want 1000", p.name, got)
		}
	}

	h.stop()
	saved, err = loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Session{alice, bob} {
		if got := saved[p.key].Bank; got != 1000 {
			t.Errorf("%s bank %d after a stop in the run-out, want 1000 (hand cancelled)", p.name, got)
		}
		if a := saved[p.key]; a.Stats.Hands != 0 {
			t.Errorf("%s hands %d, want 0", p.name, a.Stats.Hands)
		}
	}
}

// A timeout that ends the betting starts the run-out, and the run-out then
// runs no clock.
func TestTimeoutStartsRunout(t *testing.T) {
	h := newHarness(t, runoutConfig(), nil)
	ps := []*Session{h.connect("key:alice", "alice"), h.connect("key:bob", "bob"), h.connect("key:carol", "carol")}
	for _, p := range ps {
		must(t, p.Sit(-1))
	}
	h.advance(h.r.cfg.NextHandDelay)
	first, _ := h.toAct(ps...)
	must(t, first.Act(engine.Action{Type: engine.AllIn}))
	second, _ := h.toAct(ps...)
	must(t, second.Act(engine.Action{Type: engine.AllIn}))
	third, _ := h.toAct(ps...)
	h.advance(h.r.cfg.TurnTimeout)
	snap := h.snap(first)
	checkPaused(t, snap, 0)
	if !logHas(snap, third.name+" timed out") {
		t.Errorf("no timeout line: %q", snap.Log)
	}
	for range 3 {
		h.advance(h.r.cfg.RunoutDelay)
	}
	v := h.snap(first).Table
	if v.InHand || len(v.Board) != 5 {
		t.Fatalf("run-out did not end: InHand %v board %v", v.InHand, v.Board)
	}
	if got := tableChips(v); got != 3000 {
		t.Errorf("table chips %d, want 3000", got)
	}
}

// A run-out delay changes no stat: the same deck and actions give the same
// lifetime and fun stats as an instant run-out.
func TestRunoutKeepsStats(t *testing.T) {
	type stats struct {
		Stats, Fun any
	}
	play := func(delay time.Duration) map[string]stats {
		cfg := testConfig()
		cfg.RunoutDelay = delay
		h := newHarness(t, cfg, stackedDeck(bustDeck))
		alice, _ := allInHeadsUp(t, h)
		for range 3 {
			if !h.snap(alice).Table.InHand {
				break
			}
			h.advance(cfg.RunoutDelay)
		}
		if h.snap(alice).Table.InHand {
			t.Fatalf("delay %s: hand did not end", delay)
		}
		out := map[string]stats{}
		h.inRun(func(r *Room) {
			for key, a := range r.st.Accounts {
				out[key] = stats{a.Stats, a.Fun}
			}
		})
		h.stop()
		return out
	}
	instant, slow := play(0), play(3*time.Second)
	if len(instant) != 2 || !reflect.DeepEqual(instant, slow) {
		t.Errorf("stats differ:\ninstant %+v\nslow    %+v", instant, slow)
	}
}

// A stand ends a paused run-out: the winner's hand was face up since the
// start of the run-out, so the winner cannot show it again. The log keeps
// one show line, and the next hand does not start later.
func TestRunoutStandNoSecondShow(t *testing.T) {
	h := newHarness(t, runoutConfig(), stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	h.advance(3 * time.Second) // the flop
	must(t, alice.Stand())
	before := h.snap(bob)
	r := before.Table.Result
	if r == nil || !r.NoShowdown || len(r.Revealed) != 1 || r.Revealed[0].PlayerID != bob.key || len(r.Revealed[0].Cards) != 2 {
		t.Fatalf("result after the stand: %+v", r)
	}
	if err := bob.ShowCards(true, true); !errors.Is(err, engine.ErrNotNow) {
		t.Fatalf("ShowCards = %v, want ErrNotNow", err)
	}
	after := h.snap(bob)
	n := 0
	for _, l := range after.Log {
		if strings.HasPrefix(l, "bob shows") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d 'bob shows' lines, want 1\n%v", n, after.Log)
	}
	if !after.NextHandAt.Equal(before.NextHandAt) {
		t.Errorf("next hand moved from %v to %v", before.NextHandAt, after.NextHandAt)
	}
}
