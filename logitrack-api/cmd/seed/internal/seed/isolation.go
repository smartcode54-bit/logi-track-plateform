package seed

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// The per-carrier part of invariant 10 (Appendix D §D.3 #10e, owner addition to issue #36): every carrier
// principal, staff and drivers, reads no row of another tenant in any row-level-security table logitrack_app
// may SELECT. Every such table is classified, deny by default: a tenant_id stamp, an owner found through a
// parent or a user (ownershipRules), or shared master data (sharedTables). A readable RLS table nothing
// classifies is itself a violation, so a new table cannot slip past the check.

// rlsTable is one RLS table logitrack_app may read, with the columns its ownership is derived from.
type rlsTable struct {
	name                         string
	tenantID, driverID, helperID bool
	hasVisibility, hasUploadedBy bool
}

// ownership says which visible rows of one RLS table are in order for the role-played principal. Both
// predicates are SQL over the row r and the principal's context, written {tenant}, {driver} and {user}
// ({driver} is NULL for staff); a NULL result counts as false:
//
//   - tenant: the row belongs to the principal's tenant, or to nobody in particular (a NULL tenant: platform
//     rows shared on purpose);
//   - self: the row is the principal's own in another tenant: a driver's history (driver self-scope is by
//     driver_id, not by tenant: Appendix C §C.1 "Broker carrier moves", §C.3.5 p_driver_read, R24) or the
//     user's own rows (p_self_read). These are reported as bounded R24 rows, never as a leak.
//
// Any other visible row is a leak.
type ownership struct {
	via          string // where the owner comes from when the row has no tenant_id stamp
	tenant, self string
}

// sharedTables are read by every authenticated principal on purpose (app_rls_global_table, R60): global master
// data without a tenant owner. billing_parties carries a tenant_id, but it references the party's tenant
// instead of stamping an owner (Appendix C §C.3.0).
var sharedTables = []string{"billing_parties", "customer_driver_id_types", "customers", "hub_name_aliases",
	"hub_soc_distances", "hubs"}

// memberOfTenant holds when the user has a membership (any status) in the principal's tenant. It runs under the
// principal's RLS, where staff see their tenant's memberships (p_staff_read) and a driver its own (p_self_read).
func memberOfTenant(user string) string {
	return "EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = " + user + " AND m.tenant_id = {tenant})"
}

// parentScoped is a child without its own tenant_id (app_rls_child_table, and the comms tables' p_parent_read):
// its owner is the parent row, looked up under the principal's RLS. A visible child whose parent is not a
// visible row of the principal's tenant is a leak, including one whose parent the principal cannot see at all.
// parentSelf is the parent's own-row predicate over p (a driver's trip), rowSelf the child's own (a recipient).
func parentScoped(parent, fk, key, parentSelf, rowSelf string) ownership {
	look := func(cond string) string {
		return fmt.Sprintf("EXISTS (SELECT 1 FROM %s p WHERE p.%s = r.%s AND (%s))", parent, key, fk, cond)
	}
	o := ownership{via: parent, tenant: look("p.tenant_id IS NULL OR p.tenant_id = {tenant}")}
	var self []string
	if parentSelf != "" {
		self = append(self, look(parentSelf))
	}
	if rowSelf != "" {
		self = append(self, rowSelf)
	}
	o.self = strings.Join(self, " OR ")
	return o
}

// userKeyed is a table owned by one user (p_self_read / p_self): a row of a user who is not a member of the
// principal's tenant, and not the principal, is a leak.
func userKeyed() ownership {
	return ownership{via: "user_id", tenant: memberOfTenant("r.user_id"), self: "r.user_id = {user}"}
}

// ownDriver is a driver's own parent row.
const ownDriver = "p.driver_id = {driver}"

