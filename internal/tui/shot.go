package tui

import (
	"errors"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
	"github.com/makaveli2p/stack-overflow/internal/shot"
)

// Screenshots (batch 13). P draws the screen as a PNG on the server and
// shows a link to it. See DESIGN.md "Batch 13". Batch 15: every link also
// goes to the table chat, at once between hands, or when the hand ends.
const (
	shotEvery = 3 * time.Second  // one shot in this time for each session
	shotFor   = 60 * time.Second // how long the link shows
	// shotWaitFor is how long the "Screenshot…" notice shows at most. The
	// result replaces it.
	shotWaitFor = 30 * time.Second

	shotBusyNotice = "Screenshot…"
	shotRateNotice = "One screenshot every 3 seconds"
	shotWaitNotice = "The screenshot is not ready yet"
	shotOffReason  = "screenshots are off on this server"
	shotLinkHint   = " · Cmd+click to open"
	// shotLaterHint follows the link of a shot that waits for the end of
	// the hand (Session.ShareShot gave later).
	shotLaterHint = " · goes to the chat when the hand ends"
)

// shotState is the screenshot state of one session.
type shotState struct {
	hook func(shot.Frame) (shot.Links, error) // nil: screenshots are off
	busy bool                                 // a shot runs now
	last time.Time                            // the start of the last shot
}

// shotDoneMsg is the result of the hook.
type shotDoneMsg struct {
	url string // shot.Links.Own
	err error
	// later is true when the link waits for the end of the hand
	// (Session.ShareShot).
	later bool
	// shareErr is the error of Session.ShareShot.
	shareErr error
}

// screenshot takes the screen as it is now and starts the hook in a
// tea.Cmd, because drawing the PNG takes time.
//
// Every shot goes to the table chat (DESIGN.md "Batch 15"). During a hand
// the frame shows the own hole cards; the server posts the link only when
// the hand ends. The frame leaves out the text that is the viewer's own
// and that the chat must not get: the notice line, the bank and the
// rabbit hunt offer with its count (m.public). The help box is never in a
// frame: P closes it first. The buy-in picker, the profile box and the
// dashboard are never in a frame: P does nothing in the picker and the
// dashboard, and closes the profile box first.
func (m *model) screenshot() tea.Cmd {
	now := m.clock()
	switch {
	case m.shot.hook == nil:
		m.flash("Screenshot failed: "+shotOffReason, true)
		return nil
	case !m.shot.last.IsZero() && now.Sub(m.shot.last) < shotEvery:
		// A fast second press must not lose the link of the first: the
		// link shows again when the rate notice ends.
		link := m.notice
		m.flash(shotRateNotice, false)
		if link.url != "" {
			link.then = nil
			m.notice.then = &link
		}
		return nil
	case m.shot.busy:
		m.flash(shotWaitNotice, false)
		return nil
	}
	saved := m.notice
	m.notice, m.public = notice{}, true
	view := m.View()
	m.notice, m.public = saved, false
	f := shot.Frame{
		View:   view,
		Width:  m.width,
		Height: m.height,
		Dark:   m.darkBackground(),
		Title:  m.shotTitle(),
	}
	m.shot.busy, m.shot.last = true, now
	m.notice = notice{text: shotBusyNotice, until: now.Add(shotWaitFor)}
	hook, sess := m.shot.hook, m.sess
	return func() tea.Msg {
		links, err := hook(f)
		msg := shotDoneMsg{url: links.Own, err: err}
		if err == nil && links.Own != "" {
			table := links.Table
			if table == "" {
				table = links.Own
			}
			msg.later, msg.shareErr = sess.ShareShot(table)
		}
		return msg
	}
}

// shotDone shows the result of a shot. On the panic screen the notice is
// not visible; the link shows when the panic screen ends, if its time is
// not over.
func (m *model) shotDone(msg shotDoneMsg) {
	m.shot.busy = false
	switch {
	case msg.err != nil:
		reason := msg.err.Error()
		if errors.Is(msg.err, shot.ErrTooBig) {
			reason = shot.ErrTooBig.Error()
		}
		m.flash("Screenshot failed: "+reason, true)
	case msg.url == "":
		m.flash("Screenshot failed: the server gave no link", true)
	case msg.shareErr != nil:
		m.notice = m.linkNotice(msg.url,
			"Screenshot: "+msg.url+" (not shared: "+errText(msg.shareErr)+")",
			"Screenshot: "+msg.url+" (not shared)",
			msg.url+" (not shared)")
	case msg.later:
		m.notice = m.linkNotice(msg.url,
			"Screenshot: "+msg.url+shotLaterHint,
			msg.url+shotLaterHint,
			msg.url+" · to the chat after the hand")
	default:
		lead := "Screenshot shared in the chat: "
		m.notice = m.linkNotice(msg.url, lead+msg.url+shotLinkHint, lead+msg.url, "Shared in the chat: "+msg.url)
	}
}

// linkNotice is a notice with the link url that shows for shotFor. forms
// are its texts, widest first; each one holds url in full. The notice
// line shows the first form that fits (linkNoticeLine).
func (m *model) linkNotice(url string, forms ...string) notice {
	return notice{text: forms[0], url: url, forms: forms, until: m.clock().Add(shotFor)}
}

// sharedShotNotice tells the viewer that another player shared a
// screenshot in the chat. A line that the end of a hand posts is a new
// line too.
func (m *model) sharedShotNotice(c server.ChatLine) {
	lead := c.Name + " shared a screenshot: "
	m.notice = m.linkNotice(c.Shot, lead+c.Shot+shotLinkHint, lead+c.Shot, c.Name+": "+c.Shot)
}

// shotTitle is the title bar of the PNG.
func (m *model) shotTitle() string {
	title := appTitle + " — " + m.snap.Me.Name
	if t := m.snap.Table; t.InHand && t.HandNumber > 0 {
		title += fmt.Sprintf(" · hand #%d", t.HandNumber)
	}
	return title
}

// darkBackground reports whether the player's terminal has a dark
// background.
func (m *model) darkBackground() bool {
	if m.st.r == nil {
		return true
	}
	return m.st.r.HasDarkBackground()
}

// linkNoticeLine draws the notice with a link. The URL is never cut: the
// line drops the other words first (the forms of the notice, then "› "
// and the URL). When the URL alone is wider than w, the line is the URL
// in full, and the frame clip cuts it at the screen edge.
func (m *model) linkNoticeLine(w int) string {
	url := m.notice.url
	forms := m.notice.forms
	if len(forms) == 0 {
		forms = []string{m.notice.text}
	}
	for _, text := range append(slices.Clone(forms), url) {
		if text = "› " + text; ansi.StringWidth(text) <= w {
			return m.st.info.Render(text)
		}
	}
	return m.st.info.Render(url)
}
