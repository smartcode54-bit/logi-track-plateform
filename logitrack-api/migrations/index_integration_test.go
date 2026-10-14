//go:build integration

// Index hygiene of the whole chain (issue T61, Appendix A §A.2.19): every foreign key has an index on its columns or a
// written reason not to, and no non-unique index repeats a prefix of another one. The plan-shape test of the hot
// queries on the load profile is cmd/seed's TestLoadProfile.
package migrations_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Reasons a foreign key stays without an index of its own. An index on a referencing column pays off when its parent
// rows are deleted (or their key updated) at run time, since each such statement checks every referencing table, or
// when a query looks rows up by the column; neither applies here.
const (
	// Audit columns that record who did something. users rows are never deleted (SoftDeleteUser sets status
	// 'deleted'; only cmd/seed --reset deletes them), and no query reads a table by its actor.
	whyActor = "audit actor: users are soft-deleted only, and no query looks rows up by it"
	// tenants rows are deleted only by cmd/seed --reset; reads filter the tenant through RLS next to a narrower
	// index (the party, the trip, the period) that every query of the table names.
	whyTenant = "tenants are never deleted at run time; queries reach the table through a narrower index"
	// File references of a master table: storage.gc's delete of an expired pending file checks it, but the table
	// holds at most a few thousand rows (one per customer, company, driver, release, attachment), read in a few pages.
	whySmallFile = "file reference of a small master table (a few pages for storage.gc's check)"
	// Parents that are void-only or never deleted at run time, on a column that no query looks rows up by.
	whyNoDelete = "the parent is never deleted at run time and no query looks rows up by it"
	// A filter that only narrows a list another index already ranges (by tenant, period or driver).
	whySecondary = "a secondary filter of lists another index ranges; the parent is never deleted at run time"
)

