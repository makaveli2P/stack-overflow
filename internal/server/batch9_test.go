package server

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// Batch 9: a player who folds can store a show during the hand. The room
// shows those cards right after the hand ends, before the first snapshot
// of the result. Before that, nobody else may see them.

// cardText is a card as the log writes it ("A♠").
func cardText(c cards.Card) string { return c.Rank.String() + c.Suit.Symbol() }

// walkSnap collects every card and every string in v.
func walkSnap(v reflect.Value, cs *[]cards.Card, ss *[]string) {
	cardType := reflect.TypeOf(cards.Card{})
	timeType := reflect.TypeOf(time.Time{})
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkSnap(v.Elem(), cs, ss)
		}
	case reflect.Struct:
		switch v.Type() {
		case cardType:
			*cs = append(*cs, v.Interface().(cards.Card))
			return
		case timeType:
			return
		}
		for i := range v.NumField() {
			if f := v.Field(i); f.CanInterface() {
				walkSnap(f, cs, ss)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkSnap(v.Index(i), cs, ss)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkSnap(k, cs, ss)
			walkSnap(v.MapIndex(k), cs, ss)
		}
	case reflect.String:
		*ss = append(*ss, v.String())
	}
}

// leak returns a description of the first place where snap holds a card
// of hole (as a card value anywhere, or as card text in any string), or "".
func leak(snap Snapshot, hole []cards.Card) string {
	var cs []cards.Card
	var ss []string
	walkSnap(reflect.ValueOf(snap), &cs, &ss)
	for _, c := range cs {
		if slices.Contains(hole, c) {
			return "card value " + cardText(c)
		}
	}
	for _, s := range ss {
		for _, c := range hole {
			if strings.Contains(s, cardText(c)) {
				return "text " + cardText(c) + " in " + s
			}
		}
	}
	return ""
}

// recorder keeps every snapshot that the room sends (pushHook).
type recorder struct {
	mu    sync.Mutex
	snaps []recorded
}

type recorded struct {
	key  string
	snap Snapshot
}

func recordSnaps(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	pushHook = func(s *Session, snap Snapshot) {
		rec.mu.Lock()
		rec.snaps = append(rec.snaps, recorded{s.key, snap})
		rec.mu.Unlock()
	}
	t.Cleanup(func() { pushHook = nil })
	return rec
}

func (rec *recorder) all() []recorded {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.snaps)
}

// table9 seats alice, bob and carol, connects the spectator dave and deals
// the first hand. The deck is fixed.
type table9 struct {
	h                       *harness
	alice, bob, carol, dave *Session
}

func newTable9(t *testing.T, cfg Config) *table9 {
	t.Helper()
	h := newHarness(t, cfg, stackedDeck(""))
	tb := &table9{h: h}
	tb.alice = h.connect("key:alice", "alice")
	tb.bob = h.connect("key:bob", "bob")
	tb.carol = h.connect("key:carol", "carol")
	tb.dave = h.connect("key:dave", "dave")
	for _, s := range tb.players() {
		must(t, s.Sit(-1))
	}
	h.advance(cfg.NextHandDelay)
	if !h.snap(tb.alice).Table.InHand {
		t.Fatal("hand did not start")
	}
	return tb
}

func (tb *table9) players() []*Session { return []*Session{tb.alice, tb.bob, tb.carol} }
func (tb *table9) all() []*Session     { return []*Session{tb.alice, tb.bob, tb.carol, tb.dave} }

// fold makes the first player to act fold. It returns that player, the
// hole cards as the player sees them, and the two other players.
func (tb *table9) fold(t *testing.T) (*Session, []cards.Card, []*Session) {
	t.Helper()
	folder, _ := tb.h.toAct(tb.players()...)
	must(t, folder.Act(engine.Action{Type: engine.Fold}))
	v := tb.h.snap(folder).Table
	hole := slices.Clone(v.Seats[v.MySeat].Cards)
	if len(hole) != 2 {
		t.Fatalf("folder cards %v", hole)
	}
	var rest []*Session
	for _, s := range tb.players() {
		if s != folder {
			rest = append(rest, s)
		}
	}
	return folder, hole, rest
}

// wantNoLeak fails when a session other than folder sees a card of hole.
func (tb *table9) wantNoLeak(t *testing.T, folder *Session, hole []cards.Card) {
	t.Helper()
	for _, s := range tb.all() {
		if s == folder || s.closed.Load() {
			continue
		}
		if l := leak(tb.h.snap(s), hole); l != "" {
			t.Fatalf("%s sees a card of %s: %s", s.name, folder.name, l)
		}
	}
}

