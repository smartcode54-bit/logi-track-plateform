//go:build integration

// RLS catalog test (Appendix C §C.3.8, R67): after `goose up` the database must match the coverage
// table of Appendix C §C.3.0 and the grants of §C.3.2, both read from the specification itself, so
// a schema change without the matching document change (or the reverse) fails here.
package db_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// coverage is one table of the §C.3.0 coverage table.
type coverage struct {
	rls      bool
	policies []string // expanded generator policies plus the literal ones
	freeze   bool     // G-tenant: trigger t_freeze_tenant_id
}

// The generators of Appendix C §C.3.5 and the policies they create.
var generators = map[string][]string{
	"G-tenant": {"p_bypass", "p_not_quarantine", "p_tenant_staff"},
	"G-child":  {"p_bypass", "p_parent_read", "p_parent_write"},
	"G-global": {"p_bypass", "p_read", "p_steward_write"},
	"G-base":   {"p_bypass"},
}

// helpers are the functions a policy may call (Appendix C §C.3.3, §C.3.5, §C.3.6).
var helpers = []string{
	"app_user_id", "app_tenant_id", "app_role", "app_driver_id", "app_customer_ids", "app_is_dispatcher", "app_bypass",
	"app_is_staff", "app_subtenant_ids", "app_is_steward", "app_tenant_move_allowed", "app_etl_load", "app_is_scope",
	"app_is_authenticated", "app_tenant_in_reach", "app_quarantine_tenant_id", "app_task_stops_in_scope",
	"app_task_in_scope", "app_trip_in_scope", "app_recent_work_in_scope", "app_status_entity_visible",
	"app_driver_truck_ids",
}

