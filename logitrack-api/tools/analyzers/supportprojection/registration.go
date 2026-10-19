package main

import (
	"go/ast"
	"go/types"
)

// Registration helpers are expanded at their call sites. Schema and handler
// parameters remain correlated; an unresolved registration still fails closed.
type argument struct {
	expr ast.Expr
	env  arguments
}
type arguments map[types.Object][]argument

func (a *analyzer) resolveArg(e ast.Expr, env arguments) []argument {
	if id, ok := e.(*ast.Ident); ok {
		if values, ok := env[object(id, a.infos[id])]; ok {
			return values
		}
	}
	return []argument{{e, env}}
}
func (a *analyzer) stringArg(v argument, depth int) string {
	if depth > 20 || v.expr == nil {
		return ""
	}
	info := a.infos[v.expr]
	if info == nil {
		return ""
	}
	if s := literal(v.expr, info); s != "" {
		return s
	}
	if id, ok := v.expr.(*ast.Ident); ok {
		obj := object(id, info)
		if values := v.env[obj]; len(values) == 1 {
			return a.stringArg(values[0], depth+1)
		}
		if init := a.initializers[obj]; init != nil {
			return a.stringArg(argument{init, v.env}, depth+1)
		}
	}
	return ""
}
func (a *analyzer) callEnv(params *ast.FieldList, call *ast.CallExpr, outer arguments) arguments {
	env := arguments{}
	for k, v := range outer {
		env[k] = v
	}
	index := 0
	for _, field := range params.List {
		for _, name := range field.Names {
			obj := a.infos[name].Defs[name]
			if index >= len(call.Args) {
				if _, variadic := field.Type.(*ast.Ellipsis); variadic {
					env[obj] = []argument{}
				}
				return env
			}
			values := []argument{}
			if _, variadic := field.Type.(*ast.Ellipsis); variadic {
				for _, arg := range call.Args[index:] {
					values = append(values, a.resolveArg(arg, outer)...)
				}
			} else {
				values = a.resolveArg(call.Args[index], outer)
			}
			env[obj] = values
			index++
		}
	}
	return env
}
func (a *analyzer) policyField(v argument, field string, depth int) string {
	if depth > 20 || v.expr == nil {
		return ""
	}
	info := a.infos[v.expr]
	if info == nil {
		return ""
	}
	switch e := v.expr.(type) {
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
					return a.stringArg(argument{kv.Value, v.env}, depth+1)
				}
			}
		}
	case *ast.Ident:
		obj := object(e, info)
		// A policy factory may assign SchemaID after constructing its base policy.
		var assigned ast.Expr
		for _, decl := range a.functions {
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				if stmt, ok := n.(*ast.AssignStmt); ok {
					for i, lhs := range stmt.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field && object(sel.X, a.infos[sel.X]) == obj && i < len(stmt.Rhs) {
							assigned = stmt.Rhs[i]
						}
					}
				}
				return true
			})
		}
		if assigned != nil {
			return a.stringArg(argument{assigned, v.env}, depth+1)
		}
		if values := v.env[obj]; len(values) == 1 {
			return a.policyField(values[0], field, depth+1)
		}
		if init := a.initializers[obj]; init != nil {
			return a.policyField(argument{init, v.env}, field, depth+1)
		}
	case *ast.CallExpr:
		// Framework constructors are trusted and are not loaded as domain source.
		// Preserve their explicit schema arguments rather than treating them as an opaque call.
		if api(e.Fun, info, "/internal/platform/httpx", "TenantReadPolicy") && len(e.Args) >= 3 {
			switch field {
			case "SchemaID":
				return a.stringArg(argument{e.Args[1], v.env}, depth+1)
			case "MetaSchemaID":
				return a.stringArg(argument{e.Args[2], v.env}, depth+1)
			default:
				return a.policyField(argument{e.Args[0], v.env}, field, depth+1)
			}
		}
		decl := a.functions[objectKey(object(e.Fun, info))]
		if decl == nil {
			return ""
		}
		env := a.callEnv(decl.Type.Params, e, v.env)
		result := ""
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
				if value := a.policyField(argument{ret.Results[0], env}, field, depth+1); value != "" {
					result = value
				}
			}
			return true
		})
		return result
	}
	return ""
}

func (a *analyzer) discoverRegistrations() {
	// Record lexical functions and their call sites, including local bind closures.
	enclosing := map[*ast.CallExpr]ast.Node{}
	functions := map[types.Object]ast.Node{}
	calls := map[types.Object][]*ast.CallExpr{}
	for _, pkg := range a.packages {
		for _, file := range pkg.files {
			var scan func(ast.Node, ast.Node)
			scan = func(node ast.Node, fn ast.Node) {
				ast.Inspect(node, func(n ast.Node) bool {
					if n == nil {
						return false
					}
					if n != node {
						switch f := n.(type) {
						case *ast.FuncDecl:
							functions[pkg.info.Defs[f.Name]] = f
							scan(f, f)
							return false
						case *ast.FuncLit:
							scan(f, f)
							return false
						}
					}
					if assign, ok := n.(*ast.AssignStmt); ok {
						for i, left := range assign.Lhs {
							if id, ok := left.(*ast.Ident); ok && i < len(assign.Rhs) {
								if lit, ok := assign.Rhs[i].(*ast.FuncLit); ok {
									functions[pkg.info.Defs[id]] = lit
								}
							}
						}
					}
					if call, ok := n.(*ast.CallExpr); ok {
						enclosing[call] = fn
						obj := object(call.Fun, pkg.info)
						calls[obj] = append(calls[obj], call)
					}
					return true
				})
			}
			scan(file, nil)
		}
	}
	var expand func(*ast.CallExpr, arguments, int)
	expand = func(bind *ast.CallExpr, env arguments, depth int) {
		if depth > 10 {
			a.fail(bind, "recursive registration helper")
			return
		}
		schema := a.policyField(argument{bind.Args[2], env}, "SchemaID", 0)
		handlers := []ast.Expr{}
		unresolved := schema == ""
		for _, handler := range bind.Args[3:] {
			for _, v := range a.resolveArg(handler, env) {
				handlers = append(handlers, v.expr)
				if id, ok := v.expr.(*ast.Ident); ok {
					if variable, ok := object(id, a.infos[id]).(*types.Var); ok && !variable.IsField() && a.initializers[variable] == nil && a.functions[objectKey(variable)] == nil { // middleware parameters are allowed; the final handler is checked below
						if handler == bind.Args[len(bind.Args)-1] {
							unresolved = true
						}
					}
				}
			}
		}
		fn := enclosing[bind]
		if unresolved && len(env) == 0 && fn != nil {
			var params *ast.FieldList
			switch f := fn.(type) {
			case *ast.FuncDecl:
				params = f.Type.Params
			case *ast.FuncLit:
				params = f.Type.Params
			}
			for obj, definition := range functions {
				if definition == fn && len(calls[obj]) > 0 {
					for _, call := range calls[obj] {
						expand(bind, a.callEnv(params, call, nil), depth+1)
					}
					return
				}
			}
		}
		a.roots = append(a.roots, root{handlers: handlers, schema: schema, meta: a.policyField(argument{bind.Args[2], env}, "MetaSchemaID", 0)})
	}
	for bind := range enclosing {
		info := a.infos[bind]
		if api(bind.Fun, info, "/internal/platform/ingress", "Bind") && len(bind.Args) >= 4 {
			expand(bind, nil, 0)
		}
	}
}
