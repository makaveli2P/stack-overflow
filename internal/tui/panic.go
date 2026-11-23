package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The panic key makes every screen in the room hide the game: a one-second
// notice, then a decoy that looks like a developer terminal. See
// DESIGN.md, "Panic key".
//
// There are three panic keys:
//   - Ctrl+U (byte 0x15) works only when no text input has focus. Ghostty
//     sends 0x15 for Cmd+Delete by default, and iTerm2 does too with its
//     "Natural Text Editing" preset, so Cmd+Delete hides the game with no
//     setup. In a text input, Cmd+Delete keeps its usual job: it clears
//     the line.
//   - F20 (ESC [ 3 4 ~) works on every screen, also in a text input. No
//     common keyboard has F20, so only a terminal mapping sends it. The
//     README maps Cmd+H to F20.
//   - Ctrl+H (byte 0x08) works only when no text input has focus. Many
//     terminals send 0x08 for Ctrl+Backspace, and some for Backspace. In a
//     text input, 0x08 deletes one character, so a word delete in the chat
//     does not hide every screen in the room.
const (
	panicKey       = "ctrl+h"
	panicFKey      = "f20"
	panicCmdDelKey = "ctrl+u" // what Cmd+Delete sends in Ghostty
	panicNoticeFor = time.Second
	decoyEvery     = 700 * time.Millisecond
	panicNotice    = "🤫  Shh! Someone is coming!"
	// decoyTitle is the window title while the panic screen is up.
	decoyTitle = "inventory-service — zsh"
	// decoyWaitTest is the test name of the quiet "your turn" line.
	decoyWaitTest = "TestCheckoutFlow"
)

// panicState is the panic screen of one model.
type panicState struct {
	seq   uint64 // the newest Snapshot.PanicSeq that the model saw
	on    bool   // the panic screen is up
	decoy bool   // phase 2: the decoy (else phase 1: the notice)
	gen   int    // changes at each start and exit, so old ticks do nothing
	steps int    // decoy lines added since phase 2 started
}

// Panic screen messages. gen is the panicState.gen that sent them.
type (
	panicNoticeDoneMsg struct{ gen int }
	decoyStepMsg       struct{ gen int }
)

// panicSnapshot checks a new snapshot for a new panic. The first snapshot
// of a connection is the baseline, so a player who joins later sees
// nothing. A new panic while the panic screen is up does not start the
// notice again.
func (m *model) panicSnapshot(seq uint64, first bool) tea.Cmd {
	newer := seq > m.panic.seq
	m.panic.seq = max(m.panic.seq, seq)
	if first || !newer || m.panic.on {
		return nil
	}
	m.panic.gen++
	m.panic.on, m.panic.decoy, m.panic.steps = true, false, 0
	gen := m.panic.gen
	return tea.Batch(
		tea.Tick(panicNoticeFor, func(time.Time) tea.Msg { return panicNoticeDoneMsg{gen} }),
		tea.SetWindowTitle(decoyTitle),
	)
}

// panicMsg handles the panic screen ticks.
func (m *model) panicMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case panicNoticeDoneMsg:
		if !m.panic.on || m.panic.decoy || msg.gen != m.panic.gen {
			return nil
		}
		m.panic.decoy = true
		return decoyStep(msg.gen)
	case decoyStepMsg:
		if !m.panic.on || msg.gen != m.panic.gen {
			return nil
		}
		m.panic.steps++
		return decoyStep(msg.gen)
	}
	return nil
}

func decoyStep(gen int) tea.Cmd {
	return tea.Tick(decoyEvery, func(time.Time) tea.Msg { return decoyStepMsg{gen} })
}

// textFocus reports whether keys go to a text input: the chat, a form
// field, the "yes" field of the settle-up, or the raise amount.
func (m *model) textFocus() bool {
	return m.mode == modeChat || m.mode == modeRaise || m.typingMode()
}

// isPanicKey reports whether k is a panic key on the current screen.
func (m *model) isPanicKey(k tea.KeyMsg) bool {
	switch k.String() {
	case panicFKey:
		return true
	case panicKey, panicCmdDelKey:
		return !m.textFocus()
	}
	return false
}

// panicKeyPress handles a key while the panic screen is up. A panic key
// (Ctrl+H, Ctrl+U or F20) does nothing. Any other key ends the panic screen and
// does nothing else, so a stray "f" does not fold.
func (m *model) panicKeyPress(k tea.KeyMsg) tea.Cmd {
	if s := k.String(); s == panicKey || s == panicFKey || s == panicCmdDelKey {
		return nil
	}
	m.panic.on, m.panic.decoy, m.panic.steps = false, false, 0
	m.panic.gen++
	// A chat message that came during the panic keeps the rest of its
	// highlight time, so the highlight tick starts again here.
	return tea.Batch(tea.SetWindowTitle(m.windowTitle()), m.chatHLTick())
}

// pressPanic sends the panic to the room. The room answers with a
// snapshot that starts the panic screen on every client.
func (m *model) pressPanic() tea.Cmd {
	s := m.sess
	return call(callPanic, s.Panic)
}

// panicView draws the panic screen: the notice in phase 1, else the decoy.
// Both use the default colours of the terminal.
func (m *model) panicView(w, h int) string {
	if !m.panic.decoy {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, ansi.Truncate(panicNotice, w, ""))
	}
	lines := make([]string, 0, h)
	rows := h
	wait := m.decoyWaitLine()
	if wait != "" {
		rows--
	}
	end := decoyStart + m.panic.steps
	for i := end - rows; i < end; i++ {
		lines = append(lines, decoyLine(i))
	}
	if wait != "" {
		lines = append(lines, wait)
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	return strings.Join(lines, "\n")
}

