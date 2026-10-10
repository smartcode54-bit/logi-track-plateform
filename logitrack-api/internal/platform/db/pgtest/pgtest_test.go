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

var (
	// postgresRef is a postgres image named with a tag or digest, optionally behind a registry
	// path. A host:port inside a URL (`@postgres:5432`, `postgres://`) does not match.
	postgresRef = regexp.MustCompile(`(?:^|[^\w@/.-])((?:[\w.-]+/)*postgres(?::[\w.-]+|@sha256:[0-9a-f]+))`)
	// imageKey is the value of a compose or workflow `image:` key, tagged or not.
	imageKey     = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([^\s"'#]+)`)
	yamlComment  = regexp.MustCompile(`(?m)(^|\s)#.*$`)
	repoBasename = regexp.MustCompile(`^(?:.*/)?([^/:@]+)`)
)

// compose, testcontainers and CI run the same PostgreSQL image (R34; go-ci, main spec §17.2):
// every postgres image named in the compose files, the GitHub workflows or a Go string literal
// of this module is Image, never another tag.
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

	yamls, _ := filepath.Glob(filepath.Join(root, "deploy", "*.yml"))
	workflows, _ := filepath.Glob(filepath.Join(root, "..", ".github", "workflows", "*.yml"))
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
			if repoBasename.FindStringSubmatch(m[1])[1] == "postgres" {
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
