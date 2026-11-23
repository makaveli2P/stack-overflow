package main

import (
	"crypto/subtle"
	"errors"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

// SSH permissions extensions that carry the identity from the auth step to
// the session.
const (
	keyExt = "stack-overflow-key-fingerprint" // SHA256 fingerprint of a proven key
	pinExt = "stack-overflow-pin-login"       // login of a proven PIN account
)

// maxPINTries is the number of PIN tries for one connection.
const maxPINTries = 3

// maxLoginRunes is the longest PIN login.
const maxLoginRunes = 32

// Text for the keyboard-interactive dialog.
const (
	textBadLogin   = "Your SSH user name is the name of your " + appName + " player. Use 1 to 32 letters or digits, for example: ssh -p 2222 alice@<host>"
	textWrongPIN   = "Wrong PIN. Try again."
	textPINFormat  = "The PIN must be exactly 4 digits."
	textWeakPIN    = "That PIN is too easy to guess. Do not use a year, a run like 1234, or a pattern like 1111 or 1212. Choose another PIN."
	textPINMatch   = "The two PINs are not the same. Choose a PIN again."
	textNoTries    = "Too many wrong tries. Connect again to try again."
	textPINLocked  = "Too many wrong PINs for this player. Wait 10 minutes, then connect again."
	textRoomClosed = appName + " is closing. Try again in a moment."
)

var errDenied = errors.New("permission denied")

// pinStore is the part of *server.Room that the auth handlers use.
type pinStore interface {
	PINAccountExists(login string) bool
	CheckPINFrom(login, pin, addr string) error
	CreatePINAccount(login, pin string) error
}

// authState is the auth state of one connection.
type authState struct {
	mu     sync.Mutex
	tries  int    // PIN tries used
	hangup func() // closes the connection; nil in tests without a conn
}

// end closes the connection when no PIN try can help any more. Without it,
// OpenSSH asks for a "password" after the last try, and that prompt can
// never succeed.
func (s *authState) end() {
	s.mu.Lock()
	f := s.hangup
	s.mu.Unlock()
	if f != nil {
		f()
	}
}

// authStateKey stores the *authState in the SSH context.
type authStateKey struct{}

// use takes one PIN try. It returns false when no try is left.
func (s *authState) use() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tries >= maxPINTries {
		return false
	}
	s.tries++
	return true
}

func (s *authState) left() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tries < maxPINTries
}

// stateOf returns the auth state of the connection of ctx.
func stateOf(ctx ssh.Context) *authState {
	if st, ok := ctx.Value(authStateKey{}).(*authState); ok {
		return st
	}
	st := &authState{}
	ctx.SetValue(authStateKey{}, st)
	return st
}

// auth holds the SSH auth rules:
//
//   - A client that offers a key logs in with the key. The identity is
//     "key:<fingerprint>".
//   - A client without a key logs in with a PIN (keyboard-interactive, or
//     the password method for an account that exists). A new login chooses
//     a PIN. The identity is "pin:<login>".
//   - With a shared password, every client also types that password: a key
//     client after it proves the key, a PIN client before the PIN.
type auth struct {
	password string // the shared password; "" means none
	store    pinStore
}

func (a *auth) matchShared(s string) bool {
	return subtle.ConstantTimeCompare([]byte(s), []byte(a.password)) == 1
}

// askShared asks for the shared password with keyboard-interactive.
func (a *auth) askShared(ch gossh.KeyboardInteractiveChallenge) bool {
	ans, err := ch("", appName, []string{"Table password: "}, []bool{false})
	return err == nil && len(ans) == 1 && a.matchShared(ans[0])
}

// options returns the wish options that install the auth rules.
func (a *auth) options() []ssh.Option {
	return []ssh.Option{
		// wish calls this handler for keyboard-interactive. It is also the
		// handler that keeps "none" auth off.
		wish.WithKeyboardInteractiveAuth(func(ctx ssh.Context, ch gossh.KeyboardInteractiveChallenge) bool {
			if a.password != "" && !a.askShared(ch) {
				return false
			}
			login, ok := a.pinDialog(ctx.User(), ctx.RemoteAddr().String(), stateOf(ctx), ch)
			if !ok {
				return false
			}
			p := ctx.Permissions()
			p.Permissions = &gossh.Permissions{Extensions: map[string]string{pinExt: login}}
			return true
		}),
		func(srv *ssh.Server) error {
			srv.ServerConfigCallback = func(ctx ssh.Context) *gossh.ServerConfig {
				st := stateOf(ctx)
				return &gossh.ServerConfig{
					PublicKeyCallback: a.publicKey,
					PasswordCallback: func(c gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
						return a.passwordStep(c, pw, st)
					},
				}
			}
			return nil
		},
	}
}

// publicKey accepts every key. gossh checks the key signature before it
// applies the permissions or the partial success, so the key is proven.
func (a *auth) publicKey(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
	perms := func() *gossh.Permissions {
		return &gossh.Permissions{Extensions: map[string]string{keyExt: gossh.FingerprintSHA256(key)}}
	}
	if a.password == "" {
		return perms(), nil
	}
	return nil, &gossh.PartialSuccessError{Next: gossh.ServerAuthCallbacks{
		PasswordCallback: func(_ gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if !a.matchShared(string(pw)) {
				return nil, errDenied
			}
			return perms(), nil
		},
		KeyboardInteractiveCallback: func(_ gossh.ConnMetadata, ch gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
			if !a.askShared(ch) {
				return nil, errDenied
			}
			return perms(), nil
		},
	}}
}

