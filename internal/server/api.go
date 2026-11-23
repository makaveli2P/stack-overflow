// Package server runs one poker room: one table, its clock, the chip bank
// and all connected sessions. One goroutine (Room.Run) owns all state.
// Session methods send a command to that goroutine and wait for the reply.
//
// CONTRACT FILE: the exported names and signatures in this file are fixed.
// The tui and cmd packages depend on them. Implement the bodies in other
// files; do not change the API. See DESIGN.md.
package server

import (
	"context"
	"errors"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
	"github.com/makaveli2p/stack-overflow/internal/engine"
)

var (
	ErrClosed     = errors.New("session is closed")
	ErrTableFull  = errors.New("table is full")
	ErrNotBusted  = errors.New("you can rebuy only when your stack is zero")
	ErrEmptyChat  = errors.New("empty message")
	ErrNameInUse  = errors.New("name is in use by another player")
	ErrRoomClosed = errors.New("room is closed")
	ErrBadName    = errors.New("name must be 1 to 12 letters, digits, spaces or - _ .")
	ErrBadPIN     = errors.New("wrong PIN")
	ErrPINFormat  = errors.New("PIN must be exactly 4 digits")
	ErrPINLocked  = errors.New("too many wrong PINs: wait 10 minutes")
	ErrNoAccount  = errors.New("no such player")
	ErrNotAdmin   = errors.New("only the host can do this")
	ErrHandLive   = errors.New("wait until the hand ends")
	ErrCannotLink = errors.New("this key already has a history; ask the host to merge players")
	ErrLocked     = errors.New("the state file is in use by a running server")
	// ErrCannotExtend: Extend outside the caller's own turn, or while no
	// turn clock runs.
	ErrCannotExtend = errors.New("you can add time only on your own turn")
	// ErrNoExtensions: the player used every time extension of this server run.
	ErrNoExtensions = errors.New("no time extensions left")
	// ErrPanicTooSoon: the same player pressed the panic key less than
	// PanicCooldown ago.
	ErrPanicTooSoon = errors.New("wait a moment before you press the panic key again")
	// ErrStartNotSeated: Start from a session that is not seated.
	ErrStartNotSeated = errors.New("sit down first, then start the game")
	// ErrStartNeedsTwo: Start while fewer than 2 seated players can play
	// (seated, not sitting out, chips > 0).
	ErrStartNeedsTwo = errors.New("2 players must sit to start the game")
	// ErrNoRabbits: the player used every rabbit hunt of this server run.
	ErrNoRabbits = errors.New("no rabbit hunts left")
	// ErrBadAdmin: a Config.Admins entry is not "SHA256:<fingerprint>" or
	// "pin:<login>". NewRoom wraps it with the bad entry.
	ErrBadAdmin = errors.New(`an admin must be "SHA256:<key fingerprint>" or "pin:<login>"`)
	// ErrWeakPIN: CreatePINAccount refuses a PIN that people choose most
	// (1234, 1111, 1212, 2580, a year such as 1990, and similar), because
	// a guesser tries those first.
	ErrWeakPIN = errors.New("that PIN is too easy to guess: do not use a year, a run like 1234, or a pattern like 1111 or 1212")
	// ErrTooManyKeys: one remote address made too many new key accounts in
	// the last hour.
	ErrTooManyKeys = errors.New("too many new players from this computer: connect with a key that you used before, or wait an hour")
	// ErrBadShot: ShareShot got a link that is not "http://", that has a
	// space or a control character, or that is longer than 120 bytes.
	ErrBadShot = errors.New("that is not a screenshot link")
	// ErrShotsWaiting: MaxWaitingShots links of the caller already wait
	// for the end of the hand.
	ErrShotsWaiting = errors.New("3 screenshots already wait for the end of the hand")
)

// PanicCooldown is the shortest time between two panics by one player.
const PanicCooldown = 15 * time.Second

