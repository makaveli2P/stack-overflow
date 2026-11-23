package server

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// fingerprintLen is the length of the base64 part of a SHA256 key
// fingerprint: 32 bytes in unpadded base64 (the form of ssh-keygen -lf).
const fingerprintLen = 43

// parseAdmin checks one Config.Admins entry and returns its canonical form:
// "SHA256:<fingerprint>" or "pin:<lower-cased login>". The prefix is not
// case-sensitive; the fingerprint is. Any other entry gives an error that
// wraps ErrBadAdmin and names the entry.
func parseAdmin(entry string) (string, error) {
	e := strings.TrimSpace(entry)
	bad := fmt.Errorf("%q: %w", entry, ErrBadAdmin)
	prefix, rest, ok := strings.Cut(e, ":")
	if !ok {
		return "", bad
	}
	switch strings.ToLower(prefix) {
	case "sha256":
		fp := strings.TrimRight(rest, "=")
		if len(fp) != fingerprintLen || strings.IndexFunc(fp, notBase64) >= 0 {
			return "", bad
		}
		return "SHA256:" + fp, nil
	case "pin":
		login, err := normLogin(rest)
		if err != nil || login != strings.ToLower(strings.TrimSpace(rest)) {
			return "", bad
		}
		return "pin:" + login, nil
	}
	return "", bad
}

func notBase64(r rune) bool {
	return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/')
}

// adminSet is the parsed Config.Admins.
type adminSet struct {
	keys   map[string]bool // "SHA256:..." fingerprints
	logins map[string]bool // lower-cased PIN logins
	// pinIDs holds the ids of the accounts that held a "pin:" login with a
	// PIN at the start (see resolvePINs). isAdmin compares a PIN session
	// with these ids, not with the login: anybody can create a PIN login
	// that has no PIN yet, so a login alone does not prove the owner.
	pinIDs map[string]bool
	// ignored lists the "pin:" entries that no PIN profile held at the
	// start, sorted. They make nobody an admin until the next start.
	ignored []string
}

func parseAdmins(entries []string) (adminSet, error) {
	set := adminSet{keys: map[string]bool{}, logins: map[string]bool{}, pinIDs: map[string]bool{}}
	for _, e := range entries {
		c, err := parseAdmin(e)
		if err != nil {
			return adminSet{}, err
		}
		if login, ok := strings.CutPrefix(c, "pin:"); ok {
			set.logins[login] = true
		} else {
			set.keys[c] = true
		}
	}
	return set, nil
}

// resolvePINs finds the account of each "pin:" entry: the account that
// holds the login and a PIN hash, as a PIN login finds it. An entry
// without such an account goes to ignored, and the host log warns. Else
// the first player to create that PIN login would become an admin.
func (set *adminSet) resolvePINs(st *state) {
	for login := range set.logins {
		id, a := st.byLogin(login)
		if a == nil || a.PINHash == "" {
			set.ignored = append(set.ignored, "pin:"+login)
			continue
		}
		set.pinIDs[id] = true
	}
	slices.Sort(set.ignored)
	for _, e := range set.ignored {
		hostLog(log.WarnLevel, "admin entry ignored: no PIN profile has this login; the player must create the PIN profile, then restart the server",
			"admin", e)
	}
}

// isAdmin reports whether s may mark the period settled. The host is also
// an admin. Else the room matches the credential that the session logged
// in with: the SSH key fingerprint of a key session, or the PIN login of a
// keyless session. The display name never counts, because any player can
// change it.
//
// A "pin:" entry matches the account that held the login with a PIN when
// the room started (adminSet.pinIDs), not any session with that login.
//
// After LinkPIN, a key session belongs to a PIN account, but it still
// logged in with the key. So only a "SHA256:" entry for that key makes it
// an admin; a "pin:" entry for the account does not. The host lists the
// key too when that player must be an admin with the key.
func (r *Room) isAdmin(s *Session) bool {
	switch {
	case s.admin || s.host:
		return true
	case s.fp != "":
		return r.admins.keys[s.fp]
	case s.login != "":
		return r.admins.pinIDs[s.key]
	}
	return false
}

// panicLog writes the server log line of a panic. Tests replace it.
var panicLog = func(name string) { log.Info("panic key", "player", name) }

func (s *Session) panicNow() error {
	return s.call(func(r *Room) error {
		now := r.now()
		if last, ok := r.panicAt[s.key]; ok && now.Sub(last) < PanicCooldown {
			return ErrPanicTooSoon
		}
		r.panicAt[s.key] = now
		r.panicSeq++
		panicLog(s.name)
		r.broadcast()
		return nil
	})
}

// listAccounts implements ListAccounts. It takes the state lock, as
// MergeAccounts does, so it returns ErrLocked while a server runs: the
// running server keeps newer accounts in memory than the file has.
func listAccounts(cfg Config) ([]AccountInfo, error) {
	if cfg.StatePath == "" {
		return nil, errors.New("no state file")
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	defer unlockState(lock)
	st, _, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]AccountInfo, 0, len(st.Accounts))
	for id, a := range st.Accounts {
		out = append(out, AccountInfo{
			ID:           id,
			Name:         a.Name,
			Login:        a.Login,
			Fingerprints: slices.Clone(a.Keys),
			HasPIN:       a.PINHash != "",
		})
	}
	slices.SortFunc(out, func(a, b AccountInfo) int {
		if c := cmp.Compare(nameKey(a.Name), nameKey(b.Name)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

// ignoredAdmins implements IgnoredAdmins. NewRoom sets r.admins, and
// nothing changes it later, so any goroutine can read it.
func (r *Room) ignoredAdmins() []string { return slices.Clone(r.admins.ignored) }
