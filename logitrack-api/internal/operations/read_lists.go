package operations

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// --- GET /v1/standby --------------------------------------------------------------------------------------------

// StandbyBase is a standby record as every reader may see it: the columns of the scope_standby projection (Appendix C
// §C.3.7) with the driver's name and the two photos (customer_worksheet, site_photo) signed for PhotoTTL.
type StandbyBase struct {
	ID                uuid.UUID         `json:"id"`
	TenantID          uuid.UUID         `json:"tenantId"`
	DriverID          *uuid.UUID        `json:"driverId"`
	DriverName        *string           `json:"driverName"`
	TaskID            *uuid.UUID        `json:"taskId"`
	TripID            *uuid.UUID        `json:"tripId"`
	JobCategory       *string           `json:"jobCategory"`
	StartLocation     *string           `json:"startLocation"`
	EndLocation       *string           `json:"endLocation"`
	StartedAt         *time.Time        `json:"startedAt"`
	EndedAt           *time.Time        `json:"endedAt"`
	DurationMinutes   *int32            `json:"durationMinutes"`
	Note              *string           `json:"note"`
	Status            string            `json:"status"`
	Lat               *float64          `json:"lat"`
	Lng               *float64          `json:"lng"`
	TruckID           *uuid.UUID        `json:"truckId"`
	TruckLicensePlate *string           `json:"truckLicensePlate"`
	Photos            []StandbyPhotoRow `json:"photos"`
	CreatedAt         time.Time         `json:"createdAt"`
}

// StandbyPhotoRow is one of the two photos of a standby record.
type StandbyPhotoRow struct {
	Type        string  `json:"type"`
	OverlayText *string `json:"overlayText"`
	FileRef
}

