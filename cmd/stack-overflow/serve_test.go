package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runServeScript runs scripts/serve.sh with a fake server. The fake server
// exits with the codes of codes, one code for each start, and writes the
// number of starts to a file. runServeScript returns the exit code of the
// script, the number of starts and the log.
func runServeScript(t *testing.T, codes ...int) (int, int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "serve.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "starts")
	var cases strings.Builder
	for i, c := range codes {
		cases.WriteString(strconv.Itoa(i+1) + ") exit " + strconv.Itoa(c) + " ;;\n")
	}
	fake := filepath.Join(dir, "fake-server")
	body := "#!/bin/bash\n" +
		"n=$(( $(cat " + count + " 2>/dev/null || echo 0) + 1 ))\n" +
		"echo $n > " + count + "\n" +
		"echo \"fake server start $n\"\n" +
		"case $n in\n" + cases.String() + "*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "server.log")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = append(os.Environ(), "HOME="+dir, "STACK_OVERFLOW_BIN="+fake, "STACK_OVERFLOW_LOG="+logPath)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatalf("serve.sh: %v\n%s", err, out)
	}
	if ctx.Err() != nil {
		t.Fatalf("serve.sh did not stop:\n%s", out)
	}
	raw, _ := os.ReadFile(count)
	starts, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	logText, _ := os.ReadFile(logPath)
	return code, starts, string(logText)
}

// B7-1: the Go runtime exits with code 2 after a panic. serve.sh must start
// the server again after code 2, as after any other crash.
func TestServeScriptRestartsAfterPanicCode(t *testing.T) {
	code, starts, logText := runServeScript(t, 2)
	if code != 0 || starts != 2 {
		t.Fatalf("exit %d after %d starts, want exit 0 after 2 starts. Log:\n%s", code, starts, logText)
	}
	if strings.Contains(logText, "refused the flags") || !strings.Contains(logText, "exit code 2. A new start comes") {
		t.Errorf("log:\n%s", logText)
	}
}

// B7-1: bad flags (exitUsage) stop serve.sh with the same code, and it does
// not start the server again.
func TestServeScriptStopsOnBadFlags(t *testing.T) {
	code, starts, logText := runServeScript(t, exitUsage)
	if code != exitUsage || starts != 1 {
		t.Fatalf("exit %d after %d starts, want exit %d after 1 start. Log:\n%s", code, starts, exitUsage, logText)
	}
	if !strings.Contains(logText, "refused the flags (exit code 64)") {
		t.Errorf("log:\n%s", logText)
	}
}

// B7-1: realMain never gives code 2, the code of a Go panic, for bad flags.
func TestBadFlagsAreNotPanicCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if exitUsage == 2 {
		t.Fatal("exitUsage is the exit code of a Go panic")
	}
	for _, args := range [][]string{{"-nosuchflag"}, {"-seats", "1"}, {"-admin", "Alice"}} {
		if code := realMain(args); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
}
