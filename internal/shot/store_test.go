package shot

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a fake time source for the store.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestStore(cfg StoreConfig) (*Store, *clock) {
	c := &clock{t: time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)}
	cfg.Now = c.now
	return NewStore(cfg), c
}

// fakePNG returns n bytes that start with the PNG signature.
func fakePNG(n int) []byte {
	b := bytes.Repeat([]byte{'x'}, n)
	copy(b, "\x89PNG\r\n\x1a\n")
	return b
}

var pathRE = regexp.MustCompile(`^/[a-z2-7]{10}$`)

// do sends one request to the handler.
func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "http://shots.test/", nil)
	r.URL.Path = target
	r.RequestURI = target
	h.ServeHTTP(w, r)
	return w
}

func want404(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusNotFound {
		t.Errorf("%s: status %d, want 404", what, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("%s: Content-Type %q, want text/plain", what, ct)
	}
	if got := w.Body.String(); got != "This screenshot expired or does not exist.\n" {
		t.Errorf("%s: body %q", what, got)
	}
}

func TestPutGet(t *testing.T) {
	s, _ := newTestStore(StoreConfig{})
	png := fakePNG(500)
	p := s.Put(png, "stack-overflow-Devon-20261120-180000.png")
	if !pathRE.MatchString(p) {
		t.Fatalf("Put = %q, want /<10 chars of base32>", p)
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}
	h := s.Handler()
	w := do(h, http.MethodGet, p)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("GET %s: status %d, %d bytes", p, w.Code, w.Body.Len())
	}
	for k, v := range map[string]string{
		"Content-Type":           "image/png",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Content-Disposition":    `inline; filename="stack-overflow-devon-20261120-180000.png"`,
		"Content-Length":         "500",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	head := do(h, http.MethodHead, p)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "image/png" {
		t.Errorf("HEAD: status %d, body %d bytes, type %q", head.Code, head.Body.Len(), head.Header().Get("Content-Type"))
	}
	// Two puts get two tokens.
	if q := s.Put(png, "x"); q == p || !pathRE.MatchString(q) {
		t.Errorf("second Put = %q (first %q)", q, p)
	}
}

func TestPutRejectsEmptyAndTooBig(t *testing.T) {
	s, _ := newTestStore(StoreConfig{MaxBytes: 1000})
	if p := s.Put(nil, "x"); p != "" {
		t.Errorf("Put(nil) = %q", p)
	}
	if p := s.Put(fakePNG(1001), "x"); p != "" {
		t.Errorf("Put of 1001 bytes with MaxBytes 1000 = %q", p)
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
}

func TestNotFound(t *testing.T) {
	s, _ := newTestStore(StoreConfig{})
	p := s.Put(fakePNG(100), "x")
	token := strings.TrimPrefix(p, "/")
	h := s.Handler()
	for _, path := range []string{
		"", "/", "/s/", "/s", "/.png",
		"/" + strings.Repeat("a", 10), // unknown token (or a lucky guess)
		"/s/" + token,                 // the old paths of batch 13
		"/s/" + token + ".png",
		"/" + token + ".png",
		"/" + token + ".PNG",
		"/" + token + "/",
		"/" + token + "?",
		"/" + token[:9], // a prefix of the token
		"/" + token[:5],
		"/" + token + "a", // longer
		"/" + strings.ToUpper(token),
		"//" + token,
		"/x/" + token,
		"/../" + token,
		"/./" + token,
		"/%2e%2e/" + token,
		"/../../etc/passwd",
		"/" + token + "\x00",
		" /" + token,
		token,
		"/" + token[:9] + "0", // 0 is not base32
		"/" + token[:9] + "1",
		"/" + token[:9] + "8",
	} {
		want404(t, do(h, http.MethodGet, path), "GET "+path)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodPatch, "TRACE", "PROPFIND"} {
		want404(t, do(h, m, p), m+" of a good token")
	}
	// The good token still works after all of that.
	if w := do(h, http.MethodGet, p); w.Code != http.StatusOK {
		t.Errorf("GET %s after bad requests: %d", p, w.Code)
	}
}

func TestExpiry(t *testing.T) {
	s, c := newTestStore(StoreConfig{})
	p := s.Put(fakePNG(100), "x")
	h := s.Handler()
	c.add(29*time.Minute + 59*time.Second)
	if w := do(h, http.MethodGet, p); w.Code != http.StatusOK {
		t.Fatalf("GET before 30 minutes: %d", w.Code)
	}
	c.add(time.Second)
	want404(t, do(h, http.MethodGet, p), "GET after 30 minutes")
	if s.Len() != 0 {
		t.Errorf("Len after expiry = %d", s.Len())
	}

	// A custom TTL, and expiry on put.
	s, c = newTestStore(StoreConfig{TTL: time.Minute})
	old := s.Put(fakePNG(100), "old")
	c.add(2 * time.Minute)
	if s.s.bytes != 100 {
		t.Fatalf("bytes before put = %d", s.s.bytes)
	}
	fresh := s.Put(fakePNG(50), "new")
	if s.s.bytes != 50 || len(s.s.shots) != 1 {
		t.Errorf("put did not remove the expired shot: %d bytes, %d shots", s.s.bytes, len(s.s.shots))
	}
	want404(t, do(s.Handler(), http.MethodGet, old), "expired shot")
	if w := do(s.Handler(), http.MethodGet, fresh); w.Code != http.StatusOK {
		t.Errorf("new shot: %d", w.Code)
	}
}

func TestEvictByCount(t *testing.T) {
	s, c := newTestStore(StoreConfig{MaxShots: 3})
	var paths []string
	for i := range 5 {
		paths = append(paths, s.Put(fakePNG(10), fmt.Sprint(i)))
		c.add(time.Second)
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	h := s.Handler()
	for i, p := range paths {
		got := do(h, http.MethodGet, p).Code
		if want := map[bool]int{true: 404, false: 200}[i < 2]; got != want {
			t.Errorf("shot %d: %d, want %d", i, got, want)
		}
	}
	// The default limit is 200.
	s, _ = newTestStore(StoreConfig{})
	for range 205 {
		s.Put(fakePNG(1), "x")
	}
	if s.Len() != 200 {
		t.Errorf("default limit: Len = %d, want 200", s.Len())
	}
}

func TestEvictByBytes(t *testing.T) {
	s, _ := newTestStore(StoreConfig{MaxBytes: 1000})
	a := s.Put(fakePNG(400), "a")
	b := s.Put(fakePNG(400), "b")
	c := s.Put(fakePNG(400), "c") // 1200 > 1000: a goes
	h := s.Handler()
	want404(t, do(h, http.MethodGet, a), "oldest shot over the byte limit")
	for _, p := range []string{b, c} {
		if w := do(h, http.MethodGet, p); w.Code != http.StatusOK {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	if s.s.bytes != 800 {
		t.Errorf("bytes = %d, want 800", s.s.bytes)
	}
	d := s.Put(fakePNG(1000), "d") // exactly the limit: b and c go
	if s.Len() != 1 || s.s.bytes != 1000 {
		t.Errorf("after a full-size put: %d shots, %d bytes", s.Len(), s.s.bytes)
	}
	if w := do(h, http.MethodGet, d); w.Code != http.StatusOK {
		t.Errorf("GET full-size shot: %d", w.Code)
	}
	if def := newStore(StoreConfig{}); def.maxBytes != 64<<20 || def.ttl != 30*time.Minute || def.maxShots != 200 {
		t.Errorf("defaults: %d bytes, %s, %d shots", def.maxBytes, def.ttl, def.maxShots)
	}
}

// TestPutKeepsExactSize checks that the store keeps a copy of exact size.
// Render returns the bytes of a bytes.Buffer, whose backing array can be
// almost 2x the length. The byte limit counts len, so the store must not
// keep the larger array.
func TestPutKeepsExactSize(t *testing.T) {
	s, _ := newTestStore(StoreConfig{})
	big := make([]byte, 500, 4096)
	copy(big, fakePNG(500))
	p := s.Put(big, "x")
	e := s.s.get(strings.TrimPrefix(p, "/"))
	if e == nil {
		t.Fatalf("Put = %q, and get finds nothing", p)
	}
	if len(e.png) != 500 || cap(e.png) != 500 {
		t.Errorf("stored len %d cap %d, want 500 and 500", len(e.png), cap(e.png))
	}
	// The store owns its copy: a change to the caller's slice does not
	// change the shot.
	big[0] ^= 0xff
	if e.png[0] == big[0] {
		t.Error("the stored PNG shares its bytes with the caller's slice")
	}

	// A real render, through the public API.
	b, err := Render(Frame{View: strings.Repeat("♠ ─ ₹ text\n", 20), Width: 80, Height: 24, Dark: true})
	if err != nil {
		t.Fatal(err)
	}
	p = s.Put(b, "y")
	e = s.s.get(strings.TrimPrefix(p, "/"))
	if e == nil {
		t.Fatalf("Put of a render = %q, and get finds nothing", p)
	}
	if cap(e.png) != len(b) {
		t.Errorf("rendered shot: cap %d, want %d", cap(e.png), len(b))
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"stack-overflow-Devon-20261120-180000.png": "stack-overflow-devon-20261120-180000.png",
		"stack-overflow-Carol W-20261120-180000":   "stack-overflow-carolw-20261120-180000.png",
		`a"b\c/d;e` + "\r\nX-Evil: 1":              "abcdex-evil1.png",
		"../../etc/passwd":                         "etcpasswd.png",
		"Ünïcødé 🂡 ♠":                              "ncd.png",
		"":                                         "screenshot.png",
		"🂡♠":                                       "screenshot.png",
		"---a---b---":                              "a-b.png",
		"x.PNG":                                    "x.png",
		strings.Repeat("a", 300):                   strings.Repeat("a", 100) + ".png",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
	// The header always holds only safe characters.
	s, _ := newTestStore(StoreConfig{})
	p := s.Put(fakePNG(10), "a\"; filename*=UTF-8''evil.exe")
	cd := do(s.Handler(), http.MethodGet, p).Header().Get("Content-Disposition")
	if !regexp.MustCompile(`^inline; filename="[a-z0-9-]+\.png"$`).MatchString(cd) {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestTokens(t *testing.T) {
	seen := map[string]bool{}
	for range 2000 {
		tok := newToken()
		if !validToken(tok) {
			t.Fatalf("token %q is not 10 characters of lowercase base32", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q came twice", tok)
		}
		seen[tok] = true
	}
	// Every base32 character shows up: the tokens use the whole alphabet.
	all := strings.Join(func() []string {
		var out []string
		for k := range seen {
			out = append(out, k)
		}
		return out
	}(), "")
	for _, c := range "abcdefghijklmnopqrstuvwxyz234567" {
		if !strings.ContainsRune(all, c) {
			t.Errorf("no token has %q", c)
		}
	}
}

func TestConcurrent(t *testing.T) {
	s, c := newTestStore(StoreConfig{MaxShots: 50, MaxBytes: 50 * 200})
	h := s.Handler()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				p := s.Put(fakePNG(100+i%100), fmt.Sprintf("g%d-%d", g, i))
				w := do(h, http.MethodGet, p)
				if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
					t.Errorf("GET: %d", w.Code)
				}
				_ = s.Len()
				if i%50 == 0 {
					c.add(time.Second)
				}
			}
		}()
	}
	wg.Wait()
	if n := s.Len(); n > 50 {
		t.Errorf("Len = %d, over the limit of 50", n)
	}
	if b := s.s.bytes; b > 50*200 {
		t.Errorf("bytes = %d, over the limit", b)
	}
}

// The real HTTP stack: the server cleans no path for the handler, and a
// GET returns the bytes.
func TestHandlerOverHTTP(t *testing.T) {
	s, _ := newTestStore(StoreConfig{})
	png := fakePNG(2048)
	p := s.Put(png, "real")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + p)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Errorf("GET %s: %d, %d bytes", p, res.StatusCode, len(body))
	}
	for _, path := range []string{"/s/../" + p[1:], "/", "/s/", "/s/" + p[1:] + ".png", "/%" + fmt.Sprintf("%02x", p[1]) + p[2:]} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, res.StatusCode)
		}
	}
}

// Batch 15: a token holds 50 random bits in 10 characters. The first
// character also gets every value, so the low bits are random too.
func TestTokenBits(t *testing.T) {
	if tokenLen*5 != 50 {
		t.Fatalf("a token holds %d bits, want 50", tokenLen*5)
	}
	for i := range tokenLen {
		seen := map[byte]bool{}
		for range 3000 {
			seen[newToken()[i]] = true
		}
		if len(seen) != 32 {
			t.Errorf("character %d takes %d values, want 32", i, len(seen))
		}
	}
}

// Batch 15: a link with the usual office address fits in 35 characters.
func TestShortLinkLength(t *testing.T) {
	s, _ := newTestStore(StoreConfig{})
	p := s.Put(fakePNG(10), "x")
	if len(p) != 11 {
		t.Fatalf("path %q has %d bytes, want 11", p, len(p))
	}
	if link := "http://192.168.1.20:2280" + p; len(link) != 35 {
		t.Errorf("link %q has %d characters, want 35", link, len(link))
	}
}