// fkWithoutIndex is every foreign key ("table.column[,column]") without a supporting index, with the reason.
var fkWithoutIndex = map[string]string{
	// identity and platform (0002, 0008, 0009)
	"api_keys.created_by":                       whyActor,
	"api_keys.revoked_by":                       whyActor,
	"memberships.created_by":                    whyActor,
	"password_reset_tokens.requested_by":        whyActor,
	"role_capability_overrides.updated_by":      whyActor,
	"security_events.target_user_id":            whyActor,
	"sessions.revoked_by":                       whyActor,
	"settings.updated_by":                       whyActor,
	"status_history.changed_by_user_id":         whyActor,
	"user_platform_roles.granted_by":            whyActor,
	"user_scopes.created_by":                    whyActor,
	"file_objects.uploaded_by":                  whyActor,
	"api_keys.tenant_id":                        whyTenant,
	"outbox_events.tenant_id":                   whyTenant,
	"sessions.active_tenant_id":                 whyTenant,
	"sessions.user_id":                          "sessions_user_active (user_id, last_seen_at) WHERE revoked_at IS NULL serves the reads; users are never deleted (ON DELETE CASCADE never fires)",
	"users.photo_file_id":                       whySmallFile,
	"mobile_app_releases.apk_file_id":           whySmallFile,
	"statement_documents.file_id":               whySmallFile,
	"leave_request_attachments.file_id":         whySmallFile,
	"customers.logo_file_id":                    whySmallFile,
	"companies.logo_file_id":                    whySmallFile,
	"companies.signature_file_id":               whySmallFile,
	"companies.stamp_file_id":                   whySmallFile,
	"drivers.id_card_file_id":                   whySmallFile,
	"drivers.license_file_id":                   whySmallFile,
	"drivers.profile_file_id":                   whySmallFile,
	"driver_penalties.evidence_file_id":         whySmallFile,
	"tenant_files.file_id":                      whySmallFile,
	"truck_files.file_id":                       whySmallFile,
	"standby_rate_entries.billing_party_id":     "standby_rate_entries_lookup (billing_party_id, ...) WHERE voided_at IS NULL serves the pricing lookup; the rate-card list reads the whole small table; parties are never deleted",
	"billing_statements.issuer_company_id":      whyNoDelete,
	"driver_customer_codes.customer_id":         whyNoDelete,
	"drivers.active_task_id":                    whyNoDelete,
	"drivers.current_assignment_id":             whyNoDelete,
	"trucks.active_maintenance_id":              whyNoDelete,
	"payroll_runs.ledger_transaction_id":        whyNoDelete,
	"driver_advances.payroll_run_id":            whyNoDelete,
	"standby_records.billing_rate_entry_id":     whyNoDelete,
	"standby_records.billing_tenant_id":         whyTenant,
	"driver_advances.driver_id":                 "driver_advances_open (driver_id, ...) WHERE status = 'pending' serves the payroll read; drivers are never deleted",
	"driver_penalties.driver_id":                "driver_penalties_open (driver_id) WHERE status IN (pending, partially_deducted) serves the payroll read; drivers are never deleted",
	"transactions.driver_id":                    whySecondary,
	"incident_reports.truck_id":                 whySecondary,
	"standby_records.truck_id":                  whySecondary,
	"task_delivery_stops.destination_hub_id":    whySecondary,
	"trip_delivery_stops.destination_hub_id":    whySecondary,
	"tasks.source_hub_id":                       whySecondary,
	"tasks.destination_hub_id":                  whySecondary,
	"trip_records.origin_hub_id":                whySecondary,
	"trip_records.destination_hub_id":           whySecondary,
	"billing_counters.tenant_id":                whyTenant,
	"broadcasts.tenant_id":                      whyTenant,
	"customer_fuel_rate_adjustments.tenant_id":  whyTenant,
	"customer_rate_entries.tenant_id":           whyTenant,
	"customer_service_fees.tenant_id":           whyTenant,
	"standby_rate_entries.tenant_id":            whyTenant,
	"trip_billing_snapshots.tenant_id":          whyTenant,
	"billing_statements.generated_by":           whyActor,
	"broadcasts.created_by_user_id":             whyActor,
	"broadcasts.voided_by":                      whyActor,
	"chat_messages.sender_user_id":              whyActor,
	"chats.last_message_by_user_id":             whyActor,
	"customer_fuel_rate_adjustments.created_by": whyActor,
	"customer_fuel_rate_adjustments.voided_by":  whyActor,
	"customer_rate_entries.created_by":          whyActor,
	"customer_rate_entries.voided_by":           whyActor,
	"driver_advances.created_by":                whyActor,
	"driver_compensation_configs.created_by":    whyActor,
	"driver_penalties.created_by":               whyActor,
	"holidays.created_by":                       whyActor,
	"hub_soc_distances.created_by":              whyActor,
	"hub_soc_distances.updated_by":              whyActor,
	"hubs.created_by":                           whyActor,
	"incident_reports.reported_by_user_id":      whyActor,
	"leave_requests.approver_user_id":           whyActor,
	"maintenance_records.created_by":            whyActor,
	"maintenance_records.updated_by":            whyActor,
	"payroll_runs.approved_by":                  whyActor,
	"standby_rate_entries.voided_by":            whyActor,
	"tasks.created_by":                          whyActor,
	"transactions.performed_by_user_id":         whyActor,
	"trip_no_history.renamed_by":                whyActor,
	"truck_assignments.admin_user_id":           whyActor,
	"trucks.created_by":                         whyActor,
	"trucks.updated_by":                         whyActor,
	"vehicle_expenses.created_by_user_id":       whyActor,
	"chats.assigned_admin_user_id":              "chats_my (assigned_admin_user_id, ...) WHERE status <> 'closed' serves the my-chats list; users are never deleted",
	"chat_read_state.user_id":                   "chat_read_state's primary key (chat_id, user_id) serves the reads by chat; users are never deleted (ON DELETE CASCADE never fires)",
}

// unindexedForeignKeys lists the foreign keys of schema public whose columns are not the leading columns (in any
// order) of a valid index: a full index, or for a one-column key a partial index WHERE <column> IS NOT NULL (which
// every equality lookup on the column, the referential checks included, can use).
const unindexedForeignKeys = `
SELECT c.conrelid::regclass::text || '.' ||
       (SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum)
FROM pg_constraint c
WHERE c.contype = 'f' AND c.connamespace = 'public'::regnamespace
  AND NOT EXISTS (
    SELECT 1 FROM pg_index i
    WHERE i.indrelid = c.conrelid AND i.indisvalid AND cardinality(c.conkey) <= i.indnkeyatts
      AND (i.indkey::int2[])[0:cardinality(c.conkey) - 1] @> c.conkey
      AND (i.indpred IS NULL OR (cardinality(c.conkey) = 1 AND pg_get_expr(i.indpred, i.indrelid) =
           '(' || (SELECT quote_ident(a.attname) FROM pg_attribute a
                   WHERE a.attrelid = c.conrelid AND a.attnum = c.conkey[1]) || ' IS NOT NULL)')))
ORDER BY 1`

// AC: every foreign-key column has an index or a documented reason not to; a reason whose key gained an index (or no
// longer exists) is removed, so the list never goes stale.
func TestEveryForeignKeyHasAnIndexOrAReason(t *testing.T) {
	d, _ := migrated(t)
	c := connect(t, d, db.RoleMigrator)
	got := strs(t, c, unindexedForeignKeys)
	var missing []string
	for _, fk := range got {
		if why := fkWithoutIndex[fk]; why == "" {
			missing = append(missing, fk)
		}
	}
	if len(missing) > 0 {
		t.Errorf("foreign keys without an index and without a reason in fkWithoutIndex: index them in a new migration "+
			"(Appendix A §A.2.19) or write why not:\n  %s", strings.Join(missing, "\n  "))
	}
	for fk := range fkWithoutIndex {
		if !slices.Contains(got, fk) {
			t.Errorf("fkWithoutIndex[%q] is stale: the key has an index now (or is gone); remove the entry", fk)
		}
	}
}