// Config sets up a room.
type Config struct {
	Seats         int           // 2..10
	SmallBlind    int64         //
	BigBlind      int64         //
	StartingStack int64         // chips for each buy-in and rebuy
	TurnTimeout   time.Duration // 0 disables the turn clock
	NextHandDelay time.Duration // pause between hands so people can read the result
	// RunoutDelay is the pause before each street of an all-in run-out (the
	// flop, the turn and the river). 0 deals the whole board at once.
	RunoutDelay time.Duration
	StatePath   string // JSON file for the chip bank; "" keeps it in memory only

	// Money for settle-up. Buy-in money = StartingStack / ChipsPerUnit.
	ChipsPerUnit int64  // chips per one currency unit (2 = "2 chips are ₹1"); <= 0 means 1
	Currency     string // symbol shown before amounts, e.g. "₹"
	// Admins lists the credentials that may mark the period settled, in
	// addition to sessions connected with Admin=true (the host). Each entry
	// is either "SHA256:<fingerprint>" (exact match against the SSH key that
	// the session logged in with) or "pin:<login>" (a session that logged in
	// with that PIN login; the login is compared lower-cased). A "pin:"
	// entry counts only when an account holds the login with a PIN when
	// NewRoom runs; see Room.IgnoredAdmins. Display names
	// never grant admin: any player can change a name. NewRoom returns an
	// error that wraps ErrBadAdmin for any other entry.
	Admins []string

	// Extensions is the number of time extensions that each player gets for
	// one server run (0 disables the feature). ExtensionTime is the time
	// that one extension adds to the turn clock; it must be > 0 when
	// Extensions > 0 (NewRoom returns an error otherwise).
	Extensions    int
	ExtensionTime time.Duration

	// ManualStart makes the room wait for Session.Start before it deals.
	// See Snapshot.Waiting. false keeps the old behaviour: the first hand
	// starts as soon as 2 players can play.
	ManualStart bool

	// RabbitHunts is the number of rabbit hunts that each account can do in
	// one server run (batch 10). 0 means no limit.
	RabbitHunts int
}

// ChatLine is one chat message.
type ChatLine struct {
	At   time.Time
	Name string
	Text string // already sanitized: printable runes only, max 200 runes
	// Shot is the link of a screenshot that Name shared with the table
	// (Session.ShareShot), or "". Text is then "shared a screenshot".
	Shot string
}

// Standing is one row of the leaderboard.
type Standing struct {
	Name    string
	Net     int64 // bank + chips on the table - total buy-ins
	BuyIns  int   // number of buy-ins and rebuys
	Online  bool
	Playing bool // seated now
}

// Me describes the session's own player.
type Me struct {
	Key  string // the identity passed to Connect
	Name string // display name
	Seat int    // -1 when not seated
	Bank int64  // chips held off the table
	// NeedsName is true for a brand-new account: the TUI shows the welcome
	// screen and calls SetName (or keeps the suggested name).
	NeedsName bool
	// CanLink is true when the identity is an SSH key whose account has no
	// history yet, so LinkPIN can attach it to an existing PIN account.
	CanLink bool
	Alerts  bool // bell and desktop notifications on (default true)
	Admin   bool // may call MarkSettled
	Keyless bool // logged in with a PIN, not an SSH key (the TUI shows the ssh-keygen tip)
	Host    bool // connected from the server machine; sees PlayerStats.Host
	// RabbitsLeft is the number of rabbit hunts that the player can still
	// do in this server run. It is 0 when Config.RabbitHunts is 0 (no limit).
	RabbitsLeft int
	// RabbitHand is what the player would have had after a rabbit hunt of
	// the last hand: the player's own 2 hole cards with the dealt board plus
	// Result.Rabbit. Zero (Hand == "") when the last hand has no rabbit cards
	// or the player was not dealt in. Only the player's own snapshots carry
	// it; it is never in Result, the log, stats or another snapshot.
	RabbitHand RabbitHand
	// ShowAtEnd is the stored show choice of a folded player in the live
	// hand: [left, right]. Both false when there is none. See ShowCards.
	ShowAtEnd [2]bool
	// ExtensionsLeft is the number of time extensions that the player can
	// still use in this server run. It is 0 when Config.Extensions is 0.
	ExtensionsLeft int
}

