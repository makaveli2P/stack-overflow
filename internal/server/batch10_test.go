package server

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 10: a limit of rabbit hunts for each account, and the private
// "you would have had" hand after a hunt.

func rabbitConfig(n int) Config {
	cfg := testConfig()
	cfg.RabbitHunts = n
	return cfg
}

// nextHand waits long enough for the next hand, also after a reveal hold.
func (h *harness) nextHand(s *Session) {
	h.t.Helper()
	h.advance(h.r.cfg.NextHandDelay + revealHold)
	if !h.snap(s).Table.InHand {
		h.t.Fatal("the next hand did not start")
	}
}

// foldHeadsUp makes the player to act fold, so that the hand ends with a
// rabbit hunt possible.
func foldHeadsUp(t *testing.T, h *harness, alice, bob *Session) {
	t.Helper()
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	if res := h.snap(alice).Table.Result; res == nil || !res.CanRabbit {
		t.Fatalf("no rabbit hunt possible: %+v", res)
	}
}

func TestRabbitHuntLimit(t *testing.T) {
	h := newHarness(t, rabbitConfig(2), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	if got := h.snap(alice).Me.RabbitsLeft; got != 2 {
		t.Fatalf("RabbitsLeft at the start = %d, want 2", got)
	}
	for want := 1; want >= 0; want-- {
		foldHeadsUp(t, h, alice, bob)
		must(t, alice.RabbitHunt())
		if got := h.snap(alice).Me.RabbitsLeft; got != want {
			t.Fatalf("RabbitsLeft = %d, want %d", got, want)
		}
		if got := h.snap(bob).Me.RabbitsLeft; got != 2 {
			t.Fatalf("bob RabbitsLeft = %d, want 2 (the count is per account)", got)
		}
		h.nextHand(alice)
	}
	foldHeadsUp(t, h, alice, bob)
	if err := alice.RabbitHunt(); !errors.Is(err, ErrNoRabbits) {
		t.Fatalf("third hunt = %v, want ErrNoRabbits", err)
	}
	if res := h.snap(alice).Table.Result; res == nil || len(res.Rabbit) != 0 || !res.CanRabbit {
		t.Fatalf("a refused hunt changed the result: %+v", res)
	}
	// The other account still has its own hunts.
	must(t, bob.RabbitHunt())
	if got := h.snap(bob).Me.RabbitsLeft; got != 1 {
		t.Errorf("bob RabbitsLeft = %d, want 1", got)
	}
	if got := h.snap(alice).Me.RabbitsLeft; got != 0 {
		t.Errorf("alice RabbitsLeft = %d, want 0", got)
	}
}

func TestRabbitHuntCountsOnlySuccess(t *testing.T) {
	h := newHarness(t, rabbitConfig(1), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	spectator := h.connect("key:carol", "carol")
	// During the hand: refused, not counted.
	if err := alice.RabbitHunt(); !errors.Is(err, engine.ErrNotNow) {
		t.Fatalf("hunt during the hand = %v, want ErrNotNow", err)
	}
	if err := spectator.RabbitHunt(); !errors.Is(err, engine.ErrNotSeated) {
		t.Fatalf("spectator hunt = %v, want ErrNotSeated", err)
	}
	if got := h.snap(alice).Me.RabbitsLeft; got != 1 {
		t.Fatalf("RabbitsLeft after a refused hunt = %d, want 1", got)
	}
	foldHeadsUp(t, h, alice, bob)
	must(t, bob.RabbitHunt())
	// The board is complete now: a second hunt is refused and not counted.
	if err := alice.RabbitHunt(); !errors.Is(err, engine.ErrNotNow) {
		t.Fatalf("second hunt of the hand = %v, want ErrNotNow", err)
	}
	if got := h.snap(alice).Me.RabbitsLeft; got != 1 {
		t.Errorf("alice RabbitsLeft = %d, want 1", got)
	}
	if got := h.snap(bob).Me.RabbitsLeft; got != 0 {
		t.Errorf("bob RabbitsLeft = %d, want 0", got)
	}
	if got := h.snap(spectator).Me.RabbitsLeft; got != 1 {
		t.Errorf("spectator RabbitsLeft = %d, want 1", got)
	}
}

func TestRabbitHuntReconnectKeepsCount(t *testing.T) {
	h := newHarness(t, rabbitConfig(1), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	foldHeadsUp(t, h, alice, bob)
	must(t, alice.RabbitHunt())
	alice.Close() // Between hands: the seat goes at once.

	back := h.connect(alice.ident, alice.name)
	if got := h.snap(back).Me.RabbitsLeft; got != 0 {
		t.Fatalf("after reconnect: RabbitsLeft = %d, want 0", got)
	}
	must(t, back.Sit(-1))
	h.nextHand(back)
	foldHeadsUp(t, h, back, bob)
	if err := back.RabbitHunt(); !errors.Is(err, ErrNoRabbits) {
		t.Errorf("hunt after reconnect = %v, want ErrNoRabbits", err)
	}
}

func TestRabbitHuntNoLimit(t *testing.T) {
	h := newHarness(t, rabbitConfig(0), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	for i := range 5 {
		if i > 0 {
			h.nextHand(alice)
		}
		foldHeadsUp(t, h, alice, bob)
		if err := alice.RabbitHunt(); err != nil {
			t.Fatalf("hunt %d: %v", i+1, err)
		}
		if got := h.snap(alice).Me.RabbitsLeft; got != 0 {
			t.Fatalf("RabbitsLeft with no limit = %d, want 0", got)
		}
	}
}

func TestRabbitHuntCountResetsOnNewRoom(t *testing.T) {
	cfg := rabbitConfig(3)
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	foldHeadsUp(t, h, alice, bob)
	must(t, alice.RabbitHunt())
	if got := h.snap(alice).Me.RabbitsLeft; got != 2 {
		t.Fatalf("RabbitsLeft = %d, want 2", got)
	}
	h.stop()
	h2 := newHarness(t, cfg, stackedDeck(bustDeck))
	again := h2.connect(alice.ident, alice.name)
	if got := h2.snap(again).Me.RabbitsLeft; got != 3 {
		t.Errorf("new Room: RabbitsLeft = %d, want 3", got)
	}
}

func TestNewRoomChecksRabbitHunts(t *testing.T) {
	for n, bad := range map[int]bool{-1: true, 0: false, 3: false} {
		_, err := NewRoom(rabbitConfig(n))
		if (err != nil) != bad || (bad && !errors.Is(err, engine.ErrBadConfig)) {
			t.Errorf("RabbitHunts %d: err = %v", n, err)
		}
	}
}

// wantRabbit computes the expected RabbitHand of hole with the result.
func wantRabbit(hole []cards.Card, res *engine.Result) RabbitHand {
	all := append(append(slices.Clone(hole), res.Board...), res.Rabbit...)
	hv, best := cards.Best(all)
	return RabbitHand{Category: hv.Category, Hand: hv.String(), Best: best[:]}
}

// rabbitFound is one RabbitHand value that the walk found in a snapshot.
type rabbitFound struct {
	path string
	val  RabbitHand
}

// walkRabbit collects every RabbitHand value in v that is not zero, with
// its path.
func walkRabbit(v reflect.Value, path string, out *[]rabbitFound) {
	rhType := reflect.TypeOf(RabbitHand{})
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkRabbit(v.Elem(), path, out)
		}
	case reflect.Struct:
		if v.Type() == rhType {
			if rh := v.Interface().(RabbitHand); rh.Hand != "" || rh.Category != 0 || len(rh.Best) > 0 {
				*out = append(*out, rabbitFound{path, rh})
			}
			return
		}
		for i := range v.NumField() {
			if f := v.Field(i); f.CanInterface() {
				walkRabbit(f, path+"."+v.Type().Field(i).Name, out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkRabbit(v.Index(i), path+"[]", out)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkRabbit(v.MapIndex(k), path+"[k]", out)
		}
	}
}

// TestRabbitHandPrivate: after a hunt, each player who was dealt in sees
// the own "would have had" hand: the winner, the folders, and a folder who
// stood up but is still connected. A player who sat down after the deal
// and a spectator see nothing. No snapshot ever holds the hand or the
// hole cards of another player, and the next deal clears it.
func TestRabbitHandPrivate(t *testing.T) {
	rec := recordSnaps(t)
	tb := newTable9(t, testConfig())
	h := tb.h
	holes := map[string][]cards.Card{}
	for _, s := range tb.players() {
		v := h.snap(s).Table
		holes[s.key] = slices.Clone(v.Seats[v.MySeat].Cards)
		if len(holes[s.key]) != 2 {
			t.Fatalf("%s cards %v", s.name, holes[s.key])
		}
	}
	late := h.connect("key:erin", "erin")
	must(t, late.Sit(-1)) // seated, but not dealt into this hand

	first, _ := h.toAct(tb.players()...)
	must(t, first.Act(engine.Action{Type: engine.Fold}))
	must(t, first.Stand()) // still connected
	var live []*Session
	for _, s := range tb.players() {
		if s != first {
			live = append(live, s)
		}
	}
	second, winner := h.toAct(live...)
	must(t, second.Act(engine.Action{Type: engine.Fold}))
	res := h.snap(winner).Table.Result
	if res == nil || !res.CanRabbit {
		t.Fatalf("result %+v", res)
	}
	everyone := append(tb.all(), late)
	for _, s := range everyone {
		if rh := h.snap(s).Me.RabbitHand; rh.Hand != "" || rh.Best != nil {
			t.Fatalf("%s has a RabbitHand before the hunt: %+v", s.name, rh)
		}
	}

	must(t, second.RabbitHunt())
	res = h.snap(winner).Table.Result
	if len(res.Board)+len(res.Rabbit) != 5 {
		t.Fatalf("board %v rabbit %v", res.Board, res.Rabbit)
	}
	want := map[string]RabbitHand{}
	for _, s := range tb.players() {
		want[s.key] = wantRabbit(holes[s.key], res)
		got := h.snap(s).Me.RabbitHand
		if !reflect.DeepEqual(got, want[s.key]) {
			t.Errorf("%s RabbitHand = %+v, want %+v", s.name, got, want[s.key])
		}
		if len(got.Best) != 5 || got.Hand == "" {
			t.Errorf("%s RabbitHand is not complete: %+v", s.name, got)
		}
	}
	for _, s := range []*Session{tb.dave, late} {
		if rh := h.snap(s).Me.RabbitHand; !reflect.DeepEqual(rh, RabbitHand{}) {
			t.Errorf("%s (not dealt in) RabbitHand = %+v, want zero", s.name, rh)
		}
	}
	// A reconnect of a player who was dealt in keeps the line.
	winner.Close() // between hands: the seat goes, the line stays
	back := h.connect(winner.ident, winner.name)
	if got := h.snap(back).Me.RabbitHand; !reflect.DeepEqual(got, want[winner.key]) {
		t.Errorf("after reconnect: RabbitHand = %+v, want %+v", got, want[winner.key])
	}
	must(t, back.Sit(-1))
	must(t, first.Sit(-1))

	// Every snapshot so far: a RabbitHand only in Me, only the owner's,
	// and no hole card of another player anywhere.
	recs := rec.all()
	owned := 0
	for _, rc := range recs {
		var found []rabbitFound
		walkRabbit(reflect.ValueOf(rc.snap), "Snapshot", &found)
		owned += len(found)
		for _, f := range found {
			if f.path != "Snapshot.Me.RabbitHand" {
				t.Fatalf("snapshot of %s has a RabbitHand at %s", rc.key, f.path)
			}
			if w, ok := want[rc.key]; !ok || !reflect.DeepEqual(f.val, w) {
				t.Fatalf("snapshot of %s has RabbitHand %+v, want %+v", rc.key, f.val, w)
			}
		}
		for key, hole := range holes {
			if key == rc.key {
				continue
			}
			if l := leak(rc.snap, hole); l != "" {
				t.Fatalf("snapshot of %s holds a card of %s: %s", rc.key, key, l)
			}
		}
	}

	if owned == 0 {
		t.Fatal("the walk found no RabbitHand at all")
	}

	// The next deal clears it for everyone.
	h.advance(testConfig().NextHandDelay + revealHold + time.Second)
	if !h.snap(back).Table.InHand {
		t.Fatal("the next hand did not start")
	}
	for _, s := range []*Session{back, first, second, tb.dave, late} {
		if rh := h.snap(s).Me.RabbitHand; !reflect.DeepEqual(rh, RabbitHand{}) {
			t.Errorf("%s RabbitHand after the next deal = %+v, want zero", s.name, rh)
		}
	}
	h.inRun(func(r *Room) {
		if r.holesHand != r.table.View("").HandNumber {
			t.Errorf("holesHand %d, hand %d", r.holesHand, r.table.View("").HandNumber)
		}
		for key := range r.holes {
			if _, ok := r.table.Stack(key); !ok {
				t.Errorf("holes keeps %s, who is not dealt into the new hand", key)
			}
		}
	})
}

// TestRabbitHandZeroWithoutHunt: a hand that ends without a hunt, and a
// hand with a showdown, give no RabbitHand.
func TestRabbitHandZeroWithoutHunt(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	h.playPassive(alice, bob)
	for _, s := range []*Session{alice, bob} {
		snap := h.snap(s)
		if snap.Table.Result == nil || snap.Table.Result.NoShowdown {
			t.Fatalf("no showdown result: %+v", snap.Table.Result)
		}
		if rh := snap.Me.RabbitHand; !reflect.DeepEqual(rh, RabbitHand{}) {
			t.Errorf("%s RabbitHand after a showdown = %+v", s.name, rh)
		}
	}
}

// TestRabbitHandStaleHoles: when the kept hole cards belong to another hand
// than the result, the room gives no RabbitHand.
func TestRabbitHandStaleHoles(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	foldHeadsUp(t, h, alice, bob)
	must(t, bob.RabbitHunt())
	if h.snap(alice).Me.RabbitHand.Hand == "" {
		t.Fatal("no RabbitHand after the hunt")
	}
	h.inRun(func(r *Room) {
		r.holesHand--
		r.broadcast()
	})
	if rh := h.snap(alice).Me.RabbitHand; !reflect.DeepEqual(rh, RabbitHand{}) {
		t.Errorf("RabbitHand with old hole cards = %+v, want zero", rh)
	}
}
