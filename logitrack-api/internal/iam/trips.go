package iam

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// EventTripRecordRenamed is the security event of a trip rename (renameTripRecord.ts appended it after its batch;
// Appendix C §C.4.13 writes it in the rename's transaction).
const EventTripRecordRenamed = "trip_record_renamed"

// RouteTripUpdated is the outbox event of a rename (a trip.updated with previousTripNo, as the mirror emits it, T29).
const RouteTripUpdated = "trip.updated"

// TripRenamerDeps are the collaborators of the trip rename.
type TripRenamerDeps struct {
	Pool db.Beginner
	Log  zerolog.Logger
	Now  func() time.Time // optional, defaults to time.Now
}

// TripRenamer renames trips for POST /v1/trips/{id}/rename (T31, operations.TripRenamer): security_events is written
// in a system transaction only (Appendix C §C.4.13, the WithSystem allow-list), so the rename and its audit row run
// in one system transaction after operations.Service checked that the caller's RLS lets it update the trip.
type TripRenamer struct {
	pool db.Beginner
	log  zerolog.Logger
	now  func() time.Time
}

// NewTripRenamer builds the rename.
func NewTripRenamer(d TripRenamerDeps) (*TripRenamer, error) {
	if d.Pool == nil {
		return nil, errors.New("iam: the trip rename needs a pool")
	}
	r := &TripRenamer{pool: d.Pool, log: d.Log, now: d.Now}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

var _ operations.TripRenamer = (*TripRenamer)(nil)

// RenameTrip renames trip tripID to newNo in one system transaction: the trip is locked again and must still be in
// the caller's write reach (404 otherwise; a quarantined trip is 404) and delivered (operations.RenameTrip: 409
// failed_precondition not_delivered, 409 duplicate_trip_id, 422 tripNo), then one trip_no_history row,
// trip_record_renamed (actor the caller, tenant the trip's) and trip.updated with previousTripNo on the trips topics
// of its tenant and parties, all committing together.
func (r *TripRenamer) RenameTrip(ctx context.Context, p *authz.Principal, rid string, tripID uuid.UUID, newNo string) (operations.Trip, operations.TripRename, error) {
	var trip operations.Trip
	var hist operations.TripRename
	err := db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := q.LockTrip(ctx, tripID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.TenantID == uuid.MustParse(tenancy.QuarantineTenantID) || !tripWriteReach(p, cur.TenantID) {
			return httpx.ErrNotFound()
		}
		by := p.UserID
		at := r.now().UTC().Truncate(time.Microsecond)
		if trip, hist, err = operations.RenameTrip(ctx, tx, tripID, newNo, &by, at); err != nil {
			return err
		}
		n, err := q.IncidentsOfTrip(ctx, operationsdb.IncidentsOfTripParams{TripID: tripID, AllTenants: true, TenantIds: []uuid.UUID{}})
		if err != nil {
			return err
		}
		if err := r.audit(ctx, tx, p, rid, trip, hist.OldTripNo, hist.NewTripNo, len(n), at); err != nil {
			return err
		}
		parties, err := q.TripParties(ctx, tripID)
		if err != nil {
			return err
		}
		topics := []string{realtime.TenantTopic(trip.TenantID.String(), realtime.FamilyTrips)}
		for _, party := range parties {
			topics = append(topics, realtime.DispatchTopic(party.String(), realtime.FamilyTrips))
		}
		payload := map[string]any{"entity": "trip", "id": trip.ID, "tenantId": trip.TenantID, "status": trip.Status,
			"tripNo": trip.TripNo, "previousTripNo": hist.OldTripNo, "taskId": trip.TaskID, "changed": []string{"tripNo"},
			"source": "api"}
		return emitTopics(call{ctx: ctx, p: p, rid: rid}, tx, RouteTripUpdated, "trip", trip.ID.String(), &trip.TenantID,
			payload, topics)
	})
	return trip, hist, err
}

// AuditRename appends trip_record_renamed for a rename the write-back made in Firestore (the batch commits first, as
// renameTripRecord.ts did; the mirror applies the rename itself).
func (r *TripRenamer) AuditRename(ctx context.Context, p *authz.Principal, rid string, trip operations.Trip, oldNo, newNo string, repointed int) error {
	at := r.now().UTC().Truncate(time.Microsecond)
	return db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
		return r.audit(ctx, tx, p, rid, trip, oldNo, newNo, repointed, at)
	})
}

func (r *TripRenamer) audit(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, trip operations.Trip,
	oldNo, newNo string, repointed int, at time.Time) error {
	actor, tenant := p.UserID, trip.TenantID
	return security.Append(ctx, tx, security.Event{EventType: EventTripRecordRenamed, Severity: security.SeverityWarning,
		Summary: "Trip " + oldNo + " renamed to " + newNo, Details: map[string]any{"tripId": trip.ID, "oldId": oldNo,
			"newId": newNo, "incidentReportsRepointed": repointed, "driverId": trip.DriverID, "taskId": trip.TaskID},
		ActorUserID: &actor, TenantID: &tenant, RequestID: rid, OccurredAt: at})
}

// tripWriteReach reports whether p writes rows of tenant (its staff tenant reach, or every tenant for the read-only bypass,
// which never reaches a write route).
func tripWriteReach(p *authz.Principal, tenant uuid.UUID) bool {
	all, _ := p.TenantReach()
	return all || inWriteReach(p, tenant)
}
