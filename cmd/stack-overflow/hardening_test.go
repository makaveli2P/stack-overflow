package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// SEC-1: the dialog refuses a weak PIN before it asks for the PIN again.
func TestWeakPINInDialog(t *testing.T) {
	store := newFakeStore(nil)
	addr := startAuthServer(t, &auth{store: store}, nil)
	ki := &kiAnswers{answers: []string{"1234", "4826", "4826"}}
	got, err := dialID(t, addr, "priya", gossh.KeyboardInteractive(ki.challenge))
	if err != nil {
		t.Fatal(err)
	}
	if got != "id=pin:priya ok=true" {
		t.Fatalf("session reports %q", got)
	}
	if !strings.Contains(strings.Join(ki.notes, "|"), textWeakPIN) {
		t.Errorf("instructions %q do not explain the weak PIN", ki.notes)
	}
	if n := strings.Count(strings.Join(ki.prompts, "|"), "Type the PIN again"); n != 1 {
		t.Errorf("the dialog asked to confirm %d times, want 1 (not for the weak PIN): %q", n, ki.prompts)
	}
	if p, _ := store.pin("priya"); p != "4826" {
		t.Errorf("store has PIN %q", p)
	}
}

// SEC-3: the PIN check gets the remote address, for the limit by address
// and the host log.
func TestPINCheckGetsAddress(t *testing.T) {
	store := newFakeStore(map[string]string{"dave": "2468"})
	addr := startAuthServer(t, &auth{store: store}, nil)
	ki := &kiAnswers{answers: []string{"2468"}}
	if _, err := dialID(t, addr, "dave", gossh.KeyboardInteractive(ki.challenge)); err != nil {
		t.Fatal(err)
	}
	if _, err := dialID(t, addr, "dave", gossh.Password("2468")); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	addrs := append([]string(nil), store.addrs...)
	store.mu.Unlock()
	if len(addrs) != 2 {
		t.Fatalf("addresses %q, want 2", addrs)
	}
	for _, a := range addrs {
		if host, _, err := net.SplitHostPort(a); err != nil || host != "127.0.0.1" {
			t.Errorf("CheckPINFrom got addr %q, want 127.0.0.1:<port>", a)
		}
	}
}

// OPS-4: bad flag values stop the server before it touches a file, with
// exit code 2 and a message that names the flag.
func TestServerFlagChecks(t *testing.T) {
	bad := map[string][]string{
		"-seats":           {"-seats", "11"},
		"-seats ":          {"-seats", "1"},
		"-sb":              {"-sb", "0"},
		"-bb":              {"-bb", "5", "-sb", "10"},
		"-stack":           {"-stack", "-5"},
		"-stack ":          {"-stack", "5"},
		"-port":            {"-port", "99999"},
		"-port ":           {"-port", "0"},
		"-chips-per-unit":  {"-chips-per-unit", "-3"},
		" -chips-per-unit": {"-chips-per-unit", "0"},
		"-host":            {"-host", "999.1.1.1"},
		" -host":           {"-host", "office-mac"},
	}
	for flag, args := range bad {
		var errb bytes.Buffer
		if _, err := parseServerFlags(args, env(nil), &errb); err == nil {
			t.Errorf("%q: no error", args)
		} else if !strings.Contains(errb.String(), strings.TrimSpace(flag)) {
			t.Errorf("%q: message %q does not name %s", args, errb.String(), flag)
		}
	}
	for _, args := range [][]string{
		{"-host", "127.0.0.1"}, {"-host", "localhost"}, {"-host", "::"}, {"-host", "fe80::1%en0"}, {"-seats", "2", "-sb", "1", "-bb", "1", "-stack", "1"},
		{"-seats", "10", "-port", "65535", "-chips-per-unit", "1"},
	} {
		if _, err := parseServerFlags(args, env(nil), io.Discard); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code := realMain([]string{"-port", "99999"}); code != exitUsage {
		t.Errorf("-port 99999: exit %d, want %d", code, exitUsage)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a bad flag made files in HOME: %v", entries)
	}
}

// testRunOptions returns server options for run() on a free port of
// 127.0.0.1 (also for the screenshot server), with files in a temporary
// folder. keepAwake does nothing.
func testRunOptions(t *testing.T) options {
	t.Helper()
	old := keepAwake
	keepAwake = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { keepAwake = old })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	o := defaultOptions()
	dir := t.TempDir()
	o.host, o.port = "127.0.0.1", port
	o.shotPort = freePort(t)
	o.state = filepath.Join(dir, "state", "state.json")
	o.hostKey = filepath.Join(dir, "key", "host_ed25519")
	return o
}

