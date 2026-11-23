// Command stack-overflow runs the Stack Overflow SSH poker server.
// Players join with "ssh -p 2222 <name>@<host>"; nobody else installs
// anything.
//
// Subcommands for the host:
//
//	stack-overflow stats [-state path]          print the stats and the settlement
//	stack-overflow accounts [-state path]       list the players with their credentials (server stopped)
//	stack-overflow merge [-state path] FROM TO  merge two players (server stopped)
//	stack-overflow unlink [-state path] SHA256:...  remove an SSH key from its player (server stopped)
//	stack-overflow -version                     print the version
//
// Authentication:
//
//   - A player whose SSH client offers a key logs in with the key. The
//     identity is "key:<fingerprint>".
//   - A player without a key logs in with a 4-digit PIN for the SSH user
//     name (keyboard-interactive; the password method also works for a
//     player that exists). The first login chooses the PIN. The identity is
//     "pin:<login>".
//   - With -password (or STACK_OVERFLOW_PASSWORD), every player also types
//     the shared password.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// Default paths. "~/" means the home directory.
const (
	defaultDir     = "~/.stack-overflow"
	defaultState   = defaultDir + "/state.json"
	defaultHostKey = defaultDir + "/host_ed25519"
	// legacyDir is the folder of the old name of the game.
	legacyDir = "~/.office-poker"
)

// passwordEnv is the environment variable for the shared password. The
// -password flag wins over it.
const passwordEnv = "STACK_OVERFLOW_PASSWORD"

type options struct {
	host, state, hostKey, password string
	port, seats                    int
	sb, bb, stack                  int64
	timeout, nextHand              time.Duration
	runoutDelay                    time.Duration // pause before each street of an all-in run-out
	extensions                     int           // time extensions for each player in one server run
	extensionTime                  time.Duration // the time that one extension adds
	rabbitHunts                    int           // rabbit hunts for each player in one server run; 0 is no limit
	chipsPerUnit                   int64
	currency                       string
	admins                         []string
	version                        bool // -version: print the version and stop
	// autostart deals the first hand as soon as 2 players can play. Without
	// it, the room waits until a seated player presses G.
	autostart bool
	// shotPort is the HTTP port of the screenshot links (key P); 0 turns
	// screenshots off.
	shotPort int
	// shots makes the screenshot links. run sets it when the screenshot
	// server runs; nil means screenshots are off.
	shots *shotLinks
	// loginGrace is the time from the TCP connect to the session start.
	// Zero means defaultLoginGrace. Tests set it; there is no flag.
	loginGrace time.Duration
	// Connection limits. Zero means the default. Tests set them; there
	// are no flags.
	maxConns, maxConnsPerIP int
	// keepAlive is the time between two keepalive probes, and
	// keepAliveWait is the time that a client has to answer one. Zero
	// means the default. Tests set them; there are no flags.
	keepAlive, keepAliveWait time.Duration
}

