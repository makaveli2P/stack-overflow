package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/cards"
)

const (
	stateVersion  = 3
	v2Version     = 2 // batch 2 state: no batch 3 stats; read and migrated
	ledgerVersion = 1 // the old chip bank format, read for migration only
	maxHistory    = 200
	shownHistory  = 20
)

// state is everything that the room keeps on disk.
type state struct {
	NextID     int                 `json:"next_id"`
	HandNumber int                 `json:"hand_number"` // the last hand dealt
	Period     int                 `json:"period"`      // the current settle period, from 1
	Since      time.Time           `json:"period_since"`
	Accounts   map[string]*account `json:"accounts"` // by internal id
	History    []PastPeriod        `json:"history,omitempty"`
	// HistoryIDs gives the account ids of the transfers of each past period,
	// by period number; HistoryIDs[p][i] belongs to Transfers[i] of period
	// p. History keeps the names as they were at settle time, and a name is
	// free again after a rename, so only the ids say whose payment a record
	// is. A period without ids (an older file) has no entry.
	HistoryIDs map[int][]transferIDs `json:"history_ids,omitempty"`
	// ChipsPerUnit and Currency are the money rate of the open period. The
	// room sets them when the period starts, or at the first start of an
	// older file. A restart with other flags keeps them while the period
	// has chips in it (see pickRate). Zero means "not saved yet".
	ChipsPerUnit int64  `json:"chips_per_unit,omitempty"`
	Currency     string `json:"currency,omitempty"`
}

// transferIDs are the account ids of one past transfer.
type transferIDs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// pastIDs returns the ids of the transfers of p, or nil when the state has
// no ids that match p (an older file, or a hand-edited history).
func (st *state) pastIDs(p PastPeriod) []transferIDs {
	ids := st.HistoryIDs[p.Period]
	if len(ids) == 0 || len(ids) != len(p.Transfers) {
		return nil
	}
	return ids
}

// stateFile is the JSON form of state.
type stateFile struct {
	Version int `json:"version"`
	state
}

// account is one player: identities, profile, chips and stats.
type account struct {
	Name      string    `json:"name"`
	Named     bool      `json:"named,omitempty"` // the player chose the name (no welcome screen)
	Keys      []string  `json:"keys,omitempty"`  // SSH key fingerprints ("SHA256:...")
	Login     string    `json:"login,omitempty"` // PIN login; "" when none
	PINHash   string    `json:"pin_hash,omitempty"`
	AlertsOff bool      `json:"alerts_off,omitempty"`
	Created   time.Time `json:"created"`

	// The current settle period.
	Bank        int64 `json:"bank"`
	BuyIns      int   `json:"buy_ins"`
	BoughtChips int64 `json:"bought_chips"`
	PeriodHands int   `json:"period_hands"`

	// Lifetime.
	SettledNet  int64     `json:"settled_net"` // the sum of the nets of the settled periods
	TotalBuyIns int       `json:"total_buy_ins"`
	Stats       handStats `json:"stats"`
	// Batch 3 (version 3) lifetime stats. A version 2 file has none, so
	// they start at zero. Host.Hands stays zero here: Fun.Hands counts the
	// same hands, and hostStats copies it into the HostStats it returns.
	Fun  funStats  `json:"fun"`
	Host HostStats `json:"host"`
}

// handStats are the lifetime counts that come from the hand results.
type handStats struct {
	Hands        int       `json:"hands"`
	HandsWon     int       `json:"hands_won"`
	VPIP         int       `json:"vpip"`
	PFR          int       `json:"pfr"`
	SawFlop      int       `json:"saw_flop"`
	Showdowns    int       `json:"showdowns"`
	ShowdownsWon int       `json:"showdowns_won"`
	AllIns       int       `json:"all_ins"`
	BiggestPot   int64     `json:"biggest_pot"`
	Best         *bestHand `json:"best,omitempty"`
}

// bestHand is the best hand that a player showed at a showdown.
type bestHand struct {
	Value cards.HandValue `json:"value"`
	Cards []string        `json:"cards"` // short forms, e.g. "As"
}

// buyIn records one buy-in or rebuy of chips.
func (a *account) buyIn(chips int64) {
	a.BuyIns++
	a.TotalBuyIns++
	a.BoughtChips += chips
}

