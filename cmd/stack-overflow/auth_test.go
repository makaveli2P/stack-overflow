package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// fakeStore is a pinStore in memory.
type fakeStore struct {
	mu     sync.Mutex
	pins   map[string]string
	checks int
	addrs  []string // the addr of each CheckPINFrom
	locked bool
}

func newFakeStore(pins map[string]string) *fakeStore {
	if pins == nil {
		pins = map[string]string{}
	}
	return &fakeStore{pins: pins}
}

func (f *fakeStore) PINAccountExists(login string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.pins[login]
	return ok
}

func (f *fakeStore) CheckPINFrom(login, pin, addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	f.addrs = append(f.addrs, addr)
	if f.locked {
		return server.ErrPINLocked
	}
	if p, ok := f.pins[login]; !ok || p != pin {
		return server.ErrBadPIN
	}
	return nil
}

func (f *fakeStore) CreatePINAccount(login, pin string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !validPIN(pin) {
		return server.ErrPINFormat
	}
	if _, ok := f.pins[login]; ok {
		return server.ErrNameInUse
	}
	f.pins[login] = pin
	return nil
}

func (f *fakeStore) pin(login string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pins[login]
	return p, ok
}

func (f *fakeStore) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// startAuthServer starts an SSH server with the auth rules of a and a
// session handler that writes the identity of the connection and closes.
func startAuthServer(t *testing.T, a *auth, limiter *connLimiter) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if limiter == nil {
		limiter = newConnLimiter(0, 0)
	}
	opts := []ssh.Option{
		wish.WithHostKeyPath(filepath.Join(t.TempDir(), "hk")),
		withConnGuards(limiter, time.Minute),
		wish.WithMiddleware(func(ssh.Handler) ssh.Handler {
			return func(s ssh.Session) {
				id, _, ok := identity(s.Permissions().Permissions)
				fmt.Fprintf(s, "id=%s ok=%v", id, ok)
			}
		}),
	}
	srv, err := wish.NewServer(append(opts, a.options()...)...)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// kiAnswers is a keyboard-interactive client that gives the answers in
// order and records each prompt and instruction.
type kiAnswers struct {
	mu      sync.Mutex
	answers []string
	prompts []string
	notes   []string
}

func (k *kiAnswers) challenge(_, instruction string, questions []string, echos []bool) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.notes = append(k.notes, instruction)
	out := make([]string, len(questions))
	for i, q := range questions {
		if echos[i] {
			return nil, fmt.Errorf("prompt %q echoes the answer", q)
		}
		k.prompts = append(k.prompts, q)
		if len(k.answers) == 0 {
			return nil, errors.New("no more answers")
		}
		out[i], k.answers = k.answers[0], k.answers[1:]
	}
	return out, nil
}

// dialID connects as user with the auth methods and returns the identity
// that the session reports.
func dialID(t *testing.T, addr, user string, methods ...gossh.AuthMethod) (string, error) {
	t.Helper()
	c, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return "", err
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	out, err := s.Output("")
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}

