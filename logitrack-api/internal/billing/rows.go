package billing

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// Row types of GET /v1/billing/rows (BillingTripRow.rowType) and the preview filters' bucket of rows without a value.
const (
	RowTrip          = "trip"
	RowMultidropStop = "multidrop_stop"
	RowStandby       = "standby"

	FilterNone = "__none__"
	// CustomerAll is the customerId of the every-party view (the delivered axis; documents are never made from it).
	CustomerAll = "all"
)

// Row flags (main spec §6.10 "Repair flags"; billing-document/page.tsx:75-101, 334-385). plan_date_drifted,
// plan_date_out_of_period and supp_priced_with_fuel are repaired by a forced recompute; supp_manual_price_with_fuel is a
// human check only; no_plan_date and standby_plan_other_month are informational; other_carrier marks a price another
// billing carrier stored, which never enters this carrier's invoice.
const (
	FlagPlanDateDrifted         = "plan_date_drifted"
	FlagPlanDateOutOfPeriod     = "plan_date_out_of_period"
	FlagNoPlanDate              = "no_plan_date"
	FlagStandbyPlanOtherMonth   = "standby_plan_other_month"
	FlagSuppPricedWithFuel      = "supp_priced_with_fuel"
	FlagSuppManualPriceWithFuel = "supp_manual_price_with_fuel"
	FlagOtherCarrier            = "other_carrier"
)

// BillingRow is one billed row of a party and month (BillingTripRow, web:lib/billingDocumentModel.ts:43-110), resolved
// for display in the server: a trip, one stop of a multi-drop trip ({tripId}_s{n}) or a standby record. inInvoiceSet is
// the statement's set for the type and category toggles (exactly the lines POST /v1/billing/statements would insert);
// inPreview narrows it by the review filters (plate, vehicle class, review month), which never change the invoice set
// (ADR 0005 §1-3).
type BillingRow struct {
	ID                     string     `json:"id"`
	RowType                string     `json:"rowType"`
	TripID                 *uuid.UUID `json:"tripId"`
	StandbyID              *uuid.UUID `json:"standbyId"`
	TaskID                 *uuid.UUID `json:"taskId"`
	TripNo                 *string    `json:"tripNo"`
	StopIndex              *int32     `json:"stopIndex"`
	DeliveredAt            *time.Time `json:"deliveredAt"` // a standby record's ended_at, else started_at
	BillingDate            *time.Time `json:"billingDate"`
	AxisDate               *time.Time `json:"axisDate"` // billingDate, else deliveredAt: the date the row bills on
	PlanDate               *time.Time `json:"planDate"`
	ActualPickupAt         *time.Time `json:"actualPickupAt"`
	BillingDateBasis       *string    `json:"billingDateBasis"` // single party only, never on standby
	EstimateTHB            float64    `json:"estimateThb"`
	BaseRateTHB            *float64   `json:"baseRateThb"`
	LookupHubCode          string     `json:"lookupHubCode"`
	LookupDestinationCode  string     `json:"lookupDestinationCode"`
	RateMultiplier         *float64   `json:"rateMultiplier"`
	AddTHBPerTrip          *float64   `json:"addThbPerTrip"`
	FuelAdjustmentID       *uuid.UUID `json:"fuelAdjustmentId"`
	ManualOverride         bool       `json:"manualOverride"`
	BillingPartyID         *uuid.UUID `json:"billingPartyId"`
	CustomerID             *uuid.UUID `json:"customerId"`
	VehicleClass           *string    `json:"vehicleClass"`
	DriverID               *uuid.UUID `json:"driverId"`
	DriverName             *string    `json:"driverName"`
	DriverPhone            *string    `json:"driverPhone"`
	SubcontractorName      *string    `json:"subcontractorName"`
	TruckID                *uuid.UUID `json:"truckId"`
	TruckLicensePlate      *string    `json:"truckLicensePlate"`
	HubDisplayName         string     `json:"hubDisplayName"`
	OriginHubCode          string     `json:"originHubCode"`
	DestinationDisplayName string     `json:"destinationDisplayName"`
	JobCategory            *string    `json:"jobCategory"` // PRIMARY | SUPPLEMENTARY | null (unverified: neither on the trip nor its task)
	RoundEffectiveFromDate *string    `json:"roundEffectiveFromDate"`
	FuelBandLowerTHB       *float64   `json:"fuelBandLowerThb"`
	FuelBandUpperTHB       *float64   `json:"fuelBandUpperThb"`
	ReferenceFuelPriceTHB  *float64   `json:"referenceFuelPriceThb"`
	InInvoiceSet           bool       `json:"inInvoiceSet"`
	InPreview              bool       `json:"inPreview"`
	Flags                  []string   `json:"flags"`

	// Invoice-set inputs, kept off the wire.
	carrierOK bool
	category  string // PRIMARY | SUPPLEMENTARY | unverified ("" on standby)
}

// UnpricedRow is a delivered trip or a completed standby record of the period without a price, with the reason (R62;
// main spec §6.14: "unpriced section with a dry-run reason"). It never enters an invoice set.
type UnpricedRow struct {
	ID                     string     `json:"id"`
	RowType                string     `json:"rowType"` // trip | standby
	TripID                 *uuid.UUID `json:"tripId"`
	StandbyID              *uuid.UUID `json:"standbyId"`
	TaskID                 *uuid.UUID `json:"taskId"`
	TripNo                 *string    `json:"tripNo"`
	DeliveredAt            *time.Time `json:"deliveredAt"` // a standby record's ended_at
	BillingDate            *time.Time `json:"billingDate"`
	PlanDate               *time.Time `json:"planDate"`
	CreatedAt              time.Time  `json:"createdAt"`
	BillingPartyID         *uuid.UUID `json:"billingPartyId"` // stored, else resolved from the task (standby: its own party first)
	CustomerID             *uuid.UUID `json:"customerId"`
	SourceHub              *string    `json:"sourceHub"`   // the task's raw value (standby: start location when no task)
	Destination            *string    `json:"destination"` // likewise
	HubDisplayName         string     `json:"hubDisplayName"`
	DestinationDisplayName string     `json:"destinationDisplayName"`
	VehicleClass           *string    `json:"vehicleClass"`
	LookupHubCode          *string    `json:"lookupHubCode"`
	LookupDestinationCode  *string    `json:"lookupDestinationCode"`
	LookupVehicleClass     *string    `json:"lookupVehicleClass"`
	DriverID               *uuid.UUID `json:"driverId"`
	DriverName             *string    `json:"driverName"`
	JobCategory            *string    `json:"jobCategory"`
	DurationMinutes        *int32     `json:"durationMinutes,omitempty"`
	Reason                 string     `json:"reason"`
	ReasonSource           string     `json:"reasonSource"`      // stored | dry_run
	DryRunEstimateTHB      *float64   `json:"dryRunEstimateThb"` // what the rules would price (reason not_computed)
}

