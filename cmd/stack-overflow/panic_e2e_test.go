package main

import (
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// openTable logs in to addr as user with a new key, saves the suggested
// name and returns the screen output and the input of the session.
func openTable(t *testing.T, addr, user string) (*syncBuffer, func(string)) {
	t.Helper()
	c := keyAuthClient(t, addr, user)
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	out := &syncBuffer{}
	s.Stdout = out
	in, _ := s.StdinPipe()
	if err := s.RequestPty("xterm", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the colour query of wish times out
	_, _ = in.Write([]byte("x"))        // its leaked reader eats one key
	time.Sleep(300 * time.Millisecond)
	_, _ = in.Write([]byte("\r")) // keep the suggested name
	time.Sleep(300 * time.Millisecond)
	return out, func(k string) { _, _ = in.Write([]byte(k)) }
}

// waitFor waits until out contains want.
func waitFor(t *testing.T, out *syncBuffer, want string, d time.Duration) bool {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if strings.Contains(out.String(), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// Ctrl+H (the byte 0x08) from one client shows the panic screen on every
// client: the notice, then the decoy.
func TestPanicKeyOverSSH(t *testing.T) {
	addr := startTestServer(t, options{})
	aOut, aKey := openTable(t, addr, "anna")
	bOut, _ := openTable(t, addr, "ben")
	if !waitFor(t, aOut, "anna", 2*time.Second) || !waitFor(t, bOut, "ben", 2*time.Second) {
		t.Fatal("the tables did not open")
	}
	aKey("\x08")
	for name, out := range map[string]*syncBuffer{"anna": aOut, "ben": bOut} {
		if !waitFor(t, out, "Shh! Someone is coming!", 2*time.Second) {
			t.Errorf("%s: no panic notice", name)
		}
	}
	for name, out := range map[string]*syncBuffer{"anna": aOut, "ben": bOut} {
		if !waitFor(t, out, "go test -v ./...", 3*time.Second) {
			t.Errorf("%s: no decoy", name)
		}
	}
}

// B7-3: in the chat, 0x08 (Ctrl+Backspace in many terminals) deletes a
// character and starts no panic. F20 (ESC [34~, the Cmd+H mapping of the
// README) starts the panic also in the chat.
func TestPanicKeyInChatOverSSH(t *testing.T) {
	addr := startTestServer(t, options{})
	aOut, aKey := openTable(t, addr, "anna")
	bOut, _ := openTable(t, addr, "ben")
	if !waitFor(t, aOut, "anna", 2*time.Second) || !waitFor(t, bOut, "ben", 2*time.Second) {
		t.Fatal("the tables did not open")
	}
	aKey("t")
	time.Sleep(200 * time.Millisecond)
	aKey("xyz")
	time.Sleep(200 * time.Millisecond)
	aKey("\x08")
	if waitFor(t, bOut, "Shh! Someone is coming!", time.Second) {
		t.Fatal("0x08 in the chat started a panic")
	}
	aKey("\r") // send "xy"
	if !waitFor(t, bOut, "xy", 2*time.Second) || strings.Contains(bOut.String(), "xyz") {
		t.Error("0x08 in the chat did not delete one character")
	}
	aKey("t")
	time.Sleep(200 * time.Millisecond)
	aKey("\x1b[34~")
	for name, out := range map[string]*syncBuffer{"anna": aOut, "ben": bOut} {
		if !waitFor(t, out, "Shh! Someone is coming!", 2*time.Second) {
			t.Errorf("%s: F20 in the chat started no panic", name)
		}
	}
}

// The fingerprint that the SSH layer gives a key login is a valid -admin
// entry, unchanged.
func TestAdminAcceptsSSHFingerprint(t *testing.T) {
	for range 20 {
		fp := gossh.FingerprintSHA256(newKey(t).PublicKey())
		got, err := parseAdmins(fp)
		if err != nil || len(got) != 1 || got[0] != fp {
			t.Fatalf("parseAdmins(%q) = %q, %v", fp, got, err)
		}
	}
}