func newKey(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewPlayerChoosesPIN(t *testing.T) {
	store := newFakeStore(nil)
	addr := startAuthServer(t, &auth{store: store}, nil)
	ki := &kiAnswers{answers: []string{"4826", "4826"}}
	got, err := dialID(t, addr, "  Alice ", gossh.KeyboardInteractive(ki.challenge))
	if err != nil {
		t.Fatal(err)
	}
	if got != "id=pin:alice ok=true" {
		t.Fatalf("session reports %q", got)
	}
	want := []string{`New player "alice". Choose a 4-digit PIN: `, "Type the PIN again: "}
	if strings.Join(ki.prompts, "|") != strings.Join(want, "|") {
		t.Fatalf("prompts %q, want %q", ki.prompts, want)
	}
	if p, ok := store.pin("alice"); !ok || p != "4826" {
		t.Fatalf("store has PIN %q (%v)", p, ok)
	}
}

func TestNewPlayerPINMistakesUseTries(t *testing.T) {
	store := newFakeStore(nil)
	addr := startAuthServer(t, &auth{store: store}, nil)
	// A bad format, then two PINs that differ, then a good PIN.
	ki := &kiAnswers{answers: []string{"12a4", "1357", "2468", "6284", "6284"}}
	got, err := dialID(t, addr, "bob", gossh.KeyboardInteractive(ki.challenge))
	if err != nil {
		t.Fatal(err)
	}
	if got != "id=pin:bob ok=true" {
		t.Fatalf("session reports %q", got)
	}
	notes := strings.Join(ki.notes, "|")
	if !strings.Contains(notes, textPINFormat) || !strings.Contains(notes, textPINMatch) {
		t.Fatalf("instructions %q do not explain the mistakes", ki.notes)
	}

	// Three mistakes end the connection.
	ki = &kiAnswers{answers: []string{"1", "2", "3", "5555", "5555"}}
	if _, err := dialID(t, addr, "carol", gossh.KeyboardInteractive(ki.challenge)); err == nil {
		t.Fatal("login after three bad PINs works")
	}
	if _, ok := store.pin("carol"); ok {
		t.Fatal("store made an account after three bad PINs")
	}
}

func TestExistingPlayerPIN(t *testing.T) {
	store := newFakeStore(map[string]string{"dave": "2468"})
	addr := startAuthServer(t, &auth{store: store}, nil)

	ki := &kiAnswers{answers: []string{"0000", "2468"}}
	got, err := dialID(t, addr, "Dave", gossh.KeyboardInteractive(ki.challenge))
	if err != nil {
		t.Fatal(err)
	}
	if got != "id=pin:dave ok=true" {
		t.Fatalf("session reports %q", got)
	}
	if ki.prompts[0] != `PIN for "dave": ` {
		t.Fatalf("prompt %q", ki.prompts[0])
	}
	if !strings.Contains(strings.Join(ki.notes, "|"), textWrongPIN) {
		t.Fatalf("instructions %q do not say the PIN was wrong", ki.notes)
	}

	// At most three PIN tries for one connection, also when the client
	// starts keyboard-interactive again and also uses the password method.
	before := store.checkCount()
	ki = &kiAnswers{answers: strings.Fields("1 2 3 4 5 6 7 8 9 10")}
	_, err = dialID(t, addr, "dave",
		gossh.RetryableAuthMethod(gossh.KeyboardInteractive(ki.challenge), 3),
		gossh.RetryableAuthMethod(gossh.Password("2468"), 3))
	if err == nil {
		t.Fatal("login works after three wrong PINs")
	}
	if n := store.checkCount() - before; n != maxPINTries {
		t.Fatalf("server checked %d PINs on one connection, want %d", n, maxPINTries)
	}
}

func TestLockedPINStops(t *testing.T) {
	store := newFakeStore(map[string]string{"erin": "1357"})
	store.locked = true
	addr := startAuthServer(t, &auth{store: store}, nil)
	ki := &kiAnswers{answers: []string{"1357", "1357", "1357"}}
	if _, err := dialID(t, addr, "erin", gossh.KeyboardInteractive(ki.challenge)); err == nil {
		t.Fatal("login works while the PIN is locked")
	}
	if store.checkCount() != 1 {
		t.Fatalf("server checked %d PINs after a lock, want 1", store.checkCount())
	}
	if !strings.Contains(strings.Join(ki.notes, "|"), textPINLocked) {
		t.Fatalf("instructions %q do not explain the lock", ki.notes)
	}
}

func TestPasswordMethodForExistingPIN(t *testing.T) {
	store := newFakeStore(map[string]string{"frank": "9999"})
	addr := startAuthServer(t, &auth{store: store}, nil)
	got, err := dialID(t, addr, "frank", gossh.Password("9999"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "id=pin:frank ok=true" {
		t.Fatalf("session reports %q", got)
	}
	if _, err := dialID(t, addr, "frank", gossh.Password("1111")); err == nil {
		t.Fatal("password method accepts a wrong PIN")
	}
	// A new player cannot choose a PIN with the password method.
	if _, err := dialID(t, addr, "newbie", gossh.Password("4826")); err == nil {
		t.Fatal("password method makes a new account")
	}
	if _, ok := store.pin("newbie"); ok {
		t.Fatal("password method made an account")
	}
}

func TestBadLoginName(t *testing.T) {
	addr := startAuthServer(t, &auth{store: newFakeStore(nil)}, nil)
	ki := &kiAnswers{answers: []string{"4826", "4826"}}
	if _, err := dialID(t, addr, strings.Repeat("x", 33), gossh.KeyboardInteractive(ki.challenge)); err == nil {
		t.Fatal("a 33-rune login works")
	}
	if len(ki.prompts) != 0 || !strings.Contains(strings.Join(ki.notes, "|"), "1 to 32") {
		t.Fatalf("prompts %q, instructions %q", ki.prompts, ki.notes)
	}
}

func TestKeyLogin(t *testing.T) {
	store := newFakeStore(nil)
	addr := startAuthServer(t, &auth{store: store}, nil)
	key := newKey(t)
	got, err := dialID(t, addr, "gina", gossh.PublicKeys(key))
	if err != nil {
		t.Fatal(err)
	}
	if want := "id=key:" + gossh.FingerprintSHA256(key.PublicKey()) + " ok=true"; got != want {
		t.Fatalf("session reports %q, want %q", got, want)
	}
	if store.checkCount() != 0 {
		t.Fatal("a key login checked a PIN")
	}
}

func TestSharedPassword(t *testing.T) {
	store := newFakeStore(map[string]string{"hal": "1111"})
	addr := startAuthServer(t, &auth{password: "secret", store: store}, nil)
	key := newKey(t)
	keyID := "id=key:" + gossh.FingerprintSHA256(key.PublicKey()) + " ok=true"

	// A key alone is not enough.
	if _, err := dialID(t, addr, "ivy", gossh.PublicKeys(key)); err == nil {
		t.Fatal("key login works without the shared password")
	}
	// Key, then the password (both methods).
	got, err := dialID(t, addr, "ivy", gossh.PublicKeys(key), gossh.Password("secret"))
	if err != nil || got != keyID {
		t.Fatalf("key + password: %q, %v", got, err)
	}
	ki := &kiAnswers{answers: []string{"secret"}}
	got, err = dialID(t, addr, "ivy", gossh.PublicKeys(key), gossh.KeyboardInteractive(ki.challenge))
	if err != nil || got != keyID {
		t.Fatalf("key + keyboard-interactive: %q, %v", got, err)
	}

	// No key: the shared password, then the PIN.
	ki = &kiAnswers{answers: []string{"secret", "1111"}}
	got, err = dialID(t, addr, "hal", gossh.KeyboardInteractive(ki.challenge))
	if err != nil || got != "id=pin:hal ok=true" {
		t.Fatalf("password + PIN: %q, %v", got, err)
	}
	if ki.prompts[0] != "Table password: " || ki.prompts[1] != `PIN for "hal": ` {
		t.Fatalf("prompts %q", ki.prompts)
	}
	// A wrong shared password stops before the PIN.
	ki = &kiAnswers{answers: []string{"guess", "1111"}}
	if _, err := dialID(t, addr, "hal", gossh.KeyboardInteractive(ki.challenge)); err == nil {
		t.Fatal("login works with a wrong shared password")
	}
	// The password method gives the shared password; the PIN follows.
	ki = &kiAnswers{answers: []string{"1111"}}
	got, err = dialID(t, addr, "hal", gossh.Password("secret"), gossh.KeyboardInteractive(ki.challenge))
	if err != nil || got != "id=pin:hal ok=true" {
		t.Fatalf("password method + PIN: %q, %v", got, err)
	}
	// The PIN alone is not the shared password.
	if _, err := dialID(t, addr, "hal", gossh.Password("1111")); err == nil {
		t.Fatal("the PIN works as the shared password")
	}
}

func TestConnectionLimits(t *testing.T) {
	limiter := newConnLimiter(3, 2)
	addr := startAuthServer(t, &auth{store: newFakeStore(nil)}, limiter)
	dial := func() net.Conn {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	// readBanner reads what the server sends first.
	readBanner := func(c net.Conn) string {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b, _ := io.ReadAll(io.LimitReader(c, 200))
		return string(b)
	}
	a, b := dial(), dial()
	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 7)
	if _, err := io.ReadFull(a, buf); err != nil || string(buf) != "SSH-2.0" {
		t.Fatalf("first connection: %q, %v", buf, err)
	}
	if got := readBanner(dial()); got != textIPFull {
		t.Fatalf("third connection from one address got %q", got)
	}
	// Closing a connection frees its place.
	_ = b.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c := dial()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := io.ReadFull(c, buf)
		if string(buf[:n]) == "SSH-2.0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed connection keeps its place in the limit")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The total limit.
	l := newConnLimiter(2, 5)
	if l.acquire("1.1.1.1") != "" || l.acquire("2.2.2.2") != "" {
		t.Fatal("limiter refuses connections under the limit")
	}
	if got := l.acquire("3.3.3.3"); got != textServerFull {
		t.Fatalf("total limit: %q", got)
	}
	l.release("1.1.1.1")
	if l.acquire("3.3.3.3") != "" {
		t.Fatal("limiter keeps a released place")
	}
}

func TestPinLogin(t *testing.T) {
	cases := map[string]string{
		"Alice":                 "alice",
		"  bob ":                "bob",
		"ÉLodie":                "élodie",
		strings.Repeat("a", 32): strings.Repeat("a", 32),
		strings.Repeat("a", 33): "",
		"":                      "",
		"   ":                   "",
		"x\x1b[2J":              "",
		"tab\tname":             "",
	}
	for in, want := range cases {
		got, ok := pinLogin(in)
		if got != want || ok != (want != "") {
			t.Errorf("pinLogin(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestIdentity(t *testing.T) {
	if _, _, ok := identity(nil); ok {
		t.Fatal("nil permissions give an identity")
	}
	id, keyless, ok := identity(&gossh.Permissions{Extensions: map[string]string{keyExt: "SHA256:abc"}})
	if id != "key:SHA256:abc" || keyless || !ok {
		t.Fatalf("key: %q %v %v", id, keyless, ok)
	}
	id, keyless, ok = identity(&gossh.Permissions{Extensions: map[string]string{pinExt: "zoe"}})
	if id != "pin:zoe" || !keyless || !ok {
		t.Fatalf("pin: %q %v %v", id, keyless, ok)
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:5000":   true,
		"[::1]:22":         true,
		"192.168.1.9:4000": false,
	} {
		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := isLoopback(a); got != want {
			t.Errorf("isLoopback(%s) = %v", addr, got)
		}
	}
}

// After the last PIN try, after a lock, or for a login that can never work,
// the server closes the connection. Before this rule, the server offered the
// password method again, and OpenSSH asked for a "password" that could never
// work. (The Go client still calls its password callback after the close, so
// the test checks the close itself: EOF, not an auth failure.)
func TestHangUpAfterLastTry(t *testing.T) {
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: login works", name)
		}
		if !strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "unable to authenticate") {
			t.Errorf("%s: server did not close the connection: %v", name, err)
		}
	}
	for _, locked := range []bool{false, true} {
		store := newFakeStore(map[string]string{"gina": "6284"})
		store.locked = locked
		addr := startAuthServer(t, &auth{store: store}, nil)
		ki := &kiAnswers{answers: strings.Fields("1 2 3 4 5 6")}
		_, err := dialID(t, addr, "gina",
			gossh.RetryableAuthMethod(gossh.KeyboardInteractive(ki.challenge), 3),
			gossh.RetryableAuthMethod(gossh.Password("6284"), 3))
		check(fmt.Sprintf("keyboard-interactive, locked=%v", locked), err)
	}

	// Three wrong PINs with the password method alone.
	store := newFakeStore(map[string]string{"gina": "6284"})
	addr := startAuthServer(t, &auth{store: store}, nil)
	_, err := dialID(t, addr, "gina", gossh.RetryableAuthMethod(gossh.Password("0000"), 5))
	check("password method", err)
	if n := store.checkCount(); n != maxPINTries {
		t.Errorf("server checked %d PINs, want %d", n, maxPINTries)
	}

	addr = startAuthServer(t, &auth{store: newFakeStore(nil)}, nil)
	_, err = dialID(t, addr, strings.Repeat("x", 40), gossh.KeyboardInteractive((&kiAnswers{}).challenge))
	check("bad login", err)
}