// net is the result of the player in this period: bank plus the chips on
// the table minus the chips bought in this period.
func (a *account) net(stack int64) int64 {
	return a.Bank + stack - a.BoughtChips
}

// hasKey reports whether fp is one of the fingerprints of a.
func (a *account) hasKey(fp string) bool { return slices.Contains(a.Keys, fp) }

// fresh reports whether the account has no history: no chips, no buy-ins,
// no hands and no PIN login.
func (a *account) fresh() bool {
	return a.Bank == 0 && a.BuyIns == 0 && a.BoughtChips == 0 && a.TotalBuyIns == 0 &&
		a.SettledNet == 0 && a.Stats.Hands == 0 && a.PeriodHands == 0 && a.Login == ""
}

// addStats adds the stats of o to s (sums; biggest pot and best hand = max).
func (s *handStats) add(o handStats) {
	s.Hands += o.Hands
	s.HandsWon += o.HandsWon
	s.VPIP += o.VPIP
	s.PFR += o.PFR
	s.SawFlop += o.SawFlop
	s.Showdowns += o.Showdowns
	s.ShowdownsWon += o.ShowdownsWon
	s.AllIns += o.AllIns
	s.BiggestPot = max(s.BiggestPot, o.BiggestPot)
	if o.Best != nil && (s.Best == nil || o.Best.Value.Compare(s.Best.Value) > 0) {
		b := *o.Best
		b.Cards = slices.Clone(o.Best.Cards)
		s.Best = &b
	}
}

func newState(now time.Time) *state {
	return &state{NextID: 1, Period: 1, Since: now, Accounts: make(map[string]*account)}
}

// newAccount adds an empty account and returns its id.
func (st *state) newAccount(now time.Time) (string, *account) {
	if st.NextID < 1 {
		st.NextID = 1
	}
	for {
		id := fmt.Sprintf("p%d", st.NextID)
		st.NextID++
		if st.Accounts[id] == nil {
			a := &account{Created: now}
			st.Accounts[id] = a
			return id, a
		}
	}
}

// byKey returns the account that holds the fingerprint fp. When two
// accounts hold it (a hand-edited file), the lowest id wins. It does not
// sort the ids, because each connect calls it.
func (st *state) byKey(fp string) (string, *account) {
	return st.first(func(a *account) bool { return a.hasKey(fp) })
}

// byLogin returns the account with the PIN login.
func (st *state) byLogin(login string) (string, *account) {
	return st.first(func(a *account) bool { return a.Login != "" && a.Login == login })
}

// first returns the account with the lowest id for which match is true.
func (st *state) first(match func(*account) bool) (string, *account) {
	var bestID string
	var best *account
	for id, a := range st.Accounts {
		if match(a) && (best == nil || id < bestID) {
			bestID, best = id, a
		}
	}
	return bestID, best
}

