package authz

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The DDL CHECK of role_capability_overrides.capability (Appendix A 0002_identity).
var colonKey = regexp.MustCompile(`^[a-z]+:[a-z_]+$`)

func TestCatalogHas81ColonKeys(t *testing.T) {
	cat := Catalog()
	if len(cat) != 81 {
		t.Fatalf("catalog has %d keys, want 81 (77 + 4 platform, R73)", len(cat))
	}
	if len(cat) > capWords*64 {
		t.Fatalf("CapSet holds %d keys, the catalog has %d: raise capWords", capWords*64, len(cat))
	}
	seen := map[Cap]bool{}
	classes := map[Class]int{}
	for i, e := range cat {
		if !colonKey.MatchString(string(e.Key)) {
			t.Errorf("row %d: %q is not colon form module:action", i+1, e.Key)
		}
		if seen[e.Key] {
			t.Errorf("row %d: %q twice", i+1, e.Key)
		}
		seen[e.Key] = true
		classes[e.Class]++
		if module, _, _ := strings.Cut(string(e.Key), ":"); e.Module != module {
			t.Errorf("%s: module %q, want %q", e.Key, e.Module, module)
		}
		if e.TitleEn == "" || !strings.ContainsFunc(e.TitleTh, func(r rune) bool { return unicode.Is(unicode.Thai, r) }) {
			t.Errorf("%s: titles en %q th %q (both required, th in Thai)", e.Key, e.TitleEn, e.TitleTh)
		}
		if got, ok := Lookup(e.Key); !ok || got != e || !Known(e.Key) || ClassOf(e.Key) != e.Class {
			t.Errorf("%s: Lookup/Known/ClassOf disagree with the catalog", e.Key)
		}
	}
	want := map[Class]int{ClassTenant: 57, ClassGlobal: 7, ClassSelf: 12, ClassScope: 1, ClassPlatform: 4}
	for c, n := range want {
		if classes[c] != n {
			t.Errorf("class %s: %d keys, want %d", c, classes[c], n)
		}
	}
	if platform := classes[ClassPlatform]; len(cat)-platform != 77 {
		t.Errorf("%d non-platform keys, want 77", len(cat)-platform)
	}
	if !Known(MobileCreateHub) {
		t.Error("mobile:create_hub (R5) is missing")
	}
	globals := []Cap{FleetManageCustomers, FleetManageSubcontractors, OperationsManageSources, OperationsCalcDistances,
		SecurityViewStatus, SecurityManageMobileRel, WaitlistView}
	if got := GlobalCaps(); !sameKeys(got, globals) {
		t.Errorf("global keys %v, want the seven steward keys of R60 %v", got, globals)
	}
	if Known("security:manage_users") || Known("fleet_view_trucks") {
		t.Error("legacy keys must not be catalog keys")
	}
}

func sameKeys(a, b []Cap) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// appendixC reads the specification next to the module (the catalog, its classes and the default
// holders must not drift from Appendix C §C.2.3 / §C.2.4).
func appendixC(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "..", "shared-docs", "specs", "mv-go", "C-auth-rbac.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found above the test directory")
		}
	}
}

// section is the text from the heading that starts with from to the next "### " heading.
func section(t *testing.T, doc, from string) string {
	t.Helper()
	i := strings.Index(doc, "\n"+from)
	if i < 0 {
		t.Fatalf("Appendix C has no %q", from)
	}
	rest := doc[i+1:]
	if j := strings.Index(rest[len(from):], "\n### "); j >= 0 {
		rest = rest[:len(from)+j]
	}
	return rest
}

