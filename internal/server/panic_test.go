package server

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testFP returns a fingerprint in the form of ssh-keygen -lf for seed.
func testFP(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func TestParseAdmin(t *testing.T) {
	fp := testFP("alice")
	good := map[string]string{
		fp:                 fp,
		" " + fp + " ":     fp,
		"sha256:" + fp[7:]: fp,
		fp + "=":           fp,
		"pin:Bob":          "pin:bob",
		"PIN:carol.k":      "pin:carol.k",
		"pin:  dave ":      "pin:dave",
		"Sha256:" + fp[7:]: fp,
	}
	for in, want := range good {
		got, err := ParseAdmin(in)
		if err != nil || got != want {
			t.Errorf("ParseAdmin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "Alice", "alice", "key:" + fp, fp[:20], fp + "x", "SHA256:" + strings.Repeat("!", 43),
		"pin:", "pin:   ", "pin:a\x01b", "pin:" + strings.Repeat("a", 40), "name:Alice", "MD5:aa:bb",
	} {
		_, err := ParseAdmin(in)
		if !errors.Is(err, ErrBadAdmin) {
			t.Errorf("ParseAdmin(%q) err = %v, want ErrBadAdmin", in, err)
			continue
		}
		if !strings.Contains(err.Error(), strconv.Quote(in)) {
			t.Errorf("ParseAdmin(%q) error does not name the entry: %v", in, err)
		}
	}
}

func TestNewRoomRejectsBadAdmin(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	cfg.Admins = []string{testFP("a"), "Alice"}
	_, err := NewRoom(cfg)
	if !errors.Is(err, ErrBadAdmin) || !strings.Contains(err.Error(), `"Alice"`) {
		t.Fatalf("NewRoom: %v", err)
	}
	// The failed NewRoom released the state lock.
	cfg.Admins = []string{testFP("a"), "pin:alice"}
	r, err := NewRoom(cfg)
	if err != nil {
		t.Fatalf("NewRoom after a bad admin: %v", err)
	}
	unlockState(r.lock)
}

// pinStateConfig returns a config with a state file that holds a PIN
// profile (PIN 4826) for each login. A "pin:" admin entry counts only for
// a PIN profile that exists when the room starts.
func pinStateConfig(t *testing.T, logins ...string) Config {
	t.Helper()
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	for _, l := range logins {
		must(t, h.r.CreatePINAccount(l, "4826"))
	}
	h.stop()
	return cfg
}

func TestAdminByCredential(t *testing.T) {
	cfg := pinStateConfig(t, "ops", "guest")
	cfg.Admins = []string{testFP("boss key"), "pin:Ops"}
	h := newHarness(t, cfg, nil)
	if got := h.r.IgnoredAdmins(); len(got) != 0 {
		t.Fatalf("IgnoredAdmins = %q, want none", got)
	}

	keyAdmin := h.connectWith("key:"+testFP("boss key"), "boss", ConnectOptions{})
	pinAdmin := h.connectWith("pin:ops", "ops", ConnectOptions{})
	other := h.connectWith("key:"+testFP("other key"), "mallory", ConnectOptions{})
	guest := h.connectWith("pin:guest", "guest", ConnectOptions{})
	host := h.connectWith("key:"+testFP("host key"), "host", ConnectOptions{Admin: true, Host: true})
	for _, c := range []struct {
		s    *Session
		want bool
	}{{keyAdmin, true}, {pinAdmin, true}, {other, false}, {guest, false}, {host, true}} {
		if got := h.snap(c.s).Me.Admin; got != c.want {
			t.Errorf("%s: Me.Admin = %v, want %v", c.s.name, got, c.want)
		}
	}

	// A player who takes an admin's name is still not an admin. (A name
	// cannot be another account's PIN login, so "ops" is not possible.)
	must(t, keyAdmin.SetName("Anyone"))
	for _, name := range []string{"boss", "Admin", "root"} {
		must(t, other.SetName(name))
		if h.snap(other).Me.Admin {
			t.Errorf("a player renamed to %q is an admin", name)
		}
		if err := other.MarkSettled(); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("player renamed to %q: MarkSettled %v", name, err)
		}
	}
	// The real admins keep admin after a rename.
	must(t, pinAdmin.SetName("Ops Lead"))
	if !h.snap(keyAdmin).Me.Admin || !h.snap(pinAdmin).Me.Admin {
		t.Error("an admin lost admin after a rename")
	}
	must(t, pinAdmin.MarkSettled())
	must(t, keyAdmin.MarkSettled())
}

// TestAdminAfterLinkPIN: admin follows the credential that the session
// logged in with. A key session that links to the "pin:" admin account is
// not an admin, because it logged in with the key.
func TestAdminAfterLinkPIN(t *testing.T) {
	cfg := pinStateConfig(t, "ops")
	cfg.Admins = []string{"pin:ops"}
	h := newHarness(t, cfg, nil)
	laptop := h.connectWith("key:"+testFP("laptop"), "laptop", ConnectOptions{})
	must(t, laptop.LinkPIN("ops", "4826"))
	if h.snap(laptop).Me.Admin {
		t.Error("a key session that linked to a pin: admin account is an admin")
	}
	pin := h.connectWith("pin:ops", "ops", ConnectOptions{})
	if !h.snap(pin).Me.Admin {
		t.Error("the PIN login of the admin is not an admin")
	}
}

// B7-2: a "pin:" admin entry for a login that has no PIN profile at the
// start makes nobody an admin. Else the first player who creates that PIN
// login (with any SSH user name and a new PIN) gets the right to settle.
// A legacy profile with the login and no PIN does not count either.
func TestPINAdminNotClaimable(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	h.connect("user:carol", "carol") // a legacy profile: login, no PIN
	h.stop()

	cfg.Admins = []string{"pin:alice", "pin:carol"}
	h = newHarness(t, cfg, nil)
	if got, want := h.r.IgnoredAdmins(), []string{"pin:alice", "pin:carol"}; !slices.Equal(got, want) {
		t.Fatalf("IgnoredAdmins = %q, want %q", got, want)
	}
	for _, login := range []string{"alice", "carol"} {
		must(t, h.r.CreatePINAccount(login, "4826")) // Mallory is first.
		mallory := h.connectWith("pin:"+login, "mallory", ConnectOptions{})
		if h.snap(mallory).Me.Admin {
			t.Errorf("pin:%s: the player who created the PIN login is an admin", login)
		}
		if err := mallory.MarkSettled(); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("pin:%s: MarkSettled = %v, want ErrNotAdmin", login, err)
		}
	}

	// After a restart, the PIN profiles exist, and the entries count.
	h.stop()
	h = newHarness(t, cfg, nil)
	if got := h.r.IgnoredAdmins(); len(got) != 0 {
		t.Fatalf("after the restart IgnoredAdmins = %q, want none", got)
	}
	if s := h.connectWith("pin:alice", "alice", ConnectOptions{}); !h.snap(s).Me.Admin {
		t.Error("after the restart the PIN login is not an admin")
	}
}

// capturePanicLog records the panic log lines until the test ends.
func capturePanicLog(t *testing.T) func() []string {
	var mu sync.Mutex
	var lines []string
	old := panicLog
	panicLog = func(name string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, name)
	}
	t.Cleanup(func() { panicLog = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(lines)
	}
}

func TestPanicBroadcast(t *testing.T) {
	logged := capturePanicLog(t)
	h := newHarness(t, testConfig(), stackedDeck(bustDeck))
	alice, bob := startHeadsUp(t, h)
	watcher := h.connect("key:carol", "carol") // a spectator
	all := []*Session{alice, bob, watcher}
	for _, s := range all {
		if got := h.snap(s).PanicSeq; got != 0 {
			t.Fatalf("%s: PanicSeq %d before any panic", s.name, got)
		}
	}
	logLen := len(h.snap(alice).Log)
	chatLen := len(h.snap(alice).Chat)

	must(t, watcher.Panic())
	for _, s := range all {
		if got := h.snap(s).PanicSeq; got != 1 {
			t.Errorf("%s: PanicSeq %d, want 1", s.name, got)
		}
	}
	snap := h.snap(alice)
	if len(snap.Log) != logLen || len(snap.Chat) != chatLen {
		t.Errorf("the panic wrote to the table log or the chat: %q %v", snap.Log[logLen:], snap.Chat)
	}
	if got := logged(); len(got) != 1 || got[0] != "carol" {
		t.Errorf("server log lines %q, want one line for carol", got)
	}
	// The game does not stop: the hand runs on.
	if !snap.Table.InHand {
		t.Error("the panic stopped the hand")
	}

	// The cooldown is per account.
	if err := watcher.Panic(); !errors.Is(err, ErrPanicTooSoon) {
		t.Errorf("second panic at once: %v", err)
	}
	must(t, bob.Panic())
	if got := h.snap(alice).PanicSeq; got != 2 {
		t.Errorf("PanicSeq after bob %d, want 2", got)
	}
	h.clock.add(PanicCooldown - time.Second)
	if err := watcher.Panic(); !errors.Is(err, ErrPanicTooSoon) {
		t.Errorf("panic 1 s before the cooldown ends: %v", err)
	}
	h.clock.add(time.Second)
	must(t, watcher.Panic())
	if got := h.snap(bob).PanicSeq; got != 3 {
		t.Errorf("PanicSeq %d, want 3", got)
	}
	// The cooldown belongs to the account, so a new connection does not
	// reset it.
	again := h.connect("key:carol", "carol")
	if err := again.Panic(); !errors.Is(err, ErrPanicTooSoon) {
		t.Errorf("panic after a reconnect: %v", err)
	}
	if got := len(logged()); got != 3 {
		t.Errorf("%d log lines, want 3", got)
	}

	// A player who joins later gets the count as the baseline.
	late := h.connect("key:dave", "dave")
	if got := h.snap(late).PanicSeq; got != 3 {
		t.Errorf("late joiner PanicSeq %d, want 3", got)
	}
}

func TestPanicClosedSession(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	s := h.connect("key:alice", "alice")
	s.Close()
	if err := s.Panic(); !errors.Is(err, ErrClosed) {
		t.Errorf("Panic after Close: %v", err)
	}
}

func TestListAccounts(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

	if rows, err := ListAccounts(cfg); err != nil || len(rows) != 0 {
		t.Fatalf("ListAccounts without a file: %v %v", rows, err)
	}
	if _, err := ListAccounts(Config{}); err == nil {
		t.Error("ListAccounts without a state path gives no error")
	}

	h := newHarness(t, cfg, nil)
	must(t, h.r.CreatePINAccount("zed", "4826"))
	// The welcome screen calls SetName (Enter or Esc). A key account that
	// never did is idle and is not saved (see TestIdleKeyAccountsNotSaved).
	must(t, h.connectWith("key:"+testFP("a"), "alice", ConnectOptions{}).SetName("alice"))
	must(t, h.connectWith("key:"+testFP("b"), "Bob", ConnectOptions{}).SetName("Bob"))
	h.connectWith("key:"+testFP("c"), "idle", ConnectOptions{})
	if _, err := ListAccounts(cfg); !errors.Is(err, ErrLocked) {
		t.Errorf("ListAccounts while the room runs: %v", err)
	}
	h.stop()

	rows, err := ListAccounts(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range rows {
		got = append(got, a.Name+"|"+a.Login+"|"+strings.Join(a.Fingerprints, ","))
		if a.ID == "" {
			t.Errorf("%s has no id", a.Name)
		}
	}
	want := []string{"alice||" + testFP("a"), "Bob||" + testFP("b"), "zed|zed|"}
	if !slices.Equal(got, want) {
		t.Errorf("ListAccounts:\n got %q\nwant %q", got, want)
	}
}
