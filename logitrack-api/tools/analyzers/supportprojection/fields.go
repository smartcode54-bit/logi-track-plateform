package main

import (
	"go/ast"
	"go/types"
	"strings"
)

// Evaluate only constant map construction: literals, local assignments, bounded
// ranges and helper calls. Unknown expressions produce no classification.
func (a *analyzer) fieldValue(e ast.Expr, env map[types.Object]any, depth int) any {
	if e == nil || depth > 30 {
		return nil
	}
	info := a.infos[e]
	if info == nil {
		return nil
	}
	if value, ok := info.Types[e]; ok && value.Value != nil {
		return literal(e, info)
	}
	switch e := e.(type) {
	case *ast.Ident:
		obj := object(e, info)
		if v, ok := env[obj]; ok {
			return v
		}
		if init := a.initializers[obj]; init != nil {
			return a.fieldValue(init, env, depth+1)
		}
	case *ast.SelectorExpr:
		if v, ok := a.fieldValue(e.X, env, depth+1).(map[string]any); ok {
			return v[e.Sel.Name]
		}
	case *ast.CompositeLit:
		typ := info.TypeOf(e)
		if typ == nil {
			return nil
		}
		switch typ.Underlying().(type) {
		case *types.Slice, *types.Array:
			values := []any{}
			for _, elt := range e.Elts {
				values = append(values, a.fieldValue(elt, env, depth+1))
			}
			return values
		case *types.Map, *types.Struct:
			values := map[string]any{}
			for i, elt := range e.Elts {
				key := ""
				value := elt
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					value = kv.Value
					if _, isStruct := typ.Underlying().(*types.Struct); isStruct {
						if id, ok := kv.Key.(*ast.Ident); ok {
							key = id.Name
						}
					} else {
						key, _ = a.fieldValue(kv.Key, env, depth+1).(string)
					}
				} else if st, ok := typ.Underlying().(*types.Struct); ok && i < st.NumFields() {
					key = st.Field(i).Name()
				}
				if key == "Schema" {
					values[key] = objectKey(object(value, a.infos[value]))
				} else {
					values[key] = a.fieldValue(value, env, depth+1)
				}
			}
			return values
		}
	case *ast.CallExpr:
		obj := object(e.Fun, info)
		if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "strings" && obj.Name() == "Fields" && len(e.Args) == 1 {
			s, ok := a.fieldValue(e.Args[0], env, depth+1).(string)
			if !ok {
				return nil
			}
			v := []any{}
			for _, word := range strings.Fields(s) {
				v = append(v, word)
			}
			return v
		}
		var params *ast.FieldList
		var body *ast.BlockStmt
		if fn, ok := e.Fun.(*ast.FuncLit); ok {
			params, body = fn.Type.Params, fn.Body
		} else if decl := a.functions[objectKey(obj)]; decl != nil {
			params, body = decl.Type.Params, decl.Body
		}
		if body == nil {
			return nil
		}
		local := map[types.Object]any{}
		for k, v := range env {
			local[k] = v
		}
		index := 0
		for _, param := range params.List {
			for _, name := range param.Names {
				if index >= len(e.Args) {
					return nil
				}
				local[a.infos[name].Defs[name]] = a.fieldValue(e.Args[index], env, depth+1)
				index++
			}
		}
		value, _ := a.fieldBlock(body, local, depth+1)
		return value
	}
	return nil
}
func (a *analyzer) fieldBlock(body *ast.BlockStmt, env map[types.Object]any, depth int) (any, bool) {
	if depth > 30 {
		return nil, false
	}
	for _, stmt := range body.List {
		switch stmt := stmt.(type) {
		case *ast.ReturnStmt:
			if len(stmt.Results) == 1 {
				return a.fieldValue(stmt.Results[0], env, depth+1), true
			}
			return nil, true
		case *ast.AssignStmt:
			values := []any{}
			for _, rhs := range stmt.Rhs {
				values = append(values, a.fieldValue(rhs, env, depth+1))
			}
			for i, lhs := range stmt.Lhs {
				if i >= len(values) {
					return nil, false
				}
				switch lhs := lhs.(type) {
				case *ast.Ident:
					obj := object(lhs, a.infos[lhs])
					if obj == nil {
						obj = a.infos[lhs].Defs[lhs]
					}
					env[obj] = values[i]
				case *ast.IndexExpr:
					m, ok := a.fieldValue(lhs.X, env, depth+1).(map[string]any)
					key, good := a.fieldValue(lhs.Index, env, depth+1).(string)
					if !ok || !good {
						return nil, false
					}
					m[key] = values[i]
				default:
					return nil, false
				}
			}
		case *ast.RangeStmt:
			value := a.fieldValue(stmt.X, env, depth+1)
			run := func(key, value any) (any, bool) {
				for name, v := range map[ast.Expr]any{stmt.Key: key, stmt.Value: value} {
					if id, ok := name.(*ast.Ident); ok {
						obj := object(id, a.infos[id])
						if obj == nil {
							obj = a.infos[id].Defs[id]
						}
						env[obj] = v
					}
				}
				return a.fieldBlock(stmt.Body, env, depth+1)
			}
			switch v := value.(type) {
			case []any:
				for i, elt := range v {
					if result, done := run(i, elt); done {
						return result, true
					}
				}
			case map[string]any:
				for key, elt := range v {
					if result, done := run(key, elt); done {
						return result, true
					}
				}
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return nil, false
}
func (a *analyzer) discoverFieldMaps() {
	for _, pkg := range a.packages {
		for _, file := range pkg.files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !api(call.Fun, pkg.info, "/internal/platform/httpx", "NewSchema") || len(call.Args) < 2 {
					return true
				}
				id := literal(call.Args[0], pkg.info)
				s := a.schemas[id]
				s.fields = map[string]field{}
				values, _ := a.fieldValue(call.Args[1], map[types.Object]any{}, 0).(map[string]any)
				for name, value := range values {
					props, ok := value.(map[string]any)
					if !ok {
						continue
					}
					kind, _ := props["Kind"].(string)
					nested, _ := props["Schema"].(string)
					keys, _ := props["MapKeys"].([]any)
					s.fields[name] = field{kind: kind, nested: nested, keys: len(keys) > 0}
				}
				a.schemas[id] = s
				return true
			})
		}
	}
}
