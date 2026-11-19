package domain

import (
	"strings"
	"unicode"
)

// foldTable maps each ASCII letter to the Latin-1 and Latin Extended-A accented
// forms that fold to it (lowercase only; callers lowercase first).
var foldTable = map[rune]string{
	'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ďđ", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥħ",
	'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľŀł", 'n': "ñńņň", 'o': "òóôõöøōŏő",
	'r': "ŕŗř", 's': "śŝşš", 't': "ţťŧ", 'u': "ùúûüũūŭůűų", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
}

// foldRunes is foldTable inverted for per-rune lookup.
var foldRunes = func() map[rune]rune {
	m := make(map[rune]rune, 128)
	for to, froms := range foldTable {
		for _, r := range froms {
			m[r] = to
		}
	}
	return m
}()

// NormalizeName returns the search key of a name: lowercase, accents removed
// (Latin-1 and Latin Extended-A, plus combining marks) and whitespace collapsed.
func NormalizeName(s string) string {
	folded := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		if to, ok := foldRunes[r]; ok {
			return to
		}
		return r
	}, strings.ToLower(s))
	return strings.Join(strings.Fields(folded), " ")
}
