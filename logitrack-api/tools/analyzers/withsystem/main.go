// Command withsystem fails the build when a package outside its allow-list can widen the RLS context of
// a transaction (Appendix C §C.3.2, R12). make lint runs it:
//
//	go run ./tools/analyzers/withsystem [-root DIR]
//
// It reports, in non-test Go files:
//   - a reference to db.WithSystem (app.bypass_tenant=on) outside Allowed: every other package opens
//     its transactions with db.WithPrincipal and lets RLS decide;
//   - the two helpers that hand a WithSystem transaction to their caller, outside the same Allowed: a
//     reference to inbox.Run (a consumer's side-effect transaction with the event's tenant; a domain
//     consumer package joins Allowed in its own task), and an InTx hook of jobs.Service.Submit, that is
//     a key InTx in a jobs.SubmitInput literal (or one whose type is elided) or a write of .InTx, in a
//     file that imports internal/jobs (a request reads tenant rows under db.WithPrincipal before it
//     calls Submit; go vet's composites check refuses an unkeyed jobs.SubmitInput outside its package);
//   - a reference to db.RLS outside RLSAllowed: db.WithPrincipal trusts the RLS() of the principal it
//     is given, so only internal/authz (authz.Principal.RLS) may build a request context; a hand-made
//     type with an RLS() method could otherwise ask for the bypass or any tenant with the steward flag;
//   - a composite literal of authz.Principal outside PrincipalAllowed, and a write of its
//     X-Act-On-Tenant fields ActOnAll / ActOnTenant outside ActOnAllowed: the principal of a request
//     comes from auth.RequireAuth and is completed by iam.RBAC.Authorize, nowhere else;
//   - a reference to auth.RolePlayPrincipal outside RolePlayAllowed: it builds a principal from a user id
//     without a credential, for cmd/seed --verify (Appendix D §D.3 #10); a request principal comes from
//     auth.RequireAuth;
//   - a string literal that names a request GUC (app.user_id ... app.bypass_tenant) or the tenant-move
//     and ETL GUCs, or calls set_config, outside the packages of GUCRules: the raw way around both
//     helpers.
//
// It parses every non-test Go file of the module (standard library only, no type checking), so it
// matches import names (plain, renamed or dot imports) and identifiers, not types: it catches the
// mistakes a reviewer could miss, not a deliberate evasion (a string built by concatenation, an alias
// declared in an allowed package). Test files are exempt: fixtures play the system context the way
// cmd/seed does.
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
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DBImport is the package that defines WithSystem, WithPrincipal and RLS.
const DBImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"

// AuthzImport is the package that defines the request principal.
const AuthzImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"

// InboxImport is the package of inbox.Run, which runs a consumer's callback in a WithSystem transaction.
const InboxImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"

// AuthImport is the package of auth.RolePlayPrincipal.
const AuthImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"

// JobsImport is the package of jobs.Service.Submit, whose SubmitInput.InTx hook runs in Submit's
// WithSystem transaction.
const JobsImport = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"

// Allowed are the module-relative package directories (and everything below them) that may call
// WithSystem, inbox.Run, or Submit with an InTx hook: identity and security services, storage key
// lookups after entity authorization, the anonymous public forms, the outbox relay and inbox, tenancy
// moves, and the processes without a request principal (worker consumers, scheduler jobs, ETL, seed).
// Appendix C §C.3.2 lists the same.
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

// RLSAllowed may name db.RLS: its definition and authz.Principal.RLS, the one mapping of a principal to
// the request GUCs.
var RLSAllowed = []string{"internal/platform/db", "internal/authz"}

// PrincipalAllowed may build an authz.Principal: auth.RequireAuth from a verified credential, iam while
// resolving it, and authz itself.
var PrincipalAllowed = []string{"internal/auth", "internal/iam", "internal/authz"}

// RolePlayAllowed may call auth.RolePlayPrincipal: its definition and the --verify role-play of cmd/seed.
var RolePlayAllowed = []string{"internal/auth", "cmd/seed"}

// ActOnAllowed may write Principal.ActOnAll / ActOnTenant: iam.RBAC.Authorize (X-Act-On-Tenant, C.3.9)
// and authz.
var ActOnAllowed = []string{"internal/iam", "internal/authz"}

