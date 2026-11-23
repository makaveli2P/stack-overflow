package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
	"golang.org/x/crypto/bcrypt"
)

func (h *harness) connectWith(key, name string, o ConnectOptions) *Session {
	h.t.Helper()
	s, err := h.r.ConnectWith(key, name, o)
	if err != nil {
		h.t.Fatalf("ConnectWith(%q, %q): %v", key, name, err)
	}
	return s
}

// inRun runs fn on the Run goroutine.
func (h *harness) inRun(fn func(r *Room)) {
	h.t.Helper()
	if err := h.r.do(func() error { fn(h.r); return nil }); err != nil {
		h.t.Fatal(err)
	}
}

func statRow(snap Snapshot, name string) (PlayerStats, bool) {
	for _, row := range snap.Stats {
		if row.Name == name {
			return row, true
		}
	}
	return PlayerStats{}, false
}

func TestConnectWithNewKey(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	if h.r.KnownKey("SHA256:aaa") {
		t.Fatal("unknown key is known")
	}
	a := h.connectWith("key:SHA256:aaa", "Alice Smith!", ConnectOptions{})
	me := h.snap(a).Me
	want := Me{Key: "key:SHA256:aaa", Name: "Alice Smith", Seat: -1, NeedsName: true, CanLink: true, Alerts: true}
	if !reflect.DeepEqual(me, want) {
		t.Errorf("Me = %+v\nwant %+v", me, want)
	}
	for _, fp := range []string{"SHA256:aaa", "key:SHA256:aaa"} {
		if !h.r.KnownKey(fp) {
			t.Errorf("KnownKey(%q) = false", fp)
		}
	}
	must(t, a.SetName("Ally"))
	if me := h.snap(a).Me; me.NeedsName || me.Name != "Ally" {
		t.Errorf("after SetName: %+v", me)
	}
	if !logHas(h.snap(a), "Alice Smith is now Ally") {
		t.Error("log has no rename line")
	}
	b := h.connectWith("key:SHA256:bbb", "ally", ConnectOptions{})
	if got := h.snap(b).Me.Name; got != "ally2" {
		t.Errorf("suggested name = %q, want ally2", got)
	}
	if err := b.SetName("ALLY"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("SetName to a used name: %v", err)
	}
	if err := b.SetName("bad@name"); !errors.Is(err, ErrBadName) {
		t.Errorf("SetName bad: %v", err)
	}
	must(t, a.SetName("ALLY")) // A new case of the own name is fine.
	if _, err := h.r.ConnectWith("pin:nobody", "nobody", ConnectOptions{}); !errors.Is(err, ErrNoAccount) {
		t.Errorf("ConnectWith unknown PIN login: %v", err)
	}
	// The same key connects again: same account, the old session closes.
	a2 := h.connectWith("key:SHA256:aaa", "whatever", ConnectOptions{})
	for range a.Updates() {
	}
	if me := h.snap(a2).Me; me.Name != "ALLY" || me.NeedsName {
		t.Errorf("reconnect: %+v", me)
	}
}

func TestNamesAreUniqueAcrossOfflineAccounts(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	a := h.connectWith("key:a", "alice", ConnectOptions{})
	must(t, a.SetName("Zoë"))
	a.Close()
	b := h.connectWith("key:b", "bob", ConnectOptions{})
	for _, n := range []string{"zoë", "ZOË", "Zoe\u0308"} {
		if err := b.SetName(n); !errors.Is(err, ErrNameInUse) {
			t.Errorf("SetName(%q) while an offline account has Zoë: %v", n, err)
		}
	}
	if _, err := h.r.Connect("key:c", "ZOË"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("legacy Connect with an offline account's name: %v", err)
	}
}

