package search

import (
	"strings"
	"unicode"
)

// foldReplacer maps Danish and common accented letters to ASCII.
//
// This is a deliberate, tested deviation from "preserve æ/ø/å": folding
// makes equivalent keyboard input compare reliably ("snøfler" ≡
// "snoefler", "coca-cola" ≡ "COCA COLA") and matches the normalization
// the project's existing keyword matcher has used in production. Both
// query and offer text run through the same pipeline, so Danish letters
// always still match; they simply also match their ASCII foldings.
var foldReplacer = strings.NewReplacer(
	"æ", "ae", "ø", "oe", "å", "aa",
	"ä", "ae", "ö", "oe", "ü", "u",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"á", "a", "à", "a", "â", "a", "ã", "a",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u",
	"ý", "y", "ÿ", "y", "ç", "c", "ñ", "n", "ß", "ss",
)

// Normalize is the single text normalization pipeline used for queries,
// offers, aliases and concepts. It lowercases, folds diacritics (see
// foldReplacer) and reduces every run of non-letter/digit characters to a
// single space, then trims. Whitespace runs collapse, punctuation sits on
// boundaries ("Coca-Cola" -> "coca cola").
//
// Words are never merged: punctuation becomes a boundary, so unrelated
// words stay separate.
func Normalize(s string) string {
	s = foldReplacer.Replace(strings.ToLower(s))
	var builder strings.Builder
	builder.Grow(len(s))
	needSpace := false
	wrote := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if needSpace && wrote {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			needSpace, wrote = false, true
		} else {
			needSpace = wrote
		}
	}
	return builder.String()
}

// Tokens returns the normalized words of s in order.
func Tokens(s string) []string {
	norm := Normalize(s)
	if norm == "" {
		return nil
	}
	return strings.Split(norm, " ")
}

// compact removes all spaces from a normalized string so Danish
// compounds ("chokoladebar") can be compared against spaced forms
// ("chokolade bar").
func compact(normalized string) string {
	return strings.ReplaceAll(normalized, " ", "")
}
