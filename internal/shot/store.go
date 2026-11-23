package shot

import (
	"crypto/rand"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store defaults.
const (
	defaultTTL      = 30 * time.Minute
	defaultMaxShots = 200
	defaultMaxBytes = 64 << 20
)

// A token has tokenLen characters of lowercase base32 (a-z, 2-7). Each
// character holds 5 random bits, so a token holds 50 bits (batch 15).
const (
	tokenLen      = 10
	tokenAlphabet = "abcdefghijklmnopqrstuvwxyz234567"
	pngSuffix     = ".png"
	maxNameLen    = 100
)

// entry is one stored shot.
type entry struct {
	token string
	png   []byte
	name  string
	at    time.Time
}

// store keeps the shots in a map, and in the order of their put for the
// eviction. The oldest shot is first in order.
type store struct {
	ttl      time.Duration
	maxShots int
	maxBytes int
	now      func() time.Time

	mu    sync.Mutex
	shots map[string]*entry
	order []*entry
	bytes int
}

func newStore(cfg StoreConfig) *store {
	s := &store{ttl: cfg.TTL, maxShots: cfg.MaxShots, maxBytes: cfg.MaxBytes, now: cfg.Now, shots: map[string]*entry{}}
	if s.ttl <= 0 {
		s.ttl = defaultTTL
	}
	if s.maxShots <= 0 {
		s.maxShots = defaultMaxShots
	}
	if s.maxBytes <= 0 {
		s.maxBytes = defaultMaxBytes
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// put stores a copy of png and returns its path. A PNG that is larger
// than the byte limit alone is not stored, and put returns "".
//
// The copy has the exact size. png often comes from a bytes.Buffer, and
// its backing array can be almost 2x its length. The byte limit counts
// len, so a kept backing array would let the memory grow past the limit.
func (s *store) put(png []byte, name string) string {
	if len(png) == 0 || len(png) > s.maxBytes {
		return ""
	}
	kept := make([]byte, len(png))
	copy(kept, png)
	e := &entry{png: kept, name: safeName(name)}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.expire(now)
	e.at = now
	for {
		e.token = newToken()
		if _, used := s.shots[e.token]; !used {
			break
		}
	}
	s.shots[e.token] = e
	s.order = append(s.order, e)
	s.bytes += len(png)
	for len(s.order) > s.maxShots || s.bytes > s.maxBytes {
		s.drop()
	}
	return "/" + e.token
}

// get returns the shot with the token, or nil when the token is unknown
// or the shot expired.
func (s *store) get(token string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now())
	return s.shots[token]
}

func (s *store) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now())
	return len(s.order)
}

// expire removes the shots that are older than the TTL. The caller holds
// the lock.
func (s *store) expire(now time.Time) {
	for len(s.order) > 0 && now.Sub(s.order[0].at) >= s.ttl {
		s.drop()
	}
}

// drop removes the oldest shot. The caller holds the lock.
func (s *store) drop() {
	e := s.order[0]
	s.order[0] = nil
	s.order = s.order[1:]
	delete(s.shots, e.token)
	s.bytes -= len(e.png)
}

// newToken returns 50 random bits as 10 characters of lowercase base32.
// crypto/rand.Read never returns an error; it stops the program when the
// system has no random source.
func newToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint64(b[:])
	var t [tokenLen]byte
	for i := range t {
		t[i] = tokenAlphabet[v&31]
		v >>= 5
	}
	return string(t[:])
}

// validToken reports whether t has the form of a token: 10 characters of
// lowercase base32.
func validToken(t string) bool {
	if len(t) != tokenLen {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// safeName makes the file name for the Content-Disposition header. It
// keeps only a-z, 0-9 and "-" (upper case becomes lower case), joins
// repeated dashes, cuts the name to maxNameLen characters and adds ".png".
func safeName(name string) string {
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, pngSuffix)
	var b strings.Builder
	dash := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
			dash = false
		case c == '-':
			if !dash && b.Len() > 0 {
				b.WriteByte(c)
			}
			dash = true
		}
		if b.Len() >= maxNameLen {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "screenshot"
	}
	return out + pngSuffix
}

func (s *store) handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// serve answers GET and HEAD for "/<token>". The path must be exactly
// that: no prefix, no suffix and no other characters. Every other request
// (the old "/s/<token>.png" path too) gets the same 404, so that the
// answer tells nothing about the store. It never logs the token.
func (s *store) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		notFound(w)
		return
	}
	token, ok := strings.CutPrefix(r.URL.Path, "/")
	if !ok || r.URL.RawPath != "" || !validToken(token) {
		notFound(w)
		return
	}
	e := s.get(token)
	if e == nil {
		notFound(w)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", `inline; filename="`+e.name+`"`)
	h.Set("Content-Length", strconv.Itoa(len(e.png)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(e.png)
	}
}

// notFoundText is the body of every 404. A shared link in the chat
// expires after the store TTL, so the text names that case first.
const notFoundText = "This screenshot expired or does not exist."

func notFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, notFoundText, http.StatusNotFound)
}