// redundantIndexes lists each non-unique btree index that backs no constraint and whose key columns (with the same
// operator classes, collations, sort options, no expressions and the same predicate) are a prefix of another valid
// btree index of the table, which can serve every lookup it serves. For two identical indexes the later one is listed.
const redundantIndexes = `
SELECT format('%s: %s is a prefix of %s', a.indrelid::regclass, ai.relname, bi.relname)
FROM pg_index a
JOIN pg_class ai ON ai.oid = a.indexrelid
JOIN pg_am aam ON aam.oid = ai.relam AND aam.amname = 'btree'
JOIN pg_index b ON b.indrelid = a.indrelid AND b.indexrelid <> a.indexrelid AND b.indisvalid
JOIN pg_class bi ON bi.oid = b.indexrelid
JOIN pg_am bam ON bam.oid = bi.relam AND bam.amname = 'btree'
WHERE ai.relnamespace IN ('public'::regnamespace, 'etl'::regnamespace)
  AND NOT a.indisunique
  AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = a.indexrelid)
  AND a.indexprs IS NULL AND b.indexprs IS NULL
  AND a.indnkeyatts <= b.indnkeyatts
  AND (a.indkey::int2[])[0:a.indnkeyatts - 1] = (b.indkey::int2[])[0:a.indnkeyatts - 1]
  AND (a.indclass::oid[])[0:a.indnkeyatts - 1] = (b.indclass::oid[])[0:a.indnkeyatts - 1]
  AND (a.indcollation::oid[])[0:a.indnkeyatts - 1] = (b.indcollation::oid[])[0:a.indnkeyatts - 1]
  AND (a.indoption::int2[])[0:a.indnkeyatts - 1] = (b.indoption::int2[])[0:a.indnkeyatts - 1]
  AND coalesce(pg_get_expr(a.indpred, a.indrelid), '') = coalesce(pg_get_expr(b.indpred, b.indrelid), '')
  AND (a.indnkeyatts < b.indnkeyatts OR b.indisunique OR a.indexrelid > b.indexrelid)
ORDER BY 1`

// AC: no redundant non-unique index remains (indexes that back a constraint are kept).
func TestNoRedundantIndex(t *testing.T) {
	d, _ := migrated(t)
	c := connect(t, d, db.RoleMigrator)
	if got := strs(t, c, redundantIndexes); len(got) > 0 {
		t.Errorf("redundant indexes: drop each in a new migration (DROP INDEX CONCURRENTLY, R31):\n  %s",
			strings.Join(got, "\n  "))
	}
}

// The redundancy query finds what it is meant to: a prefix duplicate and an exact duplicate, but not an index with
// another predicate, another sort order or one that backs a constraint.
func TestRedundantIndexQueryFindsPrefixDuplicates(t *testing.T) {
	d, _ := migrated(t)
	c := connect(t, d, db.RoleMigrator)
	for _, s := range []string{
		`CREATE TABLE t61_probe (a int, b int, c int, CONSTRAINT t61_probe_ab UNIQUE (a, b))`,
		`CREATE INDEX t61_a ON t61_probe (a)`,                         // prefix of t61_probe_ab (a constraint index)
		`CREATE INDEX t61_bc ON t61_probe (b, c)`,                     //
		`CREATE INDEX t61_b ON t61_probe (b)`,                         // prefix of t61_bc
		`CREATE INDEX t61_bc_again ON t61_probe (b, c)`,               // the same as t61_bc
		`CREATE INDEX t61_c_partial ON t61_probe (c) WHERE c > 0`,     // no other index on (c ...) WHERE c > 0
		`CREATE INDEX t61_b_desc ON t61_probe (b DESC)`,               // other sort option than t61_bc
		`CREATE INDEX t61_bc_partial ON t61_probe (b, c) WHERE c > 0`, // other predicate than t61_bc
	} {
		exec(t, c, s)
	}
	got := strs(t, c, redundantIndexes)
	want := []string{
		"t61_probe: t61_a is a prefix of t61_probe_ab",
		"t61_probe: t61_b is a prefix of t61_bc",
		"t61_probe: t61_b is a prefix of t61_bc_again",
		"t61_probe: t61_bc_again is a prefix of t61_bc",
	}
	sameSet(t, "redundant indexes of the probe table", got, want)
}
