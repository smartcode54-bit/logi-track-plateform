package seed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Verifier runs the twelve invariants of Appendix D §D.3 and compares the table counts with the profile's
// manifest. Every SQL check reads as logitrack_etl (ETL_DATABASE_URL) in a read-only transaction; the
// tenant-isolation role-play (#10c, #10d and the per-carrier check) runs as logitrack_app through
// DATABASE_URL with the request context the API sets: the principal is built by auth.RolePlayPrincipal from
// the user's memberships and scopes, resolved by iam.RBAC and opened with db.WithPrincipal (R87). Nothing
// uses SET ROLE or writes a GUC by hand.
type Verifier struct {
	ETL      *pgxpool.Pool
	App      *pgxpool.Pool
	Redis    redis.UniversalClient
	Keys     cache.Keyspace
	Backends Backends
	Plan     *Plan
	Log      zerolog.Logger
}

// Check is the outcome of one invariant.
type Check struct {
	N          int
	Name       string
	Detail     string   // what was looked at
	Violations []string // empty = pass
}

// CountDiff is one table whose row count differs from the manifest.
type CountDiff struct {
	Table            string
	Expected, Actual int
}

// Fingerprint is one table's row count and the md5 of its key columns (invariant 12).
type Fingerprint struct {
	Table string
	Rows  int
	MD5   string
}

// Report is the outcome of --verify.
type Report struct {
	Profile     Profile
	Checks      []Check
	Counts      []CountDiff
	Tables      int
	Fingerprint []Fingerprint
}

// OK reports a pass: no violation and no count difference.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if len(c.Violations) > 0 {
			return false
		}
	}
	return len(r.Counts) == 0
}

// maxShown bounds the violations printed per invariant.
const maxShown = 20

// Write prints one line per invariant, the count diff and, last, the fingerprint block.
func (r Report) Write(w io.Writer) {
	_, _ = fmt.Fprintf(w, "seed verify: profile %s\n", r.Profile)
	for _, c := range r.Checks {
		status := "PASS"
		if len(c.Violations) > 0 {
			status = "FAIL"
		}
		line := fmt.Sprintf("%2d %s %s", c.N, status, c.Name)
		if c.Detail != "" {
			line += " (" + c.Detail + ")"
		}
		_, _ = fmt.Fprintln(w, line)
		for i, v := range c.Violations {
			if i == maxShown {
				_, _ = fmt.Fprintf(w, "     ... %d more\n", len(c.Violations)-maxShown)
				break
			}
			_, _ = fmt.Fprintf(w, "     - %s\n", v)
		}
	}
	if len(r.Counts) == 0 {
		_, _ = fmt.Fprintf(w, "counts: %d tables match the %s manifest\n", r.Tables, r.Profile)
	} else {
		_, _ = fmt.Fprintf(w, "counts: %d of %d tables differ from the %s manifest\n", len(r.Counts), r.Tables, r.Profile)
		for _, d := range r.Counts {
			_, _ = fmt.Fprintf(w, "     - %s: expected %d, found %d\n", d.Table, d.Expected, d.Actual)
		}
	}
	_, _ = fmt.Fprintln(w, "fingerprint:")
	for _, f := range r.Fingerprint {
		_, _ = fmt.Fprintf(w, "  %-32s %8d %s\n", f.Table, f.Rows, f.MD5)
	}
}

