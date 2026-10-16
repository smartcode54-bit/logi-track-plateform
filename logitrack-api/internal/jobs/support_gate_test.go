package jobs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestPlatformJobPayloadRequiresOwnerOrOwnTenant(t *testing.T) {
	user, other, own, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, platform := range []Caller{{PlatformAdmin: true}, {PlatformSupport: true}} {
		platform.UserID, platform.TenantID = user, &own
		platform.Can = func(authz.Cap) bool { return true }
		for _, readerTenant := range []*uuid.UUID{nil, &own} {
			platform.TenantID = readerTenant
			for _, tenant := range []*uuid.UUID{nil, &own, &foreign} {
				if !platform.canRead(Job{TenantID: tenant, OwnerUserID: &user, Type: "payroll.run"}) {
					t.Errorf("platform owner lost job access: reader tenant %v, job tenant %v", readerTenant, tenant)
				}
				want := readerTenant != nil && tenant != nil && *tenant == *readerTenant
				if got := platform.canRead(Job{TenantID: tenant, OwnerUserID: &other, Type: "payroll.run"}); got != want {
					t.Errorf("foreign-owned payload access: reader tenant %v, job tenant %v: %t, want %t", readerTenant, tenant, got, want)
				}
			}
		}
		platform.TenantID, platform.Can = &own, nil
		if platform.canRead(Job{TenantID: &own, OwnerUserID: &other, Type: "payroll.run"}) {
			t.Fatal("non-owner without the job capability can read payload")
		}
	}
	if !(Caller{UserID: user}).canRead(Job{TenantID: &foreign, OwnerUserID: &user}) {
		t.Fatal("ordinary owner contract changed")
	}
}