// parseServerFlags reads the server flags from args. getenv reads the
// environment.
func parseServerFlags(args []string, getenv func(string) string, errOut io.Writer) (options, error) {
	var o options
	var admins string
	fs := flag.NewFlagSet("stack-overflow", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&o.host, "host", "0.0.0.0", "address to listen on")
	fs.IntVar(&o.port, "port", 2222, "SSH port")
	fs.IntVar(&o.shotPort, "shot-port", defaultShotPort, "HTTP port for the screenshot links of the P key (0 turns screenshots off)")
	fs.IntVar(&o.seats, "seats", 9, "seats at the table (2-10)")
	fs.Int64Var(&o.sb, "sb", 5, "small blind")
	fs.Int64Var(&o.bb, "bb", 10, "big blind")
	fs.Int64Var(&o.stack, "stack", 1000, "chips for each buy-in")
	fs.DurationVar(&o.timeout, "timeout", 60*time.Second, "turn clock (0 turns it off)")
	fs.DurationVar(&o.nextHand, "next-hand", 20*time.Second, "pause between hands")
	fs.DurationVar(&o.runoutDelay, "runout-delay", 3*time.Second, "pause before each street of an all-in run-out (0 deals the board at once)")
	fs.IntVar(&o.extensions, "extensions", 3, "time extensions for each player in one server run (0 turns them off)")
	fs.DurationVar(&o.extensionTime, "extension-time", 30*time.Second, "time that one extension adds to the turn clock")
	fs.IntVar(&o.rabbitHunts, "rabbit-hunts", 3, "rabbit hunts for each player in one server run (0: no limit)")
	fs.StringVar(&o.state, "state", defaultState, "state file (players, banks, stats)")
	fs.StringVar(&o.hostKey, "hostkey", defaultHostKey, "SSH host key (made when missing)")
	fs.StringVar(&o.password, "password", "", "shared table password (empty: no password; also $"+passwordEnv+")")
	fs.Int64Var(&o.chipsPerUnit, "chips-per-unit", 5, "chips for one unit of money (5: 5 chips = ₹1)")
	fs.StringVar(&o.currency, "currency", "₹", "currency symbol for settle-up")
	fs.BoolVar(&o.version, "version", false, "print the version and stop")
	fs.BoolVar(&o.autostart, "autostart", false, "start hands as soon as 2 players sit (default: a seated player presses G)")
	fs.StringVar(&admins, "admin", "", "comma-separated credentials that can mark the period settled:\n"+
		"\"SHA256:<key fingerprint>\" or \"pin:<login>\" (see \"stack-overflow accounts\")")
	fs.Usage = func() {
		fmt.Fprintf(errOut, "Usage:\n"+
			"  stack-overflow [flags]                      run the server\n"+
			"  stack-overflow stats [-state path]          print the stats and the settlement\n"+
			"  stack-overflow accounts [-state path]       list the players with their key fingerprints and PIN logins\n"+
			"  stack-overflow merge [-state path] FROM TO  merge player FROM into player TO\n"+
			"      (FROM and TO: a name or a PIN login; \"name:<name>\" or \"login:<login>\" selects one)\n"+
			"  stack-overflow unlink [-state path] SHA256:...  remove an SSH key from its player\n"+
			"  stack-overflow reset [-state path] -yes     start fresh: move the state file away (stop the server first)\n"+
			"  stack-overflow -version                     print the version\n\n"+
			"Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return o, fmt.Errorf("unknown argument %q", fs.Arg(0))
	}
	if err := o.checkDurations(); err != nil {
		fmt.Fprintln(errOut, err)
		return o, err
	}
	if err := o.checkExtensions(); err != nil {
		fmt.Fprintln(errOut, err)
		return o, err
	}
	if err := o.checkTable(); err != nil {
		fmt.Fprintln(errOut, err)
		fmt.Fprintln(errOut, `Type "stack-overflow -h" to see the flags.`)
		return o, err
	}
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "password" })
	if !set {
		o.password = getenv(passwordEnv)
	}
	admList, err := parseAdmins(admins)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return o, err
	}
	o.admins = admList
	return o, nil
}

// parseAdmins reads the -admin list. Each entry is a credential:
// "SHA256:<key fingerprint>" or "pin:<login>". The error names the first
// bad entry.
func parseAdmins(s string) ([]string, error) {
	var out []string
	for _, e := range splitNames(s) {
		c, err := server.ParseAdmin(e)
		if err != nil {
			return nil, fmt.Errorf("-admin: %q is not a credential. -admin takes \"SHA256:<key fingerprint>\" "+
				"or \"pin:<login>\", not a display name. Run \"stack-overflow accounts\" to see them.", e)
		}
		out = append(out, c)
	}
	return out, nil
}

// checkDurations refuses negative clock and delay flags.
func (o options) checkDurations() error {
	for _, d := range []struct {
		flag string
		v    time.Duration
	}{{"-timeout", o.timeout}, {"-next-hand", o.nextHand}, {"-runout-delay", o.runoutDelay}} {
		if d.v < 0 {
			return fmt.Errorf("%s must not be negative (got %s)", d.flag, d.v)
		}
	}
	return nil
}

// checkExtensions refuses time extension and rabbit hunt flags that the
// room cannot use.
func (o options) checkExtensions() error {
	switch {
	case o.extensions < 0:
		return fmt.Errorf("-extensions must not be negative (got %d)", o.extensions)
	case o.extensionTime < 0:
		return fmt.Errorf("-extension-time must not be negative (got %s)", o.extensionTime)
	case o.extensions > 0 && o.extensionTime == 0:
		return errors.New("-extension-time must be more than 0 when -extensions is more than 0")
	case o.rabbitHunts < 0:
		return fmt.Errorf("-rabbit-hunts must not be negative (got %d)", o.rabbitHunts)
	}
	return nil
}

