package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// writeTestFile writes data to path and makes the folders.
func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// oldPokerDir makes an old office-poker folder with a bank and a host key.
func oldPokerDir(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, "bank.json"), "bank")
	writeTestFile(t, filepath.Join(dir, "host_ed25519"), "key")
	writeTestFile(t, filepath.Join(dir, "host_ed25519.pub"), "pub")
}

// Batch 10 audit (P2, F1): the old bank comes over once. A reset that
// deletes state.json, the whole new folder, or uses a custom -hostkey never
// brings it back.
func TestMigrateBankOnlyOnce(t *testing.T) {
	root := t.TempDir()
	oldDir, newDir := filepath.Join(root, ".office-poker"), filepath.Join(root, ".stack-overflow")
	oldPokerDir(t, oldDir)
	state := filepath.Join(newDir, "state.json")

	copied, err := migrateDir(oldDir, newDir, true, true)
	if err != nil || !reflect.DeepEqual(copied, []string{"bank.json", "host_ed25519", "host_ed25519.pub"}) {
		t.Fatalf("first start: %q %v", copied, err)
	}
	for _, m := range []string{filepath.Join(oldDir, oldMarker), filepath.Join(newDir, newMarker)} {
		if !exists(m) {
			t.Fatalf("no marker %s", m)
		}
	}

	// Reset 1: delete state.json.
	os.Remove(state)
	if copied, err := migrateDir(oldDir, newDir, true, true); err != nil || len(copied) != 0 || exists(state) {
		t.Fatalf("after rm state.json: %q %v", copied, err)
	}

	// Reset 2: delete the whole new folder. The server path gets the host
	// key back (no warning for the players), but not the bank.
	os.RemoveAll(newDir)
	copied, err = migrateDir(oldDir, newDir, true, true)
	if err != nil || !reflect.DeepEqual(copied, []string{"host_ed25519", "host_ed25519.pub"}) || exists(state) {
		t.Fatalf("after rm -rf, server: %q %v, state %v", copied, err, exists(state))
	}
	// The stats path (no host key) copies nothing.
	os.RemoveAll(newDir)
	if copied, err := migrateDir(oldDir, newDir, true, false); err != nil || len(copied) != 0 || exists(state) {
		t.Fatalf("after rm -rf, stats: %q %v", copied, err)
	}

	// A custom -hostkey: the default key path never exists.
	old2, new2 := filepath.Join(root, "old2"), filepath.Join(root, "new2")
	oldPokerDir(t, old2)
	if copied, _ := migrateDir(old2, new2, true, false); !reflect.DeepEqual(copied, []string{"bank.json"}) {
		t.Fatalf("custom key, first start: %q", copied)
	}
	os.Remove(filepath.Join(new2, "state.json"))
	if copied, _ := migrateDir(old2, new2, true, false); len(copied) != 0 {
		t.Fatalf("custom key, after rm state.json: %q", copied)
	}

	// A folder from before the markers (the import ran with an older
	// binary): the next start writes the markers, so a later rm -rf
	// does not bring the bank back.
	old3, new3 := filepath.Join(root, "old3"), filepath.Join(root, "new3")
	oldPokerDir(t, old3)
	writeTestFile(t, filepath.Join(new3, "host_ed25519"), "key")
	writeTestFile(t, filepath.Join(new3, "state.json"), "played")
	if copied, err := migrateDir(old3, new3, true, true); err != nil || len(copied) != 0 || !exists(filepath.Join(old3, oldMarker)) {
		t.Fatalf("old import: %q %v", copied, err)
	}
	os.RemoveAll(new3)
	if copied, _ := migrateDir(old3, new3, true, true); slices.Contains(copied, "bank.json") {
		t.Fatalf("old import, after rm -rf: %q", copied)
	}

	// The old folder cannot take a marker: the copy still works, the error
	// gets a line, and the new marker still stops a second copy.
	if os.Getuid() != 0 {
		old4, new4 := filepath.Join(root, "old4"), filepath.Join(root, "new4")
		oldPokerDir(t, old4)
		if err := os.Chmod(old4, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(old4, 0o700) })
		copied, err := migrateDir(old4, new4, true, true)
		if err == nil || !slices.Contains(copied, "bank.json") || !exists(filepath.Join(new4, newMarker)) {
			t.Fatalf("read-only old folder: %q %v", copied, err)
		}
		os.Remove(filepath.Join(new4, "state.json"))
		if copied, _ := migrateDir(old4, new4, true, false); len(copied) != 0 {
			t.Fatalf("read-only old folder, second start: %q", copied)
		}
	}
}

