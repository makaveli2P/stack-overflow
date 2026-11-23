package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
	"github.com/makaveli2p/stack-overflow/internal/tui"
)

// sessionKey stores the room session in the SSH context.
type sessionKey struct{}

// loginTimerKey stores the login grace timer in the SSH context.
type loginTimerKey struct{}

// outputKey stores the locked session output in the SSH context.
type outputKey struct{}

// The TUI fits each line to the window, so no line must wrap. A client
// that draws a glyph wider than the server measures it makes a line too
// long. With auto-wrap (DECAWM) on, the terminal moves the rest of that
// line to the next row, and old cells then stay on the screen. So the
// server turns auto-wrap off before the first frame, and on again at exit.
const (
	autoWrapOff = "\x1b[?7l"
	autoWrapOn  = "\x1b[?7h"
)

// defaultLoginGrace is the time that a client has from the TCP connect to
// the start of an SSH session (the handshake, the login and the shell
// request). OpenSSH uses the same default for LoginGraceTime.
const defaultLoginGrace = 2 * time.Minute

func newSSHServer(o options, hostKey string, room *server.Room) (*ssh.Server, error) {
	return newSSHServerWith(o, hostKey, room, room)
}

// newSSHServerWith is newSSHServer with a separate PIN store, so that tests
// can check the auth rules without a room.
func newSSHServerWith(o options, hostKey string, room *server.Room, store pinStore) (*ssh.Server, error) {
	grace := o.loginGrace
	if grace <= 0 {
		grace = defaultLoginGrace
	}
	every, wait := o.keepAlive, o.keepAliveWait
	if every <= 0 {
		every = defaultKeepAlive
	}
	if wait <= 0 {
		wait = defaultKeepAliveWait
	}
	a := &auth{password: o.password, store: store}
	// Wish runs the last middleware first.
	opts := []ssh.Option{
		wish.WithAddress(net.JoinHostPort(o.host, strconv.Itoa(o.port))),
		wish.WithHostKeyPath(hostKey),
		withConnGuards(newConnLimiter(o.maxConns, o.maxConnsPerIP), grace),
		wish.WithMiddleware(
			bm.MiddlewareWithProgramHandler(teaHandler(room, o.port, o.shots), termenv.ANSI256),
			restoreAutoWrap,
			closeSession,
			activeterm.Middleware(),
			logConnections,
			endLoginGrace,
			keepAlive(every, wait),
		),
	}
	opts = append(opts, a.options()...)
	return wish.NewServer(opts...)
}

// teaHandler starts the TUI for each session. shots makes the screenshot
// links of the P key; nil turns screenshots off.
func teaHandler(room *server.Room, port int, shots *shotLinks) bm.ProgramHandler {
	return func(s ssh.Session) *tea.Program {
		key, _, ok := identity(s.Permissions().Permissions)
		if !ok {
			wish.Fatalln(s, "\r\n"+appName+" cannot tell who you are. Connect again.\r")
			return nil
		}
		name := s.User()
		// A connection from the server machine is the host. The host is
		// also an admin. The room adds the admins from -admin by the
		// credential of the login (key fingerprint or PIN login).
		host := isHostConn(s.RemoteAddr())
		sess, err := room.ConnectWith(key, name, server.ConnectOptions{Admin: host, Host: host, Addr: s.RemoteAddr().String()})
		if err != nil {
			wish.Fatalln(s, joinError(err, name, port))
			return nil
		}
		s.Context().SetValue(sessionKey{}, sess)
		pty, _, _ := s.Pty()
		out := newLockedWriter(sessionOutput(s))
		_, _ = io.WriteString(out, autoWrapOff)
		s.Context().SetValue(outputKey{}, out)
		m := tui.New(sess, bm.MakeRenderer(s), pty.Window.Width, pty.Window.Height, tui.Options{Alert: out.alert, Shot: shots.hook(s)})
		guard := newInputGuard(sessionInput(s), s.Context().Done(), func(reason string) {
			log.Warn("input limit: closing the connection",
				"user", logSafe(s.User()), "addr", s.RemoteAddr().String(), "reason", reason)
			closeConn(s)
		})
		// WithInput and WithOutput come after MakeOptions, so the guard
		// replaces the raw session input and the locked writer replaces the
		// raw session output. Alerts use the same locked writer, so they
		// never split a frame. The server process owns SIGINT; each program
		// must not react to it.
		opts := append(bm.MakeOptions(s),
			tea.WithInput(guard),
			tea.WithOutput(out),
			tea.WithAltScreen(),
			tea.WithReportFocus(),
			tea.WithoutSignalHandler(),
		)
		return tea.NewProgram(m, opts...)
	}
}

// sessionInput returns the reader that bm.MakeOptions gives to Bubble Tea:
// the PTY slave when the server allocates a real PTY, else the session.
func sessionInput(s ssh.Session) io.Reader {
	if pty, _, ok := s.Pty(); ok && !s.EmulatedPty() && pty.Slave != nil {
		return pty.Slave
	}
	return s
}

// sessionOutput returns the writer that bm.MakeOptions gives to Bubble Tea:
// the PTY slave when the server allocates a real PTY, else the session.
func sessionOutput(s ssh.Session) io.Writer {
	if pty, _, ok := s.Pty(); ok && !s.EmulatedPty() && pty.Slave != nil {
		return pty.Slave
	}
	return s
}

// closeConn closes the whole SSH connection of the session.
func closeConn(s ssh.Session) {
	if c, ok := s.Context().Value(ssh.ContextKeyConn).(gossh.Conn); ok {
		_ = c.Close()
		return
	}
	_ = s.Close()
}

