package main

import (
	"net"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
)

// Connection limits. A real office game has a few dozen players at most.
const (
	defaultMaxConns    = 64 // SSH connections in total
	defaultMaxConnsPer = 8  // SSH connections from one remote IP address
)

// Short lines for a connection over a limit. The server writes them before
// the SSH handshake and then closes the connection.
const (
	textServerFull = appName + ": the server has too many connections. Try again later.\r\n"
	textIPFull     = appName + ": too many connections from your address. Close one and try again.\r\n"
)

// connLimiter counts the open connections in total and for each remote IP.
type connLimiter struct {
	mu       sync.Mutex
	total    int
	perIP    map[string]int
	maxTotal int
	maxPerIP int
}

func newConnLimiter(maxTotal, maxPerIP int) *connLimiter {
	if maxTotal <= 0 {
		maxTotal = defaultMaxConns
	}
	if maxPerIP <= 0 {
		maxPerIP = defaultMaxConnsPer
	}
	return &connLimiter{perIP: map[string]int{}, maxTotal: maxTotal, maxPerIP: maxPerIP}
}

// acquire counts a new connection from ip. It returns "" when the
// connection can stay, else the text for the client.
func (l *connLimiter) acquire(ip string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.total >= l.maxTotal:
		return textServerFull
	case l.perIP[ip] >= l.maxPerIP:
		return textIPFull
	}
	l.total++
	l.perIP[ip]++
	return ""
}

func (l *connLimiter) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if l.perIP[ip]--; l.perIP[ip] <= 0 {
		delete(l.perIP, ip)
	}
}

// countedConn releases its place in the limiter when it closes.
type countedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *countedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// remoteIP returns the IP address part of addr.
func remoteIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// isLoopback reports whether addr is a loopback address (a connection from
// the host machine).
func isLoopback(addr net.Addr) bool {
	ip := net.ParseIP(remoteIP(addr))
	return ip != nil && ip.IsLoopback()
}

// withConnGuards installs the connection limits and the login grace. A
// connection over a limit gets a short line and closes at once. A
// connection that does not start an SSH session within grace after the TCP
// connect closes; without that, a client that connects and sends nothing
// keeps a socket and goroutines on the host for ever, also in -password
// mode. endLoginGrace stops the timer.
func withConnGuards(l *connLimiter, grace time.Duration) ssh.Option {
	return func(srv *ssh.Server) error {
		srv.ConnCallback = func(ctx ssh.Context, c net.Conn) net.Conn {
			ip := remoteIP(c.RemoteAddr())
			if text := l.acquire(ip); text != "" {
				log.Warn("connection limit: refusing a connection", "addr", c.RemoteAddr().String())
				_ = c.SetWriteDeadline(time.Now().Add(time.Second))
				_, _ = c.Write([]byte(text))
				return nil // the SSH server closes c
			}
			cc := &countedConn{Conn: c, release: func() { l.release(ip) }}
			ctx.SetValue(authStateKey{}, &authState{hangup: func() { _ = cc.Close() }})
			ctx.SetValue(loginTimerKey{}, time.AfterFunc(grace, func() { _ = cc.Close() }))
			return cc
		}
		return nil
	}
}
