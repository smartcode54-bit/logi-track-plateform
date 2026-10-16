package main

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"testing"
)

func TestManagementGETExplicitBindings(t *testing.T) {
	for _, tc := range []struct {
		method, policy string
		want           bool
	}{
		{"GET", `Policy("GET", "/stats", "schema")`, true},
		{"POST", `Policy("POST", "/stats", "schema")`, false},
		{"literal", `EndpointPolicy{Method:"GET"}`, true},
		{"variable", `p`, true},
	} {
		t.Run(tc.method, func(t *testing.T) {
			expr, err := parser.ParseExpr(`ingress.Bind(r,"/stats",` + tc.policy + `,auth,manage,handler)`)
			if err != nil {
				t.Fatal(err)
			}
			call := expr.(*ast.CallExpr)
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
			ast.Inspect(expr, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					value, _ := strconv.Unquote(lit.Value)
					info.Types[lit] = types.TypeAndValue{Type: types.Typ[types.String], Value: constant.MakeString(value)}
				}
				return true
			})
			pkg := types.NewPackage(module+"/internal/platform/ingress", "ingress")
			info.Uses[call.Fun.(*ast.SelectorExpr).Sel] = types.NewFunc(token.NoPos, pkg, "Bind", nil)
			a := &analyzer{initializers: map[types.Object]ast.Expr{}}
			if tc.policy == "p" {
				obj := types.NewVar(token.NoPos, nil, "p", types.Typ[types.String])
				info.Uses[call.Args[2].(*ast.Ident)] = obj
				a.initializers[obj] = &ast.CompositeLit{Elts: []ast.Expr{&ast.KeyValueExpr{Key: ast.NewIdent("Method"), Value: call.Args[1]}}}
				info.Types[call.Args[1]] = types.TypeAndValue{Type: types.Typ[types.String], Value: constant.MakeString("GET")}
			}
			_, handlers, got := a.managementGET(call, info)
			if got != tc.want || got && len(handlers) != 3 {
				t.Fatalf("got=%v handlers=%d", got, len(handlers))
			}
		})
	}
}
