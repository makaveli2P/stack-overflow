package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// defaultOptions returns the server options with every flag at its default.
func defaultOptions() options {
	o, _ := parseServerFlags(nil, func(string) string { return "" }, io.Discard)
	return o
}

// runStats is "stack-overflow stats": it prints the stats table and the
// settlement from the state file. It works while the server runs, because
// it only reads the file.
func runStats(args []string, stdout, stderr io.Writer) int {
	o := defaultOptions()
	fs := flag.NewFlagSet("stack-overflow stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.state, "state", defaultState, "state file")
	fs.Int64Var(&o.chipsPerUnit, "chips-per-unit", o.chipsPerUnit, "chips for one unit of money (only for a state file that has no saved rate)")
	fs.StringVar(&o.currency, "currency", o.currency, "currency symbol (only for a state file that has no saved rate)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stack-overflow stats [-state path] [-chips-per-unit n] [-currency symbol]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return usageCode(err)
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return exitUsage
	}
	migrateDefaults(stderr, o.state == defaultState, false)
	path, err := expandHome(o.state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !exists(path) {
		fmt.Fprintf(stderr, "There is no state file at %s yet. Start the server and play a hand first.\n", path)
		return 1
	}
	stats, settle, err := server.ReadStats(o.roomConfig(path))
	if err != nil {
		fmt.Fprintf(stderr, "Cannot read %s: %v\n", path, err)
		return 1
	}
	writeStats(stdout, path, stats, settle)
	return 0
}

// runMerge is "stack-overflow merge FROM TO". The server must be stopped.
func runMerge(args []string, stdout, stderr io.Writer) int {
	o := defaultOptions()
	fs := flag.NewFlagSet("stack-overflow merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.state, "state", defaultState, "state file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `Usage: stack-overflow merge [-state path] "FROM" "TO"`)
		fmt.Fprintln(stderr, "Moves everything of player FROM (name or PIN login) into player TO, then deletes FROM.")
		fmt.Fprintln(stderr, `Write "name:<display name>" or "login:<PIN login>" to select one kind of lookup.`)
		fmt.Fprintln(stderr, "Stop the server first.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return usageCode(err)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return exitUsage
	}
	from, to := strings.TrimSpace(fs.Arg(0)), strings.TrimSpace(fs.Arg(1))
	if from == "" || to == "" {
		fs.Usage()
		return exitUsage
	}
	if strings.EqualFold(from, to) {
		fmt.Fprintln(stderr, "FROM and TO are the same player. Give two different players.")
		return exitUsage
	}
	migrateDefaults(stderr, o.state == defaultState, false)
	path, err := expandHome(o.state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !exists(path) {
		fmt.Fprintf(stderr, "There is no state file at %s.\n", path)
		return 1
	}
	err = server.MergeAccounts(o.roomConfig(path), from, to)
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "Merged %q into %q. The player %q does not exist now.\n", from, to, from)
		return 0
	case errors.Is(err, server.ErrLocked):
		fmt.Fprintf(stderr, "The server is running and uses %s.\n"+
			"Stop the server (press Ctrl+C in its window), then do the merge again.\n", path)
	case errors.Is(err, server.ErrNoAccount):
		// The server error names the player it cannot find: "<ErrNoAccount>: \"bob\"".
		who := fmt.Sprintf("%q or %q", from, to)
		if name, ok := strings.CutPrefix(err.Error(), server.ErrNoAccount.Error()+": "); ok && name != "" {
			who = name
		}
		fmt.Fprintf(stderr, "Cannot find the player %s. Use \"stack-overflow stats\" to see the names.\n", who)
	default:
		fmt.Fprintf(stderr, "Cannot merge: %v\n", err)
	}
	return 1
}

// runAccounts is "stack-overflow accounts": it lists every player with the
// PIN login and the key fingerprints, so that the host can write -admin.
// The server must be stopped.
func runAccounts(args []string, stdout, stderr io.Writer) int {
	o := defaultOptions()
	fs := flag.NewFlagSet("stack-overflow accounts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.state, "state", defaultState, "state file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stack-overflow accounts [-state path]")
		fmt.Fprintln(stderr, "Lists each player with the PIN login and the SSH key fingerprints.")
		fmt.Fprintln(stderr, `Copy a value into -admin: "SHA256:<fingerprint>" or "pin:<login>".`)
		fmt.Fprintln(stderr, "Stop the server first.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return usageCode(err)
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return exitUsage
	}
	migrateDefaults(stderr, o.state == defaultState, false)
	path, err := expandHome(o.state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !exists(path) {
		fmt.Fprintf(stderr, "There is no state file at %s yet. Start the server and let the players log in first.\n", path)
		return 1
	}
	accounts, err := server.ListAccounts(o.roomConfig(path))
	switch {
	case errors.Is(err, server.ErrLocked):
		fmt.Fprintf(stderr, "The server is running and uses %s.\n"+
			"Stop the server (press Ctrl+C in its window), then run this command again.\n"+
			"Or ask the player for the key fingerprint: \"ssh-keygen -lf ~/.ssh/id_ed25519.pub\" prints it.\n", path)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "Cannot read %s: %v\n", path, err)
		return 1
	}
	writeAccounts(stdout, path, accounts)
	return 0
}

// runUnlink is "stack-overflow unlink SHA256:...": it removes an SSH key
// from the player that holds it, for example a key that somebody linked to
// another player's PIN profile. The server must be stopped.
func runUnlink(args []string, stdout, stderr io.Writer) int {
	o := defaultOptions()
	fs := flag.NewFlagSet("stack-overflow unlink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.state, "state", defaultState, "state file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stack-overflow unlink [-state path] SHA256:<fingerprint>")
		fmt.Fprintln(stderr, "Removes the SSH key from the player that has it. That key then logs in as a new player.")
		fmt.Fprintln(stderr, `"stack-overflow accounts" lists the keys of each player. Stop the server first.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return usageCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}
	migrateDefaults(stderr, o.state == defaultState, false)
	path, err := expandHome(o.state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !exists(path) {
		fmt.Fprintf(stderr, "There is no state file at %s.\n", path)
		return 1
	}
	name, err := server.UnlinkKey(o.roomConfig(path), strings.TrimSpace(fs.Arg(0)))
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "Removed the key %s from %q. A backup of the old file is next to %s.\n", fs.Arg(0), name, path)
		return 0
	case errors.Is(err, server.ErrLocked):
		fmt.Fprintf(stderr, "The server is running and uses %s.\n"+
			"Stop the server (press Ctrl+C in its window), then run this command again.\n", path)
	default:
		fmt.Fprintf(stderr, "Cannot remove the key: %v\n", err)
	}
	return 1
}

// runReset is "stack-overflow reset": it moves the state file away, so
// that the next start has no players, banks or stats. The host key stays,
// so the players get no host key warning. The server must be stopped: a
// running server writes all banks back at its next save.
func runReset(args []string, stdout, stderr io.Writer) int {
	o := defaultOptions()
	yes := false
	flags := flag.NewFlagSet("stack-overflow reset", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.state, "state", defaultState, "state file")
	flags.BoolVar(&yes, "yes", false, "do the reset (without it, the command only tells what it does)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stack-overflow reset [-state path] -yes")
		fmt.Fprintln(stderr, "Moves the state file to <state>.<time>.reset. The next start has no players, banks or stats.")
		fmt.Fprintln(stderr, "The host key stays, so the players get no warning. Stop the server first.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return usageCode(err)
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return exitUsage
	}
	path, err := expandHome(o.state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !yes {
		fmt.Fprintf(stderr, "This moves %s to %s.<time>.reset.\n"+
			"The next start has no players, banks or stats. Nothing changed now.\n"+
			"Stop the server, then run \"stack-overflow reset -yes\".\n", path, path)
		return 1
	}
	dst, err := server.ResetState(o.roomConfig(path))
	switch {
	case errors.Is(err, server.ErrLocked):
		// The lock comes first: a running server writes the state file
		// again at its next save, also when somebody deleted it.
		fmt.Fprintf(stderr, "The server is running and uses %s.\n"+
			"Stop the server first (press Ctrl+C in its window). A running server writes all banks back.\n", path)
		return 1
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stderr, "There is no state file at %s. There is nothing to reset.\n", path)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "Cannot reset: %v\n", err)
		return 1
	}
	if o.state == defaultState {
		if err := markLegacyBank(); err != nil {
			fmt.Fprintf(stderr, "  Problem with the old files in %s: %v\n", legacyDir, err)
		}
	}
	fmt.Fprintf(stdout, "Moved %s to %s.\n"+
		"The next start has no players, banks or stats. The host key stays.\n"+
		"To undo, stop the server and move the file back.\n", path, dst)
	return 0
}

// writeAccounts prints one line for each credential of each player. A
// login without a PIN is not a credential: the room ignores a "pin:" entry
// for it.
func writeAccounts(w io.Writer, path string, accounts []server.AccountInfo) {
	fmt.Fprintf(w, "%s · accounts · %s\n\n", appName, path)
	if len(accounts) == 0 {
		fmt.Fprintln(w, "No players yet.")
		return
	}
	t := textTable{head: []string{"Name", "Credential for -admin"}}
	for _, a := range accounts {
		var creds []string
		if a.Login != "" && a.HasPIN {
			creds = append(creds, "pin:"+a.Login)
		}
		creds = append(creds, a.Fingerprints...)
		if len(creds) == 0 {
			creds = []string{"—"}
		}
		for i, c := range creds {
			name := a.Name
			if i > 0 {
				name = ""
			}
			t.add(name, c)
		}
	}
	t.write(w, "  ")
	fmt.Fprintln(w, "\nExample: stack-overflow -admin \"pin:alice,SHA256:...\"")
	fmt.Fprintln(w, "A pin: entry counts only for a PIN profile that exists when the server starts.")
	fmt.Fprintln(w, "To remove a key that a player does not know: stack-overflow unlink SHA256:...")
}

// usageCode returns the exit code for a flag parse error.
func usageCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return exitUsage
}

// writeStats prints the stats table and the settlement.
func writeStats(w io.Writer, path string, stats []server.PlayerStats, st server.Settlement) {
	cur := st.Currency
	cpu := max(st.ChipsPerUnit, 1)
	fmt.Fprintf(w, "%s · stats · %s\n", appName, path)
	if st.Period > 0 {
		line := fmt.Sprintf("Period %d", st.Period)
		if !st.Since.IsZero() {
			line += " since " + st.Since.Local().Format(dateTime)
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)

	if len(stats) == 0 {
		fmt.Fprintln(w, "  No hands yet.")
	} else {
		money := map[string]server.Money{}
		for _, b := range st.Balances {
			money[b.Name] = b.Money
		}
		t := &textTable{
			head:  []string{"Player", "Net", "Money", "Hands", "Won", "VPIP", "PFR", "WTSD", "W$SD", "Biggest pot", "Best hand", "All-ins", "Buy-ins", "Lifetime"},
			right: []bool{false, true, true, true, true, true, true, true, true, true, false, true, true, true},
		}
		for _, p := range stats {
			m, ok := money[p.Name]
			if !ok {
				m = chipsToMoney(p.PeriodNet, cpu)
			}
			best := p.BestHand
			if best == "" {
				best = "—"
			}
			t.add(p.Name, signedChips(p.PeriodNet), signedMoney(m, cur),
				strconv.Itoa(p.Hands), strconv.Itoa(p.HandsWon),
				percent(p.VPIP, p.Hands), percent(p.PFR, p.Hands),
				percent(p.Showdowns, p.SawFlop), percent(p.ShowdownsWon, p.Showdowns),
				formatChips(p.BiggestPot), best,
				strconv.Itoa(p.AllIns), strconv.Itoa(p.BuyIns), signedChips(p.LifetimeNet))
		}
		t.write(w, "  ")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Net and Money: this period. The other numbers: all periods.")
		fmt.Fprintln(w, "  VPIP: hands with chips put in by choice before the flop. PFR: hands with a raise before the flop.")
		fmt.Fprintln(w, "  WTSD: showdowns / flops seen. W$SD: showdowns won / showdowns.")
		fmt.Fprintln(w)
		writeFunStats(w, stats)
		fmt.Fprintln(w)
		writeHostStats(w, stats, cur, cpu)
	}
	fmt.Fprintln(w)
	writeSettlement(w, st, cpu)
}

// dateTime is the date format of the host commands.
const dateTime = "2 Jan 2006 15:04"

func writeSettlement(w io.Writer, st server.Settlement, cpu int64) {
	cur := st.Currency
	fmt.Fprintf(w, "Settle up · %d chips = %s\n", cpu, formatMoney(100, cur))
	if len(st.Balances) == 0 {
		fmt.Fprintln(w, "  Everybody is even. Nobody pays.")
	} else {
		t := &textTable{right: []bool{false, true, false}}
		for _, b := range st.Balances {
			what := "receives " + formatMoney(b.Money, cur)
			if b.Money < 0 {
				what = "pays " + formatMoney(-b.Money, cur)
			} else if b.Money == 0 {
				what = "is even"
			}
			t.add(b.Name, signedChips(b.Chips)+" chips", what)
		}
		t.write(w, "  ")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Transfers")
		if len(st.Transfers) == 0 {
			fmt.Fprintln(w, "  None.")
		}
		for _, tr := range st.Transfers {
			fmt.Fprintf(w, "  %s pays %s %s\n", tr.From, tr.To, formatMoney(tr.Amount, cur))
		}
	}
	if len(st.History) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "History")
		for _, p := range st.History {
			line := fmt.Sprintf("  Period %d", p.Period)
			if !p.Since.IsZero() {
				line += " · " + p.Since.Local().Format(dateTime)
			}
			if !p.SettledAt.IsZero() {
				line += " to " + p.SettledAt.Local().Format(dateTime)
			}
			fmt.Fprintln(w, line)
			if len(p.Transfers) == 0 {
				fmt.Fprintln(w, "    Nobody paid.")
			}
			c := p.Currency
			if c == "" {
				c = cur
			}
			for _, tr := range p.Transfers {
				fmt.Fprintf(w, "    %s paid %s %s\n", tr.From, tr.To, formatMoney(tr.Amount, c))
			}
		}
	}
}

// textTable prints aligned columns. It measures cells in terminal cells,
// so wide runes keep the columns aligned.
type textTable struct {
	head  []string
	right []bool // right-align the column
	rows  [][]string
}

func (t *textTable) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *textTable) write(w io.Writer, indent string) {
	rows := t.rows
	if t.head != nil {
		rows = append([][]string{t.head}, rows...)
	}
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(indent)
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-ansi.StringWidth(c))
			if i < len(t.right) && t.right[i] {
				b.WriteString(pad + c)
			} else {
				b.WriteString(c + pad)
			}
			if i < len(r)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

// percent returns n/d as a whole percentage, or "—" when d is 0.
func percent(n, d int) string {
	if d <= 0 {
		return "—"
	}
	return strconv.Itoa((n*100+d/2)/d) + "%"
}

// chipsToMoney converts chips to hundredths of the currency unit, rounded
// half away from zero.
func chipsToMoney(chips, chipsPerUnit int64) server.Money {
	cpu := max(chipsPerUnit, 1)
	neg := chips < 0
	if neg {
		chips = -chips
	}
	m := (chips*100*2 + cpu) / (2 * cpu)
	if neg {
		m = -m
	}
	return server.Money(m)
}

// formatChips writes n with plain thousands separators: 12,345.
func formatChips(n int64) string {
	if n < 0 {
		return "-" + groupDigits(strconv.FormatInt(-n, 10), false)
	}
	return groupDigits(strconv.FormatInt(n, 10), false)
}

// signedChips is formatChips with a "+" for amounts above zero.
func signedChips(n int64) string {
	if n > 0 {
		return "+" + formatChips(n)
	}
	return formatChips(n)
}

// formatMoney writes m with the currency symbol, for example ₹1,23,456.50
// or $1,234. It drops ".00" when the amount is whole. The rupee uses Indian
// grouping (lakh, crore); other currencies use groups of three.
func formatMoney(m server.Money, currency string) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	units := groupDigits(strconv.FormatInt(int64(m)/100, 10), indianGrouping(currency))
	s := sign + currency + units
	if cents := int64(m) % 100; cents != 0 {
		s += fmt.Sprintf(".%02d", cents)
	}
	return s
}

// signedMoney is formatMoney with a "+" for amounts above zero.
func signedMoney(m server.Money, currency string) string {
	if m > 0 {
		return "+" + formatMoney(m, currency)
	}
	return formatMoney(m, currency)
}

func indianGrouping(currency string) bool {
	switch strings.ToLower(strings.TrimSpace(currency)) {
	case "₹", "rs", "rs.", "inr":
		return true
	}
	return false
}

// groupDigits puts commas into a string of digits: groups of three, or the
// Indian groups (last three, then twos) when indian is true.
func groupDigits(d string, indian bool) string {
	if len(d) <= 3 {
		return d
	}
	head, tail := d[:len(d)-3], d[len(d)-3:]
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
	return strings.Join(parts, ",") + "," + tail
}
