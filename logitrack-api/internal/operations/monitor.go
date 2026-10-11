package operations

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
)

// The driver monitor (GET /v1/trips/monitor, Appendix B §B.2.12, main spec §5.4, W8): one paged server join replacing
// useDriverMonitor.ts (an unbounded trips listener, three capped side listeners for drivers, incidents and tasks, a
// rate-card fan-out and per-row pricing calls). Pages of at most 200 trips, newest first on created_at (the loading
// time, the "depart" of ADR 0001), keyset (created_at, id); every filter of the page runs in SQL, so a filter never
// narrows only the rows already loaded; the first page also carries the filter options of the date range with their
// counts (ADR 0005 §4, ADR 0006 §2: built from the trips, so every option matches a trip and the unresolved places,
// the orphan plates and the "not specified" buckets stay reachable).

// MonitorMaxSpan is the widest created_at range a monitor request reads (the page's options are counted over it).
const MonitorMaxSpan = 366 * 24 * time.Hour

// MonitorInput is the query of GET /v1/trips/monitor:
//
//	from, to      Bangkok days (both inclusive) or RFC 3339 instants ([from, to)); or month=yyyy-MM (a Bangkok month).
//	              Required, at most 366 days.
//	status        trip statuses (repeatable or comma-separated); "incident" also matches a trip with an incident report
//	jobType       first_mile | line_haul
//	driverId      the trip's driver
//	customerId    the billed party (staff only)
//	partner       a partner code, or __none__
//	plate         id:<truckId> | plate:<raw plate of a trip without a truck> | __none__ (ADR 0005)
//	vehicleClass  4W | 4WJ | 6WH | 10WH | 18WH | VAN, or __none__
//	origin        code:<CODE> | raw:<text> | __none__ (ADR 0006); destination the same, matched by any stop
//	q             a substring of the trip or task number or the driver's name
//	limit         1-200 (default 200), cursor; facets=false skips the options of the first page
type MonitorInput struct {
	From, To, Month               string
	Statuses                      []string
	JobType, DriverID, CustomerID string
	Partner, Plate, VehicleClass  string
	Origin, Destination, Q        string
	Limit                         int
	Cursor                        string
	NoFacets                      bool
}

