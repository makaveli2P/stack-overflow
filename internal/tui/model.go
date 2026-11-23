package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/makaveli2p/stack-overflow/internal/engine"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// session is the part of *server.Session that the TUI uses. Tests and the
// preview tool use a fake.
type session interface {
	Updates() <-chan server.Snapshot
	Sit(seat int) error
	SitWith(seat int, c server.SitChoice) error
	Stand() error
	Act(a engine.Action) error
	SitOut(out bool) error
	Rebuy() error
	Chat(text string) error
	ShareShot(url string) (later bool, err error)
	Close()
	SetName(name string) error
	LinkPIN(login, pin string) error
	RabbitHunt() error
	ShowCards(left, right bool) error
	SetAlerts(on bool) error
	MarkSettled() error
	Extend() error
	Panic() error
	Start() error
}

const (
	tickEvery    = 250 * time.Millisecond
	noticeFor    = 3 * time.Second
	armedFor     = 3 * time.Second
	quitDelay    = 2 * time.Second
	maxChatRunes = 200
	appTitle     = "Stack Overflow"
	armNotice    = "Press A again to go all-in."
)

// The client chooses the window size: SSH pty-req and window-change carry
// uint32 columns and rows. The frame has width*height cells, so the model
// clamps the size. A larger terminal shows the frame in its top-left corner.
const (
	maxWidth  = 400
	maxHeight = 150
)

// windowSize returns a safe size for the client's window.
func windowSize(width, height int) (int, int) {
	// Some clients report no size at first; assume a classic terminal.
	if width <= 0 || height <= 0 {
		return 80, 24
	}
	return min(width, maxWidth), min(height, maxHeight)
}

type mode int

const (
	modeTable mode = iota
	modeRaise
	modeChat
	modeConfirm
	modeWelcome // pick a display name (new account)
	modeLink    // link this key to a PIN account
	modeProfile // the M box
	modeRename  // the name input inside the M box
	modeDash    // the full-screen stats and settle-up dashboard
	modeSit     // the buy-in picker over the table
)

type panelTab int

const (
	tabChat panelTab = iota // the first and default tab
	tabLog
	tabPlayers
	numTabs
)

// Messages.
type (
	snapshotMsg struct{ snap server.Snapshot }
	closedMsg   struct{}
	tickMsg     time.Time
	callDoneMsg struct {
		kind callKind
		err  error
		ok   string // notice to show when the call succeeds
	}
)

// callKind says what a finished Session call was for, so that the model
// knows where to show its result.
type callKind int

const (
	callPlain   callKind = iota // errors go to the notice line
	callAct                     // a poker action: clears the "sending" state
	callWelcome                 // SetName from the welcome screen
	callLink                    // LinkPIN from the link form
	callRename                  // SetName from the profile box
	callSettle                  // MarkSettled from the dashboard
	callSit                     // SitWith from the buy-in picker
	callPanic                   // Panic (Ctrl+H)
	callStart                   // Start (G): errors go to the status line
)

type notice struct {
	text  string
	err   bool
	until time.Time
	url   string // a screenshot link in text; the notice line never cuts it
	// forms are the texts of a link notice, widest first, each with url
	// in full (linkNotice).
	forms []string
	// then is the notice that shows again when this one ends, if its time
	// is not over. A rate notice of the P key keeps the link this way.
	then *notice
}

// confirm is a yes/no question. cmd runs on yes.
type confirm struct {
	text string
	quit bool
	cmd  func() tea.Cmd
}

type model struct {
	sess    session
	updates <-chan server.Snapshot
	st      styles
	clock   func() time.Time

	width, height int

	snap     server.Snapshot
	haveSnap bool
	recvAt   time.Time // local time when snap arrived

	mode     mode
	raise    raiseEditor
	chatBuf  []rune
	ask      confirm
	help     bool
	tab      panelTab
	chatSeen time.Time
	acting   bool
	allInAt  time.Time // when the first A press armed all-in
	notice   notice
	gone     bool // the room closed the session
	myTurn   bool

	alert   func(Alert) // nil disables alerts
	blurred bool        // the terminal reported focus-out
	shot    shotState   // the P key (batch 13)
	// public is true while the TUI draws the frame of a screenshot (batch
	// 14; batch 15: every frame, because every shot goes to the chat).
	// The frame then leaves out the bank and the rabbit hunt offer with
	// its count (see shot.go).
	public bool

	forms       forms
	welcomeDone bool // the welcome screen saved a name; do not open it again
	dash        dashState
	pick        sitPicker // the buy-in picker (modeSit)
	panic       panicState
	hl          chatHL // the highlight of new chat messages

	hideRanks bool   // I hid the hand rankings box for this session
	startErr  notice // the last Start error, for the status line

	// dealtHand and dealtIDs record the players who were dealt into the
	// last hand seen live, by seat. Blind and button badges belong to
	// those players, not to a new occupant of the seat.
	dealtHand int
	dealtIDs  map[int]string
}

