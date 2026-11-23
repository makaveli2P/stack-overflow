package server

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 15: every screenshot goes to the chat. During a hand the link
// waits, and the end of the hand posts it with the result.

func shotURL(n int) string {
	return "http://192.168.1.20:2280/abcdefghj" + string(rune('a'+n))
}

// shots returns the chat lines that hold a screenshot link.
func shots(snap Snapshot) []ChatLine {
	var out []ChatLine
	for _, c := range snap.Chat {
		if c.Shot != "" {
			out = append(out, c)
		}
	}
	return out
}

// shareLater presses P during the hand: the link must wait.
func shareLater(t *testing.T, s *Session, url string) {
	t.Helper()
	later, err := s.ShareShot(url)
	if err != nil || !later {
		t.Fatalf("ShareShot during a hand = %v, %v; want later", later, err)
	}
}

// checkPosted checks that the hand ended, that the result is in the log,
// and that the chat holds the waiting links in this order, all in one
// snapshot.
func checkPosted(t *testing.T, h *harness, viewer *Session, want ...ChatLine) {
	t.Helper()
	snap := h.snap(viewer)
	if snap.Table.InHand {
		t.Fatal("the hand did not end")
	}
	if !logHas(snap, " wins ") {
		t.Errorf("no result in the log of the same snapshot: %q", snap.Log)
	}
	got := shots(snap)
	if len(got) != len(want) {
		t.Fatalf("chat has %d shot lines, want %d: %+v", len(got), len(want), snap.Chat)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Shot != want[i].Shot || got[i].Text != "shared a screenshot" {
			t.Errorf("line %d = %+v, want %s with %s", i, got[i], want[i].Name, want[i].Shot)
		}
		if !got[i].At.Equal(h.clock.now()) {
			t.Errorf("line %d at %v, want the hand end %v", i, got[i].At, h.clock.now())
		}
	}
}

// noShots checks that no link is in the chat yet.
func noShots(t *testing.T, h *harness, viewer *Session) {
	t.Helper()
	if got := shots(h.snap(viewer)); len(got) != 0 {
		t.Fatalf("a link is in the chat during the hand: %+v", got)
	}
}

func TestShareShotNoHandPostsNow(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	later, err := alice.ShareShot(shotURL(0))
	if err != nil || later {
		t.Fatalf("ShareShot with no hand = %v, %v; want now", later, err)
	}
	got := shots(h.snap(bob))
	if len(got) != 1 || got[0].Name != "alice" || got[0].Text != "shared a screenshot" || got[0].Shot != shotURL(0) {
		t.Errorf("chat = %+v", h.snap(bob).Chat)
	}
}

// A showdown: the lines post with the result, in the order of the
// ShareShot calls.
func TestShareShotWaitsForShowdown(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(splitDeck))
	alice, bob := startHeadsUp(t, h)
	carol := h.connect("key:carol", "carol")
	shareLater(t, alice, shotURL(0))
	shareLater(t, bob, shotURL(1))
	h.clock.add(time.Second)
	shareLater(t, alice, shotURL(2))
	shareLater(t, carol, shotURL(3)) // a spectator too
	noShots(t, h, carol)
	h.playPassive(alice, bob)
	checkPosted(t, h, carol,
		ChatLine{Name: "alice", Shot: shotURL(0)},
		ChatLine{Name: "bob", Shot: shotURL(1)},
		ChatLine{Name: "alice", Shot: shotURL(2)},
		ChatLine{Name: "carol", Shot: shotURL(3)},
	)
	if h.snap(carol).Table.Result == nil {
		t.Error("no result in the snapshot")
	}
	// The next press, between hands, posts at once.
	if later, err := bob.ShareShot(shotURL(4)); err != nil || later {
		t.Errorf("ShareShot after the hand = %v, %v", later, err)
	}
	if n := len(shots(h.snap(carol))); n != 5 {
		t.Errorf("chat has %d shot lines, want 5", n)
	}
	// The next hand starts with no waiting link.
	h.nextHand(alice)
	h.inRun(func(r *Room) {
		if len(r.waitingShots) != 0 {
			t.Errorf("waiting links after the post: %+v", r.waitingShots)
		}
	})
}

