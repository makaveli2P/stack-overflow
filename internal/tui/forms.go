package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Input limits. The server checks the real rules; these only stop
// runaway typing.
const (
	maxNameRunes  = 12
	maxLoginRunes = 32
	pinDigits     = 4
)

// textInput is a one-line text field.
type textInput struct {
	buf    []rune
	limit  int
	mask   bool // draw • for each rune (PIN)
	digits bool // accept only 0-9
}

func (t *textInput) set(s string) {
	t.buf = t.buf[:0]
	t.typeRunes([]rune(s))
}

func (t *textInput) String() string { return string(t.buf) }

func (t *textInput) typeRunes(rs []rune) {
	for _, r := range rs {
		if t.limit > 0 && len(t.buf) >= t.limit {
			return
		}
		if !unicode.IsPrint(r) || (t.digits && (r < '0' || r > '9')) {
			continue
		}
		t.buf = append(t.buf, r)
	}
}

// key edits the field. It reports false for keys that the field does not use.
func (t *textInput) key(k tea.KeyMsg) bool {
	switch k.Type {
	case tea.KeyBackspace:
		if n := len(t.buf); n > 0 {
			t.buf = t.buf[:n-1]
		}
	case tea.KeyCtrlU:
		t.buf = t.buf[:0]
	case tea.KeySpace:
		t.typeRunes([]rune{' '})
	case tea.KeyRunes:
		t.typeRunes(k.Runes)
	default:
		return false
	}
	return true
}

// forms holds the state of the welcome, link and rename inputs.
type forms struct {
	name     textInput
	login    textInput
	pin      textInput
	field    int    // link form: 0 = login, 1 = PIN
	linkBack mode   // where Esc goes from the link form
	err      string // the last error of a form call
	busy     bool   // a form call is running
}

// typingMode reports whether keys go to a text field, so that several
// runes in one message must not be split into commands.
func (m *model) typingMode() bool {
	switch m.mode {
	case modeWelcome, modeLink, modeRename:
		return true
	case modeDash:
		return m.dash.confirm
	}
	return false
}

// inWelcome reports whether the welcome flow (name or link) is on screen.
func (m *model) inWelcome() bool {
	return m.mode == modeWelcome || (m.mode == modeLink && m.forms.linkBack == modeWelcome)
}

// syncForms opens the welcome screen for a new account, and closes forms
// that the new snapshot made pointless.
func (m *model) syncForms() {
	me := m.snap.Me
	switch {
	case me.NeedsName && !m.welcomeDone && !m.inWelcome() && !m.forms.busy:
		m.mode = modeWelcome
		m.forms.name = textInput{limit: maxNameRunes}
		m.forms.name.set(me.Name)
		m.forms.err = ""
		m.help = false
	case !me.NeedsName && m.inWelcome() && !m.forms.busy:
		m.mode = modeTable
	case m.mode == modeLink && !me.CanLink && !m.forms.busy:
		m.mode = modeTable
	}
}

// formFieldKey routes a key to a text field and clears an old error.
func (m *model) formFieldKey(t *textInput, k tea.KeyMsg) {
	if t.key(k) {
		m.forms.err = ""
	}
}

func (m *model) welcomeKey(k tea.KeyMsg) tea.Cmd {
	if m.forms.busy {
		return nil
	}
	switch k.String() {
	case "enter":
		name := strings.TrimSpace(m.forms.name.String())
		if name == "" {
			m.forms.err = "Type a name first, or press Esc to keep " + quote(m.snap.Me.Name) + "."
			return nil
		}
		return m.saveName(callWelcome, name)
	case "esc":
		return m.saveName(callWelcome, m.snap.Me.Name)
	case "tab", "ctrl+l":
		if m.snap.Me.CanLink {
			m.openLink(modeWelcome)
		}
		return nil
	}
	m.formFieldKey(&m.forms.name, k)
	return nil
}

func (m *model) saveName(kind callKind, name string) tea.Cmd {
	m.forms.busy, m.forms.err = true, ""
	s := m.sess
	ok := "You play as " + name + "."
	if kind == callWelcome {
		ok = "Welcome to Stack Overflow, " + name + "! Press S to sit down."
	}
	return callOK(kind, ok, func() error { return s.SetName(name) })
}

func (m *model) openLink(back mode) {
	m.mode = modeLink
	m.forms.linkBack = back
	m.forms.login = textInput{limit: maxLoginRunes}
	m.forms.pin = textInput{limit: pinDigits, mask: true, digits: true}
	m.forms.field = 0
	m.forms.err = ""
}

