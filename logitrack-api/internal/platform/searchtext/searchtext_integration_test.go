//go:build integration

package searchtext

import (
	"context"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// The SQL twin: search_fold(text) of migration 0026 folds exactly as Fold, except that it leaves white space as it is
// (the words of a query hold none, so a run of spaces changes no LIKE match); collapseSpaces closes that gap here. The
// cases are those of the unit test (lib/searchText.test.ts) plus every mark, digit and compatibility form the fold
// names, and random strings over those blocks.
func TestSQLFoldMatchesGo(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)

	cases := []string{
		"Café DÉPÔT", "ห้วยขวาง", "หวยขวาง", "หัวยขวาง", "จันทร์", "เป็น", "ทะเบียน ๗๐-๑๒๓๔", "ＳＰＫ",
		"  J&T   EXPRESS\tบางปู ", "ทำ", "SOCE (บัวโรย)", "SPK890103-ลาดกระบัง26", "ÀÉÎÕÜ Ñ ç Å", "ﬁ ² Ⅻ ①", "한국어",
		`50% a_b c\d`, "",
	}
	// Every code point of the folded blocks on its own and after a base letter.
	for r := rune(0x0300); r <= 0x036f; r++ {
		cases = append(cases, "e"+string(r))
	}
	for r := rune(0x0e01); r <= 0x0e5b; r++ {
		cases = append(cases, string(r), "ก"+string(r))
	}
	alphabet := []rune("aAzZ09 -_%éÉñÑüÜçÇøØ")
	for r := rune(0x0e01); r <= 0x0e5b; r++ {
		alphabet = append(alphabet, r)
	}
	for r := rune(0x0300); r <= 0x036f; r += 7 {
		alphabet = append(alphabet, r)
	}
	alphabet = append(alphabet, 0xff21, 0xff41, 0xff10, 0x00a0, 0x3000, '\t')
	rng := rand.New(rand.NewPCG(142, 2))
	for range 300 {
		var b strings.Builder
		for range 1 + rng.IntN(12) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		cases = append(cases, b.String())
	}

	rows, err := pool.Query(ctx, `SELECT search_fold(x) FROM unnest($1::text[]) WITH ORDINALITY AS t(x, n) ORDER BY n`, cases)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := Fold(cases[i]); collapseSpaces(got) != want {
			t.Errorf("search_fold(%q) = %q, Fold = %q", cases[i], got, want)
		}
		i++
	}
	if err := rows.Err(); err != nil || i != len(cases) {
		t.Fatalf("%d of %d rows (%v)", i, len(cases), err)
	}
	var null *string
	if err := pool.QueryRow(ctx, `SELECT search_fold(NULL)`).Scan(&null); err != nil || null != nil {
		t.Fatalf("search_fold(NULL) = %v (%v), want NULL", null, err)
	}
}
