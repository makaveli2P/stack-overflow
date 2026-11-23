package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		m    server.Money
		cur  string
		want string
	}{
		{0, "₹", "₹0"},
		{35000, "₹", "₹350"},
		{35050, "₹", "₹350.50"},
		{5, "₹", "₹0.05"},
		{-35000, "₹", "-₹350"},
		{12345678900, "₹", "₹12,34,56,789"},
		{100000000, "₹", "₹10,00,000"},
		{12345678900, "$", "$123,456,789"},
		{123456, "€", "€1,234.56"},
		{99999, "Rs", "Rs999.99"},
	}
	for _, c := range cases {
		if got := formatMoney(c.m, c.cur); got != c.want {
			t.Errorf("formatMoney(%d, %q) = %q, want %q", c.m, c.cur, got, c.want)
		}
	}
	if got := signedMoney(500, "₹"); got != "+₹5" {
		t.Errorf("signedMoney = %q", got)
	}
}

func TestChipsToMoney(t *testing.T) {
	cases := []struct {
		chips, cpu int64
		want       server.Money
	}{
		{1000, 2, 50000},
		{1, 2, 50},
		{1, 3, 33},
		{2, 3, 67},
		{-1, 3, -33},
		{-2, 3, -67},
		{1, 200, 1}, // 0.5 rounds away from zero
		{-1, 200, -1},
		{7, 0, 700},
	}
	for _, c := range cases {
		if got := chipsToMoney(c.chips, c.cpu); got != c.want {
			t.Errorf("chipsToMoney(%d, %d) = %d, want %d", c.chips, c.cpu, got, c.want)
		}
	}
}

func TestChipsAndPercent(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", -1234567: "-1,234,567"} {
		if got := formatChips(in); got != want {
			t.Errorf("formatChips(%d) = %q, want %q", in, got, want)
		}
	}
	if signedChips(700) != "+700" || signedChips(-700) != "-700" || signedChips(0) != "0" {
		t.Error("signedChips")
	}
	if percent(1, 0) != "—" || percent(1, 3) != "33%" || percent(2, 3) != "67%" || percent(5, 5) != "100%" {
		t.Error("percent")
	}
}

