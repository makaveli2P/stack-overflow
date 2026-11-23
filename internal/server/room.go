package server

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

const (
	maxLogLines  = 200
	maxChatLines = 100
)

// Room is one poker room. Only the Run goroutine reads or writes the fields
// below the "owned by Run" mark. Other goroutines send commands.
type Room struct {
	cfg    Config
	now    func() time.Time
	ticker func() (<-chan time.Time, func())

	cmds    chan command
	done    chan struct{} // closed when Run returns
	running atomic.Bool

	lock    *os.File // flock on StatePath + ".lock"; released when Run returns
	pinCost int      // bcrypt cost of PIN hashes
	admins  adminSet // the parsed Config.Admins
	// startCfg is the config as NewRoom set it up, with the money rate of
	// the open period (see Room.Config). It does not change.
	startCfg Config

	// saveErr is the result of the last save (see noteSave).
	saveMu  sync.Mutex
	saveErr error

	// Owned by Run. A "key" below is an account id; the engine uses it as
	// the player id.
	table      *engine.Table
	st         *state                 // accounts, periods, history
	sessions   map[string]*Session    // by key; one live session for each key
	away       map[string]bool        // seated keys whose session closed during a hand
	leftInHand map[string]int64       // chips in the live hand of keys that stood up during it
	pins       map[string]*pinGuard   // wrong-PIN counts by login and address
	pinTotals  map[string]*pinGuard   // wrong-PIN counts by login, from all addresses
	newKeys    map[string][]time.Time // new key accounts by remote address, last hour
	// flagCPU and flagCurrency are the money rate of the flags. A new
	// period takes them; the open period can keep a saved rate.
	flagCPU      int64
	flagCurrency string
	turnFor      string // key whose turn it is; "" when nobody must act
	turnSeq      uint64 // changes at each new turn
	log          ring[string]
	chat         ring[ChatLine]
	handLive     bool      // a hand ran at the last check
	clockFor     string    // key that the turn clock runs for; "" when none
	deadline     time.Time // turn clock end; zero when no clock runs
	// clockSeq changes at each new deadline: a new turn clock or a time
	// extension. The "hurry" alert fires once for each value.
	clockSeq uint64
	// turnExt is the number of time extensions used on the running clock.
	turnExt int
	// extUsed is the number of time extensions that each key used in this
	// server run. It is in memory only, so a restart gives the full count.
	extUsed    map[string]int
	nextHandAt time.Time // zero when no hand is scheduled
	// waiting is Snapshot.Waiting: ManualStart is on and nobody started the
	// game yet. startedBy is Snapshot.StartedBy.
	waiting   bool
	startedBy string
	// runoutAt is the time of the next street of a paused all-in run-out
	// (Config.RunoutDelay). It is zero when no run-out waits.
	runoutAt time.Time
	// holes are the hole cards of the last hand dealt, by key, as each
	// player saw them at the deal. holesHand is the number of that hand.
	// They stay after the hand ends, until the next deal, for the
	// "you would have had" line of a rabbit hunt (Me.RabbitHand). Only
	// the owner's favourite hand and the owner's own snapshots use them.
	holes     map[string][]cards.Card
	holesHand int
	// rabbitUsed is the number of rabbit hunts that each key did in this
	// server run (batch 10). It is in memory only, like extUsed.
	rabbitUsed map[string]int
	// seatedAt is the time up to which SeatedSeconds counts each key that
	// is seated and not sitting out.
	seatedAt map[string]time.Time
	// panicSeq is Snapshot.PanicSeq: the number of panics in this server
	// run. panicAt is the time of the last panic of each key, for the
	// PanicCooldown. Both are in memory only.
	panicSeq uint64
	panicAt  map[string]time.Time
	// showAtEnd holds the show choices of folded players for the end of the
	// live hand, by key: [left, right] (batch 9, see Session.ShowCards).
	// showHold is true when shows at the end of a hand need the reveal
	// hold on the next-hand timer.
	showAtEnd map[string][2]bool
	showHold  bool
	// waitingShots are the screenshot links that wait for the end of the
	// live hand, in the order of the presses (batch 15, see
	// Session.ShareShot). They are in memory only: a server stop drops
	// them.
	waitingShots []waitingShot
}

// Session is one connected client.
type Session struct {
	room    *Room
	ident   string // the identity passed to Connect (Me.Key)
	addr    string // ConnectOptions.Addr: the remote address, for the host log
	keyless bool   // a PIN login
	fp      string // the key fingerprint of a key login
	login   string // the PIN login of a keyless login ("pin:"); "" else
	admin   bool   // ConnectOptions.Admin
	host    bool   // ConnectOptions.Host: sees the host stats; also an admin
	ch      chan Snapshot
	closed  atomic.Bool

	// Owned by Run. key (the account id) changes when LinkPIN switches the
	// account; name changes with SetName.
	key        string
	name       string
	chatLimit  rateLimiter
	turnAlert  uint64 // turnSeq of the last "turn" cue
	hurryAlert uint64 // Room.clockSeq of the last "hurry" cue
}

