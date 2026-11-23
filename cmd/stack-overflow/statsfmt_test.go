package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// The stats CLI and the Host tab of the TUI round bb/100 and money/hour in
// the same way, so the host sees one number for one stat.
func TestStatsRatesRoundAsTUI(t *testing.T) {
	for _, c := range []struct {
		netBB float64
		hands int
		want  string // what the TUI prints for the same row
	}{
		{2.25, 100, "+2.3"},
		{-2.25, 100, "-2.3"},
		{0.25, 10, "+2.5"},
		{-0.0004, 100, "0.0"},
	} {
		if got := bbPer100(c.netBB, c.hands); got != c.want {
			t.Errorf("bbPer100(%v, %d) = %q, the TUI shows %q", c.netBB, c.hands, got, c.want)
		}
	}
	// 1 chip at 3 chips per unit is 33.33 hundredths; 10 minutes seated make
	// exactly 200 hundredths (₹2) per hour.
	if got, want := moneyPerHour(1, 600, "₹", 3), signedMoney(200, "₹"); got != want {
		t.Errorf("moneyPerHour(1 chip, 600 s, cpu 3) = %q, the exact rate is %q", got, want)
	}
	// No currency: chips per hour, as the TUI shows under "chips/h".
	if got := moneyPerHour(1868, 622, "", 2); got != "+10,812" {
		t.Errorf("moneyPerHour(1868 chips, 622 s, no currency) = %q, want %q", got, "+10,812")
	}
}

// With -currency "", the rate column and its legend name the unit.
func TestHostStatsRateHeadWithoutCurrency(t *testing.T) {
	rows := []server.PlayerStats{{
		Name: "Maximilian.X", Hands: 50, LifetimeNet: 1868,
		Host: &server.HostStats{Hands: 50, NetChips: 1868, NetBB: 121.05, SeatedSeconds: 622},
	}}
	var b bytes.Buffer
	writeHostStats(&b, rows, "", 2)
	out := b.String()
	if strings.Contains(out, "  /hour") || strings.Contains(out, ". /hour:") {
		t.Errorf("rate column without a unit:\n%s", out)
	}
	if !strings.Contains(out, "chips/hour") || !strings.Contains(out, "chips/hour: chips won since batch 3") || !strings.Contains(out, "+10,812") {
		t.Errorf("no chips/hour column:\n%s", out)
	}
	b.Reset()
	writeHostStats(&b, rows, "₹", 2)
	if out := b.String(); !strings.Contains(out, "₹/hour: money won since batch 3") || !strings.Contains(out, "+₹5,405.79") {
		t.Errorf("the ₹/hour column changed:\n%s", out)
	}
}

// A veteran with 500 hands before batch 3 and 10 hands after it. bb/100
// and the hourly rate count only the batch 3 hands (HostStats.Hands and
// HostStats.NetChips), not the lifetime totals.
func TestHostStatsRatesUseBatch3Era(t *testing.T) {
	rows := []server.PlayerStats{{
		Name: "Veteran", Hands: 510, LifetimeNet: 4000,
		Host: &server.HostStats{Hands: 10, NetChips: 100, NetBB: 10, SeatedSeconds: 600},
	}}
	var b bytes.Buffer
	writeHostStats(&b, rows, "₹", 2)
	line := rowOf(b.String(), "Veteran")
	// 100 × 10 bb / 10 hands; 100 chips = ₹50 in 10 minutes = ₹300 per hour.
	if !strings.Contains(line, "+100.0") || !strings.Contains(line, "+₹300") {
		t.Errorf("want +100.0 bb/100 and +₹300/hour: %q", line)
	}
}
