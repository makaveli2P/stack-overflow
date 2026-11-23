package server

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/makaveli2p/stack-overflow/internal/engine"
)

func (s *Session) rabbitHunt() error {
	return s.call(func(r *Room) error {
		if !r.seated(s.key) {
			return engine.ErrNotSeated
		}
		if r.cfg.RabbitHunts > 0 && r.rabbitsLeft(s.key) <= 0 {
			return ErrNoRabbits
		}
		r.drainEvents()
		if err := r.table.RabbitHunt(); err != nil {
			return err
		}
		r.rabbitUsed[s.key]++ // Only a hunt that succeeds counts.
		// The engine does not know who hunted; the room adds the name to
		// the engine line and keeps its street groups:
		// "Rabbit hunt (Bob): [K♠ 7♥ 2♦] [9♣] [A♥]".
		for _, ev := range r.table.DrainEvents() {
			if ev.Kind == engine.EvRabbit {
				ev.Text = rabbitLine(s.name, ev.Text)
			}
			r.log.add(ev.Text)
		}
		r.holdForReveal()
		r.settle()
		return nil
	})
}

// rabbitLine puts the hunter's name into the engine's rabbit hunt line.
func rabbitLine(name, text string) string {
	const prefix = "Rabbit hunt:"
	if rest, ok := strings.CutPrefix(text, prefix); ok {
		return fmt.Sprintf("Rabbit hunt (%s):%s", name, rest)
	}
	return text
}

func (s *Session) markSettled() error {
	return s.call(func(r *Room) error {
		if !r.isAdmin(s) {
			return ErrNotAdmin
		}
		if r.table.InHand() {
			return ErrHandLive
		}
		// A settle cannot be undone, so keep a copy of the file first.
		r.save()
		if err := backupState(r.cfg.StatePath, r.now(), "settle"); err != nil {
			hostLog(log.ErrorLevel, "settle refused: no backup", "err", err)
			return err
		}
		for _, sv := range r.table.View("").Seats {
			if !sv.Empty {
				_ = r.standUp(sv.PlayerID, sv.Name)
			}
		}
		clear(r.away)
		clear(r.showAtEnd)
		r.drainEvents()
		past := r.st.closePeriod(r.now(), r.cfg.ChipsPerUnit, r.cfg.Currency)
		r.logf("%s marks period %d settled", s.name, past.Period)
		// The new period takes the money rate of the flags.
		if r.flagCPU != r.cfg.ChipsPerUnit || r.flagCurrency != r.cfg.Currency {
			r.logf("Period %d: %d chips are %s1", r.st.Period, r.flagCPU, r.flagCurrency)
		}
		r.cfg.ChipsPerUnit, r.cfg.Currency = r.flagCPU, r.flagCurrency
		r.st.ChipsPerUnit, r.st.Currency = r.flagCPU, r.flagCurrency
		r.save()
		r.settle()
		return nil
	})
}

func readStats(cfg Config) ([]PlayerStats, Settlement, error) {
	st, _, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		return nil, Settlement{}, err
	}
	// The state file is the host's: every row gets the host stats. The
	// favourite hand stays empty, because it is only for its owner.
	rows, ids := st.statsTable(nil, nil, nil)
	for i, id := range ids {
		h := st.hostStats(id)
		rows[i].Host = &h
	}
	// The saved rate of the open period wins over cfg (see pickRate).
	cpu, currency := st.rate(cfg)
	return rows, st.settlement(nil, cpu, currency), nil
}

// find returns the account that nameOrLogin names: a display name
// (compared as names are) or a PIN login. "name:<name>" and "login:<login>"
// pick one kind only. When a display name and a PIN login match two
// different accounts, find returns an error that names both: a guess could
// move one player's key and chips into another player's account.
func (st *state) find(nameOrLogin string) (string, *account, error) {
	byName := func(n string) (string, *account) {
		k := nameKey(n)
		for _, id := range st.ids() {
			if a := st.Accounts[id]; a.Name != "" && nameKey(a.Name) == k {
				return id, a
			}
		}
		return "", nil
	}
	byLogin := func(l string) (string, *account) {
		if login, err := normLogin(l); err == nil {
			return st.byLogin(login)
		}
		return "", nil
	}
	notFound := fmt.Errorf("%w: %q", ErrNoAccount, nameOrLogin)
	if rest, ok := strings.CutPrefix(nameOrLogin, "name:"); ok {
		if id, a := byName(rest); a != nil {
			return id, a, nil
		}
		return "", nil, notFound
	}
	if rest, ok := strings.CutPrefix(nameOrLogin, "login:"); ok {
		if id, a := byLogin(rest); a != nil {
			return id, a, nil
		}
		return "", nil, notFound
	}
	nid, na := byName(nameOrLogin)
	lid, la := byLogin(nameOrLogin)
	switch {
	case na != nil && la != nil && nid != lid:
		return "", nil, fmt.Errorf("%q is the name of one player and the PIN login of another (named %q); "+
			"write \"name:%s\" or \"login:%s\"", nameOrLogin, la.Name, nameOrLogin, nameOrLogin)
	case na != nil:
		return nid, na, nil
	case la != nil:
		return lid, la, nil
	}
	return "", nil, notFound
}