func newModel(sess *server.Session, r *lipgloss.Renderer, width, height int, opts Options) tea.Model {
	m := newModelWith(sess, r, width, height, time.Now)
	m.alert = opts.Alert
	m.shot.hook = opts.Shot
	return m
}

func newModelWith(sess session, r *lipgloss.Renderer, width, height int, clock func() time.Time) *model {
	width, height = windowSize(width, height)
	return &model{
		sess:    sess,
		updates: sess.Updates(),
		st:      newStyles(r),
		clock:   clock,
		width:   width,
		height:  height,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(waitSnapshot(m.updates), tick(), tea.SetWindowTitle(appTitle))
}

func waitSnapshot(ch <-chan server.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return closedMsg{}
		}
		return snapshotMsg{snap}
	}
}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// call runs one Session method off the update loop.
func call(kind callKind, f func() error) tea.Cmd {
	return callOK(kind, "", f)
}

// callOK is call with a notice to show when the call succeeds.
func callOK(kind callKind, ok string, f func() error) tea.Cmd {
	return func() tea.Msg { return callDoneMsg{kind: kind, err: f(), ok: ok} }
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = windowSize(msg.Width, msg.Height)
		return m, nil
	case snapshotMsg:
		return m, tea.Batch(m.applySnapshot(msg.snap), waitSnapshot(m.updates))
	case closedMsg:
		m.gone = true
		return m, tea.Tick(quitDelay, func(time.Time) tea.Msg { return tea.QuitMsg{} })
	case tickMsg:
		if !m.notice.until.IsZero() && m.clock().After(m.notice.until) {
			if next := m.notice.then; next != nil && !m.clock().After(next.until) {
				m.notice = *next
			} else {
				m.notice = notice{}
			}
		}
		if !m.startErr.until.IsZero() && m.clock().After(m.startErr.until) {
			m.startErr = notice{}
		}
		if !m.allInAt.IsZero() && !m.allInArmed() {
			m.disarm()
		}
		return m, tick()
	case callDoneMsg:
		m.callDone(msg)
		return m, nil
	case shotDoneMsg:
		m.shotDone(msg)
		return m, nil
	case panicNoticeDoneMsg, decoyStepMsg:
		return m, m.panicMsg(msg)
	case chatHLMsg:
		return m, m.chatHLDone(msg)
	case tea.FocusMsg:
		m.blurred = false
		return m, nil
	case tea.BlurMsg:
		m.blurred = true
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func errText(err error) string {
	if errors.Is(err, server.ErrRoomClosed) || errors.Is(err, server.ErrClosed) {
		return "the room is closed"
	}
	return err.Error()
}

// callDone shows the result of a Session call where it belongs.
func (m *model) callDone(msg callDoneMsg) {
	switch msg.kind {
	case callPanic:
		// The panic screen hides every notice: a notice gives the game
		// away. ErrPanicTooSoon thus shows only on the normal screens.
		if m.panic.on {
			return
		}
	case callAct:
		m.acting = false
	case callStart:
		// The status line tells the viewer how to start; the reason for
		// a failed start shows there too, in place of that text.
		if msg.err != nil {
			m.startErr = notice{text: errText(msg.err), err: true, until: m.clock().Add(noticeFor)}
			return
		}
		m.startErr = notice{}
	case callWelcome, callLink, callRename:
		m.forms.busy = false
		if msg.err != nil {
			m.forms.err = errText(msg.err)
			return
		}
		m.forms.err = ""
		if msg.kind != callRename {
			m.welcomeDone = true
		}
		m.mode = modeTable
	case callSettle:
		// The call is over: the confirmation takes keys again. On an
		// error it stays open with the reason under the typed word,
		// because the turn cue can hide the notice line.
		m.dash.busy = false
		if msg.err == nil {
			m.dash.confirm, m.dash.err = false, ""
		} else if m.dash.confirm {
			m.dash.err = errText(msg.err)
		}
	case callSit:
		// The picker closes after the call, also on an error: the
		// reason shows on the notice line, and S opens the picker again.
		m.pick.busy = false
		if m.mode == modeSit {
			m.mode = modeTable
		}
	}
	switch {
	case msg.err != nil:
		m.flash(errText(msg.err), true)
	case msg.ok != "":
		m.flash(msg.ok, false)
	}
}

// applySnapshot stores a new snapshot and keeps the input modes valid.
func (m *model) applySnapshot(s server.Snapshot) tea.Cmd {
	first := !m.haveSnap
	if s.Table.Runout {
		// Nobody acts during a run-out and no turn clock runs. Drop any
		// stale turn data, so that no action bar, clock or key works.
		s.Table.Legal, s.Table.ToAct, s.Deadline = nil, -1, time.Time{}
	}
	if m.haveSnap && (s.Waiting != m.snap.Waiting || s.Table.MySeat != m.snap.Table.MySeat || seatedIn(s) != seatedIn(m.snap)) {
		// The reason for a failed start is old news when the table changes.
		m.startErr = notice{}
	}
	newHand := m.haveSnap && s.Table.HandNumber != m.snap.Table.HandNumber
	prevChat := m.snap.Chat
	m.snap, m.haveSnap, m.recvAt = s, true, m.clock()
	shared, haveShared := m.followChat(prevChat, first)
	var dealCmd tea.Cmd
	if newHand {
		dealCmd = m.newDeal()
	}
	panicCmd := m.panicSnapshot(s.PanicSeq, first)
	if haveShared && !m.panic.on && m.mode != modeChat && !m.typingMode() {
		// After the panic check: the panic screen gets no such notice,
		// also not later. The chat line is enough.
		m.sharedShotNotice(shared)
	}
	hlCmd := m.chatHLTick() // after the panic check: the panic screen starts no tick
	if first || m.tab == tabChat {
		m.markChatSeen()
	}
	m.recordDealt()
	m.syncForms()
	m.dash.follow(s.Stats)
	m.keepHostTab()
	m.syncPicker()
	alert := m.alertCmd()
	legal := s.Table.Legal
	if legal == nil {
		m.disarm()
		if m.mode == modeRaise {
			m.mode = modeTable
		}
	} else if m.mode == modeRaise {
		if !legal.Bet && !legal.Raise {
			m.mode = modeTable
		} else {
			m.raise.clampTo(*legal)
		}
	}
	turn := legal != nil
	if turn == m.myTurn || m.panic.on {
		// The panic screen keeps its own window title; the exit from it
		// sets the title for the turn.
		m.myTurn = turn
		return tea.Batch(alert, panicCmd, dealCmd, hlCmd)
	}
	m.myTurn = turn
	return tea.Batch(alert, tea.SetWindowTitle(m.windowTitle()), dealCmd, hlCmd)
}

// newDealFor is how long the "new cards" notice shows after a deal.
const newDealFor = 2 * time.Second

// newDeal marks a new hand number. A viewer with new cards gets a short
// notice, unless an error notice or a screenshot link shows. The returned
// command clears the whole screen, so that a row that the terminal drew
// wrong (for example after a width mismatch) cannot stay on screen into
// the new hand.
func (m *model) newDeal() tea.Cmd {
	t := m.snap.Table
	if me, ok := m.mySeat(); ok && t.InHand && me.HasCards && len(me.Cards) == 2 && !m.notice.err && m.notice.url == "" {
		m.notice = notice{text: fmt.Sprintf("Hand #%d · new cards", t.HandNumber), until: m.clock().Add(newDealFor)}
	}
	return tea.ClearScreen
}

// seatedIn returns the number of players in a seat of s.
// readyIn counts the seated players who can play a hand: not sitting out
// and with chips. The room needs 2 of them to start.
func readyIn(s server.Snapshot) int {
	n := 0
	for _, sv := range s.Table.Seats {
		if !sv.Empty && !sv.SittingOut && sv.Stack > 0 {
			n++
		}
	}
	return n
}

func seatedIn(s server.Snapshot) int {
	n := 0
	for _, sv := range s.Table.Seats {
		if !sv.Empty {
			n++
		}
	}
	return n
}

// windowTitle is the terminal window title for the table.
func (m *model) windowTitle() string {
	if m.myTurn {
		return "▶ Your turn · " + appTitle
	}
	return appTitle
}

// alertCmd turns the snapshot's one-shot cue into an Alert. The bell
// always rings; the desktop notification goes out only while the
// terminal has no focus, because a focused player sees the screen. The
// panic screen stops both.
func (m *model) alertCmd() tea.Cmd {
	cue := m.snap.Alert
	// The panic screen sends no bell and no desktop notification: a popup
	// gives the game away.
	if cue == "" || m.alert == nil || !m.snap.Me.Alerts || m.panic.on {
		return nil
	}
	a := Alert{Bell: true, Notify: m.blurred, Title: appTitle}
	switch cue {
	case "turn":
		a.Body = "Your turn"
		if last := m.lastAction(); last != "" {
			a.Body += " — " + last
		}
	case "hurry":
		a.Body = "5 seconds left"
	default:
		return nil
	}
	send := m.alert
	return func() tea.Msg {
		send(a)
		return nil
	}
}

// lastAction returns the newest action or street line of the current
// hand, for example "Bob raises to 60", or "" when the log has none.
// Room lines in the same log ("Omar sits down with 1000", "erin is now
// Carol W", "Bob timed out", "Bob sits out") are not actions, so they
// are skipped.
func (m *model) lastAction() string {
	log := m.snap.Log
	for i := len(log) - 1; i >= 0; i-- {
		l := log[i]
		if strings.HasPrefix(l, "Hand #") {
			return ""
		}
		if isActionLine(l) {
			return l
		}
	}
	return ""
}

// actionVerbs are the texts of the engine's action and blind lines
// (engine/hand.go), with the street names of the street lines.
var actionVerbs = []string{" posts ", " is all-in for ", " calls ", " bets ", " raises to "}

// isActionLine reports whether a log line is a player action, a blind or
// a new street, and not a room line.
func isActionLine(l string) bool {
	if strings.HasSuffix(l, " folds") || strings.HasSuffix(l, " checks") {
		return true
	}
	for _, v := range actionVerbs {
		if strings.Contains(l, v) {
			return true
		}
	}
	for _, s := range []string{"Flop: ", "Turn: ", "River: "} {
		if strings.HasPrefix(l, s) {
			return true
		}
	}
	return false
}

// recordDealt stores the players who are dealt into the live hand.
func (m *model) recordDealt() {
	t := m.snap.Table
	if !t.InHand || t.HandNumber <= 0 {
		return
	}
	if m.dealtHand != t.HandNumber || m.dealtIDs == nil {
		m.dealtHand, m.dealtIDs = t.HandNumber, map[int]string{}
	}
	for i, sv := range t.Seats {
		if !sv.Empty && sv.InHand {
			m.dealtIDs[i] = sv.PlayerID
		}
	}
}

// dealtIn reports whether the player in seat i took part in the current
// hand, or in the last hand when no hand runs.
func (m *model) dealtIn(i int) bool {
	t := m.snap.Table
	sv := t.Seats[i]
	if t.InHand {
		return sv.InHand
	}
	if m.dealtHand != t.HandNumber {
		// The model saw no live snapshot of this hand.
		return true
	}
	id, ok := m.dealtIDs[i]
	return ok && id == sv.PlayerID
}

// allInArmed reports whether one A press armed the all-in, and the arm
// window is still open.
func (m *model) allInArmed() bool {
	return !m.allInAt.IsZero() && m.clock().Sub(m.allInAt) <= armedFor
}

// disarm cancels the all-in arm and removes its notice.
func (m *model) disarm() {
	m.allInAt = time.Time{}
	if m.notice.text == armNotice {
		m.notice = notice{}
	}
}

func (m *model) markChatSeen() {
	if n := len(m.snap.Chat); n > 0 {
		m.chatSeen = m.snap.Chat[n-1].At
	}
}

func (m *model) unreadChat() bool {
	n := len(m.snap.Chat)
	return n > 0 && m.snap.Chat[n-1].At.After(m.chatSeen)
}

func (m *model) flash(text string, isErr bool) {
	m.notice = notice{text: text, err: isErr, until: m.clock().Add(noticeFor)}
}

// serverNow estimates the server clock from the last snapshot.
func (m *model) serverNow() time.Time {
	return m.snap.Now.Add(m.clock().Sub(m.recvAt))
}

// turnLeft returns the time left on the turn clock, and false when no clock runs.
func (m *model) turnLeft() (time.Duration, bool) {
	if m.snap.Deadline.IsZero() {
		return 0, false
	}
	return max(m.snap.Deadline.Sub(m.serverNow()), 0), true
}

// clockTotal returns the full length of the running turn clock: the
// snapshot's ClockTotal, which grows with each time extension, or the
// configured TurnTimeout when the server sends none.
func (m *model) clockTotal() time.Duration {
	if m.snap.ClockTotal > 0 {
		return m.snap.ClockTotal
	}
	return m.snap.Config.TurnTimeout
}

// mySeat returns the viewer's seat, and false when the viewer is not seated.
func (m *model) mySeat() (engine.SeatView, bool) {
	i := m.snap.Table.MySeat
	if i < 0 || i >= len(m.snap.Table.Seats) {
		return engine.SeatView{}, false
	}
	return m.snap.Table.Seats[i], true
}

// inLiveHand reports whether the viewer holds a live hand now.
func (m *model) inLiveHand() bool {
	me, ok := m.mySeat()
	return ok && m.snap.Table.InHand && me.InHand && !me.Folded
}
