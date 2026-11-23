package server

import (
	"errors"
	"testing"
	"time"
)

var testTime = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

func TestCheckName(t *testing.T) {
	good := map[string]string{
		"alice":        "alice",
		"  Bob  ":      "Bob",
		"John 2":       "John 2",
		"a.b-c_d":      "a.b-c_d",
		"abcdefghijkl": "abcdefghijkl", // 12 runes
		"Иван":         "Иван",
		"राम":          "राम", // Devanagari with a vowel sign
		"山田たろう":        "山田たろう",
		"ａｌｉｃｅ":        "ａｌｉｃｅ",
		"Zoë":          "Zoë",
		"Zoe\u0308":    "Zoë", // NFC
		"007":          "007",
	}
	for in, want := range good {
		got, err := checkName(in)
		if err != nil || got != want {
			t.Errorf("checkName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "abcdefghijklm", "a@b", "a\tb", "a\x1b[31m", "😀", "a/b",
		"\u0430lice", // Cyrillic а in a Latin name
		"Ivan Иван",  // two scripts
		"\u0308a",    // starts with a combining mark
		"x\u200by",   // zero-width space
	}
	for _, in := range bad {
		if got, err := checkName(in); !errors.Is(err, ErrBadName) {
			t.Errorf("checkName(%q) = %q, %v; want ErrBadName", in, got, err)
		}
	}
}

func TestNameKey(t *testing.T) {
	same := [][2]string{
		{"alice", "ALICE"},
		{"alice", "ａｌｉｃｅ"},
		{"Straße", "STRASSE"},
		{"Zoë", "zoe\u0308"},
		{" bob", "Bob "},
	}
	for _, p := range same {
		if nameKey(p[0]) != nameKey(p[1]) {
			t.Errorf("nameKey(%q) != nameKey(%q)", p[0], p[1])
		}
	}
	if nameKey("alice") == nameKey("alise") {
		t.Error("different names have one key")
	}
}

func TestSuggestName(t *testing.T) {
	tests := map[string]string{
		"bob":                   "bob",
		"bob@laptop":            "boblaptop",
		"":                      defaultName,
		"@@@":                   defaultName,
		"averyveryverylongname": "averyveryver",
		"\u0430lice":            defaultName, // mixed scripts
		"Devon K.":              "Devon K.",
	}
	for in, want := range tests {
		if got := suggestName(in); got != want {
			t.Errorf("suggestName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	st := newState(testTime)
	st.Accounts["p1"] = &account{Name: "Alice"}
	st.Accounts["p2"] = &account{Name: "alice2"}
	st.Accounts["p3"] = &account{Name: "abcdefghijkl"}
	if got := st.uniqueName("alice", ""); got != "alice3" {
		t.Errorf("uniqueName(alice) = %q, want alice3", got)
	}
	if got := st.uniqueName("alice", "p1"); got != "alice" {
		t.Errorf("own name: %q", got)
	}
	if got := st.uniqueName("ABCDEFGHIJKL", ""); got != "ABCDEFGHIJK2" {
		t.Errorf("long name: %q", got)
	}
}

func TestNormLoginAndPIN(t *testing.T) {
	if got, err := normLogin("  Bob "); err != nil || got != "bob" {
		t.Errorf("normLogin = %q, %v", got, err)
	}
	if _, err := normLogin(""); !errors.Is(err, ErrBadName) {
		t.Errorf("empty login: %v", err)
	}
	if _, err := normLogin("abcdefghijklmnopqrstuvwxyz0123456"); !errors.Is(err, ErrBadName) {
		t.Errorf("33-rune login: %v", err)
	}
	for pin, ok := range map[string]bool{"4826": true, "0000": true, "123": false, "12345": false, "12a4": false, "１２３４": false, "": false} {
		if pinFormatOK(pin) != ok {
			t.Errorf("pinFormatOK(%q) = %v", pin, !ok)
		}
	}
}

func TestParseIdentity(t *testing.T) {
	tests := []struct {
		raw  string
		want identity
		err  bool
	}{
		{"key:SHA256:abc", identity{value: "SHA256:abc"}, false},
		{"SHA256:abc", identity{value: "SHA256:abc"}, false},
		{"pin:Bob", identity{pin: true, value: "bob"}, false},
		{"user:Bob", identity{pin: true, value: "bob", legacy: true}, false},
		{"key:", identity{}, true},
		{"", identity{}, true},
		{"pin:", identity{}, true},
	}
	for _, tc := range tests {
		got, err := parseIdentity(tc.raw)
		if (err != nil) != tc.err || (!tc.err && got != tc.want) {
			t.Errorf("parseIdentity(%q) = %+v, %v", tc.raw, got, err)
		}
	}
}

// Invisible runes (variation selectors, CGJ, Hangul fillers) must not make a
// name that draws exactly like another name, or as blank (auth-3).
func TestInvisibleMarkNameLookalike(t *testing.T) {
	h := newHarness(t, testConfig(), nil)
	alice := h.connectWith("key:SHA256:alice", "alice", ConnectOptions{})
	must(t, alice.SetName("Alice"))
	mallory := h.connectWith("key:SHA256:mallory", "mallory", ConnectOptions{})
	for _, n := range []string{"Alice️", "Alice︀", "Alic͏e", "Alice\U000E0100", "Alice‍"} {
		if err := mallory.SetName(n); !errors.Is(err, ErrNameInUse) && !errors.Is(err, ErrBadName) {
			t.Errorf("SetName(%+q) = %v; the name draws as \"Alice\", want ErrNameInUse or ErrBadName", n, err)
		}
	}
	// A Hangul filler is a "letter" that draws as nothing: a blank name.
	for _, n := range []string{"ㅤ", "ᅟᅠ", "ﾠ"} {
		if err := mallory.SetName(n); !errors.Is(err, ErrBadName) {
			t.Errorf("SetName(%+q) = %v; the name draws as blank, want ErrBadName", n, err)
		}
	}
	// Control: a plain case-folded duplicate is rejected.
	if err := mallory.SetName("alice"); !errors.Is(err, ErrNameInUse) {
		t.Errorf("control: SetName(alice) = %v", err)
	}
	// A name stored with an ignorable rune (an older file) still equals the
	// name that it looks like.
	if nameKey("Alice️") != nameKey("alice") {
		t.Error("nameKey keeps a variation selector")
	}
	if got := suggestName("Alice️ㅤ"); got != "Alice" {
		t.Errorf("suggestName = %q, want \"Alice\"", got)
	}
}