// command is one call that the Run goroutine executes.
type command struct {
	fn    func() error
	reply chan error
}

// options are test seams. The zero value gives production behaviour.
type options struct {
	now     func() time.Time
	ticker  func() (<-chan time.Time, func())
	newDeck func() *cards.Deck
	pinCost int // 0 = defaultPINCost
}

const defaultPINCost = 10

func newRoom(cfg Config) (*Room, error) { return newRoomWith(cfg, options{}) }

func newRoomWith(cfg Config, opt options) (r *Room, err error) {
	if cfg.StartingStack <= 0 || cfg.TurnTimeout < 0 || cfg.NextHandDelay < 0 || cfg.RunoutDelay < 0 {
		return nil, fmt.Errorf("%w: starting stack must be more than zero and durations must not be negative", engine.ErrBadConfig)
	}
	if cfg.Extensions < 0 || (cfg.Extensions > 0 && cfg.ExtensionTime <= 0) {
		return nil, fmt.Errorf("%w: time extensions must not be negative, and the extension time must be more than zero", engine.ErrBadConfig)
	}
	if cfg.RabbitHunts < 0 {
		return nil, fmt.Errorf("%w: rabbit hunts must not be negative", engine.ErrBadConfig)
	}
	admins, err := parseAdmins(cfg.Admins)
	if err != nil {
		return nil, err
	}
	if opt.now == nil {
		opt.now = time.Now
	}
	if opt.ticker == nil {
		opt.ticker = secondTicker
	}
	if opt.pinCost == 0 {
		opt.pinCost = defaultPINCost
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			unlockState(lock)
		}
	}()
	st, migrated, err := loadState(cfg.StatePath, opt.now())
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := backupOld(cfg.StatePath); err != nil {
			return nil, fmt.Errorf("back up the old chip bank: %w", err)
		}
	}
	// A copy at each start lets the host undo a bad night. A failure does
	// not stop the room: the host log shows it, and a full disk also makes
	// the saves fail, which the host log shows too.
	if err := backupState(cfg.StatePath, opt.now(), "start"); err != nil {
		hostLog(log.WarnLevel, "could not back up the state file", "err", err)
	}
	pruned := st.pruneIdle()
	admins.resolvePINs(st)
	flagCPU, flagCurrency := max(cfg.ChipsPerUnit, 1), cfg.Currency
	cpu, currency, kept := st.pickRate(cfg)
	if kept {
		hostLog(log.WarnLevel, "the open period keeps the money rate of the state file; the flags apply from the next period",
			"period", st.Period, "chips_per_unit", cpu, "currency", currency,
			"flag_chips_per_unit", flagCPU, "flag_currency", flagCurrency)
	}
	cfg.ChipsPerUnit, cfg.Currency = cpu, currency
	table, err := engine.NewTable(engine.Config{
		Seats:      cfg.Seats,
		SmallBlind: cfg.SmallBlind,
		BigBlind:   cfg.BigBlind,
		FirstHand:  st.HandNumber + 1,
		// A run-out delay makes the engine stop before each street of an
		// all-in run-out; the room deals the streets (see dealRunout).
		StepRunout: cfg.RunoutDelay > 0,
	}, opt.newDeck)
	if err != nil {
		return nil, err
	}
	r = &Room{
		cfg:          cfg,
		startCfg:     cfg,
		waiting:      cfg.ManualStart,
		now:          opt.now,
		ticker:       opt.ticker,
		cmds:         make(chan command),
		done:         make(chan struct{}),
		lock:         lock,
		pinCost:      opt.pinCost,
		admins:       admins,
		table:        table,
		st:           st,
		sessions:     make(map[string]*Session),
		away:         make(map[string]bool),
		leftInHand:   make(map[string]int64),
		pins:         make(map[string]*pinGuard),
		pinTotals:    make(map[string]*pinGuard),
		newKeys:      make(map[string][]time.Time),
		flagCPU:      flagCPU,
		flagCurrency: flagCurrency,
		holes:        make(map[string][]cards.Card),
		seatedAt:     make(map[string]time.Time),
		extUsed:      make(map[string]int),
		rabbitUsed:   make(map[string]int),
		panicAt:      make(map[string]time.Time),
		showAtEnd:    make(map[string][2]bool),
		log:          ring[string]{max: maxLogLines},
		chat:         ring[ChatLine]{max: maxChatLines},
	}
	r.startCfg.Admins = slices.Clone(cfg.Admins)
	if migrated || pruned > 0 {
		r.save()
	}
	return r, nil
}

func secondTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(time.Second)
	return t.C, t.Stop
}

func (r *Room) run(ctx context.Context) {
	if !r.running.CompareAndSwap(false, true) {
		return // Run already ran or runs now.
	}
	defer func() {
		if p := recover(); p != nil {
			// A command stopped half-way, so the state in memory can be
			// wrong. Do not save it: the last save stays on disk, and a
			// restart refunds the live hand from it.
			r.noteSave(fmt.Errorf("the room stopped on an internal error; the last save stays: %v", p))
			unlockState(r.lock)
			close(r.done)
			panic(p)
		}
		r.shutdown()
	}()
	tick, stop := r.ticker()
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-r.cmds:
			c.reply <- c.fn()
		case <-tick:
			r.tick()
		}
	}
}

// shutdown saves the bank, closes every session, releases the state lock
// and releases waiting callers.
func (r *Room) shutdown() {
	r.save()
	for _, s := range r.sessions {
		r.dropSession(s)
	}
	unlockState(r.lock)
	close(r.done)
}

// do runs fn on the Run goroutine and returns its error. It returns
// ErrRoomClosed when Run has returned.
func (r *Room) do(fn func() error) error {
	c := command{fn: fn, reply: make(chan error, 1)}
	select {
	case r.cmds <- c:
		select {
		case err := <-c.reply:
			return err
		case <-r.done:
			// Run sends the reply before it closes done, so a reply that
			// exists is here now. No reply means that the command stopped
			// the room (see run).
			select {
			case err := <-c.reply:
				return err
			default:
				return ErrRoomClosed
			}
		}
	case <-r.done:
		return ErrRoomClosed
	}
}

func (r *Room) logf(format string, args ...any) {
	r.log.add(fmt.Sprintf(format, args...))
}

// account returns the account for key and makes it when it is missing.
func (r *Room) account(key, name string) *account {
	a := r.st.Accounts[key]
	if a == nil {
		a = &account{Name: name, Named: true, Created: r.now()}
		r.st.Accounts[key] = a
	}
	return a
}

func (r *Room) seated(key string) bool {
	_, ok := r.table.Stack(key)
	return ok
}

// save writes the bank to StatePath. The game continues when the save
// fails; noteSave tells the host and keeps the error for SaveErr.
func (r *Room) save() {
	r.accrueSeated()
	if r.cfg.StatePath == "" {
		return
	}
	r.noteSave(saveState(r.cfg.StatePath, r.diskView()))
}

// tableChips returns the chips of each seated key: the stack, plus the chips
// that the player put in during the live hand. It also returns the chips that
// a key put in during the live hand before it stood up (leftInHand), because
// those chips stay in the pot. The live hand thus counts as not played yet,
// so a restart can refund it and the leaderboard does not move with each bet.
func (r *Room) tableChips() map[string]int64 {
	onTable := make(map[string]int64)
	view := r.table.View("")
	if view.InHand {
		for key, chips := range r.leftInHand {
			onTable[key] += chips
		}
	}
	for _, sv := range view.Seats {
		if sv.Empty {
			continue
		}
		chips := sv.Stack
		if view.InHand && sv.InHand {
			chips += sv.Committed
		}
		onTable[sv.PlayerID] += chips
	}
	return onTable
}

// diskView returns the state as it must be on disk: chips on the table (see
// tableChips) count as bank, and the hand counter is the last hand dealt.
func (r *Room) diskView() *state {
	onTable := r.tableChips()
	out := *r.st
	if hn := r.table.View("").HandNumber; hn > out.HandNumber {
		r.st.HandNumber = hn
		out.HandNumber = hn
	}
	out.Accounts = make(map[string]*account, len(r.st.Accounts))
	for key, a := range r.st.Accounts {
		if a.idle() && onTable[key] == 0 {
			continue // see account.idle
		}
		saved := *a
		saved.Bank += onTable[key]
		out.Accounts[key] = &saved
	}
	return &out
}

// ring keeps the newest max items.
type ring[T any] struct {
	buf []T
	max int
}

func (r *ring[T]) add(v T) {
	r.buf = append(r.buf, v)
	if len(r.buf) >= 2*r.max {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.max:]...)
	}
}

// items returns a copy of the newest max items, oldest first.
func (r *ring[T]) items() []T {
	start := max(0, len(r.buf)-r.max)
	return append([]T(nil), r.buf[start:]...)
}