func mergeAccounts(cfg Config, from, to string) (err error) {
	if cfg.StatePath == "" {
		return errors.New("no state file")
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		return err
	}
	defer unlockState(lock)
	st, migrated, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		return err
	}
	if migrated {
		if err := backupOld(cfg.StatePath); err != nil {
			return err
		}
	}
	if err := st.merge(from, to); err != nil {
		return err
	}
	if err := backupState(cfg.StatePath, time.Now(), "merge"); err != nil {
		return err
	}
	return saveState(cfg.StatePath, st)
}

func unlinkKey(cfg Config, fingerprint string) (string, error) {
	if cfg.StatePath == "" {
		return "", errors.New("no state file")
	}
	fp, err := parseAdmin(fingerprint)
	if err != nil || !strings.HasPrefix(fp, "SHA256:") {
		return "", fmt.Errorf("%q is not a key fingerprint: write \"SHA256:<43 characters>\" as \"stack-overflow accounts\" prints it", fingerprint)
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		return "", err
	}
	defer unlockState(lock)
	st, migrated, err := loadState(cfg.StatePath, time.Now())
	if err != nil {
		return "", err
	}
	if migrated {
		if err := backupOld(cfg.StatePath); err != nil {
			return "", err
		}
	}
	_, a := st.byKey(fp)
	if a == nil {
		return "", fmt.Errorf("%w: no player has the key %s", ErrNoAccount, fp)
	}
	if len(a.Keys) == 1 && a.PINHash == "" {
		return "", fmt.Errorf("the key %s is the only way to log in as %q: merge that player into another one instead", fp, a.Name)
	}
	a.Keys = slices.DeleteFunc(a.Keys, func(k string) bool { return k == fp })
	if err := backupState(cfg.StatePath, time.Now(), "unlink"); err != nil {
		return "", err
	}
	return a.Name, saveState(cfg.StatePath, st)
}

// merge moves everything of the account from into the account to and
// deletes from.
func (st *state) merge(from, to string) error {
	fromID, src, err := st.find(from)
	if err != nil {
		return err
	}
	toID, dst, err := st.find(to)
	if err != nil {
		return err
	}
	if fromID == toID {
		return fmt.Errorf("%q and %q are the same player", from, to)
	}
	for _, k := range src.Keys {
		if !dst.hasKey(k) {
			dst.Keys = append(dst.Keys, k)
		}
	}
	if dst.Login == "" && src.Login != "" {
		dst.Login, dst.PINHash = src.Login, src.PINHash
	}
	dst.Named = dst.Named || src.Named
	dst.Bank += src.Bank
	dst.BuyIns += src.BuyIns
	dst.BoughtChips += src.BoughtChips
	dst.PeriodHands += src.PeriodHands
	dst.SettledNet += src.SettledNet
	dst.TotalBuyIns += src.TotalBuyIns
	dst.Stats.add(src.Stats)
	dst.Fun.merge(src.Fun)
	dst.Host.add(src.Host)
	dst.Host.CurrentWinStreak = max(dst.Host.CurrentWinStreak, src.Host.CurrentWinStreak)
	st.mergeFlows(fromID, toID)
	st.mergeHistory(fromID, src, toID, dst)
	delete(st.Accounts, fromID)
	return nil
}

// mergeHistory gives the past transfers of the account fromID to the account
// toID. History keeps the names of settle time, and a name is free again
// after a rename, so a match by the current name could move another
// player's payment. A period with ids is rewritten by id. A period without
// ids (an older file) is rewritten by name only when src played in a
// settled period; else it stays as it is. A transfer that becomes a payment
// to oneself is removed.
func (st *state) mergeHistory(fromID string, src *account, toID string, dst *account) {
	byName := src.SettledNet != 0 || src.Stats.Hands > src.PeriodHands
	for i := range st.History {
		p := &st.History[i]
		ids := st.pastIDs(*p)
		if ids == nil && !byName {
			continue
		}
		var keep []Transfer
		var keepIDs []transferIDs
		for j, t := range p.Transfers {
			if ids != nil {
				id := ids[j]
				if id.From == fromID {
					id.From, t.From = toID, dst.Name
				}
				if id.To == fromID {
					id.To, t.To = toID, dst.Name
				}
				if id.From == id.To && id.From != "" {
					continue
				}
				keepIDs = append(keepIDs, id)
			} else {
				if t.From == src.Name {
					t.From = dst.Name
				}
				if t.To == src.Name {
					t.To = dst.Name
				}
				if t.From == t.To {
					continue
				}
			}
			keep = append(keep, t)
		}
		p.Transfers = keep
		if ids != nil {
			st.HistoryIDs[p.Period] = keepIDs
		}
	}
}
