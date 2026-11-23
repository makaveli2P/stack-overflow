package main

import (
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/tui"
)

func TestAlertBytes(t *testing.T) {
	cases := []struct {
		in   tui.Alert
		want string
	}{
		{tui.Alert{}, ""},
		{tui.Alert{Bell: true}, "\a"},
		{tui.Alert{Bell: true, Notify: true, Title: "Stack Overflow", Body: "Your turn — Bob raises to 60"},
			"\a\x1b]9;Stack Overflow: Your turn — Bob raises to 60\x07"},
		{tui.Alert{Notify: true, Body: "5 seconds left"}, "\x1b]9;Stack Overflow: 5 seconds left\x07"},
		{tui.Alert{Notify: true, Title: "T"}, "\x1b]9;T\x07"},
		// Control characters in the text cannot end the OSC early or
		// start a new escape.
		{tui.Alert{Notify: true, Title: "A\x07B", Body: "x\x1b]52;c;ZZZ\x07\x9b2J‮y\r\n"},
			"\x1b]9;AB: x]52;c;ZZZ2Jy\x07"},
	}
	for _, c := range cases {
		if got := string(alertBytes(c.in)); got != c.want {
			t.Errorf("alertBytes(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
	long := cleanAlertText(strings.Repeat("é", 500))
	if n := len([]rune(long)); n != maxAlertText {
		t.Errorf("long text keeps %d runes, want %d", n, maxAlertText)
	}
}

// slowWriter records writes. Each write holds the lock of the locked
// writer, so the test can see that an alert does not come inside a frame.
type slowWriter struct {
	writes chan string
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(20 * time.Millisecond)
	w.writes <- string(p)
	return len(p), nil
}

func TestLockedWriterAlert(t *testing.T) {
	sw := &slowWriter{writes: make(chan string, 10)}
	lw := newLockedWriter(sw)
	done := make(chan struct{})
	go func() {
		_, _ = lw.Write([]byte("frame-1"))
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	lw.alert(tui.Alert{Bell: true})
	lw.alert(tui.Alert{Bell: true, Notify: true, Body: "dropped while busy"})
	<-done

	got := []string{<-sw.writes, <-sw.writes}
	if got[0] != "frame-1" || got[1] != "\a" {
		t.Fatalf("writes %q", got)
	}
	select {
	case extra := <-sw.writes:
		t.Fatalf("second alert during a busy write was written: %q", extra)
	case <-time.After(100 * time.Millisecond):
	}
	// After the first alert ends, a new alert goes out.
	lw.alert(tui.Alert{Bell: true})
	select {
	case w := <-sw.writes:
		if w != "\a" {
			t.Fatalf("got %q", w)
		}
	case <-time.After(time.Second):
		t.Fatal("alert after a finished alert was not written")
	}
}
