package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// startTestServer starts a room and an SSH server on a free local port and
// returns the server address.
func startTestServer(t *testing.T, o options) string {
	t.Helper()
	room, err := server.NewRoom(server.Config{Seats: 4, SmallBlind: 5, BigBlind: 10, StartingStack: 1000, NextHandDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o.host, o.port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	srv, err := newSSHServer(o, filepath.Join(t.TempDir(), "hk"), room)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); cancel() })
	return ln.Addr().String()
}

// pinAnswers answers each keyboard-interactive prompt with the PIN 4826. A
// new player chooses that PIN (two prompts); a known player types it.
func pinAnswers(_, _ string, questions []string, _ []bool) ([]string, error) {
	answers := make([]string, len(questions))
	for i := range answers {
		answers[i] = "4826"
	}
	return answers, nil
}

// openAuthClient dials addr as user with a PIN login (keyboard-interactive).
func openAuthClient(t *testing.T, addr, user string) *gossh.Client {
	t.Helper()
	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.KeyboardInteractive(pinAnswers)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	c, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// keyAuthClient dials addr as user with a new SSH key.
func keyAuthClient(t *testing.T, addr, user string) *gossh.Client {
	t.Helper()
	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(newKey(t))},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	c, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// startShell opens a PTY session with a shell, drains its output and returns
// the session input. It waits until the colour query of wish times out (1 s)
// and gives its leaked reader one key to eat, as a real client does.
func startShell(t *testing.T, c *gossh.Client) (*gossh.Session, io.WriteCloser) {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := s.StdoutPipe()
	go func() { _, _ = io.Copy(io.Discard, out) }()
	in, _ := s.StdinPipe()
	if err := s.RequestPty("xterm", 30, 100, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	_, _ = in.Write([]byte("x"))
	time.Sleep(300 * time.Millisecond)
	return s, in
}

// The TUI reads the SSH input with the Bubble Tea reader. For a run of
// printable runes without a space or control byte, or for a bracketed paste
// without its end marker, that reader keeps every byte and decodes the whole
// buffer again on each 256-byte read: O(n^2) CPU. Before the input guard, a
// 4 MB run from one client pinned a host core for minutes. Now the server
// closes the connection after a short run.
func TestLongInputFloodClosesConnectionQuickly(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		chunk  string
	}{
		{"run of runes", "", "a"},
		{"unterminated bracketed paste", pasteStart, "a b "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := startTestServer(t, options{})
			_, in := startShell(t, openAuthClient(t, addr, "flooder"))

			// 4 MB: about 2 MB fit in the SSH channel window; the server
			// must read the rest before the writes return.
			const total = 4 << 20
			done := make(chan error, 1)
			go func() {
				if _, err := in.Write([]byte(tc.prefix)); err != nil {
					done <- err
					return
				}
				chunk := []byte(strings.Repeat(tc.chunk, (32<<10)/len(tc.chunk)))
				for sent := 0; sent < total; sent += len(chunk) {
					if _, err := in.Write(chunk); err != nil {
						done <- err // the server closed the session
						return
					}
				}
				done <- nil
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("the server accepted %d bytes of %s from one client and kept the session open", total, tc.name)
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("after 15s the server still parses a %d-byte %s from one client "+
					"(quadratic input buffering; a host CPU core is pinned meanwhile)", total, tc.name)
			}
		})
	}
}

