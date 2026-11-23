package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

func extConfig() Config {
	cfg := testConfig()
	cfg.Extensions = 3
	cfg.ExtensionTime = 30 * time.Second
	return cfg
}

// The player to act can extend more than once in a turn. Each extension
// moves the deadline by ExtensionTime from the old deadline, and ClockTotal
// grows with it. The next turn starts with a plain clock again.
func TestExtendOwnTurn(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)

	snap := h.snap(actor)
	start := snap.Deadline
	if snap.ClockTotal != 30*time.Second || snap.Me.ExtensionsLeft != 3 {
		t.Fatalf("before: ClockTotal %v, ExtensionsLeft %d; want 30s, 3", snap.ClockTotal, snap.Me.ExtensionsLeft)
	}
	h.advance(10 * time.Second)

	must(t, actor.Extend())
	snap = h.snap(actor)
	if want := start.Add(30 * time.Second); !snap.Deadline.Equal(want) {
		t.Errorf("Deadline = %v, want %v (old deadline + 30s)", snap.Deadline, want)
	}
	if snap.ClockTotal != 60*time.Second {
		t.Errorf("ClockTotal = %v, want 60s", snap.ClockTotal)
	}
	if snap.Me.ExtensionsLeft != 2 {
		t.Errorf("ExtensionsLeft = %d, want 2", snap.Me.ExtensionsLeft)
	}
	if line := actor.name + " adds 30s (2 left)"; !logHas(snap, line) {
		t.Errorf("log has no %q: %q", line, snap.Log)
	}
	// Every session gets the new snapshot.
	osnap := h.snap(other)
	if !osnap.Deadline.Equal(snap.Deadline) || osnap.ClockTotal != 60*time.Second || osnap.Me.ExtensionsLeft != 3 {
		t.Errorf("other: Deadline %v, ClockTotal %v, ExtensionsLeft %d", osnap.Deadline, osnap.ClockTotal, osnap.Me.ExtensionsLeft)
	}

	must(t, actor.Extend())
	must(t, actor.Extend())
	snap = h.snap(actor)
	if want := start.Add(90 * time.Second); !snap.Deadline.Equal(want) {
		t.Errorf("after 3: Deadline = %v, want %v", snap.Deadline, want)
	}
	if snap.ClockTotal != 120*time.Second || snap.Me.ExtensionsLeft != 0 {
		t.Errorf("after 3: ClockTotal %v, ExtensionsLeft %d; want 2m0s, 0", snap.ClockTotal, snap.Me.ExtensionsLeft)
	}
	if line := actor.name + " adds 30s (0 left)"; !logHas(snap, line) {
		t.Errorf("log has no %q", line)
	}
	if err := actor.Extend(); !errors.Is(err, ErrNoExtensions) {
		t.Errorf("4th Extend = %v, want ErrNoExtensions", err)
	}
	if got := h.snap(actor); !got.Deadline.Equal(start.Add(90 * time.Second)) {
		t.Errorf("a refused Extend moved the deadline to %v", got.Deadline)
	}

	// A new turn: the clock is plain again.
	must(t, actor.Act(engine.Action{Type: engine.Call}))
	snap = h.snap(other)
	if want := h.clock.now().Add(30 * time.Second); !snap.Deadline.Equal(want) || snap.ClockTotal != 30*time.Second {
		t.Errorf("new turn: Deadline %v (want %v), ClockTotal %v (want 30s)", snap.Deadline, want, snap.ClockTotal)
	}
	if snap.Me.ExtensionsLeft != 3 {
		t.Errorf("other ExtensionsLeft = %d, want 3", snap.Me.ExtensionsLeft)
	}
	must(t, other.Extend())
	if got := h.snap(other); got.ClockTotal != 60*time.Second || got.Me.ExtensionsLeft != 2 {
		t.Errorf("other after Extend: ClockTotal %v, ExtensionsLeft %d", got.ClockTotal, got.Me.ExtensionsLeft)
	}
	must(t, other.Act(engine.Action{Type: engine.Check}))
	// Heads-up, the big blind acts first on the flop: a new turn for the
	// same player starts with a plain clock.
	if a, _ := h.toAct(alice, bob); a != other {
		t.Fatalf("%s must act first on the flop", other.name)
	}
	if got := h.snap(other); got.ClockTotal != 30*time.Second || got.Me.ExtensionsLeft != 2 {
		t.Errorf("flop: other ClockTotal %v, ExtensionsLeft %d; want 30s, 2", got.ClockTotal, got.Me.ExtensionsLeft)
	}
	must(t, other.Act(engine.Action{Type: engine.Check}))

	// The first player has none left on a later turn too.
	if a, _ := h.toAct(alice, bob); a != actor {
		t.Fatalf("%s must act on the flop", actor.name)
	}
	if got := h.snap(actor); got.ClockTotal != 30*time.Second || got.Me.ExtensionsLeft != 0 {
		t.Errorf("flop: ClockTotal %v, ExtensionsLeft %d; want 30s, 0", got.ClockTotal, got.Me.ExtensionsLeft)
	}
	if err := actor.Extend(); !errors.Is(err, ErrNoExtensions) {
		t.Errorf("flop Extend = %v, want ErrNoExtensions", err)
	}
}