func TestPINAccounts(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	if h.r.PINAccountExists("bob") {
		t.Fatal("bob exists")
	}
	if err := h.r.CreatePINAccount("Bob", "12a4"); !errors.Is(err, ErrPINFormat) {
		t.Errorf("bad PIN: %v", err)
	}
	if err := h.r.CreatePINAccount("", "4826"); !errors.Is(err, ErrBadName) {
		t.Errorf("empty login: %v", err)
	}
	must(t, h.r.CreatePINAccount("Bob", "4826"))
	if err := h.r.CreatePINAccount("bob", "7316"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("second create: %v", err)
	}
	if !h.r.PINAccountExists(" BOB ") {
		t.Error("PINAccountExists(BOB) = false")
	}
	must(t, h.r.CheckPIN("bob", "4826"))
	if err := h.r.CheckPIN("bob", "6284"); !errors.Is(err, ErrBadPIN) {
		t.Errorf("wrong PIN: %v", err)
	}
	if err := h.r.CheckPIN("carol", "4826"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("no account: %v", err)
	}
	h.inRun(func(r *Room) {
		_, a := r.st.byLogin("bob")
		if !strings.HasPrefix(a.PINHash, "$2") || strings.Contains(a.PINHash, "4826") {
			t.Errorf("PIN hash %q", a.PINHash)
		}
	})
	s := h.connectWith("pin:bob", "Bob", ConnectOptions{})
	me := h.snap(s).Me
	if !me.Keyless || me.CanLink || !me.NeedsName || me.Name != "bob" || me.Key != "pin:bob" {
		t.Errorf("PIN session Me = %+v", me)
	}
}

func TestPINLockout(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("bob", "4826"))
	must(t, h.r.CreatePINAccount("carol", "4826"))
	for i := range 5 {
		if err := h.r.CheckPIN("bob", "0000"); !errors.Is(err, ErrBadPIN) {
			t.Fatalf("wrong PIN %d: %v", i+1, err)
		}
	}
	if err := h.r.CheckPIN("bob", "4826"); !errors.Is(err, ErrPINLocked) {
		t.Errorf("right PIN while locked: %v", err)
	}
	must(t, h.r.CheckPIN("carol", "4826")) // Another login is not locked.
	h.clock.add(9 * time.Minute)
	if err := h.r.CheckPIN("bob", "4826"); !errors.Is(err, ErrPINLocked) {
		t.Errorf("after 9 minutes: %v", err)
	}
	h.clock.add(time.Minute)
	must(t, h.r.CheckPIN("bob", "4826"))

	// Wrong PINs spread over more than 10 minutes do not lock.
	for range 4 {
		if err := h.r.CheckPIN("carol", "0000"); !errors.Is(err, ErrBadPIN) {
			t.Fatal(err)
		}
	}
	h.clock.add(10 * time.Minute)
	if err := h.r.CheckPIN("carol", "0000"); !errors.Is(err, ErrBadPIN) {
		t.Errorf("fifth wrong PIN after the window: %v", err)
	}
	must(t, h.r.CheckPIN("carol", "4826"))
}

// Many parallel wrong PINs must not get more than 5 bcrypt checks through.
func TestPINLockoutParallel(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("bob", "4826"))
	errs := make(chan error, 40)
	for i := range 40 {
		go func() { errs <- h.r.CheckPIN("bob", "00"+string(rune('0'+i%10))+"1") }()
	}
	bad := 0
	for range 40 {
		switch err := <-errs; {
		case errors.Is(err, ErrBadPIN):
			bad++
		case errors.Is(err, ErrPINLocked):
		default:
			t.Errorf("err = %v", err)
		}
	}
	if bad > pinMaxFails {
		t.Errorf("%d wrong PINs were checked, want at most %d", bad, pinMaxFails)
	}
	if err := h.r.CheckPIN("bob", "4826"); !errors.Is(err, ErrPINLocked) {
		t.Errorf("after the burst: %v", err)
	}
}

func TestPINHashCostIsTen(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	r, err := NewRoom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	must(t, r.CreatePINAccount("bob", "4826"))
	cancel()
	<-done
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range saved {
		if cost, err := bcrypt.Cost([]byte(a.PINHash)); err != nil || cost != 10 {
			t.Errorf("bcrypt cost = %d, %v; want 10", cost, err)
		}
	}
}