// passwordStep is the first password step of a client without a key. Without
// a shared password, the password is the PIN of an account that exists.
// With a shared password, the password must be the shared password, and the
// PIN comes next.
func (a *auth) passwordStep(c gossh.ConnMetadata, pw []byte, st *authState) (*gossh.Permissions, error) {
	if a.password == "" {
		return a.pinPassword(c, pw, st)
	}
	if !a.matchShared(string(pw)) {
		return nil, errDenied
	}
	return nil, &gossh.PartialSuccessError{Next: gossh.ServerAuthCallbacks{
		PasswordCallback: func(c gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			return a.pinPassword(c, pw, st)
		},
		KeyboardInteractiveCallback: func(c gossh.ConnMetadata, ch gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
			login, ok := a.pinDialog(c.User(), c.RemoteAddr().String(), st, ch)
			if !ok {
				return nil, errDenied
			}
			return pinPerms(login), nil
		},
	}}
}

// pinPassword checks a PIN that comes with the password method. Only an
// account that exists can use it; a new player chooses a PIN with
// keyboard-interactive.
func (a *auth) pinPassword(c gossh.ConnMetadata, pw []byte, st *authState) (*gossh.Permissions, error) {
	login, ok := pinLogin(c.User())
	if !ok || !a.store.PINAccountExists(login) {
		return nil, errDenied
	}
	if !st.use() {
		st.end()
		return nil, errDenied
	}
	if err := a.store.CheckPINFrom(login, string(pw), c.RemoteAddr().String()); err != nil {
		if errors.Is(err, server.ErrPINLocked) || !st.left() {
			st.end()
		}
		return nil, errDenied
	}
	return pinPerms(login), nil
}

func pinPerms(login string) *gossh.Permissions {
	return &gossh.Permissions{Extensions: map[string]string{pinExt: login}}
}

// pinDialog asks for the PIN of the login of user, or lets a new player
// choose a PIN. It returns the login when the player proved the PIN. addr
// is the remote address of the client, for the wrong-PIN limits and the
// host log.
func (a *auth) pinDialog(user, addr string, st *authState, ch gossh.KeyboardInteractiveChallenge) (string, bool) {
	say := func(text string) { _, _ = ch("", text, nil, nil) }
	ask := func(note, prompt string) (string, bool) {
		ans, err := ch("", note, []string{prompt}, []bool{false})
		if err != nil || len(ans) != 1 {
			return "", false
		}
		return ans[0], true
	}

	if !st.left() {
		st.end() // the client tries again after the last try
		return "", false
	}
	login, ok := pinLogin(user)
	if !ok {
		say(textBadLogin)
		st.end()
		return "", false
	}
	note := appName // the instruction line above the next prompt
	// Each round uses a try, except a round after a race with another
	// connection that made the same account. The limit stops a loop if
	// the store does not agree with itself.
	for round := 0; round < 2*maxPINTries+2 && st.left(); round++ {
		if a.store.PINAccountExists(login) {
			pin, ok := ask(note, `PIN for "`+login+`": `)
			if !ok || !st.use() {
				return "", false
			}
			switch err := a.store.CheckPINFrom(login, pin, addr); {
			case err == nil:
				return login, true
			case errors.Is(err, server.ErrPINLocked):
				say(textPINLocked)
				st.end()
				return "", false
			case errors.Is(err, server.ErrRoomClosed):
				say(textRoomClosed)
				return "", false
			}
			note = textWrongPIN
			continue
		}

		pin, ok := ask(note, `New player "`+login+`". Choose a 4-digit PIN: `)
		if !ok {
			return "", false
		}
		if !validPIN(pin) {
			st.use()
			note = textPINFormat
			continue
		}
		if server.WeakPIN(pin) {
			st.use()
			note = textWeakPIN
			continue
		}
		again, ok := ask("", "Type the PIN again: ")
		if !ok {
			return "", false
		}
		if again != pin {
			st.use()
			note = textPINMatch
			continue
		}
		switch err := a.store.CreatePINAccount(login, pin); {
		case err == nil:
			return login, true
		case errors.Is(err, server.ErrNameInUse):
			// Another connection made the account at the same time.
			// The next round asks for its PIN.
			note = appName
		case errors.Is(err, server.ErrPINFormat):
			st.use()
			note = textPINFormat
		case errors.Is(err, server.ErrWeakPIN):
			st.use()
			note = textWeakPIN
		case errors.Is(err, server.ErrRoomClosed):
			say(textRoomClosed)
			return "", false
		default:
			say("Cannot make the player: " + err.Error())
			return "", false
		}
	}
	say(textNoTries)
	st.end()
	return "", false
}

// pinLogin returns the PIN login for an SSH user name: lower case, trimmed,
// 1 to 32 runes, printable only.
func pinLogin(user string) (string, bool) {
	login := strings.ToLower(strings.TrimSpace(user))
	if login == "" || !utf8.ValidString(login) || utf8.RuneCountInString(login) > maxLoginRunes {
		return "", false
	}
	for _, r := range login {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return login, true
}

// validPIN reports whether pin is exactly 4 ASCII digits.
func validPIN(pin string) bool {
	if len(pin) != 4 {
		return false
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return false
		}
	}
	return true
}

// identity returns the room identity of a proven connection: "key:<fp>"
// or "pin:<login>". ok is false when the permissions carry neither.
func identity(perms *gossh.Permissions) (id string, keyless, ok bool) {
	if perms == nil {
		return "", false, false
	}
	if fp := perms.Extensions[keyExt]; fp != "" {
		return "key:" + fp, false, true
	}
	if login := perms.Extensions[pinExt]; login != "" {
		return "pin:" + login, true, true
	}
	return "", false, false
}