// checkTable refuses table, address and money flags that the server cannot
// use. It runs before the server touches a file or a port.
func (o options) checkTable() error {
	switch {
	case o.seats < 2 || o.seats > 10:
		return fmt.Errorf("-seats must be 2 to 10 (got %d)", o.seats)
	case o.sb <= 0:
		return fmt.Errorf("-sb must be more than 0 (got %d)", o.sb)
	case o.bb < o.sb:
		return fmt.Errorf("-bb must be the same as -sb or more (got -sb %d -bb %d)", o.sb, o.bb)
	case o.stack <= 0:
		return fmt.Errorf("-stack must be more than 0 (got %d)", o.stack)
	case o.stack < o.bb:
		return fmt.Errorf("-stack must be the same as -bb or more (got -bb %d -stack %d)", o.bb, o.stack)
	case o.port < 1 || o.port > 65535:
		return fmt.Errorf("-port must be 1 to 65535 (got %d)", o.port)
	case o.shotPort < 0 || o.shotPort > 65535:
		return fmt.Errorf("-shot-port must be 0 to 65535 (got %d)", o.shotPort)
	case o.shotPort == o.port:
		return fmt.Errorf("-shot-port must not be the same as -port (got %d)", o.shotPort)
	case o.chipsPerUnit < 1:
		return fmt.Errorf("-chips-per-unit must be 1 or more (got %d)", o.chipsPerUnit)
	case o.host != "localhost" && !validIP(o.host):
		return fmt.Errorf("-host must be an IP address of this computer, 0.0.0.0 or localhost (got %q)", o.host)
	}
	return nil
}

// validIP reports whether host is an IP address, also an IPv6 address with
// a zone ("fe80::1%en0").
func validIP(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

// splitNames splits a comma-separated list and drops empty names.
func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func main() {
	os.Exit(realMain(os.Args[1:]))
}

// exitUsage is the exit code for bad flags (EX_USAGE of sysexits.h). It
// is not 2, because the Go runtime exits with 2 after a panic or a fatal
// error. scripts/serve.sh must start the server again after a crash, but
// not after bad flags.
const exitUsage = 64

func realMain(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "stats":
			return runStats(args[1:], os.Stdout, os.Stderr)
		case "merge":
			return runMerge(args[1:], os.Stdout, os.Stderr)
		case "accounts":
			return runAccounts(args[1:], os.Stdout, os.Stderr)
		case "unlink":
			return runUnlink(args[1:], os.Stdout, os.Stderr)
		case "reset":
			return runReset(args[1:], os.Stdout, os.Stderr)
		}
	}
	o, err := parseServerFlags(args, os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return exitUsage
	}
	if o.version {
		fmt.Println(appName, buildVersion())
		return 0
	}
	// The output can go to a pipe (scripts/serve.sh). When the reader of
	// that pipe ends, a log line must fail, not kill the server before it
	// saves.
	signal.Ignore(syscall.SIGPIPE)
	if err := run(o); err != nil {
		log.Error(appName+" stopped", "err", err)
		if errors.Is(err, server.ErrBadAdmin) {
			return exitUsage
		}
		return 1
	}
	return 0
}

func (o options) roomConfig(statePath string) server.Config {
	return server.Config{
		Seats:         o.seats,
		SmallBlind:    o.sb,
		BigBlind:      o.bb,
		StartingStack: o.stack,
		TurnTimeout:   o.timeout,
		NextHandDelay: o.nextHand,
		RunoutDelay:   o.runoutDelay,
		StatePath:     statePath,
		ChipsPerUnit:  o.chipsPerUnit,
		Currency:      o.currency,
		Admins:        o.admins,
		Extensions:    o.extensions,
		ExtensionTime: o.extensionTime,
		ManualStart:   !o.autostart,
		RabbitHunts:   o.rabbitHunts,
	}
}