func TestAuthCallsAfterRunReturns(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("bob", "4826"))
	h.connectWith("key:k", "k", ConnectOptions{})
	h.stop()
	if h.r.KnownKey("k") || h.r.PINAccountExists("bob") {
		t.Error("lookups after Run returned must be false")
	}
	if err := h.r.CheckPIN("bob", "4826"); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("CheckPIN: %v", err)
	}
	if err := h.r.CreatePINAccount("carol", "4826"); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("CreatePINAccount: %v", err)
	}
	if _, err := h.r.ConnectWith("key:k", "k", ConnectOptions{}); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("ConnectWith: %v", err)
	}
}

func TestLinkPIN(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("bob", "4826"))
	old := h.connectWith("pin:bob", "bob", ConnectOptions{})
	must(t, old.SetName("Bobby"))
	must(t, old.Sit(-1))
	must(t, old.Stand()) // Bobby now has a bank of 1000 and one buy-in.
	if err := old.LinkPIN("bob", "4826"); !errors.Is(err, ErrCannotLink) {
		t.Errorf("link from a PIN session: %v", err)
	}

	k := h.connectWith("key:SHA256:new", "laptop", ConnectOptions{})
	if !h.snap(k).Me.CanLink {
		t.Fatal("fresh key cannot link")
	}
	if err := k.LinkPIN("bob", "0000"); !errors.Is(err, ErrBadPIN) {
		t.Errorf("wrong PIN: %v", err)
	}
	if err := k.LinkPIN("nobody", "4826"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown login: %v", err)
	}
	must(t, k.LinkPIN("BOB", "4826"))
	me := h.snap(k).Me
	if me.Name != "Bobby" || me.Bank != 1000 || me.CanLink || me.Keyless || me.NeedsName {
		t.Errorf("after link: %+v", me)
	}
	// The PIN session of the same account closed.
	for range old.Updates() {
	}
	if err := old.Chat("hi"); !errors.Is(err, ErrClosed) {
		t.Errorf("old PIN session: %v", err)
	}
	h.inRun(func(r *Room) {
		if n := len(r.st.Accounts); n != 1 {
			t.Errorf("%d accounts after the link, want 1 (the empty key account goes)", n)
		}
	})
	k.Close()
	k2 := h.connectWith("key:SHA256:new", "laptop", ConnectOptions{})
	if me := h.snap(k2).Me; me.Name != "Bobby" || me.Bank != 1000 {
		t.Errorf("key login after the link: %+v", me)
	}

	// A key whose account has history cannot link.
	c := h.connectWith("key:SHA256:carol", "carol", ConnectOptions{})
	must(t, c.Sit(-1))
	if h.snap(c).Me.CanLink {
		t.Error("seated key can link")
	}
	if err := c.LinkPIN("bob", "4826"); !errors.Is(err, ErrCannotLink) {
		t.Errorf("link with history: %v", err)
	}
}

func TestSetAlertsPersists(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	a := h.connectWith("key:a", "alice", ConnectOptions{})
	must(t, a.SetAlerts(false))
	if h.snap(a).Me.Alerts {
		t.Error("alerts still on")
	}
	h.stop()
	h2 := newHarness(t, cfg, nil)
	a = h2.connectWith("key:a", "alice", ConnectOptions{})
	if h2.snap(a).Me.Alerts {
		t.Error("alerts on after restart")
	}
	must(t, a.SetAlerts(true))
	if !h2.snap(a).Me.Alerts {
		t.Error("alerts off after SetAlerts(true)")
	}
}

