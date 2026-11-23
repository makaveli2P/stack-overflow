package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/x/ansi"
	gossh "golang.org/x/crypto/ssh"

	"github.com/makaveli2p/stack-overflow/internal/server"
	"github.com/makaveli2p/stack-overflow/internal/shot"
)

// Batch 13: the screenshot server and the links of the P key.

// freePort returns a TCP port of 127.0.0.1 that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestShotPortFlag(t *testing.T) {
	o, err := parseServerFlags(nil, env(nil), io.Discard)
	if err != nil || o.shotPort != 2280 {
		t.Fatalf("default -shot-port = %d, %v; want 2280", o.shotPort, err)
	}
	for _, args := range [][]string{{"-shot-port", "0"}, {"-shot-port", "8080"}, {"-shot-port", "65535"}} {
		if _, err := parseServerFlags(args, env(nil), io.Discard); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"-shot-port", "-1"},
		{"-shot-port", "65536"},
		{"-shot-port", "2222"},
		{"-port", "3000", "-shot-port", "3000"},
	} {
		var errb bytes.Buffer
		if _, err := parseServerFlags(args, env(nil), &errb); err == nil {
			t.Errorf("%q: no error", args)
		} else if !strings.Contains(errb.String(), "-shot-port") {
			t.Errorf("%q: message %q does not name -shot-port", args, errb.String())
		}
	}
	t.Setenv("HOME", t.TempDir())
	if code := realMain([]string{"-shot-port", "2222"}); code != exitUsage {
		t.Errorf("-shot-port equal to -port: exit %d, want %d", code, exitUsage)
	}
	if code := realMain([]string{"-shot-port", "70000"}); code != exitUsage {
		t.Errorf("-shot-port 70000: exit %d, want %d", code, exitUsage)
	}
}

func TestShotHostPort(t *testing.T) {
	l := &shotLinks{port: 2280, lanHost: "192.168.1.20"}
	for _, c := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 2222}, "192.168.1.20:2280"},
		{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, "127.0.0.1:2280"},
		{&net.TCPAddr{IP: net.ParseIP("::ffff:10.0.0.5"), Port: 2222}, "10.0.0.5:2280"},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 2222}, "[::1]:2280"},
		{&net.TCPAddr{IP: net.ParseIP("2001:db8::7"), Port: 2222}, "[2001:db8::7]:2280"},
		{&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 2222, Zone: "en0"}, "[fe80::1%25en0]:2280"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 2222}, "192.168.1.20:2280"},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 2222}, "192.168.1.20:2280"},
		{&net.TCPAddr{Port: 2222}, "192.168.1.20:2280"},
		{nil, "192.168.1.20:2280"},
		{&net.UnixAddr{Name: "/x", Net: "unix"}, "192.168.1.20:2280"},
	} {
		if got := l.hostPort(c.addr); got != c.want {
			t.Errorf("hostPort(%v) = %q, want %q", c.addr, got, c.want)
		}
	}
	if got := urlHostPort("fe80::1%en0", 2280); got != "[fe80::1%25en0]:2280" {
		t.Errorf("urlHostPort = %q", got)
	}
}

func TestShotFileName(t *testing.T) {
	at := time.Date(2026, 11, 20, 18, 4, 5, 0, time.Local)
	for _, c := range []struct{ title, user, want string }{
		{"Stack Overflow — Devon · hand #42", "devon", "stack-overflow-Devon-20261120-180405.png"},
		{"Stack Overflow — Carol W", "carol", "stack-overflow-Carol W-20261120-180405.png"},
		{"Stack Overflow — ", "sam", "stack-overflow-sam-20261120-180405.png"},
		{"something else", "sam", "stack-overflow-sam-20261120-180405.png"},
	} {
		if got := shotFileName(c.title, c.user, at); got != c.want {
			t.Errorf("shotFileName(%q, %q) = %q, want %q", c.title, c.user, got, c.want)
		}
	}
}

// fakeAddrSession gives the parts of ssh.Session that hook uses.
type fakeAddrSession struct {
	ssh.Session
	local net.Addr
	user  string
}

func (s fakeAddrSession) LocalAddr() net.Addr { return s.local }
func (s fakeAddrSession) User() string        { return s.user }