// Totals are the sums of a set of rows (computeBillingTotals, billing-document/page.tsx:146-166) with the withholding
// of the rate the statement would store (the default issuer company's, R18; null when the billing tenant has none).
type Totals struct {
	GrandTotal         float64  `json:"grandTotal"`
	WithholdingTaxRate *float64 `json:"withholdingTaxRate"`
	WithholdingTax     float64  `json:"withholdingTax"`
	Net                float64  `json:"net"`
	RowCount           int      `json:"rowCount"`
	TripOnlyCount      int      `json:"tripOnlyCount"`
	TripSubtotal       float64  `json:"tripSubtotal"`
	StandbyCount       int      `json:"standbyCount"`
	StandbySubtotal    float64  `json:"standbySubtotal"`
	MultiDropCount     int      `json:"multiDropCount"`
	MultiDropSubtotal  float64  `json:"multiDropSubtotal"`
}

// FilterOption is one choice of a preview filter, built from the invoice set (every option matches a billable row).
type FilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// RowIssues summarises the flags of the party's priced rows, whatever the toggles (hiding a type never hides a problem
// in it); repairTripIds are the trips a forced recompute repairs.
type RowIssues struct {
	PlanDateDrifted         int         `json:"planDateDrifted"`
	PlanDateOutOfPeriod     int         `json:"planDateOutOfPeriod"`
	NoPlanDate              int         `json:"noPlanDate"`
	StandbyPlanOtherMonth   int         `json:"standbyPlanOtherMonth"`
	SuppPricedWithFuel      int         `json:"suppPricedWithFuel"`
	SuppManualPriceWithFuel int         `json:"suppManualPriceWithFuel"`
	RepairTripIDs           []uuid.UUID `json:"repairTripIds"`
}

// Rows is the answer of GET /v1/billing/rows.
type Rows struct {
	CustomerID     string                    `json:"customerId"`
	BillingPartyID *uuid.UUID                `json:"billingPartyId"`
	Year           int                       `json:"year"`
	Month          int                       `json:"month"`
	Basis          *string                   `json:"basis"` // plan | delivered for one party; null for all
	PeriodFrom     time.Time                 `json:"periodFrom"`
	PeriodTo       time.Time                 `json:"periodTo"`
	Types          []string                  `json:"types"`
	Categories     []string                  `json:"categories"`
	Rows           []BillingRow              `json:"rows"`
	Unpriced       []UnpricedRow             `json:"unpriced"`
	Invoice        Totals                    `json:"invoice"`
	Preview        Totals                    `json:"preview"`
	TypeCounts     map[string]int            `json:"typeCounts"`
	CategoryCounts map[string]int            `json:"categoryCounts"`
	Issues         RowIssues                 `json:"issues"`
	FilterOptions  map[string][]FilterOption `json:"filterOptions"`
	OutsideReach   int                       `json:"outsideReach"` // invoice-set rows of the month the caller cannot see (POST /v1/billing/statements refuses them)
	Documentable   bool                      `json:"documentable"` // one party, nothing outside reach, at least one row in the invoice set
}

// RowsInput are the query parameters of GET /v1/billing/rows: the period, the invoice toggles (absent = all) and the
// preview filters (absent = none).
type RowsInput struct {
	CustomerID    string
	Year, Month   int
	Types         []string
	TypesSet      bool
	Categories    []string
	CategoriesSet bool
	Plate         string
	VehicleClass  string
	ReviewMonth   string
}

// rowsScope is the resolved party and month of a read.
type rowsScope struct {
	all      bool
	party    billingdb.ResolvePartyRow
	found    bool
	plan     bool
	from, to time.Time
	tenant   uuid.UUID
	ownFleet bool
}

// periodChecks validates a year and month.
func (ck *checker) period(year, month int) {
	if year < 2000 || year > 2999 {
		ck.add("year", "out_of_range")
	}
	if month < 1 || month > 12 {
		ck.add("month", "out_of_range")
	}
}

// readScope resolves the billing tenant (own fleet only, as the statements it previews) and the party of a read.
func (s *Service) readScope(ctx context.Context, p *authz.Principal, customer string, year, month int, allowAll bool) (rowsScope, error) {
	tenant, err := billingTenantOf(p)
	if err != nil {
		return rowsScope{}, err
	}
	var ck checker
	ck.period(year, month)
	sc := rowsScope{tenant: tenant, ownFleet: p.TenantKind == authz.TenantKindOwnFleet}
	var id uuid.UUID
	if allowAll && strings.TrimSpace(customer) == CustomerAll {
		sc.all = true
	} else {
		id = ck.party("customerId", customer)
	}
	if err := ck.err(); err != nil {
		return rowsScope{}, err
	}
	sc.from, sc.to = bangkokMonth(year, month)
	if sc.all {
		return sc, nil
	}
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		sc.party, sc.found, err = resolveParty(ctx, q, id)
		return err
	})
	sc.plan = sc.found && sc.party.BillingDateBasis == string(compute.BasisPlan)
	return sc, err
}

