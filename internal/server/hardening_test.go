package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/bcrypt"
)

// hostLines records the host log lines of a test.
type hostLines struct {
	mu    sync.Mutex
	lines []string
}

// captureHostLog replaces hostLog until the test ends. Each line is
// "LEVEL msg key=value ...".
func captureHostLog(t *testing.T) *hostLines {
	t.Helper()
	h := &hostLines{}
	old := hostLog
	hostLog = func(level log.Level, msg string, kv ...any) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s", strings.ToUpper(level.String()), msg)
		for i := 0; i+1 < len(kv); i += 2 {
			fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
		}
		h.mu.Lock()
		h.lines = append(h.lines, b.String())
		h.mu.Unlock()
	}
	t.Cleanup(func() { hostLog = old })
	return h
}

// has reports whether one line contains every part.
func (h *hostLines) has(parts ...string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.lines {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(l, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func (h *hostLines) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.lines, "\n")
}

// SEC-1: the PINs that people choose most are refused for a new account.
func TestWeakPINRefused(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	for _, pin := range []string{"1234", "0000", "1111", "1212", "7777", "4321", "0123", "6789", "9876",
		"2580", "0852", "6969", "1004", "2000", "1990", "2024", "1900", "2099"} {
		if !WeakPIN(pin) {
			t.Errorf("WeakPIN(%q) = false", pin)
		}
		if err := h.r.CreatePINAccount("priya", pin); !errors.Is(err, ErrWeakPIN) {
			t.Errorf("CreatePINAccount(priya, %s) = %v, want ErrWeakPIN", pin, err)
		}
	}
	for _, pin := range []string{"4826", "7316", "1357", "2468", "3141", "8090", "1122", "2100", "1899"} {
		if WeakPIN(pin) {
			t.Errorf("WeakPIN(%q) = true", pin)
		}
	}
	if WeakPIN("12a4") {
		t.Error("WeakPIN of a bad format must be false (ErrPINFormat covers it)")
	}
	if h.r.PINAccountExists("priya") {
		t.Fatal("a weak PIN made the account")
	}
	must(t, h.r.CreatePINAccount("priya", "4826"))

	// An account from before the rule keeps its weak PIN.
	hash, err := bcrypt.GenerateFromPassword([]byte("1234"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	h.inRun(func(r *Room) {
		id, a := r.st.newAccount(r.now())
		a.Login, a.Name, a.PINHash = "old", "old", string(hash)
		_ = id
	})
	must(t, h.r.CheckPIN("old", "1234"))
}

// SEC-1: each wrong PIN tells the host (with the address) and the table,
// and a LinkPIN that adds a key says so.
func TestWrongPINAndLinkAreVisible(t *testing.T) {
	logs := captureHostLog(t)
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("priya", "4826"))
	watcher := h.connect("key:watcher", "watcher")

	if err := h.r.CheckPINFrom("priya", "0000", "10.0.0.7:5555"); !errors.Is(err, ErrBadPIN) {
		t.Fatal(err)
	}
	if !logs.has("WARN wrong PIN", "player=priya", "addr=10.0.0.7:5555", "wrong_in_last_hour=1") {
		t.Errorf("no host line for the wrong PIN:\n%s", logs)
	}
	if !logHas(h.snap(watcher), "Wrong PIN for priya (1 in the last hour)") {
		t.Errorf("no table line for the wrong PIN: %q", h.snap(watcher).Log)
	}

	// A fresh key guesses the PIN through LinkPIN.
	guesser := h.connectWith("key:"+testFP("guesser"), "x", ConnectOptions{Addr: "10.0.0.8:6000"})
	if err := guesser.LinkPIN("priya", "1111"); !errors.Is(err, ErrBadPIN) {
		t.Fatal(err)
	}
	if !logs.has("wrong PIN", "addr=10.0.0.8:6000", "wrong_in_last_hour=2") {
		t.Errorf("LinkPIN wrong PIN: no host line with the address:\n%s", logs)
	}
	must(t, guesser.LinkPIN("priya", "4826"))
	if !logs.has("INFO SSH key linked", "player=priya", "key="+testFP("guesser"), "addr=10.0.0.8:6000") {
		t.Errorf("no host line for the link:\n%s", logs)
	}
	if !logHas(h.snap(watcher), "A new SSH key now logs in as priya") {
		t.Errorf("no table line for the link: %q", h.snap(watcher).Log)
	}
}

// SEC-1: the host can remove a linked key offline.
func TestUnlinkKey(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	must(t, h.r.CreatePINAccount("priya", "4826"))
	s := h.connectWith("key:"+testFP("guesser"), "x", ConnectOptions{})
	must(t, s.LinkPIN("priya", "4826"))
	must(t, h.connectWith("key:"+testFP("solo"), "solo", ConnectOptions{}).SetName("solo"))
	if _, err := UnlinkKey(cfg, testFP("guesser")); !errors.Is(err, ErrLocked) {
		t.Errorf("UnlinkKey while the room runs: %v", err)
	}
	h.stop()

	if _, err := UnlinkKey(cfg, "priya"); err == nil || !strings.Contains(err.Error(), "not a key fingerprint") {
		t.Errorf("UnlinkKey(priya) = %v", err)
	}
	if _, err := UnlinkKey(cfg, testFP("nobody")); !errors.Is(err, ErrNoAccount) {
		t.Errorf("UnlinkKey(unknown key) = %v", err)
	}
	if _, err := UnlinkKey(cfg, testFP("solo")); err == nil || !strings.Contains(err.Error(), "only way to log in") {
		t.Errorf("UnlinkKey(the only key) = %v", err)
	}
	name, err := UnlinkKey(cfg, testFP("guesser"))
	if err != nil || name != "priya" {
		t.Fatalf("UnlinkKey = %q, %v", name, err)
	}
	st, _, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, a := st.byKey(testFP("guesser")); a != nil {
		t.Error("the key is still linked")
	}
	if _, a := st.byLogin("priya"); a == nil || len(a.Keys) != 0 || a.PINHash == "" {
		t.Errorf("priya after unlink: %+v", a)
	}
	if list, _ := backups(cfg.StatePath); !slices.ContainsFunc(list, func(p string) bool { return strings.HasSuffix(p, ".unlink.bak") }) {
		t.Errorf("no unlink backup: %q", list)
	}
}

// SEC-3: wrong PINs from one address lock that address only. The owner
// can still log in from another computer. Many addresses together hit the
// limit for the login.
func TestPINLockPerAddress(t *testing.T) {
	logs := captureHostLog(t)
	h := newHarness(t, testConfig(), nil)
	must(t, h.r.CreatePINAccount("priya", "4826"))
	prank := "10.0.0.66:40000"
	for i := range pinMaxFails {
		if err := h.r.CheckPINFrom("priya", "9999", prank); !errors.Is(err, ErrBadPIN) {
			t.Fatalf("wrong PIN %d: %v", i+1, err)
		}
	}
	// Another port of the same computer is the same address.
	if err := h.r.CheckPINFrom("priya", "4826", "10.0.0.66:40001"); !errors.Is(err, ErrPINLocked) {
		t.Errorf("the right PIN from the locked address: %v", err)
	}
	if !logs.has("PIN login locked for one address", "addr="+prank) {
		t.Errorf("no host line for the lock:\n%s", logs)
	}
	must(t, h.r.CheckPINFrom("priya", "4826", "10.0.0.5:50000")) // the owner

	// Four addresses with 5 wrong PINs each lock the login everywhere.
	for i := range pinMaxLoginFails - pinMaxFails {
		addr := fmt.Sprintf("10.0.1.%d:1", i/pinMaxFails)
		if err := h.r.CheckPINFrom("priya", "9999", addr); !errors.Is(err, ErrBadPIN) {
			t.Fatalf("wrong PIN %d from %s: %v", i+1, addr, err)
		}
	}
	if err := h.r.CheckPINFrom("priya", "4826", "10.0.0.5:50000"); !errors.Is(err, ErrPINLocked) {
		t.Errorf("after %d wrong PINs from all addresses: %v", pinMaxLoginFails, err)
	}
	if !logs.has("PIN login locked for all addresses", "player=priya") {
		t.Errorf("no host line for the login lock:\n%s", logs)
	}
	h.clock.add(pinLoginWindow)
	must(t, h.r.CheckPINFrom("priya", "4826", "10.0.0.5:50000"))
	h.inRun(func(r *Room) {
		if len(r.pins) > len(r.pinTotals)+5 {
			t.Errorf("guards are not removed: %d by address, %d by login", len(r.pins), len(r.pinTotals))
		}
	})
}

// SEC-4: a name that looks the same as another name is in use.
func TestLookAlikeNames(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	joe := h.connectWith("key:"+testFP("joe"), "j", ConnectOptions{})
	must(t, joe.SetName("Joe"))
	other := h.connectWith("key:"+testFP("other"), "o", ConnectOptions{})
	for _, fake := range []string{"Јое", "јое", "JOE", "Јoе", "Joе"} {
		// The mixed ones fail the one-script rule; the others are look-alikes.
		if err := other.SetName(fake); !errors.Is(err, ErrNameInUse) && !errors.Is(err, ErrBadName) {
			t.Errorf("SetName(%q) = %v, want ErrNameInUse or ErrBadName", fake, err)
		}
	}
	if err := other.SetName("Јое"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("all-Cyrillic Јое: %v, want ErrNameInUse", err)
	}
	if err := other.SetName("Ροκ"); err != nil { // Greek, no Latin twin in use
		t.Errorf("SetName(Ροκ): %v", err)
	}
	must(t, h.r.CreatePINAccount("pok", "4826"))
	third := h.connectWith("key:"+testFP("third"), "t", ConnectOptions{})
	if err := third.SetName("рок"); !errors.Is(err, ErrNameInUse) { // Cyrillic copy of the login "pok"
		t.Errorf("Cyrillic copy of a login: %v", err)
	}
	must(t, third.SetName("Иван")) // Cyrillic without a Latin twin
	if skeleton("Јое") != skeleton("joe") || skeleton("Иван") == skeleton("NBah") {
		t.Errorf("skeleton: %q %q %q", skeleton("Јое"), skeleton("joe"), skeleton("Иван"))
	}
}

// SEC-2: a new key costs one pass over the accounts, not one per name
// candidate, so many accounts with one name do not stop the room.
func TestUniqueNameIsLinear(t *testing.T) {
	st := newState(testTime)
	const n = 4000
	for i := range n {
		_, a := st.newAccount(testTime)
		a.Name = "x"
		if i > 0 {
			a.Name = fmt.Sprintf("x%d", i+1)
		}
	}
	start := time.Now()
	got := st.uniqueName("x", "")
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("uniqueName took %v with %d accounts", took, n)
	}
	if want := fmt.Sprintf("x%d", n+1); got != want {
		t.Errorf("uniqueName = %q, want %q", got, want)
	}
	if id, a := st.byKey("nope"); a != nil || id != "" {
		t.Error("byKey found a key that nobody has")
	}
}

// SEC-2: an account that a new key made and that did nothing is not saved,
// and the room removes such accounts at start.
func TestIdleKeyAccountsNotSaved(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	h.connectWith("key:"+testFP("idle"), "idle", ConnectOptions{})
	named := h.connectWith("key:"+testFP("named"), "named", ConnectOptions{})
	must(t, named.SetName("named"))
	h.stop()
	saved, err := loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range saved {
		names = append(names, a.Name)
	}
	if !slices.Equal(names, []string{"named"}) {
		t.Errorf("saved accounts %q, want only [named]", names)
	}

	// An older file with idle accounts: the next start removes them.
	st := newState(testTime)
	for i := range 50 {
		_, a := st.newAccount(testTime)
		a.Name, a.Keys = fmt.Sprintf("junk%d", i), []string{testFP(fmt.Sprint(i))}
	}
	_, keep := st.newAccount(testTime)
	keep.Name, keep.Keys, keep.Bank, keep.BoughtChips, keep.BuyIns = "keep", []string{testFP("keep")}, 1000, 1000, 1
	must(t, saveState(cfg.StatePath, st))
	h2 := newHarness(t, cfg, nil)
	h2.stop()
	saved, err = loadLedger(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Errorf("after the start: %d accounts, want 1", len(saved))
	}
}

// SEC-2: one address cannot make new key accounts without a limit.
func TestNewKeyLimitPerAddress(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	addr := "10.0.0.9:2000"
	for i := range maxNewKeysPerHour {
		s := h.connectWith("key:"+testFP(fmt.Sprint("k", i)), "x", ConnectOptions{Addr: addr})
		s.Close()
	}
	if _, err := h.r.ConnectWith("key:"+testFP("one more"), "x", ConnectOptions{Addr: "10.0.0.9:2001"}); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("new key over the limit: %v", err)
	}
	h.connectWith("key:"+testFP("k0"), "x", ConnectOptions{Addr: addr}) // a known key
	h.connectWith("key:"+testFP("other"), "x", ConnectOptions{Addr: "10.0.0.10:1"})
	h.connectWith("key:"+testFP("host"), "x", ConnectOptions{Addr: "127.0.0.1:1", Host: true})
	h.clock.add(time.Hour)
	h.connectWith("key:"+testFP("later"), "x", ConnectOptions{Addr: addr})
}

