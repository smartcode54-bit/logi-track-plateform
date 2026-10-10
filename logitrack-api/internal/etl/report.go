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

// tenantStamped are the target tables that carry tenant_source.
var tenantStamped = map[string]bool{"tenants": false, "customers": false, "trucks": true, "drivers": true, "tasks": true,
	"trip_records": true, "standby_records": true, "incident_reports": true, "vehicle_expenses": true, "maintenance_records": true}

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
				if err := tx.QueryRow(ctx, `SELECT tenant_source FROM `+pgx.Identifier{d.table}.Sanitize()+` WHERE id = $1::uuid`, d.target).Scan(&src); err != nil {
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