func TestShareShotWaitsForFold(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	shareLater(t, bob, shotURL(0))
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	checkPosted(t, h, alice, ChatLine{Name: "bob", Shot: shotURL(0)})
}

// An all-in run-out is a live hand: the link waits for the river.
func TestShareShotWaitsForRunout(t *testing.T) {
	h := newHarness(t, runoutConfig(), stackedDeck(splitDeck))
	alice, bob := allInHeadsUp(t, h)
	checkPaused(t, h.snap(alice), 0)
	shareLater(t, alice, shotURL(0))
	h.advance(3 * time.Second)
	h.advance(3 * time.Second)
	checkPaused(t, h.snap(alice), 4)
	noShots(t, h, bob)
	shareLater(t, bob, shotURL(1))
	h.advance(3 * time.Second)
	checkPosted(t, h, bob,
		ChatLine{Name: "alice", Shot: shotURL(0)},
		ChatLine{Name: "bob", Shot: shotURL(1)},
	)
}

// The hand ends because a player stands up: the links post, also the
// link of the player who stood up.
func TestShareShotWaitsForStand(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	shareLater(t, bob, shotURL(0))
	shareLater(t, alice, shotURL(1))
	must(t, bob.Stand())
	checkPosted(t, h, alice,
		ChatLine{Name: "bob", Shot: shotURL(0)},
		ChatLine{Name: "alice", Shot: shotURL(1)},
	)
}

// The hand ends because a player leaves (SSH disconnect). The player who
// left keeps the waiting links, and the lines keep the name.
func TestShareShotWaitsAfterLeave(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	carol := h.connect("key:carol", "carol")
	shareLater(t, bob, shotURL(0))
	shareLater(t, alice, shotURL(1))
	// The player to act leaves: the room folds for that player at once,
	// and the hand ends.
	actor, other := h.toAct(alice, bob)
	actor.Close()
	if h.snap(other).Table.InHand {
		t.Fatal("the hand did not end after the leave")
	}
	checkPosted(t, h, carol,
		ChatLine{Name: "bob", Shot: shotURL(0)},
		ChatLine{Name: "alice", Shot: shotURL(1)},
	)
}

// The player who is not to act leaves; the other player plays on and the
// hand ends later. The link still posts.
func TestShareShotLeaveBeforeEnd(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	carol := h.connect("key:carol", "carol")
	actor, other := h.toAct(alice, bob)
	shareLater(t, other, shotURL(0))
	other.Close()
	if !h.snap(carol).Table.InHand {
		t.Fatal("the hand ended at the leave; this test needs a live hand")
	}
	noShots(t, h, carol)
	// The actor raises; the room folds for the player who left.
	must(t, actor.Act(engine.Action{Type: engine.Raise, Amount: 60}))
	checkPosted(t, h, carol, ChatLine{Name: other.name, Shot: shotURL(0)})
}

// B15-order: the lines post in the order in which the links reach the
// server, not in the order of the P presses. Alice (188x55) presses P
// first and Bob (80x24) 50 ms later; Bob's small picture is ready first,
// so his ShareShot comes first. The room has no press time, so Bob's line
// is first. Each player's own links keep their order. Between hands the
// same order holds: each line posts when its link arrives.
func TestShareShotOrderIsArrivalOrder(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	shareLater(t, bob, shotURL(1))   // pressed second, ready first
	shareLater(t, alice, shotURL(0)) // pressed first, ready second
	h.clock.add(shotEveryForTest)
	shareLater(t, alice, shotURL(2))
	shareLater(t, bob, shotURL(3))
	h.playPassive(alice, bob)
	checkPosted(t, h, bob,
		ChatLine{Name: "bob", Shot: shotURL(1)},
		ChatLine{Name: "alice", Shot: shotURL(0)},
		ChatLine{Name: "alice", Shot: shotURL(2)},
		ChatLine{Name: "bob", Shot: shotURL(3)},
	)
	// Between hands: the same rule, each line posts at once.
	for _, c := range []struct {
		s   *Session
		url string
	}{{bob, shotURL(5)}, {alice, shotURL(4)}} {
		if later, err := c.s.ShareShot(c.url); err != nil || later {
			t.Fatalf("ShareShot between hands = %v, %v", later, err)
		}
	}
	got := shots(h.snap(alice))
	if len(got) != 6 || got[4].Shot != shotURL(5) || got[5].Shot != shotURL(4) {
		t.Errorf("lines between hands = %+v; want bob's %s, then alice's %s", got, shotURL(5), shotURL(4))
	}
}