func revealedOf(res *engine.Result, key string) (engine.Shown, bool) {
	if res == nil {
		return engine.Shown{}, false
	}
	for _, sh := range res.Revealed {
		if sh.PlayerID == key {
			return sh, true
		}
	}
	return engine.Shown{}, false
}

func logIndex(snap Snapshot, sub string) int {
	return slices.IndexFunc(snap.Log, func(l string) bool { return strings.Contains(l, sub) })
}

// TestShowAtEndQueuedThenShown: a folded player stores a show of both
// cards. Nobody else sees them until the result; the first snapshot of the
// result already holds them, and the log has the show line right after the
// result lines.
func TestShowAtEndQueuedThenShown(t *testing.T) {
	rec := recordSnaps(t)
	tb := newTable9(t, testConfig())
	h := tb.h
	folder, hole, rest := tb.fold(t)
	tb.wantNoLeak(t, folder, hole)

	must(t, folder.ShowCards(true, true))
	if got := h.snap(folder).Me.ShowAtEnd; got != [2]bool{true, true} {
		t.Fatalf("ShowAtEnd %v, want both", got)
	}
	for _, s := range tb.all() {
		if s != folder && h.snap(s).Me.ShowAtEnd != [2]bool{} {
			t.Fatalf("%s has ShowAtEnd %v", s.name, h.snap(s).Me.ShowAtEnd)
		}
	}
	tb.wantNoLeak(t, folder, hole)
	h.playPassive(rest...)

	snap := h.snap(tb.dave)
	res := snap.Table.Result
	if snap.Table.InHand || res == nil || res.NoShowdown {
		t.Fatalf("no showdown result: %+v", res)
	}
	sh, ok := revealedOf(res, folder.key)
	if !ok || !slices.Equal(sh.Cards, hole) {
		t.Fatalf("Revealed %+v, want %v of %s", res.Revealed, hole, folder.name)
	}
	if sv := seatOf(snap.Table, folder.key); !slices.Equal(sv.Cards, hole) {
		t.Fatalf("dave sees %v at the folder's seat", sv.Cards)
	}
	line := folder.name + " shows " + cardText(hole[0]) + " " + cardText(hole[1])
	show, end := logIndex(snap, line), logIndex(snap, "Hand #1 ends")
	if show < 0 || end < 0 || show != end+1 {
		t.Fatalf("show line at %d, hand end at %d: %q", show, end, snap.Log)
	}
	if got := h.snap(folder).Me.ShowAtEnd; got != [2]bool{} {
		t.Fatalf("ShowAtEnd %v after the hand", got)
	}
	for _, p := range tb.players() {
		if err := p.ShowCards(true, true); p != folder && !errors.Is(err, engine.ErrNotNow) {
			t.Errorf("%s after a showdown: %v", p.name, err)
		}
	}

	// Every snapshot that another session got: no folded card during the
	// hand, and every result snapshot of hand 1 holds the show.
	results := 0
	for _, r := range rec.all() {
		if r.key == folder.key {
			continue
		}
		v := r.snap.Table
		if v.HandNumber != 1 {
			continue
		}
		if v.InHand || v.Result == nil {
			if l := leak(r.snap, hole); l != "" {
				t.Fatalf("%s saw %s during the hand", r.key, l)
			}
			continue
		}
		results++
		if _, ok := revealedOf(v.Result, folder.key); !ok {
			t.Fatalf("a result snapshot to %s has no show: %+v", r.key, v.Result.Revealed)
		}
	}
	if results == 0 {
		t.Fatal("no result snapshot")
	}
	// The stats rows do not change for a show.
	for _, row := range snap.Stats {
		for _, c := range append(slices.Clone(row.BestCards), row.WorstBeatWith...) {
			if slices.Contains(hole, c) {
				t.Errorf("stats row %s holds %v", row.Name, c)
			}
		}
	}
}

