package main

import (
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type getAudit struct {
	Source       string   `json:"source"`
	RelativePath string   `json:"registrationPath"`
	MutationCaps []string `json:"mutationCapabilityReferences"`
	Disposition  string   `json:"disposition"`
}

var readKeys = map[string]bool{}

func init() {
	for _, key := range strings.Fields(`fleet:view_trucks fleet:view_renewals fleet:view_assignments fleet:view_live_map fleet:view_maintenance fleet:view_subcontractors drivers:view drivers:view_pii users:view users:view_sessions chat:view broadcasts:view operations:view_first_mile operations:view_line_haul operations:view_driver_monitor operations:view_incidents accounting:view_fuel accounting:view_other accounting:view_rate_card accounting:view_income accounting:billing_result accounting:shopee_report reporting:view_analytics hr:view_payroll hr:view_leave company:view packages:view security:view_overview security:view_audit security:view_mobile_clients security:view_status waitlist:view mobile:view_tasks mobile:view_history mobile:broadcasts dispatch:view_operations platform:cross_tenant_read`) {
		readKeys[key] = true
	}
}
func (a *analyzer) writeReport(root, path string) error {
	report := []getAudit{}
	for _, pkg := range a.packages {
		for _, file := range pkg.files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				path, handlers, isGET := a.managementGET(call, pkg.info)
				if !isGET {
					return true
				}
				caps := map[string]bool{}
				seen := map[types.Object]bool{}
				var walk func(ast.Node)
				walk = func(node ast.Node) {
					if node == nil {
						return
					}
					info := a.infos[node]
					if info == nil {
						return
					}
					ast.Inspect(node, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok {
							obj := info.Uses[id]
							if c, ok := obj.(*types.Const); ok && c.Pkg() != nil && c.Pkg().Path() == module+"/internal/authz" && strings.HasSuffix(types.TypeString(c.Type(), nil), "/internal/authz.Cap") && c.Val().Kind() == constant.String {
								key := constant.StringVal(c.Val())
								if !readKeys[key] {
									caps[key] = true
								}
							}
							// Only handler/factory aliases are relevant. Never descend
							// into package data tables, capability catalogs or services.
							if obj != nil && !seen[obj] && handlerType(obj.Type()) {
								seen[obj] = true
								walk(a.initializers[obj])
							}
						}
						if call, ok := n.(*ast.CallExpr); ok {
							obj := a.callee(call.Fun, info, map[types.Object]bool{})
							if obj != nil && !seen[obj] && handlerFactory(obj.Type()) {
								seen[obj] = true
								if decl := a.functions[objectKey(obj)]; decl != nil {
									walk(decl.Body)
								}
							}
						}
						return true
					})
				}
				for _, handler := range handlers {
					walk(handler)
				}

				if len(caps) == 0 {
					return true
				}
				source, _ := filepath.Rel(root, a.fset.Position(call.Pos()).Filename)
				disposition := "deferred tenant GET: denied in support OFF and ON"
				switch {
				case literal(path, pkg.info) == "/probe" || literal(path, pkg.info) == "/shopee-report.pdf":
					disposition = "side-effecting GET: support_read_only before job generation; ready artifacts need separate admission"
				case strings.Contains(source, "internal/jobs/") || strings.Contains(source, "internal/sse/"):
					disposition = "P1.4 pending source/transport binding; unknown and deferred sources denied"
				case strings.Contains(source, "internal/forms/"):
					disposition = "P1.2 pending platform counts DTO; raw lead rows denied"
				case strings.Contains(source, "internal/iam/"):
					disposition = "P1.2 pending binding: split tenant read from explicit platform predicate; legacy tenant GET denied"
				case strings.Contains(source, "internal/mobileapp/"):
					disposition = "P1.2 pending platform release read binding; existing ordinary authorization retained"
				case strings.Contains(source, "internal/dashboard/") || strings.Contains(source, "internal/fleet/") || strings.Contains(source, "internal/drivers/") || strings.Contains(source, "internal/maintenance/"):
					disposition = "P1.3 pending read-key/projection binding; denied until bound"
				}
				item := getAudit{Source: filepath.ToSlash(source), RelativePath: literal(path, pkg.info), Disposition: disposition}
				for cap := range caps {
					item.MutationCaps = append(item.MutationCaps, cap)
				}
				slices.Sort(item.MutationCaps)
				report = append(report, item)
				return true
			})
		}
	}
	slices.SortFunc(report, func(a, b getAudit) int {
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		return strings.Compare(a.RelativePath, b.RelativePath)
	})
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func handlerType(t types.Type) bool {
	sig, ok := t.Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
		return false
	}
	return types.TypeString(sig.Params().At(0).Type(), func(p *types.Package) string { return p.Path() }) == "github.com/gofiber/fiber/v3.Ctx"
}
func handlerFactory(t types.Type) bool {
	sig, ok := t.Underlying().(*types.Signature)
	return ok && sig.Results().Len() == 1 && handlerType(sig.Results().At(0).Type())
}
