package scope

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
)

// views reads the column lists of the scope_* views from migrations/ (Appendix C §C.3.7).
func views(t *testing.T) map[string][]string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE VIEW (scope_[a-z_]+) WITH \(security_invoker = true\) AS\s+SELECT (.*?)\s+FROM`)
	files, err := filepath.Glob(filepath.Join(moduleRoot(t), "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			var cols []string
			for c := range strings.SplitSeq(m[2], ",") {
				cols = append(cols, strings.TrimSpace(c))
			}
			out[m[1]] = cols
		}
	}
	return out
}

// camel is sqlc's camel JSON tag of a column name.
func camel(col string) string {
	parts := strings.Split(col, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// TestScopeDTOsAreTheViews: every scope DTO has exactly the columns of its view (Appendix C §C.9.2 #11),
// and neither the views nor the DTOs carry a billing, party, evidence, HR, PII, insurance, tax or cost
// column.
func TestScopeDTOsAreTheViews(t *testing.T) {
	v := views(t)
	want := []string{"scope_drivers", "scope_incidents", "scope_standby", "scope_tasks", "scope_tenants", "scope_trips", "scope_trucks"}
	got := make([]string, 0, len(v))
	for name := range v {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("scope views %v, want the seven of §C.3.7 %v", got, want)
	}
	for name, cols := range v {
		for _, c := range cols {
			if IsHidden(c) {
				t.Errorf("view %s exposes %s", name, c)
			}
		}
	}
	dtos := map[string]any{
		"scope_tasks": scopedb.ScopeTask{}, "scope_trips": scopedb.ScopeTrip{}, "scope_standby": scopedb.ScopeStandby{},
		"scope_incidents": scopedb.ScopeIncident{}, "scope_drivers": scopedb.ScopeDriver{},
		"scope_trucks": scopedb.ScopeTruck{}, "scope_tenants": scopedb.ScopeTenant{},
	}
	for name, dto := range dtos {
		typ := reflect.TypeOf(dto)
		var tags []string
		for f := range typ.Fields() {
			tags = append(tags, f.Tag.Get("json"))
		}
		var wantTags []string
		for _, c := range v[name] {
			wantTags = append(wantTags, camel(c))
		}
		if !slices.Equal(tags, wantTags) {
			t.Errorf("%s DTO keys %v, view columns %v", typ.Name(), tags, wantTags)
		}
	}
}

func TestIsHidden(t *testing.T) {
	for _, c := range []string{"billing_party_id", "billing_date", "evidence_token", "id_card", "license_file_id",
		"birth_date", "insurance_premium_thb", "tax_expense_thb", "customer_party_id", "source_linked_party_id",
		"review_status", "ocr_data", "legacy_doc_id", "created_by"} {
		if !IsHidden(c) {
			t.Errorf("%s must be hidden", c)
		}
	}
	for _, c := range []string{"id", "tenant_id", "license_plate", "truck_license_plate_snapshot", "status", "plan_at"} {
		if IsHidden(c) {
			t.Errorf("%s is a projected column", c)
		}
	}
}
