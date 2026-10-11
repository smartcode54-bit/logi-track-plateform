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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// TripInsert are the columns of a new trip (operationsdb.InsertTripParams).
type TripInsert = operationsdb.InsertTripParams

// NewTrip is a trip to create with its stop plan (multi-drop: the stops whose progress the driver reports) and the
// photos of its loading.
type NewTrip struct {
	TripInsert
	Stops  []NewTripStop
	Photos []PhotoInput
}

// NewTripStop is one stop of a trip's progress (trip_delivery_stops), pending until delivered.
type NewTripStop struct {
	Index            int32
	Destination      string
	DestinationHubID *uuid.UUID
	SourceID         *string
}

// PhotoInput is one evidence photo of a trip: its type (any non-empty text, R19), the committed file and the
// geocoding of the overlay.
type PhotoInput struct {
	Type           string
	FileID         uuid.UUID
	StopID         *uuid.UUID
	GeocodeLat     *float64
	GeocodeLng     *float64
	GeocodeAddress *string
	GeocodedAt     *time.Time
}

// CreateTrip inserts a trip in the caller's transaction with its stop plan and photos. The trip number is trimmed and
// must be a valid Firestore document id (ValidateTripNo, 422 tripNo); a number another trip holds is 409
// duplicate_trip_id (trip_records_trip_no). The tenant is the caller's stamping (T28: the task's, else the driver's);
// the deferred trip_records_tenant_check refuses at COMMIT a trip off its task's tenant.
func CreateTrip(ctx context.Context, tx pgx.Tx, in NewTrip) (Trip, error) {
	in.TripNo = strings.TrimSpace(in.TripNo)
	var ck checker
	if r := ValidateTripNo(in.TripNo); r != "" {
		ck.add("tripNo", r)
	}
	if !slices.Contains(TripStatuses, in.Status) {
		ck.add("status", "invalid")
	}
	if !slices.Contains(TaskTypes, in.JobType) {
		ck.add("jobType", "invalid")
	}
	if !slices.Contains(TenantSources, in.TenantSource) {
		ck.add("tenantSource", "invalid")
	}
	if in.TenantID == uuid.Nil {
		ck.add("tenantId", "required")
	}
	if in.JobCategory != nil && !slices.Contains(JobCategories, *in.JobCategory) {
		ck.add("jobCategory", "invalid")
	}
	if in.VehicleClass != nil && !slices.Contains(VehicleClasses, *in.VehicleClass) {
		ck.add("vehicleClass", "invalid")
	}
	for _, ph := range in.Photos {
		if strings.TrimSpace(ph.Type) == "" {
			ck.add("photos.type", "required")
			break
		}
	}
	if err := ck.err(); err != nil {
		return Trip{}, err
	}
	if len(in.Stops) > 0 {
		in.IsMultiDelivery = true
	}
	q := operationsdb.New(tx)
	trip, err := q.InsertTrip(ctx, in.TripInsert)
	if err != nil {
		return Trip{}, tripNoError(err, in.TripNo)
	}
	for _, st := range in.Stops {
		if _, err := q.InsertTripStop(ctx, operationsdb.InsertTripStopParams{TripID: trip.ID, StopIndex: st.Index,
			DestinationRaw: strings.TrimSpace(st.Destination), DestinationHubID: st.DestinationHubID,
			SourceID: st.SourceID}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Trip{}, MapError(err)
		}
	}
	for _, ph := range in.Photos {
		if _, err := PutTripPhoto(ctx, tx, trip.ID, ph); err != nil {
			return Trip{}, err
		}
	}
	return trip, nil
}

// tripNoError maps a taken trip number to 409 duplicate_trip_id with the number, anything else through MapError.
func tripNoError(err error, tripNo string) error {
	err = MapError(err)
	var he *httpx.Error
	if errors.As(err, &he) && he.Code == CodeDuplicateTripID {
		return ErrDuplicateTripNo(tripNo).Wrap(err)
	}
	return err
}

