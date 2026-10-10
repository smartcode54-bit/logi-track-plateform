// Package scope is the read side of the two scope axes (Appendix C §C.3.7, R85): a dispatcher (dsp, a
// dispatcher grant over billing parties, reading across tenants) and a customer-scope user (cs without a
// tenant). Both are served exclusively from the security_invoker views scope_tasks, scope_trips,
// scope_standby, scope_incidents, scope_drivers, scope_trucks and scope_tenants: rows come from the
// p_scope_read policies under the caller's RLS (db.WithPrincipal), columns from the views, so no
// billing, party, evidence-token, HR, ID-card, licence, insurance, tax or cost column ever reaches a
// scope principal.
//
// The queries live in repo/scope_*.sql (sqlc block "scope", generated into scopedb). sqlc vet rule
// scope-views-only (make gen-check) fails when one of them names a base table or writes; domain
// services (T2x) build the dispatcher and customer endpoints on these queries, never on their own
// queries of the base tables. Carrier-internal tables (rate cards, billing snapshots, statements,
// expenses, maintenance, payroll, penalties, compensation config) have no scope policy at all.
package scope

import "strings"

// Columns no scope projection may expose (Appendix C §C.3.7, §C.9.2 #11): the billing, party and
// evidence columns of the operational tables, the PII and HR columns of drivers, the insurance, tax and
// cost columns of trucks, review and notification flags, and creator / legacy bookkeeping.
var (
	hiddenPrefixes = []string{"billing_", "insurance_", "tax_", "employment_", "review_", "legacy_", "evidence_"}
	hiddenNames    = []string{
		"source_linked_party_id", "destination_linked_party_id", "customer_party_id", "customer_resolved",
		"customer_resolved_from", "id_card", "id_card_expired_date", "id_card_file_id", "truck_license_id",
		"license_type", "license_expired_date", "license_file_id", "birth_date", "contract_years", "hire_date",
		"probation_passed", "email", "user_id", "payment_method", "ocr_data", "needs_admin_review",
		"line_checkin_notified_at", "line_notified_at", "line_delivered_notified_at", "created_by", "updated_by",
		"client_op_id", "maintenance_responsible", "tax_responsible",
	}
)

// IsHidden reports whether a column name must never appear in a scope projection or its DTO.
func IsHidden(column string) bool {
	for _, p := range hiddenPrefixes {
		if strings.HasPrefix(column, p) {
			return true
		}
	}
	for _, n := range hiddenNames {
		if column == n {
			return true
		}
	}
	return false
}