// StandbyListView is a standby record as tenant staff read it: StandbyBase plus the customer resolution, the stamping
// and the backfill flag, and the billing block only for the billing carrier's staff holding accounting:view_income
// (Appendix C §C.3.4, R61; Project).
type StandbyListView struct {
	StandbyBase
	TenantSource         string          `json:"tenantSource"`
	CustomerPartyID      *uuid.UUID      `json:"customerPartyId"`
	CustomerResolved     bool            `json:"customerResolved"`
	CustomerResolvedFrom *string         `json:"customerResolvedFrom"`
	BackfilledByAdmin    bool            `json:"backfilledByAdmin"`
	MigratedFromTripNo   *string         `json:"migratedFromTripNo"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	Billing              *StandbyBilling `json:"billing,omitempty"`
}

// StandbyBilling is the stored price of a standby record (written by the billing service only).
type StandbyBilling struct {
	EstimateThb       *float64   `json:"estimateThb"`
	PartyID           *uuid.UUID `json:"partyId"`
	RateSource        *string    `json:"rateSource"`
	RateEntryID       *uuid.UUID `json:"rateEntryId"`
	EffectiveFromDate *string    `json:"effectiveFromDate"`
	UnpricedReason    *string    `json:"unpricedReason"`
	ComputedAt        *time.Time `json:"computedAt"`
	BillingTenantID   *uuid.UUID `json:"billingTenantId"`
}

// StandbyListInput is the query of GET /v1/standby (Appendix B §B.2.13): sort created (default: every record, newest
// first) | ended (completed records by ended_at, latest first); from, to on that axis (a Bangkok day or an RFC 3339
// instant, [from, to)); driverId; customerId (staff: the record's customer or billed party); limit, cursor. No row cap
// (the legacy page read the newest 300 and filtered those).
type StandbyListInput struct {
	Sort, From, To, DriverID, CustomerID string
	Limit                                int
	Cursor                               string
}

// ListStandbyView is GET /v1/standby: staff read their reach ([]StandbyListView), a dispatcher or a customer-scope
// principal the projection ([]StandbyBase).
func (s *Service) ListStandbyView(ctx context.Context, p *authz.Principal, in StandbyListInput) (any, string, error) {
	var ck checker
	f := StandbyFilter{Sort: strings.TrimSpace(in.Sort), From: bound(&ck, "from", in.From, false),
		To: bound(&ck, "to", in.To, true), DriverID: optUUID(&ck, "driverId", in.DriverID),
		CustomerID: optUUID(&ck, "customerId", in.CustomerID), Limit: in.Limit, Cursor: in.Cursor}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		ck.add("to", "before_from")
	}
	sc := scoped(p)
	if sc && f.CustomerID != nil {
		notAvailable(&ck, "customerId") // the projection carries no party column
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	if sc {
		return s.listScopeStandby(ctx, p, f)
	}
	pg, err := s.ListStandby(ctx, p, f)
	if err != nil {
		return nil, "", err
	}
	ids := make([]uuid.UUID, len(pg.Items))
	var drivers []uuid.UUID
	for i, v := range pg.Items {
		ids[i] = v.Standby.ID
		drivers = appendID(drivers, v.Standby.DriverID)
	}
	photos := map[uuid.UUID][]operationsdb.StandbyPhoto{}
	if len(ids) > 0 {
		if err := s.read(ctx, p, func(q *operationsdb.Queries) error {
			rows, err := q.StandbyPhotosOf(ctx, ids)
			for _, r := range rows {
				photos[r.StandbyID] = append(photos[r.StandbyID], r)
			}
			return err
		}); err != nil {
			return nil, "", err
		}
	}
	var files []uuid.UUID
	for _, list := range photos {
		for _, ph := range list {
			files = append(files, ph.FileID)
		}
	}
	signed, err := s.sign(ctx, files)
	if err != nil {
		return nil, "", err
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, "", err
	}
	income := viewIncome(p)
	out := make([]StandbyListView, 0, len(pg.Items))
	for _, v := range pg.Items {
		r := v.Standby
		b := StandbyBase{ID: r.ID, TenantID: r.TenantID, DriverID: r.DriverID, DriverName: nameOf(names, r.DriverID),
			TaskID: r.TaskID, TripID: r.TripID, JobCategory: r.JobCategory, StartLocation: r.StartLocationRaw,
			EndLocation: r.EndLocationRaw, StartedAt: r.StartedAt, EndedAt: r.EndedAt, DurationMinutes: r.DurationMinutes,
			Note: r.Note, Status: r.Status, Lat: r.Lat, Lng: r.Lng, TruckID: r.TruckID,
			TruckLicensePlate: r.TruckLicensePlateSnapshot, Photos: []StandbyPhotoRow{}, CreatedAt: r.CreatedAt}
		for _, ph := range photos[r.ID] {
			b.Photos = append(b.Photos, StandbyPhotoRow{Type: ph.PhotoType, OverlayText: ph.OverlayText,
				FileRef: *fileRef(signed, &ph.FileID)})
		}
		lv := StandbyListView{StandbyBase: b, TenantSource: r.TenantSource, CustomerPartyID: r.CustomerPartyID,
			CustomerResolved: r.CustomerResolved, CustomerResolvedFrom: r.CustomerResolvedFrom,
			BackfilledByAdmin: r.BackfilledByAdmin, MigratedFromTripNo: r.MigratedFromTripNo, UpdatedAt: r.UpdatedAt}
		if v.Billing && income {
			lv.Billing = &StandbyBilling{EstimateThb: r.BillingEstimateThb, PartyID: r.BillingPartyID,
				RateSource: r.BillingRateSource, RateEntryID: r.BillingRateEntryID,
				EffectiveFromDate: dayString(r.BillingEffectiveFromDate), UnpricedReason: r.BillingUnpricedReason,
				ComputedAt: r.BillingComputedAt, BillingTenantID: r.BillingTenantID}
		}
		out = append(out, lv)
	}
	return out, pg.NextCursor, nil
}

func (s *Service) listScopeStandby(ctx context.Context, p *authz.Principal, f StandbyFilter) (any, string, error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	axis := map[string]string{"": axisCreated, StandbyByCreated: axisCreated, StandbyByEnded: axisEnded}[f.Sort]
	if axis == "" {
		ck.add("sort", "invalid")
	}
	c, ok := decodeCursor(f.Cursor, axis)
	if !ok && axis != "" {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	var (
		rows   []scopedb.ScopeStandby
		photos = map[uuid.UUID][]scopedb.ScopeStandbyPhoto{}
	)
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		if axis == axisEnded {
			rows, err = q.ListScopeStandbyByEnded(ctx, scopedb.ListScopeStandbyByEndedParams{FromAt: f.From, ToAt: f.To,
				DriverID: f.DriverID, AfterEnded: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
		} else {
			rows, err = q.ListScopeStandbyByCreated(ctx, scopedb.ListScopeStandbyByCreatedParams{FromAt: f.From, ToAt: f.To,
				DriverID: f.DriverID, AfterCreated: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
		}
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		ph, err := q.ScopeStandbyPhotosOf(ctx, ids)
		for _, x := range ph {
			photos[x.StandbyID] = append(photos[x.StandbyID], x)
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	pg := page(rows, limit, func(r scopedb.ScopeStandby) cursor {
		if axis == axisEnded {
			return cursor{Axis: axis, At: r.EndedAt, ID: r.ID}
		}
		return cursor{Axis: axis, At: ptr(r.CreatedAt), ID: r.ID}
	})
	var files, drivers []uuid.UUID
	for _, r := range pg.Items {
		drivers = appendID(drivers, r.DriverID)
		for _, ph := range photos[r.ID] {
			files = append(files, ph.FileID)
		}
	}
	signed, err := s.sign(ctx, files)
	if err != nil {
		return nil, "", err
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, "", err
	}
	out := make([]StandbyBase, 0, len(pg.Items))
	for _, r := range pg.Items {
		b := StandbyBase{ID: r.ID, TenantID: r.TenantID, DriverID: r.DriverID, DriverName: nameOf(names, r.DriverID),
			TaskID: r.TaskID, TripID: r.TripID, JobCategory: r.JobCategory, StartLocation: r.StartLocationRaw,
			EndLocation: r.EndLocationRaw, StartedAt: r.StartedAt, EndedAt: r.EndedAt, DurationMinutes: r.DurationMinutes,
			Note: r.Note, Status: r.Status, Lat: r.Lat, Lng: r.Lng, TruckID: r.TruckID,
			TruckLicensePlate: r.TruckLicensePlateSnapshot, Photos: []StandbyPhotoRow{}, CreatedAt: r.CreatedAt}
		for _, ph := range photos[r.ID] {
			b.Photos = append(b.Photos, StandbyPhotoRow{Type: ph.PhotoType, OverlayText: ph.OverlayText,
				FileRef: *fileRef(signed, &ph.FileID)})
		}
		out = append(out, b)
	}
	return out, pg.NextCursor, nil
}

// --- GET /v1/incidents ------------------------------------------------------------------------------------------

// IncidentBase is an incident report as every reader may see it: the columns of the scope_incidents projection
// (Appendix C §C.3.7) with the trip's number, the driver's name and the three photos signed for PhotoTTL.
type IncidentBase struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenantId"`
	TripID            *uuid.UUID `json:"tripId"`
	TripNo            *string    `json:"tripNo"`
	DriverID          *uuid.UUID `json:"driverId"`
	DriverName        *string    `json:"driverName"`
	DelayCause        *string    `json:"delayCause"`
	Description       *string    `json:"description"`
	Lat               *float64   `json:"lat"`
	Lng               *float64   `json:"lng"`
	TruckID           *uuid.UUID `json:"truckId"`
	TruckLicensePlate *string    `json:"truckLicensePlate"`
	MapPhoto          *FileRef   `json:"mapPhoto"`
	Situation1Photo   *FileRef   `json:"situation1Photo"`
	Situation2Photo   *FileRef   `json:"situation2Photo"`
	ReportedByKind    string     `json:"reportedByKind"`
	CreatedAt         time.Time  `json:"createdAt"`
}