func TestAlertsAreOneShot(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	if got := h.snap(actor).Alert; got != "turn" {
		t.Errorf("actor first snapshot Alert = %q, want turn", got)
	}
	if got := h.snap(other).Alert; got != "" {
		t.Errorf("other Alert = %q", got)
	}
	must(t, other.Chat("hi"))
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("second snapshot Alert = %q, want empty", got)
	}
	h.advance(24 * time.Second) // 6 s left
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("6 s left: Alert = %q", got)
	}
	h.advance(time.Second) // 5 s left
	if got := h.snap(actor).Alert; got != "hurry" {
		t.Errorf("5 s left: Alert = %q, want hurry", got)
	}
	h.advance(time.Second)
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("4 s left: Alert = %q", got)
	}
	must(t, actor.Act(engine.Action{Type: engine.Call}))
	if got := h.snap(other).Alert; got != "turn" {
		t.Errorf("other after call: Alert = %q, want turn", got)
	}
	if got := h.snap(actor).Alert; got != "" {
		t.Errorf("actor after call: Alert = %q", got)
	}
}

// An alert in a snapshot that the client did not read moves to the newer
// snapshot, so the cue is never lost.
func TestAlertSurvivesDroppedSnapshot(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(h.r.cfg.NextHandDelay)
	must(t, alice.Chat("one"))
	must(t, bob.Chat("two"))
	var actor *Session
	h.inRun(func(r *Room) {
		id, _ := r.table.ToAct()
		actor = r.sessions[id]
	})
	snap := <-actor.Updates()
	if snap.Alert != "turn" || len(snap.Chat) != 2 {
		t.Errorf("newest snapshot: Alert %q, chat %d lines", snap.Alert, len(snap.Chat))
	}
}

func TestMarkSettled(t *testing.T) {
	cfg := testConfig()
	cfg.Admins = []string{testFP("carol")}
	cfg.ChipsPerUnit = 2
	cfg.Currency = "₹"
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice := h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	bob := h.connectWith("key:bob", "bob", ConnectOptions{})
	carol := h.connectWith("key:"+testFP("carol"), "carol", ConnectOptions{})
	if !h.snap(alice).Me.Admin || h.snap(bob).Me.Admin || !h.snap(carol).Me.Admin {
		t.Errorf("Admin flags: alice %v bob %v carol %v", h.snap(alice).Me.Admin, h.snap(bob).Me.Admin, h.snap(carol).Me.Admin)
	}
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	h.advance(cfg.NextHandDelay)
	if err := alice.MarkSettled(); !errors.Is(err, ErrHandLive) {
		t.Errorf("during a hand: %v", err)
	}
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	if err := bob.MarkSettled(); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("bob: %v", err)
	}
	snap := h.snap(alice)
	if len(snap.Settle.Balances) != 2 || len(snap.Settle.Transfers) != 1 || snap.Settle.Period != 1 {
		t.Fatalf("settle before: %+v", snap.Settle)
	}
	tr := snap.Settle.Transfers[0]
	if tr.Amount != 500 { // 10 chips at 2 chips per rupee
		t.Errorf("transfer %+v, want ₹5.00", tr)
	}
	loser := tr.From
	h.clock.add(time.Hour)
	must(t, carol.MarkSettled())
	snap = h.snap(alice)
	if snap.Me.Seat != -1 || snap.Me.Bank != 0 || h.snap(bob).Me.Seat != -1 {
		t.Errorf("after settle: alice %+v bob %+v", snap.Me, h.snap(bob).Me)
	}
	s := snap.Settle
	if s.Period != 2 || !s.Since.Equal(h.clock.now()) || len(s.Balances) != 0 || len(s.Transfers) != 0 {
		t.Errorf("settle after: %+v", s)
	}
	if len(s.History) != 1 || s.History[0].Period != 1 || len(s.History[0].Transfers) != 1 || s.History[0].Transfers[0] != tr || s.History[0].Currency != "₹" {
		t.Errorf("history: %+v", s.History)
	}
	row, ok := statRow(snap, loser)
	if !ok || row.PeriodNet != 0 || row.LifetimeNet != -10 || row.BuyIns != 1 || row.PeriodBuyIns != 0 || row.Hands != 1 || row.PeriodHands != 0 {
		t.Errorf("loser stats after settle: %+v", row)
	}
	if !logHas(snap, "carol marks period 1 settled") {
		t.Error("log has no settle line")
	}
	// A new period: sitting buys in again.
	must(t, bob.Sit(-1))
	if row, _ := statRow(h.snap(bob), "bob"); row.PeriodBuyIns != 1 || row.BuyIns != 2 {
		t.Errorf("bob after a new buy-in: %+v", row)
	}
}

