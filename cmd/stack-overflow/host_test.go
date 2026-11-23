package main

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/server"
)

// fakeAddr is a net.Addr that is not a *net.TCPAddr.
type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func mustIPNet(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return n
}

func TestIsLocalIP(t *testing.T) {
	locals := []net.Addr{
		mustIPNet(t, "127.0.0.1/8"),
		mustIPNet(t, "192.168.1.9/24"),
		mustIPNet(t, "fe80::1c2b:3cff:fe4d:5e6f/64"),
		mustIPNet(t, "2001:db8::42/64"),
		&net.IPAddr{IP: net.ParseIP("10.0.0.5")},
	}
	for in, want := range map[string]bool{
		"127.0.0.1":                 true,  // loopback
		"127.0.0.2":                 true,  // loopback range
		"::1":                       true,  // IPv6 loopback
		"::ffff:127.0.0.1":          true,  // IPv4-mapped loopback
		"192.168.1.9":               true,  // own LAN address
		"::ffff:192.168.1.9":        true,  // own LAN address, IPv4-mapped
		"10.0.0.5":                  true,  // *net.IPAddr entry
		"2001:db8::42":              true,  // own IPv6 address
		"fe80::1c2b:3cff:fe4d:5e6f": false, // link-local: the list has no zones, isLocalLinkLocal decides
		"192.168.1.10":              false, // a colleague in the same subnet
		"::ffff:192.168.1.10":       false,
		"2001:db8::43":              false,
		"8.8.8.8":                   false,
		"0.0.0.0":                   false,
		"::":                        false,
	} {
		ip := netip.MustParseAddr(in)
		if got := isLocalIP(ip, locals); got != want {
			t.Errorf("isLocalIP(%s) = %v, want %v", in, got, want)
		}
	}
	// A zone does not make a link-local address local: the zone-less list
	// cannot tell which link owns it.
	if isLocalIP(netip.MustParseAddr("fe80::1c2b:3cff:fe4d:5e6f%en0"), locals) {
		t.Error("isLocalIP matched a link-local address without its interface")
	}
	// A zone on an address that is not link-local does not change the result.
	if !isLocalIP(netip.MustParseAddr("2001:db8::42%en0"), locals) {
		t.Error("a zoned global address of this machine is not local")
	}
	// No interface list: only loopback is local.
	if isLocalIP(netip.MustParseAddr("192.168.1.9"), nil) || !isLocalIP(netip.MustParseAddr("127.0.0.1"), nil) {
		t.Error("empty interface list")
	}
}

func TestIsHostConn(t *testing.T) {
	old, oldZone := interfaceAddrs, lookupZone
	t.Cleanup(func() { interfaceAddrs, lookupZone = old, oldZone })
	lookupZone = func(zone string) (zoneInterface, error) {
		switch zone {
		case "lo0":
			return zoneInterface{loopback: true, addrs: []net.Addr{mustIPNet(t, "fe80::1/64")}}, nil
		case "en0":
			return zoneInterface{addrs: []net.Addr{mustIPNet(t, "fe80::aa/64")}}, nil
		}
		return zoneInterface{}, errors.New("no such interface")
	}
	calls := 0
	locals := []net.Addr{mustIPNet(t, "192.168.1.9/24")}
	interfaceAddrs = func() ([]net.Addr, error) {
		calls++
		return locals, nil
	}

	tcp := func(s string) net.Addr {
		a, err := net.ResolveTCPAddr("tcp", s)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	cases := []struct {
		addr net.Addr
		want bool
	}{
		{tcp("127.0.0.1:5000"), true},
		{tcp("[::1]:22"), true},
		{tcp("192.168.1.9:4000"), true},
		{tcp("[::ffff:192.168.1.9]:4000"), true},
		{&net.TCPAddr{IP: net.ParseIP("::ffff:192.168.1.9"), Port: 1}, true}, // 16-byte form
		{tcp("192.168.1.10:4000"), false},
		{fakeAddr("192.168.1.9:4000"), true},
		{fakeAddr("[::ffff:192.168.1.9]:4000"), true},
		{fakeAddr("[fe80::1%en0]:4000"), false}, // lo0's address on link en0
		{fakeAddr("[fe80::1%lo0]:4000"), true},  // lo0's address on lo0
		{fakeAddr("[fe80::aa%en0]:4000"), true}, // en0's own address on en0
		{&net.TCPAddr{IP: net.ParseIP("fe80::aa"), Zone: "en0"}, true},
		{&net.TCPAddr{IP: net.ParseIP("fe80::1"), Zone: "en0"}, false},
		{&net.TCPAddr{IP: net.ParseIP("fe80::aa")}, false},              // no zone: the link is not known
		{&net.TCPAddr{IP: net.ParseIP("fe80::aa"), Zone: "en9"}, false}, // unknown interface
		{fakeAddr("garbage"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isHostConn(c.addr); got != c.want {
			t.Errorf("isHostConn(%v) = %v, want %v", c.addr, got, c.want)
		}
	}

	// The interfaces are read again at each connect: a new address of the
	// machine counts at once.
	before := calls
	locals = append(locals, mustIPNet(t, "10.1.2.3/16"))
	if !isHostConn(tcp("10.1.2.3:9")) || calls != before+1 {
		t.Errorf("new interface address: host=%v, reads=%d", isHostConn(tcp("10.1.2.3:9")), calls-before)
	}

	// The interface list fails: loopback is still the host, others are not.
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("no interfaces") }
	if !isHostConn(tcp("127.0.0.1:1")) || isHostConn(tcp("192.168.1.9:1")) {
		t.Error("interface error")
	}
}

// TestIsHostConnRealInterfaces uses the interfaces of the test machine: a
// connection from one of its own addresses is the host. A link-local
// address has the zone of its own interface, as the kernel gives it.
func TestIsHostConnRealInterfaces(t *testing.T) {
	ifs, err := net.Interfaces()
	if err != nil {
		t.Skip("no interfaces:", err)
	}
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLoopback() {
				continue
			}
			checkOwnAddr(t, n.IP, ifc.Name)
		}
	}
}

