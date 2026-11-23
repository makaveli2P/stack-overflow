package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// lanIP returns an IPv4 address of this machine that is not loopback, or
// "" when the machine has none.
func lanIP(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
}

// openDashboard logs in to addr as user, saves the name, opens the stats
// screen and pushes Tab three times. It returns all the screen output.
func openDashboard(t *testing.T, addr, user string) string {
	t.Helper()
	c := keyAuthClient(t, addr, user)
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var out syncBuffer
	s.Stdout = &out
	in, _ := s.StdinPipe()
	if err := s.RequestPty("xterm", 40, 160, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the colour query of wish times out
	_, _ = in.Write([]byte("x"))        // its leaked reader eats one key
	time.Sleep(300 * time.Millisecond)
	for _, k := range []string{"\r", "d", "\t", "\t", "\t"} {
		_, _ = in.Write([]byte(k))
		time.Sleep(300 * time.Millisecond)
	}
	return out.String()
}

// A client from the server machine is the host and sees the Host tab. A
// client from another address does not. The test cannot dial from another
// machine, so it hides the interface list for the second client: its LAN
// address is then not an address of the server machine.
func TestHostTabOverSSH(t *testing.T) {
	ip := lanIP(t)
	if ip == "" {
		t.Skip("no LAN address on this machine")
	}
	var hide atomic.Bool
	real := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		if hide.Load() {
			return nil, nil
		}
		return real()
	}

	room, err := server.NewRoom(server.Config{Seats: 4, SmallBlind: 5, BigBlind: 10, StartingStack: 1000, NextHandDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ip, err)
	}
	o := options{host: ip, port: ln.Addr().(*net.TCPAddr).Port}
	srv, err := newSSHServer(o, filepath.Join(t.TempDir(), "hk"), room)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		cancel()
		interfaceAddrs = real
	})
	addr := ln.Addr().String()

	host := openDashboard(t, addr, "hosty")
	if !strings.Contains(host, "bb/100") {
		t.Errorf("the host from %s does not see the Host tab", ip)
	}
	hide.Store(true)
	guest := openDashboard(t, addr, "guest")
	if !strings.Contains(guest, "Settle up") {
		t.Fatalf("the guest dashboard did not open: %q", guest)
	}
	if strings.Contains(guest, "bb/100") || strings.Contains(guest, "Host") {
		t.Errorf("a guest sees the Host tab")
	}
}
