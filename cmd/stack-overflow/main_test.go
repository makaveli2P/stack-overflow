package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makaveli2p/stack-overflow/internal/server"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	o, err := parseServerFlags(nil, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.stack != 1000 || o.sb != 5 || o.bb != 10 || o.nextHand != 20*time.Second ||
		o.chipsPerUnit != 5 || o.currency != "₹" || o.timeout != 60*time.Second || o.runoutDelay != 3*time.Second || o.seats != 9 || o.port != 2222 {
		t.Fatalf("defaults %+v", o)
	}
	if o.state != "~/.stack-overflow/state.json" || o.hostKey != "~/.stack-overflow/host_ed25519" {
		t.Fatalf("default paths %q %q", o.state, o.hostKey)
	}
	if o.password != "" || o.admins != nil {
		t.Fatalf("password %q admins %q", o.password, o.admins)
	}
	cfg := o.roomConfig("/s.json")
	if cfg.ChipsPerUnit != 5 || cfg.Currency != "₹" || cfg.StartingStack != 1000 || cfg.StatePath != "/s.json" ||
		cfg.TurnTimeout != 60*time.Second || cfg.RunoutDelay != 3*time.Second {
		t.Fatalf("room config %+v", cfg)
	}
}

func TestRunoutDelayFlag(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"-runout-delay", "5s"}, 5 * time.Second},
		{[]string{"-runout-delay", "1500ms"}, 1500 * time.Millisecond},
		{[]string{"-runout-delay", "0"}, 0},
	} {
		o, err := parseServerFlags(c.args, env(nil), io.Discard)
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if got := o.roomConfig("").RunoutDelay; got != c.want {
			t.Errorf("%q: RunoutDelay = %s, want %s", c.args, got, c.want)
		}
	}
	for _, args := range [][]string{
		{"-runout-delay", "-1s"},
		{"-runout-delay", "x"},
		{"-timeout", "-1s"},
		{"-next-hand", "-1s"},
	} {
		var errOut bytes.Buffer
		if _, err := parseServerFlags(args, env(nil), &errOut); err == nil {
			t.Errorf("%q is accepted", args)
		} else if errOut.Len() == 0 {
			t.Errorf("%q: no message for the host", args)
		}
	}
}

func TestPasswordFromEnv(t *testing.T) {
	e := env(map[string]string{passwordEnv: "from-env"})
	o, _ := parseServerFlags(nil, e, io.Discard)
	if o.password != "from-env" {
		t.Fatalf("password %q, want the env value", o.password)
	}
	o, _ = parseServerFlags([]string{"-password", "from-flag"}, e, io.Discard)
	if o.password != "from-flag" {
		t.Fatalf("password %q, want the flag value", o.password)
	}
	// An empty flag also wins: the host can turn the password off.
	o, _ = parseServerFlags([]string{"-password="}, e, io.Discard)
	if o.password != "" {
		t.Fatalf("password %q, want none", o.password)
	}
}