func TestExtendOffTurn(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	carol := h.connect("key:carol", "carol")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	if err := alice.Extend(); !errors.Is(err, ErrCannotExtend) {
		t.Errorf("between hands: Extend = %v, want ErrCannotExtend", err)
	}
	if got := h.snap(alice); got.ClockTotal != 0 {
		t.Errorf("between hands: ClockTotal = %v, want 0", got.ClockTotal)
	}
	h.advance(h.r.cfg.NextHandDelay)
	_, other := h.toAct(alice, bob)
	if err := other.Extend(); !errors.Is(err, ErrCannotExtend) {
		t.Errorf("off turn: Extend = %v, want ErrCannotExtend", err)
	}
	if err := carol.Extend(); !errors.Is(err, ErrCannotExtend) {
		t.Errorf("spectator: Extend = %v, want ErrCannotExtend", err)
	}
	if got := h.snap(other); got.Me.ExtensionsLeft != 3 || got.ClockTotal != 30*time.Second {
		t.Errorf("refused Extend: ExtensionsLeft %d, ClockTotal %v", got.Me.ExtensionsLeft, got.ClockTotal)
	}
}

func TestExtendWithoutTurnClock(t *testing.T) {
	cfg := extConfig()
	cfg.TurnTimeout = 0
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	if err := actor.Extend(); !errors.Is(err, ErrCannotExtend) {
		t.Errorf("Extend = %v, want ErrCannotExtend", err)
	}
	snap := h.snap(actor)
	if snap.ClockTotal != 0 || !snap.Deadline.IsZero() || snap.Me.ExtensionsLeft != 3 {
		t.Errorf("ClockTotal %v, Deadline %v, ExtensionsLeft %d", snap.ClockTotal, snap.Deadline, snap.Me.ExtensionsLeft)
	}
}

func TestExtendDisabled(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck)) // Extensions 0
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	if err := actor.Extend(); !errors.Is(err, ErrNoExtensions) {
		t.Errorf("Extend = %v, want ErrNoExtensions", err)
	}
	if err := other.Extend(); !errors.Is(err, ErrNoExtensions) {
		t.Errorf("off turn: Extend = %v, want ErrNoExtensions", err)
	}
	snap := h.snap(actor)
	if snap.Me.ExtensionsLeft != 0 || snap.ClockTotal != 30*time.Second {
		t.Errorf("ExtensionsLeft %d, ClockTotal %v; want 0, 30s", snap.Me.ExtensionsLeft, snap.ClockTotal)
	}
}

// The count belongs to the account: a reconnect does not reset it.
func TestExtendReconnectKeepsCount(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Extend())
	must(t, actor.Act(engine.Action{Type: engine.Call}))
	actor.Close() // Off turn: the seat stays until the hand ends.

	back := h.connect(actor.ident, actor.name)
	if got := h.snap(back).Me.ExtensionsLeft; got != 2 {
		t.Fatalf("after reconnect: ExtensionsLeft = %d, want 2", got)
	}
	for range 10 {
		if a, _ := h.toAct(other, back); a == back {
			break
		}
		o := h.snap(other).Table.Legal
		a := engine.Action{Type: engine.Call}
		if o != nil && o.Check {
			a.Type = engine.Check
		}
		must(t, other.Act(a))
	}
	if a, _ := h.toAct(other, back); a != back {
		t.Fatal("reconnected player never got the turn")
	}
	must(t, back.Extend())
	must(t, back.Extend())
	if err := back.Extend(); !errors.Is(err, ErrNoExtensions) {
		t.Errorf("4th Extend over two sessions = %v, want ErrNoExtensions", err)
	}
	if got := h.snap(back).Me.ExtensionsLeft; got != 0 {
		t.Errorf("ExtensionsLeft = %d, want 0", got)
	}
}

