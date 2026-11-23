package server

import (
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// TestHostRatesUseBatch3Era checks that a migrated account (lifetime Stats
// and SettledNet from before batch 3, Host stats from zero) gets bb/100 and
// money per hour from counters of one era only. bb/100 and the rate divide
// HostStats.NetBB by HostStats.Hands and HostStats.NetChips by
// HostStats.SeatedSeconds; the lifetime PlayerStats.Hands and LifetimeNet
// also count the older hands and must not go into those rates.
func TestHostRatesUseBatch3Era(t *testing.T) {
	st := newState(testTime)
	// The v2 to v3 migration keeps Stats and SettledNet, and starts the
	// new Fun and Host stats at zero.
	st.Accounts["v"] = &account{Name: "Vet", SettledNet: 4000, Stats: handStats{Hands: 500, HandsWon: 120}}
	st.Accounts["o"] = &account{Name: "Opp"}
	for i := 0; i < 10; i++ {
		record(st, &engine.Result{BigBlind: 10, Players: []engine.PlayerSummary{
			{PlayerID: "v", Committed: 10, Won: 20},
			{PlayerID: "o", Committed: 10},
		}})
	}
	st.Accounts["v"].Host.SeatedSeconds = 600
	rows, ids := st.statsTable(nil, nil, nil)
	var row PlayerStats
	for i, id := range ids {
		if id == "v" {
			row = rows[i]
		}
	}
	h := st.hostStats("v")
	if row.Hands != 510 {
		t.Fatalf("lifetime row: Hands=%d, want 510 (500 old hands + 10 new hands)", row.Hands)
	}
	if h.Hands != 10 || h.NetChips != 100 || h.NetBB != 10 {
		t.Fatalf("host stats: Hands=%d NetChips=%d NetBB=%.1f, want 10, 100 and 10.0", h.Hands, h.NetChips, h.NetBB)
	}
	bb100 := 100 * h.NetBB / float64(h.Hands)
	perHourChips := float64(h.NetChips) * 3600 / float64(h.SeatedSeconds)
	if bb100 < 99 || bb100 > 101 {
		t.Errorf("bb/100 = %.1f, want +100.0 (10 bb in the 10 hands that have host stats)", bb100)
	}
	if perHourChips < 599 || perHourChips > 601 {
		t.Errorf("rate = %.0f chips/hour, want 600 (100 chips in the 10 minutes that SeatedSeconds counts)", perHourChips)
	}

	// The opponent lost the same chips: the counters are per player.
	if o := st.hostStats("o"); o.Hands != 10 || o.NetChips != -100 {
		t.Errorf("opponent: Hands=%d NetChips=%d, want 10 and -100", o.Hands, o.NetChips)
	}
}

// TestHostRateCountersMergeAndLoad checks that MergeAccounts sums Hands
// and NetChips, and that an early version 3 file, which has no
// Host.NetChips and no Host.Hands, gives Hands from Fun.Hands.
func TestHostRateCountersMergeAndLoad(t *testing.T) {
	st := newState(testTime)
	st.Accounts["a"] = &account{Name: "Ann", Fun: funStats{Hands: 3}, Host: HostStats{NetChips: 50, NetBB: 5}}
	st.Accounts["b"] = &account{Name: "Bob", Fun: funStats{Hands: 2}, Host: HostStats{NetChips: -20, NetBB: -2}}
	if err := st.merge("Bob", "Ann"); err != nil {
		t.Fatal(err)
	}
	if h := st.hostStats("a"); h.Hands != 5 || h.NetChips != 30 || h.NetBB != 3 {
		t.Errorf("merge: Hands=%d NetChips=%d NetBB=%.1f, want 5, 30 and 3.0", h.Hands, h.NetChips, h.NetBB)
	}

	data := []byte(`{"version": 3, "next_id": 2, "period": 1, "accounts": {
		"a": {"name": "Ann", "stats": {"hands": 40}, "fun": {"hands": 12}, "host": {"NetBB": 6}}
	}}`)
	loaded, _, err := parseState(data, "test", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if h := loaded.hostStats("a"); h.Hands != 12 || h.NetBB != 6 {
		t.Errorf("early v3 file: Hands=%d NetBB=%.1f, want 12 (from Fun.Hands) and 6.0", h.Hands, h.NetBB)
	}
}