// The same reset through "stack-overflow stats" with a temp HOME: after
// rm -rf ~/.stack-overflow, stats says that there is no state file, and
// it does not copy the old bank.
func TestStatsAfterFolderResetImportsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPokerDir(t, filepath.Join(home, ".office-poker"))
	var out, errOut bytes.Buffer
	// The bank is a fake, so stats cannot read it. The copy is what
	// counts here.
	runStats(nil, &out, &errOut)
	if !strings.Contains(errOut.String(), "Copied bank.json") {
		t.Fatalf("first stats:\n%s", errOut.String())
	}
	if err := os.RemoveAll(filepath.Join(home, ".stack-overflow")); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	code := runStats(nil, &out, &errOut)
	if code != 1 || strings.Contains(errOut.String(), "Copied") || !strings.Contains(errOut.String(), "There is no state file") {
		t.Fatalf("stats after rm -rf: code %d\n%s", code, errOut.String())
	}
	if exists(filepath.Join(home, ".stack-overflow", "state.json")) {
		t.Fatal("stats made a state file from the old bank")
	}
}

// Batch 10 audit (P3): "stack-overflow reset" refuses while the server
// runs, does nothing without -yes, and moves the state file away with -yes.
func TestResetCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var out, errb bytes.Buffer
	if code := runReset([]string{"-state", path, "-yes"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "nothing to reset") {
		t.Errorf("reset without a file: exit %d, %q", code, errb.String())
	}
	if code := runReset([]string{"-state", path, "extra"}, &out, &errb); code != exitUsage {
		t.Errorf("reset with an argument: exit %d", code)
	}

	room, err := server.NewRoom(defaultOptions().roomConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { room.Run(ctx); close(done) }()
	if err := room.CreatePINAccount("priya", "4826"); err != nil {
		t.Fatal(err)
	}
	// The host deleted state.json while the server runs. The reset must
	// still refuse: the server writes the file again at its stop.
	os.Remove(path)
	errb.Reset()
	if code := runReset([]string{"-state", path, "-yes"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "Stop the server first") {
		t.Errorf("reset while the server runs: exit %d, %q", code, errb.String())
	}
	cancel()
	<-done
	if !exists(path) {
		t.Fatal("the stopped server wrote no state file")
	}

	errb.Reset()
	if code := runReset([]string{"-state", path}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "Nothing changed") || !exists(path) {
		t.Errorf("reset without -yes: exit %d, %q", code, errb.String())
	}
	out.Reset()
	if code := runReset([]string{"-state", path, "-yes"}, &out, &errb); code != 0 || exists(path) || !strings.Contains(out.String(), ".reset") {
		t.Fatalf("reset: exit %d, out %q, err %q", code, out.String(), errb.String())
	}
	matches, _ := filepath.Glob(path + ".*.reset")
	if len(matches) != 1 {
		t.Fatalf("reset files %q", matches)
	}
}

// With the default state path, reset also marks the old bank as copied, so
// that a later start does not bring it back.
func TestResetCommandMarksOldBank(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldDir, newDir := filepath.Join(home, ".office-poker"), filepath.Join(home, ".stack-overflow")
	oldPokerDir(t, oldDir)
	writeTestFile(t, filepath.Join(newDir, "state.json"), "{}")
	var out, errb bytes.Buffer
	if code := runReset([]string{"-yes"}, &out, &errb); code != 0 {
		t.Fatalf("reset: exit %d, %q", code, errb.String())
	}
	if !exists(filepath.Join(oldDir, oldMarker)) {
		t.Fatal("reset wrote no marker in the old folder")
	}
	os.RemoveAll(newDir)
	if copied, _ := migrateDir(oldDir, newDir, true, true); slices.Contains(copied, "bank.json") {
		t.Fatalf("after reset and rm -rf: copied %q", copied)
	}
}
