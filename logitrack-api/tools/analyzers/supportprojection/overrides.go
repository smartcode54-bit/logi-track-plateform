package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Human header rejection/audit may retain the header constant. Principal acting
// flags and the retired blanket write key must never construct request reach.
func (a *analyzer) overrideCounts(root string) map[string]int {
	counts := map[string]int{}
	for _, pkg := range a.packages {
		for _, file := range pkg.files {
			relative, _ := filepath.Rel(root, a.fset.Position(file.Pos()).Filename)
			if filepath.ToSlash(relative) == "internal/authz/catalog.go" {
				continue
			}
			refusals := overrideRefusals(file, pkg.info)
			ast.Inspect(file, func(n ast.Node) bool {
				selector, ok := n.(*ast.SelectorExpr)
				if !ok || refusals[selector] {
					return true
				}
				obj := object(selector, pkg.info)
				retiredField := slices.Contains([]string{"ActOnAll", "ActOnTenant"}, selector.Sel.Name) && humanPrincipalType(pkg.info.TypeOf(selector.X))
				retiredKey := selector.Sel.Name == "PlatformCrossTenantWrite" && obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == module+"/internal/authz"
				if !retiredField && !retiredKey {
					return true
				}
				counts[filepath.ToSlash(relative)+":"+selector.Sel.Name]++
				return true
			})
		}
	}
	return counts
}

// Wrapping Principal must not hide a reintroduced compatibility field from the release gate.
func humanPrincipalType(t types.Type) bool {
	return humanPrincipalTypeSeen(t, map[types.Type]bool{})
}

func humanPrincipalTypeSeen(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	if ptr, ok := t.(*types.Pointer); ok {
		return humanPrincipalTypeSeen(ptr.Elem(), seen)
	}
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == module+"/internal/authz" && named.Obj().Name() == "Principal" {
		return true
	}
	if fields, ok := t.Underlying().(*types.Struct); ok {
		for i := 0; i < fields.NumFields(); i++ {
			field := fields.Field(i)
			if field.Embedded() && humanPrincipalTypeSeen(field.Type(), seen) {
				return true
			}
		}
	}
	return false
}

// A positive override flag that unconditionally returns a known refusal cannot
// construct reach. Keep counting inverted/conditional guards, side effects and
// all other uses; do not exempt a whole function or file containing a refusal.
func overrideRefusals(file *ast.File, info *types.Info) map[*ast.SelectorExpr]bool {
	refusals := map[*ast.SelectorExpr]bool{}
	isNil := func(e ast.Expr) bool {
		return object(e, info) == types.Universe.Lookup("nil")
	}
	var positive func(ast.Expr)
	positive = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.ParenExpr:
			positive(e.X)
		case *ast.BinaryExpr:
			if e.Op == token.LOR {
				positive(e.X)
				positive(e.Y)
			} else if e.Op == token.NEQ && isNil(e.Y) {
				if selector, ok := e.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "ActOnTenant" {
					refusals[selector] = true
				}
			}
		case *ast.SelectorExpr:
			if e.Sel.Name == "ActOnAll" {
				refusals[e] = true
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		guard, ok := n.(*ast.IfStmt)
		if !ok || guard.Init != nil || guard.Else != nil || len(guard.Body.List) != 1 {
			return true
		}
		ret, ok := guard.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 2 || !isNil(ret.Results[0]) {
			return true
		}
		call, ok := ret.Results[1].(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return true
		}
		fn, ok := object(call.Fun, info).(*types.Func)
		if ok && fn.Pkg() != nil && fn.Pkg().Path() == module+"/internal/platform/httpx" && fn.Name() == "ErrSupportViewUnavailable" {
			positive(guard.Cond)
		}
		return true
	})
	return refusals
}

func overrideRatchet(current, baseline map[string]int) error {
	var violations []string
	for key, count := range current {
		if count > baseline[key] {
			violations = append(violations, fmt.Sprintf("%s: %d retired override consumers exceed baseline %d", key, count, baseline[key]))
		}
	}
	for key, count := range baseline {
		if current[key] < count {
			violations = append(violations, fmt.Sprintf("%s: stale baseline %d; now %d (remove resolved TODO)", key, count, current[key]))
		}
	}
	slices.Sort(violations)
	if len(violations) > 0 {
		return fmt.Errorf("%s", strings.Join(violations, "\n"))
	}
	return nil
}

func (a *analyzer) humanOverrides(root string) error {
	return overrideRatchet(a.overrideCounts(root), map[string]int{})
}
func (a *analyzer) humanOverrideBaseline(root, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var baseline map[string]int
	if err := json.Unmarshal(data, &baseline); err != nil {
		return err
	}
	if err := overrideRatchet(a.overrideCounts(root), baseline); err != nil {
		return err
	}
	for key, count := range baseline {
		fmt.Fprintf(os.Stderr, "warning: D1 migration TODO %s (%d sites)\n", key, count)
	}
	return nil
}
