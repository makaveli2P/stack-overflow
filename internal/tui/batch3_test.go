package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// ---- formatting ----

func TestBatch3Formatting(t *testing.T) {
	for _, c := range []struct {
		n, d int
		want string
	}{{0, 0, "—"}, {5, 0, "—"}, {0, 9, "0%"}, {1, 8, "13%"}, {9, 41, "22%"}, {7, 7, "100%"}} {
		if got := pct(c.n, c.d); got != c.want {
			t.Errorf("pct(%d, %d) = %q, want %q", c.n, c.d, got, c.want)
		}
	}
	for _, c := range []struct {
		br, calls int
		want      string
	}{{0, 0, "—"}, {5, 0, "∞"}, {0, 4, "0.0"}, {88, 35, "2.5"}, {1, 3, "0.3"}, {10, 4, "2.5"}} {
		if got := aggression(c.br, c.calls); got != c.want {
			t.Errorf("aggression(%d, %d) = %q, want %q", c.br, c.calls, got, c.want)
		}
	}
	for _, c := range []struct {
		netBB float64
		hands int
		want  string
	}{{0, 0, "—"}, {12, 0, "—"}, {62, 182, "+34.1"}, {-15, 164, "-9.1"}, {0, 10, "0.0"}, {-0.001, 100, "0.0"}, {5, 200, "+2.5"}} {
		if got := bb100(c.netBB, c.hands); got != c.want {
			t.Errorf("bb100(%v, %d) = %q, want %q", c.netBB, c.hands, got, c.want)
		}
	}
	for _, c := range []struct {
		net, cpu, secs int64
		cur            string
		exact          bool
		want           string
		ok             bool
	}{
		{600, 2, 7200, "₹", false, "+₹150", true},     // ₹300 in 2 hours
		{-125, 2, 3600, "₹", false, "-₹63", true},     // -₹62.50 rounds away from zero
		{-125, 2, 3600, "₹", true, "-₹62.50", true},   // the card keeps the paise
		{3400, 2, 19200, "₹", true, "+₹318.75", true}, // 5h 20m
		{0, 2, 3600, "₹", false, "₹0", true},
		{500, 2, 599, "₹", false, "—", false},               // under 10 minutes
		{500, 2, 600, "₹", false, "+₹1,500", true},          // ₹250 in 10 minutes
		{1234567, 1, 3600, "₹", false, "+₹12,34,567", true}, // Indian grouping
		{300, 2, 1800, "", false, "+600", true},             // no currency: chips per hour
	} {
		got, _, ok := perHour(c.net, c.cpu, c.secs, c.cur, c.exact)
		if got != c.want || ok != c.ok {
			t.Errorf("perHour(%d, %d, %d, %q, %v) = %q, %v; want %q, %v", c.net, c.cpu, c.secs, c.cur, c.exact, got, ok, c.want, c.ok)
		}
	}
	for in, want := range map[string]string{"AKs": "Ace-King suited", "QJo": "Queen-Jack offsuit", "77": "Pocket Sevens",
		"T9s": "Ten-Nine suited", "AA": "Pocket Aces", "AK": "", "": "", "XYs": "", "AKx": ""} {
		if got := handCodeName(in); got != want {
			t.Errorf("handCodeName(%q) = %q, want %q", in, got, want)
		}
	}
	if equityPct(0.955) != "96%" || equityPct(0.7) != "70%" || equityPct(1) != "100%" {
		t.Error("equityPct")
	}
	if duration(9*60) != "9m" || duration(5*3600+20*60) != "5h 20m" {
		t.Error("duration")
	}
}

// ---- tabs ----

func TestDashboardTabCycle(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard host tab"))
	h.press("d")
	got := []int{h.m.dash.tab}
	for range 4 {
		h.press("tab")
		got = append(got, h.m.dash.tab)
	}
	if want := []int{dashStats, dashFun, dashSettle, dashHost, dashStats}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Tab order = %v, want %v", got, want)
	}
	h.press("left")
	if h.m.dash.tab != dashHost {
		t.Errorf("← from Stats must wrap to Host, got %d", h.m.dash.tab)
	}
	h.press("shift+tab")
	if h.m.dash.tab != dashSettle {
		t.Errorf("Shift+Tab from Host must go to Settle up, got %d", h.m.dash.tab)
	}
}

