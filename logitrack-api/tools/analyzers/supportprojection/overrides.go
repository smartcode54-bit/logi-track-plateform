package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
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
			ast.Inspect(file, func(n ast.Node) bool {
				selector, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				obj := object(selector, pkg.info)
				if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != module+"/internal/authz" {
					return true
				}
				if !slices.Contains([]string{"ActOnAll", "ActOnTenant", "PlatformCrossTenantWrite"}, selector.Sel.Name) {
					return true
				}
				counts[filepath.ToSlash(relative)+":"+selector.Sel.Name]++
				return true
			})
		}
	}
	return counts
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
