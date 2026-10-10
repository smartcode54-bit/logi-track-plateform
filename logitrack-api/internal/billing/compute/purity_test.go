package compute_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api"

// moneyPackages are the packages that compute money (main spec §6.2): they
// may not call math.Round or math.FMA, and every product must be wrapped in
// an explicit float64(...) conversion so the compiler cannot fuse it into a
// multiply-add (arm64 does; see TestNoFusedMultiplyAdd). The value lists the
// non-standard-library imports each may use.
var moneyPackages = map[string][]string{
	"internal/billing/compute":   {module + "/internal/platform/clock", module + "/internal/platform/jsmath"},
	"internal/billing/documents": {module + "/internal/platform/jsmath"},
	"internal/platform/jsmath":   nil,
}

// purePackages are the packages that must stay pure (§2.3, §6.1): the money
// packages plus platform/clock, the Bangkok calendar compute imports. clock
// does no money arithmetic, so TestNoFusedMultiplyAdd skips it.
func purePackages() map[string][]string {
	pkgs := map[string][]string{"internal/platform/clock": nil}
	for pkg, allowed := range moneyPackages {
		pkgs[pkg] = allowed
	}
	return pkgs
}

// pureStd is the standard library a pure package may import: computation
// only. Everything else (os, io, net, syscall, os/exec, database/sql,
// math/rand, sync, unsafe, log, ...) fails TestImportsArePure.
var pureStd = map[string]bool{
	"errors": true, "fmt": true, "math": true, "math/big": true, "regexp": true, "slices": true, "sort": true,
	"strconv": true, "strings": true, "time": true, "unicode": true, "unicode/utf8": true,
}

// impureCalls are the package-level functions and variables of allowed
// standard-library packages that still reach outside the process: the wall
// clock and timers (R19: the engine takes every instant as input), the host
// zone database (§6.3: Bangkok is a fixed offset), and fmt's console and
// io.Writer printing.
var impureCalls = map[string]map[string]string{
	"time": {
		"Now": "reads the wall clock", "Since": "reads the wall clock", "Until": "reads the wall clock",
		"Sleep": "waits on the wall clock", "After": "starts a timer", "AfterFunc": "starts a timer",
		"Tick": "starts a timer", "NewTimer": "starts a timer", "NewTicker": "starts a timer",
		"LoadLocation": "reads the host zone database", "Local": "uses the host zone",
	},
	"fmt": {
		"Print": "writes to stdout", "Printf": "writes to stdout", "Println": "writes to stdout",
		"Fprint": "writes to an io.Writer", "Fprintf": "writes to an io.Writer", "Fprintln": "writes to an io.Writer",
		"Scan": "reads stdin", "Scanf": "reads stdin", "Scanln": "reads stdin",
		"Fscan": "reads an io.Reader", "Fscanf": "reads an io.Reader", "Fscanln": "reads an io.Reader",
	},
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod")
		}
		dir = parent
	}
}

// parseDir parses the non-test Go files of one package directory.
func parseDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	return fset, files
}

// TestImportsArePure: the engine is pure (§2.3, §6.1): no I/O and no clock.
// A pure package imports only the pureStd allow-list and its listed module
// leaves, never dot-imports, and never calls impureCalls (time.Now, timers,
// time.Local, fmt printing).
func TestImportsArePure(t *testing.T) {
	root := moduleRoot(t)
	for pkg, allowed := range purePackages() {
		fset, files := parseDir(t, filepath.Join(root, pkg))
		for _, f := range files {
			// local package name -> import path, for the call check below
			names := map[string]string{}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				name := path[strings.LastIndex(path, "/")+1:]
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "." {
					t.Errorf("%s dot-imports %s: name the package so its calls can be checked", pkg, path)
				}
				names[name] = path
				first, _, _ := strings.Cut(path, "/")
				if !strings.Contains(first, ".") {
					if !pureStd[path] {
						t.Errorf("%s imports %s: the engine does no I/O; allowed standard library: %v", pkg, path, keys(pureStd))
					}
					continue
				}
				if !slices.Contains(allowed, path) {
					t.Errorf("%s imports %s; allowed outside the standard library: %v", pkg, path, allowed)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if why, bad := impureCalls[names[id.Name]][sel.Sel.Name]; bad {
					t.Errorf("%s: %s.%s %s; a pure package takes every instant and table as input", fset.Position(sel.Pos()), id.Name, sel.Sel.Name, why)
				}
				return true
			})
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestNoFusedMultiplyAdd enforces the §6.2 CI rule: no math.Round (ties away
// from zero, not JavaScript's toward +Inf), no math.FMA, and every product a
// float64(...) conversion's operand, which forces the rounding V8 performs
// between the multiply and a following add.
func TestNoFusedMultiplyAdd(t *testing.T) {
	root := moduleRoot(t)
	for pkg := range moneyPackages {
		fset, files := parseDir(t, filepath.Join(root, pkg))
		for _, f := range files {
			wrapped := map[ast.Expr]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "float64" && len(call.Args) == 1 {
						wrapped[ast.Unparen(call.Args[0])] = true
					}
				}
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "math" && (x.Sel.Name == "Round" || x.Sel.Name == "FMA") {
						t.Errorf("%s: math.%s in a money package; use jsmath", fset.Position(x.Pos()), x.Sel.Name)
					}
				case *ast.BinaryExpr:
					if x.Op != token.MUL || wrapped[x] {
						return true
					}
					if isLiteral(x.X) && isLiteral(x.Y) {
						return true // constant folding, no float multiply at run time
					}
					t.Errorf("%s: product not wrapped in float64(...)", fset.Position(x.Pos()))
				case *ast.AssignStmt:
					if x.Tok == token.MUL_ASSIGN {
						t.Errorf("%s: *= in a money package; write x = float64(x * y)", fset.Position(x.Pos()))
					}
				}
				return true
			})
		}
	}
}

func isLiteral(e ast.Expr) bool {
	_, ok := ast.Unparen(e).(*ast.BasicLit)
	return ok
}

// TestFrozenRuleHasOneDefinition: the acceptance criterion "is frozen has
// exactly one definition" — one IsFrozen and one CarriesFuel in the whole
// module, both here.
func TestFrozenRuleHasOneDefinition(t *testing.T) {
	root := moduleRoot(t)
	pattern := regexp.MustCompile(`(?i)frozen|carriesfuel`)
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && pattern.MatchString(fn.Name.Name) {
				rel, _ := filepath.Rel(root, path)
				found[fn.Name.Name] = append(found[fn.Name.Name], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"IsFrozen": "internal/billing/compute/frozen.go", "CarriesFuel": "internal/billing/compute/frozen.go"}
	for name, where := range found {
		if want[name] == "" {
			t.Errorf("unexpected frozen-rule definition %s in %v: use compute.IsFrozen / compute.CarriesFuel", name, where)
		}
	}
	for name, path := range want {
		if got := found[name]; len(got) != 1 || filepath.ToSlash(got[0]) != path {
			t.Errorf("%s defined at %v, want exactly once in %s", name, got, path)
		}
	}
}