// GUCRule is one pattern of string literals and the packages that may contain it.
type GUCRule struct {
	Name    string
	Pattern *regexp.Regexp
	Allowed []string
}

// GUCRules: the nine request GUCs are written only by internal/platform/db (WithPrincipal, WithSystem);
// app.tenant_move only by the tenant-move paths of internal/platform/tenancy; app.etl_load only by
// cmd/etl; set_config by those three (Appendix C §C.3.2).
var GUCRules = []GUCRule{
	{"a request GUC", regexp.MustCompile(`(?i)\bapp\.(user_id|tenant_id|role|driver_id|customer_ids|subtenant_ids|dispatcher|steward|bypass_tenant)\b`),
		[]string{"internal/platform/db"}},
	{"the tenant-move GUC", regexp.MustCompile(`(?i)\bapp\.tenant_move\b`), []string{"internal/platform/db", "internal/platform/tenancy"}},
	{"the ETL GUC", regexp.MustCompile(`(?i)\bapp\.etl_load\b`), []string{"internal/platform/db", "cmd/etl"}},
	{"set_config", regexp.MustCompile(`(?i)\bset_config\s*\(`), []string{"internal/platform/db", "internal/platform/tenancy", "cmd/etl"}},
}

// Finding is one forbidden reference.
type Finding struct {
	Pos  token.Position
	Pkg  string // module-relative package directory
	What string // what was found, for the message
	Fix  string // what to do instead
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s in %s, which may not widen the RLS context (%s; Appendix C §C.3.2)", f.Pos, f.What, f.Pkg, f.Fix)
}

// Check scans the module rooted at root. allowed is the WithSystem allow-list; the other rules use
// RLSAllowed, PrincipalAllowed, ActOnAllowed and GUCRules.
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
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, h := range inspect(f) {
			if h.allowed(pkg, allowed) {
				continue
			}
			out = append(out, Finding{Pos: fset.Position(h.pos), Pkg: pkg, What: h.what, Fix: h.fix})
		}
		return nil
	})
	return out, err
}

func isAllowed(pkg string, allowed []string) bool {
	return slices.ContainsFunc(allowed, func(a string) bool { return pkg == a || strings.HasPrefix(pkg, a+"/") })
}

// hit is one match of a rule; allowed decides it against the package.
type hit struct {
	pos       token.Pos
	what, fix string
	list      []string // nil: the WithSystem allow-list passed to Check
}

func (h hit) allowed(pkg string, withSystem []string) bool {
	if h.list == nil {
		return isAllowed(pkg, withSystem)
	}
	return isAllowed(pkg, h.list)
}

// importNames are the names under which f imports path, and whether it dot-imports it.
func importNames(f *ast.File, path string) (names map[string]bool, dot bool) {
	names = map[string]bool{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		switch {
		case imp.Name == nil:
			names[path[strings.LastIndexByte(path, '/')+1:]] = true
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name != "_":
			names[imp.Name.Name] = true
		}
	}
	return names, dot
}