// Keepalive probes. A client that answers no probe within
// defaultKeepAliveWait is gone (for example after a Wi-Fi drop without a
// TCP close, or a frozen client), so the server closes the connection. The
// room then marks the player away and folds or checks for the player at
// once. Without the probe, only the TCP stack of the host notices a dead
// client, and on macOS that takes many minutes.
const (
	defaultKeepAlive     = 15 * time.Second
	defaultKeepAliveWait = 30 * time.Second
)

// keepAliveRequest is the global request that OpenSSH uses for the same
// check (ServerAliveInterval and ClientAliveInterval). Each client answers
// it: OpenSSH and Go clients answer "failure" to a request that they do not
// know, and each answer shows that the client is alive.
const keepAliveRequest = "keepalive@openssh.com"

// keepAlive sends a keepalive probe every interval while the session runs.
// When no answer comes within wait, it closes the connection.
func keepAlive(every, wait time.Duration) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			if conn, ok := s.Context().Value(ssh.ContextKeyConn).(gossh.Conn); ok {
				stop := make(chan struct{})
				defer close(stop)
				go probeConn(conn, every, wait, stop, func() {
					log.Warn("keepalive: the client does not answer; closing the connection",
						"user", logSafe(s.User()), "addr", s.RemoteAddr().String(), "wait", wait)
					closeConn(s)
				})
			}
			next(s)
		}
	}
}

// probeConn sends a keepalive request every interval until stop closes or
// the connection fails. It calls dead when an answer does not come within
// wait.
func probeConn(conn gossh.Conn, every, wait time.Duration, stop <-chan struct{}, dead func()) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		answer := make(chan error, 1)
		go func() {
			// SendRequest returns when the answer comes or the
			// connection closes.
			_, _, err := conn.SendRequest(keepAliveRequest, true, nil)
			answer <- err
		}()
		timer := time.NewTimer(wait)
		select {
		case <-stop:
			timer.Stop()
			return
		case err := <-answer:
			timer.Stop()
			if err != nil {
				return // the connection is closed
			}
		case <-timer.C:
			dead()
			return
		}
	}
}

// endLoginGrace stops the login grace timer when a session starts. A quiet
// player or spectator then stays connected.
func endLoginGrace(next ssh.Handler) ssh.Handler {
	return func(s ssh.Session) {
		if t, ok := s.Context().Value(loginTimerKey{}).(*time.Timer); ok {
			t.Stop()
		}
		next(s)
	}
}

// logConnections logs each session start and end. The user name, the
// command, TERM and the client version come from the client, so logSafe
// escapes them before they reach the host terminal.
func logConnections(next ssh.Handler) ssh.Handler {
	return func(s ssh.Session) {
		start := time.Now()
		pty, _, _ := s.Pty()
		log.Info("connect",
			"user", logSafe(s.User()),
			"addr", s.RemoteAddr().String(),
			"login", loginKind(s.Permissions().Permissions),
			"command", logSafe(strings.Join(s.Command(), " ")),
			"term", logSafe(pty.Term),
			"size", fmt.Sprintf("%dx%d", pty.Window.Width, pty.Window.Height),
			"client", logSafe(s.Context().ClientVersion()),
		)
		next(s)
		log.Info("disconnect",
			"user", logSafe(s.User()),
			"addr", s.RemoteAddr().String(),
			"duration", time.Since(start).Round(time.Millisecond),
		)
	}
}

// maxLogField is the longest client text (in bytes, before escapes) that a
// log line shows.
const maxLogField = 100

// logSafe makes client text safe for the host terminal. It cuts s to
// maxLogField bytes and writes control characters, escapes and other
// characters that are not graphic as Go escapes (for example \x1b).
func logSafe(s string) string {
	cut := len(s) > maxLogField
	if cut {
		// A rune that the cut splits shows as \x escapes.
		s = s[:maxLogField]
	}
	q := strconv.QuoteToGraphic(s)
	q = q[1 : len(q)-1]
	if cut {
		q += "..."
	}
	return q
}

// joinError turns a Connect error into a friendly line for the terminal.
func joinError(err error, name string, port int) string {
	switch {
	case errors.Is(err, server.ErrNameInUse):
		return fmt.Sprintf("\r\n%s: the name %q is in use by another player right now.\r\n"+
			"Pick another name: ssh -p %d othername@<host>\r\n", appName, name, port)
	case errors.Is(err, server.ErrRoomClosed):
		return "\r\n" + textRoomClosed + "\r\n"
	case errors.Is(err, server.ErrNoAccount):
		return "\r\n" + appName + " cannot find your player. Connect again.\r\n"
	}
	return fmt.Sprintf("\r\n%s: cannot join the table: %v\r\n", appName, err)
}

// loginKind names the login method for the host log.
func loginKind(perms *gossh.Permissions) string {
	switch _, keyless, ok := identity(perms); {
	case !ok:
		return "none"
	case keyless:
		return "pin"
	}
	return "key"
}

// restoreAutoWrap turns auto-wrap on again when the program ends, so that
// the client's shell wraps lines as before.
func restoreAutoWrap(next ssh.Handler) ssh.Handler {
	return func(s ssh.Session) {
		next(s)
		if out, ok := s.Context().Value(outputKey{}).(*lockedWriter); ok {
			_, _ = io.WriteString(out, autoWrapOn)
		}
	}
}

// closeSession closes the room session when the SSH session ends, also
// when the Bubble Tea program stops early.
func closeSession(next ssh.Handler) ssh.Handler {
	return func(s ssh.Session) {
		defer func() {
			if sess, ok := s.Context().Value(sessionKey{}).(*server.Session); ok {
				sess.Close()
			}
		}()
		next(s)
	}
}