func appendixC(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found above the test directory")
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "..", "shared-docs", "specs", "mv-go", "C-auth-rbac.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text between a heading line starting with from and the next "### " heading.
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

var backticked = regexp.MustCompile("`([a-z_][a-z0-9_.]*)`")

// parseCoverage reads the §C.3.0 table: | Table | RLS | Family | Policies | Notes |.
func parseCoverage(t *testing.T, doc string) map[string]coverage {
	t.Helper()
	out := map[string]coverage{}
	for line := range strings.Lines(section(t, doc, "### C.3.0 ")) {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 5 || !strings.HasPrefix(strings.TrimSpace(cells[0]), "`") {
			continue
		}
		var cv coverage
		switch strings.TrimSpace(cells[1]) {
		case "yes":
			cv.rls = true
		case "no":
		default:
			t.Fatalf("§C.3.0 row %q: RLS column is neither yes nor no", line)
		}
		for part := range strings.SplitSeq(cells[3], ",") {
			part = strings.TrimSpace(part)
			switch {
			case generators[part] != nil:
				cv.policies = append(cv.policies, generators[part]...)
				cv.freeze = cv.freeze || part == "G-tenant"
			case strings.HasPrefix(part, "`p_"):
				cv.policies = append(cv.policies, strings.Trim(part, "`"))
			case part == "—":
			default:
				t.Fatalf("§C.3.0 row %q: unknown policy entry %q", line, part)
			}
		}
		for _, m := range backticked.FindAllStringSubmatch(cells[0], -1) {
			out[m[1]] = cv
		}
	}
	if len(out) != 85 {
		t.Fatalf("§C.3.0 lists %d tables, want 85 (81 public + 4 etl)", len(out))
	}
	return out
}

// parseExemptGrants reads "on the exempt tables exactly: `t` SELECT/INSERT, ...; nothing" of §C.3.2.
func parseExemptGrants(t *testing.T, doc string) map[string][]string {
	t.Helper()
	s := section(t, doc, "### C.3.2 ")
	i := strings.Index(s, "on the exempt tables exactly:")
	j := strings.Index(s[i:], "; nothing")
	if i < 0 || j < 0 {
		t.Fatal("§C.3.2 lost the sentence listing the exempt-table privileges")
	}
	out := map[string][]string{}
	rx := regexp.MustCompile("`([a-z_]+)` ((?:SELECT|INSERT|UPDATE|DELETE)(?:/(?:SELECT|INSERT|UPDATE|DELETE))*)")
	for _, m := range rx.FindAllStringSubmatch(s[i:i+j], -1) {
		out[m[1]] = strings.Split(m[2], "/")
	}
	if len(out) != 11 {
		t.Fatalf("§C.3.2 lists privileges for %d exempt tables, want 11", len(out))
	}
	return out
}

// tablePrivileges are the table privileges of PostgreSQL 18, as a SQL array literal for has_table_privilege.
const tablePrivileges = `ARRAY['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER', 'MAINTAIN']`

func rows(t *testing.T, c *pgx.Conn, sql string, args ...any) []string {
	t.Helper()
	r, err := c.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(r, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	sort.Strings(s)
	return slices.Compact(s)
}

func TestRLSCatalogMatchesAppendixC(t *testing.T) {
	doc := appendixC(t)
	want := parseCoverage(t, doc)
	exempt := parseExemptGrants(t, doc)

	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, d.URL(db.RoleMigrator))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()

	// Every table of public and etl (goose's own table excluded by name), its RLS flags, policies and
	// the frozen-tenant trigger.
	type table struct {
		name           string
		rls, force     bool
		policies       []string
		restrictive    []string
		freezeTriggers int
	}
	r, err := c.Query(ctx, `SELECT CASE n.nspname WHEN 'public' THEN '' ELSE n.nspname || '.' END || c.relname,
			c.relrowsecurity, c.relforcerowsecurity,
			coalesce(array_agg(p.polname::text ORDER BY p.polname) FILTER (WHERE p.polname IS NOT NULL), '{}'),
			coalesce(array_agg(p.polname::text ORDER BY p.polname) FILTER (WHERE NOT p.polpermissive), '{}'),
			(SELECT count(*) FROM pg_trigger g WHERE g.tgrelid = c.oid AND g.tgname = 't_freeze_tenant_id')::int
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_policy p ON p.polrelid = c.oid
		WHERE n.nspname IN ('public', 'etl') AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
		GROUP BY 1, 2, 3, c.oid`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(r, func(row pgx.CollectableRow) (table, error) {
		var x table
		err := row.Scan(&x.name, &x.rls, &x.force, &x.policies, &x.restrictive, &x.freezeTriggers)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	nRLS := 0
	for _, g := range got {
		seen[g.name] = true
		w, ok := want[g.name]
		if !ok {
			t.Errorf("%s exists but §C.3.0 does not list it", g.name)
			continue
		}
		if g.rls != w.rls || g.force != w.rls {
			t.Errorf("%s: ENABLE=%t FORCE=%t, §C.3.0 says RLS %t", g.name, g.rls, g.force, w.rls)
		}
		if g.rls && g.force {
			nRLS++
		}
		if !slices.Equal(sorted(g.policies), sorted(w.policies)) {
			t.Errorf("%s: policies %v, §C.3.0 says %v", g.name, g.policies, sorted(w.policies))
		}
		wantRestrictive := []string{}
		if slices.Contains(w.policies, "p_not_quarantine") {
			wantRestrictive = []string{"p_not_quarantine"}
		}
		if !slices.Equal(g.restrictive, wantRestrictive) {
			t.Errorf("%s: restrictive policies %v, want %v", g.name, g.restrictive, wantRestrictive)
		}
		if (g.freezeTriggers == 1) != w.freeze || g.freezeTriggers > 1 {
			t.Errorf("%s: %d t_freeze_tenant_id triggers, G-tenant=%t", g.name, g.freezeTriggers, w.freeze)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("§C.3.0 lists %s, the database has no such table", name)
		}
	}
	if nRLS != 70 {
		t.Errorf("%d tables with ENABLE + FORCE ROW LEVEL SECURITY, want 70", nRLS)
	}

	// logitrack_app: DML on the RLS tables minus the append-only / void-only REVOKEs of Appendix A
	// §A.1.8, nothing on the counters, exactly §C.3.2 on the exempt tables, nothing in schema etl.
	// Effective privileges (has_table_privilege: direct, through PUBLIC or inherited from a role the
	// login is a member of), keyed like want, so a grant to PUBLIC or on an etl table cannot hide.
	appendOnly := []string{"status_history", "trip_no_history", "billing_statement_lines", "transactions", "security_events"}
	noDelete := []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "standby_rate_entries"}
	privs := map[string][]string{}
	for _, row := range rows(t, c, `SELECT CASE n.nspname WHEN 'public' THEN '' ELSE n.nspname || '.' END || c.relname || ' ' ||
			coalesce(string_agg(p.priv, ' ' ORDER BY p.priv) FILTER (WHERE has_table_privilege('logitrack_app', c.oid, p.priv)), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN unnest(`+tablePrivileges+`) AS p(priv)
		WHERE n.nspname IN ('public', 'etl') AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
		GROUP BY n.nspname, c.relname`) {
		name, list, _ := strings.Cut(row, " ")
		privs[name] = strings.Fields(list)
	}
	// Nothing in public or etl is granted to PUBLIC (aclexplode grantee 0): every login's access is explicit.
	if public := rows(t, c, `SELECT DISTINCT n.nspname || '.' || c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace CROSS JOIN LATERAL aclexplode(c.relacl) a
		WHERE n.nspname IN ('public', 'etl') AND a.grantee = 0 ORDER BY 1`); len(public) > 0 {
		t.Errorf("relations granted to PUBLIC: %v", public)
	}
	for name, w := range want {
		var expect []string
		switch {
		case name == "task_number_counters" || name == "billing_counters" || strings.HasPrefix(name, "etl."):
		case !w.rls:
			expect = exempt[name]
			if expect == nil {
				t.Errorf("exempt table %s has no privilege entry in §C.3.2", name)
			}
		case slices.Contains(appendOnly, name):
			expect = []string{"INSERT", "SELECT"}
		case slices.Contains(noDelete, name):
			expect = []string{"INSERT", "SELECT", "UPDATE"}
		default:
			expect = []string{"DELETE", "INSERT", "SELECT", "UPDATE"}
		}
		if !slices.Equal(sorted(privs[name]), sorted(expect)) {
			t.Errorf("logitrack_app on %s: %v, want %v", name, privs[name], sorted(expect))
		}
	}
	if extra := rows(t, c, `SELECT nspname FROM pg_namespace WHERE nspname = 'etl'
		AND has_schema_privilege('logitrack_app', oid, 'USAGE')`); len(extra) > 0 {
		t.Error("logitrack_app has USAGE on schema etl")
	}
	// Projections for dispatcher and customer-scope principals (§C.3.7): security_invoker views, SELECT only.
	views := rows(t, c, `SELECT format('%s invoker=%s app=%s', c.relname, coalesce('security_invoker=true' = ANY (c.reloptions), false),
			(SELECT string_agg(p.priv, ',' ORDER BY p.priv) FROM unnest(`+tablePrivileges+`) AS p(priv)
			  WHERE has_table_privilege('logitrack_app', c.oid, p.priv)))
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'v' ORDER BY c.relname`)
	wantViews := []string{}
	for _, v := range []string{"scope_drivers", "scope_incidents", "scope_standby", "scope_tasks", "scope_tenants", "scope_trips", "scope_trucks"} {
		wantViews = append(wantViews, v+" invoker=t app=SELECT")
	}
	if !slices.Equal(views, wantViews) {
		t.Errorf("views = %v\nwant %v", views, wantViews)
	}

	// Policies call only the helper functions (§C.3.3, §C.3.5).
	called := rows(t, c, `SELECT DISTINCT p.proname::text FROM pg_depend dep
		JOIN pg_proc p ON p.oid = dep.refobjid
		WHERE dep.classid = 'pg_policy'::regclass AND dep.refclassid = 'pg_proc'::regclass`)
	for _, f := range called {
		if !slices.Contains(helpers, f) {
			t.Errorf("a policy calls %s, which is not a §C.3.3 helper", f)
		}
	}
	if len(called) < 15 {
		t.Errorf("policies call only %v; the dependency query found too little", called)
	}
}
