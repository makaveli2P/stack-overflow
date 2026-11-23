package server

import (
	"slices"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/bcrypt"
)

const (
	// pinMaxFails wrong PINs for one login from one address in pinWindow
	// lock that login from that address for pinWindow.
	pinMaxFails = 5
	pinWindow   = 10 * time.Minute
	// pinMaxLoginFails wrong PINs for one login from all addresses in
	// pinLoginWindow lock the login for pinLoginWindow.
	pinMaxLoginFails = 20
	pinLoginWindow   = time.Hour
)

func (r *Room) connect(key, name string) (*Session, error) {
	name = cleanName(name)
	if key == "" {
		key = "user:" + name
	}
	return r.open(key, name, ConnectOptions{}, true)
}

func (r *Room) connectWith(key, name string, o ConnectOptions) (*Session, error) {
	return r.open(key, name, o, false)
}

// open parses the identity and registers the session on the Run goroutine.
// legacy is true for the old Connect: it creates a missing "user:" account
// without a PIN, and it uses name as the display name of a new account
// (ErrNameInUse when another account has it).
func (r *Room) open(raw, name string, o ConnectOptions, legacy bool) (*Session, error) {
	id, err := parseIdentity(raw)
	if err != nil {
		return nil, err
	}
	var s *Session
	err = r.do(func() error {
		var err error
		s, err = r.handleConnect(raw, id, name, o, legacy)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// findAccount returns the account of an identity.
func (r *Room) findAccount(id identity) (string, *account) {
	if id.pin {
		return r.st.byLogin(id.value)
	}
	return r.st.byKey(id.value)
}

func (r *Room) handleConnect(raw string, id identity, name string, o ConnectOptions, legacy bool) (*Session, error) {
	key, a := r.findAccount(id)
	created := false
	if a == nil {
		switch {
		case legacy:
			if r.st.nameTaken(name, "") {
				return nil, ErrNameInUse
			}
			key, a = r.st.newAccount(r.now())
			a.Name, a.Named = name, true
		case id.pin:
			return nil, ErrNoAccount
		default:
			if !o.Host && !r.allowNewKey(o.Addr, r.now()) {
				hostLog(log.WarnLevel, "too many new keys from one address", "addr", o.Addr, "limit_per_hour", maxNewKeysPerHour)
				return nil, ErrTooManyKeys
			}
			key, a = r.st.newAccount(r.now())
			a.Name = r.st.uniqueName(suggestName(name), key)
		}
		if id.pin {
			a.Login = id.value
		} else {
			a.Keys = []string{id.value}
		}
		created = true
	}
	if a.Name == "" {
		a.Name = r.st.uniqueName(suggestName(name), key)
		created = true
	}
	s := &Session{
		room:      r,
		ident:     raw,
		addr:      o.Addr,
		keyless:   id.pin,
		admin:     o.Admin,
		host:      o.Host,
		key:       key,
		name:      a.Name,
		ch:        make(chan Snapshot, 1),
		chatLimit: rateLimiter{limit: chatBurst, window: chatWindow},
	}
	switch {
	case !id.pin:
		s.fp = id.value
	case !id.legacy:
		s.login = id.value
	}
	switch {
	case r.sessions[key] != nil:
		r.dropSession(r.sessions[key])
		r.logf("%s connected again", s.name)
	case r.away[key]:
		delete(r.away, key)
		r.logf("%s is back", s.name)
	default:
		r.logf("%s joined", s.name)
	}
	r.sessions[key] = s
	if created && !a.idle() {
		r.save() // An idle account is not saved (see account.idle).
	}
	r.settle()
	return s, nil
}

func (r *Room) knownKey(fingerprint string) bool {
	id, err := parseIdentity(fingerprint)
	if err != nil || id.pin {
		return false
	}
	var known bool
	_ = r.do(func() error {
		_, a := r.st.byKey(id.value)
		known = a != nil
		return nil
	})
	return known
}

func (r *Room) pinAccountExists(login string) bool {
	login, err := normLogin(login)
	if err != nil {
		return false
	}
	var ok bool
	_ = r.do(func() error {
		_, a := r.st.byLogin(login)
		ok = a != nil && a.PINHash != ""
		return nil
	})
	return ok
}

// pinGuard counts the wrong PINs of one login from one address (the short
// lock), or of one login from all addresses (the backstop).
type pinGuard struct {
	max         int           // wrong PINs in window that start the lock
	window      time.Duration // the count window and the lock time
	fails       []time.Time   // wrong PINs in the last window
	pending     int           // checks that run now (each counts as a possible failure)
	lockedUntil time.Time
}

func (g *pinGuard) recent(now time.Time) int {
	g.fails = slices.DeleteFunc(g.fails, func(t time.Time) bool { return now.Sub(t) >= g.window })
	return len(g.fails)
}

func (g *pinGuard) locked(now time.Time) bool {
	return now.Before(g.lockedUntil) || g.recent(now)+g.pending >= g.max
}

// fail counts one wrong PIN. It returns true when the wrong PIN starts a
// lock.
func (g *pinGuard) fail(now time.Time) bool {
	g.fails = append(g.fails, now)
	if g.recent(now) >= g.max {
		g.lockedUntil = now.Add(g.window)
		g.fails = nil
		return true
	}
	return false
}

// idle reports whether the guard holds nothing: no recent wrong PIN, no
// check in progress and no lock.
func (g *pinGuard) idle(now time.Time) bool {
	return g.pending == 0 && g.recent(now) == 0 && !now.Before(g.lockedUntil)
}

// pinGuards are the two guards of one PIN check.
type pinGuards struct{ addr, login *pinGuard }

// guards returns the guard of login from the address addr (host part only)
// and the guard of login from all addresses. It makes the missing ones.
func (r *Room) guards(login, addr string) pinGuards {
	key := login + "\x00" + addrHost(addr)
	g := pinGuards{addr: r.pins[key], login: r.pinTotals[login]}
	if g.addr == nil {
		g.addr = &pinGuard{max: pinMaxFails, window: pinWindow}
		r.pins[key] = g.addr
	}
	if g.login == nil {
		g.login = &pinGuard{max: pinMaxLoginFails, window: pinLoginWindow}
		r.pinTotals[login] = g.login
	}
	return g
}

// forget removes the guards of login and addr when they hold nothing.
func (r *Room) forget(login, addr string, now time.Time) {
	key := login + "\x00" + addrHost(addr)
	if g := r.pins[key]; g != nil && g.idle(now) {
		delete(r.pins, key)
	}
	if g := r.pinTotals[login]; g != nil && g.idle(now) {
		delete(r.pinTotals, login)
	}
}

func (r *Room) checkPIN(login, pin, addr string) error {
	login, err := normLogin(login)
	if err != nil {
		return ErrNoAccount
	}
	_, err = r.verifyPIN(login, pin, addr)
	return err
}

// verifyPIN checks the PIN of a login and returns the account id. The
// bcrypt work runs on the calling goroutine, so a check does not stop the
// room. A check in progress counts against the limits until it ends.
//
// Two limits apply. pinMaxFails wrong PINs for one login from one address
// lock that login from that address for pinWindow. pinMaxLoginFails wrong
// PINs for one login from all addresses lock the login for pinLoginWindow.
// The first limit stops one person from guessing. It cannot lock out the
// owner, who connects from another computer. The second limit stops a
// guess from many addresses. Each wrong PIN writes a host log line with the
// address and a table log line.
func (r *Room) verifyPIN(login, pin, addr string) (string, error) {
	var hash string
	err := r.do(func() error {
		_, a := r.st.byLogin(login)
		if a == nil || a.PINHash == "" {
			return ErrNoAccount
		}
		g := r.guards(login, addr)
		if now := r.now(); g.addr.locked(now) || g.login.locked(now) {
			return ErrPINLocked
		}
		g.addr.pending++
		g.login.pending++
		hash = a.PINHash
		return nil
	})
	if err != nil {
		return "", err
	}
	ok := pinFormatOK(pin) && bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)) == nil
	var key string
	err = r.do(func() error {
		now := r.now()
		g := r.guards(login, addr)
		g.addr.pending--
		g.login.pending--
		defer r.forget(login, addr, now)
		if !ok {
			r.wrongPIN(login, addr, g, now)
			return ErrBadPIN
		}
		g.addr.fails = nil
		id, a := r.st.byLogin(login)
		if a == nil || a.PINHash != hash {
			return ErrBadPIN // The account changed during the check.
		}
		key = id
		return nil
	})
	return key, err
}

// wrongPIN counts a wrong PIN and tells the host and the table.
func (r *Room) wrongPIN(login, addr string, g pinGuards, now time.Time) {
	addrLocked := g.addr.fail(now)
	loginLocked := g.login.fail(now)
	name := login
	if _, a := r.st.byLogin(login); a != nil && a.Name != "" {
		name = a.Name
	}
	total := len(g.login.fails)
	if loginLocked {
		total = pinMaxLoginFails
	}
	hostLog(log.WarnLevel, "wrong PIN", "player", name, "login", login, "addr", addrOrUnknown(addr), "wrong_in_last_hour", total)
	r.logf("Wrong PIN for %s (%d in the last hour)", name, total)
	switch {
	case loginLocked:
		hostLog(log.WarnLevel, "PIN login locked for all addresses", "player", name, "login", login,
			"addr", addrOrUnknown(addr), "for", pinLoginWindow)
		r.logf("Too many wrong PINs for %s: PIN logins wait 1 hour", name)
	case addrLocked:
		hostLog(log.WarnLevel, "PIN login locked for one address", "player", name, "login", login,
			"addr", addrOrUnknown(addr), "for", pinWindow)
		r.logf("Too many wrong PINs for %s from one computer: it waits 10 minutes", name)
	}
	r.broadcast()
}

func (r *Room) createPINAccount(login, pin string) error {
	if !pinFormatOK(pin) {
		return ErrPINFormat
	}
	if weakPIN(pin) {
		return ErrWeakPIN
	}
	login, err := normLogin(login)
	if err != nil {
		return err
	}
	exists := func() error {
		if _, a := r.st.byLogin(login); a != nil && a.PINHash != "" {
			return ErrNameInUse
		}
		return nil
	}
	if err := r.do(exists); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), r.pinCost)
	if err != nil {
		return err
	}
	return r.do(func() error {
		if err := exists(); err != nil {
			return err
		}
		key, a := r.st.byLogin(login)
		if a == nil {
			key, a = r.st.newAccount(r.now())
			a.Login = login
			a.Name = r.st.uniqueName(suggestName(login), key)
		}
		a.PINHash = string(hash)
		r.save()
		return nil
	})
}