// Normal typing stays far below the input limits and keeps the session open.
func TestNormalTypingKeepsSessionOpen(t *testing.T) {
	addr := startTestServer(t, options{})
	s, in := startShell(t, openAuthClient(t, addr, "typist"))
	ended := make(chan error, 1)
	go func() { ended <- s.Wait() }()

	keys := []string{"?", "?", "\t", "t", "hello there, how is the game going today?", "\r", "\x1b[A", "\x1b[B"}
	for _, k := range keys {
		if _, err := in.Write([]byte(k)); err != nil {
			t.Fatalf("write %q: %v", k, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-ended:
		t.Fatalf("the server ended a session with normal typing: %v", err)
	case <-time.After(time.Second):
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// The SSH user name, the TERM value, the command and the client version
// come from the client. The connection log must not print them raw to the
// host terminal, or any player can send escape sequences (clear screen,
// change the title, OSC 52 clipboard writes) to the terminal of the person
// who runs the server.
func TestClientEscapesDoNotReachHostLog(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	addr := startTestServer(t, options{})
	cfg := &gossh.ClientConfig{
		User:            "x\x1b]52;c;ZWNobyBwd25lZA==\x07\x1b[2J", // OSC 52 clipboard write + clear screen
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(newKey(t))},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	c, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.RequestPty("xterm\x1b]0;TITLE\x07", 24, 80, gossh.TerminalModes{})
	_ = s.Shell()
	deadline := time.Now().Add(5 * time.Second)
	// Wait for the connect line of this client. A disconnect line of an
	// earlier test can also come.
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "INFO connect user=x") {
		time.Sleep(50 * time.Millisecond)
	}
	out := buf.String()
	if !strings.Contains(out, "connect") {
		t.Fatalf("no connect line in the host log: %q", out)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("host log contains raw escape bytes from the client: %q", out)
	}
	if !strings.Contains(out, `\x1b]52`) || !strings.Contains(out, `xterm\x1b]0;TITLE\a`) {
		t.Fatalf("host log does not show the escaped user name and TERM: %q", out)
	}
}

func TestLogSafe(t *testing.T) {
	cases := map[string]string{
		"alice":                  "alice",
		"bob smith":              "bob smith",
		"x\x1b[2J\x07":           `x\x1b[2J\a`,
		"ünïcødé♠":               "ünïcødé♠",
		"a\u202eb":               `a\u202eb`, // right-to-left override
		"\x9b31m":                `\x9b31m`,
		strings.Repeat("a", 150): strings.Repeat("a", maxLogField) + "...",
	}
	for in, want := range cases {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// waitForServerClose reads from conn until the server closes it or wait
// passes. It returns true when the server closed the connection.
func waitForServerClose(t *testing.T, conn net.Conn, wait time.Duration) bool {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 256)
	for {
		if _, err := conn.Read(buf); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false
			}
			return true
		}
	}
}

// A TCP client that connects and then sends nothing must not keep a socket
// and goroutines on the host for ever, also in -password mode. OpenSSH drops
// such a client after LoginGraceTime (120 s by default). This test allows
// 130 s with the default grace.
func TestSilentConnectionIsDroppedAfterDefaultLoginGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits past the default login grace time")
	}
	t.Parallel()
	addr := startTestServer(t, options{password: "secret"})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const wait = 130 * time.Second
	if !waitForServerClose(t, conn, wait) {
		t.Fatalf("the server kept a connection that never started the SSH handshake open for %v", wait)
	}
}

// The same check with a short grace time, so that it runs fast.
func TestSilentConnectionIsDroppedAfterLoginGrace(t *testing.T) {
	t.Parallel()
	for _, pw := range []string{"", "secret"} {
		addr := startTestServer(t, options{password: pw, loginGrace: 500 * time.Millisecond})
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if !waitForServerClose(t, conn, 5*time.Second) {
			t.Fatalf("password %q: the server kept a silent connection open past the login grace", pw)
		}
	}
}

// The login grace ends when the session starts: a quiet player or spectator
// stays connected after the grace time.
func TestQuietSessionOutlivesLoginGrace(t *testing.T) {
	t.Parallel()
	addr := startTestServer(t, options{loginGrace: 500 * time.Millisecond})
	// A key login: a PIN login can take longer than this short grace,
	// because the server hashes the new PIN with bcrypt.
	s, _ := startShell(t, keyAuthClient(t, addr, "quiet"))
	ended := make(chan error, 1)
	go func() { ended <- s.Wait() }()
	select {
	case err := <-ended:
		t.Fatalf("the server ended a quiet session after the login grace: %v", err)
	case <-time.After(2 * time.Second):
	}
}

// A new player without a key chooses a PIN; the next connection needs that
// PIN. The room keeps the PIN account.
func TestPINLoginWithRoom(t *testing.T) {
	addr := startTestServer(t, options{})
	s, _ := startShell(t, openAuthClient(t, addr, "Pat"))
	_ = s.Close()

	wrong := &kiAnswers{answers: []string{"9999", "9999", "9999"}}
	if _, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "pat",
		Auth:            []gossh.AuthMethod{gossh.KeyboardInteractive(wrong.challenge)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}); err == nil {
		t.Fatal("a wrong PIN logs in")
	}
	if len(wrong.prompts) == 0 || wrong.prompts[0] != `PIN for "pat": ` {
		t.Fatalf("prompts %q", wrong.prompts)
	}
	c := openAuthClient(t, addr, "PAT")
	_, _ = startShell(t, c)
}