// Run executes every check. An error means a dependency could not be reached or a check could not run
// (exit 2); violations are in the report (exit 1).
func (v *Verifier) Run(ctx context.Context) (Report, error) {
	rep := Report{Profile: v.Plan.Profile}
	if err := v.Redis.Ping(ctx).Err(); err != nil {
		return rep, fmt.Errorf("seed: verify: redis: %w", err)
	}
	err := db.WithSystem(ctx, v.ETL, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		for _, c := range sqlChecks {
			var out []string
			for _, q := range c.queries {
				rows, err := violations(ctx, tx, q)
				if err != nil {
					return fmt.Errorf("invariant %d: %w", c.n, err)
				}
				out = append(out, rows...)
			}
			chk := Check{N: c.n, Name: c.name, Violations: out}
			switch c.n {
			case 2:
				mism, err := CheckEngine(ctx, tx, nil, nil)
				if err != nil {
					return fmt.Errorf("invariant 2 (engine): %w", err)
				}
				for _, m := range mism {
					chk.Violations = append(chk.Violations, "engine: "+m.String())
				}
				chk.Detail = "SQL 2a, 2b; engine recomputes every priced row"
			case 8:
				hv, err := v.hubMaps(ctx, tx)
				if err != nil {
					return fmt.Errorf("invariant 8 (Redis hub maps): %w", err)
				}
				chk.Violations = append(chk.Violations, hv...)
				chk.Detail = "SQL; Redis cache:hubs:n2c and cache:hubs:c2n warmed and compared"
			case 9:
				ov, n, err := checkObjects(ctx, tx, v.Backends)
				if err != nil {
					return fmt.Errorf("invariant 9 (objects): %w", err)
				}
				chk.Violations = append(chk.Violations, ov...)
				chk.Detail = fmt.Sprintf("%d file_objects rows stat-ed and hashed", n)
			case 12:
				nv, err := v.nonV5(ctx, tx)
				if err != nil {
					return fmt.Errorf("invariant 12: %w", err)
				}
				chk.Violations = append(chk.Violations, nv...)
				chk.Detail = "every seeded uuid is version 5; fingerprint below"
			}
			rep.Checks = append(rep.Checks, chk)
		}
		var err error
		if rep.Counts, rep.Tables, err = v.counts(ctx, tx); err != nil {
			return fmt.Errorf("counts: %w", err)
		}
		if rep.Fingerprint, err = fingerprint(ctx, tx, append(append([]string(nil), LoadOrder...), DerivedTable)); err != nil {
			return fmt.Errorf("fingerprint: %w", err)
		}
		return nil
	})
	if err != nil {
		return rep, fmt.Errorf("seed: verify: %w", err)
	}
	iso, detail, err := v.isolation(ctx)
	if err != nil {
		return rep, fmt.Errorf("seed: verify: invariant 10 role-play: %w", err)
	}
	for i := range rep.Checks {
		if rep.Checks[i].N == 10 {
			rep.Checks[i].Violations = append(rep.Checks[i].Violations, iso...)
			rep.Checks[i].Detail = detail
		}
	}
	return rep, nil
}

// violations runs a query whose rows are violations and renders each row's columns.
func violations(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	var out []string
	err = scanAll(rows, func(r pgx.Rows) error {
		vals, err := r.Values()
		if err != nil {
			return err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
			if v == nil {
				parts[i] = "NULL"
			}
		}
		out = append(out, strings.Join(parts, " | "))
		return nil
	})
	return out, err
}

// counts compares every manifest table with the database.
func (v *Verifier) counts(ctx context.Context, tx pgx.Tx) ([]CountDiff, int, error) {
	tables := make([]string, 0, len(v.Plan.Expected))
	for t := range v.Plan.Expected {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	var out []CountDiff
	for _, t := range tables {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return nil, 0, err
		}
		if n != v.Plan.Expected[t] {
			out = append(out, CountDiff{Table: t, Expected: v.Plan.Expected[t], Actual: n})
		}
	}
	return out, len(tables), nil
}

// fingerprintExtra are columns a table's fingerprint covers besides its primary key (§D.3 #12).
var fingerprintExtra = map[string][]string{
	"trip_records":           {"trip_no"},
	"trip_billing_snapshots": {"estimate_thb"},
	"file_objects":           {"object_key", "sha256"},
	"outbox_events":          {"event_id"},
	"users":                  {"email"},
	"tasks":                  {"task_no"},
}

// fingerprint is the row count and the md5 of the ordered key columns of every table. updated_at and the
// password hashes are left out on purpose: a back-fill stamps the load time and Argon2id salts are random.
func fingerprint(ctx context.Context, tx pgx.Tx, tables []string) ([]Fingerprint, error) {
	out := make([]Fingerprint, 0, len(tables))
	for _, t := range tables {
		pk, err := primaryKey(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		cols := append(pk, fingerprintExtra[t]...)
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = "coalesce(" + pgx.Identifier{c}.Sanitize() + "::text, '-')"
		}
		expr := "concat_ws('|', " + strings.Join(parts, ", ") + ")"
		f := Fingerprint{Table: t}
		q := fmt.Sprintf("SELECT count(*), md5(coalesce(string_agg(%s, ',' ORDER BY %s), '')) FROM %s", expr, expr, pgx.Identifier{t}.Sanitize())
		if err := tx.QueryRow(ctx, q).Scan(&f.Rows, &f.MD5); err != nil {
			return nil, fmt.Errorf("%s: %w", t, err)
		}
		out = append(out, f)
	}
	return out, nil
}

