package main

import (
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiscoverNegativeFixturesWithoutDTOInventory(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	_, exports, err := exportPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture := `package fixture
import (
 "github.com/gofiber/fiber/v3"
 "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
 "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)
type Child struct { Label string ` + "`json:\"label\"`" + ` /* CHILD */ }
var child = httpx.NewSchema[Child]("child",map[string]httpx.Field{"label":{Kind:httpx.Person}})
type Output struct { Child Child ` + "`json:\"child\"`" + ` /* OUTPUT */ }
var schema = httpx.NewSchema[Output]("output",map[string]httpx.Field{"child":{Kind:httpx.Nested,Schema:child}})
type NewDTO struct { Value string ` + "`json:\"value\"`" + ` }
func dynamic() any {return Output{}}
func childFields() map[string]httpx.Field { out:=map[string]httpx.Field{}; for _,name:=range []string{"label"} {out[name]=httpx.Field{Kind:httpx.Person}};return out }
func helper(c fiber.Ctx)error{ return httpx.JSON(c,200,Output{}) }
type Renderer interface { Render(fiber.Ctx) error }
var renderer Renderer
var dynamicWriter func(fiber.Ctx) error
func viaParam(c fiber.Ctx, render func(fiber.Ctx) error) error { return render(c) }
func Mounted(r fiber.Router){ingress.Bind(r,"",httpx.EndpointPolicy{SchemaID:"output"},func(c fiber.Ctx)error{ /* WRITE */ return helper(c) })}
`
	for _, tc := range []struct {
		name, from, to string
		bad            bool
	}{
		{"complete", "", "", false},
		{"wrapped complete", "", "", false},
		{"wrapped new DTO", "httpx.JSON(c,200,Output{})", "httpx.JSON(c,200,NewDTO{})", true},
		{"wrapped direct writer", "httpx.JSON(c,200,Output{})", "c.JSON(Output{})", true},
		{"helper fields complete", "", "", false},
		{"helper fields nested addition", "/* CHILD */", "; Added string `json:\"added,omitempty\"`", true},
		{"sealed page wrong type", "httpx.JSON(c,200,Output{})", `httpx.SupportPage(c,"child",Output{},"",nil)`, true},
		{"sealed page unknown metadata", "httpx.JSON(c,200,Output{})", `httpx.SupportPage(c,"output",Output{},"",map[string]string{"secret":"sentinel"})`, true},
		{"explicit variant wrong type", "httpx.JSON(c,200,Output{})", `httpx.PageAs(c,"child",Output{},"",nil)`, true},
		{"explicit variant unknown metadata", "httpx.JSON(c,200,Output{})", `httpx.PageAs(c,"output",Output{},"",map[string]string{"secret":"sentinel"})`, true},
		{"resolved function delegate", "return helper(c)", "render:=helper; return render(c)", false},
		{"resolved literal delegate", "return helper(c)", "render:=func(c fiber.Ctx) error{return helper(c)}; return render(c)", false},
		{"unsupported string option", `json:"label"`, `json:"label,string"`, true},
		{"unsupported omitzero option", `json:"label"`, `json:"label,omitzero"`, true},
		{"unregistered stream", `SchemaID:"output"`, `SchemaID:"stream:missing"`, true},
		{"nested omitted field", "/* CHILD */", "; Added string `json:\"added,omitempty\"`", true},
		{"embedded promotion", "/* OUTPUT */", "; Embedded", true},
		{"new concrete DTO", "httpx.JSON(c,200,Output{})", "httpx.JSON(c,200,NewDTO{})", true},
		{"dynamic interface", "httpx.JSON(c,200,Output{})", "httpx.JSON(c,200,dynamic())", true},
		{"direct writer alias", "return httpx.JSON(c,200,Output{})", "send:=c.JSON; return send(Output{})", true},
		{"direct writer in helper", "httpx.JSON(c,200,Output{})", "c.JSON(Output{})", true},
		{"interface response delegate", "return helper(c)", "return renderer.Render(c)", true},
		{"function response delegate", "return helper(c)", "return dynamicWriter(c)", true},
		{"function parameter delegate", "return helper(c)", "return viaParam(c, helper)", true},
		{"raw fasthttp writer", "/* WRITE */", "c.RequestCtx().Response.SetBodyString(\"sentinel\");", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fixture
			if strings.HasPrefix(tc.name, "wrapped ") {
				source += `
func policy(schema string) httpx.EndpointPolicy { p:=httpx.EndpointPolicy{};p.SchemaID=schema;return p }
func bind(r fiber.Router,schema string,handler any,handlers ...any) {ingress.Bind(r,"",policy(schema),handler,handlers...)}
`
				source = strings.Replace(source, `ingress.Bind(r,"",httpx.EndpointPolicy{SchemaID:"output"},`, `bind(r,"output",`, 1)
			}
			if strings.HasPrefix(tc.name, "helper fields ") {
				source = strings.Replace(source, `map[string]httpx.Field{"label":{Kind:httpx.Person}}`, `childFields()`, 1)
			}
			if tc.name == "embedded promotion" {
				source += "\ntype Embedded struct { Secret string `json:\"secret,omitempty\"` }\n"
			}
			if tc.from != "" {
				source = strings.Replace(source, tc.from, tc.to, 1)
			}
			a := &analyzer{fset: token.NewFileSet(), functions: map[string]*ast.FuncDecl{}, infos: map[ast.Node]*types.Info{}, schemas: map[string]schema{}}
			file, err := parser.ParseFile(a.fset, "mounted.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			conf := types.Config{Importer: importer.ForCompiler(a.fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })}
			if _, err := conf.Check(module+"/internal/fixture", a.fset, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			a.packages = []*sourcePackage{{module + "/internal/fixture", []*ast.File{file}, info}}
			a.discover()
			err = a.check()
			if (err != nil) != tc.bad {
				t.Fatalf("bad=%v, diagnostic=%v", tc.bad, err)
			}
		})
	}
}

func TestManagementGETDiscoveryFollowsSharedHandlers(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	a, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(t.TempDir(), "audit.json")
	if err := a.writeReport(root, reportPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report []getAudit
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	expected := map[string][]string{
		"internal/iam/admin_http.go|/:id/sessions": {"users:revoke_sessions"},
		"internal/iam/apikeys_http.go|":            {"security:manage_api_keys"},
		"internal/iam/http.go|/matrix":             {"security:manage_roles"},
		"internal/parties/http.go|/:id":            {"fleet:manage_customers"},
		"internal/hr/http.go|/config":              {"hr:manage_payroll"},
		"internal/mobileapp/http.go|":              {"security:manage_mobile_release"},
	}
	for key, want := range expected {
		found := false
		for _, item := range report {
			if item.Source+"|"+item.RelativePath == key {
				found = true
				if !slices.Equal(item.MutationCaps, want) {
					t.Fatalf("%s: %v want %v", key, item.MutationCaps, want)
				}
			}
		}
		if !found {
			t.Fatal("guard missed", key)
		}
	}
	for _, item := range report {
		if len(item.MutationCaps) > 4 {
			t.Fatalf("catalog contamination: %+v", item)
		}
	}
}

func TestOverrideRatchet(t *testing.T) {
	baseline := map[string]int{"a.go:ActOnAll": 2}
	for _, tc := range []struct {
		current map[string]int
		bad     bool
	}{
		{map[string]int{"a.go:ActOnAll": 2}, false},
		{map[string]int{"a.go:ActOnAll": 3}, true},
		{map[string]int{"a.go:ActOnAll": 1}, true},
		{map[string]int{"a.go:ActOnAll": 2, "b.go:ActOnAll": 1}, true},
	} {
		if err := overrideRatchet(tc.current, baseline); (err != nil) != tc.bad {
			t.Fatal(tc, err)
		}
	}
}

func TestOverrideCountsDistinguishRefusalsFromReach(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	_, exports, err := exportPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"refuse all", `if p.ActOnAll { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 0},
		{"refuse tenant", `if p.ActOnTenant != nil { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 0},
		{"refuse either", `if authz.IsSupport(p) || p.TenantID == nil || p.ActOnAll || p.ActOnTenant != nil { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 0},
		{"parenthesized refusal", `if (p.ActOnAll) { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 0},
		{"refusal then reach", `if p.ActOnAll { return nil, httpx.ErrSupportViewUnavailable() }; return p.ActOnTenant,nil`, 1},
		{"guard initializer", `if p.Steward=true; p.ActOnAll { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"inverted all", `if !p.ActOnAll { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"inverted tenant", `if p.ActOnTenant == nil { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"conditional refusal", `if p.ActOnAll && p.Steward { return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"else reach", `if p.ActOnAll { return nil, httpx.ErrSupportViewUnavailable() } else { return p,nil }`, 1},
		{"side effect", `if p.ActOnAll { p.Steward=true; return nil, httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"allowed return", `if p.ActOnAll { return p,nil }; return nil,httpx.ErrSupportViewUnavailable()`, 1},
		{"tenant reach", `return p.ActOnTenant,httpx.ErrSupportViewUnavailable()`, 1},
		{"write override", `if p.Can(authz.PlatformCrossTenantWrite) { return nil,httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
		{"shadowed error", `httpx := struct{ErrSupportViewUnavailable func() error}{func() error{return nil}}; if p.ActOnAll { return nil,httpx.ErrSupportViewUnavailable() }; return nil,nil`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &analyzer{fset: token.NewFileSet()}
			source := `package fixture
import (
 "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
 "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)
var _ = httpx.ErrSupportViewUnavailable
type legacyPrincipal struct { authz.Principal; ActOnAll bool; ActOnTenant *string }
func read(p *legacyPrincipal) (any,error) { ` + strings.ReplaceAll(tc.body, "authz.IsSupport(p)", "authz.IsSupport(&p.Principal)") + ` }`
			file, err := parser.ParseFile(a.fset, filepath.Join(root, "fixture.go"), source, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			conf := types.Config{Importer: importer.ForCompiler(a.fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })}
			if _, err := conf.Check(module+"/internal/fixture", a.fset, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			a.packages = []*sourcePackage{{module + "/internal/fixture", []*ast.File{file}, info}}
			got := 0
			for _, count := range a.overrideCounts(root) {
				got += count
			}
			if got != tc.want {
				t.Fatalf("override consumers = %d, want %d", got, tc.want)
			}
		})
	}
}