// REL-2 / OPS-3: a failed save goes to the host log, and SaveErr reports
// the result of the last save, also of the final save in Run.
func TestSaveErrorReported(t *testing.T) {
	logs := captureHostLog(t)
	dir := filepath.Join(t.TempDir(), "state")
	cfg := testConfig()
	cfg.StatePath = filepath.Join(dir, "state.json")
	h := newHarness(t, cfg, nil)
	alice := h.connect("key:alice", "alice")
	if err := h.r.SaveErr(); err != nil {
		t.Fatalf("SaveErr before a failure: %v", err)
	}
	must(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	must(t, alice.Sit(-1))
	if h.r.SaveErr() == nil {
		t.Fatal("SaveErr is nil after a failed save")
	}
	if !logs.has("ERROR could not save the state file", "path="+cfg.StatePath) {
		t.Errorf("no host line for the failed save:\n%s", logs)
	}
	if !logHas(h.snap(alice), "Could not save the chip bank") {
		t.Error("no table line for the failed save")
	}
	must(t, os.Chmod(dir, 0o700))
	must(t, alice.SetAlerts(false))
	if err := h.r.SaveErr(); err != nil {
		t.Errorf("SaveErr after a save that worked: %v", err)
	}
	if !logs.has("INFO the state file is saved again") {
		t.Errorf("no host line for the recovery:\n%s", logs)
	}

	must(t, os.Chmod(dir, 0o500))
	h.stop()
	if h.r.SaveErr() == nil {
		t.Error("SaveErr is nil after the final save failed")
	}
}

// OPS-1: a panic in a command does not save a half-changed state, and it
// releases the callers and the state lock.
func TestPanicInCommandDoesNotSave(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	r, err := newRoomWith(cfg, options{pinCost: bcrypt.MinCost})
	if err != nil {
		t.Fatal(err)
	}
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		r.Run(context.Background())
	}()
	s, err := r.Connect("key:alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	must(t, s.Sit(-1))
	before, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	err = r.do(func() error {
		for _, a := range r.st.Accounts {
			a.Bank = 999999
		}
		panic("boom")
	})
	if !errors.Is(err, ErrRoomClosed) {
		t.Errorf("do after the panic: %v", err)
	}
	if p := <-panicked; p != "boom" {
		t.Errorf("Run panicked with %v, want boom", p)
	}
	after, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the room saved the state after the panic")
	}
	if r.SaveErr() == nil {
		t.Error("SaveErr is nil after the panic")
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		t.Fatalf("the state lock is still held: %v", err)
	}
	unlockState(lock)
}

