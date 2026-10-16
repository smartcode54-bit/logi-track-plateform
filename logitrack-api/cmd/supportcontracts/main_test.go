package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func TestSeedAndActualRegistrations(t *testing.T) {
	a, err := app.FullAPI(&app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}, zerolog.Nop(), app.APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	seed := map[string]bool{}
	for _, p := range authz.SupportPolicySeed() {
		if seed[p.ID] {
			t.Fatal("duplicate seed", p.ID)
		}
		seed[p.ID] = true
	}
	for listener, policies := range a.SupportPolicies() {
		if len(policies) == 0 {
			t.Fatal(listener)
		}
		for _, p := range policies {
			if p.Phase == 1 && !seed[p.ID] {
				t.Fatal("widened admission", p.ID)
			}
		}
	}
	if !seed["GET /v1/support/customer-options"] || seed["GET /v1/customers"] || seed["GET /v1/incidents"] || seed["GET /v1/stats/users-by-role"] {
		t.Fatal("dependency seed widened")
	}
}
func TestNewRegistrationRequiresFixtureReview(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "internal/platform/ingress/testdata")
	if err := os.MkdirAll(fixture, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "support-routes.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := run(root, false, false)
	if err == nil || !strings.Contains(err.Error(), "unknown/missing registration") {
		t.Fatal(err)
	}
}
func TestGeneratedPolicyReportMatchesSeed(t *testing.T) {
	data, err := os.ReadFile("../../api/support-policies.json")
	if err != nil {
		t.Fatal(err)
	}
	var report contracts
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.EntryEnabled || report.Phase != 1 || len(report.Seed) != len(authz.SupportPolicySeed()) {
		t.Fatal("phase/seed drift")
	}
	for _, item := range report.Seed {
		if item.Status != "bound" && !strings.HasPrefix(item.Status, "pending binding: P1.") {
			t.Fatal(item.Status)
		}
	}
}

func TestMixedOwnershipAndProvisioningRequirePredicates(t *testing.T) {
	for _, id := range []string{"DELETE /v1/api-keys/:id", "POST /v1/tenants"} {
		if !mixedPredicateRequired(id) {
			t.Fatalf("missing mixed predicate requirement: %s", id)
		}
	}
}

func TestSupportWireContractExcludesOrdinaryLeadRows(t *testing.T) {
	data, err := wireTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ResponseFormsWaitlistCounts") || strings.Contains(string(data), "ResponseFormsWaitlistEntry") {
		t.Fatal("support client contract admitted raw lead rows")
	}
}