func cells(line string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// holderSets are the default sets behind the holder abbreviations of §C.2.3.
func holderSets() map[string]CapSet {
	return map[string]CapSet{
		"TA": TenantDefaults(TenantAdmin), "MG": TenantDefaults(Manager), "OS": TenantDefaults(OperationStaff),
		"OP": TenantDefaults(Operator), "US": TenantDefaults(User), "DR": TenantDefaults(Driver),
		"CU": ScopeDefaults(ScopeCustomer), "DS": ScopeDefaults(ScopeDispatcher),
		"PA": PlatformDefaults(PlatformAdmin), "SU": PlatformDefaults(Support),
	}
}

// TestCatalogMatchesAppendixC compares the catalog with §C.2.3 row by row: order, key, class and the
// default holders.
func TestCatalogMatchesAppendixC(t *testing.T) {
	cat := Catalog()
	sets := holderSets()
	n := 0
	for line := range strings.Lines(section(t, appendixC(t), "### C.2.3 ")) {
		c := cells(line)
		if len(c) != 6 {
			continue
		}
		row, err := strconv.Atoi(c[0])
		if err != nil {
			continue
		}
		n++
		if row != n || row > len(cat) {
			t.Fatalf("§C.2.3 row %d out of order (want %d)", row, n)
		}
		e := cat[row-1]
		if key := Cap(strings.Trim(c[1], "`")); key != e.Key {
			t.Errorf("row %d: spec %s, catalog %s", row, key, e.Key)
			continue
		}
		if Class(c[2]) != e.Class {
			t.Errorf("%s: spec class %s, catalog %s", e.Key, c[2], e.Class)
		}
		holders := strings.Fields(c[4])
		for abbr, set := range sets {
			if want := slices.Contains(holders, abbr); set.Has(e.Key) != want {
				t.Errorf("%s: holder %s in spec %v, in Go %v", e.Key, abbr, want, set.Has(e.Key))
			}
		}
	}
	if n != len(cat) {
		t.Errorf("§C.2.3 lists %d keys, the catalog %d", n, len(cat))
	}
}

// TestRoleDefaultsMatchAppendixC reads the "Keys (C.2.3 rows)" and "Count" columns of §C.2.4.
func TestRoleDefaultsMatchAppendixC(t *testing.T) {
	names := map[string]CapSet{
		"`tenant_admin`": TenantDefaults(TenantAdmin), "`manager`": TenantDefaults(Manager),
		"`operation_staff`": TenantDefaults(OperationStaff), "`operator`": TenantDefaults(Operator),
		"`user`": TenantDefaults(User), "`driver`": TenantDefaults(Driver),
		"customer scope": ScopeDefaults(ScopeCustomer), "dispatcher scope": ScopeDefaults(ScopeDispatcher),
		"`platform_admin`": PlatformDefaults(PlatformAdmin), "`support`": PlatformDefaults(Support),
	}
	leadingInt := regexp.MustCompile(`^\d+`)
	seen := 0
	for line := range strings.Lines(section(t, appendixC(t), "### C.2.4 ")) {
		c := cells(line)
		if len(c) != 4 {
			continue
		}
		set, ok := names[c[0]]
		if !ok {
			continue
		}
		seen++
		var want CapSet
		for part := range strings.SplitSeq(c[1], ",") {
			lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
			a, err1 := strconv.Atoi(lo)
			b := a
			var err2 error
			if isRange {
				b, err2 = strconv.Atoi(hi)
			}
			if err1 != nil || err2 != nil || a < 1 || b > len(catalog) {
				t.Fatalf("%s: unreadable rows %q", c[0], part)
			}
			for r := a; r <= b; r++ {
				want.Add(catalog[r-1].Key)
			}
		}
		if want != set {
			t.Errorf("%s: spec rows %v, Go %v", c[0], want.Keys(), set.Keys())
		}
		count, _ := strconv.Atoi(leadingInt.FindString(c[2]))
		if count != set.Len() {
			t.Errorf("%s: spec count %d, Go %d", c[0], count, set.Len())
		}
	}
	if seen != len(names) {
		t.Errorf("§C.2.4 has %d of the %d role rows", seen, len(names))
	}
}

func TestDefaultMatrix(t *testing.T) {
	m := DefaultMatrix()
	if len(m) != 10 {
		t.Fatalf("%d rows, want 6 tenant roles + 2 scopes + 2 platform roles", len(m))
	}
	for _, r := range m {
		if len(r.Capabilities) == 0 {
			t.Errorf("%s: empty default set", r.Role)
		}
	}
	if m[0].Role != "tenant_admin" || len(m[0].Capabilities) != 64 {
		t.Errorf("first row %s with %d keys, want tenant_admin with 64", m[0].Role, len(m[0].Capabilities))
	}
}

func TestCapSet(t *testing.T) {
	var s CapSet
	if s.Len() != 0 || s.Has(FleetViewTrucks) {
		t.Fatal("zero CapSet is not empty")
	}
	if s.Add("fleet:unknown") || s.Len() != 0 {
		t.Fatal("an unknown key joined the set")
	}
	s.Add(PlatformCrossTenantWrite) // the last bit
	s.Add(FleetViewTrucks)          // the first bit
	if got := s.Keys(); !slices.Equal(got, []Cap{FleetViewTrucks, PlatformCrossTenantWrite}) {
		t.Fatalf("Keys %v", got)
	}
	b, err := s.MarshalJSON()
	if err != nil || string(b) != `["fleet:view_trucks","platform:cross_tenant_write"]` {
		t.Fatalf("MarshalJSON %s %v", b, err)
	}
	var back CapSet
	if err := back.UnmarshalJSON([]byte(`["fleet:view_trucks","gone:key","platform:cross_tenant_write"]`)); err != nil || back != s {
		t.Fatalf("UnmarshalJSON %v %v", back.Keys(), err)
	}
	s.Remove(FleetViewTrucks)
	if s.Has(FleetViewTrucks) || s.Len() != 1 {
		t.Fatal("Remove")
	}
	if u := NewCapSet(ChatView).Union(NewCapSet(ChatSend)).Minus(NewCapSet(ChatView)); u != NewCapSet(ChatSend) {
		t.Fatalf("Union/Minus %v", u.Keys())
	}
}
