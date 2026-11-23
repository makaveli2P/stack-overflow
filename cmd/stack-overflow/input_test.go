package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// chunkReader returns one chunk on each Read, like a stream of SSH data
// packets.
type chunkReader struct{ chunks []string }

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

// testGuard makes a guard with small limits and a clock that the test moves.
// A wait moves the clock and adds to *waited.
func testGuard(chunks []string, clock *time.Time) (*inputGuard, *[]string) {
	g, reasons, _ := testGuardWaits(chunks, clock)
	return g, reasons
}

func testGuardWaits(chunks []string, clock *time.Time) (*inputGuard, *[]string, *time.Duration) {
	var reasons []string
	var waited time.Duration
	g := newInputGuard(&chunkReader{chunks: append([]string(nil), chunks...)}, nil, func(r string) { reasons = append(reasons, r) })
	g.maxRun, g.maxBurst, g.rate = 8, 64, 32
	g.now = func() time.Time { return *clock }
	g.wait = func(d time.Duration, _ <-chan struct{}) bool {
		waited += d
		*clock = clock.Add(d)
		return true
	}
	return g, &reasons, &waited
}

// readAll reads until an error and returns the bytes that the guard let
// through and the last error.
func readAll(g *inputGuard) (string, error) {
	var out bytes.Buffer
	buf := make([]byte, 256)
	for {
		n, err := g.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			return out.String(), err
		}
	}
}

func TestInputGuardPassesNormalInput(t *testing.T) {
	clock := time.Unix(1000, 0)
	in := []string{"t", "hello ", "world\r", "\x1b[A", "\x1b[200~a\x1b[201~", "12345678 "}
	want := strings.Join(in, "")
	g, reasons := testGuard(in, &clock)
	got, err := readAll(g)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF (reasons %q)", err, *reasons)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(*reasons) != 0 {
		t.Fatalf("onFlood called: %q", *reasons)
	}
}

func TestInputGuardStopsLongRunOfRunes(t *testing.T) {
	cases := map[string][]string{
		"one read":    {"aaaaaaaaa"},
		"split reads": {"aaaa", "aaaa", "a"},
		"utf-8 runes": {"♠♠♠"},
		"after space": {"aa aaaaaaaaa"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			clock := time.Unix(1000, 0)
			g, reasons := testGuard(in, &clock)
			_, err := readAll(g)
			if !errors.Is(err, errInputFlood) {
				t.Fatalf("err = %v, want errInputFlood", err)
			}
			if len(*reasons) != 1 || (*reasons)[0] != "run of characters too long" {
				t.Fatalf("reasons = %q", *reasons)
			}
			// Later reads fail too and do not call onFlood again.
			if _, err := g.Read(make([]byte, 8)); !errors.Is(err, errInputFlood) {
				t.Fatalf("later read: err = %v", err)
			}
			if len(*reasons) != 1 {
				t.Fatalf("onFlood called %d times", len(*reasons))
			}
		})
	}
}

func TestInputGuardStopsLongPaste(t *testing.T) {
	cases := map[string][]string{
		"spaces inside paste": {pasteStart + "a b c d e"},
		"marker split":        {"\x1b[2", "00~", "a b ", "c d e"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			clock := time.Unix(1000, 0)
			g, reasons := testGuard(in, &clock)
			if _, err := readAll(g); !errors.Is(err, errInputFlood) {
				t.Fatalf("err = %v, want errInputFlood", err)
			}
			if len(*reasons) != 1 || (*reasons)[0] != "paste too long" {
				t.Fatalf("reasons = %q", *reasons)
			}
		})
	}
}

func TestInputGuardEndsPasteAtEndMarker(t *testing.T) {
	clock := time.Unix(1000, 0)
	in := []string{pasteStart + "a b", "\x1b[20", "1~ c d e f g h"}
	g, reasons := testGuard(in, &clock)
	if _, err := readAll(g); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF (reasons %q)", err, *reasons)
	}
}

// Input faster than the rate, after the burst, is delayed, not refused.
func TestInputGuardDelaysFastInput(t *testing.T) {
	clock := time.Unix(1000, 0)
	// 96 bytes, 96 messages ("a" and " " alternate): no long run, but 32
	// messages more than the burst of 64. At 32 messages per second that is
	// a one-second delay.
	in := strings.Repeat("a ", 48)
	g, reasons, waited := testGuardWaits([]string{in}, &clock)
	got, err := readAll(g)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF (reasons %q)", err, *reasons)
	}
	if got != in {
		t.Fatalf("got %q, want %q", got, in)
	}
	if *waited != time.Second {
		t.Fatalf("waited %v, want 1s", *waited)
	}
}

func TestInputGuardBurstFillsAgain(t *testing.T) {
	clock := time.Unix(1000, 0)
	g, reasons, waited := testGuardWaits(nil, &clock)
	// n bytes of "a a a ..." are n messages.
	send := func(n int) {
		t.Helper()
		g.r = &chunkReader{chunks: []string{strings.Repeat("a ", n/2)}}
		if _, err := readAll(g); !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v (reasons %q)", err, *reasons)
		}
	}
	// The full burst of 64 messages goes through at once.
	send(64)
	// One second later, 32 more messages are allowed (30 here).
	clock = clock.Add(time.Second)
	send(30)
	if *waited != 0 {
		t.Fatalf("waited %v inside the allowance", *waited)
	}
	// Right after, 6 messages are 4 over the allowance.
	send(6)
	if want := 4 * time.Second / 32; *waited != want {
		t.Fatalf("waited %v, want %v", *waited, want)
	}
}

// Runs of runes and pastes are few messages, so they use little of the
// allowance; maxInputRun limits them.
func TestInputGuardChargesRunsAsFewMessages(t *testing.T) {
	clock := time.Unix(1000, 0)
	var in []string
	for i := 0; i < 20; i++ {
		in = append(in, "aaaaaaa ", pasteStart+"b c"+pasteEnd)
	}
	g, reasons, waited := testGuardWaits(in, &clock)
	if _, err := readAll(g); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v (reasons %q)", err, *reasons)
	}
	// "aaaaaaa " has 1 break and costs 3; the paste has 2 breaks (the ESC
	// of the start marker and the end) and costs 5. 20*(3+5) = 160 is 96
	// over the burst of 64: 3 s at 32 per second. A charge by bytes,
	// 20*(8+15) = 460, would wait 12.375 s.
	if *waited != 3*time.Second {
		t.Fatalf("waited %v, want 3s", *waited)
	}
}

// A delay stops when the session ends.
func TestInputGuardDelayStopsWhenSessionEnds(t *testing.T) {
	done := make(chan struct{})
	close(done)
	g := newInputGuard(strings.NewReader(strings.Repeat("ab ", 400)), done, nil)
	start := time.Now()
	_, err := readAll(g)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("read took %v after the session ended", d)
	}
}
