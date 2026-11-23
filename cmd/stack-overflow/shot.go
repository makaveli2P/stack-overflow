package main

import (
	"context"
	"errors"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"

	"github.com/makaveli2p/stack-overflow/internal/shot"
)

// Screenshots (batch 13): P draws the player's screen as a PNG, and a
// small HTTP server on -shot-port serves it at a random link that
// expires. See DESIGN.md "Batch 13". Batch 14: each shot has two links,
// one for the player and one for the table chat. Batch 15: the link is
// short ("/<token>", 10 characters), so it fits the chat panel.

// defaultShotPort is the default -shot-port.
const defaultShotPort = 2280

// Limits of the screenshot HTTP server. Each request is a small GET; the
// largest answer is one PNG of about 2 MB.
const (
	shotReadHeaderTimeout = 5 * time.Second
	shotReadTimeout       = 10 * time.Second
	shotWriteTimeout      = 30 * time.Second
	shotIdleTimeout       = 60 * time.Second
	shotMaxHeaderBytes    = 8 << 10
	shotShutdownWait      = 2 * time.Second
)

// errShotTooLarge is the hook error when the store does not keep a PNG.
var errShotTooLarge = errors.New("the image is too large to keep")

// shotLinks makes the links of the P key: it draws the PNG, stores it and
// builds the URL from the address that the player's SSH client used.
type shotLinks struct {
	store *shot.Store
	port  int
	// lanHost is the address in the banner. A link uses it when the
	// local address of the connection is unknown or unspecified.
	lanHost string
	// office gives the office-network address for the link in the chat
	// (shot.Links.Table). It runs for each shot, because the Wi-Fi
	// address can change. nil, or "", means no such address: Table is
	// then Own.
	office func() string
	render func(shot.Frame) ([]byte, error) // shot.Render; tests replace it
	now    func() time.Time
}

func newShotLinks(store *shot.Store, port int, lanHost string) *shotLinks {
	return &shotLinks{store: store, port: port, lanHost: lanHost, render: shot.Render, now: time.Now}
}

// hook returns the tui.Options.Shot hook for one SSH session, or nil when
// screenshots are off.
func (l *shotLinks) hook(s ssh.Session) func(shot.Frame) (shot.Links, error) {
	if l == nil {
		return nil
	}
	local, user := s.LocalAddr(), s.User()
	return func(f shot.Frame) (shot.Links, error) {
		png, err := l.render(f)
		if err != nil {
			return shot.Links{}, err
		}
		path := l.store.Put(png, shotFileName(f.Title, user, l.now()))
		if path == "" {
			return shot.Links{}, errShotTooLarge
		}
		links := shot.Links{Own: "http://" + l.hostPort(local) + path}
		links.Table = links.Own
		if l.office != nil {
			if host := l.office(); host != "" {
				links.Table = "http://" + urlHostPort(host, l.port) + path
			}
		}
		return links, nil
	}
}

// hostPort is the host and port of a link for a player whose SSH client
// connected to local.
func (l *shotLinks) hostPort(local net.Addr) string {
	host := l.lanHost
	if ta, ok := local.(*net.TCPAddr); ok && ta.IP != nil && !ta.IP.IsUnspecified() {
		ip := ta.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4 // an IPv4 client of a dual-stack listener
		}
		host = ip.String()
		if ta.Zone != "" {
			host += "%" + ta.Zone
		}
	}
	return urlHostPort(host, l.port)
}

// urlHostPort joins host and port for a URL: an IPv6 address goes in
// brackets, and the "%" of a zone becomes "%25" (RFC 6874).
func urlHostPort(host string, port int) string {
	return net.JoinHostPort(strings.ReplaceAll(host, "%", "%25"), strconv.Itoa(port))
}

// shotFileName is the file name for a browser that saves the PNG:
// "stack-overflow-<name>-<yyyymmdd-hhmmss>.png". The name comes from the
// title ("Stack Overflow — <name> · hand #N"), else from the SSH user
// name. The store removes the unsafe characters.
func shotFileName(title, user string, now time.Time) string {
	name, ok := strings.CutPrefix(title, appName+" — ")
	if ok {
		name, _, _ = strings.Cut(name, " · hand #")
	}
	if !ok || strings.TrimSpace(name) == "" {
		name = user
	}
	return "stack-overflow-" + name + "-" + now.Format("20060102-150405") + ".png"
}

// startShotServer starts the screenshot HTTP server on -host and
// -shot-port. It returns nil, nil when -shot-port is 0, or when the port
// cannot be used: the game then runs without screenshots.
func startShotServer(o options, lanHost string) (*shotLinks, *http.Server) {
	if o.shotPort == 0 {
		return nil, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.shotPort)))
	if err != nil {
		log.Warn("cannot listen for screenshots; the game runs without them (key P)", "port", o.shotPort, "err", err)
		return nil, nil
	}
	store := shot.NewStore(shot.StoreConfig{})
	srv := &http.Server{
		Handler:           store.Handler(),
		ReadHeaderTimeout: shotReadHeaderTimeout,
		ReadTimeout:       shotReadTimeout,
		WriteTimeout:      shotWriteTimeout,
		IdleTimeout:       shotIdleTimeout,
		MaxHeaderBytes:    shotMaxHeaderBytes,
		// net/http logs nothing with a link in it, but a client can
		// make it log much (bad TLS hellos, for example). The server
		// keeps the host log quiet.
		ErrorLog: stdlog.New(io.Discard, "", 0),
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("the screenshot server stopped", "err", err)
		}
	}()
	l := newShotLinks(store, o.shotPort, lanHost)
	l.office = func() string { return officeHost(o.host) }
	return l, srv
}

// stopShotServer stops the screenshot server. Open downloads get a short
// time to end.
func stopShotServer(srv *http.Server) {
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shotShutdownWait)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
	}
}