// inspect returns every match of every rule in f.
func inspect(f *ast.File) []hit {
	dbNames, dbDot := importNames(f, DBImport)
	azNames, azDot := importNames(f, AuthzImport)
	ibNames, ibDot := importNames(f, InboxImport)
	jbNames, jbDot := importNames(f, JobsImport)
	auNames, auDot := importNames(f, AuthImport)
	importsJobs := jbDot || len(jbNames) > 0
	// Identifiers that declare or select a name rather than refer to a dot-imported one.
	declared := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			declared[n.Sel] = true // pkg.Name or value.Field: Sel is never a dot-imported name
		case *ast.FuncDecl:
			declared[n.Name] = true
		case *ast.Field:
			for _, id := range n.Names {
				declared[id] = true
			}
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok {
				declared[id] = true
			}
		case *ast.TypeSpec:
			declared[n.Name] = true
		case *ast.ValueSpec:
			for _, id := range n.Names {
				declared[id] = true
			}
		}
		return true
	})
	var out []hit
	isDB := func(e ast.Expr, sel string) bool { return isRef(e, sel, dbNames, dbDot, declared) }
	isAuthz := func(e ast.Expr, sel string) bool { return isRef(e, sel, azNames, azDot, declared) }
	isInbox := func(e ast.Expr, sel string) bool { return isRef(e, sel, ibNames, ibDot, declared) }
	isJobs := func(e ast.Expr, sel string) bool { return isRef(e, sel, jbNames, jbDot, declared) }
	isAuth := func(e ast.Expr, sel string) bool { return isRef(e, sel, auNames, auDot, declared) }
	const (
		inTxWhat = "a jobs.SubmitInput InTx hook (it runs in Submit's db.WithSystem transaction)"
		inTxFix  = "read tenant rows under db.WithPrincipal before Submit; only allow-listed packages pass a hook"
	)
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr, *ast.Ident:
			e := n.(ast.Expr)
			switch {
			case isDB(e, "WithSystem"):
				out = append(out, hit{e.Pos(), "db.WithSystem", "use db.WithPrincipal", nil})
			case isDB(e, "RLS"):
				out = append(out, hit{e.Pos(), "db.RLS", "pass the request's authz.Principal to db.WithPrincipal", RLSAllowed})
			case isAuth(e, "RolePlayPrincipal"):
				out = append(out, hit{e.Pos(), "auth.RolePlayPrincipal (a principal without a credential)",
					"take the request principal from auth.RequireAuth", RolePlayAllowed})
			case isInbox(e, "Run"):
				out = append(out, hit{e.Pos(), "inbox.Run (a db.WithSystem transaction)",
					"only allow-listed consumer packages run it; a domain consumer joins Allowed in its own task", nil})
			}
		case *ast.CompositeLit:
			t := n.Type
			if u, ok := t.(*ast.StarExpr); ok {
				t = u.X
			}
			if t != nil && isAuthz(t, "Principal") {
				out = append(out, hit{n.Pos(), "an authz.Principal literal", "use authz.PrincipalFrom(c) of a RequireAuth route", PrincipalAllowed})
			}
			// A SubmitInput literal, or an elided one (in a []jobs.SubmitInput, say), in a file of jobs.
			submitInput := importsJobs && (t == nil || isJobs(t, "SubmitInput"))
			for _, el := range n.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					id, ok := kv.Key.(*ast.Ident)
					switch {
					case ok && (id.Name == "ActOnAll" || id.Name == "ActOnTenant"):
						out = append(out, hit{kv.Pos(), id.Name, "X-Act-On-Tenant is applied by iam.RBAC.Authorize only", ActOnAllowed})
					case ok && id.Name == "InTx" && submitInput:
						out = append(out, hit{kv.Pos(), inTxWhat, inTxFix, nil})
					}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				s, ok := lhs.(*ast.SelectorExpr)
				switch {
				case ok && (s.Sel.Name == "ActOnAll" || s.Sel.Name == "ActOnTenant"):
					out = append(out, hit{s.Pos(), "a write of " + s.Sel.Name, "X-Act-On-Tenant is applied by iam.RBAC.Authorize only", ActOnAllowed})
				case ok && s.Sel.Name == "InTx" && importsJobs:
					out = append(out, hit{s.Pos(), "a write of " + inTxWhat, inTxFix, nil})
				}
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(n.Value)
			if err != nil {
				return true
			}
			for _, r := range GUCRules {
				if r.Pattern.MatchString(v) {
					out = append(out, hit{n.Pos(), "a string naming " + r.Name, "leave the GUCs to db.WithPrincipal / db.WithSystem", r.Allowed})
				}
			}
		}
		return true
	})
	return out
}

// isRef reports whether e refers to the exported name sel of a package imported under names (or
// dot-imported).
func isRef(e ast.Expr, sel string, names map[string]bool, dot bool, declared map[*ast.Ident]bool) bool {
	switch e := e.(type) {
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && names[x.Name] && e.Sel.Name == sel
	case *ast.Ident:
		return dot && e.Name == sel && !declared[e]
	}
	return false
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
		fmt.Fprintln(os.Stderr, "withsystem: the allow-lists are in tools/analyzers/withsystem/main.go (Appendix C §C.3.2)")
		os.Exit(1)
	}
}