func TestAdminFlag(t *testing.T) {
	fp := "SHA256:" + strings.Repeat("Ab3/", 10) + "xyz"
	o, err := parseServerFlags([]string{"-admin", " pin:Alice, " + fp + " ,,PIN:bob ", "-chips-per-unit", "4", "-currency", "$"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pin:alice", fp, "pin:bob"}; !reflect.DeepEqual(o.admins, want) {
		t.Fatalf("admins %q, want %q", o.admins, want)
	}
	cfg := o.roomConfig("")
	if !reflect.DeepEqual(cfg.Admins, o.admins) || cfg.ChipsPerUnit != 4 || cfg.Currency != "$" {
		t.Fatalf("room config %+v", cfg)
	}
	if _, err := parseServerFlags([]string{"stray"}, env(nil), io.Discard); err == nil {
		t.Fatal("a stray argument is accepted")
	}
	// A display name is not a credential any more. The error names the
	// bad entry.
	for _, bad := range []string{"Alice", "pin:alice,Bob", fp[:30], "key:" + fp} {
		var errb bytes.Buffer
		_, err := parseServerFlags([]string{"-admin", bad}, env(nil), &errb)
		if err == nil {
			t.Errorf("-admin %q is accepted", bad)
			continue
		}
		want := bad
		if i := strings.Index(bad, ","); i >= 0 {
			want = bad[i+1:]
		}
		if msg := errb.String(); !strings.Contains(msg, strconv.Quote(want)) || !strings.Contains(msg, "stack-overflow accounts") {
			t.Errorf("-admin %q: message %q", bad, msg)
		}
	}
}

func TestBadAdminExitCode(t *testing.T) {
	if code := realMain([]string{"-admin", "Alice", "-state", filepath.Join(t.TempDir(), "s.json")}); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
}

func TestWelcomeBannerAdmins(t *testing.T) {
	fpA := "SHA256:" + strings.Repeat("a", 43)
	fpB := "SHA256:" + strings.Repeat("b", 43)
	o, err := parseServerFlags([]string{"-host", "10.0.0.5", "-admin", fpA + "," + fpB + ",pin:ops,pin:nobody"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	accounts := []server.AccountInfo{
		{ID: "1", Name: "Alice", Fingerprints: []string{fpA}},
		{ID: "2", Name: "Ops Lead", Login: "ops"},
	}
	var b bytes.Buffer
	printWelcome(&b, o, "/s.json", accounts, []string{"pin:nobody"})
	want := "admins: Alice (SHA256:aaaaaaaa…), " + fpB + ", Ops Lead (pin:ops), pin:nobody (ignored) (and anybody who connects from this machine)"
	if !strings.Contains(b.String(), want) {
		t.Errorf("banner lacks %q:\n%s", want, b.String())
	}
	// B7-2: the banner warns about a pin: entry that the room ignored.
	if warn := "WARNING: no PIN profile has the login of pin:nobody"; !strings.Contains(b.String(), warn) {
		t.Errorf("banner lacks %q:\n%s", warn, b.String())
	}
}

func TestWelcomeBanner(t *testing.T) {
	o, _ := parseServerFlags([]string{"-host", "10.0.0.5"}, env(nil), io.Discard)
	var b bytes.Buffer
	printWelcome(&b, o, "/home/x/.stack-overflow/state.json", nil, nil)
	out := b.String()
	for _, want := range []string{"Stack Overflow is listening on 10.0.0.5:2222", "blinds 5/10", "buy-in 1000 chips (₹200)",
		"5 chips = ₹1", "turn clock 60s", "ssh -p 2222 yourname@10.0.0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "1m0s") {
		t.Errorf("banner prints the turn clock as 1m0s:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "office poker") {
		t.Errorf("banner uses the old name:\n%s", out)
	}
}

func TestMigrateDir(t *testing.T) {
	root := t.TempDir()
	oldDir, newDir := filepath.Join(root, ".office-poker"), filepath.Join(root, ".stack-overflow")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	write(filepath.Join(oldDir, "bank.json"), "bank")
	write(filepath.Join(oldDir, "host_ed25519"), "key")
	write(filepath.Join(oldDir, "host_ed25519.pub"), "pub")

	copied, err := migrateDir(oldDir, newDir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bank.json", "host_ed25519", "host_ed25519.pub"}; !reflect.DeepEqual(copied, want) {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	if read(filepath.Join(newDir, "state.json")) != "bank" || read(filepath.Join(newDir, "host_ed25519")) != "key" ||
		read(filepath.Join(newDir, "host_ed25519.pub")) != "pub" {
		t.Fatal("new files do not have the old content")
	}
	if fi, err := os.Stat(filepath.Join(newDir, "host_ed25519")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("host key mode: %v %v", fi.Mode(), err)
	}
	if read(filepath.Join(oldDir, "bank.json")) != "bank" {
		t.Fatal("the old file changed")
	}

	// The second start copies nothing and keeps the new files.
	write(filepath.Join(newDir, "state.json"), "new")
	copied, err = migrateDir(oldDir, newDir, true, true)
	if err != nil || len(copied) != 0 || read(filepath.Join(newDir, "state.json")) != "new" {
		t.Fatalf("second migration: %q %v %q", copied, err, read(filepath.Join(newDir, "state.json")))
	}

	// A path from a flag (not the default) gets no copy.
	newDir2 := filepath.Join(root, "other")
	copied, _ = migrateDir(oldDir, newDir2, false, true)
	if !reflect.DeepEqual(copied, []string{"host_ed25519", "host_ed25519.pub"}) || read(filepath.Join(newDir2, "state.json")) != "<missing>" {
		t.Fatalf("state flag set: copied %q", copied)
	}
	// A folder that has a host key is in use. Its missing state file means a
	// reset, so the old bank does not come back, and the .pub file of the
	// existing key is not replaced.
	newDir3 := filepath.Join(root, "third")
	write(filepath.Join(newDir3, "host_ed25519"), "mine")
	copied, _ = migrateDir(oldDir, newDir3, true, true)
	if len(copied) != 0 || read(filepath.Join(newDir3, "state.json")) != "<missing>" || read(filepath.Join(newDir3, "host_ed25519.pub")) != "<missing>" {
		t.Fatalf("existing host key: copied %q", copied)
	}
	// Nothing to copy.
	copied, err = migrateDir(filepath.Join(root, "none"), filepath.Join(root, "empty"), true, true)
	if err != nil || len(copied) != 0 {
		t.Fatalf("no old folder: %q %v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(root, "empty")); err == nil {
		t.Fatal("migration made a folder with nothing to copy")
	}
}

func TestExtensionFlags(t *testing.T) {
	o, err := parseServerFlags(nil, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.extensions != 3 || o.extensionTime != 30*time.Second {
		t.Fatalf("default extensions %d × %s, want 3 × 30s", o.extensions, o.extensionTime)
	}
	cfg := o.roomConfig("")
	if cfg.Extensions != 3 || cfg.ExtensionTime != 30*time.Second {
		t.Fatalf("room config %+v", cfg)
	}

	o, err = parseServerFlags([]string{"-extensions", "5", "-extension-time", "45s"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := o.roomConfig(""); cfg.Extensions != 5 || cfg.ExtensionTime != 45*time.Second {
		t.Fatalf("room config %+v", cfg)
	}
	// 0 turns the feature off; the time then does not matter.
	if _, err := parseServerFlags([]string{"-extensions", "0", "-extension-time", "0"}, env(nil), io.Discard); err != nil {
		t.Fatalf("-extensions 0: %v", err)
	}

	for _, args := range [][]string{
		{"-extensions", "-1"},
		{"-extension-time", "-5s"},
		{"-extensions", "2", "-extension-time", "0"},
		{"-extensions", "x"},
	} {
		var errOut bytes.Buffer
		if _, err := parseServerFlags(args, env(nil), &errOut); err == nil {
			t.Errorf("%q is accepted", args)
		} else if errOut.Len() == 0 {
			t.Errorf("%q: no message for the host", args)
		}
	}
}

func TestWelcomeBannerExtensions(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"-extensions", "0"}, false},
		{[]string{"-timeout", "0"}, false},
	} {
		o, err := parseServerFlags(append([]string{"-host", "10.0.0.5"}, c.args...), env(nil), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		printWelcome(&b, o, "/s.json", nil, nil)
		if got := strings.Contains(b.String(), "3 time extensions of 30s"); got != c.want {
			t.Errorf("%q: banner names extensions = %v, want %v\n%s", c.args, got, c.want, b.String())
		}
	}
}

func TestAutostartFlag(t *testing.T) {
	for _, c := range []struct {
		args   []string
		manual bool
		banner string
	}{
		{nil, true, "Hands start when a seated player presses G"},
		{[]string{"-autostart"}, false, "Hands start as soon as 2 players sit"},
		{[]string{"-autostart=false"}, true, "Hands start when a seated player presses G"},
	} {
		o, err := parseServerFlags(append([]string{"-host", "10.0.0.5"}, c.args...), env(nil), io.Discard)
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if got := o.roomConfig("").ManualStart; got != c.manual {
			t.Errorf("%q: ManualStart = %v, want %v", c.args, got, c.manual)
		}
		var b bytes.Buffer
		printWelcome(&b, o, "/s.json", nil, nil)
		if !strings.Contains(b.String(), c.banner) {
			t.Errorf("%q: banner has no %q:\n%s", c.args, c.banner, b.String())
		}
	}
}

func TestRabbitHuntsFlag(t *testing.T) {
	o, err := parseServerFlags(nil, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.rabbitHunts != 3 || o.roomConfig("").RabbitHunts != 3 {
		t.Fatalf("default rabbit hunts %d, room %d, want 3", o.rabbitHunts, o.roomConfig("").RabbitHunts)
	}
	for _, n := range []string{"0", "1", "7"} {
		o, err := parseServerFlags([]string{"-rabbit-hunts", n}, env(nil), io.Discard)
		if err != nil {
			t.Fatalf("-rabbit-hunts %s: %v", n, err)
		}
		if got := strconv.Itoa(o.roomConfig("").RabbitHunts); got != n {
			t.Errorf("-rabbit-hunts %s: room config %s", n, got)
		}
	}
	for _, args := range [][]string{{"-rabbit-hunts", "-1"}, {"-rabbit-hunts", "x"}} {
		var errOut bytes.Buffer
		if _, err := parseServerFlags(args, env(nil), &errOut); err == nil {
			t.Errorf("%q is accepted", args)
		} else if errOut.Len() == 0 {
			t.Errorf("%q: no message for the host", args)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code := realMain([]string{"-rabbit-hunts", "-2"}); code != exitUsage {
		t.Errorf("-rabbit-hunts -2: exit %d, want %d", code, exitUsage)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a bad flag made files in HOME: %v", entries)
	}
}

func TestWelcomeBannerRabbitHunts(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string // "" when the banner must not name rabbit hunts
	}{
		{nil, "    3 rabbit hunts for each player (key H)\n"},
		{[]string{"-rabbit-hunts", "1"}, "    1 rabbit hunt for each player (key H)\n"},
		{[]string{"-rabbit-hunts", "0"}, ""},
	} {
		o, err := parseServerFlags(append([]string{"-host", "10.0.0.5"}, c.args...), env(nil), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		printWelcome(&b, o, "/s.json", nil, nil)
		got := b.String()
		switch {
		case c.want == "" && strings.Contains(got, "rabbit hunt"):
			t.Errorf("%q: banner names rabbit hunts:\n%s", c.args, got)
		case c.want != "" && !strings.Contains(got, c.want):
			t.Errorf("%q: banner has no %q:\n%s", c.args, c.want, got)
		}
	}
}