// RabbitHand is the hand a player would have had (Me.RabbitHand).
type RabbitHand struct {
	Category cards.Category // from cards.HandValue, the same evaluator as the best-hand hint
	Hand     string         // e.g. "Flush, Ace high"
	Best     []cards.Card   // the 5 best cards
}

// PlayerStats is one row of the stats dashboard. Counts are lifetime unless
// the field name starts with Period.
type PlayerStats struct {
	Name         string
	Online       bool
	Playing      bool
	Hands        int // hands dealt in
	HandsWon     int // hands where the player won chips
	VPIP         int // hands with voluntary chips preflop
	PFR          int // hands with a preflop bet or raise
	SawFlop      int
	Showdowns    int
	ShowdownsWon int
	AllIns       int
	BiggestPot   int64  // largest amount won in one hand (sum over its pots)
	BestHand     string // best hand shown at showdown, e.g. "Full House, Kings over Sevens"; "" when none
	BestCards    []cards.Card
	LifetimeNet  int64 // chips, over all periods
	BuyIns       int   // lifetime count
	PeriodNet    int64 // chips, this settle period (bank + stack - chips bought this period)
	PeriodBuyIns int
	PeriodHands  int

	// Fun stats, visible to everyone. Counted from batch 3 onward.
	BadBeats      int     // lost the main pot after being >= 70% to win it when the money went in
	WorstBeat     float64 // the highest such equity (0..1); 0 when none
	WorstBeatWith []cards.Card
	LuckiestRiver int64 // biggest amount won in a hand where the river turned a losing hand into the winner; 0 when none
	RiverCard     cards.Card
	FirstWinAfter int    // hands dealt up to and including the first won hand; 0 = no win yet
	Nemesis       string // the opponent who took the most net chips from this player; "" when none
	NemesisChips  int64  // those net chips (> 0)
	// FavouriteHand is the starting hand (e.g. "AKs", "QJo", "77") this player
	// voluntarily played most often, with its count. Filled ONLY in the row of
	// the viewing player's own account; "" in every other row, also for the host.
	FavouriteHand  string
	FavouriteCount int

	// Host holds the host-only stats. nil unless the viewer is the host
	// (Me.Host). ReadStats fills it for every row.
	Host *HostStats
}

// HostStats are the stats only the host sees. Each percentage has its
// count and its chances, so the TUI can show "—" when Chances is 0.
type HostStats struct {
	ThreeBet, ThreeBetChances         int // re-raised a single open raise preflop / faced one
	FoldTo3Bet, FoldTo3BetChances     int // folded after own open raise was re-raised / faced that
	Steal, StealChances               int // raised first in from cutoff, button or small blind / folded to there
	Limp, LimpChances                 int // first voluntary preflop action was a call with no raise before / hands dealt with a chance to act unraised
	CBet, CBetChances                 int // preflop last raiser bet the flop when checked to or first / had that chance
	FoldToCBet, FoldToCBetChances     int // folded to a flop continuation bet / faced one
	CheckRaise, CheckRaiseChances     int // checked then raised on the same postflop street / checked and then faced a bet on that street
	PostflopBetsRaises, PostflopCalls int // AF = PostflopBetsRaises / PostflopCalls
	// Hands, NetBB, NetChips and SeatedSeconds count only from batch 3
	// onward, as every HostStats field does. PlayerStats.Hands and
	// LifetimeNet also count the older hands, so a rate must not mix the
	// two: bb/100 = 100*NetBB/Hands and money per hour = NetChips per hour
	// of SeatedSeconds, all from this struct.
	Hands                              int     // hands dealt in that have host stats (from batch 3 onward)
	NetBB                              float64 // sum over those hands of (Won-Committed)/BigBlind
	NetChips                           int64   // sum over those hands of Won-Committed
	SeatedSeconds                      int64   // time seated (not sitting out) on the table
	BiggestLoss                        int64   // largest Committed-Won in one hand
	LongestWinStreak, CurrentWinStreak int     // consecutive hands dealt in with Won > 0
	AllInShowdowns, AllInShowdownsWon  int     // all-in and reached showdown / won chips there
}

// Money is an amount in hundredths of the currency unit (paise / cents).
type Money int64

