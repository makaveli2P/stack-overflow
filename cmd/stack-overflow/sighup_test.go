package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv makes the test binary run realMain with the arguments in
// helperArgs, as a separate server process.
const (
	helperEnv  = "STACK_OVERFLOW_TEST_SERVER"
	helperArgs = "STACK_OVERFLOW_TEST_ARGS"
)

// TestHelperServer is not a test. It is the server process of
// TestSIGHUPAfterCtrlCSaves.
func TestHelperServer(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("helper process only")
	}
	os.Exit(realMain(strings.Split(os.Getenv(helperArgs), "\n")))
}

// Final check: a tmux pane or a terminal window that closes sends SIGHUP
// right after the Ctrl+C. The SIGHUP must not kill the server during the
// final save: the server exits with code 0, and no temporary state file
// stays behind.
func TestSIGHUPAfterCtrlCSaves(t *testing.T) {
	home := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	stateDir := filepath.Join(home, "state")
	args := []string{
		"-host", "127.0.0.1", "-port", strconv.Itoa(port),
		"-state", filepath.Join(stateDir, "state.json"),
		"-hostkey", filepath.Join(home, "host_ed25519"),
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperServer$")
	cmd.Env = append(os.Environ(), "HOME="+home, helperEnv+"=1", helperArgs+"="+strings.Join(args, "\n"))
	out, err := os.Create(filepath.Join(home, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatalf("the server does not answer: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The SIGHUP after the first signal comes many times, so that one of
	// them comes during the final save.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case waitErr = <-exited:
			break loop
		case <-timeout:
			cmd.Process.Kill()
			t.Fatal("the server did not stop")
		case <-time.After(time.Millisecond):
			_ = cmd.Process.Signal(syscall.SIGHUP)
		}
	}
	log, _ := os.ReadFile(out.Name())
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		t.Fatalf("the server did not exit cleanly: %v\n%s", waitErr, log)
	}
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if !strings.Contains(string(log), "state saved") {
		t.Errorf("no \"state saved\" line:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err != nil {
		t.Errorf("no state file: %v", err)
	}
	tmp, _ := filepath.Glob(filepath.Join(stateDir, ".*.tmp"))
	if len(tmp) > 0 {
		t.Errorf("temporary state files stay behind: %v", tmp)
	}
}
