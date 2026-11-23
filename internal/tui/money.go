package tui

import (
	"strconv"
	"strings"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// toMoney converts chips to hundredths of the currency unit, rounded half
// away from zero, as the server does for the settlement.
func toMoney(chipCount, perUnit int64) server.Money {
	if perUnit <= 0 {
		perUnit = 1
	}
	n := chipCount * 100
	q, r := n/perUnit, n%perUnit
	if 2*abs64(r) >= perUnit {
		if n < 0 {
			q--
		} else {
			q++
		}
	}
	return server.Money(q)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// money formats an amount with the currency symbol: "₹1,23,456.50",
// "₹350" (no ".00" for whole amounts), "-₹70.25". The rupee uses Indian
// grouping (lakh, crore); other symbols use groups of three.
func money(a server.Money, currency string) string {
	if a < 0 {
		return "-" + money(-a, currency)
	}
	whole, frac := int64(a)/100, int64(a)%100
	s := currency + groupDigits(strconv.FormatInt(whole, 10), currency == "₹")
	if frac != 0 {
		s += "." + strconv.FormatInt(100+frac, 10)[1:]
	}
	return s
}

// signedMoney is money with an explicit "+" for amounts above zero.
func signedMoney(a server.Money, currency string) string {
	if a > 0 {
		return "+" + money(a, currency)
	}
	return money(a, currency)
}

// groupDigits puts commas into a string of digits.
func groupDigits(s string, indian bool) string {
	if len(s) <= 3 {
		return s
	}
	head, tail := s[:len(s)-3], s[len(s)-3:]
	size := 3
	if indian {
		size = 2
	}
	var parts []string
	for len(head) > size {
		parts = append([]string{head[len(head)-size:]}, parts...)
		head = head[:len(head)-size]
	}
	parts = append([]string{head}, parts...)
	return strings.Join(append(parts, tail), ",")
}

// perUnit returns the chips per currency unit for the money columns.
func (m *model) perUnit() int64 {
	if p := m.snap.Settle.ChipsPerUnit; p > 0 {
		return p
	}
	return max(m.snap.Config.ChipsPerUnit, 1)
}

// currency returns the currency symbol for the money columns.
func (m *model) currency() string {
	if c := m.snap.Settle.Currency; c != "" {
		return c
	}
	return m.snap.Config.Currency
}