// canLink reports whether LinkPIN is legal for s: a key login whose account
// has no history and no seat.
func (r *Room) canLink(s *Session) bool {
	if s.keyless || s.fp == "" {
		return false
	}
	a := r.st.Accounts[s.key]
	return a != nil && a.fresh() && len(a.Keys) == 1 && !r.seated(s.key)
}

func (s *Session) setName(name string) error {
	name, err := checkName(name)
	if err != nil {
		return err
	}
	return s.call(func(r *Room) error {
		a := r.st.Accounts[s.key]
		if a == nil {
			return ErrNoAccount
		}
		if r.st.nameTaken(name, s.key) {
			return ErrNameInUse
		}
		old := a.Name
		if r.seated(s.key) {
			r.drainEvents()
			if err := r.table.Rename(s.key, name); err != nil {
				return err
			}
			r.table.DrainEvents() // The room writes its own log line below.
		}
		a.Name, a.Named = name, true
		s.name = name
		if old != name {
			r.logf("%s is now %s", old, name)
		}
		r.save()
		r.settle()
		return nil
	})
}

func (s *Session) linkPIN(login, pin string) error {
	login, err := normLogin(login)
	if err != nil {
		return ErrNoAccount
	}
	check := func(r *Room) error {
		if !r.canLink(s) {
			return ErrCannotLink
		}
		return nil
	}
	if err := s.call(check); err != nil {
		return err
	}
	target, err := s.room.verifyPIN(login, pin, s.addr)
	if err != nil {
		return err
	}
	return s.call(func(r *Room) error {
		if err := check(r); err != nil {
			return err
		}
		a := r.st.Accounts[target]
		if a == nil || target == s.key {
			return ErrNoAccount
		}
		old := s.key
		oldName := s.name
		if !a.hasKey(s.fp) {
			a.Keys = append(a.Keys, s.fp)
			// A link is the one way to gain a key login to a PIN account.
			// The owner and the host must see it, in case it was a guess.
			hostLog(log.InfoLevel, "SSH key linked to a PIN account", "player", a.Name, "login", login,
				"key", s.fp, "addr", addrOrUnknown(s.addr))
			r.logf("A new SSH key now logs in as %s", a.Name)
		}
		delete(r.st.Accounts, old)
		delete(r.sessions, old)
		if prev := r.sessions[target]; prev != nil {
			r.dropSession(prev)
		}
		delete(r.away, target)
		s.key, s.name = target, a.Name
		r.sessions[target] = s
		if oldName != a.Name {
			r.logf("%s is now %s", oldName, a.Name)
		}
		r.save()
		r.settle()
		return nil
	})
}

func (s *Session) setAlerts(on bool) error {
	return s.call(func(r *Room) error {
		if a := r.st.Accounts[s.key]; a != nil {
			a.AlertsOff = !on
		}
		r.save()
		r.settle()
		return nil
	})
}
