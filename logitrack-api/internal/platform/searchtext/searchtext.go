// Package searchtext is the text matching of the server-side searches (UI2B, issue #142): `q` of GET /v1/hubs (in
// memory) and of the SQL lists of drivers, trucks, customers and users. It is the Go twin of the web's
// logitrack-web/lib/searchText.ts (the searchable select's local matching), so a picker finds the same rows whether it
// filters a loaded list or asks the server. Keep the two in lock step: searchtext_test.go runs the cases of
// lib/searchText.test.ts, and a change on one side is made on the other. In SQL the same fold is search_fold(text)
// (migration 0026); searchtext_integration_test.go checks it against Fold.
//
//   - Compatibility forms fold (NFKD: full-width "ＡＢ" = "AB", sara am "ำ" = nikhahit + sara aa), and Latin accents
//     fold away (the combining marks U+0300-U+036F are dropped): "Café" = "cafe".
//   - Thai tone marks and the other marks above a consonant that change neither the letters nor the order fold away
//     (U+0E47 maitaikhu, U+0E48-U+0E4B tone marks, U+0E4C thanthakhat, U+0E4D nikhahit, U+0E4E yamakkan): "ห้วยขวาง"
//     matches "หวยขวาง". Vowel signs stay: they change the word.
//   - Thai digits read as Arabic digits: "๑๒" = "12".
//   - Case folds (strings.ToLower: toLocaleLowerCase outside the Turkic and Lithuanian locales, except that a Greek
//     final sigma stays σ, as in PostgreSQL's lower()), and runs of white space (JavaScript's \s) collapse to one space.
//
// A query matches when every one of its space-separated words is somewhere in the text (any order), so "บางปู j&t"
// finds "J&T EXPRESS บางปู".
package searchtext

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// MaxQueryLen is the longest q (in characters) a list endpoint accepts; longer is 422 too_long.
const MaxQueryLen = 100

// Fold is the folded form of s that searches compare (foldSearchText of lib/searchText.ts; see the package comment).
func Fold(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		switch {
		case r >= 0x0300 && r <= 0x036f, r >= 0x0e47 && r <= 0x0e4e: // Latin combining marks, Thai marks
		case r >= 0x0e50 && r <= 0x0e59: // Thai digits
			b.WriteRune('0' + r - 0x0e50)
		default:
			b.WriteRune(r)
		}
	}
	return collapseSpaces(strings.ToLower(norm.NFC.String(b.String())))
}

// collapseSpaces replaces each run of white space with one space and trims both ends (JavaScript's
// .replace(/\s+/g, " ").trim()).
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if isJSSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isJSSpace is JavaScript's \s: the ECMAScript WhiteSpace and LineTerminator code points (unicode.IsSpace differs:
// it holds U+0085 and not U+FEFF).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// Terms are the folded words of a query (searchTerms); none for a blank one, or one that folds to nothing.
func Terms(q string) []string {
	folded := Fold(q)
	if folded == "" {
		return nil
	}
	return strings.Split(folded, " ")
}

// Matches says whether every term is in foldedText, an already folded haystack (matchesTerms).
func Matches(foldedText string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(foldedText, t) {
			return false
		}
	}
	return true
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikePatterns are the terms of q as LIKE patterns ("%term%", the wildcards and the escape character of a term
// escaped) for the SQL lists' `search_fold(<searchable columns>) LIKE ALL (patterns)`; nil for no term, which the
// queries read as no search (sqlc.narg(q_words) IS NULL).
func LikePatterns(q string) []string {
	terms := Terms(q)
	if len(terms) == 0 {
		return nil
	}
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = "%" + likeEscaper.Replace(t) + "%"
	}
	return out
}