// checkOwnAddr fails when a connection from ip, an address of the
// interface iface, is not the host.
func checkOwnAddr(t *testing.T, ip net.IP, iface string) {
	t.Helper()
	zone := ""
	if ip.To4() == nil && ip.IsLinkLocalUnicast() {
		zone = iface
	}
	if !isHostConn(&net.TCPAddr{IP: ip, Port: 1234, Zone: zone}) {
		t.Errorf("own address %s%%%s is not the host", ip, zone)
	}
	if v4 := ip.To4(); v4 != nil {
		mapped := net.IPv4(v4[0], v4[1], v4[2], v4[3]) // the 16-byte ::ffff: form
		if !isHostConn(&net.TCPAddr{IP: mapped, Port: 1234}) {
			t.Errorf("own address %s in IPv4-mapped form is not the host", ip)
		}
	}
}

func TestWriteStatsFunAndHost(t *testing.T) {
	ace := cards.Card{Rank: cards.Ace, Suit: cards.Spades}
	aceH := cards.Card{Rank: cards.Ace, Suit: cards.Hearts}
	stats := []server.PlayerStats{
		{
			Name: "Alice", Hands: 200, VPIP: 60, PFR: 30, LifetimeNet: 1200, PeriodNet: 700,
			BadBeats: 2, WorstBeat: 0.853, WorstBeatWith: []cards.Card{ace, aceH},
			LuckiestRiver: 1240, RiverCard: cards.Card{Rank: cards.Ten, Suit: cards.Clubs},
			FirstWinAfter: 7, Nemesis: "Bob", NemesisChips: 340,
			FavouriteHand: "AKs", FavouriteCount: 9,
			Host: &server.HostStats{
				ThreeBet: 3, ThreeBetChances: 12, FoldTo3Bet: 1, FoldTo3BetChances: 4,
				Steal: 5, StealChances: 10, Limp: 2, LimpChances: 40,
				CBet: 6, CBetChances: 8, FoldToCBet: 0, FoldToCBetChances: 0,
				CheckRaise: 1, CheckRaiseChances: 5,
				PostflopBetsRaises: 7, PostflopCalls: 4,
				Hands: 200, NetChips: 1200, NetBB: 25.5, SeatedSeconds: 2 * 3600, BiggestLoss: 1500,
				LongestWinStreak: 4, AllInShowdowns: 3, AllInShowdownsWon: 2,
			},
		},
		{
			Name: "Bob", Hands: 50, LifetimeNet: -1200, PeriodNet: -700,
			Host: &server.HostStats{PostflopBetsRaises: 3, Hands: 50, NetChips: -1200, NetBB: -10, SeatedSeconds: 300},
		},
		{Name: "Carol", Hands: 0}, // a row without host stats
	}
	settle := server.Settlement{Period: 1, ChipsPerUnit: 2, Currency: "₹"}
	var b bytes.Buffer
	writeStats(&b, "/x/state.json", stats, settle)
	out := b.String()

	fun := section(t, out, "Fun (all players see these)\n")
	for _, want := range []string{
		"Bad beats", "Worst beat", "Luckiest river", "First win", "Nemesis",
		"85% with A♠ A♥", "1,240 on 10♣", "hand 7", "Bob took 340",
	} {
		if !strings.Contains(fun, want) {
			t.Errorf("Fun table has no %q:\n%s", want, fun)
		}
	}
	host := section(t, out, "Host (only the host sees these)\n")
	for _, want := range []string{
		"bb/100", "₹/hour", "VPIP/PFR", "3-Bet", "AF", "C-Bet", "Fold to C-Bet",
		"Fold to 3-Bet", "Steal", "Limp", "Check-raise", "Biggest loss", "Win streak", "All-in win",
		"+12.8", // 100 × 25.5 / 200
		"+₹300", // 1,200 chips = ₹600 in 2 hours
		"30/15", // VPIP/PFR
		"25%",   // 3-Bet 3/12
		"1.8",   // AF 7/4
		"75%",   // C-Bet 6/8
		"1,500", // biggest loss
		"67%",   // all-in win 2/3
		"-20.0", // Bob bb/100
		"∞",     // Bob AF 3/0
	} {
		if !strings.Contains(host, want) {
			t.Errorf("Host table has no %q:\n%s", want, host)
		}
	}
	// Bob sat less than 10 minutes: no rate.
	if line := rowOf(host, "Bob"); !strings.Contains(line, "—") || strings.Contains(line, "/hour") {
		t.Errorf("Bob row: %q", line)
	}
	// The favourite hand is private to each player: the host command does
	// not print it.
	if strings.Contains(out, "AKs") || strings.Contains(strings.ToLower(out), "favourite") {
		t.Errorf("output shows a favourite hand:\n%s", out)
	}
	// The Fun table comes before the Host table, and both come before the
	// settlement.
	iFun, iHost, iSettle := strings.Index(out, "Fun (all"), strings.Index(out, "Host (only"), strings.Index(out, "Settle up")
	if !(iFun > 0 && iFun < iHost && iHost < iSettle) {
		t.Errorf("section order: fun %d host %d settle %d", iFun, iHost, iSettle)
	}
	// Columns line up in the Host table: each row has the same width up to
	// the right-aligned "All-in win" column.
	head := rowOf(host, "Player")
	end := len([]rune(head[:strings.Index(head, "All-in win")+len("All-in win")]))
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if r := []rune(rowOf(host, name)); len(r) != end {
			t.Errorf("Host row %s is %d runes, header ends at %d:\n%s", name, len(r), end, host)
		}
	}
}