// MonitorRow is one trip of the monitor: the trip, its task's check-in and round, the driver's name, the resolved
// places (each stop of a multi-drop trip too), the truck it ran on (the trip's snapshot, else its task's; never the
// driver's live truck, ADR 0005 §6) with its filter key, the incident and photo counts, and, for staff, the delivery
// channel, the review state and (accounting:view_income) the stored price. A dispatcher's or customer-scope
// principal's row has none of the staff fields.
type MonitorRow struct {
	ID               uuid.UUID     `json:"id"`
	TenantID         uuid.UUID     `json:"tenantId"`
	TripNo           string        `json:"tripNo"`
	Status           string        `json:"status"`
	JobType          string        `json:"jobType"`
	JobCategory      *string       `json:"jobCategory"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	DeliveredAt      *time.Time    `json:"deliveredAt"`
	TaskID           *uuid.UUID    `json:"taskId"`
	TaskNo           *string       `json:"taskNo"`
	TaskStatus       *string       `json:"taskStatus"`
	CheckInAt        *time.Time    `json:"checkInAt"`
	RunOrder         *int32        `json:"runOrder"`
	PlanAt           *time.Time    `json:"planAt"`
	DriverID         *uuid.UUID    `json:"driverId"`
	DriverName       *string       `json:"driverName"`
	Origin           Place         `json:"origin"`
	Destination      Place         `json:"destination"`
	Stops            []MonitorStop `json:"stops"`
	Truck            MonitorTruck  `json:"truck"`
	PartnerCode      *string       `json:"partnerCode"`
	SealCode         *string       `json:"sealCode"`
	IsMultiDelivery  bool          `json:"isMultiDelivery"`
	IncidentCount    int32         `json:"incidentCount"`
	PhotoCount       int32         `json:"photoCount"`
	DeliveredVia     *string       `json:"deliveredVia,omitempty"`
	NeedsAdminReview *bool         `json:"needsAdminReview,omitempty"`
	ReviewStatus     *string       `json:"reviewStatus,omitempty"`
	Price            *PricePreview `json:"price,omitempty"`
}

// MonitorStop is one stop of a multi-drop trip on the monitor.
type MonitorStop struct {
	Index        int32      `json:"index"`
	Place        Place      `json:"place"`
	Status       string     `json:"status"`
	DeliveredAt  *time.Time `json:"deliveredAt"`
	CompletedSeq *int32     `json:"completedSeq"`
}

// MonitorTruck is the truck a trip ran on: key is its plate filter value (id:<truckId> | plate:<raw> | __none__),
// vehicleClassKey its class filter value (the class or __none__).
type MonitorTruck struct {
	TruckID         *uuid.UUID `json:"truckId"`
	Plate           *string    `json:"plate"`
	Key             string     `json:"key"`
	VehicleClass    *string    `json:"vehicleClass"`
	VehicleClassKey string     `json:"vehicleClassKey"`
}

// MonitorMeta is the meta of the first monitor page.
type MonitorMeta struct {
	Facets *MonitorFacets `json:"facets,omitempty"`
}

// MonitorFacets are the filter options of the date range (and the customer, when given) with the number of trips
// each matches: the statuses, the trips with an incident (status or report), every origin and destination (resolved
// places by label, then each unresolved raw value as its own option, then the none bucket), every truck and orphan
// plate, every vehicle class and partner code.
type MonitorFacets struct {
	Total          int            `json:"total"`
	StatusCounts   map[string]int `json:"statusCounts"`
	Incident       int            `json:"incident"`
	Origins        []PlaceOption  `json:"origins"`
	Destinations   []PlaceOption  `json:"destinations"`
	Plates         []PlateOption  `json:"plates"`
	VehicleClasses []Option       `json:"vehicleClasses"`
	Partners       []Option       `json:"partners"`
}

// PlaceOption is an origin or destination option (lib/placeFilter.ts PlaceFilterOption).
type PlaceOption struct {
	Value        string `json:"value"`
	Label        string `json:"label"`
	IsUnresolved bool   `json:"isUnresolved"`
	Count        int    `json:"count"`
}

// PlateOption is a plate option (lib/truckPlate.ts PlateFilterOption): an orphan plate has no truck.
type PlateOption struct {
	Value    string  `json:"value"`
	Label    *string `json:"label"`
	IsOrphan bool    `json:"isOrphan"`
	Count    int     `json:"count"`
}

// Option is a vehicle class or partner option ("" label for the none bucket).
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// monitorQuery is the checked input in the arguments of both monitor queries.
type monitorQuery struct {
	from, to            time.Time
	statuses            []string
	incident            bool
	jobType             *string
	driverID, partyID   *uuid.UUID
	partner             *string
	partnerNone         bool
	plateTruck          *uuid.UUID
	plateRaw            *string
	plateNone           bool
	vehicleClass        *string
	vehicleClassNone    bool
	origin, destination placeFilter
	q                   *string
	limit               int
	after               *cursor
	facets              bool
}

func (d directory) monitorQuery(p *authz.Principal, in MonitorInput) (monitorQuery, error) {
	var ck checker
	var m monitorQuery
	// The range.
	switch month := strings.TrimSpace(in.Month); {
	case month != "":
		if in.From != "" || in.To != "" {
			ck.add("month", "exclusive")
		}
		start, err := time.ParseInLocation("2006-01", month, clock.Bangkok)
		if err != nil {
			ck.add("month", "invalid")
		}
		m.from, m.to = start, start.AddDate(0, 1, 0)
	default:
		from, to := bound(&ck, "from", in.From, false), bound(&ck, "to", in.To, true)
		switch {
		case strings.TrimSpace(in.From) == "":
			ck.add("from", "required")
		case strings.TrimSpace(in.To) == "":
			ck.add("to", "required")
		case from != nil && to != nil:
			m.from, m.to = *from, *to
		}
	}
	if !m.from.IsZero() && !m.to.IsZero() {
		switch {
		case !m.to.After(m.from):
			ck.add("to", "before_from")
		case m.to.Sub(m.from) > MonitorMaxSpan:
			ck.add("to", "range_too_long")
		}
	}
	// The filters.
	m.statuses = statusList(&ck, "status", in.Statuses, TripStatuses)
	m.incident = slices.Contains(m.statuses, TripIncident)
	if jt := strings.TrimSpace(in.JobType); jt != "" && jt != "all" {
		if !slices.Contains(TaskTypes, jt) {
			ck.add("jobType", "invalid")
		}
		m.jobType = &jt
	}
	m.driverID = optUUID(&ck, "driverId", in.DriverID)
	m.partyID = optUUID(&ck, "customerId", in.CustomerID)
	if m.partyID != nil && scoped(p) {
		notAvailable(&ck, "customerId")
	}
	switch v := strings.TrimSpace(in.Partner); {
	case v == "" || v == "all":
	case v == BucketNone:
		m.partnerNone = true
	case !validText(v) || len([]rune(v)) > 100:
		ck.add("partner", "invalid")
	default:
		m.partner = &v
	}
	switch v := strings.TrimSpace(in.Plate); {
	case v == "" || v == "all":
	case v == BucketNone:
		m.plateNone = true
	case strings.HasPrefix(v, plateID):
		id, err := uuid.Parse(strings.TrimPrefix(v, plateID))
		if err != nil || id == uuid.Nil {
			ck.add("plate", "invalid")
		}
		m.plateTruck = &id
	case strings.HasPrefix(v, plateText):
		raw := strings.TrimSpace(strings.TrimPrefix(v, plateText))
		if raw == "" || !validText(raw) {
			ck.add("plate", "invalid")
		}
		m.plateRaw = &raw
	default:
		ck.add("plate", "invalid")
	}
	switch v := strings.TrimSpace(in.VehicleClass); v {
	case "", "all":
	case BucketNone:
		m.vehicleClassNone = true
	default:
		u := strings.ToUpper(v)
		if !slices.Contains(VehicleClasses, u) {
			ck.add("vehicleClass", "invalid")
		}
		m.vehicleClass = &u
	}
	m.origin = d.parsePlace(&ck, "origin", in.Origin)
	m.destination = d.parsePlace(&ck, "destination", in.Destination)
	m.q = nonEmpty(qText(&ck, in.Q))
	m.limit = MonitorLimit
	if in.Limit != 0 {
		if in.Limit < 1 || in.Limit > MonitorLimit {
			ck.add("limit", "out_of_range")
		}
		m.limit = in.Limit
	}
	c, ok := decodeCursor(in.Cursor, axisCreated)
	if !ok {
		ck.add("cursor", "invalid")
	}
	m.after = c
	m.facets = in.Cursor == "" && !in.NoFacets
	return m, ck.err()
}

func emptyIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

// Monitor is GET /v1/trips/monitor: one page of rows and, on the first page, the options of the range.
func (s *Service) Monitor(ctx context.Context, p *authz.Principal, in MonitorInput) ([]MonitorRow, string, *MonitorMeta, error) {
	d, err := s.directory(ctx, p)
	if err != nil {
		return nil, "", nil, err
	}
	m, err := d.monitorQuery(p, in)
	if err != nil {
		return nil, "", nil, err
	}
	var (
		rows   []MonitorRow
		facets []facetRow
	)
	if scoped(p) {
		rows, facets, err = s.scopeMonitor(ctx, p, d, m)
	} else {
		rows, facets, err = s.staffMonitor(ctx, p, d, m)
	}
	if err != nil {
		return nil, "", nil, err
	}
	pg := page(rows, m.limit, func(r MonitorRow) cursor { return cursor{Axis: axisCreated, At: ptr(r.CreatedAt), ID: r.ID} })
	var meta *MonitorMeta
	if m.facets {
		meta = &MonitorMeta{Facets: d.facets(facets)}
	}
	return pg.Items, pg.NextCursor, meta, nil
}

func (s *Service) staffMonitor(ctx context.Context, p *authz.Principal, d directory, m monitorQuery) ([]MonitorRow, []facetRow, error) {
	r := ReachOf(p)
	income := viewIncome(p)
	var (
		rows   []operationsdb.MonitorTripsRow
		stops  []TripStop
		facets []operationsdb.MonitorFacetsRow
	)
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		rows, err = q.MonitorTrips(ctx, operationsdb.MonitorTripsParams{WithPrice: income, AllTenants: r.All,
			TenantIds: r.tenants(), FromAt: m.from, ToAt: m.to, Statuses: m.statuses, IncidentStatus: m.incident,
			JobType: m.jobType, DriverID: m.driverID, BillingPartyID: m.partyID, Partner: m.partner,
			PartnerNone: m.partnerNone, PlateTruck: m.plateTruck, PlateRaw: m.plateRaw, PlateNone: m.plateNone,
			VehicleClass: m.vehicleClass, VehicleClassNone: m.vehicleClassNone, OriginOn: m.origin.on,
			OriginHubs: emptyIDs(m.origin.hubs), OriginSoc: m.origin.soc, OriginRaw: m.origin.raw, OriginNone: m.origin.none,
			DestinationOn: m.destination.on, DestinationHubs: emptyIDs(m.destination.hubs), DestinationSoc: m.destination.soc,
			DestinationRaw: m.destination.raw, DestinationNone: m.destination.none, Q: m.q, AfterCreated: m.after.at(),
			AfterID: m.after.id(), RowLimit: int32(m.limit + 1)})
		if err != nil {
			return err
		}
		// The stops of every trip of the page (not only the flagged ones: the destination filter matches by stops).
		pageIDs := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			pageIDs[i] = row.TripRecord.ID
		}
		if len(pageIDs) > 0 {
			if stops, err = q.TripStopsOf(ctx, pageIDs); err != nil {
				return err
			}
		}
		if m.facets {
			facets, err = q.MonitorFacets(ctx, operationsdb.MonitorFacetsParams{AllTenants: r.All, TenantIds: r.tenants(),
				FromAt: m.from, ToAt: m.to, BillingPartyID: m.partyID})
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	byTrip := map[uuid.UUID][]MonitorStop{}
	for _, st := range stops {
		byTrip[st.TripID] = append(byTrip[st.TripID], MonitorStop{Index: st.StopIndex,
			Place: d.place(&st.DestinationRaw, st.DestinationHubID, nil), Status: st.Status, DeliveredAt: st.DeliveredAt,
			CompletedSeq: st.CompletedSeq})
	}
	out := make([]MonitorRow, 0, len(rows))
	for _, row := range rows {
		t := row.TripRecord
		mr := monitorRow(d, monitorCols{id: t.ID, tenant: t.TenantID, tripNo: t.TripNo, status: t.Status, jobType: t.JobType,
			jobCategory: t.JobCategory, createdAt: t.CreatedAt, updatedAt: t.UpdatedAt, deliveredAt: t.DeliveredAt,
			taskID: t.TaskID, driverID: t.DriverID, originRaw: t.OriginRaw, originHub: t.OriginHubID,
			destinationRaw: t.DestinationRaw, destinationHub: t.DestinationHubID, destinationSoc: t.DestinationSocKey,
			truckID: t.TruckID, plate: t.TruckLicensePlateSnapshot, vehicleClass: t.VehicleClass, partner: t.PartnerCode,
			seal: t.SealCode, multi: t.IsMultiDelivery, taskNo: row.TaskNo, taskStatus: row.TaskStatus,
			checkInAt: row.TaskCheckInAt, runOrder: row.TaskRunOrder, planAt: row.TaskPlanAt, taskTruck: row.TaskTruckID,
			taskPlate: row.TaskPlate, taskClass: row.TaskVehicleClass, fullTh: row.DriverFullNameTh,
			first: row.DriverFirstName, last: row.DriverLastName, incidents: row.IncidentCount, photos: row.PhotoCount},
			byTrip[t.ID])
		mr.DeliveredVia, mr.NeedsAdminReview, mr.ReviewStatus = t.DeliveredVia, &t.NeedsAdminReview, t.ReviewStatus
		if income && row.PriceComputedAt != nil {
			mr.Price = &PricePreview{EstimateThb: row.PriceEstimateThb, UnpricedReason: row.PriceUnpricedReason,
				ManualOverride:       row.PriceManualOverride != nil && *row.PriceManualOverride,
				JobCategoryAtPricing: row.PriceJobCategory,
				IsMultiDelivery:      row.PriceIsMultiDelivery != nil && *row.PriceIsMultiDelivery,
				ComputedAt:           *row.PriceComputedAt}
		}
		out = append(out, mr)
	}
	fr := make([]facetRow, len(facets))
	for i, f := range facets {
		fr[i] = facetRow{facet: f.Facet, ref: f.RefID, value: f.Value, label: f.Label, n: int(f.N)}
	}
	return out, fr, nil
}

func (s *Service) scopeMonitor(ctx context.Context, p *authz.Principal, d directory, m monitorQuery) ([]MonitorRow, []facetRow, error) {
	var (
		rows   []scopedb.ScopeMonitorTripsRow
		stops  []scopedb.ScopeTripStop
		facets []scopedb.ScopeMonitorFacetsRow
	)
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		rows, err = q.ScopeMonitorTrips(ctx, scopedb.ScopeMonitorTripsParams{FromAt: m.from, ToAt: m.to,
			Statuses: m.statuses, IncidentStatus: m.incident, JobType: m.jobType, DriverID: m.driverID, Partner: m.partner,
			PartnerNone: m.partnerNone, PlateTruck: m.plateTruck, PlateRaw: m.plateRaw, PlateNone: m.plateNone,
			VehicleClass: m.vehicleClass, VehicleClassNone: m.vehicleClassNone, OriginOn: m.origin.on,
			OriginHubs: emptyIDs(m.origin.hubs), OriginSoc: m.origin.soc, OriginRaw: m.origin.raw, OriginNone: m.origin.none,
			DestinationOn: m.destination.on, DestinationHubs: emptyIDs(m.destination.hubs), DestinationSoc: m.destination.soc,
			DestinationRaw: m.destination.raw, DestinationNone: m.destination.none, Q: m.q, AfterCreated: m.after.at(),
			AfterID: m.after.id(), RowLimit: int32(m.limit + 1)})
		if err != nil {
			return err
		}
		pageIDs := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			pageIDs[i] = row.ScopeTrip.ID
		}
		if len(pageIDs) > 0 {
			if stops, err = q.ScopeTripStopsOf(ctx, pageIDs); err != nil {
				return err
			}
		}
		if m.facets {
			facets, err = q.ScopeMonitorFacets(ctx, scopedb.ScopeMonitorFacetsParams{FromAt: m.from, ToAt: m.to})
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	byTrip := map[uuid.UUID][]MonitorStop{}
	for _, st := range stops {
		byTrip[st.TripID] = append(byTrip[st.TripID], MonitorStop{Index: st.StopIndex,
			Place: d.place(&st.DestinationRaw, st.DestinationHubID, nil), Status: st.Status, DeliveredAt: st.DeliveredAt,
			CompletedSeq: st.CompletedSeq})
	}
	out := make([]MonitorRow, 0, len(rows))
	for _, row := range rows {
		t := row.ScopeTrip
		out = append(out, monitorRow(d, monitorCols{id: t.ID, tenant: t.TenantID, tripNo: t.TripNo, status: t.Status,
			jobType: t.JobType, jobCategory: t.JobCategory, createdAt: t.CreatedAt, updatedAt: t.UpdatedAt,
			deliveredAt: t.DeliveredAt, taskID: t.TaskID, driverID: t.DriverID, originRaw: t.OriginRaw,
			originHub: t.OriginHubID, destinationRaw: t.DestinationRaw, destinationHub: t.DestinationHubID,
			destinationSoc: t.DestinationSocKey, truckID: t.TruckID, plate: t.TruckLicensePlateSnapshot,
			vehicleClass: t.VehicleClass, partner: t.PartnerCode, seal: t.SealCode, multi: t.IsMultiDelivery,
			taskNo: row.TaskNo, taskStatus: row.TaskStatus, checkInAt: row.TaskCheckInAt, runOrder: row.TaskRunOrder,
			planAt: row.TaskPlanAt, taskTruck: row.TaskTruckID, taskPlate: row.TaskPlate, taskClass: row.TaskVehicleClass,
			fullTh: row.DriverFullNameTh, first: row.DriverFirstName, last: row.DriverLastName,
			incidents: row.IncidentCount, photos: row.PhotoCount}, byTrip[t.ID]))
	}
	fr := make([]facetRow, len(facets))
	for i, f := range facets {
		fr[i] = facetRow{facet: f.Facet, ref: f.RefID, value: f.Value, label: f.Label, n: int(f.N)}
	}
	return out, fr, nil
}

// monitorCols are the columns a monitor row is built from, on either read path.
type monitorCols struct {
	id, tenant                                uuid.UUID
	tripNo, status, jobType                   string
	jobCategory                               *string
	createdAt, updatedAt                      time.Time
	deliveredAt                               *time.Time
	taskID, driverID                          *uuid.UUID
	originRaw                                 *string
	originHub                                 *uuid.UUID
	destinationRaw                            *string
	destinationHub                            *uuid.UUID
	destinationSoc                            *string
	truckID                                   *uuid.UUID
	plate, vehicleClass, partner, seal        *string
	multi                                     bool
	taskNo, taskStatus                        *string
	checkInAt, planAt                         *time.Time
	runOrder                                  *int32
	taskTruck                                 *uuid.UUID
	taskPlate, taskClass, fullTh, first, last *string
	incidents, photos                         int32
}

func trimmedPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

func monitorRow(d directory, c monitorCols, stops []MonitorStop) MonitorRow {
	out := MonitorRow{ID: c.id, TenantID: c.tenant, TripNo: c.tripNo, Status: c.status, JobType: c.jobType,
		JobCategory: c.jobCategory, CreatedAt: c.createdAt, UpdatedAt: c.updatedAt, DeliveredAt: c.deliveredAt,
		TaskID: c.taskID, TaskNo: c.taskNo, TaskStatus: c.taskStatus, CheckInAt: c.checkInAt, RunOrder: c.runOrder,
		PlanAt: c.planAt, DriverID: c.driverID, DriverName: driverName(c.fullTh, c.first, c.last),
		Origin: d.place(c.originRaw, c.originHub, nil), Destination: d.place(c.destinationRaw, c.destinationHub, c.destinationSoc),
		Stops: stops, PartnerCode: c.partner, SealCode: c.seal, IsMultiDelivery: c.multi, IncidentCount: c.incidents,
		PhotoCount: c.photos}
	if out.Stops == nil {
		out.Stops = []MonitorStop{}
	}
	// The truck the trip ran on: the trip's snapshot, else its task's (resolveTripPlate, lib/truckPlate.ts).
	truck := c.truckID
	if truck == nil {
		truck = c.taskTruck
	}
	plate := trimmedPtr(c.plate)
	if plate == nil {
		plate = trimmedPtr(c.taskPlate)
	}
	class := c.vehicleClass
	if class == nil {
		class = c.taskClass
	}
	out.Truck = MonitorTruck{TruckID: truck, Plate: plate, VehicleClass: class, Key: BucketNone, VehicleClassKey: BucketNone}
	switch {
	case truck != nil:
		out.Truck.Key = plateID + truck.String()
	case plate != nil:
		out.Truck.Key = plateText + *plate
	}
	if class != nil {
		out.Truck.VehicleClassKey = *class
	}
	return out
}

// facetRow is one row of either facet query.
type facetRow struct {
	facet, value, label string
	ref                 *uuid.UUID
	n                   int
}

// facets builds the options (one per key, counts merged when two stored forms are one key: a hub row and a SOC key of
// the same code).
func (d directory) facets(rows []facetRow) *MonitorFacets {
	out := &MonitorFacets{StatusCounts: map[string]int{}, Origins: []PlaceOption{}, Destinations: []PlaceOption{},
		Plates: []PlateOption{}, VehicleClasses: []Option{}, Partners: []Option{}}
	origins, destinations := map[string]*PlaceOption{}, map[string]*PlaceOption{}
	plates := map[string]*PlateOption{}
	addPlace := func(m map[string]*PlaceOption, r facetRow) {
		var pl Place
		switch {
		case r.ref != nil:
			pl = d.place(nil, r.ref, nil)
		case r.value == "":
			pl = Place{Key: PlaceNone}
		default:
			soc := ""
			if _, ok := socLabels[r.value]; ok {
				soc = r.value
			}
			raw := r.value
			pl = d.place(&raw, nil, &soc)
		}
		if o, ok := m[pl.Key]; ok {
			o.Count += r.n
			return
		}
		m[pl.Key] = &PlaceOption{Value: pl.Key, Label: pl.Label, IsUnresolved: strings.HasPrefix(pl.Key, placeRaw), Count: r.n}
	}
	for _, r := range rows {
		switch r.facet {
		case "total":
			out.Total = r.n
		case "incident":
			out.Incident = r.n
		case "status":
			out.StatusCounts[r.value] = r.n
		case "origin":
			addPlace(origins, r)
		case "destination":
			addPlace(destinations, r)
		case "plate":
			o := PlateOption{Value: BucketNone, Count: r.n}
			switch {
			case r.ref != nil:
				o.Value = plateID + r.ref.String()
				if r.label != "" {
					l := r.label
					o.Label = &l
				}
			case r.value != "":
				v := r.value
				o.Value, o.Label, o.IsOrphan = plateText+v, &v, true
			}
			if prev, ok := plates[o.Value]; ok {
				prev.Count += o.Count
			} else {
				plates[o.Value] = &o
			}
		case "vehicle_class":
			o := Option{Value: r.value, Label: r.value, Count: r.n}
			if r.value == "" {
				o.Value = BucketNone
			}
			out.VehicleClasses = append(out.VehicleClasses, o)
		case "partner":
			o := Option{Value: r.value, Label: r.value, Count: r.n}
			if r.value == "" {
				o.Value = BucketNone
			}
			out.Partners = append(out.Partners, o)
		}
	}
	out.Origins, out.Destinations = sortedPlaces(origins), sortedPlaces(destinations)
	for _, o := range plates {
		out.Plates = append(out.Plates, *o)
	}
	slices.SortFunc(out.Plates, func(a, b PlateOption) int {
		if c := noneLast(a.Value, b.Value); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(deref2(a.Label), deref2(b.Label)), cmp.Compare(a.Value, b.Value))
	})
	byLabel := func(a, b Option) int {
		if c := noneLast(a.Value, b.Value); c != 0 {
			return c
		}
		return cmp.Compare(a.Label, b.Label)
	}
	slices.SortFunc(out.VehicleClasses, byLabel)
	slices.SortFunc(out.Partners, byLabel)
	return out
}

// sortedPlaces orders place options as buildPlaceFilterOptions does: resolved places by label, then the unresolved
// raw values, then the none bucket.
func sortedPlaces(m map[string]*PlaceOption) []PlaceOption {
	out := make([]PlaceOption, 0, len(m))
	for _, o := range m {
		out = append(out, *o)
	}
	slices.SortFunc(out, func(a, b PlaceOption) int {
		if c := noneLast(a.Value, b.Value); c != 0 {
			return c
		}
		if a.IsUnresolved != b.IsUnresolved {
			if a.IsUnresolved {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Value, b.Value))
	})
	return out
}

func noneLast(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == BucketNone:
		return 1
	case b == BucketNone:
		return -1
	}
	return 0
}

func deref2(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