// OPS-4: a port in use stops the server before it touches the state file
// or prints the join command.
func TestPortInUseStopsEarly(t *testing.T) {
	o := testRunOptions(t)
	ln, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.port)))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	err = run(o)
	if err == nil || !strings.Contains(err.Error(), "cannot listen on port "+strconv.Itoa(o.port)) {
		t.Fatalf("run on a port in use: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(o.state)); err == nil {
		t.Error("run made the state folder before it found the port in use")
	}
}

// startRun starts run(o) and waits until the SSH server answers. The
// signal handlers are in place then.
func startRun(t *testing.T, o options) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run(o) }()
	addr := net.JoinHostPort(o.host, strconv.Itoa(o.port))
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
			User: "probe", Auth: []gossh.AuthMethod{gossh.PublicKeys(newKey(t))},
			HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second,
		})
		if err == nil {
			c.Close()
			return done
		}
		select {
		case err := <-done:
			t.Fatalf("run stopped early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server does not answer: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop")
		return nil
	}
}

// REL-4 / OPS-1: SIGHUP (the host closes the terminal window) stops the
// server the same way as Ctrl+C: the room saves the state.
func TestSIGHUPStopsCleanly(t *testing.T) {
	o := testRunOptions(t)
	done := startRun(t, o)
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := waitRun(t, done); err != nil {
		t.Fatalf("run after SIGHUP: %v", err)
	}
	if _, err := os.Stat(o.state); err != nil {
		t.Errorf("no state file after SIGHUP: %v", err)
	}
}

// REL-2 / OPS-3: when the final save fails, run reports it and does not
// claim that it saved the state.
func TestFinalSaveFailureIsReported(t *testing.T) {
	o := testRunOptions(t)
	done := startRun(t, o)
	dir := filepath.Dir(o.state)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := waitRun(t, done)
	if err == nil || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("run with a read-only state folder: %v", err)
	}
}

// The version comes from -ldflags, else from the VCS facts of the build.
func TestVersion(t *testing.T) {
	cases := []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "d9daf20aa11"}, {Key: "vcs.time", Value: "2026-11-18T01:00:00Z"}},
			"dev d9daf20 2026-11-18T01:00:00Z"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}, "dev abc +dirty"},
	}
	for _, c := range cases {
		if got := versionFrom(c.settings); got != c.want {
			t.Errorf("versionFrom(%v) = %q, want %q", c.settings, got, c.want)
		}
	}
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })
	if buildVersion() != "v1.2.3" {
		t.Errorf("buildVersion = %q", buildVersion())
	}
	o, err := parseServerFlags([]string{"-version"}, env(nil), io.Discard)
	if err != nil || !o.version {
		t.Errorf("-version: %v %v", o.version, err)
	}
	var b bytes.Buffer
	printWelcome(&b, defaultOptions(), "/s.json", nil, nil)
	if !strings.Contains(b.String(), "(version v1.2.3)") {
		t.Errorf("the banner has no version:\n%s", b.String())
	}
}

// REL-1: on macOS the server keeps the Mac awake with caffeinate.
func TestKeepAwake(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("caffeinate is a macOS tool")
	}
	stop, err := startCaffeinate(os.Getpid())
	if err != nil {
		t.Fatalf("startCaffeinate: %v", err)
	}
	stop()
}

// SEC-1: the host can remove a key that somebody linked to a player.
func TestUnlinkCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var out, errb bytes.Buffer
	if code := runUnlink([]string{"-state", path}, &out, &errb); code != exitUsage {
		t.Errorf("unlink without a key: exit %d", code)
	}
	fp := "SHA256:" + strings.Repeat("g", 43)
	if code := runUnlink([]string{"-state", path, fp}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no state file") {
		t.Errorf("unlink without a file: exit %d, %q", code, errb.String())
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
	s, err := room.ConnectWith("key:"+fp, "x", server.ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkPIN("priya", "4826"); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := runUnlink([]string{"-state", path, fp}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "Stop the server") {
		t.Errorf("unlink while the server runs: exit %d, %q", code, errb.String())
	}
	cancel()
	<-done

	out.Reset()
	errb.Reset()
	if code := runUnlink([]string{"-state", path, fp}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"priya"`) {
		t.Fatalf("unlink: exit %d, out %q, err %q", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := runUnlink([]string{"-state", path, fp}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no player has the key") {
		t.Errorf("unlink again: exit %d, %q", code, errb.String())
	}
}