// TestShowAtEndCancelAndChange: the same choice again cancels; another
// choice replaces it; no card cancels. After a cancel nothing shows, and
// the folded player can still show after the hand.
func TestShowAtEndCancelAndChange(t *testing.T) {
	tb := newTable9(t, testConfig())
	h := tb.h
	folder, hole, rest := tb.fold(t)
	steps := []struct {
		left, right bool
		want        [2]bool
	}{
		{true, false, [2]bool{true, false}},
		{true, false, [2]bool{}},
		{false, true, [2]bool{false, true}},
		{true, true, [2]bool{true, true}},
		{true, true, [2]bool{}},
		{true, false, [2]bool{true, false}},
		{false, false, [2]bool{}},
		{false, false, [2]bool{}},
	}
	for i, st := range steps {
		must(t, folder.ShowCards(st.left, st.right))
		if got := h.snap(folder).Me.ShowAtEnd; got != st.want {
			t.Fatalf("step %d: ShowAtEnd %v, want %v", i, got, st.want)
		}
		tb.wantNoLeak(t, folder, hole)
	}
	h.playPassive(rest...)
	snap := h.snap(tb.dave)
	if _, ok := revealedOf(snap.Table.Result, folder.key); ok || logHas(snap, folder.name+" shows") {
		t.Fatalf("a cancelled show showed: %+v %q", snap.Table.Result.Revealed, snap.Log)
	}
	tb.wantNoLeak(t, folder, hole)

	// After the hand the folded player shows at once (the engine path).
	must(t, folder.ShowCards(false, true))
	snap = h.snap(tb.dave)
	if sh, ok := revealedOf(snap.Table.Result, folder.key); !ok || !slices.Equal(sh.Cards, hole[1:]) {
		t.Fatalf("Revealed %+v", snap.Table.Result.Revealed)
	}
	if !logHas(snap, folder.name+" shows "+cardText(hole[1])) {
		t.Fatalf("no show line: %q", snap.Log)
	}
	if want := snap.Now.Add(revealHold); snap.NextHandAt.Before(want) {
		t.Errorf("next hand at %v, want >= %v", snap.NextHandAt, want)
	}
}

// TestShowAtEndNotFolded: during a hand, only a seated player who folded
// in this hand can store a show.
func TestShowAtEndNotFolded(t *testing.T) {
	tb := newTable9(t, testConfig())
	h := tb.h
	erin := h.connect("key:erin", "erin")
	must(t, erin.Sit(-1)) // not dealt in
	for _, s := range append(tb.all(), erin) {
		for _, lr := range [][2]bool{{true, true}, {true, false}, {false, true}, {false, false}} {
			if err := s.ShowCards(lr[0], lr[1]); !errors.Is(err, engine.ErrNotNow) {
				t.Fatalf("%s ShowCards%v before any fold: %v", s.name, lr, err)
			}
		}
	}
	folder, hole, rest := tb.fold(t)
	for _, s := range append(append(rest, tb.dave), erin) {
		if err := s.ShowCards(true, true); !errors.Is(err, engine.ErrNotNow) {
			t.Fatalf("%s ShowCards during the hand: %v", s.name, err)
		}
		if got := h.snap(s).Me.ShowAtEnd; got != [2]bool{} {
			t.Fatalf("%s ShowAtEnd %v", s.name, got)
		}
	}
	tb.wantNoLeak(t, folder, hole)
}

// TestShowAtEndStandClears: a stand drops the stored show. A return to a
// seat in the same hand cannot store a new one.
func TestShowAtEndStandClears(t *testing.T) {
	tb := newTable9(t, testConfig())
	h := tb.h
	folder, hole, rest := tb.fold(t)
	must(t, folder.ShowCards(true, true))
	must(t, folder.Stand())
	if got := h.snap(folder).Me.ShowAtEnd; got != [2]bool{} {
		t.Fatalf("ShowAtEnd %v after the stand", got)
	}
	must(t, folder.Sit(-1))
	if err := folder.ShowCards(true, true); !errors.Is(err, engine.ErrNotNow) {
		t.Fatalf("ShowCards after a new sit: %v", err)
	}
	h.playPassive(rest...)
	snap := h.snap(tb.dave)
	if _, ok := revealedOf(snap.Table.Result, folder.key); ok || logHas(snap, folder.name+" shows") {
		t.Fatalf("a dropped show showed: %q", snap.Log)
	}
	tb.wantNoLeak(t, folder, hole)
	h.inRun(func(r *Room) {
		if len(r.showAtEnd) != 0 {
			t.Errorf("showAtEnd %v", r.showAtEnd)
		}
	})
}

// TestShowAtEndDisconnectKeeps: a disconnect keeps the choice until the
// hand ends; the show comes before the stand-up of the absent player.
func TestShowAtEndDisconnectKeeps(t *testing.T) {
	tb := newTable9(t, testConfig())
	h := tb.h
	folder, hole, rest := tb.fold(t)
	must(t, folder.ShowCards(true, false))
	folder.Close()
	tb.wantNoLeak(t, folder, hole)
	h.playPassive(rest...)
	snap := h.snap(tb.dave)
	if sh, ok := revealedOf(snap.Table.Result, folder.key); !ok || !slices.Equal(sh.Cards, hole[:1]) {
		t.Fatalf("Revealed %+v", snap.Table.Result.Revealed)
	}
	show, stand := logIndex(snap, folder.name+" shows "+cardText(hole[0])), logIndex(snap, folder.name+" stands up")
	if show < 0 || stand < 0 || show > stand {
		t.Fatalf("show line %d, stand line %d: %q", show, stand, snap.Log)
	}
	if strings.Contains(strings.Join(snap.Log, "\n"), cardText(hole[1])) {
		t.Fatalf("the log names the card that was not shown: %q", snap.Log)
	}
}

