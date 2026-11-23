package main

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// freezeProxy forwards one TCP connection to addr. After freeze, it stops:
// it reads nothing from the client and drops what the server sends, as a
// client behind a dead Wi-Fi link does. serverClosed closes when the server
// closes its side.
type freezeProxy struct {
	addr         string
	frozen       atomic.Bool
	serverClosed chan struct{}
	stop         chan struct{}
}

func startFreezeProxy(t *testing.T, target string) *freezeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &freezeProxy{addr: ln.Addr().String(), serverClosed: make(chan struct{}), stop: make(chan struct{})}
	var conns []net.Conn
	var mu sync.Mutex
	t.Cleanup(func() {
		close(p.stop)
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	go func() {
		down, err := ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", target)
		if err != nil {
			down.Close()
			return
		}
		mu.Lock()
		conns = append(conns, down, up)
		mu.Unlock()
		go func() { // client to server
			buf := make([]byte, 32<<10)
			for {
				n, err := down.Read(buf)
				if p.frozen.Load() {
					<-p.stop
					return
				}
				if n > 0 {
					_, _ = up.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		go func() { // server to client
			buf := make([]byte, 32<<10)
			for {
				n, err := up.Read(buf)
				if n > 0 && !p.frozen.Load() {
					_, _ = down.Write(buf[:n])
				}
				if err != nil {
					close(p.serverClosed)
					return
				}
			}
		}()
	}()
	return p
}

// Batch 10 audit (F2): a client that stops answering (a Wi-Fi drop without
// a TCP close) loses its connection after the keepalive wait. Before the
// fix, the server kept such a client online with its seat for minutes.
func TestKeepAliveClosesDeadClient(t *testing.T) {
	t.Parallel()
	addr := startTestServer(t, options{keepAlive: 200 * time.Millisecond, keepAliveWait: 600 * time.Millisecond})
	p := startFreezeProxy(t, addr)
	_, _ = startShell(t, keyAuthClient(t, p.addr, "ghost"))
	select {
	case <-p.serverClosed:
		t.Fatal("the server closed a live connection")
	default:
	}
	p.frozen.Store(true)
	select {
	case <-p.serverClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the server kept a dead client connected for 5 s")
	}
}

// A live client answers each probe, so a quiet session stays open over
// many probe periods.
func TestKeepAliveKeepsLiveClient(t *testing.T) {
	t.Parallel()
	addr := startTestServer(t, options{keepAlive: 100 * time.Millisecond, keepAliveWait: 300 * time.Millisecond})
	s, _ := startShell(t, keyAuthClient(t, addr, "quiet"))
	ended := make(chan error, 1)
	go func() { ended <- s.Wait() }()
	select {
	case err := <-ended:
		t.Fatalf("the server ended a live session: %v", err)
	case <-time.After(2 * time.Second):
	}
}
