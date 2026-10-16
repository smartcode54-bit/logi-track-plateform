// Command supportprojection discovers admitted handler call graphs and their
// concrete serialized Go types. No DTO or writer inventory is maintained.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

const module = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api"

type listed struct {
	Dir, ImportPath, Export string
	GoFiles                 []string
}
type sourcePackage struct {
	path  string
	files []*ast.File
	info  *types.Info
}
type schema struct {
	typ    types.Type
	fields map[string]field
}
type field struct {
	kind   string
	nested string
	keys   bool
}
type root struct {
	handlers     []ast.Expr
	schema, meta string
}
type analyzer struct {
	fset         *token.FileSet
	packages     []*sourcePackage
	functions    map[string]*ast.FuncDecl
	infos        map[ast.Node]*types.Info
	schemas      map[string]schema
	bindings     map[string]string
	streams      map[string]bool
	initializers map[types.Object]ast.Expr
	roots        []root
	errors       []string
}

func main() {
	root := flag.String("root", ".", "module root")
	report := flag.String("report", "", "write discovered management-gated GET audit")
	overrides := flag.Bool("human-overrides", false, "refuse human header reach consumers for the atomic D1 release")
	baseline := flag.String("human-overrides-baseline", "", "ratchet retired human override consumers against a reviewed baseline")
	flag.Parse()
	a, err := load(*root)
	if err == nil {
		err = a.check()
		if err == nil && *baseline != "" {
			abs, _ := filepath.Abs(*root)
			err = a.humanOverrideBaseline(abs, *baseline)
		}
		if err == nil && *overrides {
			abs, _ := filepath.Abs(*root)
			err = a.humanOverrides(abs)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *report != "" {
		abs, _ := filepath.Abs(*root)
		if err := a.writeReport(abs, *report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("supportprojection: %d bound routes, %d discovered schemas; no unclassified writers or fields\n", len(a.roots), len(a.schemas))
}
func exportPackages(root string) ([]listed, map[string]string, error) {
	cmd := exec.Command("go", "list", "-export", "-deps", "-json", "./internal/...")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("go list: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(output)))
	var pkgs []listed
	exports := map[string]string{}
	for {
		var p listed
		err := dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		pkgs = append(pkgs, p)
		exports[p.ImportPath] = p.Export
	}
	return pkgs, exports, nil
}
func load(root string) (*analyzer, error) {
	pkgs, exports, err := exportPackages(root)
	if err != nil {
		return nil, err
	}
	a := &analyzer{fset: token.NewFileSet(), functions: map[string]*ast.FuncDecl{}, infos: map[ast.Node]*types.Info{}, schemas: map[string]schema{}}
	imp := importer.ForCompiler(a.fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
	for _, pkg := range pkgs {
		if !strings.HasPrefix(pkg.ImportPath, module+"/internal/") || strings.Contains(pkg.ImportPath, "/platform/") {
			continue
		}
		var files []*ast.File
		for _, name := range pkg.GoFiles {
			data, err := os.ReadFile(filepath.Join(pkg.Dir, name))
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(strings.TrimSpace(string(data)), "// Code generated ") {
				continue
			}
			file, err := parser.ParseFile(a.fset, filepath.Join(pkg.Dir, name), data, 0)
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
		if len(files) == 0 {
			continue
		}
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check(pkg.ImportPath, a.fset, files, info); err != nil {
			return nil, fmt.Errorf("type discovery %s: %w", pkg.ImportPath, err)
		}
		a.packages = append(a.packages, &sourcePackage{pkg.ImportPath, files, info})
	}
	a.discover()
	return a, nil
}
func literal(e ast.Expr, info *types.Info) string {
	if v, ok := info.Types[e]; ok && v.Value != nil && v.Value.Kind() == constant.String {
		return constant.StringVal(v.Value)
	}
	return ""
}
func object(e ast.Expr, info *types.Info) types.Object {
	switch e := e.(type) {
	case *ast.Ident:
		return info.Uses[e]
	case *ast.SelectorExpr:
		return info.Uses[e.Sel]
	case *ast.IndexExpr:
		return object(e.X, info)
	case *ast.IndexListExpr:
		return object(e.X, info)
	}
	return nil
}
func objectKey(o types.Object) string {
	if o == nil || o.Pkg() == nil {
		return ""
	}
	name := o.Name()
	if f, ok := o.(*types.Func); ok {
		if recv := f.Signature().Recv(); recv != nil {
			name = types.TypeString(recv.Type(), func(p *types.Package) string { return p.Path() }) + "." + name
		}
	}
	return o.Pkg().Path() + "." + name
}
func api(e ast.Expr, info *types.Info, pkg, name string) bool {
	o := object(e, info)
	return o != nil && o.Pkg() != nil && o.Pkg().Path() == module+pkg && o.Name() == name
}
func (a *analyzer) discover() {
	a.bindings = map[string]string{}
	a.streams = map[string]bool{}
	a.initializers = map[types.Object]ast.Expr{}
	for _, pkg := range a.packages {
		for _, file := range pkg.files {
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					return true
				}
				a.infos[n] = pkg.info
				switch n := n.(type) {
				case *ast.AssignStmt:
					for i, left := range n.Lhs {
						if id, ok := left.(*ast.Ident); ok && i < len(n.Rhs) {
							obj := pkg.info.Defs[id]
							if obj != nil {
								a.initializers[obj] = n.Rhs[i]
							}
						}
					}
				case *ast.ValueSpec:
					for i, name := range n.Names {
						if i < len(n.Values) {
							a.initializers[pkg.info.Defs[name]] = n.Values[i]
						}
					}
					for i, value := range n.Values {
						call, ok := value.(*ast.CallExpr)
						if !ok || i >= len(n.Names) {
							continue
						}
						if api(call.Fun, pkg.info, "/internal/platform/httpx", "NewSchema") && len(call.Args) > 0 {
							a.bindings[objectKey(pkg.info.Defs[n.Names[i]])] = literal(call.Args[0], pkg.info)
						}
					}
				case *ast.FuncDecl:
					a.functions[objectKey(pkg.info.Defs[n.Name])] = n
				case *ast.CallExpr:
					if api(n.Fun, pkg.info, "/internal/platform/httpx", "NewStreamAdapter") && len(n.Args) > 0 {
						id := literal(n.Args[0], pkg.info)
						if id != "" {
							a.streams["stream:"+id] = true
						}
					}
					if api(n.Fun, pkg.info, "/internal/platform/httpx", "NewSchema") && len(n.Args) >= 2 {
						id := literal(n.Args[0], pkg.info)
						fields := map[string]field{}
						if m, ok := n.Args[1].(*ast.CompositeLit); ok {
							for _, entry := range m.Elts {
								kv, ok := entry.(*ast.KeyValueExpr)
								if !ok {
									continue
								}
								name := literal(kv.Key, pkg.info)
								rule := field{}
								if v, ok := kv.Value.(*ast.CompositeLit); ok {
									for _, prop := range v.Elts {
										p, ok := prop.(*ast.KeyValueExpr)
										if !ok {
											continue
										}
										key, ok := p.Key.(*ast.Ident)
										if !ok {
											continue
										}
										switch key.Name {
										case "Kind":
											rule.kind = literal(p.Value, pkg.info)
										case "Schema":
											rule.nested = objectKey(object(p.Value, pkg.info))
										case "MapKeys":
											rule.keys = true
										}
									}
								}
								fields[name] = rule
							}
						}
						var typ types.Type
						if index, ok := n.Fun.(*ast.IndexExpr); ok {
							typ = pkg.info.TypeOf(index.Index)
						}
						a.schemas[id] = schema{typ, fields}
					}

				}
				return true
			})
		}
	}
	a.discoverFieldMaps()
	a.discoverRegistrations()
}
func base(t types.Type) types.Type {
	for {
		switch v := t.(type) {
		case *types.Pointer:
			t = v.Elem()
		case *types.Slice:
			t = v.Elem()
		case *types.Array:
			t = v.Elem()
		default:
			return t
		}
	}
}
func plain(t types.Type) bool {
	t = base(t)
	if t == nil {
		return false
	}
	if n, ok := t.(*types.Named); ok {
		if n.Obj().Pkg() != nil && ((n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time") || (n.Obj().Pkg().Path() == "github.com/google/uuid" && n.Obj().Name() == "UUID")) {
			return true
		}
		if n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "encoding/json" {
			return false
		}
		// Any custom marshaler needs a reviewed typed adapter.
		for i := 0; i < n.NumMethods(); i++ {
			if n.Method(i).Name() == "MarshalJSON" {
				return false
			}
		}
	}
	_, ok := t.Underlying().(*types.Basic)
	return ok
}
func (a *analyzer) fail(n ast.Node, msg string) {
	a.errors = append(a.errors, fmt.Sprintf("%s: supportprojection: %s", a.fset.Position(n.Pos()), msg))
}
func jsonFields(t types.Type) (map[string]types.Type, error) {
	out := map[string]types.Type{}
	s, ok := base(t).Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("not a typed struct: %v", t)
	}
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		if !f.Exported() && !f.Embedded() {
			continue
		}
		options := strings.Split(reflect.StructTag(s.Tag(i)).Get("json"), ",")
		tag := options[0]
		if tag == "-" {
			continue
		}
		if slices.Contains(options[1:], "omitzero") || slices.Contains(options[1:], "string") {
			return nil, fmt.Errorf("unsupported JSON options on %s", f.Name())
		}
		if f.Embedded() && tag == "" {
			embedded, err := jsonFields(f.Type())
			if err != nil {
				return nil, err
			}
			for k, v := range embedded {
				if out[k] != nil {
					return nil, fmt.Errorf("ambiguous embedded field %s", k)
				}
				out[k] = v
			}
			continue
		}
		if tag == "" {
			tag = f.Name()
		}
		if out[tag] != nil {
			return nil, fmt.Errorf("ambiguous field %s", tag)
		}
		out[tag] = f.Type()
	}
	return out, nil
}
func (a *analyzer) validate(t types.Type, s schema, seen map[string]bool) error {
	if t == nil || s.typ == nil || types.TypeString(base(t), nil) != types.TypeString(base(s.typ), nil) {
		return fmt.Errorf("unregistered concrete response type %v", t)
	}
	key := types.TypeString(base(t), nil)
	if seen[key] {
		return fmt.Errorf("recursive schema %s", key)
	}
	seen[key] = true
	defer delete(seen, key)
	fields, err := jsonFields(t)
	if err != nil {
		return err
	}
	if len(fields) != len(s.fields) {
		return fmt.Errorf("incomplete schema for %v: discovered %d JSON-visible fields, classified %d", t, len(fields), len(s.fields))
	}
	for name, typ := range fields {
		rule, ok := s.fields[name]
		if !ok {
			return fmt.Errorf("unclassified field %s", name)
		}
		if m, ok := typ.Underlying().(*types.Map); ok {
			if !rule.keys {
				return fmt.Errorf("unclassified map keys %s", name)
			}
			typ = m.Elem()
		}
		switch rule.kind {
		case "safe", "person", "money", "address", "free_text":
			if !plain(typ) {
				return fmt.Errorf("unclassified nested/dynamic field %s (%v)", name, typ)
			}
		case "nested":
			// The concrete child schema must be discovered, never inferred from fixture values.
			child, found := a.schemas[a.bindings[rule.nested]]
			if !found {
				return fmt.Errorf("missing child schema %s", name)
			}
			if err := a.validate(typ, child, seen); err != nil {
				return err
			}
		case "file", "secret":
		default:
			return fmt.Errorf("unclassified field kind %s", name)
		}
	}
	return nil
}
func (a *analyzer) check() error {
	for _, r := range a.roots {
		s, ok := a.schemas[r.schema]
		if !ok && r.schema != "empty" && !a.streams[r.schema] {
			a.errors = append(a.errors, "supportprojection: missing schema "+r.schema)
			continue
		}
		seen := map[string]bool{}
		visitedInit := map[ast.Expr]bool{}
		var walk func(ast.Node)
		walk = func(n ast.Node) {
			if n == nil {
				return
			}
			info := a.infos[n]
			if info == nil {
				return
			}
			ast.Inspect(n, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ci := a.infos[call]
				if ci == nil {
					ci = info
				}
				if api(call.Fun, ci, "/internal/platform/httpx", "JSON") || api(call.Fun, ci, "/internal/platform/httpx", "JSONWithRedactions") || api(call.Fun, ci, "/internal/platform/httpx", "Page") || api(call.Fun, ci, "/internal/platform/httpx", "PageAs") {
					index, metaIndex := 2, -1
					if api(call.Fun, ci, "/internal/platform/httpx", "Page") {
						index, metaIndex = 1, 3
					}
					responseSchema := s
					if api(call.Fun, ci, "/internal/platform/httpx", "PageAs") {
						responseSchema = a.schemas[literal(call.Args[1], ci)]
						metaIndex = 4
					}
					if len(call.Args) > index {
						if err := a.validate(ci.TypeOf(call.Args[index]), responseSchema, map[string]bool{}); err != nil {
							a.fail(call, err.Error())
						}
					}
					if metaIndex >= 0 && len(call.Args) > metaIndex {
						if id, ok := call.Args[metaIndex].(*ast.Ident); !ok || id.Name != "nil" {
							meta, ok := a.schemas[r.meta]
							if !ok {
								a.fail(call, "unregistered metadata")
							} else if err := a.validate(ci.TypeOf(call.Args[metaIndex]), meta, map[string]bool{}); err != nil {
								a.fail(call, err.Error())
							}
						}
					}
					return true
				}
				if api(call.Fun, ci, "/internal/platform/httpx", "Empty") {
					if r.schema != "empty" {
						a.fail(call, "unregistered bodyless response")
					}
					return true
				}
				if api(call.Fun, ci, "/internal/platform/httpx", "Stream") {
					if !strings.HasPrefix(r.schema, "stream:") {
						a.fail(call, "unregistered stream adapter")
					}
					return true
				}
				callee := a.callee(call.Fun, ci, map[types.Object]bool{})
				if responseWriter(call, ci, callee) && slices.Contains([]string{"JSON", "JSONP", "Send", "SendString", "SendStream", "SendStreamWriter", "SendFile", "SendStatus", "Redirect", "Write", "WriteString", "SetBody", "SetBodyString", "SetBodyStream", "SetBodyStreamWriter", "Hijack"}, callee.Name()) {
					a.fail(call, "direct response writer "+callee.Name())
					return true
				}
				if init := a.initializers[object(call.Fun, ci)]; init != nil && !visitedInit[init] {
					visitedInit[init] = true
					walk(init)
				}
				key := objectKey(callee)
				decl := a.functions[key]
				_, literal := a.initializers[object(call.Fun, ci)].(*ast.FuncLit)
				if decl == nil && !literal && responseContextCall(call, ci) && !trustedContextCall(callee) {
					a.fail(call, "unresolved response-context callee requires a typed registered adapter")
				}
				if key == "" || seen[key] {
					return true
				}
				seen[key] = true
				if decl != nil {
					walk(decl.Body)
				}
				return true
			})
		}
		for index, handler := range r.handlers {
			info := a.infos[handler]
			if info == nil {
				continue
			}
			if decl := a.functions[objectKey(object(handler, info))]; decl != nil {
				walk(decl.Body)
			} else if fn, ok := handler.(*ast.FuncLit); ok {
				walk(fn.Body)
			} else if init := a.initializers[object(handler, info)]; init != nil {
				walk(init)
			} else if index == len(r.handlers)-1 {
				a.fail(handler, "unresolved handler requires a typed registered adapter")
			}
		}
	}
	if len(a.errors) > 0 {
		slices.Sort(a.errors)
		a.errors = slices.Compact(a.errors)
		return fmt.Errorf("%s", strings.Join(a.errors, "\n"))
	}
	return nil
}

func responseContextCall(call *ast.CallExpr, info *types.Info) bool {
	for _, arg := range call.Args {
		t := info.TypeOf(arg)
		if t != nil && types.TypeString(types.Unalias(t), func(p *types.Package) string { return p.Path() }) == "github.com/gofiber/fiber/v3.Ctx" {
			return true
		}
	}
	return false
}

func (a *analyzer) callee(expr ast.Expr, info *types.Info, seen map[types.Object]bool) types.Object {
	if call, ok := expr.(*ast.CallExpr); ok {
		return a.callee(call.Fun, info, seen)
	}
	obj := object(expr, info)
	if obj == nil || seen[obj] {
		return nil
	}
	seen[obj] = true
	if init := a.initializers[obj]; init != nil {
		if ii := a.infos[init]; ii != nil {
			return a.callee(init, ii, seen)
		}
	}
	return obj
}

// These shared context accessors/guards are part of the reviewed admission boundary.
func trustedContextCall(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	if obj.Pkg().Path() == module+"/internal/platform/httpx" {
		switch obj.Name() {
		case "WriteError", "RequireResponseProjection", "ResolveProjectionAudience", "OrdinaryPage", "ClientIPFrom", "ListenerFrom", "SetProjectionAudience", "EndpointGate", "ProjectionAudienceFrom", "EndpointPolicyFrom", "PolicyTargetFrom", "RequestIDFrom", "PolicyInput", "SetPolicyInput", "StrictQuery", "StrictBody", "DecodeJSON":
			return true
		}
	}
	return (obj.Pkg().Path() == module+"/internal/authz" || obj.Pkg().Path() == module+"/internal/auth") && obj.Name() == "PrincipalFrom"
}

func responseWriter(call *ast.CallExpr, info *types.Info, obj types.Object) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if typ := info.TypeOf(sel.X); typ != nil && types.TypeString(typ, nil) == "hash.Hash" {
			return false
		}
	}
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	path := obj.Pkg().Path()
	return path == "github.com/gofiber/fiber/v3" || path == "github.com/valyala/fasthttp" || path == "net/http" || path == "io" || path == "bufio"
}