func TestHostTabOnlyForHost(t *testing.T) {
	// The rows have host stats, but the viewer is not the host (a server
	// bug must not leak them).
	f := fixture(t, "dashboard host tab")
	f.Snap.Me.Host = false
	f.Keys = nil
	h := newHarness(t, f)
	h.press("d")
	seen := map[int]bool{}
	for range 6 {
		seen[h.m.dash.tab] = true
		h.press("tab")
	}
	for range 6 {
		seen[h.m.dash.tab] = true
		h.press("left")
	}
	if seen[dashHost] {
		t.Error("a player who is not the host reached the Host tab")
	}
	if !seen[dashFun] || !seen[dashSettle] {
		t.Errorf("tabs seen: %v", seen)
	}
	for _, sz := range allSizes {
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		tabLine := strings.Split(v, "\n")[1]
		if strings.Contains(tabLine, "Host") {
			t.Errorf("the tab line shows Host to a player:\n%s", tabLine)
		}
	}
	h.press("?")
	if v := stripANSI(h.viewAt(100, 30)); strings.Contains(v, "host (") {
		t.Errorf("the help names the Host tab for a player\n%s", v)
	}

	// The host is on the Host tab when a snapshot says Me.Host = false:
	// the dashboard goes back to Stats and draws no host numbers.
	h = newHarness(t, fixture(t, "dashboard host card"))
	h.press(fixture(t, "dashboard host card").Keys...)
	if h.m.dash.tab != dashHost || !h.m.dash.detail {
		t.Fatal("setup: the host card must be open")
	}
	s := h.m.snap
	s.Me.Host = false
	h.snapshot(s)
	if h.m.dash.tab == dashHost || h.m.dash.detail {
		t.Errorf("the Host tab stayed open after Me.Host went false")
	}
	if v := stripANSI(h.viewAt(140, 40)); strings.Contains(v, "HOST STATS") || strings.Contains(v, "bb/100") {
		t.Errorf("host numbers on screen for a player\n%s", v)
	}
}

func TestHostTabForHost(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard host tab"))
	h.press("d")
	if tabLine := strings.Split(stripANSI(h.viewAt(100, 30)), "\n")[1]; !strings.Contains(tabLine, "Host") {
		t.Errorf("the host has no Host tab:\n%s", tabLine)
	}
	h.press("shift+tab")
	v := stripANSI(h.viewAt(140, 40))
	for _, want := range []string{"bb/100", "₹/hour", "VPIP/PFR", "3-Bet", "AF", "C-Bet", "Fold CB", "Fold 3B", "Steal",
		"Limp", "Chk-raise", "Big loss", "Streak", "AI win", "+34.1", "+₹319", "31/18", "∞"} {
		if !strings.Contains(v, want) {
			t.Errorf("host tab at 140 cells lacks %q\n%s", want, v)
		}
	}
	// Chen sat for 9 minutes: no hourly rate.
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "Chen") && strings.Contains(l, "₹") {
			t.Errorf("a player under 10 minutes seated has a rate: %q", l)
		}
	}
}

func TestHostColumnsDropByPriority(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard host tab"))
	names := func(w int) string {
		var out []string
		for _, c := range pickCols(h.m.hostCols(), w) {
			out = append(out, c.head)
		}
		return strings.Join(out, ",")
	}
	all := "Player,bb/100,₹/hour,VPIP/PFR,3-Bet,AF,C-Bet,Fold CB,Fold 3B,Steal,Limp,Chk-raise,Big loss,Streak,AI win"
	if got := names(200); got != all {
		t.Errorf("200 cells: %s", got)
	}
	for _, w := range []int{40, 56, 76, 96, 136} {
		if got := names(w); !strings.HasPrefix(all, got) {
			t.Errorf("%d cells: %s is not a priority prefix", w, got)
		}
	}
	if got := names(76); got != "Player,bb/100,₹/hour,VPIP/PFR,3-Bet,AF,C-Bet,Fold CB" {
		t.Errorf("76 cells (80-cell terminal): %s", got)
	}
}

// ---- fun tab ----

func TestFunTab(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard fun tab"))
	h.press("d", "tab")
	v := stripANSI(h.viewAt(80, 24))
	for _, want := range []string{"Bad beats", "Luckiest river", "First win", "Nemesis",
		"3 (96%)", "+12,340 on 10♥", "hand 14", "not yet", "Priya 2,310", "Your favourite hand  AJs Ace-Jack suited"} {
		if !strings.Contains(v, want) {
			t.Errorf("fun tab at 80x24 lacks %q\n%s", want, v)
		}
	}
	// Card of Leo: every fun number with its explanation.
	for h.m.snap.Stats[h.m.dash.row].Name != "Leo" {
		h.press("down")
	}
	h.press("enter")
	v = stripANSI(h.viewAt(80, 24))
	for _, want := range []string{"LEO · FUN", "12 · worst at 100%", "Q♠ Q♦", "70% or more",
		"+12,340 on the 10♥", "from behind on the turn", "hand 14", "first pot won", "Priya won 2,310 net chips from Leo"} {
		if !strings.Contains(v, want) {
			t.Errorf("Leo's fun card lacks %q\n%s", want, v)
		}
	}
}

