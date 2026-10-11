package iam

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// The quarantine review of P1 (Appendix B §B.2.4, Appendix C §C.3.10, R11, R71): rows whose tenant chain ran out at the
// ETL load live in the quarantine tenant, invisible to every tenant; a platform principal lists them with the
// resolver trace (GET /v1/tenants/quarantine/rows, platform:cross_tenant_read) and a platform admin re-homes one with
// its children and their files (POST /v1/tenants/quarantine/rows/{table}/{id}/rehome, platform:cross_tenant_write,
// tenancy.Rehome under app.tenant_move, tenant_rehomed in the same transaction). Both run in the system context: the
// quarantine tenant is readable only under bypass. The api login cannot reach schema etl, so the trace is what the
// rows themselves carry (the raw legacy references and how they matched, R12) plus where each linked row is now; the
// ETL settles its own bookkeeping (etl.source_docs, the tenant_unresolved findings) on its next command.

// EventTenantRehomed is the audit of a re-home (Appendix C §C.4.13).
const EventTenantRehomed = "tenant_rehomed"

// QuarantineRow is one row of GET /v1/tenants/quarantine/rows.
type QuarantineRow struct {
	Table       string          `json:"table"`
	ID          uuid.UUID       `json:"id"`
	LegacyDocID *string         `json:"legacyDocId"`
	Label       *string         `json:"label"` // the row's human key: task or trip number, plate, name, type
	CreatedAt   time.Time       `json:"createdAt"`
	Trace       QuarantineTrace `json:"trace"`
}

// QuarantineTrace is the resolver trace of a row (main spec §13.5): the chain of its table, first hit wins, each link
// with the raw legacy reference the row kept, how it matched, and the linked row and its tenant when one resolved. A
// link whose row is itself in the quarantine tenant is why a child followed it there (Outcome follows_parent);
// otherwise every link ran out (chain_exhausted).
type QuarantineTrace struct {
	Chain   []string    `json:"chain"`
	Steps   []TraceStep `json:"steps"`
	Outcome string      `json:"outcome"` // chain_exhausted | follows_parent
}

// TraceStep is one link of the chain.
type TraceStep struct {
	Link        string     `json:"link"`  // driver | task | trip | truck | carrier
	Ref         *string    `json:"ref"`   // the legacy reference the row kept (legacy_*_ref), when the table stores one
	Match       *string    `json:"match"` // doc_id | auth_uid | name | task_no | ambiguous | none
	RowID       *uuid.UUID `json:"rowId"`
	TenantID    *uuid.UUID `json:"tenantId"`
	Quarantined bool       `json:"quarantined"`
}

