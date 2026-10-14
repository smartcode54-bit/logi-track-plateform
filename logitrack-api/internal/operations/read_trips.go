package operations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
)

// --- payloads -------------------------------------------------------------------------------------------------

// TripBase is a trip as every reader may see it: the columns of the scope_trips projection (Appendix C §C.3.7) with the
// driver's name and the resolved places. It is the whole payload of a dispatcher or a customer-scope principal.
type TripBase struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenantId"`
	TripNo            string     `json:"tripNo"`
	Status            string     `json:"status"`
	JobType           string     `json:"jobType"`
	JobCategory       *string    `json:"jobCategory"`
	TaskID            *uuid.UUID `json:"taskId"`
	DriverID          *uuid.UUID `json:"driverId"`
	DriverName        *string    `json:"driverName"`
	Origin            *string    `json:"origin"`
	OriginHubID       *uuid.UUID `json:"originHubId"`
	OriginPlace       Place      `json:"originPlace"`
	Destination       *string    `json:"destination"`
	DestinationHubID  *uuid.UUID `json:"destinationHubId"`
	DestinationSocKey *string    `json:"destinationSocKey"`
	DestinationPlace  Place      `json:"destinationPlace"`
	SealCode          *string    `json:"sealCode"`
	PartnerCode       *string    `json:"partnerCode"`
	TruckID           *uuid.UUID `json:"truckId"`
	TruckLicensePlate *string    `json:"truckLicensePlate"`
	VehicleClass      *string    `json:"vehicleClass"`
	ParcelCount       *int32     `json:"parcelCount"`
	TotalWeightKg     *float64   `json:"totalWeightKg"`
	LoadingLat        *float64   `json:"loadingLat"`
	LoadingLng        *float64   `json:"loadingLng"`
	Std               *time.Time `json:"std"`
	Sta               *time.Time `json:"sta"`
	Ata               *time.Time `json:"ata"`
	DeliveredAt       *time.Time `json:"deliveredAt"`
	DeliveredLat      *float64   `json:"deliveredLat"`
	DeliveredLng      *float64   `json:"deliveredLng"`
	IsMultiDelivery   bool       `json:"isMultiDelivery"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// TripView is a trip as tenant staff read it: TripBase plus the stamping, the raw truck type, the review state, the OCR
// record and, with accounting:view_income only, the billing block (Appendix B §B.2.12: never for a dispatcher).
type TripView struct {
	TripBase
	TenantSource     string          `json:"tenantSource"`
	TruckType        *string         `json:"truckType"`
	DistanceKm       *float64        `json:"distanceKm"`
	SealTime         *time.Time      `json:"sealTime"`
	DurationMinutes  *float64        `json:"durationMinutes"`
	DeliveredVia     *string         `json:"deliveredVia"`
	NeedsAdminReview bool            `json:"needsAdminReview"`
	ReviewStatus     *string         `json:"reviewStatus"`
	ReviewReason     *string         `json:"reviewReason"`
	ResubmittedAt    *time.Time      `json:"resubmittedAt"`
	OcrData          json.RawMessage `json:"ocrData"`
	Billing          *TripBilling    `json:"billing,omitempty"`
}

// TripBilling is the billing block of a trip (accounting:view_income, never a dispatcher): billing.TripBillingOf's
// projection (Appendix B §B.2.14 as built, T39: the stamped party, the billed customer, the billing day, the stored
// snapshot with its provenance, base rate, lookup codes, fuel round, multiplier, add, band and multi-drop stops; the
// Income page and its exports read it from GET /v1/trips?axis=delivered) plus the trip's billing axis day (ADR 0027).
type TripBilling struct {
	billing.TripBilling
	BillingAxisDate *string `json:"billingAxisDate"`
}

// PricePreview is the stored price of a trip as the monitor row carries it: a subset of billing.TripBillingOf's
// projection (the one billing projection of a trip, F59; GET /v1/trips and the trip detail carry the whole of it),
// written by the billing service only (main spec §6.10), never computed on read.
type PricePreview struct {
	EstimateThb          *float64  `json:"estimateThb"`
	UnpricedReason       *string   `json:"unpricedReason"`
	ManualOverride       bool      `json:"manualOverride"`
	JobCategoryAtPricing *string   `json:"jobCategoryAtPricing"`
	IsMultiDelivery      bool      `json:"isMultiDelivery"`
	ComputedAt           time.Time `json:"computedAt"`
}

// pricePreview is the monitor's view of a trip's billing projection: nil before pricing (no snapshot the caller may
// read, R61).
func pricePreview(b billing.TripBilling) *PricePreview {
	if b.ComputedAt == nil {
		return nil
	}
	return &PricePreview{EstimateThb: b.EstimateTHB, UnpricedReason: b.UnpricedReason, ManualOverride: b.ManualOverride,
		JobCategoryAtPricing: b.JobCategoryAtPricing, IsMultiDelivery: b.IsMultiDelivery, ComputedAt: *b.ComputedAt}
}

// TripStopRow is one stop of a trip's progress (multi-drop).
type TripStopRow struct {
	ID               uuid.UUID  `json:"id"`
	Index            int32      `json:"index"`
	Destination      string     `json:"destination"`
	DestinationHubID *uuid.UUID `json:"destinationHubId"`
	Place            Place      `json:"place"`
	SourceID         *string    `json:"sourceId"`
	Status           string     `json:"status"`
	DeliveredAt      *time.Time `json:"deliveredAt"`
	DeliveredLat     *float64   `json:"deliveredLat"`
	DeliveredLng     *float64   `json:"deliveredLng"`
	CompletedSeq     *int32     `json:"completedSeq"`
}

// TripRenameRow is one rename of a trip (trip_no_history; staff only).
type TripRenameRow struct {
	OldTripNo string     `json:"oldTripNo"`
	NewTripNo string     `json:"newTripNo"`
	RenamedBy *uuid.UUID `json:"renamedBy"`
	RenamedAt time.Time  `json:"renamedAt"`
}

// TripDetailView is GET /v1/trips/{id} for staff: the trip with its stop progress, its photos in workflow order
// (signed URLs) and its renames. MatchedPreviousNo marks a by-number lookup answered by a pre-rename number.
type TripDetailView struct {
	TripView
	Stops             []TripStopRow   `json:"stops"`
	Photos            []Photo         `json:"photos"`
	Renames           []TripRenameRow `json:"renames"`
	MatchedPreviousNo bool            `json:"matchedPreviousNo,omitempty"`
}

// TripScopeDetail is GET /v1/trips/{id} for a dispatcher or a customer-scope principal: the projection with its stops
// and photos.
type TripScopeDetail struct {
	TripBase
	Stops  []TripStopRow `json:"stops"`
	Photos []Photo       `json:"photos"`
}

func (d directory) tripBase(t Trip) TripBase {
	return TripBase{ID: t.ID, TenantID: t.TenantID, TripNo: t.TripNo, Status: t.Status, JobType: t.JobType,
		JobCategory: t.JobCategory, TaskID: t.TaskID, DriverID: t.DriverID, Origin: t.OriginRaw, OriginHubID: t.OriginHubID,
		OriginPlace: d.place(t.OriginRaw, t.OriginHubID, nil), Destination: t.DestinationRaw,
		DestinationHubID: t.DestinationHubID, DestinationSocKey: t.DestinationSocKey,
		DestinationPlace: d.place(t.DestinationRaw, t.DestinationHubID, t.DestinationSocKey), SealCode: t.SealCode,
		PartnerCode: t.PartnerCode, TruckID: t.TruckID, TruckLicensePlate: t.TruckLicensePlateSnapshot,
		VehicleClass: t.VehicleClass, ParcelCount: t.ParcelCount, TotalWeightKg: t.TotalWeightKg, LoadingLat: t.LoadingLat,
		LoadingLng: t.LoadingLng, Std: t.Std, Sta: t.Sta, Ata: t.Ata, DeliveredAt: t.DeliveredAt,
		DeliveredLat: t.DeliveredLat, DeliveredLng: t.DeliveredLng, IsMultiDelivery: t.IsMultiDelivery,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

func (d directory) tripBaseScope(t scopedb.ScopeTrip) TripBase {
	return TripBase{ID: t.ID, TenantID: t.TenantID, TripNo: t.TripNo, Status: t.Status, JobType: t.JobType,
		JobCategory: t.JobCategory, TaskID: t.TaskID, DriverID: t.DriverID, Origin: t.OriginRaw, OriginHubID: t.OriginHubID,
		OriginPlace: d.place(t.OriginRaw, t.OriginHubID, nil), Destination: t.DestinationRaw,
		DestinationHubID: t.DestinationHubID, DestinationSocKey: t.DestinationSocKey,
		DestinationPlace: d.place(t.DestinationRaw, t.DestinationHubID, t.DestinationSocKey), SealCode: t.SealCode,
		PartnerCode: t.PartnerCode, TruckID: t.TruckID, TruckLicensePlate: t.TruckLicensePlateSnapshot,
		VehicleClass: t.VehicleClass, ParcelCount: t.ParcelCount, TotalWeightKg: t.TotalWeightKg, LoadingLat: t.LoadingLat,
		LoadingLng: t.LoadingLng, Std: t.Std, Sta: t.Sta, Ata: t.Ata, DeliveredAt: t.DeliveredAt,
		DeliveredLat: t.DeliveredLat, DeliveredLng: t.DeliveredLng, IsMultiDelivery: t.IsMultiDelivery,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// tripView is the staff payload; bill is the trip's billing projection when p may read it (viewIncome), else nil. A
// trip the projection did not return (no row under the caller's RLS) keeps its own party and day, unpriced.
func tripView(p *authz.Principal, b TripBase, t Trip, bill map[uuid.UUID]billing.TripBilling) TripView {
	v := TripView{TripBase: b, TenantSource: t.TenantSource, TruckType: t.TruckTypeRaw, DistanceKm: t.DistanceKm,
		SealTime: t.SealTime, DurationMinutes: t.DurationMinutes, DeliveredVia: t.DeliveredVia,
		NeedsAdminReview: t.NeedsAdminReview, ReviewStatus: t.ReviewStatus, ReviewReason: t.ReviewReason,
		ResubmittedAt: t.ResubmittedAt}
	if len(t.OcrData) > 0 && json.Valid(t.OcrData) {
		v.OcrData = json.RawMessage(t.OcrData)
	}
	if bill != nil && viewIncome(p) {
		tb, ok := bill[t.ID]
		if !ok {
			tb = billing.TripBilling{BillingPartyID: t.BillingPartyID, BillingDate: t.BillingDate, Stops: []billing.StopCharge{}}
		}
		v.Billing = &TripBilling{TripBilling: tb, BillingAxisDate: dayString(t.BillingAxisDate)}
	}
	return v
}

func (d directory) tripStop(s TripStop) TripStopRow {
	return TripStopRow{ID: s.ID, Index: s.StopIndex, Destination: s.DestinationRaw, DestinationHubID: s.DestinationHubID,
		Place: d.place(&s.DestinationRaw, s.DestinationHubID, nil), SourceID: s.SourceID, Status: s.Status,
		DeliveredAt: s.DeliveredAt, DeliveredLat: s.DeliveredLat, DeliveredLng: s.DeliveredLng, CompletedSeq: s.CompletedSeq}
}

func (d directory) tripStopScope(s scopedb.ScopeTripStop) TripStopRow {
	return TripStopRow{ID: s.ID, Index: s.StopIndex, Destination: s.DestinationRaw, DestinationHubID: s.DestinationHubID,
		Place: d.place(&s.DestinationRaw, s.DestinationHubID, nil), SourceID: s.SourceID, Status: s.Status,
		DeliveredAt: s.DeliveredAt, DeliveredLat: s.DeliveredLat, DeliveredLng: s.DeliveredLng, CompletedSeq: s.CompletedSeq}
}

// --- GET /v1/trips ----------------------------------------------------------------------------------------------

// TripListInput is the query of GET /v1/trips (Appendix B §B.2.12): axis created (default) | delivered | billing; from
// and to (created and delivered axes: a Bangkok day or an RFC 3339 instant, [from, to); billing axis: Bangkok days,
// both inclusive); status (created axis; repeatable or comma-separated); driverId, customerId (the billed party),
// truckId; limit, cursor. The Excel exports page through the same cursor.
type TripListInput struct {
	Axis, From, To                string
	Statuses                      []string
	DriverID, CustomerID, TruckID string
	Limit                         int
	Cursor                        string
}

// ListTripsView is GET /v1/trips: staff read their reach ([]TripView, the billing block with accounting:view_income),
// a dispatcher or a customer-scope principal the projection on the created or delivered axis ([]TripBase).
func (s *Service) ListTripsView(ctx context.Context, p *authz.Principal, in TripListInput) (any, string, error) {
	var ck checker
	axis := strings.TrimSpace(in.Axis)
	f := TripFilter{Axis: axis, Statuses: statusList(&ck, "status", in.Statuses, TripStatuses),
		DriverID: optUUID(&ck, "driverId", in.DriverID), BillingPartyID: optUUID(&ck, "customerId", in.CustomerID),
		TruckID: optUUID(&ck, "truckId", in.TruckID), Limit: in.Limit, Cursor: in.Cursor}
	if axis == AxisBilling {
		f.FromDay, f.ToDay = planDay(&ck, "from", in.From), planDay(&ck, "to", in.To)
		if f.FromDay != nil && f.ToDay != nil && f.ToDay.Before(*f.FromDay) {
			ck.add("to", "before_from")
		}
	} else {
		f.From, f.To = bound(&ck, "from", in.From, false), bound(&ck, "to", in.To, true)
		if f.From != nil && f.To != nil && !f.To.After(*f.From) {
			ck.add("to", "before_from")
		}
	}
	sc := scoped(p)
	if sc && axis == AxisBilling {
		notAvailable(&ck, "axis") // the billing axis is a billing column the projection does not carry
	}
	if sc && f.BillingPartyID != nil {
		notAvailable(&ck, "customerId")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return nil, "", err
	}
	if sc {
		pg, err := s.listScopeTrips(ctx, p, f)
		if err != nil {
			return nil, "", err
		}
		out := make([]TripBase, 0, len(pg.Items))
		var drivers []uuid.UUID
		for _, t := range pg.Items {
			drivers = appendID(drivers, t.DriverID)
		}
		names, err := s.names(ctx, p, drivers)
		if err != nil {
			return nil, "", err
		}
		for _, t := range pg.Items {
			b := d.tripBaseScope(t)
			b.DriverName = nameOf(names, b.DriverID)
			out = append(out, b)
		}
		return out, pg.NextCursor, nil
	}
	pg, err := s.ListTrips(ctx, p, f)
	if err != nil {
		return nil, "", err
	}
	out, err := s.staffTripViews(ctx, p, d, pg.Items)
	return out, pg.NextCursor, err
}

// staffTripViews completes a page of staff trips: names and, with accounting:view_income, the billing projection
// (billing.TripBillingOf in a request transaction of p: RLS decides which snapshots it reads, R61).
func (s *Service) staffTripViews(ctx context.Context, p *authz.Principal, d directory, trips []Trip) ([]TripView, error) {
	ids := make([]uuid.UUID, len(trips))
	var drivers []uuid.UUID
	for i, t := range trips {
		ids[i] = t.ID
		drivers = appendID(drivers, t.DriverID)
	}
	var bill map[uuid.UUID]billing.TripBilling
	if viewIncome(p) {
		if err := s.Tx(ctx, p, func(tx pgx.Tx) error {
			var err error
			bill, err = billing.TripBillingOf(ctx, tx, ids)
			return err
		}); err != nil {
			return nil, err
		}
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, err
	}
	out := make([]TripView, 0, len(trips))
	for _, t := range trips {
		b := d.tripBase(t)
		b.DriverName = nameOf(names, b.DriverID)
		out = append(out, tripView(p, b, t, bill))
	}
	return out, nil
}

// listScopeTrips is ListTrips over scope_trips on the created or delivered axis.
func (s *Service) listScopeTrips(ctx context.Context, p *authz.Principal, f TripFilter) (Page[scopedb.ScopeTrip], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	axis := map[string]string{"": axisCreated, AxisCreated: axisCreated, AxisDelivered: axisDelivered}[f.Axis]
	if axis == "" {
		ck.add("axis", "invalid")
	}
	if len(f.Statuses) > 0 && axis != axisCreated {
		ck.add("status", "not_on_axis")
	}
	c, ok := decodeCursor(f.Cursor, axis)
	if !ok && axis != "" {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[scopedb.ScopeTrip]{}, err
	}
	var rows []scopedb.ScopeTrip
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		if axis == axisDelivered {
			rows, err = q.ListScopeTripsByDelivered(ctx, scopedb.ListScopeTripsByDeliveredParams{FromAt: f.From, ToAt: f.To,
				DriverID: f.DriverID, TruckID: f.TruckID, AfterDelivered: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
			return err
		}
		rows, err = q.ListScopeTripsByCreated(ctx, scopedb.ListScopeTripsByCreatedParams{FromAt: f.From, ToAt: f.To,
			Statuses: f.Statuses, DriverID: f.DriverID, TruckID: f.TruckID, AfterCreated: c.at(), AfterID: c.id(),
			RowLimit: int32(limit + 1)})
		return err
	})
	if err != nil {
		return Page[scopedb.ScopeTrip]{}, err
	}
	return page(rows, limit, func(t scopedb.ScopeTrip) cursor {
		if axis == axisDelivered {
			return cursor{Axis: axis, At: t.DeliveredAt, ID: t.ID}
		}
		return cursor{Axis: axis, At: ptr(t.CreatedAt), ID: t.ID}
	}), nil
}

// --- GET /v1/trips/{id}, /by-no/{tripNo}, /{id}/photos -------------------------------------------------------

// GetTripView is GET /v1/trips/{id}: staff read the trip of their reach with stops, signed photos, renames and (with
// accounting:view_income) the billing block; a scope principal the projection with stops and signed photos.
func (s *Service) GetTripView(ctx context.Context, p *authz.Principal, id uuid.UUID) (any, error) {
	if scoped(p) {
		return s.scopeTripDetail(ctx, p, func(q *scopedb.Queries) (scopedb.ScopeTrip, error) { return q.GetScopeTrip(ctx, id) })
	}
	detail, err := s.GetTrip(ctx, p, id)
	if err != nil {
		return nil, err
	}
	return s.staffTripDetail(ctx, p, detail, false)
}

// TripByNoView is GET /v1/trips/by-no/{tripNo}: the trip that holds the number now or, for staff, the one most
// recently renamed away from it (matchedPreviousNo).
func (s *Service) TripByNoView(ctx context.Context, p *authz.Principal, tripNo string) (any, error) {
	tripNo = strings.TrimSpace(tripNo)
	if tripNo == "" {
		return nil, httpx.ErrNotFound()
	}
	if scoped(p) {
		return s.scopeTripDetail(ctx, p, func(q *scopedb.Queries) (scopedb.ScopeTrip, error) { return q.ScopeTripByNo(ctx, tripNo) })
	}
	detail, renamed, err := s.TripByNo(ctx, p, tripNo)
	if err != nil {
		return nil, err
	}
	return s.staffTripDetail(ctx, p, detail, renamed)
}

func (s *Service) staffTripDetail(ctx context.Context, p *authz.Principal, detail TripDetail, renamed bool) (TripDetailView, error) {
	d, err := s.directory(ctx, p)
	if err != nil {
		return TripDetailView{}, err
	}
	views, err := s.staffTripViews(ctx, p, d, []Trip{detail.Trip})
	if err != nil {
		return TripDetailView{}, err
	}
	out := TripDetailView{TripView: views[0], Stops: []TripStopRow{}, Renames: []TripRenameRow{}, MatchedPreviousNo: renamed}
	for _, st := range detail.Stops {
		out.Stops = append(out.Stops, d.tripStop(st))
	}
	for _, r := range detail.Renames {
		out.Renames = append(out.Renames, TripRenameRow{OldTripNo: r.OldTripNo, NewTripNo: r.NewTripNo, RenamedBy: r.RenamedBy,
			RenamedAt: r.RenamedAt})
	}
	if out.Photos, err = s.photos(ctx, photoRows(detail.Photos, nil)); err != nil {
		return TripDetailView{}, err
	}
	return out, nil
}

func (s *Service) scopeTripDetail(ctx context.Context, p *authz.Principal,
	get func(q *scopedb.Queries) (scopedb.ScopeTrip, error)) (TripScopeDetail, error) {
	var (
		trip   scopedb.ScopeTrip
		stops  []scopedb.ScopeTripStop
		photos []scopedb.ScopeTripPhoto
	)
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		if trip, err = get(q); err != nil {
			return notFound(err)
		}
		if stops, err = q.ScopeTripStopsOf(ctx, []uuid.UUID{trip.ID}); err != nil {
			return err
		}
		photos, err = q.ScopeTripPhotos(ctx, trip.ID)
		return err
	})
	if err != nil {
		return TripScopeDetail{}, err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return TripScopeDetail{}, err
	}
	names, err := s.names(ctx, p, appendID(nil, trip.DriverID))
	if err != nil {
		return TripScopeDetail{}, err
	}
	out := TripScopeDetail{TripBase: d.tripBaseScope(trip), Stops: []TripStopRow{}}
	out.DriverName = nameOf(names, out.DriverID)
	for _, st := range stops {
		out.Stops = append(out.Stops, d.tripStopScope(st))
	}
	if out.Photos, err = s.photos(ctx, photoRows(nil, photos)); err != nil {
		return TripScopeDetail{}, err
	}
	return out, nil
}

// photos signs a trip's photos (already in workflow order).
func (s *Service) photos(ctx context.Context, rows []photoRow) ([]Photo, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.fileID
	}
	signed, err := s.sign(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Photo, 0, len(rows))
	for _, r := range rows {
		out = append(out, photoOf(signed, r))
	}
	return out, nil
}

// TripPhotosView is GET /v1/trips/{id}/photos (operations:view_driver_monitor): the trip's photos in the ADR 0018
// workflow rank with URLs signed for PhotoTTL; the ZIP stays in the browser (R69). A trip outside the reach is 404.
func (s *Service) TripPhotosView(ctx context.Context, p *authz.Principal, id uuid.UUID) ([]Photo, error) {
	var rows []photoRow
	if scoped(p) {
		err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			if _, err := q.GetScopeTrip(ctx, id); err != nil {
				return notFound(err)
			}
			ph, err := q.ScopeTripPhotos(ctx, id)
			rows = photoRows(nil, ph)
			return err
		})
		if err != nil {
			return nil, err
		}
		return s.photos(ctx, rows)
	}
	r := ReachOf(p)
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		if _, err := q.GetTrip(ctx, operationsdb.GetTripParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(),
			ReachDriver: r.Driver}); err != nil {
			return notFound(err)
		}
		ph, err := q.TripPhotos(ctx, id)
		rows = photoRows(ph, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.photos(ctx, rows)
}