func TestFavouriteHandOnlyFromOwnRow(t *testing.T) {
	f := fixture(t, "dashboard fun tab")
	f.Keys = nil
	// A server bug fills another row: the TUI must not show it.
	for i := range f.Snap.Stats {
		if f.Snap.Stats[i].Name == "Priya" {
			f.Snap.Stats[i].FavouriteHand, f.Snap.Stats[i].FavouriteCount = "72o", 99
		}
	}
	h := newHarness(t, f)
	h.press("d", "tab")
	for _, sz := range allSizes {
		v := stripANSI(h.viewAt(sz[0], sz[1]))
		if strings.Contains(v, "72o") || strings.Contains(v, "Seven-Two") {
			t.Errorf("another player's favourite hand is on screen at %v\n%s", sz, v)
		}
		if !strings.Contains(v, "AJs") {
			t.Errorf("the viewer's own favourite hand is missing at %v\n%s", sz, v)
		}
	}
	// Priya's card: no favourite hand line at all.
	for h.m.snap.Stats[h.m.dash.row].Name != "Priya" {
		h.press("up")
	}
	h.press("enter")
	if v := stripANSI(h.viewAt(100, 30)); strings.Contains(v, "Favourite") || strings.Contains(v, "72o") {
		t.Errorf("Priya's card shows a favourite hand\n%s", v)
	}
	// The viewer's own card shows it.
	h.press("esc")
	for h.m.snap.Stats[h.m.dash.row].Name != "Devon" {
		h.press("down")
	}
	h.press("enter")
	if v := stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "AJs Ace-Jack suited · 14×") || !strings.Contains(v, "only you see this") {
		t.Errorf("the viewer's card lacks the favourite hand\n%s", v)
	}

	// The viewer's own row has no favourite hand: the line says so and
	// does not take another row's value.
	s := h.m.snap
	s.Stats = append([]server.PlayerStats(nil), s.Stats...)
	for i := range s.Stats {
		if s.Stats[i].Name == "Devon" {
			s.Stats[i].FavouriteHand, s.Stats[i].FavouriteCount = "", 0
		}
	}
	h.snapshot(s)
	h.press("esc")
	v := stripANSI(h.viewAt(100, 30))
	if strings.Contains(v, "72o") || strings.Contains(v, "AJs") || !strings.Contains(v, "Your favourite hand  none yet") {
		t.Errorf("favourite hand line with an empty own row:\n%s", v)
	}
}

// ---- host card ----

func TestHostCardShowsCountsAndChances(t *testing.T) {
	h := newHarness(t, fixture(t, "dashboard host card"))
	h.press(fixture(t, "dashboard host card").Keys...)
	v := stripANSI(h.viewAt(80, 24))
	for _, want := range []string{"PRIYA · HOST STATS",
		"3-Bet", "22% 9/41", "re-raised a single raise preflop",
		"Fold to 3-Bet", "38% 3/8", "Steal", "47% 14/30", "Limp", "5% 6/120",
		"C-Bet", "75% 21/28", "Fold to C-Bet", "37% 7/19", "Check-raise", "18% 2/11",
		"All-in win", "71% 5/7", "bb/100", "+34.1", "+₹318.75", "5h 20m", "2.5 88/35",
		"-1,180", "now 1", "count/chances"} {
		if !strings.Contains(v, want) {
			t.Errorf("host card at 80x24 lacks %q\n%s", want, v)
		}
	}
	// Bartholomew: no 3-bet chance → "—" and 0/0; no calls → AF ∞.
	for h.m.snap.Stats[h.m.dash.row].Name != "Bartholomew" {
		h.press("down")
	}
	v = stripANSI(h.viewAt(100, 30))
	for _, want := range []string{"BARTHOLOMEW · HOST STATS", "— 0/0", "∞ 6/0", "+₹156"} {
		if !strings.Contains(v, want) {
			t.Errorf("Bartholomew's host card lacks %q\n%s", want, v)
		}
	}
	for h.m.snap.Stats[h.m.dash.row].Name != "Chen" {
		h.press("down")
	}
	if v = stripANSI(h.viewAt(100, 30)); !strings.Contains(v, "shows after 10 minutes seated") {
		t.Errorf("Chen (9 minutes seated) must have no rate\n%s", v)
	}
}

// Every batch 3 screen fits in every terminal size, on every row.
func TestBatch3StatesFit(t *testing.T) {
	down := strings.Split(strings.Repeat("down,", 8), ",")
	states := []struct {
		fixture string
		keys    []string
	}{
		{"dashboard fun tab", append([]string{"d", "tab"}, down...)},
		{"dashboard fun tab", append([]string{"d", "tab", "enter"}, down...)},
		{"dashboard fun tab", []string{"d", "tab", "enter", "up", "up", "up", "up", "up", "up", "up", "up"}},
		{"dashboard host tab", append([]string{"d", "shift+tab"}, down...)},
		{"dashboard host tab", append([]string{"d", "shift+tab", "enter"}, down...)},
		{"dashboard host tab", []string{"d", "shift+tab", "enter", "up", "up", "up", "up", "up", "up", "up", "up"}},
		{"dashboard host tab", []string{"d", "tab", "tab"}},
		{"help as the host", nil},
	}
	for _, s := range states {
		for _, sz := range append(allSizes, [2]int{60, 18}, [2]int{90, 26}) {
			f := fixture(t, s.fixture)
			if s.keys != nil {
				f.Keys = nil
			}
			h := newHarness(t, f)
			h.press(f.Keys...)
			for _, k := range s.keys {
				if k == "" {
					continue
				}
				h.press(k)
				wantFits(t, fmt.Sprintf("%s %v", s.fixture, s.keys), h.viewAt(sz[0], sz[1]), sz[0], sz[1])
			}
			wantFits(t, s.fixture, h.viewAt(sz[0], sz[1]), sz[0], sz[1])
		}
	}
}