// Balance is one player's settle-up result for the current period.
type Balance struct {
	Name  string
	Chips int64 // PeriodNet
	Money Money // Chips converted with ChipsPerUnit, rounded to the nearest hundredth
}

// Transfer is one payment that settles the period.
type Transfer struct {
	From   string
	To     string
	Amount Money
}

// Settlement is the settle-up view of the current period. Transfers use the
// fewest payments that a greedy largest-debtor-to-largest-creditor match
// gives (at most n-1 payments). Rounding remainders are absorbed so the
// transfers exactly cancel the Money balances.
type Settlement struct {
	Period       int
	Since        time.Time // when the period started
	ChipsPerUnit int64
	Currency     string
	Balances     []Balance // sorted by Money, high to low; zero balances omitted
	Transfers    []Transfer
	History      []PastPeriod // most recent first, max 20
}

// PastPeriod is a period that the host marked settled.
type PastPeriod struct {
	Period    int
	Since     time.Time
	SettledAt time.Time
	Transfers []Transfer
	Currency  string
	// ChipsPerUnit is the money rate of the period. It is 0 for a period
	// that an older version settled.
	ChipsPerUnit int64
}

// Snapshot is everything one session needs to draw the screen. It is a
// deep copy; the TUI may keep it.
type Snapshot struct {
	Table       engine.View
	Log         []string // recent table history, oldest first (max 200 lines)
	Chat        []ChatLine
	Spectators  []string   // connected names that are not seated
	Leaderboard []Standing // sorted by Net, high to low
	Me          Me
	Deadline    time.Time // when the player to act times out; zero when no clock runs
	// ClockTotal is the full length of the running turn clock: TurnTimeout
	// plus ExtensionTime for each extension used in this turn. It is zero
	// when no clock runs. The TUI draws the timer bar against it.
	ClockTotal time.Duration
	NextHandAt time.Time // when the next hand starts; zero when none is scheduled
	Now        time.Time // server time when the snapshot was made
	Config     Config
	Stats      []PlayerStats // every account with Hands > 0 or online; sorted by PeriodNet, high to low
	Settle     Settlement
	// Alert is a one-shot cue for this session: "turn" when it just became
	// the viewer's turn, "hurry" when 5 seconds remain on the viewer's clock,
	// "" otherwise. Each cue appears in exactly one snapshot.
	Alert string
	// PanicSeq counts the panics of this server run (0 = none yet). Each
	// Panic call adds 1. The TUI shows the panic screen when PanicSeq is
	// higher than in the snapshot before; it takes the first snapshot of a
	// connection as the baseline. A counter (not a one-shot cue) survives a
	// dropped snapshot.
	PanicSeq uint64
	// Waiting is true while Config.ManualStart is on and the game is not
	// started: the room deals no hand, and NextHandAt is zero. A room starts
	// in this state. Start ends it. The room goes back to it when a hand is
	// due but fewer than 2 players can play. Always false without
	// ManualStart.
	Waiting bool
	// StartedBy is the name of the player who pressed start, while the
	// first hand after a Start is not dealt yet; "" otherwise.
	StartedBy string
}

// NewRoom makes a room and loads the bank from cfg.StatePath when it exists.
func NewRoom(cfg Config) (*Room, error) { return newRoom(cfg) }

// Run is the room loop. It returns when ctx is done, after it saves the bank.
func (r *Room) Run(ctx context.Context) { r.run(ctx) }

// SaveErr returns the error of the last save of the state file, or nil
// when the last save worked (or the room has no state file). After Run
// returns, it is the result of the final save. Each failure also goes to
// the host log (stderr) and the table log.
func (r *Room) SaveErr() error { return r.lastSaveErr() }

// Config returns the config as NewRoom set it up. ChipsPerUnit and
// Currency are the money rate of the open period: a period with chips in
// it keeps the rate that the state file saved, also when the flags differ
// (the flag rate applies from the next period).
func (r *Room) Config() Config { return r.startConfig() }

