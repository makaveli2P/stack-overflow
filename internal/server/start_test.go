package server

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// manualConfig is testConfig with ManualStart and the default 10 s pause
// between hands.
func manualConfig() Config {
	cfg := testConfig()
	cfg.ManualStart = true
	cfg.NextHandDelay = 10 * time.Second
	return cfg
}

// logCount returns the number of log lines that are exactly line.
func logCount(snap Snapshot, line string) int {
	n := 0
	for _, l := range snap.Log {
		if l == line {
			n++
		}
	}
	return n
}

// checkWaiting verifies that the room waits for a Start and deals nothing.
func checkWaiting(t *testing.T, snap Snapshot) {
	t.Helper()
	if !snap.Waiting || snap.StartedBy != "" || !snap.NextHandAt.IsZero() || snap.Table.InHand {
		t.Fatalf("Waiting %v StartedBy %q NextHandAt %v InHand %v, want a waiting room",
			snap.Waiting, snap.StartedBy, snap.NextHandAt, snap.Table.InHand)
	}
}

// startManual seats alice and bob, presses start for alice and deals the
// first hand.
func startManual(t *testing.T, h *harness) (alice, bob *Session) {
	t.Helper()
	alice = h.connect("key:alice", "alice")
	bob = h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	must(t, alice.Start())
	h.advance(startDelayCap)
	if !h.snap(alice).Table.InHand {
		t.Fatal("hand did not start")
	}
	return alice, bob
}

func TestManualStartWaitsForStart(t *testing.T) {
	cfg := manualConfig()
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	carol := h.connect("key:carol", "carol")
	checkWaiting(t, h.snap(alice))

	// Errors: a spectator, then one player only, then two with one out.
	if err := carol.Start(); !errors.Is(err, ErrStartNotSeated) {
		t.Errorf("spectator Start: %v, want ErrStartNotSeated", err)
	}
	must(t, alice.Sit(-1))
	if err := alice.Start(); !errors.Is(err, ErrStartNeedsTwo) {
		t.Errorf("Start alone: %v, want ErrStartNeedsTwo", err)
	}
	must(t, bob.Sit(-1))
	must(t, bob.SitOut(true))
	if err := alice.Start(); !errors.Is(err, ErrStartNeedsTwo) {
		t.Errorf("Start with bob out: %v, want ErrStartNeedsTwo", err)
	}
	must(t, bob.SitOut(false))
	if err := carol.Start(); !errors.Is(err, ErrStartNotSeated) {
		t.Errorf("spectator Start with 2 seated: %v, want ErrStartNotSeated", err)
	}

	// Two players sit, but no hand starts without a Start.
	h.advance(time.Minute)
	h.advance(time.Hour)
	checkWaiting(t, h.snap(carol))

	// Bob starts. The 10 s pause is capped at 3 s.
	must(t, bob.Start())
	snap := h.snap(carol)
	if snap.Waiting || snap.StartedBy != "bob" {
		t.Errorf("after Start: Waiting %v StartedBy %q", snap.Waiting, snap.StartedBy)
	}
	if want := h.clock.now().Add(3 * time.Second); !snap.NextHandAt.Equal(want) {
		t.Errorf("NextHandAt %v, want %v", snap.NextHandAt, want)
	}
	if n := logCount(snap, "bob started the game"); n != 1 {
		t.Errorf("start log lines: %d, want 1 in %q", n, snap.Log)
	}
	// A second Start does nothing.
	must(t, alice.Start())
	must(t, carol.Start())
	if snap2 := h.snap(carol); !snap2.NextHandAt.Equal(snap.NextHandAt) || logCount(snap2, "bob started the game") != 1 ||
		logCount(snap2, "alice started the game") != 0 {
		t.Errorf("second Start changed the room: NextHandAt %v log %q", snap2.NextHandAt, snap2.Log)
	}

	h.advance(2 * time.Second)
	if h.snap(alice).Table.InHand {
		t.Fatal("hand started before the 3 s delay")
	}
	h.advance(time.Second)
	snap = h.snap(alice)
	if !snap.Table.InHand || snap.StartedBy != "" || snap.Waiting {
		t.Fatalf("after the delay: InHand %v StartedBy %q Waiting %v", snap.Table.InHand, snap.StartedBy, snap.Waiting)
	}

	// After the first hand, hands follow one another with the full pause.
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	snap = h.snap(alice)
	if snap.Waiting || snap.Table.InHand {
		t.Fatalf("after the fold: Waiting %v InHand %v", snap.Waiting, snap.Table.InHand)
	}
	if want := h.clock.now().Add(cfg.NextHandDelay); !snap.NextHandAt.Equal(want) {
		t.Errorf("NextHandAt %v, want %v (the full pause)", snap.NextHandAt, want)
	}
	h.advance(cfg.NextHandDelay)
	if !h.snap(alice).Table.InHand {
		t.Error("second hand did not start without a new Start")
	}
}

func TestManualStartShortDelay(t *testing.T) {
	cfg := manualConfig()
	cfg.NextHandDelay = time.Second
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	must(t, alice.Start())
	if want := h.clock.now().Add(time.Second); !h.snap(alice).NextHandAt.Equal(want) {
		t.Errorf("NextHandAt %v, want %v (NextHandDelay is shorter than the cap)", h.snap(alice).NextHandAt, want)
	}
	h.advance(time.Second)
	if !h.snap(alice).Table.InHand {
		t.Error("hand did not start after NextHandDelay")
	}
}

