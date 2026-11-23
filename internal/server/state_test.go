package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

// loadLedger reads the accounts of the state file at path (test helper).
func loadLedger(path string) (map[string]*account, error) {
	st, _, err := loadState(path, time.Now())
	if err != nil {
		return nil, err
	}
	return st.Accounts, nil
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	in := newState(now)
	in.HandNumber = 41
	in.Period = 3
	in.Accounts["p1"] = &account{Name: "alice", Named: true, Keys: []string{"SHA256:abc"}, Bank: 1500, BuyIns: 2, TotalBuyIns: 5, BoughtChips: 2000,
		Stats: handStats{Hands: 9, Best: &bestHand{Value: cards.HandValue{Category: cards.FullHouse, Kickers: [5]cards.Rank{13, 7}}, Cards: []string{"Kd", "Kc", "Ks", "7h", "7d"}}}}
	in.Accounts["p2"] = &account{Name: "bob", Login: "bob", PINHash: "$2a$10$x", AlertsOff: true, SettledNet: -40}
	in.History = []PastPeriod{{Period: 2, Since: now, SettledAt: now, Currency: "₹", Transfers: []Transfer{{From: "bob", To: "alice", Amount: 1250}}}}
	if err := saveState(path, in); err != nil {
		t.Fatal(err)
	}
	got, migrated, err := loadState(path, now)
	if err != nil || migrated {
		t.Fatal(err, migrated)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, in)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}
}

func TestStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for i := range 3 {
		st := newState(time.Now())
		st.Accounts["k"] = &account{Name: "a", Bank: int64(i)}
		if err := saveState(path, st); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir holds %v, want only state.json (no temp files)", names)
	}
	got, err := loadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["k"].Bank != 2 {
		t.Errorf("bank = %d, want 2 (the last write)", got["k"].Bank)
	}
}

func TestStateWriteFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := newState(time.Now())
	st.Accounts["k"] = &account{Bank: 7}
	if err := saveState(path, st); err != nil {
		t.Fatal(err)
	}
	// A directory at the target path makes the rename fail.
	bad := filepath.Join(dir, "sub")
	if err := os.Mkdir(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	st.Accounts["k"].Bank = 9
	if err := saveState(bad, st); err == nil {
		t.Fatal("save over a directory: want an error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
	got, err := loadLedger(path)
	if err != nil || got["k"].Bank != 7 {
		t.Errorf("old file changed: %+v, %v", got["k"], err)
	}
}

func TestLoadStateMissingAndBad(t *testing.T) {
	got, err := loadLedger(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(got) != 0 {
		t.Errorf("missing file: got %v, %v; want empty, nil", got, err)
	}
	got, err = loadLedger("")
	if err != nil || len(got) != 0 {
		t.Errorf("empty path: got %v, %v; want empty, nil", got, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLedger(bad); err == nil {
		t.Error("corrupt file: want an error")
	}
	if err := os.WriteFile(bad, []byte(`{"version":99,"players":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLedger(bad); err == nil {
		t.Error("unknown version: want an error")
	}
}

// TestNetMath follows one player through buy-in, a loss, a rebuy, a win and
// a stand, and checks Net = Bank + stack - BoughtChips at each step.
func TestNetMath(t *testing.T) {
	const start = 1000
	var a account

	// Sit with an empty bank: buy in.
	a.buyIn(start)
	stack := int64(start)
	if a.net(stack) != 0 || a.BuyIns != 1 {
		t.Fatalf("after buy-in: net %d, buy-ins %d", a.net(stack), a.BuyIns)
	}
	// Lose everything, then rebuy.
	stack = 0
	if a.net(stack) != -start {
		t.Fatalf("busted: net %d, want %d", a.net(stack), -start)
	}
	a.buyIn(start)
	stack = start
	if a.net(stack) != -start || a.BuyIns != 2 || a.BoughtChips != 2*start {
		t.Fatalf("after rebuy: %+v net %d", a, a.net(stack))
	}
	// Win 2500, then stand: the stack moves to the bank.
	stack += 2500
	a.Bank += stack
	stack = 0
	if a.net(stack) != 1500 {
		t.Fatalf("after stand: net %d, want 1500", a.net(stack))
	}
	// Sit again with the bank: no new buy-in, net does not change.
	stack, a.Bank = a.Bank, 0
	if a.net(stack) != 1500 || a.BuyIns != 2 {
		t.Fatalf("after second sit: net %d buy-ins %d", a.net(stack), a.BuyIns)
	}
}

func TestRing(t *testing.T) {
	r := ring[int]{max: 3}
	if got := r.items(); len(got) != 0 {
		t.Fatalf("empty ring: %v", got)
	}
	for i := range 10 {
		r.add(i)
	}
	if got := r.items(); !reflect.DeepEqual(got, []int{7, 8, 9}) {
		t.Fatalf("items = %v, want [7 8 9]", got)
	}
	got := r.items()
	got[0] = 100
	if r.items()[0] != 7 {
		t.Fatal("items shares memory with the ring")
	}
}