// IgnoredAdmins returns the "pin:<login>" entries of Config.Admins that
// NewRoom ignored, sorted. NewRoom ignores a "pin:" entry when no account
// holds that login with a PIN at the start: else the first player to
// create that PIN login would become an admin. Such an entry makes nobody
// an admin until the next start. The host log warns for each entry.
func (r *Room) IgnoredAdmins() []string { return r.ignoredAdmins() }

// ConnectOptions carries per-connection facts known by the SSH layer.
type ConnectOptions struct {
	Admin bool // may mark settled (the host, or a name in Config.Admins)
	// Host is true for a connection from the server machine itself (loopback
	// or one of the machine's own IP addresses). Only Host sees HostStats.
	Host bool
	// Addr is the remote address of the client ("10.0.0.5:51234"). The room
	// uses it for the host log lines of LinkPIN and wrong PINs, for the
	// wrong-PIN limit of LinkPIN, and for the limit of new key accounts from
	// one address (no limit for Host or an empty Addr).
	Addr string
}

// ConnectWith is Connect with options. Identity strings:
//
//	"key:<SHA256 fingerprint>" for SSH key logins,
//	"pin:<login>" for PIN logins (login = lower-cased SSH user name).
//
// A "key:" identity not seen before gets a new account (Me.NeedsName and
// Me.CanLink are true). A "pin:" identity must exist (CreatePINAccount).
// name is the suggested display name (the SSH user name).
func (r *Room) ConnectWith(key, name string, o ConnectOptions) (*Session, error) {
	return r.connectWith(key, name, o)
}

// The four calls below are for the SSH auth handlers. They are safe to call
// from any goroutine; they go through the Run loop (ErrRoomClosed after Run).

// KnownKey reports whether the fingerprint belongs to an account.
func (r *Room) KnownKey(fingerprint string) bool { return r.knownKey(fingerprint) }

// PINAccountExists reports whether a PIN account exists for login.
func (r *Room) PINAccountExists(login string) bool { return r.pinAccountExists(login) }

// CheckPIN verifies a PIN. It is CheckPINFrom with an unknown address, so
// all its calls share one address limit. PINs are stored as bcrypt hashes
// only.
func (r *Room) CheckPIN(login, pin string) error { return r.checkPIN(login, pin, "") }

// CheckPINFrom verifies a PIN that a client at the remote address addr
// typed. After 5 wrong PINs for one login from one address within 10
// minutes, it returns ErrPINLocked for that login from that address until
// the 10 minutes pass; the owner can still log in from another computer.
// After 20 wrong PINs for one login from all addresses within 1 hour, it
// returns ErrPINLocked for that login for 1 hour. Each wrong PIN writes a
// host log line with addr and a table log line.
func (r *Room) CheckPINFrom(login, pin, addr string) error { return r.checkPIN(login, pin, addr) }

// WeakPIN reports whether CreatePINAccount refuses pin with ErrWeakPIN.
// The SSH dialog uses it to refuse a weak PIN before it asks for the PIN
// again.
func WeakPIN(pin string) bool { return weakPIN(pin) }

// CreatePINAccount makes a new PIN account for login (ErrPINFormat,
// ErrWeakPIN, ErrNameInUse when the login already exists).
func (r *Room) CreatePINAccount(login, pin string) error { return r.createPINAccount(login, pin) }

// Connect registers a new session. key is a stable identity (SSH public key
// fingerprint, or "user:<name>" when there is no key); name is the display
// name. When the same key already has a session, the room closes the old
// session (its Updates channel closes) and the new session takes over the
// seat. ErrNameInUse: a different key uses this name now.
func (r *Room) Connect(key, name string) (*Session, error) { return r.connect(key, name) }

// Updates delivers snapshots. The channel has a buffer of 1 and keeps only
// the newest snapshot, so a slow client never blocks the room. The room
// sends one snapshot right after Connect, then one after every change, and
// one each second while a clock runs. The channel closes when the session
// ends.
func (s *Session) Updates() <-chan Snapshot { return s.updates() }

// Sit takes a seat (-1 = first free seat) with SitWholeBank. It is the
// same as SitWith(seat, SitWholeBank).
func (s *Session) Sit(seat int) error { return s.sit(seat) }

