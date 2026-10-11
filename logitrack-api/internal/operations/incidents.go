package operations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
)

// IncidentInsert are the columns of a new incident report (operationsdb.InsertIncidentParams).
type IncidentInsert = operationsdb.InsertIncidentParams

// CreateIncident inserts an incident report in the caller's transaction (the driver's report, the admin's report).
// A report with a client_op_id is idempotent per (driver_id, client_op_id) (R63): a replay returns the stored report
// with replayed = true. The deferred incident_reports_tenant_check keeps it on its trip's tenant.
func CreateIncident(ctx context.Context, tx pgx.Tx, in IncidentInsert) (inc Incident, replayed bool, err error) {
	var ck checker
	if !slices.Contains(TenantSources, in.TenantSource) {
		ck.add("tenantSource", "invalid")
	}
	if in.TenantID == uuid.Nil {
		ck.add("tenantId", "required")
	}
	if !slices.Contains(ReportedByKinds, in.ReportedByKind) {
		ck.add("reportedByKind", "invalid")
	}
	if in.DelayCause != nil && strings.TrimSpace(*in.DelayCause) == "" {
		ck.add("delayCause", "invalid")
	}
	if err := ck.err(); err != nil {
		return Incident{}, false, err
	}
	q := operationsdb.New(tx)
	if in.ClientOpID != nil && in.DriverID != nil {
		r, err := q.IncidentByClientOp(ctx, operationsdb.IncidentByClientOpParams{DriverID: *in.DriverID, ClientOpID: *in.ClientOpID})
		switch {
		case err == nil:
			return r, true, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return Incident{}, false, MapError(err)
		}
	}
	inc, err = q.InsertIncident(ctx, in)
	if errors.Is(err, pgx.ErrNoRows) && in.ClientOpID != nil && in.DriverID != nil {
		r, err := q.IncidentByClientOp(ctx, operationsdb.IncidentByClientOpParams{DriverID: *in.DriverID, ClientOpID: *in.ClientOpID})
		return r, err == nil, MapError(err)
	}
	return inc, false, MapError(err)
}

// IncidentFilter is the query of GET /v1/incidents: newest first; From / To bound created_at, [From, To).
type IncidentFilter struct {
	From, To *time.Time
	TripID   *uuid.UUID
	DriverID *uuid.UUID
	Limit    int
	Cursor   string
}

// ListIncidents is a keyset page of the principal's staff reach (customer scope: scope_incidents, T30).
func (s *Service) ListIncidents(ctx context.Context, p *authz.Principal, f IncidentFilter) (Page[Incident], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	c, ok := decodeCursor(f.Cursor, axisCreated)
	if !ok {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[Incident]{}, err
	}
	r := ReachOf(p)
	var rows []Incident
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		rows, err = q.ListIncidents(ctx, operationsdb.ListIncidentsParams{AllTenants: r.All, TenantIds: r.tenants(),
			FromAt: f.From, ToAt: f.To, TripID: f.TripID, DriverID: f.DriverID, AfterCreated: c.at(), AfterID: c.id(),
			RowLimit: int32(limit + 1)})
		return err
	})
	if err != nil {
		return Page[Incident]{}, err
	}
	return page(rows, limit, func(i Incident) cursor {
		return cursor{Axis: axisCreated, At: ptr(i.CreatedAt), ID: i.ID}
	}), nil
}
