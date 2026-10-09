package migrate_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const okFile = "-- +goose Up\nCREATE TABLE t (id uuid PRIMARY KEY DEFAULT uuidv7());\n\n-- +goose Down\nDROP TABLE t;\n"

func chain(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestEmbeddedChainPassesCheck(t *testing.T) {
	files, err := migrate.Check(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Name != "0001_preamble.sql" || files[0].NoTransaction || files[0].Irreversible {
		t.Fatalf("first file = %+v", files[0])
	}
}

func TestFixtureChainsPassCheck(t *testing.T) {
	shape, err := migrate.Check(os.DirFS("testdata/baseline-shape"))
	if err != nil {
		t.Fatal(err)
	}
	if len(shape) != 10 || shape[8].Name != "0009_infra.sql" || shape[9].Name != "0010_d5_unique_constraints.sql" || !shape[9].NoTransaction {
		t.Fatalf("baseline-shape = %+v", shape)
	}
	if migrate.RoundTripFloor(shape) != 0 {
		t.Fatal("baseline-shape has no irreversible file")
	}
	irr, err := migrate.Check(os.DirFS("testdata/irreversible"))
	if err != nil {
		t.Fatal(err)
	}
	if got := migrate.RoundTripFloor(irr); got != 2 {
		t.Fatalf("round-trip floor = %d, want 2", got)
	}
}

func TestCheckRejects(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no Down section", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE TABLE t (id int);\n"}, "has no '-- +goose Down' section"},
		{"empty Down", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE TABLE t (id int);\n-- +goose Down\n-- nothing\n"}, "the Down section is empty"},
		{"no Up section", map[string]string{"0001_a.sql": "CREATE TABLE t (id int);\n"}, "SQL before '-- +goose Up'"},
		{"empty Up", map[string]string{"0001_a.sql": "-- +goose Up\n-- +goose Down\nSELECT 1;\n"}, "the Up section is empty"},
		{"Down before Up", map[string]string{"0001_a.sql": "-- +goose Down\nSELECT 1;\n-- +goose Up\nSELECT 1;\n"}, "must appear once, after the Up section"},
		{"gap", map[string]string{"0001_a.sql": okFile, "0003_c.sql": okFile}, "expected version 0002 here"},
		{"duplicate version", map[string]string{"0001_a.sql": okFile, "0001_b.sql": okFile}, "version 1 is also used by"},
		{"bad name", map[string]string{"1_a.sql": okFile}, "file names are NNNN_name.sql"},
		{"upper-case name", map[string]string{"0001_Add.sql": okFile}, "file names are NNNN_name.sql"},
		{"stray file", map[string]string{"0001_a.sql": okFile, "notes.txt": "x"}, "notes.txt"},
		{"gen_random_uuid", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE TABLE t (id uuid DEFAULT gen_random_uuid());\n-- +goose Down\nDROP TABLE t;\n"}, "not gen_random_uuid()"},
		{"NO TRANSACTION without CONCURRENTLY", map[string]string{"0001_a.sql": "-- +goose NO TRANSACTION\n-- +goose Up\nCREATE TABLE t (id int);\n-- +goose Down\nDROP TABLE t;\n"}, "reserved for CREATE/DROP INDEX CONCURRENTLY"},
		{"CONCURRENTLY inside a transaction", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE INDEX CONCURRENTLY i ON t (id);\n-- +goose Down\nDROP INDEX CONCURRENTLY i;\n"}, "add '-- +goose NO TRANSACTION'"},
		{"unclosed StatementBegin", map[string]string{"0001_a.sql": "-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN END $$;\n-- +goose Down\nSELECT 1;\n"}, "is not closed before Down"},
		{"StatementEnd without Begin", map[string]string{"0001_a.sql": "-- +goose Up\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 1;\n"}, "StatementEnd' without StatementBegin"},
		{"indented annotation", map[string]string{"0001_a.sql": "  -- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"}, "must start at column 1"},
		{"ENVSUB", map[string]string{"0001_a.sql": "-- +goose ENVSUB ON\n-- +goose Up\nSELECT '${X}';\n-- +goose Down\nSELECT 1;\n"}, "ENVSUB is not used"},
		{"unknown annotation", map[string]string{"0001_a.sql": "-- +goose Up\nSELECT 1;\n-- +goose Sideways\n-- +goose Down\nSELECT 1;\n"}, "unknown goose annotation"},
		{"empty chain", map[string]string{}, "no migration files"},
		{"CONCURRENTLY only in a transactional Down", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE INDEX i ON t (id);\n-- +goose Down\nDROP INDEX CONCURRENTLY i;\n"}, "add '-- +goose NO TRANSACTION'"},
		{"empty StatementBegin block", map[string]string{"0001_a.sql": "-- +goose Up\nCREATE TABLE t (id int);\n-- +goose StatementBegin\n-- body\n-- +goose StatementEnd\nALTER TABLE t ENABLE ROW LEVEL SECURITY;\n-- +goose Down\nDROP TABLE t;\n"}, "empty '-- +goose StatementBegin' block"},
		{"irreversible marker outside the header", map[string]string{"0001_a.sql": "-- +goose Up\nUPDATE t SET id = id;\n-- irreversible\n-- +goose Down\n"}, "belongs in the file header"},
		{"gen_random_uuid in a function body", map[string]string{"0001_a.sql": "-- +goose Up\n-- +goose StatementBegin\nCREATE FUNCTION f() RETURNS uuid LANGUAGE sql AS $$ SELECT gen_random_uuid() $$;\n-- +goose StatementEnd\n-- +goose Down\nDROP FUNCTION f();\n"}, "not gen_random_uuid()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := migrate.Check(chain(tc.files))
			if _, ok := errors.AsType[*migrate.CheckError](err); !ok {
				t.Fatalf("err = %v, want a CheckError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant it to mention %q", err, tc.want)
			}
		})
	}
}

func TestCheckAccepts(t *testing.T) {
	files, err := migrate.Check(chain(map[string]string{
		"0001_a.sql": okFile,
		// Annotations are case-insensitive in goose; a "--" inside a string is not a comment.
		"0002_b.sql": "-- +goose up\nINSERT INTO t VALUES ('--x');\n-- +goose down\nDELETE FROM t;\n",
		"0003_c.sql": "-- irreversible: data fix\n-- +goose Up\nUPDATE t SET id = id;\n\n-- +goose Down\n",
		"0004_d.sql": "-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (id);\n-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS i;\n",
		// CONCURRENTLY only in the Down still needs NO TRANSACTION (goose uses one mode per file).
		"0005_e.sql": "-- +goose NO TRANSACTION\n-- +goose Up\nDROP INDEX IF EXISTS i;\n-- +goose Down\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (id);\n",
		// The words in strings, identifiers and comments are not code.
		"0006_f.sql": "-- +goose Up\nCOMMENT ON TABLE t IS 'built concurrently, never gen_random_uuid()'; -- concurrently\nALTER TABLE t ADD COLUMN \"concurrently\" int; /* gen_random_uuid() */\n-- +goose Down\nALTER TABLE t DROP COLUMN \"concurrently\";\n",
		"embed.go":   "package x\n",
		".DS_Store":  "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 || !files[2].Irreversible || !files[3].NoTransaction || files[0].Irreversible || !files[4].NoTransaction || files[5].NoTransaction {
		t.Fatalf("files = %+v", files)
	}
	if got := migrate.RoundTripFloor(files); got != 3 {
		t.Fatalf("round-trip floor = %d, want 3", got)
	}
}

// 0001 asserts the login and the roles before anything else, so that refusal is the first error
// a wrong URL or a missing role produces (the whole file still runs in one transaction).
func TestPreambleStartsWithTheRoleAssertion(t *testing.T) {
	src, err := fs.ReadFile(migrations.FS, "0001_preamble.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "-- +goose Up":
			up = true
		case !up, trimmed == "", strings.HasPrefix(trimmed, "--"):
		default:
			if trimmed != "DO $$" {
				t.Fatalf("first Up statement of 0001 is %q, want the DO $$ role assertion", trimmed)
			}
			return
		}
	}
	t.Fatal("0001 has no Up statement")
}

func TestCreateNumbersSequentiallyAndFailsCheckUntilWritten(t *testing.T) {
	dir := t.TempDir()
	p1, err := migrate.Create(dir, "add_trips")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "0001_add_trips.sql" {
		t.Fatalf("first file = %s", p1)
	}
	if _, err := migrate.Check(os.DirFS(dir)); err == nil || !strings.Contains(err.Error(), "the Up section is empty") {
		t.Fatalf("an unwritten migration must fail the check, got %v", err)
	}
	if err := os.WriteFile(p1, []byte(okFile), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := migrate.Create(dir, "add_notes")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p2) != "0002_add_notes.sql" {
		t.Fatalf("second file = %s", p2)
	}
	for _, bad := range []string{"", "Add", "add-notes", "add notes", "../x", "_x", "x_"} {
		if _, err := migrate.Create(dir, bad); err == nil {
			t.Errorf("Create(%q) succeeded", bad)
		}
	}
}
