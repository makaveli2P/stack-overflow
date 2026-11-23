package server

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

func TestChipsToMoney(t *testing.T) {
	tests := []struct {
		chips, cpu int64
		want       Money
	}{
		{0, 2, 0},
		{1, 2, 50},
		{-1, 2, -50},
		{3, 2, 150},
		{1000, 2, 50000}, // a 1,000-chip buy-in costs ₹500.00
		{-1001, 2, -50050},
		{1, 3, 33},   // 33.33
		{2, 3, 67},   // 66.67
		{-1, 3, -33}, // -33.33
		{-2, 3, -67}, // -66.67
		{1000, 3, 33333},
		{-1000, 3, -33333},
		{1, 8, 13},    // 12.5: half away from zero
		{-1, 8, -13},  // -12.5
		{3, 8, 38},    // 37.5
		{-3, 8, -38},  // -37.5
		{1, 200, 1},   // 0.5
		{-1, 200, -1}, // -0.5
		{1, 201, 0},   // 0.4975
		{5, 0, 500},   // cpu <= 0 means 1
		{5, -4, 500},  //
		{7, 1, 700},   //
	}
	for _, tc := range tests {
		if got := chipsToMoney(tc.chips, tc.cpu); got != tc.want {
			t.Errorf("chipsToMoney(%d, %d) = %d, want %d", tc.chips, tc.cpu, got, tc.want)
		}
	}
}

func TestSettlementTable(t *testing.T) {
	b := func(name string, chips int64) Balance { return Balance{Name: name, Chips: chips} }
	tests := []struct {
		name      string
		cpu       int64
		nets      []Balance
		balances  []Balance
		transfers []Transfer
	}{
		{
			name: "empty",
			cpu:  2,
		},
		{
			name: "two players, cpu 2",
			cpu:  2,
			nets: []Balance{b("bob", -700), b("alice", 700)},
			balances: []Balance{
				{Name: "alice", Chips: 700, Money: 35000},
				{Name: "bob", Chips: -700, Money: -35000},
			},
			transfers: []Transfer{{From: "bob", To: "alice", Amount: 35000}},
		},
		{
			name: "largest debtor pays first, cpu 2",
			cpu:  2,
			nets: []Balance{b("alice", 1500), b("bob", -700), b("carol", -800)},
			balances: []Balance{
				{Name: "alice", Chips: 1500, Money: 75000},
				{Name: "bob", Chips: -700, Money: -35000},
				{Name: "carol", Chips: -800, Money: -40000},
			},
			transfers: []Transfer{
				{From: "carol", To: "alice", Amount: 40000},
				{From: "bob", To: "alice", Amount: 35000},
			},
		},
		{
			name: "cpu 3: rounding leaves +1, the largest balance gives it back",
			cpu:  3,
			nets: []Balance{b("alice", 2), b("bob", -1), b("carol", -1)},
			balances: []Balance{
				{Name: "alice", Chips: 2, Money: 66}, // 66.67 -> 67 -> 66
				{Name: "bob", Chips: -1, Money: -33},
				{Name: "carol", Chips: -1, Money: -33},
			},
			transfers: []Transfer{
				{From: "bob", To: "alice", Amount: 33},
				{From: "carol", To: "alice", Amount: 33},
			},
		},
		{
			name: "cpu 3: rounding leaves -1, the largest debt takes it",
			cpu:  3,
			nets: []Balance{b("alice", 1), b("bob", 1), b("carol", -2)},
			balances: []Balance{
				{Name: "alice", Chips: 1, Money: 33},
				{Name: "bob", Chips: 1, Money: 33},
				{Name: "carol", Chips: -2, Money: -66}, // -66.67 -> -67 -> -66
			},
			transfers: []Transfer{
				{From: "carol", To: "alice", Amount: 33},
				{From: "carol", To: "bob", Amount: 33},
			},
		},
		{
			name: "cpu 3: a remainder of 3 goes one hundredth each to the 3 largest",
			cpu:  3,
			nets: []Balance{b("a", 2), b("b", 2), b("c", 2), b("d", -1), b("e", -1), b("f", -1), b("g", -1), b("h", -1), b("i", -1)},
			balances: []Balance{
				{Name: "a", Chips: 2, Money: 66},
				{Name: "b", Chips: 2, Money: 66},
				{Name: "c", Chips: 2, Money: 66},
				{Name: "d", Chips: -1, Money: -33},
				{Name: "e", Chips: -1, Money: -33},
				{Name: "f", Chips: -1, Money: -33},
				{Name: "g", Chips: -1, Money: -33},
				{Name: "h", Chips: -1, Money: -33},
				{Name: "i", Chips: -1, Money: -33},
			},
			transfers: []Transfer{
				{From: "d", To: "a", Amount: 33},
				{From: "e", To: "b", Amount: 33},
				{From: "f", To: "c", Amount: 33},
				{From: "g", To: "a", Amount: 33},
				{From: "h", To: "b", Amount: 33},
				{From: "i", To: "c", Amount: 33},
			},
		},
		{
			name: "cpu 3: 1000-chip results",
			cpu:  3,
			nets: []Balance{b("alice", 1000), b("bob", -500), b("carol", -500)},
			balances: []Balance{
				{Name: "alice", Chips: 1000, Money: 33334}, // 333.33 -> +0.01
				{Name: "bob", Chips: -500, Money: -16667},  // -166.67
				{Name: "carol", Chips: -500, Money: -16667},
			},
			transfers: []Transfer{
				{From: "bob", To: "alice", Amount: 16667},
				{From: "carol", To: "alice", Amount: 16667},
			},
		},
		{
			name: "greedy chain, cpu 1",
			cpu:  1,
			nets: []Balance{b("A", 5), b("B", 3), b("C", -4), b("D", -2), b("E", -2)},
			balances: []Balance{
				{Name: "A", Chips: 5, Money: 500},
				{Name: "B", Chips: 3, Money: 300},
				{Name: "D", Chips: -2, Money: -200},
				{Name: "E", Chips: -2, Money: -200},
				{Name: "C", Chips: -4, Money: -400},
			},
			transfers: []Transfer{
				{From: "C", To: "A", Amount: 400},
				{From: "D", To: "B", Amount: 200},
				{From: "E", To: "A", Amount: 100},
				{From: "E", To: "B", Amount: 100},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := moneyBalances(tc.nets, tc.cpu)
			if len(got) == 0 && len(tc.balances) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tc.balances) {
				t.Errorf("balances:\n got %+v\nwant %+v", got, tc.balances)
			}
			tr := greedyTransfers(got)
			if !reflect.DeepEqual(tr, tc.transfers) {
				t.Errorf("transfers:\n got %+v\nwant %+v", tr, tc.transfers)
			}
			checkSettles(t, got, tr)
		})
	}
}