func TestStatsFromShowdown(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, other := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	snap := h.snap(alice)
	if snap.Table.InHand {
		t.Fatal("hand did not end")
	}
	if len(snap.Stats) != 2 {
		t.Fatalf("stats rows: %+v", snap.Stats)
	}
	win, lose := snap.Stats[0], snap.Stats[1]
	if win.PeriodNet != 1000 || lose.PeriodNet != -1000 {
		t.Errorf("nets: %d %d", win.PeriodNet, lose.PeriodNet)
	}
	for _, row := range snap.Stats {
		if row.Hands != 1 || row.AllIns != 1 || row.Showdowns != 1 || row.SawFlop != 1 || !row.Online || !row.Playing {
			t.Errorf("row %+v", row)
		}
		if row.BestHand == "" || len(row.BestCards) != 5 {
			t.Errorf("best hand of %s: %q %v", row.Name, row.BestHand, row.BestCards)
		}
	}
	if win.HandsWon != 1 || win.ShowdownsWon != 1 || win.BiggestPot != 2000 {
		t.Errorf("winner %+v", win)
	}
	if lose.HandsWon != 0 || lose.ShowdownsWon != 0 || lose.BiggestPot != 0 {
		t.Errorf("loser %+v", lose)
	}
	if actorRow, _ := statRow(snap, actor.name); actorRow.VPIP != 1 || actorRow.PFR != 1 {
		t.Errorf("all-in raiser VPIP/PFR: %+v", actorRow)
	}
}

func TestRecordHandBestHand(t *testing.T) {
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "a"}
	pair := cards.HandValue{Category: cards.OnePair, Kickers: [5]cards.Rank{cards.King, cards.Ace, cards.Nine, cards.Two}}
	flush := cards.HandValue{Category: cards.Flush, Kickers: [5]cards.Rank{cards.Ace, cards.Jack, cards.Nine, cards.Four, cards.Two}}
	five := [5]cards.Card(cards.MustParseMany("Ah Jh 9h 4h 2h"))
	hands := []engine.PlayerSummary{
		{PlayerID: "p1", Won: 300, Committed: 100, WentToShowdown: true, WonAtShowdown: true, Hand: &flush, HandCards: five},
		{PlayerID: "p1", Won: 0, Committed: 20, VPIP: true, PFR: true, SawFlop: true, WentToShowdown: true, Hand: &pair},
		{PlayerID: "p1", Won: 900, Committed: 400, AllIn: true},
		{PlayerID: "ghost", Won: 5},
	}
	for _, p := range hands {
		st.recordHand(&engine.Result{Players: []engine.PlayerSummary{p}})
	}
	s := st.Accounts["p1"].Stats
	want := handStats{Hands: 3, HandsWon: 2, VPIP: 1, PFR: 1, SawFlop: 1, Showdowns: 2, ShowdownsWon: 1, AllIns: 1, BiggestPot: 900}
	got := s
	got.Best = nil
	if got != want {
		t.Errorf("stats %+v\nwant %+v", got, want)
	}
	if s.Best == nil || s.Best.Value != flush || strings.Join(s.Best.Cards, " ") != "Ah Jh 9h 4h 2h" {
		t.Errorf("best %+v", s.Best)
	}
	rows := st.statsRows(nil, nil, nil)
	if len(rows) != 1 || rows[0].BestHand != "Flush, Ace high" || len(rows[0].BestCards) != 5 || rows[0].PeriodHands != 3 {
		t.Errorf("rows %+v", rows)
	}
}

