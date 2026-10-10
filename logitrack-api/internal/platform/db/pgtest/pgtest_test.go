package pgtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pgRepos are the repository names of PostgreSQL server images: the official one and the
// common derivatives (bitnami/postgresql, postgis/postgis, pgvector/pgvector, timescale/...).
const pgRepos = `postgres(?:ql)?|postgis|pgvector|timescaledb(?:-ha)?`

var (
	// postgresRef is a PostgreSQL image named with a tag or digest, optionally behind a registry
	// path. A host:port inside a URL (`@postgres:5432`, `postgres://`) does not match.
	postgresRef = regexp.MustCompile(`(?:^|[^\w@/.-])((?:[\w.-]+/)*(?:` + pgRepos + `)(?::[\w.-]+|@sha256:[0-9a-f]+))`)
	// pgRepo matches the repository basename of a PostgreSQL image.
	pgRepo = regexp.MustCompile(`^(?:` + pgRepos + `)$`)
	// imageKey is the value of a compose or workflow `image:` key, tagged or not.
	imageKey     = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([^\s"'#]+)`)
	yamlComment  = regexp.MustCompile(`(?m)(^|\s)#.*$`)
	repoBasename = regexp.MustCompile(`^(?:.*/)?([^/:@]+)`)
)

// yamlFiles returns the .yml and .yaml files under dir, recursively when deep.
func yamlFiles(t *testing.T, dir string, deep bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != dir && !deep {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// compose, testcontainers and CI run the same PostgreSQL image (R34; go-ci, main spec §17.2):
// every PostgreSQL image (official or a derivative, see pgRepos) named in the compose files
// (deploy/*.yml, *.yaml), anywhere under .github (workflows and actions, .yml and .yaml) or in a
// Go string literal of this module is Image, never another tag. go-ci runs the Go jobs on every
// change under .github/workflows/ and .github/actions/ for this test.
func TestEveryPostgresImageIsTheSame(t *testing.T) {
	root, err := moduleFile()
	if err != nil {
		t.Fatal(err)
	}
	// found counts the image keys per directory and the Go literals per package.
	found := map[string]int{}
	check := func(where, ref string) {
		t.Helper()
		if ref != Image {
			t.Errorf("%s names %s, want %s", where, ref, Image)
		}
	}

	yamls := yamlFiles(t, filepath.Join(root, "deploy"), false)
	workflows := yamlFiles(t, filepath.Join(root, "..", ".github"), true)
	if len(yamls) == 0 || len(workflows) == 0 {
		t.Fatalf("found %d compose and %d workflow files", len(yamls), len(workflows))
	}
	for _, p := range append(yamls, workflows...) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := yamlComment.ReplaceAllString(string(b), "$1")
		for _, m := range imageKey.FindAllStringSubmatch(text, -1) {
			if pgRepo.MatchString(repoBasename.FindStringSubmatch(m[1])[1]) {
				check(p, m[1])
				found[filepath.Base(filepath.Dir(p))]++
			}
		}
		for _, m := range postgresRef.FindAllStringSubmatch(text, -1) {
			check(p, m[1]) // `docker run postgres:...` and the like
		}
	}

	fset := token.NewFileSet()
	self := filepath.Join(root, "internal", "platform", "db", "pgtest", "pgtest_test.go") // its literals are regexp fixtures
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || p == self {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					s = lit.Value
				}
				for _, m := range postgresRef.FindAllStringSubmatch(s, -1) {
					check(fset.Position(lit.Pos()).String(), m[1])
					found[filepath.Base(filepath.Dir(p))]++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found["deploy"] != 1 || found["pgtest"] != 1 {
		t.Fatalf("want the compose service and pgtest.Image, found %v", found)
	}
}

func TestPostgresRefMatchesImagesOnly(t *testing.T) {
	for s, want := range map[string]string{
		"image: postgres:18-alpine":                   "postgres:18-alpine",
		`docker run --rm postgres:16 psql`:            "postgres:16",
		"ghcr.io/x/postgres:18":                       "ghcr.io/x/postgres:18",
		"docker.io/library/postgres@sha256:00ff":      "docker.io/library/postgres@sha256:00ff",
		"postgres://logitrack_app:pw@postgres:5432/":  "",
		"--username=postgres":                         "",
		"./postgres-init:/docker-entrypoint-initdb.d": "",
		"image: postgis/postgis:16-3.4-alpine":        "postgis/postgis:16-3.4-alpine",
		"image: bitnami/postgresql:16":                "bitnami/postgresql:16",
		"image: pgvector/pgvector:pg16":               "pgvector/pgvector:pg16",
		"timescale/timescaledb-ha:pg16":               "timescale/timescaledb-ha:pg16",
		"postgresql://u@db:5432/x":                    "",
		"./postgresql-conf:/etc/postgresql":           "",
	} {
		got := ""
		if m := postgresRef.FindStringSubmatch(s); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", s, got, want)
		}
	}
}
