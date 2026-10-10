// Command withsystem fails the build when db.WithSystem is used outside the packages allowed to run
// without a request principal (Appendix C §C.3.2, R12). WithSystem sets app.bypass_tenant=on, so every
// other package must open its transactions with db.WithPrincipal and let RLS decide.
//
//	go run ./tools/analyzers/withsystem [-root DIR]
//
// It parses every non-test Go file of the module (standard library only, no type checking) and
// reports each reference to WithSystem of internal/platform/db (through the import's name, a renamed
// import or a dot import) in a package outside the allow-list. Test files are exempt: fixtures play
// the system context the way cmd/seed does. make lint runs it.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// DBImport is the package that defines WithSystem.
const DBImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"

// Allowed are the module-relative package directories (and everything below them) that may call
// WithSystem: identity and security services, storage key lookups after entity authorization, the
// anonymous public forms, the outbox relay and inbox, tenancy moves, and the processes without a
// request principal (worker consumers, scheduler jobs, ETL, seed). Appendix C §C.3.2 lists the same.
var Allowed = []string{
	"internal/platform/db", // the definition
	"internal/auth",
	"internal/iam",
	"internal/security",
	"internal/storage",
	"internal/public",
	"internal/platform/outbox",
	"internal/platform/inbox",
	"internal/platform/tenancy",
	"internal/jobs",      // job rows and DLQ replay (T10)
	"internal/notify",    // notify.* consumers (worker, T10)
	"internal/scheduler", // scheduler jobs (T10)
	"cmd/worker",
	"cmd/scheduler",
	"cmd/etl",
	"cmd/seed",
}

// Finding is one forbidden reference.
type Finding struct {
	Pos token.Position
	Pkg string // module-relative package directory
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: db.WithSystem in %s, which is not allowed to bypass RLS (use db.WithPrincipal; Appendix C §C.3.2)", f.Pos, f.Pkg)
}

// Check scans the module rooted at root.
func Check(root string, allowed []string) ([]Finding, error) {
	var out []Finding
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		if isAllowed(pkg, allowed) {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, pos := range references(f) {
			out = append(out, Finding{Pos: fset.Position(pos), Pkg: pkg})
		}
		return nil
	})
	return out, err
}

func isAllowed(pkg string, allowed []string) bool {
	return slices.ContainsFunc(allowed, func(a string) bool { return pkg == a || strings.HasPrefix(pkg, a+"/") })
}

// references are the positions where f uses db.WithSystem.
func references(f *ast.File) []token.Pos {
	names := map[string]bool{}
	dot := false
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != DBImport {
			continue
		}
		switch {
		case imp.Name == nil:
			names["db"] = true
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name != "_":
			names[imp.Name.Name] = true
		}
	}
	if len(names) == 0 && !dot {
		return nil
	}
	var out []token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			x, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			if names[x.Name] && n.Sel.Name == "WithSystem" {
				out = append(out, n.Pos())
			}
			return false // pkg.Name or value.Field: Sel is never the dot-imported function
		case *ast.Ident:
			if dot && n.Name == "WithSystem" {
				out = append(out, n.Pos())
			}
		}
		return true
	})
	return out
}

func main() {
	root := flag.String("root", ".", "module root (the directory holding go.mod)")
	flag.Parse()
	if _, err := os.Stat(filepath.Join(*root, "go.mod")); err != nil {
		fmt.Fprintln(os.Stderr, "withsystem: -root must be the module root:", err)
		os.Exit(2)
	}
	findings, err := Check(*root, Allowed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "withsystem:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f)
	}
	if len(findings) > 0 {
		fmt.Fprintln(os.Stderr, "withsystem: the allow-list is Allowed in tools/analyzers/withsystem/main.go (Appendix C §C.3.2)")
		os.Exit(1)
	}
}