func TestRabbitHuntAndShowCards(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	spectator := h.connect("key:carol", "carol")
	if err := alice.RabbitHunt(); err == nil {
		t.Error("rabbit hunt during the hand")
	}
	actor, winner := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	res := h.snap(alice).Table.Result
	if res == nil || !res.NoShowdown || !res.CanRabbit {
		t.Fatalf("result %+v", res)
	}
	if err := spectator.RabbitHunt(); !errors.Is(err, engine.ErrNotSeated) {
		t.Errorf("spectator rabbit: %v", err)
	}
	must(t, actor.RabbitHunt())
	snap := h.snap(spectator)
	if r := snap.Table.Result; r == nil || len(r.Rabbit) != 5 || r.CanRabbit {
		t.Errorf("rabbit result %+v", r)
	}
	if r := snap.Table.Result; r != nil && len(r.Rabbit) == 5 {
		// The line keeps the engine's street groups: flop, turn, river.
		c := func(i int) string { return r.Rabbit[i].Rank.String() + r.Rabbit[i].Suit.Symbol() }
		want := fmt.Sprintf("Rabbit hunt (%s): [%s %s %s] [%s] [%s]", actor.name, c(0), c(1), c(2), c(3), c(4))
		if !logHas(snap, want) {
			t.Errorf("log: %q, want %q", snap.Log[len(snap.Log)-1], want)
		}
	}
	if logHas(snap, "Rabbit hunt: ") {
		t.Error("log has the engine's unnamed rabbit line too")
	}
	if err := winner.RabbitHunt(); err == nil {
		t.Error("second rabbit hunt")
	}
	if err := spectator.ShowCards(true, true); err == nil {
		t.Error("spectator shows cards")
	}
	must(t, winner.ShowCards(true, false))
	snap = h.snap(spectator)
	if r := snap.Table.Result; r == nil || len(r.Revealed) != 1 {
		t.Errorf("revealed %+v", r)
	}
	if !logHas(snap, winner.name+" shows ") {
		t.Error("log has no show line")
	}
}

func TestSetNameRenamesSeat(t *testing.T) {
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	must(t, alice.SetName("Alicia"))
	snap := h.snap(bob)
	sv := seatOf(snap.Table, alice.key)
	if sv == nil || sv.Name != "Alicia" {
		t.Errorf("seat %+v", sv)
	}
	if st, ok := standing(snap, "Alicia"); !ok || !st.Playing {
		t.Errorf("leaderboard %+v", snap.Leaderboard)
	}
	n := 0
	for _, l := range snap.Log {
		if strings.Contains(l, "alice is now Alicia") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("rename log lines = %d, want 1", n)
	}
	must(t, alice.Chat("hi"))
	if c := h.snap(bob).Chat; c[len(c)-1].Name != "Alicia" {
		t.Errorf("chat name %q", c[len(c)-1].Name)
	}
}

func TestHandNumberContinuesAfterRestart(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, _ := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	h.advance(cfg.NextHandDelay)
	if n := h.snap(alice).Table.HandNumber; n != 2 {
		t.Fatalf("second hand number %d", n)
	}
	h.stop()
	h2 := newHarness(t, cfg, stackedDeck(bustDeck))
	a := h2.connect(alice.ident, "alice")
	b := h2.connect(bob.ident, "bob")
	must(t, a.Sit(-1))
	must(t, b.Sit(-1))
	h2.advance(cfg.NextHandDelay)
	if n := h2.snap(a).Table.HandNumber; n != 3 {
		t.Errorf("first hand after restart is #%d, want #3", n)
	}
}

func TestStateLock(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	a := h.connect("key:a", "alice")
	must(t, a.Sit(-1))
	if _, err := NewRoom(cfg); !errors.Is(err, ErrLocked) {
		t.Errorf("second NewRoom: %v", err)
	}
	if err := MergeAccounts(cfg, "alice", "bob"); !errors.Is(err, ErrLocked) {
		t.Errorf("merge while running: %v", err)
	}
	rows, set, err := ReadStats(cfg) // No lock needed.
	if err != nil || len(rows) != 1 || rows[0].Name != "alice" || set.Period != 1 {
		t.Errorf("ReadStats while running: %+v %+v %v", rows, set, err)
	}
	h.stop()
	r, err := NewRoom(cfg)
	if err != nil {
		t.Fatalf("NewRoom after Run returned: %v", err)
	}
	unlockState(r.lock)
}

