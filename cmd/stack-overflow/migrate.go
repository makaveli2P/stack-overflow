package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// legacyFile is one file of the old "office-poker" folder and its name in
// the new folder.
type legacyFile struct {
	old, new string
	hostKey  bool // part of the host key pair
}

var legacyFiles = []legacyFile{
	{old: "bank.json", new: "state.json"},
	{old: "host_ed25519", new: "host_ed25519", hostKey: true},
	{old: "host_ed25519.pub", new: "host_ed25519.pub", hostKey: true},
}

// Marker files that stop a second import of the old bank. The old folder
// gets oldMarker, and the new folder gets newMarker. A host who deletes the
// whole new folder to start fresh deletes newMarker too, but oldMarker
// stays, so the old bank does not come back.
const (
	oldMarker = ".copied-to-stack-overflow"
	newMarker = ".copied-from-office-poker"
)

// markerText is the content of both marker files.
const markerText = "Stack Overflow copied bank.json from ~/.office-poker once.\n" +
	"It does not copy it again while this file or the other marker exists.\n" +
	"Delete " + legacyDir + "/" + oldMarker + " and " + defaultDir + "/" + newMarker + " to allow a new copy.\n"

// migrateDefaults copies the files of the old folder (~/.office-poker) to
// the new folder (~/.stack-overflow) when the new files do not exist. It
// copies the host key too, so colleagues get no host key warning. It
// copies the old bank at most once (see migrateDir). state and hostKey
// tell which default paths are in use. It writes one line to w when it
// copies a file. An error only gets a line; the server then starts with
// new files.
func migrateDefaults(w io.Writer, state, hostKey bool) {
	if !state && !hostKey {
		return
	}
	oldDir, err1 := expandHome(legacyDir)
	newDir, err2 := expandHome(defaultDir)
	if err1 != nil || err2 != nil {
		return
	}
	copied, err := migrateDir(oldDir, newDir, state, hostKey)
	if len(copied) > 0 {
		fmt.Fprintf(w, "  Copied %s from %s to %s (the old files stay; a marker file stops a second copy of the bank).\n",
			strings.Join(copied, ", "), legacyDir, defaultDir)
	}
	if err != nil {
		fmt.Fprintf(w, "  Problem with the old files in %s: %v\n", legacyDir, err)
	}
}

// migrateDir copies each legacy file from oldDir to newDir when the old file
// exists and the new file does not. The host key and its .pub file go
// together: when the new private key exists, neither is copied.
//
// The old bank is copied at most once. It is copied only when state is
// true, no marker file exists, and the new host key does not exist yet.
// The server makes the host key at its first start, so a folder with a
// host key and no state file was reset on purpose. After each run with
// state true and an old bank, migrateDir writes a marker file in both
// folders: a later reset (a deleted state file, a deleted folder, or a
// custom -hostkey) then never brings the old bank back. It returns the
// names of the copied files.
func migrateDir(oldDir, newDir string, state, hostKey bool) ([]string, error) {
	var copied []string
	keyExists := exists(filepath.Join(newDir, "host_ed25519"))
	oldBank := filepath.Join(oldDir, "bank.json")
	bankDone := keyExists || exists(filepath.Join(oldDir, oldMarker)) || exists(filepath.Join(newDir, newMarker))
	for _, f := range legacyFiles {
		if f.hostKey && (!hostKey || keyExists) || !f.hostKey && (!state || bankDone) {
			continue
		}
		src, dst := filepath.Join(oldDir, f.old), filepath.Join(newDir, f.new)
		if !exists(src) || exists(dst) {
			continue
		}
		if err := os.MkdirAll(newDir, 0o700); err != nil {
			return copied, err
		}
		if err := copyFile(src, dst); err != nil {
			return copied, err
		}
		copied = append(copied, f.old)
	}
	if !state || !exists(oldBank) {
		return copied, nil
	}
	return copied, markBankDone(oldDir, newDir)
}

// markLegacyBank writes the marker files for the default folders when an
// old bank exists, so that nothing copies the old bank after a reset. It
// returns an error only when a marker cannot be written.
func markLegacyBank() error {
	oldDir, err1 := expandHome(legacyDir)
	newDir, err2 := expandHome(defaultDir)
	if err1 != nil || err2 != nil || !exists(filepath.Join(oldDir, "bank.json")) {
		return nil
	}
	return markBankDone(oldDir, newDir)
}

// markBankDone writes the marker files that stop a second copy of the old
// bank. It writes both markers, also when the first one fails.
func markBankDone(oldDir, newDir string) error {
	var errs []error
	if err := writeMarker(filepath.Join(oldDir, oldMarker)); err != nil {
		errs = append(errs, err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		errs = append(errs, err)
	} else if err := writeMarker(filepath.Join(newDir, newMarker)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// writeMarker writes a marker file when it does not exist.
func writeMarker(path string) error {
	if exists(path) {
		return nil
	}
	return os.WriteFile(path, []byte(markerText), 0o600)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// copyFile copies src to dst with mode 0600. It writes a temporary file and
// renames it, so dst never holds half a file.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails after the rename; that is fine
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
