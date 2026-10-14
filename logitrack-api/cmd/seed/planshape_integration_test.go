//go:build integration

package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security/securitydb"
)

// planShape is one hot query of the load profile (issue T61, Appendix A §A.2.19): the generated method called with
// representative arguments under the principal whose RLS applies, the tables its plan may never read sequentially, and
// the indexes that may serve it (any one of them must appear in the plan).
type planShape struct {
	name   string
	p      *authz.Principal
	call   func(ctx context.Context, x *pgtest.Explainer) error
	tables []string
	want   []string
}

// largeRows is the size from which a table counts as large on the load profile: there the planner must not choose a
// sequential scan for a hot query even with its real statistics (vehicle_expenses, customer_rate_entries).
const largeRows = 5000

// errRollback ends a planning transaction without committing (it writes nothing anyway).
var errRollback = errors.New("rollback")

// checkPlanShapes is the plan-shape test of T61 on a freshly loaded load profile. Each shape is planned twice under a
// request principal's RLS: once with sequential scans disabled, where no listed table may be read sequentially and one
// of the wanted indexes must serve it (the index exists for the predicate and the order, as each domain's explain test
// does with its own fixture), and once with the planner's own choice after ANALYZE, where no table of at least
// largeRows rows may be read sequentially. A later change that drops or narrows a supporting index, or a query that
// stops matching it, fails here instead of turning into a slow page.
func checkPlanShapes(t *testing.T, s *stack) {
	t.Helper()
	ctx := context.Background()
	super, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = super.Close(ctx) }()
	if _, err := super.Exec(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	id := func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var v uuid.UUID
		if err := super.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	ids := func(sql string, args ...any) []uuid.UUID {
		t.Helper()
		rows, err := super.Query(ctx, sql, args...)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return out
	}
	at := func(sql string, args ...any) time.Time {
		t.Helper()
		var v time.Time
		if err := super.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	var large []string
	rows, err := super.Query(ctx, `SELECT relname FROM pg_class WHERE relkind = 'r' AND relnamespace = 'public'::regnamespace
		AND reltuples >= $1 ORDER BY relname`, largeRows)
	if err != nil {
		t.Fatal(err)
	}
	if large, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"customer_rate_entries", "vehicle_expenses"} {
		if !slices.Contains(large, want) {
			t.Fatalf("the load profile no longer makes %s large (>= %d rows): %v", want, largeRows, large)
		}
	}

	// Principals: own-fleet staff (reach = the own fleet and its subtenants, the steward of the masters), staff of a
	// carrier without subtenants, and the read-only platform bypass (X-Act-On-Tenant: *).
	own := id(`SELECT id FROM tenants WHERE kind = 'own_fleet'`)
	subs := ids(`SELECT id FROM tenants WHERE contractor_tenant_id = $1 ORDER BY id`, own)
	carrier := id(`SELECT t.id FROM tenants t WHERE t.kind = 'carrier' AND t.contractor_tenant_id IS NULL
		AND EXISTS (SELECT 1 FROM vehicle_expenses e WHERE e.tenant_id = t.id) ORDER BY t.id LIMIT 1`)
	ownAdmin := id(`SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'tenant_admin' ORDER BY user_id LIMIT 1`, own)
	carrierAdmin := id(`SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'tenant_admin' ORDER BY user_id LIMIT 1`, carrier)
	staffOwn := &authz.Principal{UserID: ownAdmin, TenantID: &own, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, SubtenantIDs: subs}
	staffCarrier := &authz.Principal{UserID: carrierAdmin, TenantID: &carrier, TenantRole: authz.TenantAdmin, TenantKind: "carrier"}
	all := &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}, ActOnAll: true}
	reachOwn := append([]uuid.UUID{own}, subs...)

	// Arguments taken from the loaded rows.
	hub := id(`SELECT hub_id FROM hub_soc_distances GROUP BY hub_id ORDER BY count(*) DESC, hub_id LIMIT 1`)
	party := id(`SELECT billing_party_id FROM customer_rate_entries GROUP BY 1 ORDER BY count(*) DESC, 1 LIMIT 1`)
	truck := id(`SELECT truck_id FROM maintenance_records GROUP BY 1 ORDER BY count(*) DESC, 1 LIMIT 1`)
	rateAt := at(`SELECT effective_from_at FROM customer_rate_entries ORDER BY effective_from_at DESC, id DESC OFFSET 50 LIMIT 1`)
	rateID := id(`SELECT id FROM customer_rate_entries ORDER BY effective_from_at DESC, id DESC OFFSET 50 LIMIT 1`)
	var rateImport string
	if err := super.QueryRow(ctx, `SELECT import_id FROM customer_rate_entries WHERE id = $1`, rateID).Scan(&rateImport); err != nil {
		t.Fatal(err)
	}
	expAt := at(`SELECT expense_at FROM vehicle_expenses ORDER BY expense_at DESC, id DESC OFFSET 50 LIMIT 1`)
	expID := id(`SELECT id FROM vehicle_expenses ORDER BY expense_at DESC, id DESC OFFSET 50 LIMIT 1`)
	secAt := time.Now().Add(-24 * time.Hour)
	pending, fuel, other := "pending", []string{"fuel"}, []string{"other"}
	const page = 51 // the default page of 50 plus the look-ahead row

	shapes := []planShape{
		// --- M1 master data
		{"ListCustomers", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := partiesdb.New(x).ListCustomers(ctx, partiesdb.ListCustomersParams{PartyIds: []uuid.UUID{}, RowLimit: page})
			return err
		}, []string{"customers"}, []string{"customers_code", "customers_pkey"}},
		{"ListDistances", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := hubsdb.New(x).ListDistances(ctx, hubsdb.ListDistancesParams{RowLimit: page})
			return err
		}, []string{"hub_soc_distances"}, []string{"hub_soc_distances_pkey"}},
		{"ListDistances by hub", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := hubsdb.New(x).ListDistances(ctx, hubsdb.ListDistancesParams{HubID: &hub, RowLimit: page})
			return err
		}, []string{"hub_soc_distances"}, []string{"hub_soc_distances_hub_id_soc_key_direction_key"}},
		{"ListTrucks own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := fleetdb.New(x).ListTrucks(ctx, fleetdb.ListTrucksParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"trucks"}, []string{"trucks_tenant_created", "trucks_tenant_status"}},
		{"ListDrivers own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := driversdb.New(x).ListDrivers(ctx, driversdb.ListDriversParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"drivers"}, []string{"drivers_tenant_created", "drivers_tenant_status"}},
		{"ListAssignments own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := fleetdb.New(x).ListAssignments(ctx, fleetdb.ListAssignmentsParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"truck_assignments"}, []string{"truck_assignments_tenant"}},
		{"ListTenantMembers", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListTenantMembers(ctx, iamdb.ListTenantMembersParams{TenantID: own, RowLimit: page})
			return err
		}, []string{"memberships", "users"}, []string{"memberships_by_tenant_role"}},
		{"ListUserMemberships", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListUserMemberships(ctx, iamdb.ListUserMembershipsParams{UserIds: []uuid.UUID{ownAdmin},
				TenantIds: reachOwn})
			return err
		}, []string{"memberships"}, []string{"memberships_pkey"}},
		{"ListUsersByLastLogin", all, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListUsersByLastLogin(ctx, iamdb.ListUsersByLastLoginParams{AllUsers: true,
				TenantIds: []uuid.UUID{}, RowLimit: page})
			return err
		}, []string{"users"}, []string{"users_last_login"}},
		{"ListUsersByCreated", all, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListUsersByCreated(ctx, iamdb.ListUsersByCreatedParams{AllUsers: true,
				TenantIds: []uuid.UUID{}, RowLimit: page})
			return err
		}, []string{"users"}, []string{"users_created"}},
		{"ListUsersByCreated own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListUsersByCreated(ctx, iamdb.ListUsersByCreatedParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"users"}, []string{"users_created"}},
		{"ListAdminSessions", all, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := iamdb.New(x).ListAdminSessions(ctx, iamdb.ListAdminSessionsParams{UserID: ownAdmin, Now: time.Now()})
			return err
		}, []string{"sessions"}, []string{"sessions_user_active"}},
		{"ListSecurityEvents", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := securitydb.New(x).ListSecurityEvents(ctx, securitydb.ListSecurityEventsParams{Types: []string{},
				FromAt: &secAt, RowLimit: page})
			return err
		}, []string{"security_events"}, []string{"security_events_feed", "security_events_tenant"}},
		// --- M3 tables at load scale (T43): rate cards, expenses, maintenance
		{"ListRateEntries own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := billingdb.New(x).ListRateEntries(ctx, billingdb.ListRateEntriesParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"customer_rate_entries"}, []string{"customer_rate_entries_keyset"}},
		{"ListRateEntries next page", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := billingdb.New(x).ListRateEntries(ctx, billingdb.ListRateEntriesParams{TenantIds: reachOwn,
				AfterAt: &rateAt, AfterImport: rateImport, AfterID: rateID, RowLimit: page})
			return err
		}, []string{"customer_rate_entries"}, []string{"customer_rate_entries_keyset"}},
		{"PartyRateEntries", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := billingdb.New(x).PartyRateEntries(ctx, party)
			return err
		}, []string{"customer_rate_entries"}, []string{"customer_rate_entries_party", "customer_rate_entries_import",
			"customer_rate_entries_lookup"}},
		{"ListExpenses fuel own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := expensesdb.New(x).ListExpenses(ctx, expensesdb.ListExpensesParams{TenantIds: reachOwn, Types: fuel, RowLimit: page})
			return err
		}, []string{"vehicle_expenses"}, []string{"vehicle_expenses_keyset"}},
		{"ListExpenses other next page", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := expensesdb.New(x).ListExpenses(ctx, expensesdb.ListExpensesParams{TenantIds: reachOwn, Types: other,
				AfterAt: &expAt, AfterID: expID, RowLimit: page})
			return err
		}, []string{"vehicle_expenses"}, []string{"vehicle_expenses_keyset"}},
		{"ListExpenses pending carrier", staffCarrier, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := expensesdb.New(x).ListExpenses(ctx, expensesdb.ListExpensesParams{TenantIds: []uuid.UUID{carrier},
				Types: fuel, Status: &pending, RowLimit: page})
			return err
		}, []string{"vehicle_expenses"}, []string{"vehicle_expenses_status_date", "vehicle_expenses_pending"}},
		{"ListExpenses by truck", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := expensesdb.New(x).ListExpenses(ctx, expensesdb.ListExpensesParams{TenantIds: reachOwn, Types: fuel,
				TruckID: &truck, RowLimit: page})
			return err
		}, []string{"vehicle_expenses"}, []string{"vehicle_expenses_truck_date"}},
		{"ListExpenses bypass", all, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := expensesdb.New(x).ListExpenses(ctx, expensesdb.ListExpensesParams{AllTenants: true, TenantIds: []uuid.UUID{},
				Types: fuel, RowLimit: page})
			return err
		}, []string{"vehicle_expenses"}, []string{"vehicle_expenses_keyset"}},
		{"ListMaintenance own fleet", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := maintenancedb.New(x).ListMaintenance(ctx, maintenancedb.ListMaintenanceParams{TenantIds: reachOwn, RowLimit: page})
			return err
		}, []string{"maintenance_records"}, []string{"maintenance_tenant_created"}},
		{"ListMaintenance by truck", staffOwn, func(ctx context.Context, x *pgtest.Explainer) error {
			_, err := maintenancedb.New(x).ListMaintenance(ctx, maintenancedb.ListMaintenanceParams{TenantIds: reachOwn,
				TruckID: &truck, RowLimit: page})
			return err
		}, []string{"maintenance_records"}, []string{"maintenance_truck_created", "maintenance_truck_status"}},
	}

	app := s.d.Pool(t, db.RoleApp)
	plan := func(sh planShape, forced bool) (pgtest.Plan, error) {
		var out pgtest.Plan
		err := db.WithPrincipal(ctx, app, sh.p, func(tx pgx.Tx) error {
			if forced {
				if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
					return err
				}
			}
			x := &pgtest.Explainer{Tx: tx}
			if err := sh.call(ctx, x); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if len(x.Plans) != 1 {
				return errors.New("want exactly one planned statement")
			}
			out = x.Last()
			return errRollback
		})
		if errors.Is(err, errRollback) {
			err = nil
		}
		return out, err
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			p, err := plan(sh, true)
			if err != nil {
				t.Fatal(err)
			}
			if seq := p.SeqScans(sh.tables...); len(seq) > 0 {
				t.Errorf("sequential scan of %v with seqscan disabled: no index serves it\n%s", seq, p)
			}
			if got := p.Indexes(); !slices.ContainsFunc(sh.want, func(w string) bool { return slices.Contains(got, w) }) {
				t.Errorf("plan reads indexes %v, want one of %v\n%s", got, sh.want, p)
			}
			n, err := plan(sh, false)
			if err != nil {
				t.Fatal(err)
			}
			if seq := n.SeqScans(large...); len(seq) > 0 {
				t.Errorf("sequential scan of the large table(s) %v with the planner's own choice\n%s", seq, n)
			}
			t.Logf("%s: %s", sh.name, strings.Join(n.Indexes(), ", "))
		})
	}
}