// ids returns the account ids in a fixed order.
func (st *state) ids() []string {
	ids := make([]string, 0, len(st.Accounts))
	for id := range st.Accounts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// nameTaken reports whether an account other than self uses name as its
// display name or as its PIN login. Two names are the same when their
// skeletons are equal: NFKC normalization, case folding, and Cyrillic and
// Greek letters that look like Latin letters read as those Latin letters
// (see skeleton). A display name that equals another account's PIN login
// would make "merge <login>" ambiguous and let a player pose as the login.
func (st *state) nameTaken(name, self string) bool {
	return st.takenNames(self)[skeleton(name)]
}

// takenNames returns the skeletons of the display names and PIN logins of
// every account except self.
func (st *state) takenNames(self string) map[string]bool {
	taken := make(map[string]bool, 2*len(st.Accounts))
	for id, a := range st.Accounts {
		if id == self {
			continue
		}
		if a.Name != "" {
			taken[skeleton(a.Name)] = true
		}
		if a.Login != "" {
			taken[skeleton(a.Login)] = true
		}
	}
	return taken
}

// uniqueName returns base, or base with a number at the end when another
// account uses base. The result has at most maxNameRunes runes. It builds
// the set of names once, so that its cost grows with the number of
// accounts, not with its square.
func (st *state) uniqueName(base, self string) string {
	taken := st.takenNames(self)
	if !taken[skeleton(base)] {
		return base
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprint(i)
		rs := []rune(base)
		if keep := maxNameRunes - len(suffix); len(rs) > keep {
			rs = rs[:keep]
		}
		cand := strings.TrimRight(string(rs), " ") + suffix
		if !taken[skeleton(cand)] {
			return cand
		}
	}
}

// loadState reads the state from path. A missing file or an empty path gives
// an empty state. A version 1 chip bank or a version 2 state is migrated
// (migrated is true; see stateFrom for the version).
func loadState(path string, now time.Time) (st *state, migrated bool, err error) {
	if path == "" {
		return newState(now), false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newState(now), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read state: %w", err)
	}
	return parseState(data, path, now)
}

// fileVersion returns the version field of a state file.
func fileVersion(data []byte) (int, error) {
	var head struct {
		Version int `json:"version"`
	}
	err := json.Unmarshal(data, &head)
	return head.Version, err
}

func parseState(data []byte, path string, now time.Time) (*state, bool, error) {
	version, err := fileVersion(data)
	if err != nil {
		return nil, false, fmt.Errorf("read state %s: %w", path, err)
	}
	switch version {
	case ledgerVersion:
		st, err := migrateLedger(data, now)
		if err != nil {
			return nil, false, fmt.Errorf("read state %s: %w", path, err)
		}
		return st, true, nil
	case stateVersion, v2Version:
		// Version 3 adds fields only: a version 2 file reads as version 3
		// with the new stats at zero.
	default:
		return nil, false, fmt.Errorf("read state %s: unknown version %d", path, version)
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, false, fmt.Errorf("read state %s: %w", path, err)
	}
	st := f.state
	if st.Accounts == nil {
		st.Accounts = make(map[string]*account)
	}
	for id, a := range st.Accounts {
		if a == nil {
			delete(st.Accounts, id)
		}
	}
	if st.Period < 1 {
		st.Period = 1
	}
	if st.NextID < 1 {
		st.NextID = 1
	}
	return &st, version != stateVersion, nil
}

// v1Account is one entry of the version 1 chip bank.
type v1Account struct {
	Name        string `json:"name"`
	Bank        int64  `json:"bank"`
	BuyIns      int    `json:"buy_ins"`
	BoughtChips int64  `json:"bought_chips"`
}

// migrateLedger makes a version 2 state from a version 1 chip bank. Each v1
// key becomes one account. A "user:<name>" key becomes a PIN account without
// a PIN; the first login with that name sets the PIN.
func migrateLedger(data []byte, now time.Time) (*state, error) {
	var f struct {
		Players map[string]v1Account `json:"players"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	st := newState(now)
	keys := make([]string, 0, len(f.Players))
	for k := range f.Players {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		old := f.Players[k]
		id, a := st.newAccount(now)
		if rest, ok := strings.CutPrefix(k, "user:"); ok {
			login, err := normLogin(rest)
			if err != nil {
				login = "player"
			}
			if _, other := st.byLogin(login); other != nil {
				// Two v1 names with one login: keep the history, drop the login.
				login = ""
			}
			a.Login = login
		} else {
			a.Keys = []string{strings.TrimPrefix(k, "key:")}
		}
		name := cleanName(old.Name)
		a.Name = st.uniqueName(name, id)
		a.Named = true
		a.Bank = old.Bank
		a.BuyIns = old.BuyIns
		a.TotalBuyIns = old.BuyIns
		a.BoughtChips = old.BoughtChips
	}
	return st, nil
}

// saveState writes st to path atomically: it writes a temporary file in the
// same directory and then renames it.
func saveState(path string, st *state) (err error) {
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, state: *st}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// backupOld keeps a copy of an older state file (a version 1 chip bank or a
// version 2 state) next to it, as path + ".v<version>.bak", before the room
// writes the current version over it. It does not replace an older backup.
func backupOld(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	version, err := fileVersion(data)
	if err != nil {
		return err
	}
	bak := fmt.Sprintf("%s.v%d.bak", path, version)
	if _, err := os.Stat(bak); err == nil {
		return nil
	}
	return writeAtomic(bak, data)
}

// lockState takes an exclusive flock on path + ".lock". It returns ErrLocked
// when another holder has the lock. An empty path needs no lock.
func lockState(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock state: %w", err)
	}
	return f, nil
}

// unlockState releases a lock from lockState. A nil file is fine.
func unlockState(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}