// quarantineQueries are the per-table reads: id, legacy_doc_id, label, created_at and the steps as jsonb, in the
// chain order of main spec §13.5. Table names come only from this map; $1/$2 are the keyset position, $3 the limit.
var quarantineQueries = map[string]string{
	"tasks": `SELECT t.id, t.legacy_doc_id, t.task_no::text, t.created_at,
		  jsonb_build_array(jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id))
		FROM tasks t LEFT JOIN drivers d ON d.id = t.driver_id`,
	"trip_records": `SELECT t.id, t.legacy_doc_id, t.trip_no::text, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'task', 'ref', t.legacy_task_ref, 'match', t.task_ref_match, 'rowId', t.task_id, 'tenantId', k.tenant_id),
		  jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id))
		FROM trip_records t LEFT JOIN tasks k ON k.id = t.task_id LEFT JOIN drivers d ON d.id = t.driver_id`,
	"standby_records": `SELECT t.id, t.legacy_doc_id, NULL::text, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'task', 'rowId', t.task_id, 'tenantId', k.tenant_id),
		  jsonb_build_object('link', 'trip', 'rowId', t.trip_id, 'tenantId', r.tenant_id),
		  jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id))
		FROM standby_records t LEFT JOIN tasks k ON k.id = t.task_id LEFT JOIN trip_records r ON r.id = t.trip_id
		LEFT JOIN drivers d ON d.id = t.driver_id`,
	"incident_reports": `SELECT t.id, t.legacy_doc_id, t.delay_cause, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'trip', 'ref', t.legacy_trip_ref, 'rowId', t.trip_id, 'tenantId', r.tenant_id),
		  jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id))
		FROM incident_reports t LEFT JOIN trip_records r ON r.id = t.trip_id LEFT JOIN drivers d ON d.id = t.driver_id`,
	"drivers": `SELECT t.id, t.legacy_doc_id, btrim(t.first_name || ' ' || t.last_name), t.created_at,
		  jsonb_build_array(jsonb_build_object('link', 'carrier'))
		FROM drivers t`,
	"trucks": `SELECT t.id, t.legacy_doc_id, t.license_plate, t.created_at,
		  jsonb_build_array(jsonb_build_object('link', 'carrier', 'ref', t.ownership_type))
		FROM trucks t`,
	"companies": `SELECT t.id, t.legacy_doc_id, t.name_th, t.created_at,
		  jsonb_build_array(jsonb_build_object('link', 'carrier', 'ref', t.tax_id))
		FROM companies t`,
	"vehicle_expenses": `SELECT t.id, t.legacy_doc_id, t.expense_type, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id),
		  jsonb_build_object('link', 'truck', 'ref', t.truck_license_plate_snapshot, 'rowId', t.truck_id, 'tenantId', k.tenant_id))
		FROM vehicle_expenses t LEFT JOIN drivers d ON d.id = t.driver_id LEFT JOIN trucks k ON k.id = t.truck_id`,
	"maintenance_records": `SELECT t.id, t.legacy_doc_id, t.service_type, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'truck', 'ref', t.truck_license_plate_snapshot, 'rowId', t.truck_id, 'tenantId', k.tenant_id))
		FROM maintenance_records t LEFT JOIN trucks k ON k.id = t.truck_id`,
	"truck_assignments": `SELECT t.id, t.legacy_doc_id, t.status, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'truck', 'rowId', t.truck_id, 'tenantId', k.tenant_id))
		FROM truck_assignments t LEFT JOIN trucks k ON k.id = t.truck_id`,
	"driver_penalties": driverChain("driver_penalties", "t.type_code"),
	"payroll_runs":     driverChain("payroll_runs", "t.pay_period || ' ' || t.pay_round"),
	"leave_requests":   driverChain("leave_requests", "t.leave_type"),
	"chats":            driverChain("chats", "t.status"),
	// A renewal entry of a truck that did not resolve or is itself quarantined (a payout always lands on the billing
	// carrier). The ledger is immutable: listed for the review, never re-homed (409 immutable_ledger); the raw truckId
	// stays in the ETL's truck_unresolved finding, since the row keeps no legacy truck reference.
	"transactions": `SELECT t.id, t.legacy_doc_id, t.tx_type || coalesce(' ' || t.sub_type, ''), t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'truck', 'rowId', t.truck_id, 'tenantId', k.tenant_id))
		FROM transactions t LEFT JOIN trucks k ON k.id = t.truck_id`,
}

func driverChain(table, label string) string {
	return `SELECT t.id, t.legacy_doc_id, ` + label + `, t.created_at, jsonb_build_array(
		  jsonb_build_object('link', 'driver', 'ref', t.legacy_driver_ref, 'match', t.driver_ref_match, 'rowId', t.driver_id, 'tenantId', d.tenant_id))
		FROM ` + table + ` t LEFT JOIN drivers d ON d.id = t.driver_id`
}

