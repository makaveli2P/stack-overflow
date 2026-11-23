package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A crash during a hand must not make the next server run deal the same
// hand number again (Card stability: one HandNumber is one deal). The room
// saves the hand number when it deals.
func TestHandNumberSavedAtDeal(t *testing.T) {
	cfg := manualConfig()
	dir := t.TempDir()
	cfg.StatePath = filepath.Join(dir, "state.json")
	h := newHarness(t, cfg, stackedDeck(splitDeck))
	alice, _ := startManual(t, h)
	dealt := h.snap(alice).Table.HandNumber
	if dealt != 1 {
		t.Fatalf("first hand is #%d, want #1", dealt)
	}
	// The file as it is during the hand: a crash now leaves this file.
	data, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	crashed := filepath.Join(dir, "crashed.json")
	if err := os.WriteFile(crashed, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h.stop()

	cfg.StatePath = crashed
	h = newHarness(t, cfg, stackedDeck(splitDeck))
	alice = h.connect("key:alice", "alice")
	bob := h.connect("key:bob", "bob")
	must(t, alice.Sit(-1))
	must(t, bob.Sit(-1))
	must(t, alice.Start())
	h.advance(startDelayCap + time.Second)
	snap := h.snap(alice)
	if !snap.Table.InHand {
		t.Fatal("no hand after the restart")
	}
	if snap.Table.HandNumber <= dealt {
		t.Fatalf("hand #%d after the restart, want a number greater than #%d", snap.Table.HandNumber, dealt)
	}
}
