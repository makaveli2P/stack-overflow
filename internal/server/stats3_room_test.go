package server

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

// The first dealt player gets As Ad, the second gets 7c 2d (one card at a
// time). Kd, Qc and Tc are burn cards. The board 7h 3s 9d Jc 2s puts the
// sevens ahead of the aces only on the river: two pair, sevens and twos.
const badBeatDeck = "As 7c Ad 2d Kd 7h 3s 9d Qc Jc Tc 2s"

// A full hand through the room and the engine: a preflop all-in where
// pocket aces lose to seven-deuce on the river. Everyone sees the bad
// beat, the luckiest river and the nemesis. Only the host sees the host
// stats. ReadStats gives the same values from the saved file.
func TestBadBeatThroughRoom(t *testing.T) {
	cfg := testConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	h := newHarness(t, cfg, stackedDeck(badBeatDeck))
	host := h.connectWith("key:host", "hosty", ConnectOptions{Host: true})
	guest := h.connectWith("key:guest", "guest", ConnectOptions{})
	must(t, host.Sit(-1))
	must(t, guest.Sit(-1))
	h.advance(cfg.NextHandDelay)
	if !h.snap(host).Table.InHand {
		t.Fatal("hand did not start")
	}
	aces, sevens := host, guest
	if mine := h.snap(guest).Table.Seats[h.snap(guest).Table.MySeat].Cards; slices.Contains(mine, cards.MustParseMany("As")[0]) {
		aces, sevens = guest, host
	}
	actor, other := h.toAct(host, guest)
	must(t, actor.Act(engine.Action{Type: engine.AllIn}))
	must(t, other.Act(engine.Action{Type: engine.Call}))
	if h.snap(host).Table.InHand {
		t.Fatal("hand did not end")
	}

	checkFun := func(rows []PlayerStats, who string) {
		t.Helper()
		var a, s PlayerStats
		for _, r := range rows {
			switch r.Name {
			case aces.name:
				a = r
			case sevens.name:
				s = r
			}
		}
		if a.BadBeats != 1 || a.WorstBeat < 0.85 || a.WorstBeat > 0.90 {
			t.Errorf("%s: aces bad beat %d at %.4f", who, a.BadBeats, a.WorstBeat)
		}
		if got := cardStrings(a.WorstBeatWith); !slices.Equal(got, []string{"As", "Ad"}) && !slices.Equal(got, []string{"Ad", "As"}) {
			t.Errorf("%s: worst beat with %v", who, got)
		}
		if s.BadBeats != 0 || a.LuckiestRiver != 0 {
			t.Errorf("%s: sevens bad beats %d, aces river %d", who, s.BadBeats, a.LuckiestRiver)
		}
		if s.LuckiestRiver != 2000 || s.RiverCard.String() != "2s" {
			t.Errorf("%s: luckiest river %d on %v", who, s.LuckiestRiver, s.RiverCard)
		}
		if a.Nemesis != sevens.name || a.NemesisChips != 1000 || s.Nemesis != "" {
			t.Errorf("%s: nemesis %q %d / %q", who, a.Nemesis, a.NemesisChips, s.Nemesis)
		}
		if s.FirstWinAfter != 1 || a.FirstWinAfter != 0 {
			t.Errorf("%s: first win %d / %d", who, s.FirstWinAfter, a.FirstWinAfter)
		}
	}
	for _, sess := range []*Session{host, guest} {
		snap := h.snap(sess)
		checkFun(snap.Stats, sess.name)
		for _, r := range snap.Stats {
			if sess == guest && r.Host != nil {
				t.Errorf("guest sees the host stats of %s", r.Name)
			}
			if sess == host {
				if r.Host == nil || r.Host.AllInShowdowns != 1 {
					t.Errorf("host: host stats of %s: %+v", r.Name, r.Host)
				} else if won := r.Host.AllInShowdownsWon == 1; won != (r.Name == sevens.name) {
					t.Errorf("host: all-in win of %s: %+v", r.Name, r.Host)
				}
			}
			if r.Name != sess.name && r.FavouriteHand != "" {
				t.Errorf("%s sees the favourite hand of %s", sess.name, r.Name)
			}
		}
	}
	h.stop()
	rows, _, err := ReadStats(cfg)
	if err != nil {
		t.Fatal(err)
	}
	checkFun(rows, "ReadStats")
}