// QuarantineTables are the tables a quarantined row can live in, sorted: every table tenancy.Rehome supports plus the
// immutable transactions ledger, which is listed but never moved.
func QuarantineTables() []string {
	out := make([]string, 0, len(quarantineQueries))
	for t := range quarantineQueries {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// rehomable reports whether tenancy.Rehome can move rows of a listed table.
func rehomable(table string) bool {
	return slices.Contains(tenancy.RehomeTables(), table)
}

// ListQuarantineRows is GET /v1/tenants/quarantine/rows (?table&limit&cursor): the rows of the quarantine tenant,
// oldest first (keyset (created_at, id)), with their resolver trace; meta.byTable counts every table's rows.
func (a *Admin) ListQuarantineRows(c call, table string, limit int, cur string) ([]QuarantineRow, string, map[string]int, error) {
	var bad []httpx.FieldViolation
	tables := QuarantineTables()
	if table != "" {
		if _, ok := quarantineQueries[table]; !ok {
			bad = append(bad, violation("table", "one_of"))
		}
		tables = []string{table}
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var k cursor
	if cur != "" {
		var ok bool
		if k, ok = decodeCursor(cur, false); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", nil, errInvalid(bad...)
	}
	after := time.Time{}
	if k.T != nil {
		after = *k.T
	}
	quar := uuid.MustParse(tenancy.QuarantineTenantID)
	var out []QuarantineRow
	counts := map[string]int{}
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		for _, t := range tables {
			q := `SELECT * FROM (` + quarantineQueries[t] + ` WHERE t.tenant_id = app_quarantine_tenant_id()) x(id, legacy_doc_id, label, created_at, steps)
				WHERE (created_at, id) > ($1, $2) ORDER BY created_at, id LIMIT $3`
			rows, err := tx.Query(c.ctx, q, after, k.ID, limit+1)
			if err != nil {
				return fmt.Errorf("iam: quarantine rows of %s: %w", t, err)
			}
			for rows.Next() {
				var r QuarantineRow
				var steps []byte
				if err := rows.Scan(&r.ID, &r.LegacyDocID, &r.Label, &r.CreatedAt, &steps); err != nil {
					rows.Close()
					return err
				}
				r.Table = t
				if err := json.Unmarshal(steps, &r.Trace.Steps); err != nil {
					rows.Close()
					return fmt.Errorf("iam: quarantine trace of %s %s: %w", t, r.ID, err)
				}
				r.Trace.Outcome = "chain_exhausted"
				for i := range r.Trace.Steps {
					s := &r.Trace.Steps[i]
					r.Trace.Chain = append(r.Trace.Chain, s.Link)
					if s.TenantID != nil && *s.TenantID == quar {
						s.Quarantined = true
						if r.Trace.Outcome == "chain_exhausted" {
							r.Trace.Outcome = "follows_parent"
						}
					}
				}
				out = append(out, r)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			var n int
			// The table name is a key of quarantineQueries (checked above), never request data.
			if err := tx.QueryRow(c.ctx, `SELECT count(*) FROM `+pgx.Identifier{t}.Sanitize()+` WHERE tenant_id = app_quarantine_tenant_id()`).Scan(&n); err != nil {
				return fmt.Errorf("iam: quarantine count of %s: %w", t, err)
			}
			if n > 0 {
				counts[t] = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", nil, mapDBError(err)
	}
	slices.SortFunc(out, func(x, y QuarantineRow) int {
		if c := x.CreatedAt.Compare(y.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		t := last.CreatedAt
		next = encodeCursor(&t, last.ID)
	}
	if out == nil {
		out = []QuarantineRow{}
	}
	return out, next, counts, nil
}

// RehomeInput is the body of POST /v1/tenants/quarantine/rows/{table}/{id}/rehome.
type RehomeInput struct {
	TenantID *uuid.UUID `json:"tenantId"`
}

// RehomeQuarantineRow is POST /v1/tenants/quarantine/rows/{table}/{id}/rehome (platform:cross_tenant_write, a
// platform admin: no API key holds a platform key): tenancy.Rehome moves the row to tenantId (tenant_source 'form'), its children
// with the link they follow and the files they own, under app.tenant_move, and tenant_rehomed is appended in the
// same transaction (actor the platform admin; details {table, id, fromTenantId, toTenantId, moved[], via}, the shape
// `etl quarantine resolve --action=rehome` writes). 404: an unknown table or a row not in the quarantine tenant; 409
// failed_precondition immutable_ledger: a transactions row (listed for review only, the ledger is never moved); 422:
// tenantId missing, unknown or the quarantine tenant; 409 failed_precondition: the row cannot hold outside the
// quarantine tenant (its driver or truck did not resolve: needs_reference) or a link it keeps is still quarantined
// (re-home the parent task or trip first: linked_row_quarantined).
func (a *Admin) RehomeQuarantineRow(c call, table string, id uuid.UUID, in RehomeInput) ([]tenancy.Moved, error) {
	if _, ok := quarantineQueries[table]; !ok {
		return nil, httpx.ErrNotFound()
	}
	if !rehomable(table) {
		// The append-only ledger (transactions_immutable): a quarantined entry is corrected by a new entry on the right
		// tenant, never moved. Nothing is written; a row outside the quarantine tenant is 404 as for every table.
		var found bool
		err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
			// The table name is a key of quarantineQueries (checked above), never request data.
			return tx.QueryRow(c.ctx, `SELECT EXISTS (SELECT 1 FROM `+pgx.Identifier{table}.Sanitize()+
				` WHERE id = $1 AND tenant_id = app_quarantine_tenant_id())`, id).Scan(&found)
		})
		switch {
		case err != nil:
			return nil, mapDBError(err)
		case !found:
			return nil, httpx.ErrNotFound()
		}
		return nil, errFailedPrecondition("immutable_ledger", "ledger entries are immutable and never re-homed; record a correcting entry on the right tenant")
	}
	if in.TenantID == nil || *in.TenantID == uuid.Nil {
		return nil, errInvalid(violation("tenantId", "required"))
	}
	to := *in.TenantID
	var moved []tenancy.Moved
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		var err error
		moved, err = tenancy.Rehome(c.ctx, tx, table, id, to)
		switch {
		case errors.Is(err, tenancy.ErrNotQuarantined):
			return httpx.ErrNotFound()
		case errors.Is(err, tenancy.ErrBadTarget):
			return errInvalid(violation("tenantId", "not_found"))
		case err != nil:
			return err
		}
		list := make([]map[string]string, 0, len(moved))
		for _, m := range moved {
			list = append(list, map[string]string{"table": m.Table, "id": m.ID.String()})
		}
		if err := a.audit(c, tx, EventTenantRehomed,
			fmt.Sprintf("%s %s re-homed from the quarantine tenant with %d moved row(s)", table, id, len(moved)), nil, &to,
			map[string]any{"table": table, "id": id.String(), "fromTenantId": tenancy.QuarantineTenantID, "toTenantId": to.String(),
				"moved": list, "via": "POST /v1/tenants/quarantine/rows/{table}/{id}/rehome"}); err != nil {
			return err
		}
		// The deferred tenant-consistency triggers check the move before the answer, so a refusal is a 409.
		_, err = tx.Exec(c.ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
		return err
	})
	if err != nil {
		return nil, rehomeError(err)
	}
	return moved, nil
}

// rehomeError turns the constraint a move cannot satisfy into 409 failed_precondition.
func rehomeError(err error) error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return err
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23514": // a CHECK: outside the quarantine tenant the row needs its driver or truck
			return errFailedPrecondition("needs_reference", "the row has no driver or truck and cannot leave the quarantine tenant; fix it at source and retry the document").Wrap(err)
		case "23000": // tenant consistency: a linked task or trip is still quarantined
			return errFailedPrecondition("linked_row_quarantined", "a task or trip this row links is still in the quarantine tenant; re-home it first").Wrap(err)
		}
	}
	return mapDBError(err)
}

// --- HTTP ---------------------------------------------------------------------------------------------------

func (h adminHTTP) listQuarantineRows(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "table", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, counts, err := h.a.ListQuarantineRows(cl, c.Query("table"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, map[string]any{"byTable": counts})
}

func (h adminHTTP) rehomeQuarantineRow(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in RehomeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if _, err := h.a.RehomeQuarantineRow(cl, c.Params("table"), id, in); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}