func TestShotHook(t *testing.T) {
	store := shot.NewStore(shot.StoreConfig{MaxBytes: 1000})
	l := newShotLinks(store, 2280, "10.0.0.9")
	var got shot.Frame
	png := []byte("\x89PNG fake")
	l.render = func(f shot.Frame) ([]byte, error) { got = f; return png, nil }
	sess := fakeAddrSession{local: &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 2222}, user: "devon"}
	hook := l.hook(sess)
	f := shot.Frame{View: "hi", Width: 80, Height: 24, Dark: true, Title: "Stack Overflow — Devon"}
	links, err := hook(f)
	if err != nil {
		t.Fatal(err)
	}
	url := links.Own
	if links.Table != url {
		t.Errorf("no office address: Table = %q, want Own %q", links.Table, url)
	}
	if got != f {
		t.Errorf("render got %+v, want %+v", got, f)
	}
	if !regexp.MustCompile(`^http://192\.168\.1\.20:2280/[a-z2-7]{10}$`).MatchString(url) {
		t.Fatalf("url = %q", url)
	}
	w := &recorder{h: http.Header{}}
	r, _ := http.NewRequest(http.MethodGet, strings.TrimPrefix(url, "http://192.168.1.20:2280"), nil)
	store.Handler().ServeHTTP(w, r)
	if w.code != http.StatusOK || !bytes.Equal(w.body.Bytes(), png) {
		t.Errorf("GET %s: %d %q", url, w.code, w.body.String())
	}
	if cd := w.h.Get("Content-Disposition"); !regexp.MustCompile(`filename="stack-overflow-devon-\d{8}-\d{6}\.png"`).MatchString(cd) {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// A render error goes to the player.
	l.render = func(shot.Frame) ([]byte, error) { return nil, shot.ErrTooBig }
	if _, err := hook(f); !errors.Is(err, shot.ErrTooBig) {
		t.Errorf("render error: %v", err)
	}
	// A PNG that the store does not keep is an error, not an empty link.
	l.render = func(shot.Frame) ([]byte, error) { return make([]byte, 2000), nil }
	if links, err := hook(f); err == nil || links != (shot.Links{}) {
		t.Errorf("too large: %+v, %v", links, err)
	}
	// Screenshots off: no hook.
	var off *shotLinks
	if off.hook(sess) != nil {
		t.Error("a nil shotLinks gave a hook")
	}
}

// Batch 14: the host on 127.0.0.1 gets its own link with 127.0.0.1, and
// the chat link with the office address. The office address is found
// again for each shot.
func TestShotHookTableLink(t *testing.T) {
	store := shot.NewStore(shot.StoreConfig{})
	l := newShotLinks(store, 2280, "192.168.1.20")
	l.render = func(shot.Frame) ([]byte, error) { return []byte("\x89PNG fake"), nil }
	office := "192.168.1.20"
	calls := 0
	l.office = func() string { calls++; return office }
	f := shot.Frame{View: "hi", Width: 80, Height: 24, Title: "Stack Overflow — Host"}
	re := func(host string) *regexp.Regexp {
		return regexp.MustCompile(`^http://` + regexp.QuoteMeta(host) + `:2280(/[a-z2-7]{10})$`)
	}

	host := l.hook(fakeAddrSession{local: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, user: "host"})
	links, err := host(f)
	if err != nil {
		t.Fatal(err)
	}
	own, table := re("127.0.0.1").FindStringSubmatch(links.Own), re("192.168.1.20").FindStringSubmatch(links.Table)
	if own == nil || table == nil || own[1] != table[1] {
		t.Fatalf("host links = %+v", links)
	}

	// The Wi-Fi address changed: the next shot uses the new one.
	office = "10.1.2.3"
	links, err = host(f)
	if err != nil {
		t.Fatal(err)
	}
	if !re("127.0.0.1").MatchString(links.Own) || !re("10.1.2.3").MatchString(links.Table) {
		t.Errorf("links after the address change = %+v", links)
	}
	if calls != 2 {
		t.Errorf("office ran %d times for 2 shots", calls)
	}

	// A player on the Wi-Fi: both links use the Wi-Fi address.
	office = "192.168.1.20"
	wifi := l.hook(fakeAddrSession{local: &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 2222}, user: "bob"})
	if links, err = wifi(f); err != nil || links.Own != links.Table || !re("192.168.1.20").MatchString(links.Own) {
		t.Errorf("Wi-Fi links = %+v, %v", links, err)
	}

	// No office address: Table is Own.
	office = ""
	if links, err = host(f); err != nil || links.Table != links.Own || !re("127.0.0.1").MatchString(links.Own) {
		t.Errorf("links without an office address = %+v, %v", links, err)
	}

	// An IPv6 office address goes in brackets.
	office = "fe80::1%en0"
	if links, err = host(f); err != nil || !strings.HasPrefix(links.Table, "http://[fe80::1%25en0]:2280/") {
		t.Errorf("IPv6 office link = %+v, %v", links, err)
	}

	// Every Table link fits the chat (server.ShareShot takes 120 bytes).
	if len(links.Table) > 120 {
		t.Errorf("Table link has %d bytes", len(links.Table))
	}

	// Batch 15: the usual office link has 35 characters, so it fits the
	// chat panel at 160 columns. The longest IPv4 link has 39.
	office = "192.168.1.20"
	if links, err = host(f); err != nil || len(links.Table) != 35 {
		t.Errorf("office link %q has %d characters, want 35 (%v)", links.Table, len(links.Table), err)
	}
	office = "255.255.255.255"
	l.port = 65535
	if links, err = host(f); err != nil || len(links.Table) != 39 {
		t.Errorf("longest IPv4 link %q has %d characters, want 39 (%v)", links.Table, len(links.Table), err)
	}
}