func (m *model) linkKey(k tea.KeyMsg) tea.Cmd {
	if m.forms.busy {
		return nil
	}
	f := &m.forms
	switch k.String() {
	case "esc":
		m.mode = f.linkBack
		f.err = ""
		return nil
	case "tab", "shift+tab", "up", "down":
		f.field = 1 - f.field
		return nil
	case "enter":
		login := strings.ToLower(strings.TrimSpace(f.login.String()))
		switch {
		case login == "":
			f.field, f.err = 0, "Type the login you used with your PIN."
		case f.field == 0:
			f.field = 1
		case len(f.pin.buf) != pinDigits:
			f.err = "The PIN has 4 digits."
		default:
			f.busy, f.err = true, ""
			pin, s := f.pin.String(), m.sess
			return callOK(callLink, "Linked. This key now logs in as "+login+".", func() error { return s.LinkPIN(login, pin) })
		}
		return nil
	}
	if f.field == 0 {
		m.formFieldKey(&f.login, k)
	} else {
		m.formFieldKey(&f.pin, k)
	}
	return nil
}

func (m *model) openProfile() {
	m.mode = modeProfile
	m.forms.err = ""
}

// profileKey handles the M box. done is false for keys that close the
// box and then work as table keys (for example C on the viewer's turn).
func (m *model) profileKey(key string) (cmd tea.Cmd, done bool) {
	switch key {
	case "esc", "m", "q":
		m.mode = modeTable
		return nil, true
	case "r":
		if m.snap.Table.Legal != nil {
			// On the viewer's turn the action bar under the box
			// shows "R Raise": R closes the box and raises.
			break
		}
		m.mode = modeRename
		m.forms.name = textInput{limit: maxNameRunes}
		m.forms.name.set(m.snap.Me.Name)
		m.forms.err = ""
		return nil, true
	case "n":
		return m.toggleAlerts(), true
	case "l":
		if m.snap.Me.CanLink {
			m.openLink(modeProfile)
			return nil, true
		}
		// The box shows no L: close it, and do not leave the seat.
		m.mode = modeTable
		return nil, true
	}
	m.mode = modeTable
	return nil, false
}

func (m *model) renameKey(k tea.KeyMsg) tea.Cmd {
	if m.forms.busy {
		return nil
	}
	switch k.String() {
	case "esc":
		m.mode, m.forms.err = modeProfile, ""
		return nil
	case "enter":
		name := strings.TrimSpace(m.forms.name.String())
		switch {
		case name == "":
			m.forms.err = "Type a name."
		case name == m.snap.Me.Name:
			m.mode = modeProfile
		default:
			return m.saveName(callRename, name)
		}
		return nil
	}
	m.formFieldKey(&m.forms.name, k)
	return nil
}

func quote(s string) string { return "“" + s + "”" }

// ---- drawing ----

// titledBox draws lines in a rounded box w cells wide, with the title set
// into the top border.
func (m *model) titledBox(title string, lines []string, w int, border lipgloss.TerminalColor) string {
	st := m.st
	inner := w - 4
	body := make([]string, len(lines))
	for i, l := range lines {
		body[i] = fit(l, inner)
	}
	box := st.r.NewStyle().Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(border).Padding(0, 1).Width(w - 2).Render(strings.Join(body, "\n"))
	edge := st.r.NewStyle().Foreground(border)
	t := fit(" "+title+" ", max(w-4, 0))
	top := edge.Render("╭─") + st.gold.Render(t) + edge.Render(strings.Repeat("─", max(w-3-ansi.StringWidth(t), 0))+"╮")
	return top + "\n" + box
}

// squeeze removes blank lines, from the bottom up, until lines fit in h.
func squeeze(lines []string, h int) []string {
	for i := len(lines) - 1; i >= 0 && len(lines) > h; i-- {
		if ansi.Strip(lines[i]) == "" {
			lines = append(lines[:i], lines[i+1:]...)
		}
	}
	return lines
}

// field draws a text input w cells wide. A focused field shows a cursor.
func (m *model) field(t textInput, w int, focused bool) string {
	text := t.String()
	if t.mask {
		text = strings.Repeat("•", len(t.buf))
	}
	if focused {
		text += "▏"
	}
	if over := ansi.StringWidth(text) + 2 - w; over > 0 {
		text = ansi.TruncateLeft(text, over, "…")
	}
	style := m.st.inputBox
	if !focused {
		style = m.st.inputIdle
	}
	return style.Render(" " + padRight(text, max(w-2, 0)) + " ")
}

// logo is the brand line: STACK OVERFLOW between the four suits.
func (m *model) logo() string {
	st := m.st
	return st.bold.Render("♠ ") + st.redSuit.Render("♥") + "  " +
		st.brand.Render("S T A C K   O V E R F L O W") + "  " +
		st.redSuit.Render("♦") + st.bold.Render(" ♣")
}

// keylessTip is the ssh-keygen tip for players who log in with a PIN.
func (m *model) keylessTip() []string {
	st := m.st
	return []string{
		st.info.Render("Tip: ") + st.text.Render("make an SSH key and never type a PIN again:"),
		"     " + st.gold.Render("ssh-keygen -t ed25519") + st.dim.Render("  (press Enter 3 times)"),
	}
}

