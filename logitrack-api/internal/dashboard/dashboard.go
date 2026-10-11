// Package dashboard is the W8 aggregates of T53 (P6; Appendix B §B.2.19, Appendix E §E.2.1): GET
// /v1/dashboard/summary, one request for the dashboard page instead of about 10.7k document reads per visit
// (DashboardStats.tsx:111-149,164-200, ActivityChart.tsx:45-81, RecentUpdates.tsx:51-77, ComplianceSummary.tsx:32-34,
// ExpenseAuditWidget.tsx:17-21, ChatStatusWidget.tsx:19-36), and GET /v1/badges, the counters of the sidebar and the
// dashboard widgets (app-sidebar.tsx:63-74; the waitlist listener).
//
// Every block and counter is present only for a principal holding its capability, and every query runs in
// db.WithPrincipal, so RLS keeps each figure to the rows the caller's own pages list (own tenant plus the carriers
// working for it, R60). A dispatcher or customer-scope principal gets the operations blocks from the scope_* projections
// (internal/scope, scope_dashboard.sql) and no other block: carrier-internal tables have no scope policy (Appendix C
// §C.3.7). The web's role counts stay on GET /v1/stats/users-by-role (T51): memberships are platform rows.
package dashboard

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/dashboard/dashboarddb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
)

// Summary bounds.
const (
	// DefaultDays is the period without ?from: the last seven Bangkok days ending ?to (the Week of ActivityChart).
	DefaultDays = 7
	// MaxDays bounds ?from..?to.
	MaxDays = 366
	// ComplianceHorizonDays is the warning horizon of the compliance block (ComplianceSummary.tsx:38).
	ComplianceHorizonDays = 30
	// ComplianceItemsMax caps the listed compliance items; the counts and itemsTotal cover every one.
	ComplianceItemsMax = 50
	// RecentMax is the length of the recent-updates feed (RecentUpdates.tsx:166: the newest 8).
	RecentMax = 8
)

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every read runs in db.WithPrincipal.
	Pool db.Beginner
	Log  zerolog.Logger
	// Now is the clock of the default period and the compliance horizon (tests pin it); nil = time.Now.
	Now func() time.Time
}

// Service serves /v1/dashboard/summary and /v1/badges.
type Service struct {
	pool db.Beginner
	log  zerolog.Logger
	now  func() time.Time
}

// New builds the service. It does no I/O.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("dashboard: a pool is required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{pool: d.Pool, log: d.Log, now: d.Now}, nil
}

// scoped reports whether p reads the scope_* projections: a dispatcher or a customer-scope principal (never under
// X-Act-On-Tenant: *, which reads as staff).
func scoped(p *authz.Principal) bool {
	return (p.IsCustomerScope() || p.Dispatcher) && !p.ActOnAll
}