// RenameTrip changes a trip's number in place (POST /v1/trips/{id}/rename; renameTripRecord.ts:51 copied and deleted
// the document): the trip is locked, newNo is trimmed and validated (ValidateTripNo; equal to the current number is
// 422 tripNo unchanged), only a delivered trip is renamed (409 failed_precondition not_delivered: a running trip also
// lives in the driver's app, renameTripRecord.ts:94-101), a number another trip holds is 409 duplicate_trip_id, and one
// trip_no_history row records the change (append-only). Photos, stops and incidents keep the trip id, so nothing is
// re-pointed and no object moves.
func RenameTrip(ctx context.Context, tx pgx.Tx, tripID uuid.UUID, newNo string, by *uuid.UUID, at time.Time) (Trip, TripRename, error) {
	newNo = strings.TrimSpace(newNo)
	if r := ValidateTripNo(newNo); r != "" {
		return Trip{}, TripRename{}, httpx.ErrInvalidArgument(violation("tripNo", r))
	}
	q := operationsdb.New(tx)
	cur, err := q.LockTrip(ctx, tripID)
	if err != nil {
		return Trip{}, TripRename{}, MapError(notFound(err))
	}
	switch {
	case cur.TripNo == newNo:
		return Trip{}, TripRename{}, httpx.ErrInvalidArgument(violation("tripNo", "unchanged"))
	case cur.Status != TripDelivered:
		return Trip{}, TripRename{}, errPrecondition("not_delivered", "only a delivered trip can be renamed").
			WithDetails(map[string]any{"status": cur.Status})
	}
	trip, err := q.RenameTripNo(ctx, operationsdb.RenameTripNoParams{ID: tripID, NewTripNo: newNo})
	if err != nil {
		return Trip{}, TripRename{}, tripNoError(err, newNo)
	}
	h, err := q.InsertTripNoHistory(ctx, operationsdb.InsertTripNoHistoryParams{TripID: tripID, OldTripNo: cur.TripNo,
		NewTripNo: newNo, RenamedBy: by, RenamedAt: &at})
	if err != nil {
		return Trip{}, TripRename{}, MapError(err)
	}
	return trip, h, nil
}

// PutTripPhoto stores one evidence photo of a trip, merged by type (last wins, the type moves to the end of the
// stored order). Any non-empty type is accepted (R19): a workflow type or stop_{n}_{subtype} is photo_type_known, an
// unknown one is stored with photo_type_known = false and listed by UnknownTripPhotos.
func PutTripPhoto(ctx context.Context, tx pgx.Tx, tripID uuid.UUID, ph PhotoInput) (TripPhoto, error) {
	typ := strings.TrimSpace(ph.Type)
	var ck checker
	if typ == "" {
		ck.add("photos.type", "required")
	}
	if ph.FileID == uuid.Nil {
		ck.add("photos.fileId", "required")
	}
	if err := ck.err(); err != nil {
		return TripPhoto{}, err
	}
	row, err := operationsdb.New(tx).UpsertTripPhoto(ctx, operationsdb.UpsertTripPhotoParams{TripID: tripID,
		StopID: ph.StopID, PhotoType: typ, FileID: ph.FileID, GeocodeLat: ph.GeocodeLat, GeocodeLng: ph.GeocodeLng,
		GeocodeAddress: ph.GeocodeAddress, GeocodedAt: ph.GeocodedAt})
	return row, MapError(err)
}

// --- reads ----------------------------------------------------------------------------------------------------

// TripDetail is a trip with its stop progress and its photos in workflow order (ADR 0018).
type TripDetail struct {
	Trip    Trip
	Stops   []TripStop
	Photos  []TripPhoto
	Renames []TripRename // staff only (trip_no_history is p_parent_read read_staff_only): empty for a driver
}

// GetTrip reads one trip of the principal's reach (staff, or the trip's driver); outside it 404.
func (s *Service) GetTrip(ctx context.Context, p *authz.Principal, id uuid.UUID) (TripDetail, error) {
	r := ReachOf(p)
	var out TripDetail
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		t, err := q.GetTrip(ctx, operationsdb.GetTripParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return notFound(err)
		}
		return tripDetail(ctx, q, t, &out)
	})
	return out, err
}

func tripDetail(ctx context.Context, q *operationsdb.Queries, t Trip, out *TripDetail) error {
	out.Trip = t
	var err error
	if out.Stops, err = q.TripStops(ctx, t.ID); err != nil {
		return err
	}
	if out.Photos, err = q.TripPhotos(ctx, t.ID); err != nil {
		return err
	}
	SortPhotos(out.Photos)
	out.Renames, err = q.TripNoHistory(ctx, t.ID)
	return err
}