// A timeout after an extension works as before: the default action at the
// new deadline, then the player sits out.
func TestTimeoutAfterExtension(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	sb, bb := h.toAct(alice, bob)
	must(t, sb.Act(engine.Action{Type: engine.Call}))
	if actor, _ := h.toAct(alice, bob); actor != bb {
		t.Fatal("big blind does not have the option")
	}
	h.advance(20 * time.Second)
	must(t, bb.Extend()) // 40 s left
	h.advance(39 * time.Second)
	if snap := h.snap(alice); snap.Table.Street != engine.Preflop {
		t.Fatalf("clock fired before the extended deadline: street %v", snap.Table.Street)
	}
	h.advance(time.Second)
	snap := h.snap(alice)
	if snap.Table.Street != engine.Flop {
		t.Fatalf("after timeout: street %v, want Flop (check)", snap.Table.Street)
	}
	if !logHas(snap, bb.name+" timed out") {
		t.Error("log has no timeout line")
	}
	if st := seatOf(snap.Table, bb.key); !st.SittingOut {
		t.Errorf("after the timeout the player must sit out: %+v", st)
	}
	if snap.ClockTotal != 30*time.Second {
		t.Errorf("next turn: ClockTotal = %v, want 30s", snap.ClockTotal)
	}
}

// The "hurry" alert fires once for each deadline, so again after an extension.
func TestHurryAlertAfterExtension(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	if got := h.snap(actor).Alert; got != "turn" {
		t.Fatalf("Alert = %q, want turn", got)
	}
	h.advance(25 * time.Second) // 5 s left
	if got := h.snap(actor).Alert; got != "hurry" {
		t.Fatalf("5 s left: Alert = %q, want hurry", got)
	}
	must(t, actor.Extend()) // 35 s left
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("after Extend: Alert = %q, want empty", got)
	}
	h.advance(29 * time.Second) // 6 s left
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("6 s left: Alert = %q", got)
	}
	h.advance(time.Second) // 5 s left
	if got := h.snap(actor).Alert; got != "hurry" {
		t.Errorf("5 s left on the new deadline: Alert = %q, want hurry", got)
	}
	h.advance(time.Second)
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("4 s left: Alert = %q, want empty (one hurry for each deadline)", got)
	}
}

// An extension before the hurry point does not lose the hurry alert.
func TestHurryAlertAfterEarlyExtension(t *testing.T) {
	h := newHarness(t, extConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	h.snap(actor)
	h.advance(10 * time.Second)
	must(t, actor.Extend()) // 50 s left
	h.advance(44 * time.Second)
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("6 s left: Alert = %q", got)
	}
	h.advance(time.Second)
	if got := h.snap(actor).Alert; got != "hurry" {
		t.Errorf("5 s left: Alert = %q, want hurry", got)
	}
}

// The count is in memory only: a new Room gives the full count again.
func TestExtendCountResetsOnNewRoom(t *testing.T) {
	cfg := extConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Extend())
	must(t, actor.Extend())
	if got := h.snap(actor).Me.ExtensionsLeft; got != 1 {
		t.Fatalf("ExtensionsLeft = %d, want 1", got)
	}
	h.stop()

	h2 := newHarness(t, cfg, stackedDeck(bustDeck))
	again := h2.connect(actor.ident, actor.name)
	if got := h2.snap(again).Me.ExtensionsLeft; got != 3 {
		t.Errorf("new Room: ExtensionsLeft = %d, want 3", got)
	}
}

func TestNewRoomChecksExtensions(t *testing.T) {
	for _, tc := range []struct {
		n   int
		d   time.Duration
		bad bool
	}{
		{-1, 30 * time.Second, true},
		{3, 0, true},
		{3, -time.Second, true},
		{0, 0, false},
		{3, 30 * time.Second, false},
	} {
		cfg := testConfig()
		cfg.Extensions, cfg.ExtensionTime = tc.n, tc.d
		_, err := NewRoom(cfg)
		if bad := err != nil; bad != tc.bad {
			t.Errorf("Extensions %d, ExtensionTime %v: err = %v", tc.n, tc.d, err)
		}
		if err != nil && !errors.Is(err, engine.ErrBadConfig) {
			t.Errorf("Extensions %d, ExtensionTime %v: err = %v, want ErrBadConfig", tc.n, tc.d, err)
		}
	}
}

func TestExtendLogSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:        "30s",
		90 * time.Second:        "90s",
		1500 * time.Millisecond: "1.5s",
		250 * time.Millisecond:  "0.25s",
	} {
		if got := seconds(d); got != want {
			t.Errorf("seconds(%v) = %q, want %q", d, got, want)
		}
	}
	h := newHarness(t, Config{Seats: 6, SmallBlind: 10, BigBlind: 20, StartingStack: 1000,
		TurnTimeout: 30 * time.Second, NextHandDelay: 3 * time.Second,
		Extensions: 2, ExtensionTime: 2500 * time.Millisecond}, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Extend())
	if line := fmt.Sprintf("%s adds 2.5s (1 left)", actor.name); !logHas(h.snap(actor), line) {
		t.Errorf("log has no %q", line)
	}
}