func (m *model) welcomeScreen(w, h int) string {
	if m.mode == modeLink {
		return m.linkScreen(w, h)
	}
	st := m.st
	bw := min(w, 62)
	inner := bw - 4
	me := m.snap.Me
	lines := []string{
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, m.logo()),
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, st.dim.Render("No-Limit Texas Hold'em at the office")),
		"",
		st.bold.Render("Pick the name others see"),
		m.field(m.forms.name, min(inner, 30), !m.forms.busy),
		st.faint.Render("1 to 12 letters, digits, spaces or - _ ."),
	}
	lines = append(lines, m.formStatus()...)
	if me.CanLink {
		if m.forms.err != "" || m.forms.busy {
			lines = append(lines, "")
		}
		lines = append(lines, st.text.Render("Played before with a PIN? ")+m.btn("Tab", "link it"))
	}
	if me.Keyless {
		lines = append(lines, "")
		lines = append(lines, m.keylessTip()...)
	}
	lines = append(lines, "", m.hintText([]hint{{"Enter", "save"}, {"Esc", "keep " + quote(me.Name)}, {"Ctrl+C", "quit"}}))
	return m.placeBox(m.titledBox("STACK OVERFLOW", squeeze(lines, h-2), bw, colFelt), w, h)
}

// formStatus is the error or "saving" line under a form, or a blank line.
func (m *model) formStatus() []string {
	switch {
	case m.forms.busy:
		return []string{m.st.dim.Render("Saving…")}
	case m.forms.err != "":
		return []string{m.st.err.Render("✗ " + m.forms.err)}
	}
	return []string{""}
}

func (m *model) linkScreen(w, h int) string {
	return m.placeBox(m.linkBox(min(w, 60), h), w, h)
}

func (m *model) linkBox(bw, h int) string {
	st := m.st
	f := m.forms
	inner := bw - 4
	label := func(s string, on bool) string {
		if on {
			return st.gold.Render(padRight(s, 7))
		}
		return st.dim.Render(padRight(s, 7))
	}
	fw := min(inner-7, 26)
	lines := []string{""}
	for _, l := range wrapLines("Attach this SSH key to the player you made with a PIN. Your bank, buy-ins and stats stay with that player.", inner, 0) {
		lines = append(lines, st.text.Render(l))
	}
	lines = append(lines, "",
		label("Login", f.field == 0)+m.field(f.login, fw, f.field == 0 && !f.busy),
		label("PIN", f.field == 1)+m.field(f.pin, min(fw, 8), f.field == 1 && !f.busy),
	)
	lines = append(lines, m.formStatus()...)
	back := "back"
	if f.linkBack == modeProfile {
		back = "cancel"
	}
	lines = append(lines, m.hintText([]hint{{"Tab", "next field"}, {"Enter", "link"}, {"Esc", back}}))
	return m.titledBox("LINK A PIN PLAYER", squeeze(lines, h-2), bw, colMe)
}

// placeBox centres a box in w x h.
func (m *model) placeBox(box string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, clip(box, w, h))
}

// hintText joins key hints like the footer does.
func (m *model) hintText(hs []hint) string {
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = m.st.gold.Render(h.key) + " " + m.st.dim.Render(h.label)
	}
	return strings.Join(parts, m.st.faint.Render(" · "))
}

// profileView draws the M box (or the link form opened from it) in w x h.
func (m *model) profileView(w, h int) string {
	if m.mode == modeLink {
		return m.placeBox(m.linkBox(min(w, 60), h), w, h)
	}
	st := m.st
	me := m.snap.Me
	bw := min(w, 56)
	row := func(label, value string) string { return st.dim.Render(padRight(label, 10)) + value }

	login := st.text.Render("SSH key")
	if p, ok := strings.CutPrefix(me.Key, "pin:"); ok || me.Keyless {
		login = st.text.Render("PIN")
		if ok {
			login += st.dim.Render(" · login " + p)
		}
	}
	alerts := st.win.Render("on") + st.dim.Render(" · bell + desktop notification")
	if !me.Alerts {
		alerts = st.err.Render("off")
	}
	name := st.me.Render(me.Name)
	if m.mode == modeRename {
		name = m.field(m.forms.name, 20, !m.forms.busy)
	}
	lines := []string{"", row("Name", name), row("Login", login), row("Alerts", alerts), row("Bank", st.text.Render(chips(me.Bank)))}
	if me.Admin {
		lines = append(lines, row("Role", st.gold.Render("host")+st.dim.Render(" · can mark the period settled")))
	}
	if m.mode == modeRename {
		lines = append(lines, m.formStatus()...)
		lines = append(lines, m.hintText([]hint{{"Enter", "save"}, {"Esc", "cancel"}}))
	} else {
		lines = append(lines, "")
		var hs []hint
		if m.snap.Table.Legal == nil {
			hs = append(hs, hint{"R", "rename"})
		}
		hs = append(hs, hint{"N", "alerts on/off"})
		if me.CanLink {
			hs = append(hs, hint{"L", "link PIN"})
		}
		hs = append(hs, hint{"Esc", "close"})
		lines = append(lines, m.hintText(hs))
	}
	if me.Keyless {
		lines = append(lines, "")
		lines = append(lines, m.keylessTip()...)
	}
	return m.placeBox(m.titledBox("MY PROFILE", squeeze(lines, h-2), bw, colMe), w, h)
}
