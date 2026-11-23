package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// hostLog writes one line to the server log of the host (the default
// charmbracelet/log logger, on stderr). Tests replace it.
var hostLog = func(level log.Level, msg string, kv ...any) { log.Log(level, msg, kv...) }

// addrHost returns the host part of a remote address ("10.0.0.5" from
// "10.0.0.5:51234"). An address without a port stays as it is.
func addrHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// addrOrUnknown returns addr, or "unknown" when addr is empty.
func addrOrUnknown(addr string) string {
	if addr == "" {
		return "unknown"
	}
	return addr
}

// weakPIN reports whether pin is one of the PINs that people choose most:
// four equal digits, a run up or down (0123, 4321), two pairs that repeat
// (1212, 6969), a keypad line (2580, 0852), 1004, and the years 1900 to
// 2099. A guesser tries these first, so CreatePINAccount refuses them. A
// PIN that exists stays valid.
func weakPIN(pin string) bool {
	if !pinFormatOK(pin) {
		return false
	}
	d := [4]int{}
	for i := range 4 {
		d[i] = int(pin[i] - '0')
	}
	up, down := true, true
	for i := 1; i < 4; i++ {
		up = up && d[i] == d[i-1]+1
		down = down && d[i] == d[i-1]-1
	}
	switch {
	case up || down:
		return true
	case d[0] == d[2] && d[1] == d[3]: // also four equal digits
		return true
	case pin == "2580" || pin == "0852" || pin == "1004":
		return true
	case pin[:2] == "19" || pin[:2] == "20":
		return true
	}
	return false
}

// idle reports whether a is an account that holds nothing worth a save: a
// key account with no chips and no history, no PIN, no settings, and a
// name that the player did not choose. Each new SSH key makes such an
// account. The room does not save idle accounts and removes them at start,
// so a loop of new keys cannot fill the state file.
func (a *account) idle() bool {
	return a.fresh() && !a.Named && a.PINHash == "" && !a.AlertsOff && len(a.Keys) > 0
}

// pruneIdle removes the idle accounts and returns how many it removed.
func (st *state) pruneIdle() int {
	n := 0
	for id, a := range st.Accounts {
		if a.idle() {
			delete(st.Accounts, id)
			n++
		}
	}
	return n
}

// maxNewKeysPerHour is the number of new key accounts that one remote
// address can make in one hour. A connection from the server machine has
// no limit.
const maxNewKeysPerHour = 20

// allowNewKey records a new key account from addr. It returns false when
// addr made maxNewKeysPerHour accounts in the last hour.
func (r *Room) allowNewKey(addr string, now time.Time) bool {
	host := addrHost(addr)
	if host == "" {
		return true
	}
	times := slices.DeleteFunc(r.newKeys[host], func(t time.Time) bool { return now.Sub(t) >= time.Hour })
	if len(times) >= maxNewKeysPerHour {
		r.newKeys[host] = times
		return false
	}
	r.newKeys[host] = append(times, now)
	return true
}

// keepBackups is the number of timestamped backups of the state file that
// backupState keeps.
const keepBackups = 10

// backupName matches the part of a backup file name between the state
// file name and ".bak", for example "20261118T101500Z.settle".
var backupName = regexp.MustCompile(`^\d{8}T\d{6}Z\.[a-z]+(-\d+)?$`)

// backupState copies the state file at path to
// path.<UTC time>.<why>.bak and removes the oldest of these backups, so
// that keepBackups stay. A missing state file needs no backup. The room
// makes a backup at start, before a settle and before each offline change
// (merge, unlink), because those changes cannot be undone.
func backupState(path string, now time.Time, why string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("back up the state file: %w", err)
	}
	stamp := now.UTC().Format("20060102T150405Z")
	bak := fmt.Sprintf("%s.%s.%s.bak", path, stamp, why)
	for i := 2; fileExists(bak); i++ {
		bak = fmt.Sprintf("%s.%s.%s-%d.bak", path, stamp, why, i)
	}
	if err := writeAtomic(bak, data); err != nil {
		return fmt.Errorf("back up the state file: %w", err)
	}
	return pruneBackups(path)
}

// backups returns the timestamped backups of path, oldest first.
func backups(path string) ([]string, error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		mid, ok := strings.CutPrefix(e.Name(), base+".")
		if !ok {
			continue
		}
		if mid, ok = strings.CutSuffix(mid, ".bak"); ok && backupName.MatchString(mid) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out) // the time stamp is first, so the names sort by time
	return out, nil
}

func pruneBackups(path string) error {
	list, err := backups(path)
	if err != nil {
		return err
	}
	for len(list) > keepBackups {
		if err := os.Remove(list[0]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		list = list[1:]
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// noteSave records the result of a save. The first failure, and each new
// failure text, goes to the host log and to the table log, so that the
// host sees it. The first save that works after a failure says so too.
// Room.SaveErr returns the result of the last save.
func (r *Room) noteSave(err error) {
	r.saveMu.Lock()
	prev := r.saveErr
	r.saveErr = err
	r.saveMu.Unlock()
	switch {
	case err != nil && (prev == nil || prev.Error() != err.Error()):
		hostLog(log.ErrorLevel, "could not save the state file", "path", r.cfg.StatePath, "err", err)
		r.logf("Could not save the chip bank: %v", err)
	case err == nil && prev != nil:
		hostLog(log.InfoLevel, "the state file is saved again", "path", r.cfg.StatePath)
		r.logf("The chip bank is saved again")
	}
}

func (r *Room) lastSaveErr() error {
	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	return r.saveErr
}

// periodActive reports whether any account has chips, buy-ins or hands in
// the open period.
func (st *state) periodActive() bool {
	for _, a := range st.Accounts {
		if a.Bank != 0 || a.BuyIns != 0 || a.BoughtChips != 0 || a.PeriodHands != 0 {
			return true
		}
	}
	return false
}

// rate returns the money rate of the open period: the saved rate, or the
// rate of cfg when the state has none (a new state or an older file).
func (st *state) rate(cfg Config) (int64, string) {
	if st.ChipsPerUnit > 0 {
		return st.ChipsPerUnit, st.Currency
	}
	return max(cfg.ChipsPerUnit, 1), cfg.Currency
}

// pickRate sets the money rate of the open period when the room starts. A
// period with chips in it keeps its saved rate, because the players bought
// those chips at that rate. The flags then apply from the next period, and
// the host log says so. Else the period takes the rate of the flags.
func (st *state) pickRate(cfg Config) (cpu int64, currency string, kept bool) {
	flagCPU := max(cfg.ChipsPerUnit, 1)
	if st.ChipsPerUnit > 0 && st.periodActive() &&
		(st.ChipsPerUnit != flagCPU || st.Currency != cfg.Currency) {
		return st.ChipsPerUnit, st.Currency, true
	}
	st.ChipsPerUnit, st.Currency = flagCPU, cfg.Currency
	return flagCPU, cfg.Currency, false
}

func (r *Room) startConfig() Config {
	c := r.startCfg
	c.Admins = slices.Clone(c.Admins)
	return c
}
