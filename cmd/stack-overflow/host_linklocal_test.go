package main

import (
	"net"
	"testing"
	"time"
)

// A LAN peer that puts a link-local address of another interface of the
// server machine (fe80::1 of lo0, or the address of a utun) on its own
// Wi-Fi arrives with the zone of the Wi-Fi interface. It is not the host.
func TestIsHostConnLinkLocalOfOtherInterface(t *testing.T) {
	old, oldZone := interfaceAddrs, lookupZone
	t.Cleanup(func() { interfaceAddrs, lookupZone = old, oldZone })
	byIface := map[string]zoneInterface{
		"lo0":   {loopback: true, addrs: []net.Addr{mustIPNet(t, "127.0.0.1/8"), mustIPNet(t, "::1/128"), mustIPNet(t, "fe80::1/64")}},
		"en0":   {addrs: []net.Addr{mustIPNet(t, "192.168.1.9/24"), mustIPNet(t, "fe80::a1b2:c3d4:e5f6:1234/64")}},
		"utun0": {addrs: []net.Addr{mustIPNet(t, "fe80::abcd:ef01:2345:678/64")}},
	}
	var all []net.Addr
	for _, ifc := range byIface {
		all = append(all, ifc.addrs...)
	}
	interfaceAddrs = func() ([]net.Addr, error) { return all, nil }
	lookupZone = func(zone string) (zoneInterface, error) {
		ifc, ok := byIface[zone]
		if !ok {
			return zoneInterface{}, &net.AddrError{Err: "no such interface", Addr: zone}
		}
		return ifc, nil
	}
	for _, c := range []struct {
		remote *net.TCPAddr
		want   bool
	}{
		{&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 50000, Zone: "en0"}, false},                  // lo0's address on en0
		{&net.TCPAddr{IP: net.ParseIP("fe80::abcd:ef01:2345:678"), Port: 50000, Zone: "en0"}, false}, // utun0's address on en0
		{&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 50000, Zone: "lo0"}, true},
		{&net.TCPAddr{IP: net.ParseIP("fe80::a1b2:c3d4:e5f6:1234"), Port: 50000, Zone: "en0"}, true},
		{&net.TCPAddr{IP: net.ParseIP("fe80::a1b2:c3d4:e5f6:1234"), Port: 50000, Zone: "lo0"}, true}, // own traffic on lo0
		{&net.TCPAddr{IP: net.ParseIP("fe80::a1b2:c3d4:e5f6:1234"), Port: 50000}, false},             // no zone
		{&net.TCPAddr{IP: net.ParseIP("192.168.1.9"), Port: 50000}, true},
	} {
		if got := isHostConn(c.remote); got != c.want {
			t.Errorf("isHostConn(%v) = %v, want %v", c.remote, got, c.want)
		}
	}
}

// The same check with the real interfaces of the test machine: the
// link-local address of one interface, on the link of another interface
// that is not loopback, is not the host.
func TestIsHostConnLinkLocalOfOtherInterfaceReal(t *testing.T) {
	ifs, err := net.Interfaces()
	if err != nil {
		t.Skip(err)
	}
	type ll struct {
		ip       net.IP
		iface    string
		loopback bool
	}
	var lls []ll
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() == nil && n.IP.IsLinkLocalUnicast() {
				lls = append(lls, ll{n.IP, ifc.Name, ifc.Flags&net.FlagLoopback != 0})
			}
		}
	}
	if len(lls) < 2 {
		t.Skip("fewer than two IPv6 link-local interfaces")
	}
	// A "tcp" listener on 0.0.0.0 is dual stack: it accepts link-local
	// clients with a zone.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan net.Addr, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			got <- c.RemoteAddr()
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	dst := &net.TCPAddr{IP: lls[0].ip, Port: port, Zone: lls[0].iface}
	if c, err := net.DialTimeout("tcp", dst.String(), 2*time.Second); err == nil {
		c.Close()
		select {
		case a := <-got:
			t.Logf("0.0.0.0 listener accepted IPv6 link-local client %#v", a)
			if !isHostConn(a) {
				t.Errorf("isHostConn(%v) = false for a connection from this machine", a)
			}
		case <-time.After(2 * time.Second):
		}
	} else {
		t.Logf("dial %s: %v", dst, err)
	}
	for _, a := range lls {
		for _, b := range lls {
			if a.iface == b.iface || b.loopback {
				continue
			}
			if b.ip.Equal(a.ip) {
				continue // the same address on both interfaces
			}
			remote := &net.TCPAddr{IP: a.ip, Port: 50000, Zone: b.iface}
			owned := false
			for _, c := range lls {
				if c.iface == b.iface && c.ip.Equal(a.ip) {
					owned = true
				}
			}
			if !owned && isHostConn(remote) {
				t.Errorf("isHostConn(%v) = true: the address belongs to %s, not to link %s", remote, a.iface, b.iface)
				return
			}
		}
	}
}