func run(o options) error {
	// Bind the port first: a port in use then stops the server before it
	// touches a file or prints the join command.
	ln, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.port)))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d: %w", o.port, err)
	}
	defer ln.Close()

	migrateDefaults(os.Stdout, o.state == defaultState, o.hostKey == defaultHostKey)
	statePath, err := prepare(o.state)
	if err != nil {
		return err
	}
	hostKey, err := prepare(o.hostKey)
	if err != nil {
		return err
	}

	// Read the account names for the banner before the room locks the
	// state file. An error only leaves the names out.
	var accounts []server.AccountInfo
	if len(o.admins) > 0 {
		accounts, _ = server.ListAccounts(o.roomConfig(statePath))
	}
	room, err := server.NewRoom(o.roomConfig(statePath))
	if errors.Is(err, server.ErrLocked) {
		return fmt.Errorf("another %s server uses %s; stop it first", appName, statePath)
	}
	if err != nil {
		return fmt.Errorf("make room: %w", err)
	}

	// The screenshot server is optional: when its port is in use, the
	// game runs without screenshots.
	shots, shotSrv := startShotServer(o, joinAddresses(o.host)[0])
	if shotSrv != nil {
		defer shotSrv.Close()
	}
	o.shots = shots

	srv, err := newSSHServer(o, hostKey, room)
	if err != nil {
		return fmt.Errorf("make SSH server: %w", err)
	}

	roomCtx, cancelRoom := context.WithCancel(context.Background())
	defer cancelRoom()
	roomDone := make(chan struct{})
	go func() {
		room.Run(roomCtx)
		close(roomDone)
	}()

	// SIGHUP comes when the host closes the terminal window. The room then
	// stops as it does for Ctrl+C: it saves and tells each player.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignals()

	if stop, err := keepAwake(); err != nil {
		log.Warn("cannot keep this Mac awake; it can sleep during the game", "err", err)
	} else {
		defer stop()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	// The open period can keep the money rate of the state file.
	rc := room.Config()
	if rc.ChipsPerUnit != max(o.chipsPerUnit, 1) || rc.Currency != o.currency {
		fmt.Fprintf(os.Stdout, "\n  Note: the open period keeps %d chips = %s from the state file.\n"+
			"  -chips-per-unit %d -currency %s apply from the next period.\n",
			rc.ChipsPerUnit, formatMoney(100, rc.Currency), max(o.chipsPerUnit, 1), o.currency)
		o.chipsPerUnit, o.currency = rc.ChipsPerUnit, rc.Currency
	}
	printWelcome(os.Stdout, o, statePath, accounts, room.IgnoredAdmins())

	select {
	case <-sigCtx.Done():
		log.Info("shutting down; press Ctrl+C again to force")
	case err = <-serveErr:
		if errors.Is(err, ssh.ErrServerClosed) {
			err = nil
		}
	}
	// A closed terminal window or tmux pane sends SIGHUP, often right
	// after the Ctrl+C. That SIGHUP must not stop the final save, so the
	// server catches it and does nothing with it. The catch stays until
	// the process exits.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP)
	stopSignals() // a second Ctrl+C or SIGTERM now kills the process at once
	if saveErr := shutdown(srv, shotSrv, room, cancelRoom, roomDone); err == nil {
		err = saveErr
	}
	return err
}

// shutdown stops new connections and the screenshot server (shotSrv can
// be nil), stops the room (which saves the state and ends every session),
// and then waits a short time for clients to leave. It returns the error
// of the final save.
func shutdown(srv *ssh.Server, shotSrv *http.Server, room *server.Room, cancelRoom context.CancelFunc, roomDone <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sshDone := make(chan error, 1)
	go func() { sshDone <- srv.Shutdown(ctx) }()
	shotDone := make(chan struct{})
	go func() {
		stopShotServer(shotSrv)
		close(shotDone)
	}()

	cancelRoom()
	<-roomDone
	saveErr := room.SaveErr()
	if saveErr != nil {
		log.Error("room stopped; state NOT saved", "err", saveErr)
		saveErr = fmt.Errorf("the state file is not saved: %w", saveErr)
	} else {
		log.Info("room stopped; state saved")
	}

	if err := <-sshDone; err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		_ = srv.Close()
	}
	<-shotDone
	return saveErr
}

// expandHome replaces a leading "~/" with the home directory.
func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, rest), nil
}

// prepare expands "~/" and makes the parent directory.
func prepare(path string) (string, error) {
	path, err := expandHome(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("make directory for %s: %w", path, err)
	}
	return path, nil
}