// OPS-2: the room keeps timestamped backups of the state file: at start,
// before a settle and before a merge. Only the newest keepBackups stay.
func TestStateBackups(t *testing.T) {
	cfg := testConfig()
	dir := t.TempDir()
	cfg.StatePath = filepath.Join(dir, "state.json")
	h := newHarness(t, cfg, nil)
	alice := h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	must(t, alice.SetName("alice"))
	must(t, alice.Sit(-1))
	h.stop()
	if list, _ := backups(cfg.StatePath); len(list) != 0 {
		t.Errorf("backups after a start without a file: %q", list)
	}

	h = newHarness(t, cfg, nil)
	list, _ := backups(cfg.StatePath)
	if len(list) != 1 || !strings.HasSuffix(list[0], ".start.bak") {
		t.Fatalf("backups after a start: %q", list)
	}
	alice = h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	h.clock.add(time.Second)
	must(t, alice.MarkSettled())
	list, _ = backups(cfg.StatePath)
	settle := list[len(list)-1]
	if !strings.HasSuffix(settle, ".settle.bak") {
		t.Fatalf("backups after a settle: %q", list)
	}
	old, err := loadLedger(settle)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range old {
		if a.Name == "alice" && a.Bank != cfg.StartingStack {
			t.Errorf("the settle backup has bank %d, want the bank before the settle (%d)", a.Bank, cfg.StartingStack)
		}
	}
	bob := h.connectWith("key:bob", "bob", ConnectOptions{})
	must(t, bob.SetName("bob"))
	h.stop()
	must(t, MergeAccounts(cfg, "bob", "alice"))
	list, _ = backups(cfg.StatePath)
	if !strings.HasSuffix(list[len(list)-1], ".merge.bak") {
		t.Errorf("backups after a merge: %q", list)
	}

	// The rotation keeps the newest keepBackups and does not touch the
	// migration backup.
	path := filepath.Join(t.TempDir(), "s.json")
	must(t, os.WriteFile(path, []byte("{}"), 0o600))
	must(t, os.WriteFile(path+".v2.bak", []byte("{}"), 0o600))
	for i := range keepBackups + 3 {
		must(t, backupState(path, testTime.Add(time.Duration(i)*time.Second), "test"))
	}
	list, _ = backups(path)
	newest := testTime.Add(time.Duration(keepBackups+2) * time.Second).UTC().Format("20060102T150405Z")
	if len(list) != keepBackups || !strings.Contains(list[len(list)-1], newest) {
		t.Errorf("after the rotation: %q", list)
	}
	if _, err := os.Stat(path + ".v2.bak"); err != nil {
		t.Errorf("the migration backup is gone: %v", err)
	}
	// Two backups in one second get two names.
	later := testTime.Add(time.Hour)
	must(t, backupState(path, later, "same"))
	must(t, backupState(path, later, "same"))
	if _, err := os.Stat(path + "." + later.UTC().Format("20060102T150405Z") + ".same-2.bak"); err != nil {
		t.Errorf("the second backup in one second: %v", err)
	}
}