// shotEveryForTest is the time between two shots of one player in the
// TUI (shotEvery there).
const shotEveryForTest = 3 * time.Second

// The line keeps the name at the press, also after a rename.
func TestShareShotNameAtPress(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	shareLater(t, alice, shotURL(0))
	must(t, alice.SetName("Alicia"))
	h.playPassive(alice, bob)
	checkPosted(t, h, bob, ChatLine{Name: "alice", Shot: shotURL(0)})
}

// At most MaxWaitingShots links wait for each player. The cap does not
// use the rate limit, and it is free again after the hand.
func TestShareShotWaitingCap(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	for i := range MaxWaitingShots {
		shareLater(t, alice, shotURL(i))
	}
	for range 2 {
		if later, err := alice.ShareShot(shotURL(9)); !errors.Is(err, ErrShotsWaiting) || later {
			t.Fatalf("ShareShot over the cap = %v, %v; want ErrShotsWaiting", later, err)
		}
	}
	// The refused presses did not use the rate limit: one message is left.
	must(t, alice.Chat("gl"))
	// Another player has a cap of their own.
	shareLater(t, bob, shotURL(5))
	h.playPassive(alice, bob)
	checkPosted(t, h, bob,
		ChatLine{Name: "alice", Shot: shotURL(0)},
		ChatLine{Name: "alice", Shot: shotURL(1)},
		ChatLine{Name: "alice", Shot: shotURL(2)},
		ChatLine{Name: "bob", Shot: shotURL(5)},
	)
	h.nextHand(alice)
	h.clock.add(chatWindow)
	for i := range MaxWaitingShots {
		shareLater(t, alice, shotURL(i))
	}
}

// The press uses the chat rate limit. The post at the end of the hand
// does not: it posts also when the player has no message left.
func TestShareShotWaitingRateLimit(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, bob := startHeadsUp(t, h)
	for range chatBurst - 1 {
		must(t, alice.Chat("hi"))
	}
	shareLater(t, alice, shotURL(0))
	if later, err := alice.ShareShot(shotURL(1)); !errors.Is(err, errChatTooFast) || later {
		t.Fatalf("press over the rate limit = %v, %v", later, err)
	}
	h.playPassive(alice, bob)
	checkPosted(t, h, bob, ChatLine{Name: "alice", Shot: shotURL(0)})
}

func TestShareShotWaitingBadLink(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice, _ := startHeadsUp(t, h)
	if later, err := alice.ShareShot("https://x/abcdefghjk"); !errors.Is(err, ErrBadShot) || later {
		t.Errorf("bad link during a hand = %v, %v", later, err)
	}
	h.inRun(func(r *Room) {
		if len(r.waitingShots) != 0 {
			t.Errorf("a bad link waits: %+v", r.waitingShots)
		}
	})
}

// A server stop drops the waiting links: they are in memory only.
func TestShareShotWaitingDroppedOnStop(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	alice, bob := startHeadsUp(t, h)
	shareLater(t, alice, shotURL(0))
	noShots(t, h, bob)
	h.stop()
	for snap := range bob.Updates() {
		if len(shots(snap)) != 0 {
			t.Errorf("a link posted at the stop: %+v", snap.Chat)
		}
	}
	h2 := newHarness(t, cfg, nil)
	a2 := h2.connect("key:alice", "alice")
	if got := h2.snap(a2).Chat; len(got) != 0 {
		t.Errorf("chat after a restart = %+v", got)
	}
}
