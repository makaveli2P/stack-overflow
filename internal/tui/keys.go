package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

func (m *model) handleKey(k tea.KeyMsg) tea.Cmd {
	// The panic key comes first. F20 works on every screen. Ctrl+H works
	// when no text input has focus; in a text input it deletes a character
	// (see panicKey).
	if m.panic.on {
		return m.panicKeyPress(k)
	}
	if m.isPanicKey(k) {
		if m.gone || !m.haveSnap {
			return nil
		}
		return m.pressPanic()
	}
	if k.Type == tea.KeyCtrlH {
		k = tea.KeyMsg{Type: tea.KeyBackspace, Alt: k.Alt}
	}
	// Fast typing over SSH can deliver several runes in one message.
	// Handle them one by one, so that "t" can open the chat for the rest.
	if k.Type == tea.KeyRunes && len(k.Runes) > 1 && !k.Paste && m.mode != modeChat && !m.typingMode() {
		var cmds []tea.Cmd
		for i, r := range k.Runes {
			if m.mode == modeChat {
				m.typeRunes(k.Runes[i:])
				break
			}
			cmds = append(cmds, m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
		}
		return tea.Batch(cmds...)
	}
	key := k.String()
	if key == "ctrl+c" {
		if m.mode == modeConfirm && m.ask.quit {
			return m.quit()
		}
		return m.requestQuit()
	}
	if m.gone {
		return nil
	}
	switch m.mode {
	case modeChat:
		return m.chatKey(k)
	case modeRaise:
		return m.raiseKey(key)
	case modeConfirm:
		return m.confirmKey(key)
	case modeWelcome:
		return m.welcomeKey(k)
	case modeLink:
		return m.linkKey(k)
	case modeRename:
		return m.renameKey(k)
	case modeDash:
		return m.dashKey(k)
	case modeSit:
		return m.sitKey(key)
	case modeProfile:
		if cmd, done := m.profileKey(strings.ToLower(key)); done {
			return cmd
		}
	}
	if len([]rune(key)) == 1 {
		key = strings.ToLower(key)
	}
	if m.help {
		// Any key closes the help. ?, Esc and Q only close it; the other
		// keys then work as on the table, so that the action bar under the
		// help does what it shows.
		m.help = false
		if key == "?" || key == "esc" || key == "q" {
			return nil
		}
	}
	return m.tableKey(key)
}

func (m *model) tableKey(key string) tea.Cmd {
	if key != "a" {
		m.disarm()
	}
	switch key {
	case "q":
		return m.requestQuit()
	case "?":
		m.help = true
	case "tab":
		m.setTab((m.tab + 1) % numTabs)
	case "shift+tab":
		m.setTab((m.tab + numTabs - 1) % numTabs)
	case "t", "/":
		m.mode, m.chatBuf = modeChat, m.chatBuf[:0]
	case "f":
		return m.fold()
	case "c", "k":
		return m.checkOrCall()
	case "r":
		return m.openRaise()
	case "b":
		if me, ok := m.mySeat(); ok && me.Busted && m.snap.Table.Legal == nil {
			return m.rebuy()
		}
		return m.openRaise()
	case "a":
		return m.allIn()
	case "s":
		return m.sit()
	case "l":
		return m.leave()
	case "o":
		return m.toggleSitOut()
	case "d":
		m.openDash()
	case "m":
		m.openProfile()
	case "n":
		return m.toggleAlerts()
	case "h":
		return m.rabbitHunt()
	case "1", "2", "3":
		return m.showCards(key)
	case "e":
		return m.extend()
	case "g":
		return m.start()
	case "i":
		m.toggleRanks()
	case "p":
		return m.screenshot()
	}
	return nil
}

// canStart reports whether G is useful now: the table waits for a start,
// the viewer is seated, and 2 players can play.
func (m *model) canStart() bool {
	_, seated := m.mySeat()
	return m.snap.Waiting && seated && readyIn(m.snap) >= 2
}

// startedNotice is the notice for G while the game runs.
const startedNotice = "The game runs. G works only while the table waits to start."

// start asks the room to start the game. The room checks the seat and the
// player count; its error shows on the status line.
func (m *model) start() tea.Cmd {
	if !m.snap.Waiting {
		m.flash(startedNotice, false)
		return nil
	}
	s := m.sess
	return call(callStart, s.Start)
}

// toggleRanks shows or hides the hand rankings box for this session.
func (m *model) toggleRanks() {
	m.hideRanks = !m.hideRanks
	text := "Hand rankings on."
	if m.hideRanks {
		text = "Hand rankings off. Press I to show them."
	}
	if m.width < fullW || m.height < fullH {
		text += fmt.Sprintf(" The box shows in a window of %dx%d or more.", fullW, fullH)
	}
	m.flash(text, false)
}

// extend uses one time extension. It works only on the viewer's own turn;
// the server says when no clock runs or no extension is left, and that
// error shows on the notice line.
func (m *model) extend() tea.Cmd {
	if _, ok := m.legal(); !ok {
		return nil
	}
	s := m.sess
	return call(callPlain, s.Extend)
}

// extendLabels returns the labels of the E hint, longest first, for
// example "+30s (3)" and "+30s". It returns nil when the hint must not
// show: no clock runs, or the viewer has no extension left.
func (m *model) extendLabels() []string {
	n, d := m.snap.Me.ExtensionsLeft, m.snap.Config.ExtensionTime
	if _, running := m.turnLeft(); !running || n <= 0 || d <= 0 {
		return nil
	}
	add := "+" + durationText(d)
	return []string{fmt.Sprintf("%s (%d)", add, n), add}
}

// toggleAlerts turns the bell and desktop notifications on or off.
func (m *model) toggleAlerts() tea.Cmd {
	s, on := m.sess, !m.snap.Me.Alerts
	text := "Alerts off: no bell, no desktop notification."
	if on {
		text = "Alerts on: bell, and a desktop notification when this window is in the background."
	}
	return callOK(callPlain, text, func() error { return s.SetAlerts(on) })
}

// canRabbit reports whether the viewer may hunt the rabbit now.
func (m *model) canRabbit() bool {
	t := m.snap.Table
	_, seated := m.mySeat()
	return seated && !t.InHand && t.Result != nil && t.Result.CanRabbit
}

func (m *model) rabbitHunt() tea.Cmd {
	if !m.canRabbit() {
		m.flash("No rabbit to hunt: that works between hands, after everyone folded.", true)
		return nil
	}
	s := m.sess
	return call(callPlain, s.RabbitHunt)
}

// showable returns which of the viewer's hole cards the viewer can still
// show after the hand: the winner of a hand that ended without a showdown,
// and a player who folded in the hand (batch 9), for cards not shown yet.
func (m *model) showable() (left, right bool) {
	t := m.snap.Table
	me, ok := m.mySeat()
	r := t.Result
	if !ok || t.InHand || r == nil || len(me.Cards) != 2 || !m.dealtIn(t.MySeat) {
		return false, false
	}
	if !m.wonNoShowdown() && !m.foldedLast(me.PlayerID) {
		return false, false
	}
	shown := m.revealed(t.MySeat)
	left, right = !slices.Contains(shown, me.Cards[0]), !slices.Contains(shown, me.Cards[1])
	return left, right
}

// wonNoShowdown reports whether the viewer won the last hand because
// everyone else folded.
func (m *model) wonNoShowdown() bool {
	r := m.snap.Table.Result
	me, ok := m.mySeat()
	if !ok || r == nil || !r.NoShowdown {
		return false
	}
	for _, w := range r.Winnings {
		if w.PlayerID == me.PlayerID && w.Amount > 0 {
			return true
		}
	}
	return false
}

// foldedLast reports whether the player id folded in the last hand. The
// engine clears SeatView.Folded when the hand ends, so the TUI reads the
// fold from Result.Actions.
func (m *model) foldedLast(id string) bool {
	r := m.snap.Table.Result
	if r == nil || id == "" {
		return false
	}
	return slices.ContainsFunc(r.Actions, func(a engine.ActionRecord) bool { return a.PlayerID == id && a.Type == engine.Fold })
}

// canShowAtEnd reports whether the viewer folded in the live hand, so that
// keys 1, 2 and 3 store a show for the end of the hand.
func (m *model) canShowAtEnd() bool {
	me, ok := m.mySeat()
	return ok && m.snap.Table.InHand && me.InHand && me.Folded && len(me.Cards) == 2
}

// canShow reports whether keys 1, 2 and 3 do something now.
func (m *model) canShow() bool {
	l, r := m.showable()
	return l || r || m.canShowAtEnd()
}

// showKeys maps key 1, 2 or 3 to the left and right card.
func showKeys(key string) (left, right bool) {
	return key == "1" || key == "3", key == "2" || key == "3"
}

func (m *model) showCards(key string) tea.Cmd {
	s := m.sess
	if m.canShowAtEnd() {
		// During the hand the room only stores the choice; the same key
		// again cancels it. Nothing shows until the hand ends.
		left, right := showKeys(key)
		return call(callPlain, func() error { return s.ShowCards(left, right) })
	}
	canL, canR := m.showable()
	if !canL && !canR {
		return nil
	}
	left, right := showKeys(key)
	left, right = left && canL, right && canR
	if !left && !right {
		return nil
	}
	return call(callPlain, func() error { return s.ShowCards(left, right) })
}

func (m *model) setTab(t panelTab) {
	m.tab = t
	if t == tabChat {
		m.markChatSeen()
	}
}

// runoutNotice answers an action key during a run-out.
const runoutNotice = "All-in run-out: no actions until the board is dealt."

// legal returns the viewer's legal actions, or shows a notice when it is
// not the viewer's turn.
func (m *model) legal() (engine.Legal, bool) {
	l := m.snap.Table.Legal
	if l == nil {
		text := "It is not your turn."
		if m.snap.Table.InHand && m.snap.Table.Runout {
			text = runoutNotice
		}
		m.flash(text, true)
		return engine.Legal{}, false
	}
	return *l, true
}

func (m *model) act(a engine.Action) tea.Cmd {
	if m.acting {
		return nil
	}
	m.acting = true
	m.disarm()
	s := m.sess
	return call(callAct, func() error { return s.Act(a) })
}

func (m *model) fold() tea.Cmd {
	l, ok := m.legal()
	if !ok || !l.Fold {
		return nil
	}
	return m.act(engine.Action{Type: engine.Fold})
}

func (m *model) checkOrCall() tea.Cmd {
	l, ok := m.legal()
	switch {
	case !ok:
		return nil
	case l.Check:
		return m.act(engine.Action{Type: engine.Check})
	case l.Call:
		return m.act(engine.Action{Type: engine.Call})
	}
	return nil
}

func (m *model) openRaise() tea.Cmd {
	if _, ok := m.legal(); !ok {
		return nil
	}
	rm, ok := m.raiseMath()
	if !ok {
		m.flash("You cannot bet or raise now. Call, fold or go all-in.", true)
		return nil
	}
	m.mode = modeRaise
	m.raise.set(rm.legal.MinTo)
	m.help = false
	return nil
}

// allIn needs two presses of A within armedFor, so a stray key never
// puts the whole stack in.
func (m *model) allIn() tea.Cmd {
	l, ok := m.legal()
	if !ok || !l.AllIn {
		return nil
	}
	if m.allInArmed() {
		return m.act(engine.Action{Type: engine.AllIn})
	}
	m.allInAt = m.clock()
	m.flash(armNotice, false)
	return nil
}

// sit takes the first free seat. With an empty bank the player buys in
// at once; with chips in the bank the buy-in picker opens.
func (m *model) sit() tea.Cmd {
	if _, ok := m.mySeat(); ok {
		return nil
	}
	if m.snap.Me.Bank <= 0 {
		s := m.sess
		return call(callPlain, func() error { return s.SitWith(-1, server.SitBuyIn) })
	}
	m.mode, m.pick = modeSit, sitPicker{}
	return nil
}

func (m *model) rebuy() tea.Cmd {
	s := m.sess
	return call(callPlain, s.Rebuy)
}

func (m *model) leave() tea.Cmd {
	if _, ok := m.mySeat(); !ok {
		return nil
	}
	s := m.sess
	stand := func() tea.Cmd { return call(callPlain, s.Stand) }
	if m.inLiveHand() {
		m.ask = confirm{text: "Leave your seat? Your hand will be folded.", cmd: stand}
		m.mode = modeConfirm
		return nil
	}
	return stand()
}

func (m *model) toggleSitOut() tea.Cmd {
	me, ok := m.mySeat()
	if !ok {
		return nil
	}
	s, out := m.sess, !me.SittingOut
	return call(callPlain, func() error { return s.SitOut(out) })
}

func (m *model) requestQuit() tea.Cmd {
	if !m.inLiveHand() || m.gone {
		return m.quit()
	}
	m.ask = confirm{text: "Quit while you are in a hand?", quit: true, cmd: m.quit}
	m.mode = modeConfirm
	return nil
}

// quit closes the session and ends the program.
func (m *model) quit() tea.Cmd {
	s := m.sess
	return func() tea.Msg {
		s.Close()
		return tea.QuitMsg{}
	}
}

func (m *model) confirmKey(key string) tea.Cmd {
	m.mode = modeTable
	ask := m.ask
	m.ask = confirm{}
	if key == "y" || key == "Y" || (ask.quit && (key == "q" || key == "Q")) {
		return ask.cmd()
	}
	return nil
}

// raiseKey handles the amount editor. Digits always type. h, t, p and a
// pick ½ pot, ⅔ pot, pot and all-in. c and f close the editor and act.
// e adds time to the clock and keeps the editor open.
func (m *model) raiseKey(key string) tea.Cmd {
	rm, ok := m.raiseMath()
	if !ok {
		m.mode = modeTable
		return nil
	}
	e := &m.raise
	if len([]rune(key)) == 1 {
		key = strings.ToLower(key)
	}
	switch key {
	case "esc":
		m.mode = modeTable
	case "enter":
		m.mode = modeTable
		return m.act(rm.action(e.amount))
	case "c":
		m.mode = modeTable
		return m.checkOrCall()
	case "f":
		m.mode = modeTable
		return m.fold()
	case "backspace":
		e.backspace()
	case "right":
		e.set(rm.clamp(rm.clamp(e.amount) + rm.bigBet))
	case "left":
		e.set(rm.clamp(rm.clamp(e.amount) - rm.bigBet))
	case "up":
		e.set(rm.stepUp(rm.clamp(e.amount)))
	case "down":
		e.set(rm.stepDown(rm.clamp(e.amount)))
	case "h", "t", "p":
		e.set(rm.potTo(presets[strings.Index("htp", key)]))
	case "a":
		e.set(rm.legal.MaxTo)
	case "e":
		// The editor stays open, so the typed amount stays.
		return m.extend()
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		e.digit(int64(key[0] - '0'))
	}
	return nil
}

func (m *model) chatKey(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeTable
	case tea.KeyEnter:
		m.mode = modeTable
		text := strings.TrimSpace(string(m.chatBuf))
		m.chatBuf = m.chatBuf[:0]
		if text == "" {
			return nil
		}
		s := m.sess
		return call(callPlain, func() error { return s.Chat(text) })
	case tea.KeyBackspace:
		if n := len(m.chatBuf); n > 0 {
			m.chatBuf = m.chatBuf[:n-1]
		}
	case tea.KeyCtrlU:
		m.chatBuf = m.chatBuf[:0]
	case tea.KeySpace:
		m.typeRunes([]rune{' '})
	case tea.KeyRunes:
		m.typeRunes(k.Runes)
	}
	return nil
}

func (m *model) typeRunes(rs []rune) {
	for _, r := range rs {
		if len(m.chatBuf) >= maxChatRunes {
			return
		}
		if unicode.IsPrint(r) {
			m.chatBuf = append(m.chatBuf, r)
		}
	}
}