// ownershipRules classify the RLS tables without a tenant_id stamp (and tenants itself).
var ownershipRules = map[string]ownership{
	"tenants": {via: "id", tenant: "r.id = {tenant}"},
	"users":   {via: "memberships", tenant: memberOfTenant("r.id"), self: "r.id = {user}"},

	"task_delivery_stops": parentScoped("tasks", "task_id", "id",
		"p.driver_id = {driver} OR p.helper_driver_id = {driver}", ""),
	"trip_delivery_stops":          parentScoped("trip_records", "trip_id", "id", ownDriver, ""),
	"trip_photos":                  parentScoped("trip_records", "trip_id", "id", ownDriver, ""),
	"trip_no_history":              parentScoped("trip_records", "trip_id", "id", ownDriver, ""),
	"standby_photos":               parentScoped("standby_records", "standby_id", "id", ownDriver, ""),
	"trip_billing_stop_breakdown":  parentScoped("trip_billing_snapshots", "trip_id", "trip_id", "", ""),
	"billing_statement_lines":      parentScoped("billing_statements", "statement_id", "id", "", ""),
	"statement_documents":          parentScoped("billing_statements", "statement_id", "id", "", ""),
	"driver_customer_codes":        parentScoped("drivers", "driver_id", "id", "p.id = {driver}", ""),
	"truck_files":                  parentScoped("trucks", "truck_id", "id", "", ""),
	"maintenance_files":            parentScoped("maintenance_records", "maintenance_id", "id", "", ""),
	"penalty_types":                parentScoped("driver_compensation_configs", "config_id", "id", "", ""),
	"payroll_line_items":           parentScoped("payroll_runs", "payroll_run_id", "id", ownDriver, ""),
	"payroll_penalty_applications": parentScoped("payroll_runs", "payroll_run_id", "id", ownDriver, ""),
	"leave_request_attachments":    parentScoped("leave_requests", "leave_request_id", "id", ownDriver, ""),
	"chat_messages":                parentScoped("chats", "chat_id", "id", ownDriver, ""),
	"chat_read_state":              parentScoped("chats", "chat_id", "id", ownDriver, "r.user_id = {user}"),
	// A broadcast with a NULL tenant is platform-wide (stewards write it, every tenant's staff read it), so its
	// recipient and read rows are platform rows like the broadcast.
	"broadcast_recipients": parentScoped("broadcasts", "broadcast_id", "id", "", "r.user_id = {user}"),
	"broadcast_reads":      parentScoped("broadcasts", "broadcast_id", "id", "", "r.user_id = {user}"),

	"auth_identities":       userKeyed(),
	"user_platform_roles":   userKeyed(),
	"user_scopes":           userKeyed(),
	"sessions":              userKeyed(),
	"device_tokens":         userKeyed(),
	"password_reset_tokens": userKeyed(),
	"refresh_tokens": {via: "sessions",
		tenant: "EXISTS (SELECT 1 FROM sessions s WHERE s.id = r.session_id AND " + memberOfTenant("s.user_id") + ")",
		self:   "EXISTS (SELECT 1 FROM sessions s WHERE s.id = r.session_id AND s.user_id = {user})"},
	// status_history follows its entity (app_status_entity_visible).
	"status_history": {via: "entity_type, entity_id",
		tenant: `CASE r.entity_type
			WHEN 'driver' THEN EXISTS (SELECT 1 FROM drivers p WHERE p.id = r.entity_id AND p.tenant_id = {tenant})
			WHEN 'truck' THEN EXISTS (SELECT 1 FROM trucks p WHERE p.id = r.entity_id AND p.tenant_id = {tenant})
			WHEN 'truck_assignment' THEN EXISTS (SELECT 1 FROM truck_assignments p WHERE p.id = r.entity_id AND p.tenant_id = {tenant})
			WHEN 'tenant' THEN r.entity_id = {tenant}
			WHEN 'user' THEN ` + memberOfTenant("r.entity_id") + `
			ELSE false END`,
		self: `CASE r.entity_type
			WHEN 'driver' THEN r.entity_id = {driver}
			WHEN 'truck_assignment' THEN EXISTS (SELECT 1 FROM truck_assignments p WHERE p.id = r.entity_id AND p.driver_id = {driver})
			WHEN 'user' THEN r.entity_id = {user}
			ELSE false END`},
}

// stamped is the ownership of a table with a tenant_id stamp: a NULL tenant is a platform row; a driver's own
// rows (driver_id, a task's helper_driver_id) and the user's own memberships, driver row and uploads are its
// R24 history; a public file object is readable by design (C.3.5 file_objects p_read).
func stamped(t rlsTable) ownership {
	o := ownership{tenant: "r.tenant_id IS NULL OR r.tenant_id = {tenant}"}
	var self []string
	if t.driverID {
		self = append(self, "r.driver_id = {driver}")
	}
	if t.helperID {
		self = append(self, "r.helper_driver_id = {driver}")
	}
	switch t.name {
	case "memberships":
		self = append(self, "r.user_id = {user}")
	case "drivers":
		self = append(self, "r.id = {driver}")
	}
	if t.hasVisibility {
		o.tenant += " OR r.visibility = 'public'"
	}
	if t.hasUploadedBy {
		self = append(self, "r.uploaded_by = {user}")
	}
	o.self = strings.Join(self, " OR ")
	return o
}