// SitChoice is the amount that a player brings to the table.
type SitChoice int

const (
	// SitWholeBank brings the whole bank. When the bank is zero, the player
	// buys in for StartingStack.
	SitWholeBank SitChoice = iota
	// SitBuyIn brings exactly StartingStack. The chips come from the bank
	// first; the rest of the bank stays in the bank. When the bank holds
	// less than StartingStack, the player buys the difference (a top-up:
	// BoughtChips += difference, BuyIns++).
	SitBuyIn
)

// SitWith takes a seat (-1 = first free seat) with the amount that c picks.
func (s *Session) SitWith(seat int, c SitChoice) error { return s.sitWith(seat, c) }

// Stand leaves the seat. Chips behind go back to the bank. A live hand is folded.
func (s *Session) Stand() error { return s.stand() }

// Act sends a poker action for this player.
func (s *Session) Act(a engine.Action) error { return s.act(a) }

// SitOut skips hands (true) or rejoins them (false). The seat stays.
func (s *Session) SitOut(out bool) error { return s.sitOut(out) }

// Rebuy adds StartingStack to a busted, seated player. The chips come from
// the bank first; the player buys only the part that the bank does not
// cover (BoughtChips += that part, BuyIns++ when that part is > 0).
func (s *Session) Rebuy() error { return s.rebuy() }

// Chat posts a message to everyone in the room.
func (s *Session) Chat(text string) error { return s.chat(text) }

// MaxWaitingShots is the number of screenshot links that can wait for the
// end of a hand, for each player.
const MaxWaitingShots = 3

// ShareShot posts a chat line with the link of a screenshot that the
// caller took: ChatLine{Name: caller, Text: "shared a screenshot", Shot:
// url}. Only the TUI calls it, with a link from the server's own Store.
// See DESIGN.md "Batch 15".
//
// When no hand is live, the line goes to the chat now (later is false),
// under the same rate limit as Chat. When a hand is live, the room keeps
// the link and posts it when that hand ends, with the result (later is
// true). At most MaxWaitingShots links wait for each player; one more
// returns ErrShotsWaiting. A player who leaves keeps the waiting links:
// they still post when the hand ends. It returns ErrBadShot for a bad
// link.
func (s *Session) ShareShot(url string) (later bool, err error) { return s.shareShot(url) }

// Close ends the session (SSH disconnect). A seated player keeps the seat
// until the current hand ends; the clock acts for them at once on their
// turn. When the hand ends and the same key did not reconnect, the room
// stands the player up and the chips go back to the bank.
func (s *Session) Close() { s.close() }

// SetName sets the display name (welcome screen and the M rename key).
// Names are unique among ALL accounts, compared after NFKC normalization and
// case folding. ErrBadName, ErrNameInUse. A seated player's seat name changes too.
func (s *Session) SetName(name string) error { return s.setName(name) }

// LinkPIN attaches this session's SSH key to an existing PIN account and
// switches the session to that account (keeps its history and bank). Legal
// only when Me.CanLink; else ErrCannotLink. ErrBadPIN / ErrPINLocked as CheckPIN.
// After a link the key logs in to that account directly.
func (s *Session) LinkPIN(login, pin string) error { return s.linkPIN(login, pin) }

// RabbitHunt reveals the rest of the board of the last hand (any seated player).
// Batch 10: ErrNoRabbits when Config.RabbitHunts > 0 and the caller used all
// of them; a hunt counts only when it succeeds.
func (s *Session) RabbitHunt() error { return s.rabbitHunt() }

// ShowCards shows the caller's hole cards after the hand (see engine
// Table.ShowCards: the NoShowdown winner, or a player who folded).
//
// Batch 9: a player who folded in the LIVE hand may call it during the hand.
// Nothing shows then: the room stores the choice (Me.ShowAtEnd) and shows
// those cards right after the hand ends, before the next deal. A call with
// the same choice again cancels it (left=false, right=false also cancels).
// A player who has not folded, or who is not in the hand, gets ErrNotNow
// during a hand. The choice is lost when the player leaves the seat.
func (s *Session) ShowCards(left, right bool) error { return s.showCards(left, right) }

