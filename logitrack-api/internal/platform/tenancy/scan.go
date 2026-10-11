package tenancy

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ScanListLimit bounds each drift list of a scan result (the job result is one jsonb value); the totals always count
// every row, so a list shorter than its total says so (no silent cap, Appendix B §B.1.5).
const ScanListLimit = 1000

// ScanResult is the result of the tenancy.orphan-scan job (main spec §7.4, Appendix C §C.3.10, Appendix D smoke job
// JB2): the rows of the quarantine tenant per tenant-stamped table, and the broker-drift report of R24 over the rows
// stamped from a driver (tenant_source 'driver'). Nothing is moved: quarantined rows wait for a re-home by a platform
// admin, and drifted rows keep their frozen tenant by design.
type ScanResult struct {
	// QuarantineRows counts, per table with at least one, the rows in the quarantine tenant (R11).
	QuarantineRows map[string]int `json:"quarantineRows"`
	// QuarantineTotal is the sum of QuarantineRows: tenant_orphans_detected is raised when it is above zero.
	QuarantineTotal int `json:"quarantineTotal"`
	// DriverMoved are the rows stamped from their driver whose tenant is no longer the driver's (the driver moved
	// carrier after the row was written: the post-cut-over signal of R24).
	DriverMoved      []Drift `json:"driverMoved"`
	DriverMovedTotal int     `json:"driverMovedTotal"`
	// TruckTenantMismatch are the rows stamped from their driver whose truck belongs to another tenant (the only drift
	// evidence for history the ETL stamped from the driver's tenant at load time, Appendix D §D.5.3).
	TruckTenantMismatch      []Drift `json:"truckTenantMismatch"`
	TruckTenantMismatchTotal int     `json:"truckTenantMismatchTotal"`
	// Tables are every tenant-stamped table the scan counted (the metric sets each, zero included).
	Tables []string `json:"-"`
}

// Drift is one row of a drift bucket: its table and id, its driver, its tenant and the tenant that disagrees with it
// (the driver's now, or the truck's).
type Drift struct {
	Table        string     `json:"table"`
	ID           uuid.UUID  `json:"id"`
	DriverID     uuid.UUID  `json:"driverId"`
	RowTenant    uuid.UUID  `json:"rowTenant"`
	DriverTenant *uuid.UUID `json:"driverTenant,omitempty"`
	TruckID      *uuid.UUID `json:"truckId,omitempty"`
	TruckTenant  *uuid.UUID `json:"truckTenant,omitempty"`
}

// stampedTable is a tenant-stamped table of the public schema and the columns the drift checks need.
type stampedTable struct {
	name                 string
	hasID, driver, truck bool
}

// Scan is the read-only work of tenancy.orphan-scan in the caller's transaction, which must read every tenant (a
// system transaction: the quarantine tenant is invisible to every request principal, R11). It discovers the
// tenant-stamped tables from the catalog (a tenant_id and a tenant_source column; the tables the login may read), so a
// table a later migration adds is counted without a code change. Drift is checked on the tables that also have an id
// and a driver_id (and, for the truck bucket, a truck_id); rows of the quarantine tenant are never drift.
func Scan(ctx context.Context, tx pgx.Tx) (ScanResult, error) {
	tables, err := stampedTables(ctx, tx)
	if err != nil {
		return ScanResult{}, err
	}
	res := ScanResult{QuarantineRows: map[string]int{}, DriverMoved: []Drift{}, TruckTenantMismatch: []Drift{}}
	q := uuid.MustParse(QuarantineTenantID)
	for _, t := range tables {
		res.Tables = append(res.Tables, t.name)
		ident := pgx.Identifier{"public", t.name}.Sanitize()
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM `+ident+` WHERE tenant_id = $1`, q).Scan(&n); err != nil {
			return ScanResult{}, fmt.Errorf("tenancy: count the quarantine rows of %s: %w", t.name, err)
		}
		if n > 0 {
			res.QuarantineRows[t.name] = n
			res.QuarantineTotal += n
		}
		if !t.hasID || !t.driver {
			continue
		}
		moved, total, err := driftRows(ctx, tx, t.name, `SELECT r.id, r.driver_id, r.tenant_id, d.tenant_id, NULL::uuid, NULL::uuid
			FROM `+ident+` r JOIN drivers d ON d.id = r.driver_id
			WHERE r.tenant_source = 'driver' AND r.tenant_id <> $1 AND r.tenant_id <> d.tenant_id`, q, ScanListLimit-len(res.DriverMoved))
		if err != nil {
			return ScanResult{}, err
		}
		res.DriverMoved, res.DriverMovedTotal = append(res.DriverMoved, moved...), res.DriverMovedTotal+total
		if !t.truck {
			continue
		}
		mismatch, total, err := driftRows(ctx, tx, t.name, `SELECT r.id, r.driver_id, r.tenant_id, NULL::uuid, r.truck_id, k.tenant_id
			FROM `+ident+` r JOIN trucks k ON k.id = r.truck_id
			WHERE r.tenant_source = 'driver' AND r.driver_id IS NOT NULL AND r.tenant_id <> $1 AND r.tenant_id <> k.tenant_id`,
			q, ScanListLimit-len(res.TruckTenantMismatch))
		if err != nil {
			return ScanResult{}, err
		}
		res.TruckTenantMismatch = append(res.TruckTenantMismatch, mismatch...)
		res.TruckTenantMismatchTotal += total
	}
	return res, nil
}

// stampedTables lists the tenant-stamped tables of the public schema the login may read, in name order.
func stampedTables(ctx context.Context, tx pgx.Tx) ([]stampedTable, error) {
	rows, err := tx.Query(ctx, `SELECT c.relname::text,
		       EXISTS (SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'id'
		                AND a.atttypid = 'uuid'::regtype AND NOT a.attisdropped),
		       EXISTS (SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'driver_id'
		                AND a.atttypid = 'uuid'::regtype AND NOT a.attisdropped),
		       EXISTS (SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'truck_id'
		                AND a.atttypid = 'uuid'::regtype AND NOT a.attisdropped)
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_catalog.pg_attribute s ON s.attrelid = c.oid AND s.attname = 'tenant_source' AND NOT s.attisdropped
		JOIN pg_catalog.pg_attribute t ON t.attrelid = c.oid AND t.attname = 'tenant_id' AND NOT t.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND has_table_privilege(c.oid, 'SELECT')
		ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("tenancy: list the tenant-stamped tables: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stampedTable, error) {
		var t stampedTable
		return t, r.Scan(&t.name, &t.hasID, &t.driver, &t.truck)
	})
}

// driftRows runs one drift query of table: the total, and at most limit rows in id order.
func driftRows(ctx context.Context, tx pgx.Tx, table, query string, q uuid.UUID, limit int) ([]Drift, int, error) {
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM (`+query+`) x`, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tenancy: drift of %s: %w", table, err)
	}
	if total == 0 || limit <= 0 {
		return nil, total, nil
	}
	rows, err := tx.Query(ctx, query+` ORDER BY r.id LIMIT $2`, q, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("tenancy: drift of %s: %w", table, err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Drift, error) {
		d := Drift{Table: table}
		return d, r.Scan(&d.ID, &d.DriverID, &d.RowTenant, &d.DriverTenant, &d.TruckID, &d.TruckTenant)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("tenancy: drift of %s: %w", table, err)
	}
	return out, total, nil
}

// SortedQuarantine lists the tables of QuarantineRows in name order (log lines and summaries).
func (r ScanResult) SortedQuarantine() []string {
	out := make([]string, 0, len(r.QuarantineRows))
	for t := range r.QuarantineRows {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
