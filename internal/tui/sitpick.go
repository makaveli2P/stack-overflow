package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// sitPicker is the state of the buy-in picker. S opens it when the bank
// holds chips.
type sitPicker struct {
	cursor int  // the highlighted option
	busy   bool // a SitWith call is running
}

// sitOption is one row of the buy-in picker.
type sitOption struct {
	key    string // the key that picks the option at once
	label  string
	amount int64  // the chips that go to the table
	detail string // where the chips come from, or ""
	choice server.SitChoice
}

// sitOptions returns the two rows of the picker: a buy-in of
// StartingStack, and the whole bank.
func (m *model) sitOptions() []sitOption {
	bank, stack := m.snap.Me.Bank, m.snap.Config.StartingStack
	return []sitOption{
		{key: "1", label: "Buy-in", amount: stack, detail: m.buyInDetail(bank, stack), choice: server.SitBuyIn},
		{key: "2", label: "Whole bank", amount: bank, choice: server.SitWholeBank},
	}
}

// buyInDetail tells where the chips of one buy-in come from. A bank that
// covers the buy-in keeps the rest: "1,450 stays in bank". A smaller bank
// goes to the table, and the player buys the difference:
// "300 from bank + 700 top-up ₹350". An empty bank gives "".
func (m *model) buyInDetail(bank, stack int64) string {
	switch {
	case bank <= 0:
		return ""
	case bank >= stack:
		return chips(bank-stack) + " stays in bank"
	}
	return m.topUpText(bank, stack)
}

// topUpText is the split of a buy-in that the bank covers only in part:
// "300 from bank + 700 top-up ₹350". The money is the price of the
// top-up chips.
func (m *model) topUpText(bank, stack int64) string {
	buy := stack - bank
	return chips(bank) + " from bank + " + chips(buy) + " top-up " + money(toMoney(buy, m.perUnit()), m.currency())
}

// rebuyText is the label of the B key for a busted player. The rebuy
// takes the chips from the bank first. A shared frame (m.public) shows
// only the amount: "from bank" and the top-up tell the viewer's bank.
func (m *model) rebuyText() (label, detail string) {
	bank, stack := m.snap.Me.Bank, m.snap.Config.StartingStack
	label = "Buy back in for " + chips(stack)
	switch {
	case m.public:
		// The label only: no "from bank" and no top-up.
	case bank >= stack:
		label += " from bank"
	case bank > 0:
		detail = m.topUpText(bank, stack)
	}
	return label, detail
}

// syncPicker keeps the picker valid for a new snapshot. The picker
// closes when the viewer has a seat. It also closes when the bank becomes
// empty (for example, the host marks the period settled), because the
// picker is only for a bank > 0: with an empty bank, "Whole bank 0" would
// buy a full StartingStack. S then sits at once with a buy-in. A running
// SitWith call keeps the picker open until its result arrives.
func (m *model) syncPicker() {
	if m.mode != modeSit {
		return
	}
	if _, seated := m.mySeat(); seated {
		m.mode = modeTable
		return
	}
	if m.snap.Me.Bank <= 0 && !m.pick.busy {
		m.mode = modeTable
		m.flash(bankEmptyNotice, false)
	}
}

// bankEmptyNotice tells the viewer why the picker closed.
const bankEmptyNotice = "Your bank is now empty. Press S to buy in."

// sitKey handles the buy-in picker. 1 and 2 sit at once, ↑/↓ move the
// highlight, Enter picks it and Esc closes the picker. Other keys do
// nothing, so that they cannot work as table keys (1-3 show cards, Q
// quits, D opens the stats) behind the picker.
func (m *model) sitKey(key string) tea.Cmd {
	n := len(m.sitOptions())
	switch key {
	case "esc":
		m.mode = modeTable
		return nil
	}
	if m.pick.busy {
		return nil
	}
	switch key {
	case "up", "shift+tab":
		m.pick.cursor = (m.pick.cursor + n - 1) % n
	case "down", "tab":
		m.pick.cursor = (m.pick.cursor + 1) % n
	case "enter":
		return m.pickSit(m.pick.cursor)
	case "1", "2":
		return m.pickSit(int(key[0] - '1'))
	}
	return nil
}

// pickSit sits down with option i of the picker.
func (m *model) pickSit(i int) tea.Cmd {
	opts := m.sitOptions()
	if i < 0 || i >= len(opts) {
		return nil
	}
	m.pick.cursor, m.pick.busy = i, true
	s, c := m.sess, opts[i].choice
	return call(callSit, func() error { return s.SitWith(-1, c) })
}

// ---- drawing ----

// pickerBox draws the picker. It is at most w cells wide.
func (m *model) pickerBox(w int) string {
	st := m.st
	opts := m.sitOptions()
	rows := make([][2]string, len(opts))
	wide := 0
	for i, o := range opts {
		cursor, label := "  ", st.text.Render(o.label)
		if i == m.pick.cursor {
			cursor, label = st.cursor.Render("▸ "), st.gold.Render(o.label)
		}
		rows[i][0] = cursor + st.gold.Render(o.key) + "  " + label + "  " + st.bold.Render(chips(o.amount))
		if o.detail != "" {
			rows[i][1] = st.dim.Render(o.detail)
		}
		wide = max(wide, ansi.StringWidth(rows[i][0])+3+ansi.StringWidth(rows[i][1]))
	}
	hintLine := m.hintText([]hint{{"1/2", "pick"}, {"↑/↓", "move"}, {"Enter", "sit"}, {"Esc", "cancel"}})
	if m.pick.busy {
		hintLine = st.dim.Render("Sitting down…")
	}
	inner := max(wide, ansi.StringWidth(hintLine), 30)
	bw := min(inner+4, w)
	// A detail that does not fit next to the amount goes on its own line
	// under the label.
	split := wide > bw-4
	lines := []string{""}
	for _, r := range rows {
		switch {
		case r[1] == "":
			lines = append(lines, r[0])
		case split:
			lines = append(lines, r[0], strings.Repeat(" ", 5)+r[1])
		default:
			lines = append(lines, r[0]+st.faint.Render(" · ")+r[1])
		}
	}
	lines = append(lines, "", hintLine)
	return m.titledBox("SIT DOWN WITH", lines, bw, colGold)
}

// ansiReset ends every SGR style.
const ansiReset = "\x1b[0m"

// overlay draws box in the middle of the first areaW columns of block,
// which is h lines high. The block shows around the box.
func overlay(block, box string, areaW, h int) string {
	bg := strings.Split(fixHeight(block, h), "\n")
	fg := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	x := max((areaW-bw)/2, 0)
	y := max((h-len(fg))/2, 0)
	for i, l := range fg {
		if y+i >= len(bg) {
			break
		}
		row := bg[y+i]
		left := padRight(ansi.Truncate(row, x, ""), x)
		if strings.Contains(left, "\x1b") {
			// A cut style must not colour the box.
			left += ansiReset
		}
		right := ansi.TruncateLeft(row, x+bw, "")
		bg[y+i] = left + padRight(l, bw) + right
	}
	return strings.Join(bg, "\n")
}
