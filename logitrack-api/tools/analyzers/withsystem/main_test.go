package main

import (
	"maps"
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
	want := []string{"internal/authz/x.go", "internal/fleet/dot.go", "internal/tasks/repo.go", "internal/tasks/sub/renamed.go"}
	if got := files(root, findings); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("findings %v, want %v", got, want)
	}
	if !strings.Contains(findings[0].String(), "db.WithPrincipal") {
		t.Errorf("message %q does not point at WithPrincipal", findings[0])
	}
}

// files lists the module-relative files of findings, one entry per finding.
func files(root string, findings []Finding) []string {
	var got []string
	for _, f := range findings {
		got = append(got, filepath.ToSlash(strings.TrimPrefix(f.Pos.Filename, root+string(filepath.Separator))))
	}
	return got
}

const dbPkg = `"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"`

const authzPkg = `"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"`

// TestForgedContexts: the ways around WithPrincipal's trust in its principal are reported outside
// their allow-lists: a hand-made db.RLS (literal, declaration or an RLS() method that returns one),
// an authz.Principal literal, a write of ActOnAll / ActOnTenant, and raw GUC or set_config SQL.
func TestForgedContexts(t *testing.T) {
	forge := "package zz\n\nimport " + dbPkg + "\n\ntype bypass struct{}\n\n" +
		"func (bypass) RLS() db.RLS { return db.RLS{Bypass: true, ReadOnly: true} }\n" // result type + literal: 2
	declared := "package zz\n\nimport x " + dbPkg + "\n\nfunc f() { var r x.RLS; _ = r }\n"
	dotted := "package zz\n\nimport . " + dbPkg + "\n\ntype t struct{}\n\nfunc (t) RLS() RLS { return RLS{} }\n"                      // 2, not the method name
	principal := "package zz\n\nimport " + authzPkg + "\n\nfunc f() *authz.Principal { return &authz.Principal{ActOnAll: true} }\n"   // literal + key: 2
	actOn := "package zz\n\nimport " + authzPkg + "\n\nfunc f(p *authz.Principal) { p.ActOnTenant = nil; _ = p.ActOnAll }\n"          // the write only: 1
	raw := "package zz\n\nconst q = `SELECT set_config('app.bypass_tenant', 'on', true)`\n\nvar s = \"SET LOCAL APP.STEWARD = on\"\n" // 2 + 1
	moves := "package tenancy\n\nconst q = `SELECT set_config('app.tenant_move', 'on', true)`\n\nconst e = \"app.etl_load\"\n"        // only the ETL GUC
	reads := "package zz\n\nimport " + dbPkg + "\n\nfunc f(p db.Principal) bool { return p.RLS().Bypass }\n"                          // reading through the interface: clean
	root := write(t, map[string]string{
		"go.mod":                             "module example\n",
		"internal/zz/forge.go":               forge,
		"internal/zz/declared.go":            declared,
		"internal/zz/dotted.go":              dotted,
		"internal/zz/principal.go":           principal,
		"internal/zz/acton.go":               actOn,
		"internal/zz/raw.go":                 raw,
		"internal/zz/reads.go":               reads,
		"internal/zz/forge_test.go":          forge, // test files are exempt
		"internal/platform/tenancy/moves.go": moves,
		// allowed where they belong
		"internal/authz/rls.go":            forge,
		"internal/platform/db/system.go":   raw,
		"internal/auth/middleware.go":      "package auth\n\nimport " + authzPkg + "\n\nvar p = authz.Principal{}\n",
		"internal/iam/authorize.go":        actOn,
		"cmd/etl/load.go":                  "package main\n\nconst q = `SELECT set_config('app.etl_load', 'on', true)`\n",
		"internal/platform/cache/hints.go": "package cache\n\n// GUC app.subtenant_ids in a comment is not SQL.\nvar s = \"application.role\"\n",
	})
	findings, err := Check(root, Allowed)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, f := range files(root, findings) {
		count[f]++
	}
	want := map[string]int{
		"internal/zz/forge.go": 2, "internal/zz/declared.go": 1, "internal/zz/dotted.go": 2,
		"internal/zz/principal.go": 2, "internal/zz/acton.go": 1, "internal/zz/raw.go": 3,
		"internal/platform/tenancy/moves.go": 1,
	}
	if !maps.Equal(count, want) {
		for _, f := range findings {
			t.Log(f)
		}
		t.Fatalf("findings per file %v, want %v", count, want)
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
