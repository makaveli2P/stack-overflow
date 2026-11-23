package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// chips formats an amount with thousands separators: 12345 -> "12,345".
func chips(n int64) string {
	if n < 0 {
		return "-" + chips(-n)
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// signed formats a net result with an explicit sign.
func signed(n int64) string {
	if n > 0 {
		return "+" + chips(n)
	}
	return chips(n)
}

// seconds rounds a duration up to whole seconds, never below zero.
func seconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// durationText formats d as the server log does: "30s" for whole seconds,
// else the shortest decimal form, for example "1.5s". The E hint and the
// help use it, so that they show the time that one extension really adds.
func durationText(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}

// fit cuts s to at most w cells. It keeps ANSI styles intact.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// spread puts left and right on one line of width w, with space between.
// The left part gets cut first when the line is too long.
func spread(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	if rw >= w {
		return fit(right, w)
	}
	left = fit(left, w-rw-1)
	gap := w - ansi.StringWidth(left) - rw
	return left + strings.Repeat(" ", max(gap, 1)) + right
}

// padRight pads s with spaces to w cells (or cuts it to w cells).
func padRight(s string, w int) string {
	s = fit(s, w)
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// clip makes a block fit inside w x h: it cuts long lines and extra lines.
func clip(block string, w, h int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > h {
		lines = lines[:max(h, 0)]
	}
	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	return strings.Join(lines, "\n")
}

// height returns the number of lines in a block.
func height(block string) int { return lipgloss.Height(block) }

// wrapLines word-wraps text to w cells. Continuation lines get an indent.
func wrapLines(text string, w, indent int) []string {
	if w <= indent+4 {
		return []string{fit(text, w)}
	}
	first := strings.Split(ansi.Wrap(text, w, ""), "\n")
	if len(first) == 1 {
		return first
	}
	rest := strings.Join(first[1:], " ")
	out := []string{first[0]}
	pad := strings.Repeat(" ", indent)
	for _, l := range strings.Split(ansi.Wrap(rest, w-indent, ""), "\n") {
		out = append(out, pad+l)
	}
	return out
}