// Period is the summary window: Bangkok calendar days from..to, both included.
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`

	fromAt, toAt time.Time // [Bangkok midnight of From, Bangkok midnight after To)
	prevAt       time.Time // the start of the window of the same length before From
}

// ParsePeriod reads ?from and ?to (yyyy-MM-dd, Bangkok). to defaults to today, from to DefaultDays days ending to.
func ParsePeriod(from, to string, now time.Time) (Period, error) {
	day := func(field, s string) (time.Time, error) {
		t, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return time.Time{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "invalid_date"})
		}
		return t, nil
	}
	end, err := time.Parse(time.DateOnly, clock.DateString(now))
	if err != nil {
		return Period{}, err
	}
	if to != "" {
		if end, err = day("to", to); err != nil {
			return Period{}, err
		}
	}
	start := end.AddDate(0, 0, -(DefaultDays - 1))
	if from != "" {
		if start, err = day("from", from); err != nil {
			return Period{}, err
		}
	}
	days := int(end.Sub(start)/(24*time.Hour)) + 1
	switch {
	case days < 1:
		return Period{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "from", Reason: "after_to"})
	case days > MaxDays:
		return Period{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "from", Reason: "range_too_long",
			Params: map[string]any{"maxDays": MaxDays}})
	}
	midnight := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, clock.Bangkok) }
	p := Period{From: start.Format(time.DateOnly), To: end.Format(time.DateOnly), Days: days,
		fromAt: midnight(start), toAt: midnight(end.AddDate(0, 0, 1))}
	p.prevAt = midnight(start.AddDate(0, 0, -days))
	return p, nil
}

// Summary is the body of GET /v1/dashboard/summary: each block only for a principal holding its capability.
type Summary struct {
	Period Period `json:"period"`
	// Trips: operations:view_driver_monitor.
	Trips *Trips `json:"trips,omitempty"`
	// Activity: reporting:view_analytics.
	Activity *Activity `json:"activity,omitempty"`
	// Incidents: operations:view_incidents.
	Incidents *Incidents `json:"incidents,omitempty"`
	// Drivers: drivers:view.
	Drivers *Drivers `json:"drivers,omitempty"`
	// Expenses: accounting:view_fuel | accounting:view_other (each type for its key).
	Expenses *Expenses `json:"expenses,omitempty"`
	// Chats: chat:view.
	Chats *Chats `json:"chats,omitempty"`
	// Compliance: fleet:view_trucks.
	Compliance *Compliance `json:"compliance,omitempty"`
	// Recent is the recent-updates feed, newest first: each source for its key (trips: view_driver_monitor, drivers:
	// drivers:view, incidents: view_incidents, maintenance: fleet:manage_maintenance).
	Recent []RecentItem `json:"recent"`
}

// Trips is the delivered-trips card and the fleet-efficiency card of the period (DashboardStats.tsx:164-200): a
// delivered trip with an incident report counts as delayed; the percentages have one decimal (Math.round(x*1000)/10)
// and are null without a delivered trip.
type Trips struct {
	Delivered    int64    `json:"delivered"`
	Parcels      int64    `json:"parcels"`
	WithIncident int64    `json:"withIncident"`
	OnTimePct    *float64 `json:"onTimePct"`
	DelayPct     *float64 `json:"delayPct"`
}

// Activity is the delivered-trips histogram of the period, one entry per Bangkok day (zero days included), with the
// total of the window of the same length before it and the trend of ActivityChart.tsx:130-135.
type Activity struct {
	Days          []DayCount `json:"days"`
	Total         int64      `json:"total"`
	PreviousTotal int64      `json:"previousTotal"`
	TrendPct      *float64   `json:"trendPct"`
}

// DayCount is one day of the histogram.
type DayCount struct {
	Date      string `json:"date"`
	Delivered int64  `json:"delivered"`
}

// Incidents counts the incident reports created in the period.
type Incidents struct {
	InPeriod int64 `json:"inPeriod"`
}

// Drivers counts drivers by status (the card shows active + onDuty, all as the fallback).
type Drivers struct {
	Active   int64 `json:"active"`
	OnDuty   int64 `json:"onDuty"`
	Inactive int64 `json:"inactive"`
	Total    int64 `json:"total"`
}

// Expenses counts the expenses awaiting the audit, per type the caller may view.
type Expenses struct {
	PendingFuel  *int64 `json:"pendingFuel,omitempty"`
	PendingOther *int64 `json:"pendingOther,omitempty"`
}

// Chats counts the open chats nobody is assigned to, and the urgent ones among them.
type Chats struct {
	Unassigned int64 `json:"unassigned"`
	Urgent     int64 `json:"urgent"`
}

// Compliance lists the active trucks of the caller's tenant whose tax, insurance or next service is due within
// ComplianceHorizonDays, or whose next service is at most maintenance.PMAlertThresholdKm away. Items are sorted by
// urgency (ComplianceSummary.tsx:113-117: days remaining, km remaining / 100); at most ComplianceItemsMax are listed,
// itemsTotal counts them all.
type Compliance struct {
	TaxDue         int              `json:"taxDue"`
	InsuranceDue   int              `json:"insuranceDue"`
	ServiceDateDue int              `json:"serviceDateDue"`
	ServiceKmDue   int              `json:"serviceKmDue"`
	Items          []ComplianceItem `json:"items"`
	ItemsTotal     int              `json:"itemsTotal"`
}

// ComplianceItem is one due item; daysRemaining is negative once overdue.
type ComplianceItem struct {
	TruckID       uuid.UUID `json:"truckId"`
	LicensePlate  string    `json:"licensePlate"`
	Kind          string    `json:"kind"` // tax | insurance | service_date | service_km
	DueDate       *string   `json:"dueDate,omitempty"`
	DaysRemaining *int      `json:"daysRemaining,omitempty"`
	KmRemaining   *int      `json:"kmRemaining,omitempty"`
}

// RecentItem is one entry of the recent-updates feed; the web builds the title from type and the raw fields.
type RecentItem struct {
	Type  string    `json:"type"` // delivered | driver | incident | maintenance
	ID    uuid.UUID `json:"id"`
	At    time.Time `json:"at"`
	Plate *string   `json:"plate,omitempty"` // delivered
	Place *string   `json:"place,omitempty"` // delivered: destination, else origin
	Name  *string   `json:"name,omitempty"`  // driver: Thai full name, else first + last
	Text  *string   `json:"text,omitempty"`  // incident: delay cause, else description; maintenance: service type
}

// Summary computes GET /v1/dashboard/summary for p over period in one transaction.
func (s *Service) Summary(ctx context.Context, p *authz.Principal, period Period) (*Summary, error) {
	out := &Summary{Period: period, Recent: []RecentItem{}}
	today, err := time.Parse(time.DateOnly, clock.DateString(s.now()))
	if err != nil {
		return nil, err
	}
	err = db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		if scoped(p) {
			return s.scopeSummary(ctx, tx, p, out)
		}
		return s.staffSummary(ctx, tx, p, today, out)
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out.Recent, func(a, b RecentItem) int { return b.At.Compare(a.At) })
	if len(out.Recent) > RecentMax {
		out.Recent = out.Recent[:RecentMax]
	}
	return out, nil
}

func (s *Service) staffSummary(ctx context.Context, tx pgx.Tx, p *authz.Principal, today time.Time, out *Summary) error {
	q := dashboarddb.New(tx)
	per := out.Period
	if p.Can(authz.OperationsViewDriverMon) {
		t, err := q.TripTotals(ctx, dashboarddb.TripTotalsParams{FromAt: &per.fromAt, ToAt: &per.toAt})
		if err != nil {
			return err
		}
		out.Trips = trips(t.Delivered, t.Parcels, t.WithIncident)
		rows, err := q.RecentDelivered(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Recent = append(out.Recent, delivered(r.ID, r.DeliveredAt, r.TruckLicensePlateSnapshot, r.DestinationRaw, r.OriginRaw))
		}
	}
	if p.Can(authz.ReportingViewAnalytics) {
		days, err := q.DeliveredByDay(ctx, dashboarddb.DeliveredByDayParams{FromAt: &per.fromAt, ToAt: &per.toAt})
		if err != nil {
			return err
		}
		prev, err := q.DeliveredCount(ctx, dashboarddb.DeliveredCountParams{FromAt: &per.prevAt, ToAt: &per.fromAt})
		if err != nil {
			return err
		}
		counts := map[string]int64{}
		for _, d := range days {
			counts[d.Day.Format(time.DateOnly)] = d.Delivered
		}
		out.Activity = activity(per, counts, prev)
	}
	if p.Can(authz.OperationsViewIncidents) {
		n, err := q.IncidentCount(ctx, dashboarddb.IncidentCountParams{FromAt: per.fromAt, ToAt: per.toAt})
		if err != nil {
			return err
		}
		out.Incidents = &Incidents{InPeriod: n}
		rows, err := q.RecentIncidents(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Recent = append(out.Recent, incident(r.ID, r.CreatedAt, r.DelayCause, r.Description))
		}
	}
	if p.Can(authz.DriversView) {
		d, err := q.DriverStatusCounts(ctx)
		if err != nil {
			return err
		}
		out.Drivers = &Drivers{Active: d.Active, OnDuty: d.OnDuty, Inactive: d.Inactive, Total: d.Total}
		rows, err := q.RecentDrivers(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			name := strings.TrimSpace(strings.TrimSpace(r.FirstName) + " " + strings.TrimSpace(r.LastName))
			if r.FullNameTh != nil && strings.TrimSpace(*r.FullNameTh) != "" {
				name = strings.TrimSpace(*r.FullNameTh)
			}
			out.Recent = append(out.Recent, RecentItem{Type: "driver", ID: r.ID, At: r.UpdatedAt.UTC(), Name: nonEmpty(name)})
		}
	}
	fuel, other := p.Can(authz.AccountingViewFuel), p.Can(authz.AccountingViewOther)
	if fuel || other {
		e, err := q.PendingExpenses(ctx)
		if err != nil {
			return err
		}
		out.Expenses = &Expenses{}
		if fuel {
			out.Expenses.PendingFuel = &e.Fuel
		}
		if other {
			out.Expenses.PendingOther = &e.Other
		}
	}
	if p.Can(authz.ChatView) {
		c, err := q.ChatQueueCounts(ctx)
		if err != nil {
			return err
		}
		out.Chats = &Chats{Unassigned: c.Unassigned, Urgent: c.Urgent}
	}
	if p.Can(authz.FleetViewTrucks) {
		var tenant *uuid.UUID
		if !p.ActOnAll {
			tenant = p.EffectiveTenant()
			if tenant == nil { // no tenant to call "own": nothing is due
				out.Compliance = &Compliance{Items: []ComplianceItem{}}
			}
		}
		if out.Compliance == nil {
			rows, err := q.ComplianceTrucks(ctx, dashboarddb.ComplianceTrucksParams{TenantID: tenant,
				Horizon: today.AddDate(0, 0, ComplianceHorizonDays), PmKm: maintenance.PMAlertThresholdKm})
			if err != nil {
				return err
			}
			out.Compliance = compliance(rows, today)
		}
	}
	if p.Can(authz.FleetManageMaintenance) {
		rows, err := q.RecentMaintenance(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Recent = append(out.Recent, RecentItem{Type: "maintenance", ID: r.ID, At: r.UpdatedAt.UTC(), Text: nonEmpty(r.ServiceType)})
		}
	}
	return nil
}

// scopeSummary is the summary of a dispatcher or customer-scope principal: the operations blocks over the scope_*
// projections only.
func (s *Service) scopeSummary(ctx context.Context, tx pgx.Tx, p *authz.Principal, out *Summary) error {
	q := scopedb.New(tx)
	per := out.Period
	if p.Can(authz.OperationsViewDriverMon) {
		t, err := q.ScopeDashboardTripTotals(ctx, scopedb.ScopeDashboardTripTotalsParams{FromAt: &per.fromAt, ToAt: &per.toAt})
		if err != nil {
			return err
		}
		out.Trips = trips(t.Delivered, t.Parcels, t.WithIncident)
		rows, err := q.ScopeDashboardRecentDelivered(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Recent = append(out.Recent, delivered(r.ID, r.DeliveredAt, r.TruckLicensePlateSnapshot, r.DestinationRaw, r.OriginRaw))
		}
	}
	if p.Can(authz.ReportingViewAnalytics) {
		days, err := q.ScopeDashboardDeliveredByDay(ctx, scopedb.ScopeDashboardDeliveredByDayParams{FromAt: &per.fromAt, ToAt: &per.toAt})
		if err != nil {
			return err
		}
		prev, err := q.ScopeDashboardDeliveredCount(ctx, scopedb.ScopeDashboardDeliveredCountParams{FromAt: &per.prevAt, ToAt: &per.fromAt})
		if err != nil {
			return err
		}
		counts := map[string]int64{}
		for _, d := range days {
			counts[d.Day.Format(time.DateOnly)] = d.Delivered
		}
		out.Activity = activity(per, counts, prev)
	}
	if p.Can(authz.OperationsViewIncidents) {
		n, err := q.ScopeDashboardIncidentCount(ctx, scopedb.ScopeDashboardIncidentCountParams{FromAt: per.fromAt, ToAt: per.toAt})
		if err != nil {
			return err
		}
		out.Incidents = &Incidents{InPeriod: n}
		rows, err := q.ScopeDashboardRecentIncidents(ctx, RecentMax)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Recent = append(out.Recent, incident(r.ID, r.CreatedAt, r.DelayCause, r.Description))
		}
	}
	return nil
}

// pct is Math.round(part / whole * 1000) / 10 (DashboardStats.tsx:182-183).
func pct(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := jsmath.Round(float64(part)/float64(whole)*1000) / 10
	return &v
}

func trips(delivered, parcels, withIncident int64) *Trips {
	return &Trips{Delivered: delivered, Parcels: parcels, WithIncident: withIncident,
		OnTimePct: pct(delivered-withIncident, delivered), DelayPct: pct(withIncident, delivered)}
}

// activity zero-fills the histogram over the period and applies the trend rule of ActivityChart.tsx:130-135:
// round((total - previous) / previous * 100) when previous > 0 and the totals differ, 100 when only this period has
// trips, else null.
func activity(per Period, counts map[string]int64, previous int64) *Activity {
	a := &Activity{Days: make([]DayCount, 0, per.Days), PreviousTotal: previous}
	start, _ := time.Parse(time.DateOnly, per.From)
	for i := range per.Days {
		d := start.AddDate(0, 0, i).Format(time.DateOnly)
		a.Days = append(a.Days, DayCount{Date: d, Delivered: counts[d]})
		a.Total += counts[d]
	}
	switch {
	case previous > 0 && a.Total != previous:
		v := jsmath.Round(float64(a.Total-previous) / float64(previous) * 100)
		a.TrendPct = &v
	case previous == 0 && a.Total > 0:
		v := 100.0
		a.TrendPct = &v
	}
	return a
}

func delivered(id uuid.UUID, at *time.Time, plate, dest, origin *string) RecentItem {
	item := RecentItem{Type: "delivered", ID: id, Plate: nonEmptyPtr(plate), Place: nonEmptyPtr(dest)}
	if at != nil {
		item.At = at.UTC()
	}
	if item.Place == nil {
		item.Place = nonEmptyPtr(origin)
	}
	return item
}

func incident(id uuid.UUID, at time.Time, cause, description *string) RecentItem {
	text := nonEmptyPtr(cause)
	if text == nil {
		text = nonEmptyPtr(description)
	}
	return RecentItem{Type: "incident", ID: id, At: at.UTC(), Text: text}
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func nonEmptyPtr(s *string) *string {
	if s == nil {
		return nil
	}
	return nonEmpty(*s)
}

// compliance builds the block from the due trucks (dates are Bangkok calendar days; daysRemaining = due - today).
func compliance(rows []dashboarddb.ComplianceTrucksRow, today time.Time) *Compliance {
	c := &Compliance{Items: []ComplianceItem{}}
	horizon := today.AddDate(0, 0, ComplianceHorizonDays)
	var all []ComplianceItem
	dated := func(r dashboarddb.ComplianceTrucksRow, kind string, d *time.Time, n *int) {
		if d == nil || d.After(horizon) {
			return
		}
		days := int(d.Sub(today) / (24 * time.Hour))
		ds := d.Format(time.DateOnly)
		all = append(all, ComplianceItem{TruckID: r.ID, LicensePlate: r.LicensePlate, Kind: kind, DueDate: &ds, DaysRemaining: &days})
		*n++
	}
	for _, r := range rows {
		dated(r, "tax", r.TaxExpiryDate, &c.TaxDue)
		dated(r, "insurance", r.InsuranceExpiryDate, &c.InsuranceDue)
		dated(r, "service_date", r.NextServiceDate, &c.ServiceDateDue)
		if r.NextServiceMileage != nil && r.CurrentMileage != nil {
			if left := int(*r.NextServiceMileage) - int(*r.CurrentMileage); left <= maintenance.PMAlertThresholdKm {
				all = append(all, ComplianceItem{TruckID: r.ID, LicensePlate: r.LicensePlate, Kind: "service_km", KmRemaining: &left})
				c.ServiceKmDue++
			}
		}
	}
	urgency := func(i ComplianceItem) float64 {
		switch {
		case i.DaysRemaining != nil:
			return float64(*i.DaysRemaining)
		case i.KmRemaining != nil:
			return float64(*i.KmRemaining) / 100
		}
		return 999
	}
	slices.SortStableFunc(all, func(a, b ComplianceItem) int {
		ua, ub := urgency(a), urgency(b)
		switch {
		case ua < ub:
			return -1
		case ua > ub:
			return 1
		}
		return strings.Compare(a.LicensePlate, b.LicensePlate)
	})
	c.ItemsTotal = len(all)
	if len(all) > ComplianceItemsMax {
		all = all[:ComplianceItemsMax]
	}
	if all != nil {
		c.Items = all
	}
	return c
}

// Badges is the body of GET /v1/badges: each counter only for a principal holding its capability (BadgesDTO,
// features/dashboard/api/badges.ts).
type Badges struct {
	// Waitlist: waitlist:view (a steward key).
	Waitlist *int64 `json:"waitlist,omitempty"`
	// PendingExpenses: accounting:audit_expense, the expenses awaiting the audit (every type).
	PendingExpenses *int64 `json:"pendingExpenses,omitempty"`
	// UnassignedChats, UrgentChats: chat:view.
	UnassignedChats *int64 `json:"unassignedChats,omitempty"`
	UrgentChats     *int64 `json:"urgentChats,omitempty"`
}

// Badges computes GET /v1/badges for p in one transaction (no query at all for a principal without a counter).
func (s *Service) Badges(ctx context.Context, p *authz.Principal) (*Badges, error) {
	out := &Badges{}
	waitlist, expenses, chats := p.Can(authz.WaitlistView), p.Can(authz.AccountingAuditExpense) && !scoped(p),
		p.Can(authz.ChatView) && !scoped(p)
	if !waitlist && !expenses && !chats {
		return out, nil
	}
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		q := dashboarddb.New(tx)
		if waitlist {
			n, err := q.WaitlistCount(ctx)
			if err != nil {
				return err
			}
			out.Waitlist = &n
		}
		if expenses {
			e, err := q.PendingExpenses(ctx)
			if err != nil {
				return err
			}
			out.PendingExpenses = &e.Total
		}
		if chats {
			c, err := q.ChatQueueCounts(ctx)
			if err != nil {
				return err
			}
			out.UnassignedChats, out.UrgentChats = &c.Unassigned, &c.Urgent
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