// primaryKey lists the primary-key columns of a table in key order.
func primaryKey(ctx context.Context, tx pgx.Tx, table string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT a.attname::text FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = format('public.%I', $1::text)::regclass AND i.indisprimary
		ORDER BY array_position(i.indkey, a.attnum)`, table)
	if err != nil {
		return nil, err
	}
	var out []string
	err = scanAll(rows, func(r pgx.Rows) error {
		var c string
		if err := r.Scan(&c); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	if err == nil && len(out) == 0 {
		err = fmt.Errorf("%s has no primary key", table)
	}
	return out, err
}

// nonV5 lists ids that are not uuid v5 in every seeded table keyed by a uuid id (§D.3 #12): rows created by
// the running application (uuidv7) show up here. The quarantine tenant has the fixed id of migration 0002;
// the own-fleet tenant may carry OWN_FLEET_TENANT_ID, which is compared with that value instead.
func (v *Verifier) nonV5(ctx context.Context, tx pgx.Tx) ([]string, error) {
	var out []string
	for _, t := range LoadOrder {
		pk, err := primaryKey(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if len(pk) != 1 || pk[0] != "id" {
			continue
		}
		var typ string
		if err := tx.QueryRow(ctx, `SELECT format_type(atttypid, atttypmod) FROM pg_attribute
			WHERE attrelid = format('public.%I', $1::text)::regclass AND attname = 'id'`, t).Scan(&typ); err != nil {
			return nil, err
		}
		if typ != "uuid" {
			continue
		}
		q := "SELECT id::text FROM " + pgx.Identifier{t}.Sanitize() + " WHERE uuid_extract_version(id) <> 5"
		if t == "tenants" {
			q += " AND kind <> 'quarantine' AND kind <> 'own_fleet'"
		}
		rows, err := violations(ctx, tx, q+" ORDER BY id")
		if err != nil {
			return nil, err
		}
		for _, id := range rows {
			out = append(out, t+" "+id+": not a uuid v5")
		}
	}
	var own []string
	rows, err := tx.Query(ctx, `SELECT id::text FROM tenants WHERE kind = 'own_fleet'`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		own = append(own, id)
		return nil
	}); err != nil {
		return nil, err
	}
	want := v.Plan.Symbols.MustID(ownFleetSymbol).String()
	for _, id := range own {
		if id != want {
			out = append(out, fmt.Sprintf("tenants %s: the own-fleet id is not the seeded one (%s)", id, want))
		}
	}
	return out, nil
}

// hubMaps is the Go half of invariant 8: warm the two Redis hub maps through the API's read-through cache
// from PostgreSQL, then check that both hashes exist as separate keys and that no name of n2c is a code.
func (v *Verifier) hubMaps(ctx context.Context, tx pgx.Tx) ([]string, error) {
	c := cache.New(v.Redis, v.Keys)
	if _, err := c.HubMaps(ctx, func(ctx context.Context) (cache.HubMaps, error) { return loadHubMaps(ctx, tx) }); err != nil {
		return nil, err
	}
	n2c, c2n := v.Keys.HubsNameToCode(), v.Keys.HubsCodeToName()
	var out []string
	if n2c == c2n {
		out = append(out, "cache:hubs:n2c and cache:hubs:c2n are one key")
	}
	n, err := v.Redis.Exists(ctx, n2c, c2n).Result()
	if err != nil {
		return nil, err
	}
	if n != 2 {
		out = append(out, fmt.Sprintf("after warming only %d of the two hub-map hashes exist", n))
	}
	fields, err := v.Redis.HKeys(ctx, n2c).Result()
	if err != nil {
		return nil, err
	}
	codes, err := hubCodes(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f != "" && codes[strings.ToUpper(f)] {
			out = append(out, "cache:hubs:n2c maps the hub code "+f+" (directions merged)")
		}
	}
	return out, nil
}

func hubCodes(ctx context.Context, tx pgx.Tx) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT upper(source_id::text) FROM hubs`)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	err = scanAll(rows, func(r pgx.Rows) error {
		var c string
		if err := r.Scan(&c); err != nil {
			return err
		}
		out[c] = true
		return nil
	})
	return out, err
}

