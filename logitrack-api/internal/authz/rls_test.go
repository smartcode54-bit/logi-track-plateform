package authz

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func TestPrincipalRLS(t *testing.T) {
	uid, did, key := uuid.New(), uuid.New(), uuid.New()
	party, sub := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name string
		p    Principal
		want db.RLS
	}{
		{"own-fleet manager with contractor reach",
			Principal{UserID: uid, TenantID: ptr(ownFleet), TenantRole: Manager, Steward: true, SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid, TenantID: ptr(ownFleet), Role: "manager", Steward: true, SubtenantIDs: []uuid.UUID{sub}}},
		{"driver: no contractor reach",
			Principal{UserID: uid, TenantID: ptr(ownFleet), TenantRole: Driver, DriverID: ptr(did), SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid, TenantID: ptr(ownFleet), Role: "driver", DriverID: ptr(did)}},
		{"customer scope",
			Principal{UserID: uid, PartyIDs: []uuid.UUID{party}},
			db.RLS{UserID: uid, Role: "customer", CustomerIDs: []uuid.UUID{party}}},
		{"member with a customer-kind scope: membership only",
			Principal{UserID: uid, TenantID: ptr(carrier), TenantRole: User, PartyIDs: []uuid.UUID{party}, SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "user", SubtenantIDs: []uuid.UUID{sub}}},
		{"dispatcher",
			Principal{UserID: uid, TenantID: ptr(carrier), TenantRole: Operator, Dispatcher: true, PartyIDs: []uuid.UUID{party}},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "operator", Dispatcher: true, CustomerIDs: []uuid.UUID{party}}},
		{"platform_admin without the header",
			Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, Steward: true},
			db.RLS{UserID: uid, Steward: true}},
		{"X-Act-On-Tenant: <uuid>",
			Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, ActOnTenant: ptr(carrier), SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid}},
		{"X-Act-On-Tenant: *",
			Principal{UserID: uid, Platform: []PlatformRole{Support}, ActOnAll: true, TenantID: ptr(carrier), TenantRole: Manager},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "manager"}},
		{"support exact read-only reach",
			Principal{UserID: uid, TenantID: ptr(carrier), TenantRole: SupportReadOnly, Steward: true, ActOnAll: true, ActOnTenant: ptr(ownFleet), Dispatcher: true, DriverID: ptr(did), PartyIDs: []uuid.UUID{party}, SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "tenant_admin", ReadOnly: true}},
		{"tenant API key",
			Principal{UserID: uuid.Nil, AMR: AMRAPIKey, APIKeyID: ptr(key), TenantID: ptr(carrier)},
			db.RLS{UserID: key, TenantID: ptr(carrier), Role: "user"}},
		{"platform API key",
			Principal{AMR: AMRAPIKey, APIKeyID: ptr(key), Steward: true},
			db.RLS{UserID: key, Steward: true}},
	} {
		if got := tc.p.RLS(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

// TestNilPrincipal: authz.PrincipalFrom returns a nil *Principal on a route mounted without
// RequireAuth; RLS on it fails closed and db.WithPrincipal refuses it before BEGIN instead of panicking.
func TestNilPrincipal(t *testing.T) {
	var p *Principal
	if got := p.RLS(); !reflect.DeepEqual(got, db.RLS{}) {
		t.Errorf("nil principal RLS %+v, want the empty context", got)
	}
	err := db.WithPrincipal(context.Background(), nil, p, func(pgx.Tx) error {
		t.Fatal("fn ran for a nil principal")
		return nil
	})
	if !errors.Is(err, db.ErrNoPrincipal) {
		t.Fatalf("WithPrincipal with a nil *Principal: %v, want db.ErrNoPrincipal", err)
	}
}

// TestTenantReach: the reach a repository names on a G-tenant table equals the one RLS p_tenant_staff applies.
func TestTenantReach(t *testing.T) {
	uid, did, key, party, sub := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		p       Principal
		all     bool
		tenants []uuid.UUID
	}{
		{"own-fleet manager: own tenant and contractor reach",
			Principal{UserID: uid, TenantID: ptr(ownFleet), TenantRole: Manager, SubtenantIDs: []uuid.UUID{sub}},
			false, []uuid.UUID{ownFleet, sub}},
		{"carrier tenant_admin", Principal{UserID: uid, TenantID: ptr(carrier), TenantRole: TenantAdmin}, false, []uuid.UUID{carrier}},
		{"driver: no staff reach",
			Principal{UserID: uid, TenantID: ptr(ownFleet), TenantRole: Driver, DriverID: ptr(did), SubtenantIDs: []uuid.UUID{sub}},
			false, []uuid.UUID{}},
		{"customer scope", Principal{UserID: uid, PartyIDs: []uuid.UUID{party}}, false, []uuid.UUID{}},
		{"platform_admin without the header", Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, Steward: true},
			false, []uuid.UUID{}},
		{"X-Act-On-Tenant: <uuid>",
			Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, ActOnTenant: ptr(carrier), SubtenantIDs: []uuid.UUID{sub}},
			false, []uuid.UUID{}},
		{"X-Act-On-Tenant: *", Principal{UserID: uid, Platform: []PlatformRole{Support}, ActOnAll: true, TenantID: ptr(carrier),
			TenantRole: Manager}, false, []uuid.UUID{carrier}},
		{"tenant API key: role user in its tenant", Principal{AMR: AMRAPIKey, APIKeyID: ptr(key), TenantID: ptr(carrier)},
			false, []uuid.UUID{carrier}},
		{"platform API key", Principal{AMR: AMRAPIKey, APIKeyID: ptr(key), Steward: true}, false, []uuid.UUID{}},
	} {
		all, tenants := tc.p.TenantReach()
		if all != tc.all || !reflect.DeepEqual(tenants, tc.tenants) {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", tc.name, all, tenants, tc.all, tc.tenants)
		}
	}
	var p *Principal
	if all, tenants := p.TenantReach(); all || tenants == nil || len(tenants) != 0 {
		t.Errorf("nil principal: (%v, %v), want (false, [])", all, tenants)
	}
}
