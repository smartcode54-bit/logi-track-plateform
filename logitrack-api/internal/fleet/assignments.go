package fleet

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Outbox routing keys of a truck assignment (Appendix B §B.5.2, family fleet): both reach tenant:{tid}:fleet of the
// truck's tenant (and of the driver's, when it differs) with the payload {entity: "truck_assignment", id, truckId,
// driverId}; firestore.shadow binds both.
const (
	RouteAssignmentCreated = "assignment.created"
	RouteAssignmentRevoked = "assignment.revoked"
)

// Assignment statuses (truck_assignments.status); cancelled exists in loaded legacy rows only.
var AssignmentStatuses = []string{AssignmentActive, AssignmentRevoked, "cancelled"}

// Assignment statuses written here.
const (
	AssignmentActive  = "active"
	AssignmentRevoked = "revoked"
)

// Driver statuses an assignment reads and writes (drivers.status): on_duty while the driver holds one; a revoke's
// move back to active is assignment_release_driver's (0013).
const (
	driverInactive = "inactive"
	driverOnDuty   = "on_duty"
)

// TruckAssignment is one truck_assignments row (GET /v1/truck-assignments and the answer of both writes). The truck
// and driver display columns are null when the caller cannot read that row; adminLabel is the legacy adminName.
type TruckAssignment struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenantId"`
	TruckID     uuid.UUID  `json:"truckId"`
	TruckPlate  *string    `json:"truckPlate"`
	TruckModel  *string    `json:"truckModel"`
	DriverID    uuid.UUID  `json:"driverId"`
	DriverName  *string    `json:"driverName"`
	Status      string     `json:"status"`
	AdminUserID *uuid.UUID `json:"adminUserId"`
	AdminLabel  string     `json:"adminLabel"`
	CreatedAt   time.Time  `json:"createdAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
}

func assignmentOf(r fleetdb.ListAssignmentsRow) TruckAssignment {
	out := TruckAssignment{ID: r.ID, TenantID: r.TenantID, TruckID: r.TruckID, TruckPlate: r.TruckPlate,
		TruckModel: r.TruckModel, DriverID: r.DriverID, Status: r.Status, AdminUserID: r.AdminUserID,
		AdminLabel: r.AdminLabel, CreatedAt: r.CreatedAt, RevokedAt: r.RevokedAt}
	if r.DriverName != "" {
		n := r.DriverName
		out.DriverName = &n
	}
	return out
}

// ListAssignmentsInput are the query parameters of GET /v1/truck-assignments.
type ListAssignmentsInput struct {
	Status, TruckID, DriverID string
	Limit                     int
	Cursor                    string
}

// ListAssignments is GET /v1/truck-assignments (fleet:view_assignments): the assignments of the caller's tenant
// reach, newest first, keyset (the history list of the truck-assignment page, W9; the legacy page read every
// document and kept 20).
func (s *Service) ListAssignments(ctx context.Context, p *authz.Principal, in ListAssignmentsInput) ([]TruckAssignment, string, error) {
	var ck checker
	var status *string
	if in.Status != "" {
		if !slices.Contains(AssignmentStatuses, in.Status) {
			ck.add("status", "invalid")
		}
		status = &in.Status
	}
	id := func(field, v string) *uuid.UUID {
		if v == "" {
			return nil
		}
		u, err := uuid.Parse(v)
		if err != nil {
			ck.add(field, "invalid")
		}
		return &u
	}
	truck, driver := id("truckId", in.TruckID), id("driverId", in.DriverID)
	limit := pageLimit(&ck, in.Limit)
	var after cursor
	if in.Cursor != "" {
		var ok bool
		if after, ok = decodeCursor(in.Cursor); !ok || after.T == nil {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, tenants := p.TenantReach()
	var rows []fleetdb.ListAssignmentsRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *fleetdb.Queries) (err error) {
		rows, err = q.ListAssignments(ctx, fleetdb.ListAssignmentsParams{AllTenants: all, TenantIds: tenants, Status: status,
			TruckID: truck, DriverID: driver, AfterCreated: after.T, AfterID: after.ID, RowLimit: int32(limit) + 1})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		t := last.CreatedAt
		next = cursor{T: &t, ID: last.ID}.encode()
	}
	out := make([]TruckAssignment, 0, len(rows))
	for _, r := range rows {
		out = append(out, assignmentOf(r))
	}
	return out, next, nil
}

// getAssignment reads one assignment of the caller's reach after a write.
func (s *Service) getAssignment(ctx context.Context, p *authz.Principal, id uuid.UUID) (*TruckAssignment, error) {
	all, tenants := p.TenantReach()
	var out TruckAssignment
	err := s.tx(ctx, p, func(_ pgx.Tx, q *fleetdb.Queries) error {
		r, err := q.GetAssignment(ctx, fleetdb.GetAssignmentParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		out = assignmentOf(fleetdb.ListAssignmentsRow(r))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// AssignInput is the body of POST /v1/truck-assignments.
type AssignInput struct {
	TruckID  string `json:"truckId"`
	DriverID string `json:"driverId"`
}

// emitAssignment appends an assignment event in tx on the fleet topic of every tenant it concerns.
func emitAssignment(ctx context.Context, tx pgx.Tx, routingKey string, id, truck, driver uuid.UUID, tenants []uuid.UUID, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	topics := make([]string, 0, len(tenants))
	for _, t := range tenants {
		if topic := realtime.TenantTopic(t.String(), realtime.FamilyFleet); !slices.Contains(topics, topic) {
			topics = append(topics, topic)
		}
	}
	tenant := tenants[0]
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: "truck_assignment", AggregateID: id.String(), TenantID: &tenant,
		Payload: map[string]any{"entity": "truck_assignment", "id": id.String(), "truckId": truck.String(),
			"driverId": driver.String()},
		Topics: topics, Headers: headers,
	})
	return err
}

// errAssignment is 409 failed_precondition with a reason (Appendix B §B.1.5) and its details.
func errAssignment(reason, msg string, details map[string]any) *httpx.Error {
	d := map[string]any{"reason": reason}
	for k, v := range details {
		d[k] = v
	}
	return httpx.NewError(http.StatusConflict, "failed_precondition", msg).WithDetails(d)
}

// statusEntry appends the driver's status_history entry of an assignment that changed its status.
func statusEntry(ctx context.Context, q *fleetdb.Queries, p *authz.Principal, driver uuid.UUID, status, previous, reason string, at time.Time) error {
	user, label := actor(p)
	return q.InsertAssignmentDriverStatus(ctx, fleetdb.InsertAssignmentDriverStatusParams{DriverID: driver, Status: status,
		PreviousStatus: &previous, ChangedAt: at, UserID: user, Label: label, Reason: &reason})
}

// CreateAssignment is POST /v1/truck-assignments (fleet:manage_assignments), the legacy createAssignment transaction
// (truck-assignment/actions.client.ts:70-135) in one PostgreSQL transaction: the driver is locked first (two
// assignments of one driver serialise), must not be inactive and must not hold an active assignment in any tenant
// (409 failed_precondition driver_inactive / driver_assigned; several drivers may share a truck), the truck must be in
// the caller's reach and in the driver's tenant or one linked to it by contractor_tenant_id (422 driverId
// not_in_truck_tenant); then the assignment row (tenant of the truck, tenant_source 'truck'), the driver's
// current_assignment_id and status on_duty (with its status_history entry) and outbox assignment.created. Any
// failure rolls all of it back. drivers.active_truck_* and tasks.truck_id are never touched.
func (s *Service) CreateAssignment(ctx context.Context, p *authz.Principal, rid string, in AssignInput) (*TruckAssignment, error) {
	if _, err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	parse := func(field, v string) uuid.UUID {
		v = strings.TrimSpace(v)
		if v == "" {
			ck.add(field, "required")
			return uuid.Nil
		}
		u, err := uuid.Parse(v)
		if err != nil || u == uuid.Nil {
			ck.add(field, "invalid")
		}
		return u
	}
	truckID, driverID := parse("truckId", in.TruckID), parse("driverId", in.DriverID)
	if err := ck.err(); err != nil {
		return nil, err
	}
	all, tenants := p.TenantReach()
	var id uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		d, err := q.LockAssignDriver(ctx, driverID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		if d.Status == driverInactive {
			return errAssignment("driver_inactive", "the driver is inactive", nil)
		}
		// The driver's active row decides, in whichever tenant it lies (DriverActiveAssignment does not read under the
		// caller's RLS: a carrier's staff cannot see the own fleet's row that holds their driver), not
		// current_assignment_id (it may still name a revoked row of loaded data, or miss a legacy active row).
		held, err := q.DriverActiveAssignment(ctx, driverID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			return errAssignment("driver_assigned", "the driver already holds a truck assignment",
				map[string]any{"assignmentId": held.String()})
		}
		t, err := q.AssignTruck(ctx, fleetdb.AssignTruckParams{ID: truckID, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("truckId", "not_found"))
		}
		if err != nil {
			return err
		}
		if d.TenantID != t.TenantID {
			linked, err := q.TenantsLinked(ctx, fleetdb.TenantsLinkedParams{A: d.TenantID, B: t.TenantID})
			if err != nil {
				return err
			}
			if !linked {
				return httpx.ErrInvalidArgument(violation("driverId", "not_in_truck_tenant"))
			}
		}
		now := s.now()
		user, label := actor(p)
		if id, err = q.InsertAssignment(ctx, fleetdb.InsertAssignmentParams{TenantID: t.TenantID, TruckID: truckID,
			DriverID: driverID, UserID: user, Label: label, At: now}); err != nil {
			return err
		}
		if err := q.SetDriverAssignment(ctx, fleetdb.SetDriverAssignmentParams{AssignmentID: &id, Status: driverOnDuty,
			ID: driverID}); err != nil {
			return err
		}
		if d.Status != driverOnDuty {
			if err := statusEntry(ctx, q, p, driverID, driverOnDuty, d.Status, "truck assigned: "+t.LicensePlate, now); err != nil {
				return err
			}
		}
		return emitAssignment(ctx, tx, RouteAssignmentCreated, id, truckID, driverID, []uuid.UUID{t.TenantID, d.TenantID}, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.getAssignment(ctx, p, id)
}

// RevokeAssignment is POST /v1/truck-assignments/{id}/revoke (fleet:manage_assignments), the legacy
// terminateAssignment transaction (actions.client.ts:151-200) in one PostgreSQL transaction: an active assignment of
// the caller's reach (404 outside it, 409 failed_precondition not_active otherwise) becomes revoked, and the driver is
// released in the same transaction whether or not the caller can read it (ReleaseAssignmentDriver: a carrier may
// revoke the row of its truck that binds an own-fleet driver). When the driver's current_assignment_id names this
// row, a row that is no longer active, or nothing, it moves to the driver's other active assignment in any tenant
// (legacy rows) or clears, and an on_duty driver left without one becomes active (status_history entry); a pointer at
// another active assignment stays. Outbox assignment.revoked goes to the fleet topics of the row's tenant and the
// driver's. Unlike the legacy code it never reactivates an inactive driver.
func (s *Service) RevokeAssignment(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) (*TruckAssignment, error) {
	if _, err := writeTenant(p); err != nil {
		return nil, err
	}
	err := s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		a, err := q.LockAssignment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if a.Status != AssignmentActive {
			return errAssignment("not_active", "the assignment is not active", map[string]any{"status": a.Status})
		}
		now := s.now()
		if err := q.RevokeAssignment(ctx, fleetdb.RevokeAssignmentParams{At: now, ID: id}); err != nil {
			return err
		}
		user, label := actor(p)
		driverTenant, err := q.ReleaseAssignmentDriver(ctx, fleetdb.ReleaseAssignmentDriverParams{AssignmentID: id,
			UserID: user, Label: label, Reason: "truck assignment revoked", At: now})
		if err != nil {
			return err
		}
		tenants := []uuid.UUID{a.TenantID, driverTenant}
		return emitAssignment(ctx, tx, RouteAssignmentRevoked, id, a.TruckID, a.DriverID, tenants, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.getAssignment(ctx, p, id)
}
