package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A merge must not move a past transfer of another account that once used
// the same display name (money-2).
func TestMergeKeepsOtherPlayersPastDebt(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	st := newState(testTime)
	// p1 played period 1 as "Bob" and paid Alice 500.00. Then p1 renamed to
	// "Rob", and a new player p3 took the free name "Bob".
	st.Accounts["p1"] = &account{Name: "Rob", Named: true, Keys: []string{"k1"}, SettledNet: -1000}
	st.Accounts["p2"] = &account{Name: "Alice", Named: true, Keys: []string{"k2"}, SettledNet: 1000}
	st.Accounts["p3"] = &account{Name: "Bob", Named: true, Keys: []string{"k3"}}
	st.Accounts["p4"] = &account{Name: "Carol", Named: true, Keys: []string{"k4"}}
	st.History = []PastPeriod{{Period: 1, Transfers: []Transfer{{From: "Bob", To: "Alice", Amount: 50000}}}}
	st.Period = 2
	must(t, saveState(cfg.StatePath, st))

	// The host merges the new "Bob" (p3, no history) into Carol.
	must(t, MergeAccounts(cfg, "Bob", "Carol"))
	_, set, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := set.History[0].Transfers[0]
	if got.From == "Carol" {
		t.Fatalf("history now says %q paid %q %d: p1's period-1 debt moved to Carol, who never played period 1", got.From, got.To, got.Amount)
	}
}

func TestMergeKeepsRealPayment(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "Rob", Named: true, Keys: []string{"k1"}}
	st.Accounts["p2"] = &account{Name: "Alice", Named: true, Keys: []string{"k2"}}
	st.Accounts["p3"] = &account{Name: "Bob", Named: true, Keys: []string{"k3"}}
	st.History = []PastPeriod{{Period: 1, Transfers: []Transfer{{From: "Bob", To: "Alice", Amount: 50000}}}}
	st.Period = 2
	must(t, saveState(cfg.StatePath, st))
	must(t, MergeAccounts(cfg, "Bob", "Alice"))
	_, set, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.History) == 0 || len(set.History[0].Transfers) == 0 {
		t.Fatalf("p1's period-1 payment to Alice was deleted: %+v", set.History)
	}
}

// closePeriod records the account ids of the transfers, so a merge moves the
// past transfers of the merged account by id: also after that account and
// another account changed names.
func TestMergeHistoryByID(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "Bob", Named: true, Keys: []string{"k1"}, BoughtChips: 1000, Bank: 500}
	st.Accounts["p2"] = &account{Name: "Alice", Named: true, Keys: []string{"k2"}, BoughtChips: 1000, Bank: 1500}
	st.Accounts["p3"] = &account{Name: "Carol", Named: true, Keys: []string{"k3"}}
	st.closePeriod(testTime, 1, "₹")
	if tr := st.History[0].Transfers; len(tr) != 1 || tr[0].From != "Bob" || tr[0].To != "Alice" {
		t.Fatalf("period 1 transfers %+v", tr)
	}
	// p1 (the payer) renames to "Rob"; a new player p4 takes "Bob".
	st.Accounts["p1"].Name = "Rob"
	st.Accounts["p4"] = &account{Name: "Bob", Named: true, Keys: []string{"k4"}}
	must(t, saveState(cfg.StatePath, st))

	// The new "Bob" has no past transfers: nothing moves.
	must(t, MergeAccounts(cfg, "Bob", "Carol"))
	_, set, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tr := set.History[0].Transfers; len(tr) != 1 || tr[0].From != "Bob" || tr[0].To != "Alice" {
		t.Fatalf("after merge of the new Bob: %+v", tr)
	}
	// Rob (who paid as "Bob") merges into Carol: his payment moves to Carol.
	must(t, MergeAccounts(cfg, "Rob", "Carol"))
	if _, set, err = ReadStats(cfg); err != nil {
		t.Fatal(err)
	}
	if tr := set.History[0].Transfers; len(tr) != 1 || tr[0].From != "Carol" || tr[0].To != "Alice" || tr[0].Amount != 50000 {
		t.Fatalf("after merge of Rob: %+v", tr)
	}
	// Carol (now the payer) merges into Alice: the payment to oneself goes.
	must(t, MergeAccounts(cfg, "Carol", "Alice"))
	if _, set, err = ReadStats(cfg); err != nil {
		t.Fatal(err)
	}
	if tr := set.History[0].Transfers; len(tr) != 0 {
		t.Fatalf("after merge into the payee: %+v", tr)
	}
}