// loadHubMaps builds the two display maps from PostgreSQL as compute.NewHubMaps does (hubs in load order,
// trimmed names, a name equal to its code skipped, the first hub to claim a name wins; code -> Thai name).
func loadHubMaps(ctx context.Context, tx pgx.Tx) (cache.HubMaps, error) {
	m := cache.HubMaps{NameToCode: map[string]string{}, CodeToName: map[string]string{}}
	rows, err := tx.Query(ctx, `SELECT h.source_id::text, h.name_th, coalesce(h.name_en, ''),
		coalesce(array_agg(a.alias::text ORDER BY a.created_at, a.alias) FILTER (WHERE a.alias IS NOT NULL), '{}')
		FROM hubs h LEFT JOIN hub_name_aliases a ON a.hub_id = h.id GROUP BY h.id ORDER BY h.created_at, h.id`)
	if err != nil {
		return m, err
	}
	err = scanAll(rows, func(r pgx.Rows) error {
		var code, th, en string
		var aliases []string
		if err := r.Scan(&code, &th, &en, &aliases); err != nil {
			return err
		}
		code = strings.TrimSpace(code)
		if code == "" {
			return nil
		}
		for _, n := range append([]string{th, en}, aliases...) {
			n = strings.TrimSpace(n)
			if n == "" || n == code {
				continue
			}
			if _, taken := m.NameToCode[n]; !taken {
				m.NameToCode[n] = code
			}
		}
		if th = strings.TrimSpace(th); th != "" {
			if _, taken := m.CodeToName[code]; !taken {
				m.CodeToName[code] = th
			}
		}
		return nil
	})
	return m, err
}

// isolation is the role-play part of invariant 10, on DATABASE_URL as logitrack_app.
func (v *Verifier) isolation(ctx context.Context) ([]string, string, error) {
	rbac, err := iam.NewRBAC(iam.Deps{Pool: v.App, Log: v.Log})
	if err != nil {
		return nil, "", err
	}
	s := v.Plan.Symbols
	var out []string
	add := func(who string, vs []string) {
		for _, x := range vs {
			out = append(out, who+": "+x)
		}
	}
	ttp, nwr, own := s.MustID("TN_TTP"), s.MustID("TN_NWR"), s.MustID("TN_OWN")
	// 10c: the dispatcher, a carrier tenant_admin and a customer-scope user read no cost, HR or rate rows.
	for _, pc := range []struct {
		user, label string
		tenant      *uuid.UUID
	}{
		{"U_TTP_DISP", "10c dispatcher ttp.dispatch", &ttp},
		{"U_NWR_ADMIN", "10c carrier tenant_admin nwr.admin", &nwr},
		{"U_CJSF_CUST", "10c customer cjsf.viewer", nil},
	} {
		vs, err := v.rolePlay(ctx, rbac, s.MustID(pc.user), pc.tenant, func(tx pgx.Tx) ([]string, error) {
			return violations(ctx, tx, sql10c, pc.tenant)
		})
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", pc.label, err)
		}
		add(pc.label, vs)
	}
	// The 10c dispatcher still sees the own-fleet task billed to TTP through scope_tasks.
	vs, err := v.rolePlay(ctx, rbac, s.MustID("U_TTP_DISP"), &ttp, func(tx pgx.Tx) ([]string, error) {
		return violations(ctx, tx, `SELECT 'scope_tasks must show FM-12082026-001 to the dispatcher'
			WHERE NOT EXISTS (SELECT 1 FROM scope_tasks WHERE id = $1)`, s.MustID("TK08"))
	})
	if err != nil {
		return nil, "", fmt.Errorf("10c dispatcher scope: %w", err)
	}
	add("10c dispatcher ttp.dispatch", vs)
	// 10d: own-fleet staff reach the contractor NWR (R60) and nothing of TTP.
	vs, err = v.rolePlay(ctx, rbac, s.MustID("U_WRT_OPS"), &own, func(tx pgx.Tx) ([]string, error) {
		return violations(ctx, tx, sql10d, ttp)
	})
	if err != nil {
		return nil, "", fmt.Errorf("10d own-fleet staff: %w", err)
	}
	add("10d own-fleet wrt.ops", vs)
	// Every carrier principal sees only its own tenant (owner addition to issue #36).
	carriers, err := v.carrierPrincipals(ctx)
	if err != nil {
		return nil, "", err
	}
	tables, err := v.tenantTables(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, cp := range carriers {
		tid := cp.tenant
		vs, err := v.rolePlay(ctx, rbac, cp.user, &tid, func(tx pgx.Tx) ([]string, error) {
			return carrierLeaks(ctx, tx, tables, tid)
		})
		if err != nil {
			return nil, "", fmt.Errorf("carrier %s: %w", cp.label, err)
		}
		add("carrier "+cp.label, vs)
	}
	detail := fmt.Sprintf("SQL 10a, 10b; role-play on DATABASE_URL: 10c x3, 10d, %d carrier principal(s) over %d tenant tables",
		len(carriers), len(tables))
	return out, detail, nil
}