// checkSettles checks that the money balances sum to zero and that the
// transfers bring every balance to exactly zero with at most n-1 payments.
func checkSettles(t *testing.T, balances []Balance, transfers []Transfer) {
	t.Helper()
	left := map[string]Money{}
	var sum Money
	for _, b := range balances {
		left[b.Name] += b.Money
		sum += b.Money
	}
	if sum != 0 {
		t.Errorf("money balances sum to %d, want 0: %+v", sum, balances)
	}
	if n := len(balances); n > 0 && len(transfers) > n-1 {
		t.Errorf("%d transfers for %d balances, want at most %d", len(transfers), n, n-1)
	}
	for _, tr := range transfers {
		if tr.Amount <= 0 {
			t.Errorf("transfer %+v: amount must be positive", tr)
		}
		if left[tr.From] >= 0 || left[tr.To] <= 0 {
			t.Errorf("transfer %+v: payer must owe and payee must receive (%d, %d)", tr, left[tr.From], left[tr.To])
		}
		left[tr.From] += tr.Amount
		left[tr.To] -= tr.Amount
	}
	for name, m := range left {
		if m != 0 {
			t.Errorf("after the transfers %s has %d left, want 0", name, m)
		}
	}
}

func TestSettlementRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for iter := range 3000 {
		cpu := int64(rng.IntN(7) + 1)
		n := rng.IntN(9) + 1
		nets := make([]Balance, n)
		var sum int64
		for i := range n - 1 {
			c := rng.Int64N(5001) - 2500
			nets[i] = Balance{Name: fmt.Sprintf("p%02d", i), Chips: c}
			sum += c
		}
		nets[n-1] = Balance{Name: fmt.Sprintf("p%02d", n-1), Chips: -sum}
		balances := moneyBalances(nets, cpu)
		for _, b := range balances {
			exact := float64(b.Chips) * 100 / float64(cpu)
			if d := float64(b.Money) - exact; d > float64(n) || d < -float64(n) {
				t.Fatalf("iter %d: %s money %d far from %.2f", iter, b.Name, b.Money, exact)
			}
		}
		for i := 1; i < len(balances); i++ {
			if balances[i-1].Money < balances[i].Money {
				t.Fatalf("iter %d: balances not sorted high to low: %+v", iter, balances)
			}
		}
		checkSettles(t, balances, greedyTransfers(balances))
		if t.Failed() {
			t.Fatalf("iter %d: cpu %d nets %+v", iter, cpu, nets)
		}
	}
}