// SetAlerts turns the bell and notifications on or off; saved per account.
func (s *Session) SetAlerts(on bool) error { return s.setAlerts(on) }

// MarkSettled closes the current period: it records the transfers in
// History, stands every seated player (only between hands; else ErrHandLive),
// sets every account's bank and period counters to zero and starts the next
// period. Lifetime stats stay. ErrNotAdmin for non-admins.
func (s *Session) MarkSettled() error { return s.markSettled() }

// Extend adds Config.ExtensionTime to the caller's turn clock and uses one of
// the caller's time extensions. It returns ErrCannotExtend when it is not the
// caller's turn or no clock runs, and ErrNoExtensions when none are left.
func (s *Session) Extend() error { return s.extend() }

// Start ends Snapshot.Waiting: the next hand starts after
// Config.NextHandDelay capped at 3 seconds, and hands then follow one
// another as before. Any seated player may call it. ErrStartNotSeated,
// ErrStartNeedsTwo. A call while the game already runs (or without
// ManualStart) returns nil and does nothing.
func (s *Session) Start() error { return s.start() }

// Panic tells every connected session (also spectators and the caller) to
// show the panic screen: a one-second "someone is coming" notice and then a
// decoy screen. The game itself does not stop. Panic is anonymous to the
// players; the server log names the caller. ErrPanicTooSoon when the same
// account called Panic less than PanicCooldown ago.
func (s *Session) Panic() error { return s.panicNow() }

// ParseAdmin checks one Config.Admins entry and returns its canonical form:
// "SHA256:<fingerprint>" (43 base64 characters, as ssh-keygen -lf prints
// it) or "pin:<lower-cased login>". The prefix is not case-sensitive. Any
// other entry gives an error that wraps ErrBadAdmin and names the entry.
// NewRoom uses the same check.
func ParseAdmin(entry string) (string, error) { return parseAdmin(entry) }

// Offline host tools. They open StatePath directly and return ErrLocked
// while a server holds the state file (the room takes an exclusive flock on
// StatePath + ".lock" from NewRoom until Run returns).

// ReadStats loads the state file and returns the stats and the settlement,
// exactly as a snapshot would show them (every account offline).
func ReadStats(cfg Config) ([]PlayerStats, Settlement, error) { return readStats(cfg) }

// AccountInfo is one row of ListAccounts: what the host needs to write
// the -admin flag.
type AccountInfo struct {
	ID           string   // the internal account id
	Name         string   // display name
	Login        string   // PIN login; "" when the account has no PIN
	Fingerprints []string // "SHA256:..." of every SSH key of the account
	// HasPIN is true when the account has a PIN. An old profile can have a
	// Login and no PIN; a "pin:" admin entry for it makes nobody an admin.
	HasPIN bool
}

// ListAccounts loads the state file and lists every account, sorted by
// name (ErrLocked while a server holds the state file).
func ListAccounts(cfg Config) ([]AccountInfo, error) { return listAccounts(cfg) }

// UnlinkKey removes the SSH key fingerprint ("SHA256:...") from the account
// that holds it and returns the name of that account. It refuses to remove
// the only way to log in to an account (a key of an account without a PIN
// and without another key). ErrNoAccount when no account holds the key,
// ErrLocked while a server runs. It backs up the state file first.
func UnlinkKey(cfg Config, fingerprint string) (string, error) { return unlinkKey(cfg, fingerprint) }

// ResetState moves the state file to StatePath.<UTC time>.reset and returns
// that path. The next server start then has no players, banks or stats. It
// takes the state lock, so it returns ErrLocked while a server runs: a
// running server writes all banks back at its next save. The error wraps
// fs.ErrNotExist when there is no state file.
func ResetState(cfg Config) (string, error) { return resetState(cfg, time.Now()) }

// MergeAccounts moves everything of the account named from (display name or
// PIN login) into the account named to: keys, PIN login (only when to has
// none), bank, buy-ins, stats (sums; best hand and biggest pot = max), and
// past-period transfers are renamed. Then it deletes from.
func MergeAccounts(cfg Config, from, to string) error { return mergeAccounts(cfg, from, to) }
