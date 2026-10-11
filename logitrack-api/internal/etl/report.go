package etl

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The quarantine report (main spec §13.9 step 1): two sorted TSV sections, the findings and the outcome of every
// applied document, without free-text details, so two reports of the same dump compare line by line. `load
// --dry-run --report` writes it from the run; QuarantineReport reads the same from the database after a load.

// ReportLine is one line of a section.
type ReportLine []string

func (l ReportLine) String() string { return strings.Join(l, "\t") }

// ReportSections are the findings and outcome lines.
type ReportSections struct {
	Findings []ReportLine // collection, doc_path, field ('-' for the row), reason_code
	Outcomes []ReportLine // collection, doc_path, status, tenant_source ('-' when the row has none)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func sortLines(ls []ReportLine) {
	slices.SortFunc(ls, func(a, b ReportLine) int { return strings.Compare(a.String(), b.String()) })
}

// Sections builds the report of a load run.
func (r *Report) Sections() ReportSections {
	var s ReportSections
	for _, d := range r.Docs {
		s.Outcomes = append(s.Outcomes, ReportLine{d.Collection, d.Path, string(d.Outcome), dash(d.TenantSource)})
		for _, f := range d.Findings {
			s.Findings = append(s.Findings, ReportLine{d.Collection, d.Path, dash(f.Field), string(f.Reason)})
		}
	}
	for _, x := range r.Extra {
		s.Findings = append(s.Findings, ReportLine{x.Collection, x.Path, dash(x.Finding.Field), string(x.Finding.Reason)})
	}
	sortLines(s.Findings)
	sortLines(s.Outcomes)
	return s
}

// Write prints the report.
func (s ReportSections) Write(w io.Writer) error {
	var b strings.Builder
	b.WriteString("# etl quarantine report: findings (collection, doc_path, field, reason_code)\n")
	for _, l := range s.Findings {
		b.WriteString(l.String() + "\n")
	}
	b.WriteString("# outcomes (collection, doc_path, status, tenant_source)\n")
	for _, l := range s.Outcomes {
		b.WriteString(l.String() + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// tenantStamped are the target tables that carry tenant_source (true) or none (false).
var tenantStamped = map[string]bool{"tenants": false, "customers": false, "trucks": true, "drivers": true, "tasks": true,
	"trip_records": true, "standby_records": true, "incident_reports": true, "vehicle_expenses": true, "maintenance_records": true,
	"companies": true, "customer_rate_entries": true, "customer_fuel_rate_adjustments": true, "customer_service_fees": true,
	"standby_rate_entries": true, "billing_statements": true, "billing_counters": true, "truck_assignments": true,
	"mobile_installations": true, "vehicle_locations": true, "driver_compensation_configs": true, "driver_penalties": true,
	"payroll_runs": true, "transactions": true, "chats": true, "leave_requests": true}

// targetKeys are the key expressions of the tables whose etl.source_docs.target_id is not their uuid id (composite
// or natural keys); every other table is keyed by id.
var targetKeys = map[string]string{
	"mobile_installations":   "driver_id::text || '/' || install_id",
	"vehicle_locations":      "truck_id::text",
	"billing_counters":       "billing_party_id::text || '/' || period_year::text || '/' || period_month::text",
	"fuel_daily_snapshots":   "day_key::text",
	"fuel_monthly_snapshots": "month_key",
	"settings":               "key",
}

// targetKey is the SQL expression etl.source_docs.target_id is compared with (table names come from the mappers).
func targetKey(table string) string {
	if k, ok := targetKeys[table]; ok {
		return k
	}
	return "id::text"
}

// QuarantineReport reads the report of the database's current state: open findings and every recorded document.
func (e *Engine) QuarantineReport(ctx context.Context) (ReportSections, error) {
	var s ReportSections
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT collection, doc_path, coalesce(field, '-'), reason_code FROM etl.quarantine WHERE resolved_at IS NULL`)
		if err != nil {
			return err
		}
		for rows.Next() {
			l := make(ReportLine, 4)
			if err := rows.Scan(&l[0], &l[1], &l[2], &l[3]); err != nil {
				return err
			}
			s.Findings = append(s.Findings, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		type doc struct{ coll, path, status, table, target string }
		var docs []doc
		rows, err = tx.Query(ctx, `SELECT collection, doc_path, status, coalesce(target_table, ''), coalesce(target_id, '') FROM etl.source_docs`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d doc
			if err := rows.Scan(&d.coll, &d.path, &d.status, &d.table, &d.target); err != nil {
				return err
			}
			docs = append(docs, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range docs {
			src := "-"
			if d.table == "tenants" && (d.status == string(Loaded) || d.status == string(Quarantined)) {
				src = "self"
			}
			if tenantStamped[d.table] && (d.status == string(Loaded) || d.status == string(Quarantined)) {
				if err := tx.QueryRow(ctx, `SELECT tenant_source FROM `+pgx.Identifier{d.table}.Sanitize()+` WHERE `+targetKey(d.table)+` = $1`, d.target).Scan(&src); err != nil {
					return fmt.Errorf("etl: %s: target %s %s: %w", d.path, d.table, d.target, err)
				}
			}
			s.Outcomes = append(s.Outcomes, ReportLine{d.coll, d.path, d.status, src})
		}
		return nil
	})
	sortLines(s.Findings)
	sortLines(s.Outcomes)
	return s, err
}