func TestClosePeriod(t *testing.T) {
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "alice", Bank: 1700, BuyIns: 1, TotalBuyIns: 3, BoughtChips: 1000, PeriodHands: 4, SettledNet: 50}
	st.Accounts["p2"] = &account{Name: "bob", Bank: 300, BuyIns: 1, TotalBuyIns: 1, BoughtChips: 1000, PeriodHands: 4}
	later := testTime.Add(3 * time.Hour)
	past := st.closePeriod(later, 2, "₹")
	want := PastPeriod{Period: 1, Since: testTime, SettledAt: later, Currency: "₹", ChipsPerUnit: 2,
		Transfers: []Transfer{{From: "bob", To: "alice", Amount: 35000}}}
	if !reflect.DeepEqual(past, want) || !reflect.DeepEqual(st.History, []PastPeriod{want}) {
		t.Errorf("past = %+v\nhistory %+v", past, st.History)
	}
	if st.Period != 2 || !st.Since.Equal(later) {
		t.Errorf("period %d since %v", st.Period, st.Since)
	}
	a, b := st.Accounts["p1"], st.Accounts["p2"]
	if a.Bank != 0 || a.BuyIns != 0 || a.BoughtChips != 0 || a.PeriodHands != 0 || a.SettledNet != 750 || a.TotalBuyIns != 3 {
		t.Errorf("alice after close: %+v", a)
	}
	if b.SettledNet != -700 || b.TotalBuyIns != 1 {
		t.Errorf("bob after close: %+v", b)
	}
	if s := st.settlement(nil, 2, "₹"); len(s.Balances) != 0 || len(s.Transfers) != 0 || len(s.History) != 1 || s.Period != 2 {
		t.Errorf("settlement after close: %+v", s)
	}
}

// Chips that do not balance (here a version 1 chip bank that lost 700
// chips) must not move honest balances. Before the fix, the 700-chip gap
// was spread one hundredth at a time, and a player at -5 chips showed as
// receiving ₹85.
func TestSettleUnbalancedChips(t *testing.T) {
	nets := []Balance{
		{Name: "erin", Chips: 265}, {Name: "bob", Chips: -5},
		{Name: "carol", Chips: -210}, {Name: "alice", Chips: -750},
	}
	got := moneyBalances(nets, 2)
	want := []Balance{
		{Name: "erin", Chips: 265, Money: 13250},
		{Name: "bob", Chips: -5, Money: -250},
		{Name: "carol", Chips: -210, Money: -10500},
		{Name: "alice", Chips: -750, Money: -37500},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("balances:\n got %+v\nwant %+v", got, want)
	}
	tr := greedyTransfers(got)
	if len(tr) != 1 || tr[0] != (Transfer{From: "alice", To: "erin", Amount: 13250}) {
		t.Fatalf("transfers %+v", tr)
	}

	// Rounding still lands on the exact money of an odd chip total.
	got = moneyBalances([]Balance{{Name: "a", Chips: 1}, {Name: "b", Chips: 1}, {Name: "c", Chips: 1}}, 3)
	var sum Money
	for _, b := range got {
		sum += b.Money
	}
	if sum != 100 {
		t.Fatalf("3 chips at 3 per unit sum to %d hundredths, want 100: %+v", sum, got)
	}
}
