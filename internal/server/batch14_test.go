package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testShot = "http://192.168.1.20:2280/abcdefghjk"

// shareErr calls ShareShot and returns only the error.
func shareErr(s *Session, url string) error {
	_, err := s.ShareShot(url)
	return err
}

// A shared shot is one chat line with Shot set, in every snapshot.
func TestShareShotPostsChatLine(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	carol := h.connect("key:carol", "carol")
	must(t, alice.Sit(-1))
	must(t, shareErr(alice, testShot))
	for _, s := range []*Session{alice, bob, carol} {
		chat := h.snap(s).Chat
		if len(chat) != 1 {
			t.Fatalf("%s: chat has %d lines, want 1", s.name, len(chat))
		}
		got := chat[0]
		if got.Name != "alice" || got.Text != "shared a screenshot" || got.Shot != testShot || !got.At.Equal(h.clock.now()) {
			t.Errorf("%s: line = %+v", s.name, got)
		}
	}
	// A plain message has no Shot.
	must(t, bob.Chat("nice"))
	if chat := h.snap(carol).Chat; len(chat) != 2 || chat[1].Shot != "" {
		t.Errorf("chat = %+v", chat)
	}
}

func TestShareShotRejectsBadLinks(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	bad := []string{
		"",
		"http://",
		"https://192.168.1.20:2280/s/a.png",
		"HTTP://192.168.1.20/s/a.png",
		"ftp://x/s/a.png",
		"http://x/s/a b.png",
		"http://x/s/a\tb.png",
		"http://x/s/a\nb.png",
		"http://x/s/\x1b[31mred.png",
		"http://x/s/a\x7f.png",
		"http://x/s/\u0085.png",
		"http://x/s/‮.png",
		"http://x/s/é.png",
		"http://x/s/\xff.png",
		"http://" + strings.Repeat("a", 114),
	}
	for _, url := range bad {
		if _, err := alice.ShareShot(url); !errors.Is(err, ErrBadShot) {
			t.Errorf("ShareShot(%q) = %v, want ErrBadShot", url, err)
		}
	}
	// 120 bytes is the limit, and IPv6 brackets are fine.
	ok := []string{
		"http://" + strings.Repeat("a", 113),
		"http://[fe80::1]:2280/s/abcdefghjkmnpqrs.png",
	}
	for _, url := range ok {
		if len(url) > 120 {
			t.Fatalf("test link has %d bytes", len(url))
		}
		h.clock.add(5 * time.Second)
		if _, err := alice.ShareShot(url); err != nil {
			t.Errorf("ShareShot(%q) = %v", url, err)
		}
	}
	if n := len(h.snap(alice).Chat); n != len(ok) {
		t.Errorf("chat has %d lines, want %d", n, len(ok))
	}
}

// ShareShot and Chat use one rate limit.
func TestShareShotUsesChatLimit(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	for range chatBurst - 1 {
		must(t, alice.Chat("hi"))
	}
	must(t, shareErr(alice, testShot))
	if _, err := alice.ShareShot(testShot); !errors.Is(err, errChatTooFast) {
		t.Errorf("sixth line by ShareShot: %v", err)
	}
	if err := alice.Chat("hi"); !errors.Is(err, errChatTooFast) {
		t.Errorf("sixth line by Chat: %v", err)
	}
	h.clock.add(chatWindow)
	must(t, shareErr(alice, testShot))
	if n := len(h.snap(alice).Chat); n != chatBurst+1 {
		t.Errorf("chat has %d lines, want %d", n, chatBurst+1)
	}
}

func TestShareShotClosedSession(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connect("key:alice", "alice")
	alice.Close()
	if _, err := alice.ShareShot(testShot); !errors.Is(err, ErrClosed) {
		t.Errorf("ShareShot after Close = %v, want ErrClosed", err)
	}
}

// The chat stays in memory: the state file never holds a shared link.
func TestShareShotNotInStateFile(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, nil)
	alice := h.connect("key:alice", "alice")
	must(t, alice.Sit(-1)) // Sit saves the state.
	must(t, shareErr(alice, testShot))
	must(t, alice.Stand()) // Stand saves the state again.
	h.stop()
	data, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "abcdefghjk") || strings.Contains(string(data), "shared a screenshot") {
		t.Error("the state file holds the shared shot")
	}
	h2 := newHarness(t, cfg, nil)
	a2 := h2.connect("key:alice", "alice")
	if chat := h2.snap(a2).Chat; len(chat) != 0 {
		t.Errorf("chat after restart = %+v", chat)
	}
}