// IncidentView is an incident report as tenant staff read it: IncidentBase plus the stamping, the reporting user, the
// legacy trip reference of an unresolved trip and the update time.
type IncidentView struct {
	IncidentBase
	TenantSource     string     `json:"tenantSource"`
	ReportedByUserID *uuid.UUID `json:"reportedByUserId"`
	LegacyTripRef    *string    `json:"legacyTripRef"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// IncidentListInput is the query of GET /v1/incidents (Appendix B §B.2.12): from, to on created_at (a Bangkok day or
// an RFC 3339 instant, [from, to)), tripId, driverId, limit, cursor. No row cap (the legacy page read 200).
type IncidentListInput struct {
	From, To, TripID, DriverID string
	Limit                      int
	Cursor                     string
}

// ListIncidentsView is GET /v1/incidents: staff read their reach ([]IncidentView); a dispatcher or a customer-scope
// principal reads the incidents of the trips it sees, through scope_incidents ([]IncidentBase): a customer whose parties
// have no trip reads none (closes incident-reports/page.tsx:185, where zero trips showed every incident).
func (s *Service) ListIncidentsView(ctx context.Context, p *authz.Principal, in IncidentListInput) (any, string, error) {
	var ck checker
	f := IncidentFilter{From: bound(&ck, "from", in.From, false), To: bound(&ck, "to", in.To, true),
		TripID: optUUID(&ck, "tripId", in.TripID), DriverID: optUUID(&ck, "driverId", in.DriverID), Limit: in.Limit,
		Cursor: in.Cursor}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		ck.add("to", "before_from")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	if scoped(p) {
		return s.listScopeIncidents(ctx, p, f)
	}
	pg, err := s.ListIncidents(ctx, p, f)
	if err != nil {
		return nil, "", err
	}
	var trips, drivers, files []uuid.UUID
	for _, i := range pg.Items {
		trips, drivers = appendID(trips, i.TripID), appendID(drivers, i.DriverID)
		files = appendID(appendID(appendID(files, i.MapPhotoFileID), i.Situation1PhotoFileID), i.Situation2PhotoFileID)
	}
	tripNos := map[uuid.UUID]string{}
	if trips = uniqueIDs(trips); len(trips) > 0 {
		r := ReachOf(p)
		if err := s.read(ctx, p, func(q *operationsdb.Queries) error {
			rows, err := q.TripNumbersOf(ctx, operationsdb.TripNumbersOfParams{Ids: trips, AllTenants: r.All, TenantIds: r.tenants()})
			for _, x := range rows {
				tripNos[x.ID] = x.TripNo
			}
			return err
		}); err != nil {
			return nil, "", err
		}
	}
	signed, err := s.sign(ctx, files)
	if err != nil {
		return nil, "", err
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, "", err
	}
	out := make([]IncidentView, 0, len(pg.Items))
	for _, i := range pg.Items {
		out = append(out, IncidentView{IncidentBase: incidentBase(i.ID, i.TenantID, i.TripID, i.DriverID, i.DelayCause,
			i.Description, i.Lat, i.Lng, i.TruckID, i.TruckLicensePlateSnapshot, i.MapPhotoFileID, i.Situation1PhotoFileID,
			i.Situation2PhotoFileID, i.ReportedByKind, i.CreatedAt, tripNos, names, signed),
			TenantSource: i.TenantSource, ReportedByUserID: i.ReportedByUserID, LegacyTripRef: i.LegacyTripRef,
			UpdatedAt: i.UpdatedAt})
	}
	return out, pg.NextCursor, nil
}

func incidentBase(id, tenant uuid.UUID, trip, driver *uuid.UUID, cause, desc *string, lat, lng *float64, truck *uuid.UUID,
	plate *string, mp, s1, s2 *uuid.UUID, kind string, at time.Time, tripNos map[uuid.UUID]string,
	names map[uuid.UUID]string, signed map[uuid.UUID]storage.Signed) IncidentBase {
	b := IncidentBase{ID: id, TenantID: tenant, TripID: trip, DriverID: driver, DriverName: nameOf(names, driver),
		DelayCause: cause, Description: desc, Lat: lat, Lng: lng, TruckID: truck, TruckLicensePlate: plate,
		MapPhoto: fileRef(signed, mp), Situation1Photo: fileRef(signed, s1), Situation2Photo: fileRef(signed, s2),
		ReportedByKind: kind, CreatedAt: at}
	if trip != nil {
		if no, ok := tripNos[*trip]; ok {
			b.TripNo = &no
		}
	}
	return b
}

func (s *Service) listScopeIncidents(ctx context.Context, p *authz.Principal, f IncidentFilter) (any, string, error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	c, ok := decodeCursor(f.Cursor, axisCreated)
	if !ok {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	var (
		rows    []scopedb.ScopeIncident
		tripNos = map[uuid.UUID]string{}
	)
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		rows, err = q.ListScopeIncidentsFiltered(ctx, scopedb.ListScopeIncidentsFilteredParams{FromAt: f.From, ToAt: f.To,
			TripID: f.TripID, DriverID: f.DriverID, AfterCreated: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
		if err != nil {
			return err
		}
		var trips []uuid.UUID
		for _, i := range rows {
			trips = appendID(trips, i.TripID)
		}
		if trips = uniqueIDs(trips); len(trips) == 0 {
			return nil
		}
		ts, err := q.ScopeTripsByID(ctx, trips)
		for _, t := range ts {
			tripNos[t.ID] = t.TripNo
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	pg := page(rows, limit, func(i scopedb.ScopeIncident) cursor {
		return cursor{Axis: axisCreated, At: ptr(i.CreatedAt), ID: i.ID}
	})
	var drivers, files []uuid.UUID
	for _, i := range pg.Items {
		drivers = appendID(drivers, i.DriverID)
		files = appendID(appendID(appendID(files, i.MapPhotoFileID), i.Situation1PhotoFileID), i.Situation2PhotoFileID)
	}
	signed, err := s.sign(ctx, files)
	if err != nil {
		return nil, "", err
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, "", err
	}
	out := make([]IncidentBase, 0, len(pg.Items))
	for _, i := range pg.Items {
		out = append(out, incidentBase(i.ID, i.TenantID, i.TripID, i.DriverID, i.DelayCause, i.Description, i.Lat, i.Lng,
			i.TruckID, i.TruckLicensePlateSnapshot, i.MapPhotoFileID, i.Situation1PhotoFileID, i.Situation2PhotoFileID,
			i.ReportedByKind, i.CreatedAt, tripNos, names, signed))
	}
	return out, pg.NextCursor, nil
}