// section returns the text from the line start up to the next blank line
// after the table.
func section(t *testing.T, out, start string) string {
	t.Helper()
	i := strings.Index(out, start)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", start, out)
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// rowOf returns the table line that starts with "  "+name.
func rowOf(table, name string) string {
	for _, l := range strings.Split(table, "\n") {
		if strings.HasPrefix(l, "  "+name) {
			return l
		}
	}
	return ""
}

func TestStatFormatters(t *testing.T) {
	if aggression(0, 0) != "—" || aggression(3, 0) != "∞" || aggression(3, 2) != "1.5" || aggression(0, 2) != "0.0" {
		t.Error("aggression")
	}
	if bbPer100(5, 0) != "—" || bbPer100(0, 10) != "0.0" || bbPer100(-0.0001, 100) != "0.0" || bbPer100(1.5, 100) != "+1.5" {
		t.Error("bbPer100")
	}
	if moneyPerHour(1000, minRateSeconds-1, "₹", 2) != "—" || moneyPerHour(-1000, 3600, "₹", 2) != "-₹500" {
		t.Error("moneyPerHour")
	}
	if percentPair(1, 1, 0) != "—" || percentPair(1, 2, 4) != "25/50" {
		t.Error("percentPair")
	}
	if cardText(cards.Card{}) != "" || cardText(cards.Card{Rank: cards.Ten, Suit: cards.Hearts}) != "10♥" {
		t.Error("cardText")
	}
	none := server.PlayerStats{}
	if worstBeat(none) != "—" || luckiestRiver(none) != "—" || firstWin(none) != "—" || nemesis(none) != "—" {
		t.Error("empty fun stats")
	}
}
