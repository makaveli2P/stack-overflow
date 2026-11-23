package main

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/charmbracelet/log"
)

// interfaceAddrs lists the addresses of this machine. Tests replace it.
var interfaceAddrs = net.InterfaceAddrs

// zoneInterface is the network interface that the zone of an IPv6
// link-local address names.
type zoneInterface struct {
	loopback bool       // the interface is a loopback interface (lo0)
	addrs    []net.Addr // the addresses of the interface
}

// lookupZone finds the interface that a zone names. A zone is an interface
// name ("en0") or an interface index ("4"). Tests replace it.
var lookupZone = func(zone string) (zoneInterface, error) {
	ifc, err := net.InterfaceByName(zone)
	if err != nil {
		idx, aerr := strconv.Atoi(zone)
		if aerr != nil || idx <= 0 {
			return zoneInterface{}, err
		}
		if ifc, err = net.InterfaceByIndex(idx); err != nil {
			return zoneInterface{}, err
		}
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return zoneInterface{}, err
	}
	return zoneInterface{loopback: ifc.Flags&net.FlagLoopback != 0, addrs: addrs}, nil
}

// isHostConn reports whether addr is a connection from the server machine
// itself: a loopback address, or an address of one of the machine's own
// network interfaces. It reads the interfaces again at each call, because
// a laptop can change its network while the server runs.
//
// An IPv6 link-local address is unique only on one link (RFC 4007). Every
// Mac has fe80::1 on lo0, and a colleague can put fe80::1 on the office
// Wi-Fi. So a link-local address is the host only when the interface that
// its zone names (the link that the packets came on) has that address.
func isHostConn(addr net.Addr) bool {
	ip, ok := parseRemoteIP(addr)
	if !ok {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if isLinkLocal6(ip) {
		zone := ip.Zone()
		if zone == "" {
			return false
		}
		ifc, err := lookupZone(zone)
		if err != nil {
			log.Warn("cannot read the network interface of a link-local connection; it is not the host", "zone", zone, "err", err)
			return false
		}
		return isLocalLinkLocal(ip, ifc)
	}
	locals, err := interfaceAddrs()
	if err != nil {
		log.Warn("cannot read the network interfaces; only loopback connections are the host", "err", err)
		return false
	}
	return isLocalIP(ip, locals)
}

// parseRemoteIP returns the IP address of addr. An IPv4-mapped IPv6
// address (::ffff:192.168.1.9) becomes the IPv4 address. An IPv6 address
// keeps its zone, because the zone tells the link of a link-local address.
func parseRemoteIP(addr net.Addr) (netip.Addr, bool) {
	if addr == nil {
		return netip.Addr{}, false
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		ip, ok := ipFromSlice(tcp.IP)
		if ok && ip.Is6() && tcp.Zone != "" {
			ip = ip.WithZone(tcp.Zone)
		}
		return ip, ok
	}
	ip, err := netip.ParseAddr(remoteIP(addr))
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// ipFromSlice converts a net.IP to a netip.Addr without a zone and with an
// IPv4-mapped address changed to IPv4.
func ipFromSlice(b net.IP) (netip.Addr, bool) {
	ip, ok := netip.AddrFromSlice(b)
	if !ok {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// isLinkLocal6 reports whether ip is an IPv6 link-local unicast address
// (fe80::/10).
func isLinkLocal6(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.Is6() && ip.IsLinkLocalUnicast()
}

// isLocalIP reports whether ip is loopback or equal to one of the
// interface addresses in locals (as net.InterfaceAddrs returns them). It
// ignores the zone. It never matches an IPv6 link-local address, because
// the list has no zones: isLocalLinkLocal decides for those.
func isLocalIP(ip netip.Addr, locals []net.Addr) bool {
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() || ip.IsUnspecified() {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if isLinkLocal6(ip) {
		return false
	}
	return hasAddr(locals, ip)
}

// isLocalLinkLocal reports whether the IPv6 link-local address ip came
// from this machine. ifc is the interface that the zone of ip names. A
// packet on a loopback interface comes only from this machine. On another
// interface, the address must be an address of that same interface.
func isLocalLinkLocal(ip netip.Addr, ifc zoneInterface) bool {
	if !isLinkLocal6(ip) || ip.Zone() == "" {
		return false
	}
	if ifc.loopback {
		return true
	}
	return hasAddr(ifc.addrs, ip.Unmap().WithZone(""))
}

// hasAddr reports whether ip (with no zone) is one of the addresses in
// list.
func hasAddr(list []net.Addr, ip netip.Addr) bool {
	for _, a := range list {
		var raw net.IP
		switch v := a.(type) {
		case *net.IPNet:
			raw = v.IP
		case *net.IPAddr:
			raw = v.IP
		default:
			p, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			if p.Addr().Unmap().WithZone("") == ip {
				return true
			}
			continue
		}
		if local, ok := ipFromSlice(raw); ok && local == ip {
			return true
		}
	}
	return false
}
