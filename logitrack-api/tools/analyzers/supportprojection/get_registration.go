package main

import (
	"go/ast"
	"go/types"
)

// managementGET handles both Fiber's convenience method and explicit policy
// bindings. The policy argument is metadata, not a handler to traverse for caps.
func (a *analyzer) managementGET(call *ast.CallExpr, info *types.Info) (ast.Expr, []ast.Expr, bool) {
	fn := object(call.Fun, info)
	if fn == nil || fn.Pkg() == nil {
		return nil, nil, false
	}
	if fn.Pkg().Path() == "github.com/gofiber/fiber/v3" && fn.Name() == "Get" && len(call.Args) >= 2 {
		return call.Args[0], call.Args[1:], true
	}
	if fn.Pkg().Path() != module+"/internal/platform/ingress" || fn.Name() != "Bind" || len(call.Args) < 4 {
		return nil, nil, false
	}
	if a.policyMethod(call.Args[2], info, map[types.Object]bool{}) != "GET" {
		return nil, nil, false
	}
	return call.Args[1], call.Args[3:], true
}

func (a *analyzer) policyMethod(expr ast.Expr, info *types.Info, seen map[types.Object]bool) string {
	if next := a.infos[expr]; next != nil {
		info = next
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		if len(e.Args) > 0 {
			return literal(e.Args[0], info)
		}
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Method" {
					return literal(kv.Value, info)
				}
			}
		}
	case *ast.Ident:
		obj := object(e, info)
		if obj != nil && !seen[obj] {
			seen[obj] = true
			if init := a.initializers[obj]; init != nil {
				return a.policyMethod(init, info, seen)
			}
		}
	}
	return ""
}