// startShotServer finds the office address of -host for each shot.
func TestStartShotServerOffice(t *testing.T) {
	o := options{host: "127.0.0.1", shotPort: freePort(t)}
	l, srv := startShotServer(o, "127.0.0.1")
	if l == nil {
		t.Fatal("no screenshot server")
	}
	defer stopShotServer(srv)
	if l.office == nil {
		t.Fatal("no office lookup")
	}
	if got := l.office(); got != "127.0.0.1" {
		t.Errorf("office for -host 127.0.0.1 = %q", got)
	}
	if got, want := officeHost(""), lanIP(t); want != "" && got == "" {
		t.Errorf("officeHost(\"\") = %q; this machine has %s", got, want)
	}
	if got := officeHost("10.0.0.5"); got != "10.0.0.5" {
		t.Errorf("officeHost(10.0.0.5) = %q", got)
	}
	if a, err := lanAddresses(""); err == nil {
		for _, ip := range a {
			if p := net.ParseIP(ip); p == nil || p.To4() == nil || p.IsLoopback() || p.IsLinkLocalUnicast() {
				t.Errorf("lanAddresses gave %q", ip)
			}
		}
	}
}

// The 404 page tells a player why an old link from the chat fails.
func TestShotServer404Text(t *testing.T) {
	o := options{host: "127.0.0.1", shotPort: freePort(t)}
	_, srv := startShotServer(o, "127.0.0.1")
	if srv == nil {
		t.Fatal("no screenshot server")
	}
	defer stopShotServer(srv)
	res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(o.shotPort) + "/abcdefghjk")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || strings.TrimSpace(string(body)) != "This screenshot expired or does not exist." {
		t.Errorf("GET of an unknown shot: %d %q", res.StatusCode, body)
	}
}

// recorder is a small http.ResponseWriter.
type recorder struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.h }
func (r *recorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(p)
}
func (r *recorder) WriteHeader(c int) { r.code = c }

func TestStartShotServer(t *testing.T) {
	o := options{host: "127.0.0.1", shotPort: 0}
	if l, srv := startShotServer(o, "127.0.0.1"); l != nil || srv != nil {
		t.Error("-shot-port 0 started a server")
	}

	// A port in use: no screenshots, and no error.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	o.shotPort = busy.Addr().(*net.TCPAddr).Port
	if l, srv := startShotServer(o, "127.0.0.1"); l != nil || srv != nil {
		t.Error("a busy port gave a screenshot server")
	}

	o.shotPort = freePort(t)
	l, srv := startShotServer(o, "127.0.0.1")
	if l == nil || srv == nil {
		t.Fatal("no screenshot server on a free port")
	}
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("server limits not set: %+v", srv)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(o.shotPort)
	path := l.store.Put([]byte("\x89PNG x"), "x")
	for p, want := range map[string]int{path: 200, "/": 404, "/s/": 404, "/s/../" + path[1:]: 404, "/s/" + path[1:] + ".png": 404} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", p, res.StatusCode, want)
		}
	}
	stopShotServer(srv)
	if _, err := http.Get(base + path); err == nil {
		t.Error("the screenshot server answers after the stop")
	}
	stopShotServer(nil) // no server: nothing to do
}