// printWelcome prints the startup banner. accounts gives the names of the
// admins; it can be nil. ignored lists the "pin:" admin entries that the
// room ignored (Room.IgnoredAdmins).
func printWelcome(w io.Writer, o options, statePath string, accounts []server.AccountInfo, ignored []string) {
	addr := net.JoinHostPort(o.host, strconv.Itoa(o.port))
	cpu := max(o.chipsPerUnit, 1)
	fmt.Fprintf(w, "\n  ♠ %s is listening on %s (version %s)\n", appName, addr, buildVersion())
	fmt.Fprintf(w, "    blinds %d/%d · buy-in %d chips (%s) · %d seats · turn clock %s\n",
		o.sb, o.bb, o.stack, formatMoney(chipsToMoney(o.stack, cpu), o.currency), o.seats, secondsText(o.timeout))
	fmt.Fprintf(w, "    %d chips = %s · state file %s\n", cpu, formatMoney(100, o.currency), statePath)
	if o.extensions > 0 && o.timeout > 0 {
		fmt.Fprintf(w, "    %d time extensions of %s for each player (key E)\n", o.extensions, o.extensionTime)
	}
	switch {
	case o.rabbitHunts == 1:
		fmt.Fprintln(w, "    1 rabbit hunt for each player (key H)")
	case o.rabbitHunts > 1:
		fmt.Fprintf(w, "    %d rabbit hunts for each player (key H)\n", o.rabbitHunts)
	}
	if o.autostart {
		fmt.Fprintln(w, "    Hands start as soon as 2 players sit")
	} else {
		fmt.Fprintln(w, "    Hands start when a seated player presses G")
	}
	if o.shots != nil {
		fmt.Fprintf(w, "    Screenshots: http://%s\n", urlHostPort(o.shots.lanHost, o.shots.port))
	} else {
		fmt.Fprintln(w, "    Screenshots: off")
	}
	if o.password != "" {
		fmt.Fprintln(w, "    a shared password is required")
	}
	if len(o.admins) > 0 {
		fmt.Fprintf(w, "    admins: %s (and anybody who connects from this machine)\n", adminList(o.admins, accounts, ignored))
		if len(ignored) > 0 {
			fmt.Fprintf(w, "    WARNING: no PIN profile has the login of %s, so it makes nobody an admin.\n"+
				"    The player must create the PIN profile first. Then restart the server.\n", strings.Join(ignored, ", "))
		}
	}
	fmt.Fprintln(w, "\n  Players can join with:")
	for _, ip := range joinAddresses(o.host) {
		fmt.Fprintf(w, "    ssh -p %d yourname@%s\n", o.port, ip)
	}
	fmt.Fprintln(w)
}

// adminList names the admins for the banner by credential. A key admin
// shows as the account name and a short fingerprint, and a PIN admin as
// the account name and the login, when the account exists. A "pin:" entry
// in ignored shows as "pin:<login> (ignored)".
func adminList(admins []string, accounts []server.AccountInfo, ignored []string) string {
	parts := make([]string, 0, len(admins))
	for _, c := range admins {
		if canon, err := server.ParseAdmin(c); err == nil && slices.Contains(ignored, canon) {
			parts = append(parts, canon+" (ignored)")
			continue
		}
		name := ""
		for _, a := range accounts {
			if login, ok := strings.CutPrefix(c, "pin:"); ok && a.Login == login || slices.Contains(a.Fingerprints, c) {
				name = a.Name
				break
			}
		}
		switch {
		case name == "":
			parts = append(parts, c)
		case strings.HasPrefix(c, "pin:"):
			parts = append(parts, fmt.Sprintf("%s (%s)", name, c))
		default:
			parts = append(parts, fmt.Sprintf("%s (%s)", name, shortFP(c)))
		}
	}
	return strings.Join(parts, ", ")
}

// shortFP cuts a "SHA256:..." fingerprint to its first 8 characters.
func shortFP(fp string) string {
	const keep = len("SHA256:") + 8
	if len(fp) <= keep {
		return fp
	}
	return fp[:keep] + "…"
}

// secondsText formats d for the banner: "60s" for whole seconds (not
// "1m0s"), else the Go form, e.g. "1.5s".
func secondsText(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return d.String()
}

// joinAddresses lists the addresses that colleagues can use: the -host
// address when it is specific, else every LAN IPv4 address of this machine.
// With no such address, it gives one placeholder for the banner.
func joinAddresses(host string) []string {
	out, err := lanAddresses(host)
	if err != nil {
		return []string{"<this-machine's-ip>"}
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	return out
}

// lanAddresses is joinAddresses without the placeholders: it returns no
// address when the machine has no LAN IPv4 address.
func lanAddresses(host string) ([]string, error) {
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{host}, nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out, nil
}

// officeHost is the office-network address of the banner, found again
// now, or "" when the machine has no LAN address.
func officeHost(host string) string {
	if a, err := lanAddresses(host); err == nil && len(a) > 0 {
		return a[0]
	}
	return ""
}
