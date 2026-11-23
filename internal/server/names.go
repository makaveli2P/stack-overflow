package server

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maxLoginRunes = 32

// nameKey is the form that makes two names equal: NFKC normalization, then
// case folding. It also removes the default-ignorable runes (variation
// selectors, CGJ, Hangul fillers and similar) that draw as nothing, so that a
// stored name with such a rune (from before checkName rejected them) still
// equals the name that it looks like.
func nameKey(name string) string {
	name = strings.Map(func(r rune) rune {
		if ignorable(r) {
			return -1
		}
		return r
	}, name)
	return cases.Fold().String(norm.NFKC.String(strings.TrimSpace(name)))
}

// ignorable reports whether r is a Unicode default-ignorable code point:
// a rune that takes no width and draws as nothing. Variation selectors and
// U+034F COMBINING GRAPHEME JOINER are marks, and the Hangul fillers (for
// example U+3164) are letters, so the mark and letter checks alone let them
// into a name: "Alice" + U+FE0F draws exactly like "Alice", and a name of
// fillers draws as blank.
func ignorable(r rune) bool {
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r) ||
		unicode.Is(unicode.Cf, r)
}

// checkName validates a display name and returns it in NFC form. A name has
// 1..maxNameRunes runes after trimming; letters, digits, space, '-', '_'
// and '.'; and the letters of one script only (digits and the punctuation
// are always fine).
func checkName(name string) (string, error) {
	name = norm.NFC.String(strings.TrimSpace(name))
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		return "", ErrBadName
	}
	script := ""
	first := true
	for _, r := range name {
		ok, sc := nameRune(r, first)
		if !ok {
			return "", ErrBadName
		}
		first = false
		if sc == "" {
			continue
		}
		if script == "" {
			script = sc
		} else if script != sc {
			return "", ErrBadName
		}
	}
	return name, nil
}

// nameRune reports whether r may be in a name, and the script that r counts
// for ("" for digits 0-9, punctuation and combining marks).
func nameRune(r rune, first bool) (bool, string) {
	switch {
	case r == ' ' || r == '-' || r == '_' || r == '.':
		return true, ""
	case r >= '0' && r <= '9':
		return true, ""
	case r < utf8.RuneSelf:
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' {
			return true, "Latin"
		}
		return false, ""
	case ignorable(r):
		return false, ""
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return true, scriptOf(r)
	case unicode.Is(unicode.Mark, r) && !first:
		sc := scriptOf(r)
		if sc == "Inherited" {
			sc = ""
		}
		return true, sc
	}
	return false, ""
}

// scriptGroup puts scripts that one language mixes into one group.
var scriptGroup = map[string]string{
	"Hiragana": "Han",
	"Katakana": "Han",
	"Hangul":   "Han",
	"Bopomofo": "Han",
}

func scriptOf(r rune) string {
	for name, table := range unicode.Scripts {
		if unicode.Is(table, r) {
			if g, ok := scriptGroup[name]; ok {
				return g
			}
			if name == "Common" {
				return ""
			}
			return name
		}
	}
	return "Unknown"
}

// suggestName makes a valid name from an SSH user name: it keeps the runes
// that a name allows. It returns defaultName when nothing valid is left.
func suggestName(raw string) string {
	raw = norm.NFC.String(clean(raw, 64))
	var b strings.Builder
	n := 0
	first := true
	for _, r := range raw {
		if n == maxNameRunes {
			break
		}
		if ok, _ := nameRune(r, first); ok {
			b.WriteRune(r)
			n++
			first = false
		}
	}
	name, err := checkName(b.String())
	if err != nil {
		return defaultName
	}
	return name
}

// normLogin makes a PIN login: lower-cased, trimmed, 1..32 runes.
func normLogin(login string) (string, error) {
	login = strings.ToLower(clean(login, 4*maxLoginRunes))
	if n := utf8.RuneCountInString(login); n < 1 || n > maxLoginRunes {
		return "", ErrBadName
	}
	return login, nil
}

// pinFormatOK reports whether pin is exactly 4 ASCII digits.
func pinFormatOK(pin string) bool {
	if len(pin) != 4 {
		return false
	}
	for i := range len(pin) {
		if pin[i] < '0' || pin[i] > '9' {
			return false
		}
	}
	return true
}

// identity is a parsed Connect identity.
type identity struct {
	pin    bool   // a PIN login; else an SSH key
	value  string // the fingerprint or the login
	legacy bool   // a "user:<name>" identity from the old Connect
}

// parseIdentity reads "key:<fp>", "pin:<login>" or the old forms "user:<name>"
// and a bare fingerprint.
func parseIdentity(raw string) (identity, error) {
	switch {
	case strings.HasPrefix(raw, "pin:"):
		login, err := normLogin(strings.TrimPrefix(raw, "pin:"))
		return identity{pin: true, value: login}, err
	case strings.HasPrefix(raw, "user:"):
		login, err := normLogin(strings.TrimPrefix(raw, "user:"))
		return identity{pin: true, value: login, legacy: true}, err
	case strings.HasPrefix(raw, "key:"):
		fp := strings.TrimPrefix(raw, "key:")
		if fp == "" {
			return identity{}, ErrNoAccount
		}
		return identity{value: fp}, nil
	case raw == "":
		return identity{}, ErrNoAccount
	}
	return identity{value: raw}, nil
}

// lookAlike maps a Cyrillic or Greek letter (in its case-folded form) to
// the Latin letter that it looks like. Only letters that are near copies in
// common fonts are here. "Јое" (all Cyrillic) and "Joe" then have one
// skeleton, so nameTaken refuses the second.
var lookAlike = map[rune]rune{
	// Cyrillic.
	'а': 'a', 'в': 'b', 'ь': 'b', 'с': 'c', 'ԁ': 'd', 'е': 'e', 'һ': 'h',
	'н': 'h', 'і': 'i', 'ӏ': 'l', 'ј': 'j', 'к': 'k', 'м': 'm', 'п': 'n',
	'о': 'o', 'р': 'p', 'ԛ': 'q', 'ѕ': 's', 'т': 't', 'у': 'y', 'ү': 'y',
	'ԝ': 'w', 'х': 'x',
	// Greek.
	'α': 'a', 'β': 'b', 'ϲ': 'c', 'ε': 'e', 'η': 'h', 'ι': 'i', 'ϳ': 'j',
	'κ': 'k', 'μ': 'm', 'ν': 'v', 'ο': 'o', 'ρ': 'p', 'τ': 't', 'υ': 'u',
	'ω': 'w', 'χ': 'x', 'γ': 'y', 'ζ': 'z',
	// Latin letters outside ASCII that look like ASCII letters.
	'ı': 'i', 'ɑ': 'a', 'ɡ': 'g',
}

// skeleton is nameKey with each look-alike letter changed to its Latin
// letter (see lookAlike). It is a small part of the Unicode confusables
// skeleton (UTS #39): enough for the Cyrillic and Greek copies of Latin
// names.
func skeleton(name string) string {
	return strings.Map(func(r rune) rune {
		if l, ok := lookAlike[r]; ok {
			return l
		}
		return r
	}, nameKey(name))
}
