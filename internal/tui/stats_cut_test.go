package tui

import (
	"strings"
	"testing"
)

// Regression tests: the Fun and Host tabs and their detail cards show each
// number and each explanation in full, at every terminal size.

// selectRow moves the dashboard cursor to the row of the named player.
func selectRow(t *testing.T, h *harness, name string) {
	t.Helper()
	for i := 0; i < 20 && h.m.snap.Stats[h.m.dash.row].Name != name; i++ {
		h.press("up")
	}
	for i := 0; i < 20 && h.m.snap.Stats[h.m.dash.row].Name != name; i++ {
		h.press("down")
	}
	if h.m.snap.Stats[h.m.dash.row].Name != name {
		t.Fatalf("no row for %s", name)
	}
}

// lineWith returns the first line of v that contains s, or "".
func lineWith(v, s string) string {
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// A rate of ₹1,000.00/hour or more has 10 cells or more. The host card
// must show all of it, and every explanation must stay complete.
func TestHostCardShowsWideHourlyRate(t *testing.T) {
	for _, c := range []struct {
		net  int64
		secs int64
		want string
	}{
		{1868, 606, "+₹5,548.51"},
		{-1868, 606, "-₹5,548.51"},
		{20000, 606, "+₹59,405.94"},
		{-40000, 606, "-₹1,18,811.88"},
	} {
		s := hostSnap()
		for i := range s.Stats {
			if s.Stats[i].Name == "Priya" {
				s.Stats[i].Host.NetChips = c.net
				s.Stats[i].Host.SeatedSeconds = c.secs
			}
		}
		h := newHarness(t, Fixture{Name: "rate", Snap: s})
		h.press("d", "shift+tab")
		selectRow(t, h, "Priya")
		h.press("enter")
		for _, sz := range allSizes {
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			wantFits(t, "host card "+c.want, h.viewAt(sz[0], sz[1]), sz[0], sz[1])
			l := lineWith(v, "₹/hour")
			if l == "" || strings.Contains(l, "…") || !strings.Contains(l, c.want) {
				t.Errorf("%dx%d: the host card cuts the hourly rate %s: %q", sz[0], sz[1], c.want, strings.TrimSpace(l))
			}
			// A wide value may push an explanation on to a second line,
			// but no line of the card gets cut.
			for _, l := range strings.Split(v, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "│") && strings.Contains(l, "…") {
					t.Errorf("%dx%d: rate %s: the host card cuts a line: %q", sz[0], sz[1], c.want, strings.TrimSpace(l))
				}
			}
			if !strings.Contains(v, "net per hour seated") || !strings.Contains(v, "count/chances") {
				t.Errorf("%dx%d: rate %s: the host card lost a line\n%s", sz[0], sz[1], c.want, v)
			}
		}
	}
}

// A 12-rune name (the maximum) must not cut the "no nemesis" explanation.
func TestFunCardShowsLongNameInNemesisText(t *testing.T) {
	for _, name := range []string{"Maximilian.X", "Maximilian"} {
		s := funSnap()
		for i := range s.Stats {
			if s.Stats[i].Name == "Kim" { // Kim has no nemesis
				s.Stats[i].Name = name
			}
		}
		h := newHarness(t, Fixture{Name: "nemesis", Snap: s})
		h.press("d", "tab")
		selectRow(t, h, name)
		h.press("enter")
		for _, sz := range allSizes {
			v := stripANSI(h.viewAt(sz[0], sz[1]))
			wantFits(t, "fun card "+name, h.viewAt(sz[0], sz[1]), sz[0], sz[1])
			if !strings.Contains(v, "net chips from "+name) || strings.Contains(lineWith(v, "net chips from"), "…") {
				t.Errorf("%dx%d: the nemesis explanation is cut: %q", sz[0], sz[1],
					strings.TrimSpace(lineWith(v, "net chips from")))
			}
		}
	}
}

// bb/100 after 1 hand can be 8 cells or more ("-10000.0"). The Host tab
// must show the full value.
func TestHostTabShowsWideBb100(t *testing.T) {
	for _, c := range []struct {
		hands int
		netBB float64
		want  string
	}{
		{1, -100, "-10000.0"},
		{1, 200, "+20000.0"},
		{1, 1800, "+180000.0"},
		{2, -100, "-5000.0"},
	} {
		s := hostSnap()
		for i := range s.Stats {
			if s.Stats[i].Name == "Kim" {
				s.Stats[i].Hands = c.hands
				s.Stats[i].Host.Hands = c.hands
				s.Stats[i].Host.NetBB = c.netBB
			}
		}
		h := newHarness(t, Fixture{Name: "bb100", Snap: s})
		h.press("d", "shift+tab")
		for _, sz := range allSizes {
			l := lineWith(stripANSI(h.viewAt(sz[0], sz[1])), "Kim")
			if !strings.Contains(l, c.want) || strings.Contains(l, c.want[:3]+"…") {
				t.Errorf("%dx%d: the bb/100 cell is cut, want %s: %q", sz[0], sz[1], c.want, strings.TrimSpace(l))
			}
		}
	}
}

// The ₹/hour column of the Host tab must show a five-digit whole rate.
func TestHostTabShowsWideHourlyRate(t *testing.T) {
	s := hostSnap()
	for i := range s.Stats {
		if s.Stats[i].Name == "Kim" {
			s.Stats[i].Host.NetChips = 200000
			s.Stats[i].Host.SeatedSeconds = 3600
		}
	}
	h := newHarness(t, Fixture{Name: "rate col", Snap: s})
	h.press("d", "shift+tab")
	for _, sz := range allSizes {
		l := lineWith(stripANSI(h.viewAt(sz[0], sz[1])), "Kim")
		if !strings.Contains(l, "+₹1,00,000") {
			t.Errorf("%dx%d: the ₹/hour cell is cut: %q", sz[0], sz[1], strings.TrimSpace(l))
		}
	}
}

// A veteran with 500 hands before batch 3 and 10 hands after it. bb/100
// and the hourly rate count only the batch 3 hands (HostStats.Hands and
// HostStats.NetChips), not the lifetime totals.
func TestHostRatesUseBatch3Era(t *testing.T) {
	s := hostSnap()
	for i := range s.Stats {
		if s.Stats[i].Name == "Priya" {
			s.Stats[i].Hands, s.Stats[i].LifetimeNet = 510, 4000
			s.Stats[i].Host.Hands, s.Stats[i].Host.NetChips = 10, 100
			s.Stats[i].Host.NetBB, s.Stats[i].Host.SeatedSeconds = 10, 600
		}
	}
	s.Settle.ChipsPerUnit, s.Settle.Currency = 2, "₹"
	h := newHarness(t, Fixture{Name: "era", Snap: s})
	h.press("d", "shift+tab")
	row := lineWith(stripANSI(h.viewAt(140, 40)), "Priya")
	if !strings.Contains(row, "+100.0") || !strings.Contains(row, "+₹300") {
		t.Errorf("Host tab row: want +100.0 bb/100 and +₹300/hour: %q", strings.TrimSpace(row))
	}
	selectRow(t, h, "Priya")
	h.press("enter")
	v := stripANSI(h.viewAt(140, 40))
	if l := lineWith(v, "bb/100"); !strings.Contains(l, "+100.0") {
		t.Errorf("host card bb/100: %q", strings.TrimSpace(l))
	}
	if l := lineWith(v, "₹/hour"); !strings.Contains(l, "+₹300") {
		t.Errorf("host card ₹/hour: %q", strings.TrimSpace(l))
	}
}