// rolePlay opens a read-only request transaction for user acting in tenant, with the principal the API
// would build (auth.RolePlayPrincipal, iam.RBAC.Resolve, db.WithPrincipal).
func (v *Verifier) rolePlay(ctx context.Context, rbac *iam.RBAC, user uuid.UUID, tenant *uuid.UUID,
	fn func(tx pgx.Tx) ([]string, error)) ([]string, error) {
	p, err := auth.RolePlayPrincipal(ctx, v.App, user, tenant)
	if err != nil {
		return nil, err
	}
	if err := rbac.Resolve(ctx, p); err != nil {
		return nil, err
	}
	var out []string
	err = db.WithPrincipal(ctx, v.App, p, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		var err error
		out, err = fn(tx)
		return err
	})
	return out, err
}

type carrierPrincipal struct {
	user, tenant uuid.UUID
	label        string
}

// carrierPrincipals are the staff members of carrier tenants without a dispatcher grant (a dispatcher reads
// across tenants by design, C.1.7), one per (tenant, role).
func (v *Verifier) carrierPrincipals(ctx context.Context) ([]carrierPrincipal, error) {
	var out []carrierPrincipal
	err := db.WithSystem(ctx, v.ETL, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT ON (m.tenant_id, m.role) m.user_id, m.tenant_id,
			coalesce(u.email::text, m.user_id::text) || ' (' || coalesce(t.code::text, t.name_th) || ', ' || m.role || ')'
			FROM memberships m JOIN tenants t ON t.id = m.tenant_id JOIN users u ON u.id = m.user_id
			WHERE t.kind = 'carrier' AND m.status = 'active' AND m.role <> 'driver' AND u.status = 'active'
			  AND NOT EXISTS (SELECT 1 FROM user_scopes s WHERE s.user_id = m.user_id AND s.kind = 'dispatcher')
			ORDER BY m.tenant_id, m.role, u.email`)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var c carrierPrincipal
			if err := r.Scan(&c.user, &c.tenant, &c.label); err != nil {
				return err
			}
			out = append(out, c)
			return nil
		})
	})
	if err == nil && len(out) == 0 {
		err = errors.New("no carrier principal to role-play")
	}
	return out, err
}

// tenantTables are the row-level-security tables with a tenant_id stamp that logitrack_app may read at all.
// Left out: billing_parties (its tenant_id is a reference to the party's tenant, not a stamp, C.3.0), the
// tables granted to no app login (billing_counters: SECURITY DEFINER allocators only) and the
// exempt-service-layer tables without RLS (outbox_events, jobs, ...), whose service decides access (C.3.0).
func (v *Verifier) tenantTables(ctx context.Context) ([]string, error) {
	var out []string
	err := db.WithSystem(ctx, v.ETL, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.relname::text FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
			WHERE c.relkind = 'r' AND c.relrowsecurity AND c.relname <> 'billing_parties'
			  AND has_table_privilege('logitrack_app', c.oid, 'SELECT')
			ORDER BY 1`)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var t string
			if err := r.Scan(&t); err != nil {
				return err
			}
			out = append(out, t)
			return nil
		})
	})
	return out, err
}

// carrierLeaks counts, as the carrier principal, the rows of other tenants it can read (platform rows
// with a NULL tenant are shared on purpose), and checks that it reads its own tenant row at all, so an
// empty context cannot pass vacuously.
func carrierLeaks(ctx context.Context, tx pgx.Tx, tables []string, tenant uuid.UUID) ([]string, error) {
	parts := []string{`SELECT 'tenants' AS t, count(*) AS n FROM tenants WHERE id <> $1`}
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT %s, count(*) FROM %s WHERE tenant_id IS NOT NULL AND tenant_id <> $1`,
			quoteLiteral(t), pgx.Identifier{t}.Sanitize()))
	}
	rows, err := tx.Query(ctx, `SELECT t, n FROM (`+strings.Join(parts, " UNION ALL ")+`) x WHERE n > 0 ORDER BY t`, tenant)
	if err != nil {
		return nil, err
	}
	var out []string
	err = scanAll(rows, func(r pgx.Rows) error {
		var t string
		var n int64
		if err := r.Scan(&t, &n); err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("reads %d row(s) of other tenants in %s", n, t))
		return nil
	})
	if err != nil {
		return nil, err
	}
	var own int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = $1`, tenant).Scan(&own); err != nil {
		return nil, err
	}
	if own != 1 {
		out = append(out, "cannot read its own tenant row (the role-play context is empty)")
	}
	return out, nil
}

// quoteLiteral quotes a catalog name as an SQL string literal.
func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
