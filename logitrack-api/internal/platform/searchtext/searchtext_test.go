package searchtext_test

import (
	"slices"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/searchtext"
)

// The cases of logitrack-web/lib/searchText.test.ts, kept in lock step with it: a case added or changed there is
// added or changed here (and the other way round), so the server's search and the select's local filter agree.

func TestFoldLikeTheWeb(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		// "folds case and Latin accents"
		{"latin accents", "Café DÉPÔT", "cafe depot"},
		// "folds Thai tone marks but keeps vowel signs": thanthakhat (การันต์) and maitaikhu fold too
		{"thanthakhat", "จันทร์", "จันทร"},
		{"maitaikhu", "เป็น", "เปน"},
		// "reads Thai digits and full-width letters as ASCII"
		{"thai digits", "ทะเบียน ๗๐-๑๒๓๔", "ทะเบียน 70-1234"},
		{"full width", "ＳＰＫ", "spk"},
		// "collapses white space"
		{"white space", "  J&T   EXPRESS\tบางปู ", "j&t express บางปู"},
		// Go-only edges of the same rules: sara am decomposes to nikhahit (dropped) + sara aa, the JavaScript \s set.
		{"sara am", "ทำ", "ทา"},
		{"js spaces", "a" + string(rune(0x00a0)) + "b" + string(rune(0x2003)) + "c" + string(rune(0xfeff)) + "d" +
			string(rune(0x3000)) + "e", "a b c d e"},
		{"nel is not js space", "a\u0085b", "a\u0085b"},
		{"empty", "", ""},
	} {
		if got := searchtext.Fold(c.in); got != c.want {
			t.Errorf("%s: Fold(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// "folds Thai tone marks but keeps vowel signs"
	if searchtext.Fold("ห้วยขวาง") != searchtext.Fold("หวยขวาง") {
		t.Error("a tone mark changed the folded text")
	}
	if searchtext.Fold("ห้วยขวาง") == searchtext.Fold("หัวยขวาง") {
		t.Error("a vowel sign (mai han-akat) was folded away")
	}
}

func TestTerms(t *testing.T) {
	if got := searchtext.Terms("  "); got != nil {
		t.Errorf("Terms(blank) = %q, want none", got)
	}
	if got := searchtext.Terms("่"); got != nil { // a lone tone mark folds to nothing
		t.Errorf("Terms(tone mark) = %q, want none", got)
	}
	if got := searchtext.Terms(" บางปู  J&T "); !slices.Equal(got, []string{"บางปู", "j&t"}) {
		t.Errorf("Terms = %q", got)
	}
}

// filterBySearch's matching (without its ordering: the server keeps its keyset order).
func TestMatchesEveryWordInAnyOrder(t *testing.T) {
	hubs := []string{"SPK-GW J&T EXPRESS บางปู", "SOCE ศูนย์คัดแยกตะวันออก", "SPK890174 ห้วยขวาง10", "XSPK warehouse"}
	filter := func(q string) []string {
		terms := searchtext.Terms(q)
		var out []string
		for _, h := range hubs {
			if searchtext.Matches(searchtext.Fold(h), terms) {
				out = append(out, h)
			}
		}
		return out
	}
	for q, want := range map[string][]string{
		// "matches every word in any order, Thai and English"
		"บางปู j&t": {"SPK-GW J&T EXPRESS บางปู"},
		"หวยขวาง":   {"SPK890174 ห้วยขวาง10"},
		"spk":       {"SPK-GW J&T EXPRESS บางปู", "SPK890174 ห้วยขวาง10", "XSPK warehouse"},
		// "returns every item for a blank query"
		" ": hubs,
		// Thai digits in the query
		"ห้วยขวาง๑๐":  {"SPK890174 ห้วยขวาง10"},
		"spk nowhere": nil,
	} {
		if got := filter(q); !slices.Equal(got, want) {
			t.Errorf("filter(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestLikePatternsEscapeTheWildcards(t *testing.T) {
	if got := searchtext.LikePatterns(" "); got != nil {
		t.Errorf("LikePatterns(blank) = %q, want nil (no search)", got)
	}
	if got := searchtext.LikePatterns(`50% a_b c\d Café`); !slices.Equal(got, []string{`%50\%%`, `%a\_b%`, `%c\\d%`, `%cafe%`}) {
		t.Errorf("LikePatterns = %q", got)
	}
}