func TestMigrateV1(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	cfg.StatePath = filepath.Join(dir, "state.json")
	v1 := `{"version":1,"players":{
		"SHA256:abc":{"name":"alice","bank":1500,"buy_ins":2,"bought_chips":2000},
		"user:Bob":{"name":"Bob","bank":500,"buy_ins":1,"bought_chips":1000},
		"SHA256:dup":{"name":"ALICE","bank":0,"buy_ins":0,"bought_chips":0}}}`
	if err := os.WriteFile(cfg.StatePath, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, cfg, nil)
	if data, err := os.ReadFile(cfg.StatePath + ".v1.bak"); err != nil || string(data) != v1 {
		t.Errorf("backup: %v", err)
	}
	if data, _ := os.ReadFile(cfg.StatePath); !strings.Contains(string(data), `"version": 3`) {
		t.Errorf("state not saved as v3:\n%s", data)
	}
	if !h.r.KnownKey("SHA256:abc") || h.r.PINAccountExists("bob") {
		t.Error("migrated identities")
	}
	a := h.connectWith("key:SHA256:abc", "x", ConnectOptions{})
	if me := h.snap(a).Me; me.Name != "alice" || me.Bank != 1500 || me.NeedsName || me.CanLink {
		t.Errorf("migrated key account: %+v", me)
	}
	d := h.connectWith("key:SHA256:dup", "x", ConnectOptions{})
	if got := h.snap(d).Me.Name; nameKey(got) == nameKey("alice") {
		t.Errorf("duplicate v1 name kept: %q", got)
	}
	must(t, h.r.CreatePINAccount("bob", "4826")) // The first PIN login sets the PIN.
	if err := h.r.CreatePINAccount("bob", "7391"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("second create: %v", err)
	}
	b := h.connectWith("pin:bob", "bob", ConnectOptions{})
	if me := h.snap(b).Me; me.Name != "Bob" || me.Bank != 500 || !me.Keyless {
		t.Errorf("migrated PIN account: %+v", me)
	}
	row, _ := statRow(h.snap(b), "Bob")
	if row.PeriodNet != -500 || row.BuyIns != 1 {
		t.Errorf("Bob stats %+v", row)
	}
}

// The old Connect keeps working: "user:x" identities are PIN accounts
// without a PIN, so a later CreatePINAccount keeps the history.
func TestLegacyUserIdentity(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	s := h.connect("user:Dave", "Dave")
	must(t, s.Sit(-1))
	must(t, s.Stand())
	s.Close()
	if h.r.PINAccountExists("dave") {
		t.Error("legacy account has a PIN")
	}
	must(t, h.r.CreatePINAccount("dave", "4826"))
	d := h.connectWith("pin:dave", "dave", ConnectOptions{})
	if me := h.snap(d).Me; me.Name != "Dave" || me.Bank != 1000 {
		t.Errorf("Me = %+v", me)
	}
}

