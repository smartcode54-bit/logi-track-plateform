// Package migratetest holds the migration checks that need a database (R31), shared by the
// integration tests here, the T04 baseline and the go-ci "migrate" job (T14).
package migratetest

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
)

// Runner returns a migrate.Runner for fsys on d, logged in as logitrack_migrator.
func Runner(tb testing.TB, d *pgtest.Database, fsys fs.FS) *migrate.Runner {
	tb.Helper()
	pool := d.Pool(tb, db.RoleMigrator)
	if err := migrate.RequireMigrator(context.Background(), pool); err != nil {
		tb.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	tb.Cleanup(func() { _ = sqlDB.Close() })
	r, err := migrate.New(sqlDB, fsys)
	if err != nil {
		tb.Fatal(err)
	}
	return r
}

// RoundTrip applies fsys to a fresh database and dumps the schema (A), rolls back to the highest
// "-- irreversible" version (or 0), applies again and dumps (B). It fails unless A equals B and
// nothing is pending, which catches a Down that forgets an object or drops one too many.
func RoundTrip(tb testing.TB, fsys fs.FS) {
	tb.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(tb)
	r := Runner(tb, d, fsys)
	files := r.Files()
	floor := migrate.RoundTripFloor(files)

	if _, err := r.Up(ctx); err != nil {
		tb.Fatalf("up: %v", err)
	}
	a, err := d.DumpSchema(ctx)
	if err != nil {
		tb.Fatalf("dump A: %v", err)
	}
	if _, err := r.DownTo(ctx, floor); err != nil {
		tb.Fatalf("down-to %d: %v", floor, err)
	}
	if v, err := r.Version(ctx); err != nil || v != floor {
		tb.Fatalf("after down-to %d the version is %d (%v)", floor, v, err)
	}
	if _, err := r.Up(ctx); err != nil {
		tb.Fatalf("up again: %v", err)
	}
	b, err := d.DumpSchema(ctx)
	if err != nil {
		tb.Fatalf("dump B: %v", err)
	}
	if a != b {
		tb.Fatalf("schema differs after up -> down-to %d -> up:\n%s", floor, firstDiff(a, b))
	}
	st, err := r.Status(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	for _, s := range st {
		if !s.Applied {
			tb.Fatalf("%s is still pending after the round trip", s.Name)
		}
	}
	if want := files[len(files)-1].Version; len(st) != len(files) || st[len(st)-1].Version != want {
		tb.Fatalf("status lists %d migrations, the chain has %d", len(st), len(files))
	}
}

// firstDiff shows the first differing line of two dumps with a little context.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	n := min(len(al), len(bl))
	i := 0
	for i < n && al[i] == bl[i] {
		i++
	}
	lo, hi := max(0, i-3), i+4
	var sb strings.Builder
	fmt.Fprintf(&sb, "first difference at line %d\n--- A (after up)\n", i+1)
	for j := lo; j < min(hi, len(al)); j++ {
		fmt.Fprintf(&sb, "  %s\n", al[j])
	}
	sb.WriteString("--- B (after the round trip)\n")
	for j := lo; j < min(hi, len(bl)); j++ {
		fmt.Fprintf(&sb, "  %s\n", bl[j])
	}
	return sb.String()
}