// BillingRows is GET /v1/billing/rows (accounting:billing_document; W8): one request for what fetchBillingTripRows
// assembled in six browser round trips (logitrack-web/features/accounting/api/billing.ts:737-1094) plus the page's
// toggles, review filters, totals and repair flags. A single party's trips are read on its axis (billing_date for a plan
// basis, else delivered_at) inside the Bangkok month, its standby records on ended_at; customerId=all reads every party
// on the delivered axis. Priced rows carry inInvoiceSet (the statement's set: TestRowsInvoiceSetEqualsStatementLines)
// and inPreview; rows without a price are in unpriced with their reason (stored, else a dry run of the pricing rules).
// Only the own fleet reads it, as only it bills (main spec §19 Q13); an id that names no party answers an empty period.
func (s *Service) BillingRows(ctx context.Context, p *authz.Principal, in RowsInput) (*Rows, error) {
	sc, err := s.readScope(ctx, p, in.CustomerID, in.Year, in.Month, true)
	if err != nil {
		return nil, err
	}
	var ck checker
	types := ck.toggleList("types", in.Types, in.TypesSet, invoiceTypes)
	categories := ck.toggleList("categories", in.Categories, in.CategoriesSet, invoiceCategories)
	if v := strings.TrimSpace(in.ReviewMonth); v != "" && v != FilterNone && !validMonthKey(v) {
		ck.add("reviewMonth", "invalid")
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	out := &Rows{CustomerID: strings.TrimSpace(in.CustomerID), Year: in.Year, Month: in.Month, PeriodFrom: sc.from.UTC(),
		PeriodTo: sc.to.UTC(), Types: types, Categories: categories, Rows: []BillingRow{}, Unpriced: []UnpricedRow{},
		TypeCounts:     map[string]int{RowTrip: 0, RowStandby: 0, RowMultidropStop: 0},
		CategoryCounts: map[string]int{CategoryPrimary: 0, CategorySupplementary: 0, CategoryUnverified: 0},
		FilterOptions:  map[string][]FilterOption{"plates": {}, "vehicleClasses": {}, "reviewMonths": {}},
		Issues:         RowIssues{RepairTripIDs: []uuid.UUID{}}}
	if !sc.all {
		if !sc.found {
			return out, nil
		}
		party := sc.party.ID
		out.BillingPartyID = &party
		basis := string(compute.BasisDelivered)
		if sc.plan {
			basis = string(compute.BasisPlan)
		}
		out.Basis = &basis
	}
	var rate *float64
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		out.Rows, out.Unpriced, err = s.readRows(ctx, q, sc)
		if err != nil {
			return err
		}
		cs, err := q.IssuerCompanies(ctx, sc.tenant)
		if err == nil && (len(cs) == 1 || (len(cs) > 0 && cs[0].CompanyType == "owner")) {
			r := cs[0].WithholdingTaxRate
			rate = &r
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.applySets(out, sc, types, categories, in)
	out.Invoice = totals(out.Rows, func(r BillingRow) bool { return r.InInvoiceSet }, rate)
	out.Preview = totals(out.Rows, func(r BillingRow) bool { return r.InPreview }, rate)
	if !sc.all {
		var whole billingdb.StatementInvoiceSetRow
		if err := readShared(ctx, s.pool, func(q *billingdb.Queries) error {
			var err error
			whole, err = q.StatementInvoiceSet(ctx, billingdb.StatementInvoiceSetParams{PartyID: sc.party.ID,
				TenantID: sc.tenant, PlanBasis: sc.plan, PeriodFrom: sc.from, PeriodTo: sc.to,
				IncludeTrips: slices.Contains(types, TypeTrip), IncludeMultidrop: slices.Contains(types, TypeMultidrop),
				IncludeStandby: slices.Contains(types, TypeStandby), Categories: categories, OwnFleet: sc.ownFleet})
			return err
		}); err != nil {
			return nil, err
		}
		out.OutsideReach = max(0, int(whole.LineCount)-out.Invoice.RowCount)
		out.Documentable = out.OutsideReach == 0 && out.Invoice.RowCount > 0
	}
	return out, nil
}

// toggleList is checker.toggles for a query list: absent = all, present and empty (or an unknown value) refused.
func (ck *checker) toggleList(field string, v []string, set bool, allowed []string) []string {
	if !set {
		return slices.Clone(allowed)
	}
	return ck.toggles(field, &v, allowed)
}

func validMonthKey(s string) bool {
	if len(s) != 7 || s[4] != '-' {
		return false
	}
	_, err := time.Parse("2006-01", s)
	return err == nil
}

// readRows reads and resolves the period's trips and standby records in the request transaction.
func (s *Service) readRows(ctx context.Context, q *billingdb.Queries, sc rowsScope) ([]BillingRow, []UnpricedRow, error) {
	trips, err := q.BillingRowTrips(ctx, billingdb.BillingRowTripsParams{AllParties: sc.all, PeriodFrom: sc.from,
		PeriodTo: sc.to, PartyID: sc.party.ID, PlanBasis: sc.plan})
	if err != nil {
		return nil, nil, err
	}
	standby, err := q.BillingRowStandby(ctx, billingdb.BillingRowStandbyParams{PeriodFrom: sc.from, PeriodTo: sc.to,
		AllParties: sc.all, PartyID: sc.party.ID})
	if err != nil {
		return nil, nil, err
	}
	hubs, err := q.DisplayHubs(ctx)
	if err != nil {
		return nil, nil, err
	}
	disp := newHubDisplay(hubs)

	var multi, unpricedMulti []uuid.UUID
	parties := map[uuid.UUID]bool{}
	priced := func(r billingdb.BillingRowTripsRow) bool {
		return r.HasSnapshot && r.EstimateThb != nil && *r.EstimateThb != 0
	}
	for _, r := range trips {
		switch {
		case priced(r) && r.SnapshotMulti != nil && *r.SnapshotMulti:
			multi = append(multi, r.ID)
		case !priced(r):
			if r.IsMultiDelivery {
				unpricedMulti = append(unpricedMulti, r.ID)
			}
			if id := tripParty(r); id != uuid.Nil {
				parties[id] = true
			}
		}
	}
	for _, r := range standby {
		if r.BillingEstimateThb == nil || *r.BillingEstimateThb == 0 {
			if id := standbyParty(r); id != uuid.Nil {
				parties[id] = true
			}
		}
	}
	stops := map[uuid.UUID][]billingdb.TripBillingStopBreakdown{}
	if len(multi) > 0 {
		rows, err := q.BillingRowStops(ctx, multi)
		if err != nil {
			return nil, nil, err
		}
		for _, st := range rows {
			stops[st.TripID] = append(stops[st.TripID], st)
		}
	}
	progress := map[uuid.UUID][]billingdb.BillingRowTripStopsRow{}
	if len(unpricedMulti) > 0 {
		rows, err := q.BillingRowTripStops(ctx, unpricedMulti)
		if err != nil {
			return nil, nil, err
		}
		for _, st := range rows {
			progress[st.TripID] = append(progress[st.TripID], st)
		}
	}
	var dry *dryRun
	if len(parties) > 0 || len(trips) > 0 || len(standby) > 0 {
		aliases, err := q.DisplayHubAliases(ctx)
		if err != nil {
			return nil, nil, err
		}
		ids := make([]uuid.UUID, 0, len(parties))
		for id := range parties {
			ids = append(ids, id)
		}
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		if dry, err = loadDryRun(ctx, q, sc.tenant, hubMaps(hubs, aliases), ids); err != nil {
			return nil, nil, err
		}
	}
	// The billed id (customer or carrier) of every party read, for customerId on the rows.
	billed := map[uuid.UUID]uuid.UUID{}
	if dry != nil {
		for id, ref := range dry.parties {
			billed[id] = ref.BilledID
		}
	}
	var pricedParties []uuid.UUID
	for _, r := range trips {
		if r.BillingPartyID != nil && billed[*r.BillingPartyID] == uuid.Nil {
			pricedParties = append(pricedParties, *r.BillingPartyID)
		}
	}
	for _, r := range standby {
		if r.BillingPartyID != nil && billed[*r.BillingPartyID] == uuid.Nil {
			pricedParties = append(pricedParties, *r.BillingPartyID)
		}
	}
	if len(pricedParties) > 0 {
		refs, err := q.PartyRefs(ctx, pricedParties)
		if err != nil {
			return nil, nil, err
		}
		for _, ref := range refs {
			billed[ref.ID] = ref.BilledID
		}
	}

	var out []BillingRow
	var unpriced []UnpricedRow
	basis := (*string)(nil)
	if !sc.all {
		b := string(compute.BasisDelivered)
		if sc.plan {
			b = string(compute.BasisPlan)
		}
		basis = &b
	}
	for _, r := range trips {
		if !priced(r) {
			unpriced = append(unpriced, unpricedTrip(r, disp, dry, billed, progress[r.ID]))
			continue
		}
		base := pricedTrip(r, disp, basis, billed, sc)
		if r.SnapshotMulti != nil && *r.SnapshotMulti && len(stops[r.ID]) > 0 {
			for _, st := range stops[r.ID] {
				if st.FinalRateThb == 0 {
					continue
				}
				row := base
				row.ID = r.ID.String() + "_s" + strconv.Itoa(int(st.StopIndex))
				row.RowType = RowMultidropStop
				idx := st.StopIndex
				row.StopIndex = &idx
				no := r.TripNo + "-s" + strconv.Itoa(int(st.StopIndex))
				row.TripNo = &no
				row.EstimateTHB = st.FinalRateThb
				row.BaseRateTHB = nonZero(&st.BaseRateThb)
				row.LookupDestinationCode = st.DestinationCode
				row.DestinationDisplayName = disp.name(st.DestinationCode)
				row.Flags = slices.Clone(base.Flags)
				out = append(out, row)
			}
			continue
		}
		out = append(out, base)
	}
	for _, r := range standby {
		if r.BillingEstimateThb == nil || *r.BillingEstimateThb == 0 {
			unpriced = append(unpriced, unpricedStandby(r, disp, dry, billed))
			continue
		}
		out = append(out, pricedStandby(r, disp, billed, sc))
	}
	// The billing axis order (billingAxisDate, web:lib/billingDocument.ts:124-126), stable: what the invoice and the
	// detail sheet print.
	slices.SortStableFunc(out, func(a, b BillingRow) int { return cmpMillis(a.AxisDate, b.AxisDate) })
	slices.SortStableFunc(unpriced, func(a, b UnpricedRow) int { return cmpMillis(a.DeliveredAt, b.DeliveredAt) })
	if out == nil {
		out = []BillingRow{}
	}
	if unpriced == nil {
		unpriced = []UnpricedRow{}
	}
	return out, unpriced, nil
}

func cmpMillis(a, b *time.Time) int {
	ms := func(t *time.Time) int64 {
		if t == nil {
			return 0
		}
		return t.UnixMilli()
	}
	x, y := ms(a), ms(b)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// tripParty is the trip's party: the stamped one, else the task's (ResolveTaskParty); uuid.Nil when none.
func tripParty(r billingdb.BillingRowTripsRow) uuid.UUID {
	for _, id := range []*uuid.UUID{r.BillingPartyID, r.TaskPartyID, r.SourceLinkedPartyID, r.DestinationLinkedPartyID} {
		if id != nil && *id != uuid.Nil {
			return *id
		}
	}
	return uuid.Nil
}

// standbyParty is the standby record's party: the priced one, else its own, else the task's source then destination
// link (never the task's explicit billing party, §6.18 #9).
func standbyParty(r billingdb.BillingRowStandbyRow) uuid.UUID {
	for _, id := range []*uuid.UUID{r.BillingPartyID, r.CustomerPartyID, r.SourceLinkedPartyID, r.DestinationLinkedPartyID} {
		if id != nil && *id != uuid.Nil {
			return *id
		}
	}
	return uuid.Nil
}

func idStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func nonZero(v *float64) *float64 {
	if v == nil || *v == 0 {
		return nil
	}
	x := *v
	return &x
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timeUTC(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func dateStr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func firstID(ids ...*uuid.UUID) *uuid.UUID {
	for _, id := range ids {
		if id != nil && *id != uuid.Nil {
			return id
		}
	}
	return nil
}

func firstStr(vals ...*string) *string {
	for _, v := range vals {
		if v != nil && strings.TrimSpace(*v) != "" {
			return v
		}
	}
	return nil
}

func customerOf(billed map[uuid.UUID]uuid.UUID, party *uuid.UUID) *uuid.UUID {
	if party == nil {
		return nil
	}
	if c, ok := billed[*party]; ok && c != uuid.Nil {
		return &c
	}
	return nil
}

// jobCategoryOf is the display category (ResolveDisplayJobCategory: the trip's, else the task's) and the toggle bucket.
func jobCategoryOf(trip, task *string) (*string, string) {
	if c, ok := compute.ResolveDisplayJobCategory(deref(trip), deref(task)); ok {
		s := string(c)
		return &s, s
	}
	return nil, CategoryUnverified
}

func pricedTrip(r billingdb.BillingRowTripsRow, disp hubDisplay, basis *string, billed map[uuid.UUID]uuid.UUID, sc rowsScope) BillingRow {
	id := r.ID
	hub := deref(r.LookupHubCode)
	dest := deref(r.LookupDestinationCode)
	cat, bucket := jobCategoryOf(r.TripJobCategory, r.TaskJobCategory)
	row := BillingRow{
		ID: r.ID.String(), RowType: RowTrip, TripID: &id, TaskID: r.TaskID, TripNo: &r.TripNo,
		DeliveredAt: timeUTC(r.DeliveredAt), BillingDate: timeUTC(r.BillingDate), PlanDate: timeUTC(r.PlanAt),
		ActualPickupAt: timeUTC(r.ActualPickupAt), BillingDateBasis: basis, EstimateTHB: *r.EstimateThb,
		BaseRateTHB: nonZero(r.BaseRateThb), LookupHubCode: hub, LookupDestinationCode: dest,
		RateMultiplier: nonZero(r.RateMultiplier), AddTHBPerTrip: nonZero(r.AddThbPerTrip),
		FuelAdjustmentID: r.FuelAdjustmentID, ManualOverride: r.ManualOverride != nil && *r.ManualOverride,
		BillingPartyID: r.BillingPartyID, CustomerID: customerOf(billed, r.BillingPartyID),
		VehicleClass: firstStr(r.TruckType, r.TripVehicleClass), DriverID: r.DriverID,
		DriverName:  strPtr(driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)),
		DriverPhone: r.DriverMobile, SubcontractorName: strPtr(subcontractor(r.DriverTenantKind, r.DriverTenantName)),
		TruckID: firstID(r.TaskTruckID, r.TripTruckID), TruckLicensePlate: firstStr(r.TaskPlate, r.TripPlate),
		HubDisplayName:         disp.name(hub),
		OriginHubCode:          disp.code(firstNonBlank(hub, deref(r.SourceHubRaw))),
		DestinationDisplayName: disp.name(dest), JobCategory: cat,
		RoundEffectiveFromDate: dateStr(r.RoundEffectiveFromDate), FuelBandLowerTHB: r.FuelBandLowerThb,
		FuelBandUpperTHB: r.FuelBandUpperThb, ReferenceFuelPriceTHB: r.ReferenceFuelPriceThb, Flags: []string{},
		carrierOK: r.SnapshotTenantID != nil && *r.SnapshotTenantID == sc.tenant, category: bucket,
	}
	row.AxisDate = row.BillingDate
	if row.AxisDate == nil {
		row.AxisDate = row.DeliveredAt
	}
	if !row.carrierOK {
		row.Flags = append(row.Flags, FlagOtherCarrier)
	}
	if bucket == CategorySupplementary {
		var fuelID *string
		if r.FuelAdjustmentID != nil {
			s := r.FuelAdjustmentID.String()
			fuelID = &s
		}
		if compute.CarriesFuel(compute.Snapshot{FuelAdjustmentID: fuelID, RateMultiplier: r.RateMultiplier,
			AddTHBPerTrip: r.AddThbPerTrip}) {
			if row.ManualOverride {
				row.Flags = append(row.Flags, FlagSuppManualPriceWithFuel)
			} else {
				row.Flags = append(row.Flags, FlagSuppPricedWithFuel)
			}
		}
	}
	if sc.plan && !sc.all {
		periodKey := monthKeyOf(sc.from)
		switch {
		case row.PlanDate == nil:
			row.Flags = append(row.Flags, FlagNoPlanDate)
		case monthKeyOf(*row.PlanDate) != periodKey:
			row.Flags = append(row.Flags, FlagPlanDateOutOfPeriod)
		case row.BillingDate != nil && clock.DateString(*row.PlanDate) != clock.DateString(*row.BillingDate):
			row.Flags = append(row.Flags, FlagPlanDateDrifted)
		}
	}
	return row
}

func monthKeyOf(t time.Time) string { return clock.DateString(t)[:7] }

// standbyCarrier is the billing carrier of a priced standby record: the one recorded with the price (0015), else its
// rate entry's, else the own fleet (the statement's rule; operations.BillingVisible).
func standbyCarrier(r billingdb.BillingRowStandbyRow, sc rowsScope) (uuid.UUID, bool) {
	switch {
	case r.BillingTenantID != nil:
		return *r.BillingTenantID, true
	case r.RateEntryTenantID != nil:
		return *r.RateEntryTenantID, true
	case sc.ownFleet:
		return sc.tenant, true
	}
	return uuid.Nil, false
}

func pricedStandby(r billingdb.BillingRowStandbyRow, disp hubDisplay, billed map[uuid.UUID]uuid.UUID, sc rowsScope) BillingRow {
	id := r.ID
	src := firstStr(r.SourceHubRaw, r.StartLocationRaw)
	dst := firstStr(r.DestinationRaw, r.EndLocationRaw)
	cat, _ := jobCategoryOf(r.StandbyJobCategory, r.TaskJobCategory)
	carrier, ok := standbyCarrier(r, sc)
	row := BillingRow{
		ID: r.ID.String(), RowType: RowStandby, StandbyID: &id, TaskID: r.TaskID,
		TripNo: firstStr(r.TripNo, r.MigratedFromTripNo), DeliveredAt: timeUTC(firstTime(r.EndedAt, r.StartedAt)),
		PlanDate: timeUTC(r.PlanAt), ActualPickupAt: timeUTC(r.ActualPickupAt), EstimateTHB: *r.BillingEstimateThb,
		BillingPartyID: r.BillingPartyID, CustomerID: customerOf(billed, r.BillingPartyID), VehicleClass: r.TruckType,
		DriverID: r.DriverID, DriverName: strPtr(driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)),
		DriverPhone: r.DriverMobile, SubcontractorName: strPtr(subcontractor(r.DriverTenantKind, r.DriverTenantName)),
		TruckID: firstID(r.TaskTruckID, r.StandbyTruckID), TruckLicensePlate: firstStr(r.TaskPlate, r.StandbyPlate),
		HubDisplayName: disp.name(deref(src)), OriginHubCode: disp.code(deref(src)), DestinationDisplayName: disp.name(deref(dst)),
		JobCategory: cat, Flags: []string{}, carrierOK: ok && carrier == sc.tenant,
	}
	row.AxisDate = row.DeliveredAt
	if !row.carrierOK {
		row.Flags = append(row.Flags, FlagOtherCarrier)
	}
	if sc.plan && !sc.all && row.PlanDate != nil && monthKeyOf(*row.PlanDate) != monthKeyOf(sc.from) {
		row.Flags = append(row.Flags, FlagStandbyPlanOtherMonth)
	}
	return row
}

func firstTime(ts ...*time.Time) *time.Time {
	for _, t := range ts {
		if t != nil && !t.IsZero() {
			return t
		}
	}
	return nil
}

func unpricedTrip(r billingdb.BillingRowTripsRow, disp hubDisplay, dry *dryRun, billed map[uuid.UUID]uuid.UUID,
	progress []billingdb.BillingRowTripStopsRow) UnpricedRow {
	id := r.ID
	party := tripParty(r)
	var partyPtr *uuid.UUID
	if party != uuid.Nil {
		partyPtr = &party
	}
	cat, _ := jobCategoryOf(r.TripJobCategory, r.TaskJobCategory)
	u := UnpricedRow{
		ID: r.ID.String(), RowType: RowTrip, TripID: &id, TaskID: r.TaskID, TripNo: &r.TripNo,
		DeliveredAt: timeUTC(r.DeliveredAt), BillingDate: timeUTC(r.BillingDate), PlanDate: timeUTC(r.PlanAt),
		CreatedAt: r.CreatedAt.UTC(), BillingPartyID: partyPtr, CustomerID: customerOf(billed, partyPtr),
		SourceHub: r.SourceHubRaw, Destination: r.DestinationRaw, VehicleClass: firstStr(r.TruckType, r.TripVehicleClass),
		DriverID: r.DriverID, DriverName: strPtr(driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)),
		JobCategory:            cat,
		HubDisplayName:         disp.name(firstNonBlank(deref(r.SourceHubRaw), deref(r.OriginRaw))),
		DestinationDisplayName: disp.name(firstNonBlank(deref(r.DestinationRaw), deref(r.TripDestinationRaw))),
	}
	if r.HasSnapshot && r.UnpricedReason != nil {
		u.Reason, u.ReasonSource = *r.UnpricedReason, ReasonStored
		return u
	}
	u.ReasonSource = ReasonDryRun
	if !r.HasTask {
		u.Reason = ReasonTaskMissing
		return u
	}
	in := compute.TripInput{
		Task: compute.TaskInput{TaskParties: compute.TaskParties{BillingPartyID: idStr(r.TaskPartyID),
			SourceLinkedPartyID: idStr(r.SourceLinkedPartyID), DestinationLinkedPartyID: idStr(r.DestinationLinkedPartyID)},
			SourceHub: deref(r.SourceHubRaw), Destination: deref(r.DestinationRaw), TruckType: deref(r.TruckType)},
		JobCategory: deref(r.TaskJobCategory), CreatedAt: r.CreatedAt, IsMultiDelivery: r.IsMultiDelivery,
		StopProgressCount: len(progress),
	}
	if r.BillingPartyID != nil { // a stamped party wins over the task's links, as pricing stamped it
		in.Task.BillingPartyID = r.BillingPartyID.String()
	}
	if r.PlanAt != nil {
		in.PlanAt = *r.PlanAt
	}
	if r.DeliveredAt != nil {
		in.DeliveredAt = *r.DeliveredAt
	}
	for _, st := range progress {
		if st.Status == "delivered" && strings.TrimSpace(st.DestinationRaw) != "" {
			in.DeliveredStops = append(in.DeliveredStops, st.DestinationRaw)
		}
	}
	v := dry.trip(party, in)
	u.Reason, u.DryRunEstimateTHB = v.reason, v.estimate
	u.LookupHubCode, u.LookupDestinationCode, u.LookupVehicleClass = strPtr(v.hubCode), strPtr(v.destCode), strPtr(v.class)
	return u
}

func unpricedStandby(r billingdb.BillingRowStandbyRow, disp hubDisplay, dry *dryRun, billed map[uuid.UUID]uuid.UUID) UnpricedRow {
	id := r.ID
	party := standbyParty(r)
	var partyPtr *uuid.UUID
	if party != uuid.Nil {
		partyPtr = &party
	}
	src := firstStr(r.SourceHubRaw, r.StartLocationRaw)
	dst := firstStr(r.DestinationRaw, r.EndLocationRaw)
	cat, _ := jobCategoryOf(r.StandbyJobCategory, r.TaskJobCategory)
	u := UnpricedRow{
		ID: r.ID.String(), RowType: RowStandby, StandbyID: &id, TaskID: r.TaskID, TripNo: firstStr(r.TripNo, r.MigratedFromTripNo),
		DeliveredAt: timeUTC(r.EndedAt), PlanDate: timeUTC(r.PlanAt), CreatedAt: r.CreatedAt.UTC(),
		BillingPartyID: partyPtr, CustomerID: customerOf(billed, partyPtr), SourceHub: src, Destination: dst,
		HubDisplayName: disp.name(deref(src)), DestinationDisplayName: disp.name(deref(dst)), VehicleClass: r.TruckType,
		DriverID: r.DriverID, DriverName: strPtr(driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)),
		JobCategory: cat, DurationMinutes: r.DurationMinutes,
	}
	if r.BillingUnpricedReason != nil {
		u.Reason, u.ReasonSource = *r.BillingUnpricedReason, ReasonStored
		return u
	}
	u.ReasonSource = ReasonDryRun
	in := compute.StandbyInput{CustomerPartyID: idStr(firstID(r.BillingPartyID, r.CustomerPartyID)),
		TaskSourceLinkedPartyID: idStr(r.SourceLinkedPartyID), TaskDestinationLinkedPartyID: idStr(r.DestinationLinkedPartyID),
		CreatedAt: r.CreatedAt}
	if r.EndedAt != nil {
		in.EndedAt = *r.EndedAt
	}
	if r.StartedAt != nil {
		in.StartedAt = *r.StartedAt
	}
	u.Reason, u.DryRunEstimateTHB = dry.standby(in)
	return u
}

// applySets marks the invoice set and the preview set, counts the toggles' buckets, builds the filter options from
// the invoice set and sums the issues.
func (s *Service) applySets(out *Rows, sc rowsScope, types, categories []string, in RowsInput) {
	reviewPlan := !sc.plan // the review month reads the axis the period was not cut on
	plateOpts := map[string]*FilterOption{}
	classOpts := map[string]*FilterOption{}
	monthOpts := map[string]*FilterOption{}
	var plateOrder, classOrder, monthOrder []string
	add := func(m map[string]*FilterOption, order *[]string, key, label string) {
		if o, ok := m[key]; ok {
			o.Count++
			if o.Label == "" {
				o.Label = label
			}
			return
		}
		m[key] = &FilterOption{Value: key, Label: label, Count: 1}
		*order = append(*order, key)
	}
	repair := map[uuid.UUID]bool{}
	for i := range out.Rows {
		r := &out.Rows[i]
		typeKey := r.RowType
		out.TypeCounts[typeKey]++
		if r.RowType != RowStandby {
			out.CategoryCounts[r.category]++
		}
		for _, f := range r.Flags {
			switch f {
			case FlagPlanDateDrifted:
				out.Issues.PlanDateDrifted++
			case FlagPlanDateOutOfPeriod:
				out.Issues.PlanDateOutOfPeriod++
			case FlagNoPlanDate:
				out.Issues.NoPlanDate++
			case FlagStandbyPlanOtherMonth:
				out.Issues.StandbyPlanOtherMonth++
			case FlagSuppPricedWithFuel:
				out.Issues.SuppPricedWithFuel++
			case FlagSuppManualPriceWithFuel:
				out.Issues.SuppManualPriceWithFuel++
			}
			if (f == FlagPlanDateDrifted || f == FlagPlanDateOutOfPeriod || f == FlagSuppPricedWithFuel) && r.TripID != nil {
				repair[*r.TripID] = true
			}
		}
		r.InInvoiceSet = rowInInvoiceSet(*r, sc, types, categories)
		if !r.InInvoiceSet {
			continue
		}
		pk, pl := plateKey(r)
		add(plateOpts, &plateOrder, pk, pl)
		ck := classKey(r.VehicleClass)
		cl := ck
		if ck == FilterNone {
			cl = ""
		}
		add(classOpts, &classOrder, ck, cl)
		mk := reviewKey(r, reviewPlan)
		add(monthOpts, &monthOrder, mk, mk)
		r.InPreview = (in.Plate == "" || in.Plate == "all" || pk == in.Plate) &&
			(in.VehicleClass == "" || in.VehicleClass == "all" || ck == in.VehicleClass) &&
			(in.ReviewMonth == "" || in.ReviewMonth == "all" || mk == in.ReviewMonth)
	}
	for id := range repair {
		out.Issues.RepairTripIDs = append(out.Issues.RepairTripIDs, id)
	}
	slices.SortFunc(out.Issues.RepairTripIDs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	sorted := func(m map[string]*FilterOption, order []string, byLabel bool) []FilterOption {
		list := make([]FilterOption, 0, len(order))
		for _, k := range order {
			list = append(list, *m[k])
		}
		slices.SortStableFunc(list, func(a, b FilterOption) int {
			switch {
			case a.Value == FilterNone && b.Value != FilterNone:
				return 1
			case b.Value == FilterNone && a.Value != FilterNone:
				return -1
			case byLabel:
				return strings.Compare(a.Label, b.Label)
			}
			return strings.Compare(a.Value, b.Value)
		})
		return list
	}
	out.FilterOptions["plates"] = sorted(plateOpts, plateOrder, true)
	out.FilterOptions["vehicleClasses"] = sorted(classOpts, classOrder, true)
	out.FilterOptions["reviewMonths"] = sorted(monthOpts, monthOrder, false)
}

// rowInInvoiceSet is the statement's invoice set (InsertStatementLines): the party's priced row of this billing carrier
// on the period's axis, its type toggled on and, for a trip or a stop, its category toggled on (standby exempt). The
// every-party view keeps the toggles and the carrier only.
func rowInInvoiceSet(r BillingRow, sc rowsScope, types, categories []string) bool {
	if !r.carrierOK || r.EstimateTHB == 0 {
		return false
	}
	if !sc.all {
		// The period axis of the statement: billing_date for a plan-basis party's trips (a trip without one sits in no
		// plan period), delivered_at for the others, ended_at for standby (deliveredAt of a standby row).
		axis := r.DeliveredAt
		if r.RowType != RowStandby && sc.plan {
			axis = r.BillingDate
		}
		if r.BillingPartyID == nil || *r.BillingPartyID != sc.party.ID || axis == nil || axis.Before(sc.from) ||
			!axis.Before(sc.to) {
			return false
		}
	}
	switch r.RowType {
	case RowTrip:
		return slices.Contains(types, TypeTrip) && slices.Contains(categories, r.category)
	case RowMultidropStop:
		return slices.Contains(types, TypeMultidrop) && slices.Contains(categories, r.category)
	case RowStandby:
		return slices.Contains(types, TypeStandby)
	}
	return false
}

// plateKey is truckFilterKey (web:lib/truckPlate.ts): id:{truckId}, else plate:{plate}, else __none__, and the label.
func plateKey(r *BillingRow) (string, string) {
	plate := strings.TrimSpace(deref(r.TruckLicensePlate))
	if r.TruckID != nil && *r.TruckID != uuid.Nil {
		return "id:" + r.TruckID.String(), plate
	}
	if plate != "" {
		return "plate:" + plate, plate
	}
	return FilterNone, ""
}

// classKey is resolveVehicleClassKey (web:lib/vehicleClass.ts): the folded class, __none__ when blank (never 4WJ).
func classKey(vc *string) string {
	if c, ok := compute.FoldVehicleClass(deref(vc)); ok {
		return c
	}
	return FilterNone
}

// reviewKey is the Bangkok yyyy-MM of the row on the review axis (the plan date, or the delivery for a plan-cut
// period); __none__ without one.
func reviewKey(r *BillingRow, planAxis bool) string {
	t := r.DeliveredAt
	if planAxis {
		t = r.PlanDate
	}
	if t == nil {
		return FilterNone
	}
	return monthKeyOf(*t)
}

// totals sums the rows a set keeps, in row order (float sums, as the browser), and the withholding of rate on the
// two-decimal total (the statement's arithmetic: exact sums of two-decimal amounts, WithholdingTHB of the stored rate).
func totals(rows []BillingRow, in func(BillingRow) bool, rate *float64) Totals {
	t := Totals{WithholdingTaxRate: rate}
	for _, r := range rows {
		if !in(r) {
			continue
		}
		t.RowCount++
		t.GrandTotal += r.EstimateTHB
		switch r.RowType {
		case RowTrip:
			t.TripOnlyCount++
			t.TripSubtotal += r.EstimateTHB
		case RowStandby:
			t.StandbyCount++
			t.StandbySubtotal += r.EstimateTHB
		case RowMultidropStop:
			t.MultiDropCount++
			t.MultiDropSubtotal += r.EstimateTHB
		}
	}
	t.GrandTotal = jsmath.Round2(t.GrandTotal)
	t.TripSubtotal, t.StandbySubtotal, t.MultiDropSubtotal = jsmath.Round2(t.TripSubtotal),
		jsmath.Round2(t.StandbySubtotal), jsmath.Round2(t.MultiDropSubtotal)
	if rate != nil {
		t.WithholdingTax = compute.WithholdingTHB(t.GrandTotal, *rate)
	}
	t.Net = jsmath.Round2(t.GrandTotal - t.WithholdingTax)
	return t
}

// --- missing billing date, standby diagnostics -------------------------------------------------------------------

// TripMissingBillingDate is a delivered trip of the period without billing_date (TripMissingBillingDate,
// billing.ts:1097-1105): in no plan-basis period at all.
type TripMissingBillingDate struct {
	TripID      uuid.UUID  `json:"tripId"`
	TripNo      string     `json:"tripNo"`
	DeliveredAt *time.Time `json:"deliveredAt"`
	Priced      bool       `json:"priced"`
}

// MissingBillingDate is GET /v1/billing/rows/missing-billing-date (accounting:billing_document): the party's trips
// delivered in the Bangkok month that carry no billing_date (fetchTripsMissingBillingDate, billing.ts:1116-1145), never
// part of an invoice set; the Billing Document lists them for repair (a forced recompute restamps them, §6.10 state 3).
func (s *Service) MissingBillingDate(ctx context.Context, p *authz.Principal, customer string, year, month int) ([]TripMissingBillingDate, error) {
	sc, err := s.readScope(ctx, p, customer, year, month, false)
	if err != nil {
		return nil, err
	}
	out := []TripMissingBillingDate{}
	if !sc.found {
		return out, nil
	}
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.MissingBillingDateTrips(ctx, billingdb.MissingBillingDateTripsParams{PartyID: sc.party.ID,
			PeriodFrom: sc.from, PeriodTo: sc.to})
		for _, r := range rows {
			out = append(out, TripMissingBillingDate{TripID: r.ID, TripNo: r.TripNo, DeliveredAt: timeUTC(r.DeliveredAt),
				Priced: r.Priced})
		}
		return err
	})
	return out, err
}