func TestWriteStats(t *testing.T) {
	since := time.Date(2026, 11, 10, 18, 0, 0, 0, time.Local)
	stats := []server.PlayerStats{
		{Name: "Alice", Hands: 120, HandsWon: 30, VPIP: 30, PFR: 12, SawFlop: 40, Showdowns: 10, ShowdownsWon: 6,
			AllIns: 3, BiggestPot: 2400, BestHand: "Flush, Ace high", LifetimeNet: 1400, BuyIns: 2, PeriodNet: 700},
		{Name: "Bob", Hands: 4, PeriodNet: -700, LifetimeNet: -700, BuyIns: 1},
	}
	settle := server.Settlement{
		Period: 3, Since: since, ChipsPerUnit: 2, Currency: "₹",
		Balances:  []server.Balance{{Name: "Alice", Chips: 700, Money: 35000}, {Name: "Bob", Chips: -700, Money: -35000}},
		Transfers: []server.Transfer{{From: "Bob", To: "Alice", Amount: 35000}},
		History: []server.PastPeriod{{Period: 2, Since: since.Add(-48 * time.Hour), SettledAt: since,
			Transfers: []server.Transfer{{From: "Alice", To: "Bob", Amount: 10050}}, Currency: "₹"}},
	}
	var b bytes.Buffer
	writeStats(&b, "/x/state.json", stats, settle)
	out := b.String()
	for _, want := range []string{
		"Stack Overflow · stats · /x/state.json",
		"Period 3 since 10 Nov 2026 18:00",
		"Player", "W$SD", "Biggest pot",
		"Flush, Ace high",
		"+₹350", "-₹350", "25%", "60%",
		"receives ₹350", "pays ₹350",
		"Bob pays Alice ₹350",
		"Period 2", "Alice paid Bob ₹100.50",
		"2 chips = ₹1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
	// Columns line up: the header and each row have the same width up to
	// the last column.
	lines := strings.Split(out, "\n")
	var table []string
	for _, l := range lines {
		if strings.HasPrefix(l, "  Player") || strings.HasPrefix(l, "  Alice") || strings.HasPrefix(l, "  Bob ") {
			table = append(table, l)
		}
	}
	if len(table) < 3 {
		t.Fatalf("no table in:\n%s", out)
	}
	// Hands is right-aligned: its last rune is at the same rune index in
	// each line, and a space or the line end follows.
	end := len([]rune(table[0][:strings.Index(table[0], "Hands")+len("Hands")]))
	for _, l := range table[1:3] {
		r := []rune(l)
		if len(r) <= end || r[end-1] == ' ' || r[end] != ' ' {
			t.Errorf("Hands column is not right-aligned:\n%s\n%s", table[0], l)
		}
	}

	b.Reset()
	writeStats(&b, "/x", nil, server.Settlement{ChipsPerUnit: 2, Currency: "₹"})
	if !strings.Contains(b.String(), "No hands yet.") || !strings.Contains(b.String(), "Nobody pays.") {
		t.Fatalf("empty output:\n%s", b.String())
	}
}

func TestHostCommandUsage(t *testing.T) {
	var out, errb bytes.Buffer
	dir := t.TempDir()
	if code := runMerge([]string{"-state", filepath.Join(dir, "s.json"), "only-one"}, &out, &errb); code != exitUsage {
		t.Fatalf("merge with one name: exit %d", code)
	}
	if code := runMerge([]string{"-state", filepath.Join(dir, "s.json"), "Ann", "ann"}, &out, &errb); code != exitUsage {
		t.Fatalf("merge with the same name: exit %d", code)
	}
	errb.Reset()
	if code := runMerge([]string{"-state", filepath.Join(dir, "s.json"), "a", "b"}, &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "no state file") {
		t.Fatalf("merge without a file: exit %d, %q", code, errb.String())
	}
	errb.Reset()
	if code := runStats([]string{"-state", filepath.Join(dir, "s.json")}, &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "no state file") {
		t.Fatalf("stats without a file: exit %d, %q", code, errb.String())
	}
	if code := runStats([]string{"extra"}, &out, &errb); code != exitUsage {
		t.Fatalf("stats with an extra argument: exit %d", code)
	}
}

func TestMergeWhileServerRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	cfg := defaultOptions().roomConfig(path)

	// A room that runs and stops writes the state file.
	room, err := server.NewRoom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { room.Run(ctx); close(done) }()
	cancel()
	<-done

	var out, errb bytes.Buffer
	if code := runStats([]string{"-state", path}, &out, &errb); code != 0 {
		t.Fatalf("stats: exit %d, %q", code, errb.String())
	}

	// A second room holds the lock until its Run returns.
	if _, err := server.NewRoom(cfg); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	code := runMerge([]string{"-state", path, "Ann", "Bob"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "Stop the server") {
		t.Fatalf("merge while a room holds the file: exit %d, %q", code, errb.String())
	}
}

// The merge error names the player that does not exist, not both players.
func TestMergeNamesMissingPlayer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	v1 := `{"version":1,"players":{"SHA256:a":{"name":"Ann","bank":1000,"buy_ins":1,"bought_chips":1000},` +
		`"SHA256:b":{"name":"Bob","bank":1000,"buy_ins":1,"bought_chips":1000}}}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runMerge([]string{"-state", path, "Ann", "Zoe"}, &out, &errb); code != 1 {
		t.Fatalf("merge into a missing player: exit %d", code)
	}
	if got := errb.String(); !strings.Contains(got, `player "Zoe".`) || strings.Contains(got, `"Ann"`) {
		t.Fatalf("message %q must name only the missing player", got)
	}
	errb.Reset()
	if code := runMerge([]string{"-state", path, "Ann", "Bob"}, &out, &errb); code != 0 {
		t.Fatalf("merge: exit %d, %q", code, errb.String())
	}
}

func TestAccountsCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var out, errb bytes.Buffer
	if code := runAccounts([]string{"-state", path}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no state file") {
		t.Fatalf("accounts without a file: exit %d, %q", code, errb.String())
	}
	if code := runAccounts([]string{"-state", path, "extra"}, &out, &errb); code != exitUsage {
		t.Fatalf("accounts with an extra argument: exit %d", code)
	}

	fp := "SHA256:" + strings.Repeat("k", 43)
	cfg := defaultOptions().roomConfig(path)
	room, err := server.NewRoom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { room.Run(ctx); close(done) }()
	if err := room.CreatePINAccount("ops", "4826"); err != nil {
		t.Fatal(err)
	}
	// The welcome screen calls SetName. A key that never did is not saved.
	if s, err := room.ConnectWith("key:"+fp, "kim", server.ConnectOptions{}); err != nil {
		t.Fatal(err)
	} else if err := s.SetName("kim"); err != nil {
		t.Fatal(err)
	}

	// While the room runs, the command tells the host what to do.
	errb.Reset()
	if code := runAccounts([]string{"-state", path}, &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "Stop the server") || !strings.Contains(errb.String(), "ssh-keygen -lf") {
		t.Fatalf("accounts while the server runs: exit %d, %q", code, errb.String())
	}
	cancel()
	<-done

	out.Reset()
	errb.Reset()
	if code := runAccounts([]string{"-state", path}, &out, &errb); code != 0 {
		t.Fatalf("accounts: exit %d, %q", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"Stack Overflow · accounts", "Name", "kim", fp, "ops", "pin:ops", "-admin"} {
		if !strings.Contains(got, want) {
			t.Errorf("output has no %q:\n%s", want, got)
		}
	}
	// Each value that the command prints is a valid -admin entry.
	for _, c := range []string{fp, "pin:ops"} {
		if _, err := parseAdmins(c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
}
