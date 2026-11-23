package main

import (
	"io"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// The server turns auto-wrap off before the first frame and on again when
// the program ends (DESIGN.md "Card stability"). A line that a client draws
// too wide then cannot wrap and leave old cells on the next row.
func TestAutoWrapOffDuringSession(t *testing.T) {
	addr := startTestServer(t, options{})
	c := openAuthClient(t, addr, "wrapper")
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var buf syncBuffer
	out, _ := s.StdoutPipe()
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, out); close(copied) }()
	in, _ := s.StdinPipe()
	if err := s.RequestPty("xterm", 30, 100, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	waitFor := func(what string, ok func(string) bool) {
		t.Helper()
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if ok(buf.String()) {
				return
			}
		}
		t.Fatalf("no %s in the output: %q", what, buf.String())
	}
	// The colour query of wish keeps the first key, as in startShell.
	time.Sleep(1500 * time.Millisecond)
	_, _ = in.Write([]byte("x"))
	waitFor("frame", func(o string) bool {
		return strings.Contains(o, "STACK OVERFLOW") || strings.Contains(o, "Stack Overflow")
	})
	o := buf.String()
	off := strings.Index(o, autoWrapOff)
	alt := strings.Index(o, "\x1b[?1049h")
	if off < 0 || alt < 0 || off > alt {
		t.Fatalf("auto-wrap off at %d, alt screen at %d: want auto-wrap off before the first frame\n%q", off, alt, o)
	}

	// Ctrl+C quits, because no hand runs.
	_, _ = in.Write([]byte("\x03"))
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end after Ctrl+C")
	}
	<-copied
	o = buf.String()
	on := strings.LastIndex(o, autoWrapOn)
	leave := strings.LastIndex(o, "\x1b[?1049l")
	if on < 0 || leave < 0 || on < leave {
		t.Fatalf("auto-wrap on at %d, alt screen exit at %d: want auto-wrap on after the program ends\n%q", on, leave, o[max(0, len(o)-400):])
	}
	if strings.Count(o, autoWrapOff) != 1 {
		t.Errorf("auto-wrap off sent %d times, want 1", strings.Count(o, autoWrapOff))
	}
}