func TestMergeAccounts(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	st := newState(testTime)
	flush := cards.HandValue{Category: cards.Flush, Kickers: [5]cards.Rank{cards.Ace}}
	pair := cards.HandValue{Category: cards.OnePair, Kickers: [5]cards.Rank{cards.Two}}
	st.Accounts["p1"] = &account{Name: "Alice", Named: true, Keys: []string{"k1"}, Bank: 100, BuyIns: 1, TotalBuyIns: 2, BoughtChips: 1000, SettledNet: 30,
		Stats: handStats{Hands: 5, BiggestPot: 50, Best: &bestHand{Value: pair, Cards: []string{"2c", "2d", "Ah", "Kh", "9s"}}}}
	st.Accounts["p2"] = &account{Name: "alice-pc", Keys: []string{"k2"}, Login: "al", PINHash: "h", Bank: 2000, BuyIns: 1, TotalBuyIns: 1, BoughtChips: 1000, SettledNet: -30,
		Stats: handStats{Hands: 7, BiggestPot: 900, Best: &bestHand{Value: flush, Cards: []string{"Ah", "Jh", "9h", "4h", "2h"}}}}
	st.Accounts["p3"] = &account{Name: "Bob", Bank: 900, BuyIns: 1, TotalBuyIns: 1, BoughtChips: 1000}
	st.History = []PastPeriod{{Period: 1, Transfers: []Transfer{{From: "alice-pc", To: "Bob", Amount: 10}, {From: "Alice", To: "alice-pc", Amount: 5}}}}
	must(t, saveState(cfg.StatePath, st))

	if err := MergeAccounts(cfg, "nobody", "Alice"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown from: %v", err)
	}
	if err := MergeAccounts(cfg, "alice", "ALICE"); err == nil {
		t.Error("merge into itself")
	}
	must(t, MergeAccounts(cfg, "AL", "alice")) // from by PIN login, to by name
	got, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["p2"] != nil {
		t.Fatalf("accounts after merge: %v", got)
	}
	a := got["p1"]
	if a.Name != "Alice" || strings.Join(a.Keys, ",") != "k1,k2" || a.Login != "al" || a.PINHash != "h" ||
		a.Bank != 2100 || a.BuyIns != 2 || a.TotalBuyIns != 3 || a.BoughtChips != 2000 || a.SettledNet != 0 {
		t.Errorf("merged account %+v", a)
	}
	if a.Stats.Hands != 12 || a.Stats.BiggestPot != 900 || a.Stats.Best == nil || a.Stats.Best.Value != flush {
		t.Errorf("merged stats %+v", a.Stats)
	}
	rows, set, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.PeriodNet
	}
	if sum != 0 || len(set.History) != 1 {
		t.Errorf("rows %+v sum %d", rows, sum)
	}
	want := []Transfer{{From: "Alice", To: "Bob", Amount: 10}}
	if tr := set.History[0].Transfers; len(tr) != 1 || tr[0] != want[0] {
		t.Errorf("history transfers %+v, want %+v", tr, want)
	}
}

func TestReadStatsMissingFile(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "none.json")
	rows, set, err := ReadStats(cfg)
	if err != nil || len(rows) != 0 || set.Period != 1 || set.ChipsPerUnit != 1 {
		t.Errorf("ReadStats: %+v %+v %v", rows, set, err)
	}
}

// A rabbit hunt or a show late in the pause between hands moves the next
// hand later, so everyone sees the revealed cards for at least revealHold.
func TestRevealHoldsNextHand(t *testing.T) {
	cfg := testConfig()
	cfg.NextHandDelay = 8 * time.Second
	h := newHarness(t, cfg, stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	actor, winner := h.toAct(alice, bob)
	must(t, actor.Act(engine.Action{Type: engine.Fold}))
	start := h.snap(alice).NextHandAt
	if start.IsZero() {
		t.Fatal("no next hand scheduled")
	}
	h.advance(7 * time.Second)
	must(t, actor.RabbitHunt())
	after := h.snap(alice)
	if want := after.Now.Add(revealHold); after.NextHandAt.Before(want) {
		t.Errorf("after rabbit: next hand at %v, want >= %v", after.NextHandAt, want)
	}
	h.advance(3 * time.Second)
	must(t, winner.ShowCards(true, true))
	after = h.snap(alice)
	if !after.Table.InHand && after.NextHandAt.Before(after.Now.Add(revealHold)) {
		t.Errorf("after show: next hand at %v, now %v", after.NextHandAt, after.Now)
	}
	// An early reveal does not shorten or extend a long pause.
	h2 := newHarness(t, cfg, stackedDeck(bustDeck))
	a2, b2 := startHeadsUp(t, h2)
	act2, _ := h2.toAct(a2, b2)
	must(t, act2.Act(engine.Action{Type: engine.Fold}))
	first := h2.snap(a2).NextHandAt
	must(t, act2.RabbitHunt())
	if got := h2.snap(a2).NextHandAt; !got.Equal(first) {
		t.Errorf("early rabbit moved next hand from %v to %v", first, got)
	}
}
