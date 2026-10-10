package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates files (path -> content) below a temporary module root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const use = `package x

import %s"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"

func f() { _ = %s }
`

func src(name, expr string) string {
	if name != "" {
		name += " "
	}
	return strings.Replace(strings.Replace(use, "%s", name, 1), "%s", expr, 1)
}

func TestCheck(t *testing.T) {
	root := write(t, map[string]string{
		"go.mod":                             "module example\n",
		"internal/tasks/repo.go":             src("", "db.WithSystem"),
		"internal/tasks/sub/renamed.go":      src("pg", "pg.WithSystem"),
		"internal/fleet/dot.go":              src(".", "WithSystem"),
		"internal/fleet/principal.go":        src("", "db.WithPrincipal"),
		"internal/fleet/unrelated.go":        "package fleet\n\ntype s struct{ WithSystem int }\n\nfunc g(v s) int { return v.WithSystem }\n",
		"internal/tasks/repo_test.go":        src("", "db.WithSystem"),
		"internal/auth/login.go":             src("", "db.WithSystem"),
		"internal/authz/x.go":                src("", "db.WithSystem"), // internal/auth is a sibling, not a prefix
		"cmd/worker/consumer/run.go":         src("", "db.WithSystem"),
		"internal/tasks/testdata/fixture.go": src("", "db.WithSystem"),
	})
	findings, err := Check(root, Allowed)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, filepath.ToSlash(strings.TrimPrefix(f.Pos.Filename, root+string(filepath.Separator))))
	}
	want := []string{"internal/authz/x.go", "internal/fleet/dot.go", "internal/tasks/repo.go", "internal/tasks/sub/renamed.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("findings %v, want %v", got, want)
	}
	if !strings.Contains(findings[0].String(), "db.WithPrincipal") {
		t.Errorf("message %q does not point at WithPrincipal", findings[0])
	}
}

// TestModuleIsClean runs the analyzer on this module, so go test ./... guards it too.
func TestModuleIsClean(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	findings, err := Check(root, Allowed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Error(f)
	}
}
