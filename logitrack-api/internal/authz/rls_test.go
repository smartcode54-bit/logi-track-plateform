package authz

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

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
		{"dispatcher",
			Principal{UserID: uid, TenantID: ptr(carrier), TenantRole: Operator, Dispatcher: true, PartyIDs: []uuid.UUID{party}},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "operator", Dispatcher: true, CustomerIDs: []uuid.UUID{party}}},
		{"platform_admin without the header",
			Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, Steward: true},
			db.RLS{UserID: uid, Steward: true}},
		{"X-Act-On-Tenant: <uuid>",
			Principal{UserID: uid, Platform: []PlatformRole{PlatformAdmin}, ActOnTenant: ptr(carrier), SubtenantIDs: []uuid.UUID{sub}},
			db.RLS{UserID: uid, TenantID: ptr(carrier), Role: "tenant_admin", Steward: true, SubtenantIDs: []uuid.UUID{sub}}},
		{"X-Act-On-Tenant: *",
			Principal{UserID: uid, Platform: []PlatformRole{Support}, ActOnAll: true, TenantID: ptr(carrier), TenantRole: Manager},
			db.RLS{UserID: uid, Bypass: true, ReadOnly: true}},
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