// A display name cannot copy another account's PIN login, and a merge
// resolves "alice" to the PIN login when nobody can use it as a name
// (auth-2).
func TestDisplayNameCannotShadowPINLoginInMerge(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)

	// Alice made a PIN player "alice" and calls herself "Alice S".
	must(t, h.r.CreatePINAccount("alice", "4826"))
	alice := h.connectWith("pin:alice", "alice", ConnectOptions{})
	must(t, alice.SetName("Alice S"))
	alice.Close()

	// Mallory tries the display name "alice": it is a PIN login.
	mallory := h.connectWith("key:SHA256:mallory", "mallory", ConnectOptions{})
	if err := mallory.SetName("alice"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("SetName(alice) = %v, want ErrNameInUse", err)
	}
	mallory.Close()

	laptop := h.connectWith("key:SHA256:alice-laptop", "alice", ConnectOptions{})
	must(t, laptop.SetName("Alice laptop"))
	must(t, laptop.Sit(-1))
	must(t, laptop.Stand()) // bank 1000
	if err := laptop.LinkPIN("alice", "4826"); !errors.Is(err, ErrCannotLink) {
		t.Fatalf("LinkPIN: %v", err)
	}
	laptop.Close()
	h.stop()

	// The host merges the key player into the PIN player "alice".
	must(t, MergeAccounts(cfg, "Alice laptop", "alice"))

	h2 := newHarness(t, cfg, nil)
	got := h2.connectWith("key:SHA256:alice-laptop", "alice", ConnectOptions{})
	me := h2.snap(got).Me
	if me.Name != "Alice S" {
		t.Errorf("after merge into PIN login \"alice\", Alice's key logs in as %q (bank %d), want \"Alice S\"", me.Name, me.Bank)
	}
	m := h2.connectWith("key:SHA256:mallory", "mallory", ConnectOptions{})
	if bank := h2.snap(m).Me.Bank; bank != 0 {
		t.Errorf("mallory's bank = %d after the host merged Alice's player; Alice's chips went to mallory", bank)
	}
}

// A state file where a display name already equals another account's PIN
// login (made before the check): merge refuses the ambiguous identifier and
// takes the "name:" and "login:" forms.
func TestMergeAmbiguousNameAndLogin(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "Alice S", Named: true, Login: "alice", PINHash: "h"}
	st.Accounts["p2"] = &account{Name: "alice", Named: true, Keys: []string{"km"}}
	st.Accounts["p3"] = &account{Name: "Alice laptop", Named: true, Keys: []string{"kl"}, Bank: 1000}
	must(t, saveState(cfg.StatePath, st))

	err := MergeAccounts(cfg, "Alice laptop", "alice")
	if err == nil || !strings.Contains(err.Error(), "login:alice") {
		t.Fatalf("ambiguous merge: %v", err)
	}
	if err := MergeAccounts(cfg, "Alice laptop", "login:nobody"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown login: %v", err)
	}
	must(t, MergeAccounts(cfg, "name:Alice laptop", "login:alice"))
	got, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if a := got["p1"]; a == nil || a.Bank != 1000 || !a.hasKey("kl") || got["p3"] != nil {
		t.Errorf("accounts after merge: %+v", got)
	}
	if a := got["p2"]; a == nil || a.Bank != 0 || a.hasKey("kl") {
		t.Errorf("the name holder changed: %+v", a)
	}
}