// TestShowAtEndNoShowdown: the hand ends because everyone else folds. The
// stored show of the first folder applies; the winner can still show, and
// both entries stay.
func TestShowAtEndNoShowdown(t *testing.T) {
	tb := newTable9(t, testConfig())
	h := tb.h
	folder, hole, rest := tb.fold(t)
	must(t, folder.ShowCards(false, true))
	actor, winner := h.toAct(rest...)
	tb.wantNoLeak(t, folder, hole)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	snap := h.snap(tb.dave)
	res := snap.Table.Result
	if res == nil || !res.NoShowdown {
		t.Fatalf("result %+v", res)
	}
	if sh, ok := revealedOf(res, folder.key); !ok || !slices.Equal(sh.Cards, hole[1:]) {
		t.Fatalf("Revealed %+v", res.Revealed)
	}
	must(t, winner.ShowCards(true, true))
	must(t, folder.ShowCards(true, false)) // the other card, after the hand
	res = h.snap(tb.dave).Table.Result
	if len(res.Revealed) != 2 || res.Revealed[0].PlayerID != folder.key || len(res.Revealed[0].Cards) != 2 ||
		res.Revealed[1].PlayerID != winner.key || len(res.Revealed[1].Cards) != 2 {
		t.Fatalf("Revealed %+v", res.Revealed)
	}
}

// TestShowAtEndRunout: a folded player stores a show during a paused
// run-out. It applies only after the river and the result.
func TestShowAtEndRunout(t *testing.T) {
	rec := recordSnaps(t)
	cfg := runoutConfig()
	tb := newTable9(t, cfg)
	h := tb.h
	folder, hole, rest := tb.fold(t)
	actor, other := h.toAct(rest...)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	if v := h.snap(tb.dave).Table; !v.InHand || !v.Runout {
		t.Fatalf("no run-out: InHand %v Runout %v", v.InHand, v.Runout)
	}
	must(t, folder.ShowCards(true, true))
	for street := range 3 {
		if got := h.snap(folder).Me.ShowAtEnd; got != [2]bool{true, true} {
			t.Fatalf("street %d: ShowAtEnd %v", street, got)
		}
		tb.wantNoLeak(t, folder, hole)
		if v := h.snap(tb.dave).Table; !v.InHand || v.Result != nil {
			t.Fatalf("street %d: the hand ended early", street)
		}
		h.advance(cfg.RunoutDelay)
	}
	snap := h.snap(tb.dave)
	if snap.Table.InHand || len(snap.Table.Board) != 5 {
		t.Fatalf("the run-out did not end: board %v", snap.Table.Board)
	}
	if sh, ok := revealedOf(snap.Table.Result, folder.key); !ok || !slices.Equal(sh.Cards, hole) {
		t.Fatalf("Revealed %+v", snap.Table.Result.Revealed)
	}
	river := slices.IndexFunc(snap.Log, func(l string) bool { return strings.HasPrefix(l, "River") })
	show := logIndex(snap, folder.name+" shows "+cardText(hole[0]))
	if river < 0 || show < river {
		t.Fatalf("river line %d, show line %d: %q", river, show, snap.Log)
	}
	for _, r := range rec.all() {
		v := r.snap.Table
		if r.key != folder.key && v.HandNumber == 1 && (v.InHand || v.Result == nil) {
			if l := leak(r.snap, hole); l != "" {
				t.Fatalf("%s saw %s during the run-out", r.key, l)
			}
		}
	}
}

// TestShowAtEndHoldsNextHand: a show at the end of a hand keeps the next
// deal at least revealHold away, also with a short pause.
func TestShowAtEndHoldsNextHand(t *testing.T) {
	cfg := testConfig()
	cfg.NextHandDelay = time.Second
	tb := newTable9(t, cfg)
	h := tb.h
	folder, _, rest := tb.fold(t)
	must(t, folder.ShowCards(true, true))
	h.playPassive(rest...)
	snap := h.snap(tb.dave)
	if want := snap.Now.Add(revealHold); snap.NextHandAt.Before(want) {
		t.Fatalf("next hand at %v, want >= %v", snap.NextHandAt, want)
	}
	// The next deal clears the choice store.
	h.advance(revealHold)
	if !h.snap(tb.dave).Table.InHand {
		t.Fatal("the next hand did not start")
	}
	if got := h.snap(folder).Me.ShowAtEnd; got != [2]bool{} {
		t.Fatalf("ShowAtEnd %v in the next hand", got)
	}
}