func TestWelcomeBannerScreenshots(t *testing.T) {
	o, _ := parseServerFlags([]string{"-host", "10.0.0.5"}, env(nil), io.Discard)
	var b bytes.Buffer
	printWelcome(&b, o, "/s.json", nil, nil)
	if !strings.Contains(b.String(), "Screenshots: off") {
		t.Errorf("banner without a screenshot server:\n%s", b.String())
	}
	o.shots = newShotLinks(shot.NewStore(shot.StoreConfig{}), 2280, "10.0.0.5")
	b.Reset()
	printWelcome(&b, o, "/s.json", nil, nil)
	if !strings.Contains(b.String(), "Screenshots: http://10.0.0.5:2280\n") {
		t.Errorf("banner lacks the screenshot address:\n%s", b.String())
	}
}

// run starts the screenshot server with the SSH server and stops it at
// the end. A busy screenshot port does not stop the game.
func TestRunShotServer(t *testing.T) {
	o := testRunOptions(t)
	done := startRun(t, o)
	base := "http://127.0.0.1:" + strconv.Itoa(o.shotPort)
	res, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("the screenshot server does not answer: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET /: %d, want 404", res.StatusCode)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitRun(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := http.Get(base + "/"); err == nil {
		t.Error("the screenshot server answers after run ended")
	}

	o = testRunOptions(t)
	busy, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.shotPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	done = startRun(t, o) // fails the test when run stops early
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitRun(t, done); err != nil {
		t.Fatalf("run with a busy screenshot port: %v", err)
	}
}

// Over SSH: P shows a link with the address that the client used, and
// the link gives a PNG. Between hands the link also goes to the chat.
func TestScreenshotOverSSH(t *testing.T) {
	store := shot.NewStore(shot.StoreConfig{})
	hl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: store.Handler(), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(hl) }()
	t.Cleanup(func() { _ = hs.Close() })
	shotPort := hl.Addr().(*net.TCPAddr).Port

	room, err := server.NewRoom(server.Config{Seats: 4, SmallBlind: 5, BigBlind: 10, StartingStack: 1000, NextHandDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := options{host: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port, shotPort: shotPort}
	o.shots = newShotLinks(store, shotPort, "192.0.2.1")
	srv, err := newSSHServer(o, filepath.Join(t.TempDir(), "hk"), room)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); cancel() })

	c := keyAuthClient(t, ln.Addr().String(), "shooter")
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var out syncBuffer
	s.Stdout = &out
	in, _ := s.StdinPipe()
	if err := s.RequestPty("xterm-256color", 40, 160, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the colour query of wish times out
	_, _ = in.Write([]byte("x"))        // its leaked reader eats one key
	time.Sleep(300 * time.Millisecond)
	_, _ = in.Write([]byte("\r")) // save the name on the welcome screen
	time.Sleep(500 * time.Millisecond)
	_, _ = in.Write([]byte("p"))

	re := regexp.MustCompile(`http://127\.0\.0\.1:` + strconv.Itoa(shotPort) + `/[a-z2-7]{10}`)
	var url string
	deadline := time.Now().Add(10 * time.Second)
	for url == "" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		url = re.FindString(ansi.Strip(out.String()))
	}
	if url == "" {
		t.Fatalf("no link on the screen after P:\n%s", ansi.Strip(out.String()))
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" || !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("GET %s: %d %q, %d bytes", url, res.StatusCode, res.Header.Get("Content-Type"), len(body))
	}
	if store.Len() != 1 {
		t.Errorf("store has %d shots, want 1", store.Len())
	}

	// Batch 14: no hand runs and the player has no cards, so the shot
	// goes to the chat. Another player sees the line with the link.
	if !strings.Contains(ansi.Strip(out.String()), "Screenshot shared in the chat: "+url) {
		t.Errorf("no shared notice on the screen:\n%s", ansi.Strip(out.String()))
	}
	watcher, err := room.Connect("key:watcher", "watcher")
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	select {
	case snap := <-watcher.Updates():
		var line server.ChatLine
		for _, c := range snap.Chat {
			if c.Shot != "" {
				line = c
			}
		}
		if line.Shot != url || line.Text != "shared a screenshot" {
			t.Errorf("chat of another player = %+v", snap.Chat)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot for another player")
	}
}
