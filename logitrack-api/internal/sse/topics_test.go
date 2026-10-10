package sse

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

var (
	uid  = uuid.MustParse("0199c000-0000-7000-8000-0000000000a1")
	did  = uuid.MustParse("0199c000-0000-7000-8000-0000000000d1")
	own  = uuid.MustParse("0199c000-0000-7000-8000-0000000000f0")
	subA = uuid.MustParse("0199c000-0000-7000-8000-0000000000c1")
)

// staff is a resolved principal of a tenant role, its role default set as capabilities.
func staff(tenant uuid.UUID, role authz.TenantRole, kind string, subs ...uuid.UUID) *authz.Principal {
	p := &authz.Principal{UserID: uid, TenantID: &tenant, TenantRole: role, TenantKind: kind, SubtenantIDs: subs}
	p.Caps = authz.TenantDefaults(role)
	return p
}

func TestWebTopicsOfAnOwnFleetTenantAdmin(t *testing.T) {
	p := staff(own, authz.TenantAdmin, authz.TenantKindOwnFleet, subA)
	got := WebTopics(p)
	want := []string{rt.TopicGlobal, rt.TopicPlatformSecurity, rt.UserTopic(uid.String())}
	for _, f := range []string{"tasks", "trips", "chats", "fleet", "hr", "expenses", "billing", "vehicle_locations"} {
		want = append(want, rt.TenantTopic(own.String(), f), rt.TenantTopic(subA.String(), f))
	}
	want = append(want, rt.TenantTopic(own.String(), rt.FamilyConfig))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for _, topic := range got {
		if !rt.ValidTopic(topic) {
			t.Errorf("%s is outside the catalogue", topic)
		}
	}
}

func TestWebTopicsFollowCapabilities(t *testing.T) {
	carrier := uuid.MustParse("0199c000-0000-7000-8000-0000000000c9")
	p := staff(carrier, authz.User, authz.TenantKindCarrier)
	p.Caps = authz.NewCapSet(authz.FleetViewTrucks)
	got := WebTopics(p)
	want := []string{rt.TopicGlobal, rt.TenantTopic(carrier.String(), rt.FamilyConfig),
		rt.TenantTopic(carrier.String(), rt.FamilyFleet), rt.UserTopic(uid.String())}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// security:view_audit opens platform:security only for platform principals and the own fleet.
	p.Caps.Add(authz.SecurityViewAudit)
	if slices.Contains(WebTopics(p), rt.TopicPlatformSecurity) {
		t.Fatal("a carrier's audit viewer got platform:security")
	}
}

func TestWebTopicsOfScopeDriverAndPlatformPrincipals(t *testing.T) {
	// A dispatcher gets dispatch:* instead of the tenant tasks and trips topics.
	d := uuid.MustParse("0199c000-0000-7000-8000-0000000000dd")
	disp := staff(d, authz.Operator, authz.TenantKindCarrier)
	disp.Dispatcher, disp.PartyIDs = true, []uuid.UUID{uuid.New()}
	disp.Caps = disp.Caps.Union(authz.ScopeDefaults(authz.ScopeDispatcher))
	got := WebTopics(disp)
	for _, want := range []string{rt.TopicDispatchTasks, rt.TopicDispatchTrips, rt.TopicGlobal} {
		if !slices.Contains(got, want) {
			t.Errorf("dispatcher lacks %s: %v", want, got)
		}
	}
	for _, topic := range got {
		if strings.HasSuffix(topic, ":tasks") && strings.HasPrefix(topic, "tenant:") ||
			strings.HasSuffix(topic, ":trips") && strings.HasPrefix(topic, "tenant:") {
			t.Errorf("dispatcher got the tenant topic %s", topic)
		}
	}

	// A customer-scope principal: only its own user topic.
	cust := &authz.Principal{UserID: uid, PartyIDs: []uuid.UUID{uuid.New()}}
	cust.Caps = authz.ScopeDefaults(authz.ScopeCustomer)
	if got := WebTopics(cust); !slices.Equal(got, []string{rt.UserTopic(uid.String())}) {
		t.Fatalf("customer scope: %v", got)
	}

	// A driver: user, driver and its tenant's config topic; never global.
	drv := staff(own, authz.Driver, authz.TenantKindOwnFleet, subA)
	drv.DriverID = &did
	want := []string{rt.DriverTopic(did.String()), rt.TenantTopic(own.String(), rt.FamilyConfig), rt.UserTopic(uid.String())}
	slices.Sort(want)
	if got := WebTopics(drv); !slices.Equal(got, want) {
		t.Fatalf("driver: %v", got)
	}
	if got := MobileTopics(drv); !slices.Equal(got, []string{rt.DriverTopic(did.String()), rt.UserTopic(uid.String())}) {
		t.Fatalf("mobile driver: %v", got)
	}

	// A platform admin without a tenant: global and platform:security, no tenant topic.
	pa := &authz.Principal{UserID: uid, Platform: []authz.PlatformRole{authz.PlatformAdmin}}
	pa.Caps = authz.PlatformDefaults(authz.PlatformAdmin).Union(authz.NewCapSet(authz.SecurityViewAudit))
	want = []string{rt.TopicGlobal, rt.TopicPlatformSecurity, rt.UserTopic(uid.String())}
	if got := WebTopics(pa); !slices.Equal(got, want) {
		t.Fatalf("platform admin: %v", got)
	}
	// Acting as a tenant (X-Act-On-Tenant: <uuid>) gives that tenant's topics; "*" none.
	pa.ActOnTenant = &own
	pa.TenantKind = authz.TenantKindOwnFleet
	pa.Caps = pa.Caps.Union(authz.TenantDefaults(authz.TenantAdmin))
	if got := WebTopics(pa); !slices.Contains(got, rt.TenantTopic(own.String(), rt.FamilyTrips)) {
		t.Fatalf("acting as the own fleet: %v", got)
	}
}