// TripByNo reads the trip that holds tripNo now or, when none does, the trip most recently renamed away from it
// (renamed = true): an incident or a LINE card that names a pre-rename number still finds its trip.
func (s *Service) TripByNo(ctx context.Context, p *authz.Principal, tripNo string) (detail TripDetail, renamed bool, err error) {
	r := ReachOf(p)
	err = s.read(ctx, p, func(q *operationsdb.Queries) error {
		t, err := q.TripByNo(ctx, operationsdb.TripByNoParams{TripNo: tripNo, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
		if errors.Is(err, pgx.ErrNoRows) {
			renamed = true
			t, err = q.TripByOldNo(ctx, operationsdb.TripByOldNoParams{TripNo: tripNo, AllTenants: r.All,
				TenantIds: r.tenants(), ReachDriver: r.Driver})
		}
		if err != nil {
			return notFound(err)
		}
		return tripDetail(ctx, q, t, &detail)
	})
	return detail, renamed, err
}

// Trip list axes (GET /v1/trips?axis=).
const (
	AxisCreated   = "created"
	AxisDelivered = "delivered"
	AxisBilling   = "billing"
)

// TripFilter is the query of GET /v1/trips. On the created axis From / To bound created_at; on the delivered axis
// (delivered trips) delivered_at; both [From, To). On the billing axis (delivered trips) FromDay / ToDay are Bangkok
// days, both inclusive.
type TripFilter struct {
	Axis           string
	From, To       *time.Time
	FromDay, ToDay *time.Time
	Statuses       []string // created axis only
	DriverID       *uuid.UUID
	BillingPartyID *uuid.UUID
	TruckID        *uuid.UUID
	Limit          int
	Cursor         string
}

// ListTrips is a keyset page of the principal's staff reach on one axis.
func (s *Service) ListTrips(ctx context.Context, p *authz.Principal, f TripFilter) (Page[Trip], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	axis := map[string]string{"": axisCreated, AxisCreated: axisCreated, AxisDelivered: axisDelivered, AxisBilling: axisBilling}[f.Axis]
	if axis == "" {
		ck.add("axis", "invalid")
	}
	for _, st := range f.Statuses {
		if !slices.Contains(TripStatuses, st) {
			ck.add("status", "invalid")
			break
		}
	}
	if len(f.Statuses) > 0 && axis != axisCreated {
		ck.add("status", "not_on_axis") // the delivered and billing axes list delivered trips
	}
	c, ok := decodeCursor(f.Cursor, axis)
	if !ok && axis != "" {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[Trip]{}, err
	}
	r := ReachOf(p)
	var rows []Trip
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		switch axis {
		case axisDelivered:
			rows, err = q.ListTripsByDelivered(ctx, operationsdb.ListTripsByDeliveredParams{AllTenants: r.All,
				TenantIds: r.tenants(), FromAt: f.From, ToAt: f.To, DriverID: f.DriverID, BillingPartyID: f.BillingPartyID,
				TruckID: f.TruckID, AfterDelivered: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
		case axisBilling:
			rows, err = q.ListTripsByBillingAxis(ctx, operationsdb.ListTripsByBillingAxisParams{AllTenants: r.All,
				TenantIds: r.tenants(), FromDate: f.FromDay, ToDate: f.ToDay, DriverID: f.DriverID,
				BillingPartyID: f.BillingPartyID, TruckID: f.TruckID, AfterAxis: c.day(), AfterID: c.id(),
				RowLimit: int32(limit + 1)})
		default:
			rows, err = q.ListTripsByCreated(ctx, operationsdb.ListTripsByCreatedParams{AllTenants: r.All,
				TenantIds: r.tenants(), FromAt: f.From, ToAt: f.To, Statuses: f.Statuses, DriverID: f.DriverID,
				BillingPartyID: f.BillingPartyID, TruckID: f.TruckID, AfterCreated: c.at(), AfterID: c.id(),
				RowLimit: int32(limit + 1)})
		}
		return err
	})
	if err != nil {
		return Page[Trip]{}, err
	}
	return page(rows, limit, func(t Trip) cursor {
		switch axis {
		case axisDelivered:
			return cursor{Axis: axis, At: t.DeliveredAt, ID: t.ID}
		case axisBilling:
			return cursor{Axis: axis, Day: t.BillingAxisDate.Format(time.DateOnly), ID: t.ID}
		}
		return cursor{Axis: axis, At: ptr(t.CreatedAt), ID: t.ID}
	}), nil
}