// money-1: a restart with another -chips-per-unit keeps the rate of the
// open period; the new rate applies from the next period.
func TestRestartKeepsPeriodRate(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	cfg.ChipsPerUnit, cfg.Currency = 10, "₹"
	h := newHarness(t, cfg, nil)
	alice := h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	must(t, alice.SetName("alice"))
	must(t, alice.Sit(-1))
	h.inRun(func(r *Room) { r.st.Accounts[alice.key].Bank = 400 }) // alice is up 400 chips
	must(t, alice.Stand())
	h.stop()

	logs := captureHostLog(t)
	cfg.ChipsPerUnit = 2
	h = newHarness(t, cfg, nil)
	if got := h.r.Config().ChipsPerUnit; got != 10 {
		t.Errorf("Config().ChipsPerUnit = %d, want the saved 10", got)
	}
	if !logs.has("WARN the open period keeps the money rate", "chips_per_unit=10", "flag_chips_per_unit=2") {
		t.Errorf("no host line for the kept rate:\n%s", logs)
	}
	alice = h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	settle := h.snap(alice).Settle
	if settle.ChipsPerUnit != 10 || len(settle.Balances) != 1 || settle.Balances[0].Money != 4000 {
		t.Errorf("settle after the restart: %+v, want 400 chips = Money 4000 at 10 chips per unit", settle)
	}
	h.stop()
	_, offline, err := ReadStats(cfg)
	if err != nil || offline.ChipsPerUnit != 10 {
		t.Errorf("ReadStats: rate %d, %v; want 10", offline.ChipsPerUnit, err)
	}

	h = newHarness(t, cfg, nil)
	alice = h.connectWith("key:alice", "alice", ConnectOptions{Admin: true})
	must(t, alice.MarkSettled())
	snap := h.snap(alice)
	if len(snap.Settle.History) == 0 || snap.Settle.History[0].ChipsPerUnit != 10 {
		t.Errorf("history: %+v", snap.Settle.History)
	}
	if snap.Settle.ChipsPerUnit != 2 || snap.Config.ChipsPerUnit != 2 {
		t.Errorf("the new period uses %d (config %d), want 2", snap.Settle.ChipsPerUnit, snap.Config.ChipsPerUnit)
	}
	h.stop()

	// A period without chips takes the flags at once.
	cfg.ChipsPerUnit = 5
	h = newHarness(t, cfg, nil)
	if got := h.r.Config().ChipsPerUnit; got != 5 {
		t.Errorf("an empty period: Config().ChipsPerUnit = %d, want 5", got)
	}
}