// isolationCheck is the ownership of one readable RLS table.
type isolationCheck struct {
	table string
	own   ownership
}

// isolationPlan is the classification of every readable RLS table.
type isolationPlan struct {
	checks         []isolationCheck
	stamped, owned int
	shared         []string
	problems       []string // unclassified tables, stale rules: invariant 10 violations
}

// classify maps every readable RLS table onto its check: an ownership rule wins, then the shared list, then a
// tenant_id stamp; anything else is a problem, and so is a rule or shared entry for a table that is not a
// readable RLS table any more (the schema moved under the check).
func classify(tables []rlsTable) isolationPlan {
	var p isolationPlan
	seen := map[string]bool{}
	shared := map[string]bool{}
	for _, s := range sharedTables {
		shared[s] = true
	}
	for _, t := range tables {
		seen[t.name] = true
		if o, ok := ownershipRules[t.name]; ok {
			p.checks = append(p.checks, isolationCheck{table: t.name, own: o})
			p.owned++
			continue
		}
		if shared[t.name] {
			p.shared = append(p.shared, t.name)
			continue
		}
		if t.tenantID {
			p.checks = append(p.checks, isolationCheck{table: t.name, own: stamped(t)})
			p.stamped++
			continue
		}
		p.problems = append(p.problems, fmt.Sprintf("RLS table %s is readable by logitrack_app but has no isolation rule "+
			"(no tenant_id stamp, no entry in ownershipRules or sharedTables): --verify cannot prove carriers are isolated in it", t.name))
	}
	var stale []string
	for name := range ownershipRules {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	for _, name := range sharedTables {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		p.problems = append(p.problems, fmt.Sprintf("isolation rule for %s, which is not an RLS table logitrack_app may read", name))
	}
	return p
}

// rlsTables lists the row-level-security tables logitrack_app may SELECT, with the columns ownership uses. Out of
// scope by design: the exempt-service-layer tables without RLS (outbox_events, jobs, settings, ...), whose
// service decides access, and the tables granted to no app login (billing_counters: SECURITY DEFINER
// allocators only), Appendix C §C.3.0.
func (v *Verifier) rlsTables(ctx context.Context) ([]rlsTable, error) {
	var out []rlsTable
	err := db.WithSystem(ctx, v.ETL, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.relname::text,
			EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped),
			EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'driver_id' AND NOT a.attisdropped),
			EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'helper_driver_id' AND NOT a.attisdropped),
			EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'visibility' AND NOT a.attisdropped),
			EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'uploaded_by' AND NOT a.attisdropped)
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
			WHERE c.relkind IN ('r', 'p') AND c.relrowsecurity AND has_table_privilege('logitrack_app', c.oid, 'SELECT')
			ORDER BY 1`)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var t rlsTable
			if err := r.Scan(&t.name, &t.tenantID, &t.driverID, &t.helperID, &t.hasVisibility, &t.hasUploadedBy); err != nil {
				return err
			}
			out = append(out, t)
			return nil
		})
	})
	return out, err
}

// carrierPrincipal is one role-played carrier member.
type carrierPrincipal struct {
	user, tenant uuid.UUID
	label        string
	driver       bool
}

// carrierPrincipals are the active members of carrier tenants without a dispatcher grant (a dispatcher reads
// across tenants through the scope views by design, C.1.7): one per (tenant, role), which covers every
// tenant-and-role policy branch, plus every broker driver (a driver with memberships in more than one tenant,
// R24), whose own history in its former tenant is the one cross-tenant read the check allows.
func (v *Verifier) carrierPrincipals(ctx context.Context) ([]carrierPrincipal, error) {
	var out []carrierPrincipal
	err := db.WithSystem(ctx, v.ETL, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH cand AS (
			SELECT m.user_id, m.tenant_id, m.role, u.email::text AS email,
				coalesce(u.email::text, m.user_id::text) || ' (' || coalesce(t.code::text, t.name_th) || ', ' || m.role || ')' AS label,
				m.role = 'driver' AND EXISTS (SELECT 1 FROM memberships o WHERE o.user_id = m.user_id AND o.tenant_id <> m.tenant_id) AS broker
			FROM memberships m JOIN tenants t ON t.id = m.tenant_id JOIN users u ON u.id = m.user_id
			WHERE t.kind = 'carrier' AND m.status = 'active' AND u.status = 'active'
			  AND NOT EXISTS (SELECT 1 FROM user_scopes s WHERE s.user_id = m.user_id AND s.kind = 'dispatcher'))
			SELECT user_id, tenant_id, label, role = 'driver' FROM (
				SELECT DISTINCT ON (tenant_id, role) * FROM cand WHERE NOT broker ORDER BY tenant_id, role, email) one
			UNION ALL
			SELECT user_id, tenant_id, label, true FROM cand WHERE broker
			ORDER BY 3`)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var c carrierPrincipal
			if err := r.Scan(&c.user, &c.tenant, &c.label, &c.driver); err != nil {
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

// isolationSQL is one query over every classified table: per table the visible rows that are neither the
// principal's tenant's (or shared) nor its own (leak), and those that are its own in another tenant (own).
// The context goes in as uuid literals (ids read from the database, never user input), so the planner sees
// constants in every policy branch and lookup.
func isolationSQL(checks []isolationCheck, tenant uuid.UUID, driver *uuid.UUID, user uuid.UUID) string {
	lit := func(id *uuid.UUID) string {
		if id == nil || *id == uuid.Nil {
			return "NULL::uuid"
		}
		return "'" + id.String() + "'::uuid"
	}
	ctx := strings.NewReplacer("{tenant}", lit(&tenant), "{driver}", lit(driver), "{user}", lit(&user))
	parts := make([]string, len(checks))
	for i, ch := range checks {
		tenantOwn, self := "("+ch.own.tenant+")", "false"
		if ch.own.self != "" {
			self = "(" + ch.own.self + ")"
		}
		parts[i] = fmt.Sprintf(`SELECT %s AS t,
			count(*) FILTER (WHERE NOT coalesce(%s, false) AND NOT coalesce(%s, false)) AS leak,
			count(*) FILTER (WHERE NOT coalesce(%s, false) AND coalesce(%s, false)) AS own
			FROM %s r`, quoteLiteral(ch.table), tenantOwn, self, tenantOwn, self, pgx.Identifier{ch.table}.Sanitize())
	}
	return ctx.Replace(`SELECT t, leak, own FROM (` + strings.Join(parts, "\nUNION ALL ") + `) x WHERE leak > 0 OR own > 0 ORDER BY t`)
}

// carrierResult is the outcome of one carrier role-play.
type carrierResult struct {
	leaks []string
	own   []string // "<table> <n>": the principal's own rows in another tenant (R24), reported, not failed
}

// carrierLeaks runs isolationSQL as the carrier principal, and checks that it reads its own tenant row at all, so
// an empty context cannot pass vacuously.
func carrierLeaks(ctx context.Context, tx pgx.Tx, plan isolationPlan, p *authz.Principal, tenant uuid.UUID) (carrierResult, error) {
	var res carrierResult
	via := map[string]string{}
	for _, ch := range plan.checks {
		via[ch.table] = ch.own.via
	}
	// One statement over some sixty RLS tables, each with its policies' subqueries, crosses the JIT cost
	// thresholds: compiling it took seconds per principal on the load profile while executing it takes
	// milliseconds. A planner setting for this read-only transaction, not a request GUC.
	if _, err := tx.Exec(ctx, `SET LOCAL jit = off`); err != nil {
		return res, err
	}
	rows, err := tx.Query(ctx, isolationSQL(plan.checks, tenant, p.DriverID, p.UserID)) // DriverID: drivers only
	if err != nil {
		return res, err
	}
	err = scanAll(rows, func(r pgx.Rows) error {
		var t string
		var leak, own int64
		if err := r.Scan(&t, &leak, &own); err != nil {
			return err
		}
		if leak > 0 {
			msg := fmt.Sprintf("reads %d row(s) of other tenants in %s", leak, t)
			if via[t] != "" {
				msg += " (owner through " + via[t] + ")"
			}
			res.leaks = append(res.leaks, msg)
		}
		if own > 0 {
			res.own = append(res.own, fmt.Sprintf("%s %d", t, own))
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = $1`, tenant).Scan(&n); err != nil {
		return res, err
	}
	if n != 1 {
		res.leaks = append(res.leaks, "cannot read its own tenant row (the role-play context is empty)")
	}
	return res, nil
}
