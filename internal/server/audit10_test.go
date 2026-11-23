package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Batch 10 audit (P3): a reset while the server runs does nothing, because
// the running room writes all banks back at its next save. ResetState
// refuses then. After the stop, it moves the file away and keeps it, and
// the next room starts with no players.
func TestResetState(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if _, err := ResetState(cfg); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reset without a file: %v", err)
	}

	h := newHarness(t, cfg, nil)
	a := h.connect("key:a", "alice")
	must(t, a.Sit(-1))
	if _, err := ResetState(cfg); !errors.Is(err, ErrLocked) {
		t.Fatalf("reset while the server runs: %v", err)
	}
	h.stop()
	before, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}

	dst, err := ResetState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(cfg.StatePath) {
		t.Fatal("the state file is still there after the reset")
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(before) {
		t.Fatalf("the reset file %s does not keep the old state: %v", dst, err)
	}
	// The backup rotation never removes the reset file.
	list, err := backups(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b == dst {
			t.Fatalf("pruneBackups can remove the reset file %s", dst)
		}
	}
	// Two resets in the same second get two names.
	if again := resetName(cfg.StatePath, time.Now()); again == dst && fileExists(dst) {
		t.Fatalf("a second reset name is the same: %s", again)
	}

	h2 := newHarness(t, cfg, nil)
	b := h2.connect("key:b", "bob")
	if snap := h2.snap(b); len(snap.Leaderboard) != 1 || snap.Leaderboard[0].Name != "bob" {
		t.Errorf("after the reset the leaderboard is %+v, want only bob", snap.Leaderboard)
	}
	h2.stop()
}
