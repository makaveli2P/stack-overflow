package server

import (
	"os"
	"testing"

	"github.com/charmbracelet/log"
)

// TestMain keeps the host log lines out of the test output. A test that
// checks them replaces hostLog with captureHostLog.
func TestMain(m *testing.M) {
	hostLog = func(log.Level, string, ...any) {}
	os.Exit(m.Run())
}