func TestManualStartBackToWaiting(t *testing.T) {
	cfg := manualConfig()
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, bob := startManual(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	if h.snap(alice).NextHandAt.IsZero() {
		t.Fatal("no next hand after the first hand")
	}

	// Bob leaves during the pause: heads-up ends, and the room waits.
	must(t, bob.Stand())
	checkWaiting(t, h.snap(alice))
	must(t, bob.Sit(-1))
	h.advance(time.Hour)
	checkWaiting(t, h.snap(alice))
	if err := alice.Start(); err != nil {
		t.Fatal(err)
	}
	// startManual and this Start: one line each.
	if n := logCount(h.snap(alice), "alice started the game"); n != 2 {
		t.Errorf("start log lines: %d, want 2", n)
	}

	// Bob leaves again before the first hand of the new start.
	must(t, bob.Stand())
	checkWaiting(t, h.snap(alice))
	h.advance(startDelayCap)
	checkWaiting(t, h.snap(alice))

	// A player who sits out also stops a heads-up game.
	must(t, bob.Sit(-1))
	must(t, bob.Start())
	h.advance(startDelayCap)
	if !h.snap(alice).Table.InHand {
		t.Fatal("hand did not start")
	}
	must(t, bob.SitOut(true))
	h.playPassive(alice, bob)
	checkWaiting(t, h.snap(alice))
}

func TestManualStartBustInRunout(t *testing.T) {
	cfg := manualConfig()
	cfg.RunoutDelay = 3 * time.Second
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startManual(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	for range 10 {
		snap := h.snap(alice)
		if !snap.Table.InHand {
			break
		}
		if snap.Waiting || !snap.NextHandAt.IsZero() {
			t.Fatalf("during the run-out: Waiting %v NextHandAt %v", snap.Waiting, snap.NextHandAt)
		}
		h.advance(cfg.RunoutDelay)
	}
	if h.snap(alice).Table.InHand {
		t.Fatal("run-out did not end")
	}
	// One player busted: fewer than 2 can play, so the room waits.
	checkWaiting(t, h.snap(bob))
}

func TestManualStartSplitRunoutContinues(t *testing.T) {
	cfg := manualConfig()
	cfg.RunoutDelay = 3 * time.Second
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, bob := startManual(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	for range 10 {
		if !h.snap(alice).Table.InHand {
			break
		}
		h.advance(cfg.RunoutDelay)
	}
	snap := h.snap(alice)
	if snap.Table.InHand || snap.Waiting || snap.NextHandAt.IsZero() {
		t.Fatalf("after a split run-out: InHand %v Waiting %v NextHandAt %v", snap.Table.InHand, snap.Waiting, snap.NextHandAt)
	}
	h.advance(cfg.NextHandDelay)
	if !h.snap(alice).Table.InHand {
		t.Error("next hand did not start after the pause")
	}
}

func TestManualStartMarkSettled(t *testing.T) {
	cfg := manualConfig()
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice := h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	must(t, alice.Start())
	h.advance(startDelayCap)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	must(t, alice.MarkSettled())
	checkWaiting(t, h.snap(alice))

	// The players sit again for the new period: no hand without a Start.
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(time.Hour)
	checkWaiting(t, h.snap(bob))
	must(t, bob.Start())
	h.advance(startDelayCap)
	if !h.snap(alice).Table.InHand {
		t.Error("hand did not start after the new Start")
	}
}

func TestManualStartReconnect(t *testing.T) {
	cfg := manualConfig()
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	carol := h.connect("key:carol", "carol")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	carol.Close()
	carol = h.connect("key:carol", "carol")
	checkWaiting(t, h.snap(carol))
	must(t, alice.Start())
	carol.Close()
	carol = h.connect("key:carol", "carol")
	if snap := h.snap(carol); snap.Waiting || snap.StartedBy != "alice" || snap.NextHandAt.IsZero() {
		t.Errorf("after reconnect: Waiting %v StartedBy %q NextHandAt %v", snap.Waiting, snap.StartedBy, snap.NextHandAt)
	}
	h.advance(startDelayCap)
	if !h.snap(carol).Table.InHand {
		t.Error("hand did not start")
	}
}

func TestManualStartOff(t *testing.T) {
	cfg := testConfig() // ManualStart false
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	carol := h.connect("key:carol", "carol")
	if h.snap(alice).Waiting {
		t.Error("Waiting without ManualStart")
	}
	must(t, carol.Start()) // nil and nothing, also for a spectator
	must(t, alice.Sit(-1))
	must(t, alice.Start())
	must(t, bob.Sit(-1))
	snap := h.snap(alice)
	if snap.Waiting || snap.StartedBy != "" {
		t.Errorf("Waiting %v StartedBy %q without ManualStart", snap.Waiting, snap.StartedBy)
	}
	if want := h.clock.now().Add(cfg.NextHandDelay); !snap.NextHandAt.Equal(want) {
		t.Errorf("NextHandAt %v, want %v", snap.NextHandAt, want)
	}
	must(t, bob.Start())
	if logCount(h.snap(alice), "bob started the game")+logCount(h.snap(alice), "alice started the game") != 0 {
		t.Error("Start wrote a log line without ManualStart")
	}
	h.advance(cfg.NextHandDelay)
	if !h.snap(alice).Table.InHand {
		t.Fatal("hand did not start")
	}
	must(t, bob.Stand())
	h.playPassive(alice, bob)
	if snap := h.snap(alice); snap.Waiting {
		t.Error("Waiting without ManualStart after heads-up ended")
	}
}

func TestManualStartAfterRestart(t *testing.T) {
	cfg := manualConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, bob := startManual(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	h.stop()

	// A new server run waits for a new Start.
	h = newHarness(t, cfg, stackedDeck(splitDeck))
	alice = h.connect("key:alice", "alice")
	bob = h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(time.Hour)
	checkWaiting(t, h.snap(alice))
}
