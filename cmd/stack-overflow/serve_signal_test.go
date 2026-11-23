package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeServerEnv makes the test binary act as a fake server for
// runServeSignals.
const fakeServerEnv = "STACK_OVERFLOW_TEST_FAKE_SERVER"

// TestHelperFakeServer is not a test. It is the fake server of
// runServeSignals. It has the signal rules of run: the first SIGINT,
// SIGTERM or SIGHUP starts the stop. From then on, the fake server catches
// SIGHUP and does nothing with it, and SIGINT and SIGTERM have their
// default action again. The "save" then takes one second.
func TestHelperFakeServer(t *testing.T) {
	if os.Getenv(fakeServerEnv) != "1" {
		t.Skip("helper process only")
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	os.Stdout.WriteString("fake server ready\n")
	sig := <-sigs
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP)
	signal.Reset(os.Interrupt, syscall.SIGTERM)
	os.Stdout.WriteString("fake server saving after " + sig.String() + "\n")
	time.Sleep(time.Second)
	os.Stdout.WriteString("fake server saved\n")
	os.Exit(0)
}

// runServeSignals runs scripts/serve.sh with the fake server. When the
// fake server is ready, it sends first to the script. When the fake server
// starts the save, it sends each signal of then to the script, 50 ms
// apart. A stdout that is not nil is the terminal of the script. It returns
// the exit code of the script and the log.
func runServeSignals(t *testing.T, stdout *os.File, first os.Signal, then ...os.Signal) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "serve.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "server.log")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script, "-test.run=^TestHelperFakeServer$")
	cmd.Env = append(os.Environ(), "HOME="+dir, fakeServerEnv+"=1",
		"STACK_OVERFLOW_BIN="+os.Args[0], "STACK_OVERFLOW_LOG="+logPath)
	if stdout != nil {
		cmd.Stdout, cmd.Stderr = stdout, stdout
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waitLog := func(text string) {
		t.Helper()
		for {
			raw, _ := os.ReadFile(logPath)
			if strings.Contains(string(raw), text) {
				return
			}
			select {
			case err := <-exited:
				t.Fatalf("serve.sh stopped (%v) before %q:\n%s", err, text, raw)
			case <-ctx.Done():
				t.Fatalf("no %q in the log:\n%s", text, raw)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitLog("fake server ready")
	if err := cmd.Process.Signal(first); err != nil {
		t.Fatal(err)
	}
	waitLog("fake server saving")
	for _, sig := range then {
		time.Sleep(50 * time.Millisecond)
		_ = cmd.Process.Signal(sig)
	}
	var waitErr error
	select {
	case waitErr = <-exited:
	case <-ctx.Done():
		t.Fatal("serve.sh did not stop")
	}
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(waitErr, &exit):
		code = exit.ExitCode()
	case waitErr != nil:
		t.Fatal(waitErr)
	}
	logText, _ := os.ReadFile(logPath)
	return code, string(logText)
}

// Final check, serve.sh: a terminal window or tmux pane that closes sends
// SIGHUP to serve.sh, often two times, and often right after a Ctrl+C.
// serve.sh must send only one SIGINT to the server. A second SIGINT kills
// the server during the final save.
func TestServeScriptSIGHUPDoesNotStopTheSave(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first os.Signal
		then  []os.Signal
	}{
		{"Ctrl+C then a closed window", os.Interrupt, []os.Signal{syscall.SIGHUP, syscall.SIGHUP}},
		{"a closed window only", syscall.SIGHUP, []os.Signal{syscall.SIGHUP}},
		{"SIGTERM then a closed window", syscall.SIGTERM, []os.Signal{syscall.SIGHUP}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, logText := runServeSignals(t, nil, tc.first, tc.then...)
			if code != 0 {
				t.Errorf("serve.sh exit code %d, want 0", code)
			}
			if !strings.Contains(logText, "fake server saved") ||
				!strings.Contains(logText, "the server stopped (exit code 0)") {
				t.Errorf("the save did not finish. Log:\n%s", logText)
			}
			if strings.Contains(logText, "A new start comes") {
				t.Errorf("serve.sh started the server again. Log:\n%s", logText)
			}
		})
	}
}

// A second Ctrl+C or SIGTERM to serve.sh still stops the server at once,
// without the save.
func TestServeScriptSecondCtrlCForcesTheStop(t *testing.T) {
	for _, second := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(second.String(), func(t *testing.T) {
			code, logText := runServeSignals(t, nil, os.Interrupt, second)
			if code != 0 {
				t.Errorf("serve.sh exit code %d, want 0", code)
			}
			if strings.Contains(logText, "fake server saved") ||
				!strings.Contains(logText, "without the final save") ||
				!strings.Contains(logText, "the server stopped (exit code 130)") {
				t.Errorf("the second signal did not stop the server at once. Log:\n%s", logText)
			}
		})
	}
}

// Final check, serve.sh: after a terminal window closes, each write of
// stamp to the terminal fails. bash kept the failed line in its output
// buffer, and the next write to the log file wrote that line a second
// time. A stdout that is open only for reading makes each terminal write
// fail. Each line of the script and of the server must be in the log one
// time.
func TestServeScriptClosedTerminalDoesNotDoubleLogLines(t *testing.T) {
	ro, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	_, logText := runServeSignals(t, ro, syscall.SIGHUP)
	for _, text := range []string{
		"serve.sh: log file:", "serve.sh: start:", "fake server ready",
		"fake server saving", "fake server saved", "the server stopped (exit code 0)",
	} {
		if n := strings.Count(logText, text); n != 1 {
			t.Errorf("%q is in the log %d times, want 1. Log:\n%s", text, n, logText)
		}
	}
}