// StandbyIssue is a completed standby record without a price (StandbyBillingIssue, billing.ts:1397-1411).
type StandbyIssue struct {
	ID                uuid.UUID  `json:"id"`
	Reason            string     `json:"reason"` // no_customer | no_rate | not_computed | no_ended_at
	ReasonSource      string     `json:"reasonSource"`
	DriverID          *uuid.UUID `json:"driverId"`
	DriverName        *string    `json:"driverName"`
	StartLocation     *string    `json:"startLocation"`
	EndLocation       *string    `json:"endLocation"`
	StartedAt         *time.Time `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	DurationMinutes   *int32     `json:"durationMinutes"`
	BillingPartyID    *uuid.UUID `json:"billingPartyId"`
	CustomerID        *uuid.UUID `json:"customerId"`
	DryRunEstimateTHB *float64   `json:"dryRunEstimateThb"`
}

// StandbyDiagnostics is the answer of GET /v1/billing/standby-diagnostics.
type StandbyDiagnostics struct {
	Unpriced                []StandbyIssue `json:"unpriced"`
	MissingEndedAt          []StandbyIssue `json:"missingEndedAt"`
	MissingEndedAtTruncated bool           `json:"missingEndedAtTruncated"`
}

// MaxMissingEndedAt bounds the no-ended-at list (records in no month at all; the legacy page scanned 300): more is
// reported by missingEndedAtTruncated, never silently dropped.
const MaxMissingEndedAt = 1000

// StandbyDiagnostics is GET /v1/billing/standby-diagnostics (accounting:billing_document; fetchStandbyBillingDiagnostics,
// billing.ts:1455-1597): the period's completed standby records without a price, with the reason (stored, else a dry run:
// no_customer, no_rate, or not_computed when the rules would price it), read by the same query as the rows API (no
// duplicated standby read), plus every completed record without ended_at, which no period holds (no_ended_at). With one
// party, the records of that party and those no party resolves for. customerId=all reads every party.
func (s *Service) StandbyDiagnostics(ctx context.Context, p *authz.Principal, customer string, year, month int) (*StandbyDiagnostics, error) {
	sc, err := s.readScope(ctx, p, customer, year, month, true)
	if err != nil {
		return nil, err
	}
	out := &StandbyDiagnostics{Unpriced: []StandbyIssue{}, MissingEndedAt: []StandbyIssue{}}
	if !sc.all && !sc.found {
		return out, nil
	}
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		standby, err := q.BillingRowStandby(ctx, billingdb.BillingRowStandbyParams{PeriodFrom: sc.from, PeriodTo: sc.to,
			AllParties: sc.all, PartyID: sc.party.ID})
		if err != nil {
			return err
		}
		orphans, err := q.StandbyMissingEndedAt(ctx, billingdb.StandbyMissingEndedAtParams{AllParties: sc.all,
			PartyID: sc.party.ID, RowLimit: MaxMissingEndedAt + 1})
		if err != nil {
			return err
		}
		parties := map[uuid.UUID]bool{}
		for _, r := range standby {
			if r.BillingEstimateThb == nil || *r.BillingEstimateThb == 0 {
				if id := standbyParty(r); id != uuid.Nil {
					parties[id] = true
				}
			}
		}
		for _, r := range orphans {
			if id := firstID(r.CustomerPartyID, r.SourceLinkedPartyID, r.DestinationLinkedPartyID); id != nil {
				parties[*id] = true
			}
		}
		ids := make([]uuid.UUID, 0, len(parties))
		for id := range parties {
			ids = append(ids, id)
		}
		dry, err := loadDryRun(ctx, q, sc.tenant, compute.HubMaps{}, ids)
		if err != nil {
			return err
		}
		billed := map[uuid.UUID]uuid.UUID{}
		for id, ref := range dry.parties {
			billed[id] = ref.BilledID
		}
		for _, r := range standby {
			if r.BillingEstimateThb != nil && *r.BillingEstimateThb != 0 {
				continue
			}
			u := unpricedStandby(r, hubDisplay{}, dry, billed)
			out.Unpriced = append(out.Unpriced, StandbyIssue{ID: r.ID, Reason: u.Reason, ReasonSource: u.ReasonSource,
				DriverID: r.DriverID, DriverName: u.DriverName, StartLocation: r.StartLocationRaw, EndLocation: r.EndLocationRaw,
				StartedAt: timeUTC(r.StartedAt), EndedAt: timeUTC(r.EndedAt), CreatedAt: r.CreatedAt.UTC(),
				DurationMinutes: r.DurationMinutes, BillingPartyID: u.BillingPartyID, CustomerID: u.CustomerID,
				DryRunEstimateTHB: u.DryRunEstimateTHB})
		}
		if len(orphans) > MaxMissingEndedAt {
			orphans, out.MissingEndedAtTruncated = orphans[:MaxMissingEndedAt], true
		}
		for _, r := range orphans {
			party := firstID(r.CustomerPartyID, r.SourceLinkedPartyID, r.DestinationLinkedPartyID)
			out.MissingEndedAt = append(out.MissingEndedAt, StandbyIssue{ID: r.ID, Reason: string(compute.NoEndedAt),
				ReasonSource: ReasonDryRun, DriverID: r.StandbyDriverID,
				DriverName:    strPtr(driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)),
				StartLocation: r.StartLocationRaw, EndLocation: r.EndLocationRaw, StartedAt: timeUTC(r.StartedAt),
				CreatedAt: r.CreatedAt.UTC(), DurationMinutes: r.DurationMinutes, BillingPartyID: party,
				CustomerID: customerOf(billed, party)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
