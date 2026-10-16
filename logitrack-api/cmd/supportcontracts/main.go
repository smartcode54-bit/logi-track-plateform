// Command supportcontracts checks registrations against the reviewed Phase 1
// seed, writes the pending-binding report and generates shared wire types.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

//go:generate go run . -root ../.. -write
//go:generate go run ../../tools/analyzers/supportprojection -root ../.. -report ../../api/support-discovery.json

type pending struct {
	Policy httpx.EndpointPolicy `json:"policy"`
	Status string               `json:"status"`
}
type contracts struct {
	EntryEnabled bool                              `json:"entryEnabled"`
	Phase        int                               `json:"phase"`
	Seed         []pending                         `json:"allowlistSeed"`
	Routes       map[string][]httpx.EndpointPolicy `json:"routes"`
	Schemas      []string                          `json:"schemas"`
}

func main() {
	root := flag.String("root", ".", "API module root")
	write := flag.Bool("write", false, "write generated outputs")
	freeze := flag.Bool("freeze-routes", false, "create the reviewed route fixture (never used by gen)")
	verify := flag.Bool("verify-generation", false, "verify reproducible generated output in an uncommitted worktree")
	ownership := flag.Bool("ownership", false, "verify changed physical files belong to P1.0")
	flag.Parse()
	if *verify {
		if err := verifyGeneration(*root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *ownership {
		if err := checkOwnership(*root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("ownership: every changed physical file belongs to P1.0")
		return
	}
	if err := run(*root, *write, *freeze); err != nil {
		fmt.Fprintln(os.Stderr, "supportcontracts:", err)
		os.Exit(1)
	}
}
func run(root string, write, freeze bool) error {
	a, err := app.FullAPI(&app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}, zerolog.Nop(), app.APIDeps{})
	if err != nil {
		return err
	}
	if err = a.CheckRoutes(); err != nil {
		return err
	}
	out := contracts{EntryEnabled: httpx.SupportEntryEnabled, Phase: 1, Routes: a.SupportPolicies(), Schemas: httpx.SchemaIDs()}
	seeded := map[string]bool{}
	for _, seed := range authz.SupportPolicySeed() {
		if seeded[seed.ID] {
			return fmt.Errorf("duplicate seed %s", seed.ID)
		}
		seeded[seed.ID] = true
		status := "pending binding: " + seed.Owner
		for _, bound := range out.Routes[ingress.Internal] {
			if bound.ID != seed.ID || bound.Phase != 1 {
				continue
			}
			if bound.Method != seed.Method || bound.Route != seed.Route || bound.Class != seed.Class || bound.Plane != seed.Plane || bound.Target != seed.Target || bound.PredicateID != seed.PredicateID || bound.AllowHEAD != seed.AllowHEAD || bound.Owner != seed.Owner {
				return fmt.Errorf("binding differs from reviewed seed: %s", seed.ID)
			}
			if mixedPredicateRequired(seed.ID) && bound.Predicate == nil {
				return fmt.Errorf("missing strict mixed predicate: %s", seed.ID)
			}
			if bound.Target != "selected_tenant" && bound.Target != "authenticated_caller" && bound.ResolveTarget == nil {
				return fmt.Errorf("unresolved target resolver: %s", seed.ID)
			}
			if strings.HasPrefix(bound.SchemaID, "pending:") {
				return fmt.Errorf("placeholder schema bound: %s", seed.ID)
			}
			status = "bound"
		}
		out.Seed = append(out.Seed, pending{seed, status})
	}
	for listener, policies := range out.Routes {
		for _, p := range policies {
			if !httpx.ValidPolicy(p) {
				return fmt.Errorf("incomplete policy: %s %s", listener, p.ID)
			}
			if p.Phase == 1 && !seeded[p.ID] {
				return fmt.Errorf("unreviewed admission: %s", p.ID)
			}
		}
	}
	fixture := filepath.Join(root, "internal/platform/ingress/testdata/support-routes.json")
	identities := map[string][]string{}
	for listener, policies := range out.Routes {
		for _, p := range policies {
			identities[listener] = append(identities[listener], p.ID)
		}
		slices.Sort(identities[listener])
	}
	data, _ := json.MarshalIndent(identities, "", "  ")
	data = append(data, '\n')
	if freeze {
		if err := os.MkdirAll(filepath.Dir(fixture), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(fixture, data, 0644); err != nil {
			return err
		}
	} else {
		old, err := os.ReadFile(fixture)
		if err != nil {
			return err
		}
		if string(old) != string(data) {
			return fmt.Errorf("unknown/missing registration: review support-routes.json before changing the fixture")
		}
	}
	data, err = json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := output(filepath.Join(root, "api/support-policies.json"), data, write); err != nil {
		return err
	}
	types, err := wireTypes()
	if err != nil {
		return err
	}
	return output(filepath.Join(root, "../shared-docs/schemas/supportAccess.ts"), types, write)
}
func output(path string, data []byte, write bool) error {
	if write {
		return os.WriteFile(path, data, 0644)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(old) != string(data) {
		return fmt.Errorf("stale generated contract %s", path)
	}
	return nil
}
func wireTypes() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(`// Code generated by logitrack-api/cmd/supportcontracts. DO NOT EDIT.
// Classified schemas are the monetary wire contract. Deferred domains add DTOs on admission.
export type MoneyValue = number | null;
export type DecimalMoneyValue = string | null;
export type RedactionKind = "person" | "money" | "address" | "file" | "free_text" | "secret";
export interface RedactedField { path: string; kind: RedactionKind }
export interface SupportAccessMeta { masked: boolean; version: string }
export interface ProjectionMeta {
  supportAccess?: SupportAccessMeta;
  redacted: readonly RedactedField[];
}
export type SupportRefusalCode = "support_read_only" | "support_view_unavailable" | "support_projection_unavailable" | "support_access_masked";
export const SUPPORT_ENTRY_ENABLED = false as const;
`)
	schemas, err := httpx.SchemaContracts()
	if err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		fmt.Fprintf(&out, "export interface %s {\n", schema.Name)
		for _, field := range schema.Fields {
			optional := ""
			if field.Optional {
				optional = "?"
			}
			fmt.Fprintf(&out, "  %s%s: %s;\n", field.Name, optional, field.Type)
		}
		out.WriteString("}\n")
	}
	return out.Bytes(), nil
}

func mixedPredicateRequired(id string) bool {
	switch id {
	case "DELETE /v1/api-keys/:id", "POST /v1/tenants", "GET /v1/roles/matrix", "PUT /v1/roles/matrix", "POST /v1/api-keys", "PATCH /v1/tenants/:id", "PUT /v1/users/:id/scopes/:kind", "DELETE /v1/users/:id/scopes/:kind", "POST /v1/app-releases/presign":
		return true
	}
	return false
}