// decoyWaitLine is the only hint on the decoy: one quiet line with the
// seconds left when it is the viewer's turn, else "".
func (m *model) decoyWaitLine() string {
	if m.snap.Table.Legal == nil {
		return ""
	}
	line := "--- WAIT  " + decoyWaitTest
	if left, ok := m.turnLeft(); ok {
		line += fmt.Sprintf(" (%ds)", seconds(left))
	}
	return line
}

// decoyModule is the invented module of the decoy.
const decoyModule = "github.com/northwind/inventory-service"

// decoyScript is one run of "go test -v ./..." on decoyModule. The decoy
// shows it line by line and loops it. It has no word from the game.
var decoyScript = expandTabs([]string{
	"~/src/inventory-service $ go test -v ./...",
	"?   \t" + decoyModule + "/cmd/inventoryd\t[no test files]",
	"=== RUN   TestCreateItem",
	"--- PASS: TestCreateItem (0.01s)",
	"=== RUN   TestCreateItem_DuplicateSKU",
	"--- PASS: TestCreateItem_DuplicateSKU (0.00s)",
	"=== RUN   TestListItemsPagination",
	"=== RUN   TestListItemsPagination/first_page",
	"=== RUN   TestListItemsPagination/last_page",
	"=== RUN   TestListItemsPagination/empty_cursor",
	"--- PASS: TestListItemsPagination (0.03s)",
	"    --- PASS: TestListItemsPagination/first_page (0.01s)",
	"    --- PASS: TestListItemsPagination/last_page (0.01s)",
	"    --- PASS: TestListItemsPagination/empty_cursor (0.00s)",
	"=== RUN   " + decoyWaitTest,
	"    api_test.go:212: reserved 3 units of SKU-10442",
	"    api_test.go:219: order 7f3c2a confirmed in 84ms",
	"--- PASS: " + decoyWaitTest + " (0.12s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/api\t0.418s",
	"=== RUN   TestSKUValidation",
	"=== RUN   TestSKUValidation/valid",
	"=== RUN   TestSKUValidation/lowercase",
	"=== RUN   TestSKUValidation/too_long",
	"--- PASS: TestSKUValidation (0.00s)",
	"    --- PASS: TestSKUValidation/valid (0.00s)",
	"    --- PASS: TestSKUValidation/lowercase (0.00s)",
	"    --- PASS: TestSKUValidation/too_long (0.00s)",
	"=== RUN   TestCategoryTree",
	"--- PASS: TestCategoryTree (0.01s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/catalog\t0.092s",
	"=== RUN   TestReserveStock",
	"--- PASS: TestReserveStock (0.02s)",
	"=== RUN   TestReserveStock_Concurrent",
	"    reserve_test.go:88: 64 goroutines, 0 oversold",
	"--- PASS: TestReserveStock_Concurrent (0.31s)",
	"=== RUN   TestReleaseExpiredReservations",
	"--- PASS: TestReleaseExpiredReservations (0.05s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/reserve\t0.507s",
	"=== RUN   TestPriceRounding",
	"=== RUN   TestPriceRounding/half_up",
	"=== RUN   TestPriceRounding/half_even",
	"--- PASS: TestPriceRounding (0.00s)",
	"    --- PASS: TestPriceRounding/half_up (0.00s)",
	"    --- PASS: TestPriceRounding/half_even (0.00s)",
	"=== RUN   TestTaxInclusivePrices",
	"--- PASS: TestTaxInclusivePrices (0.00s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/pricing\t0.061s",
	"=== RUN   TestMigrations",
	"    store_test.go:41: applied 17 migrations",
	"--- PASS: TestMigrations (0.44s)",
	"=== RUN   TestUpsertItem",
	"--- PASS: TestUpsertItem (0.03s)",
	"=== RUN   TestStockLevelsByWarehouse",
	"--- PASS: TestStockLevelsByWarehouse (0.06s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/store\t1.127s",
	"=== RUN   TestOutboxPublish",
	"--- PASS: TestOutboxPublish (0.02s)",
	"=== RUN   TestOutboxRetryBackoff",
	"    outbox_test.go:57: retry 1 after 100ms",
	"    outbox_test.go:57: retry 2 after 200ms",
	"--- PASS: TestOutboxRetryBackoff (0.33s)",
	"PASS",
	"ok  \t" + decoyModule + "/internal/outbox\t0.233s",
})

// decoyStart is the line index at which phase 2 starts. The stream before
// it fills even the tallest window, and the newest line at the start is the
// prompt with the command.
var decoyStart = len(decoyScript)*(maxHeight/len(decoyScript)+1) + 1

// decoyLine returns line i of the endless decoy stream.
func decoyLine(i int) string {
	n := len(decoyScript)
	return decoyScript[((i%n)+n)%n]
}

// expandTabs replaces each tab with spaces up to the next 8-column stop,
// as a terminal shows it.
func expandTabs(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		var b strings.Builder
		col := 0
		for _, r := range l {
			if r == '\t' {
				n := 8 - col%8
				b.WriteString(strings.Repeat(" ", n))
				col += n
				continue
			}
			b.WriteRune(r)
			col++
		}
		out[i] = b.String()
	}
	return out
}
