package drivers_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"testing"
)

func TestSupportDirectFleetWritesRefuseBeforeDependencies(t *testing.T) {
	ctx := context.Background()
	p := &authz.Principal{TenantRole: authz.SupportReadOnly}
	id := uuid.New()
	checks := []func() error{
		func() error { _, e := (&drivers.Service{}).Create(ctx, p, "", drivers.DriverInput{}); return e },
		func() error { _, e := (&drivers.Service{}).Patch(ctx, p, "", id, drivers.DriverInput{}); return e },
		func() error { _, e := (&fleet.Service{}).CreateTruck(ctx, p, "", fleet.TruckInput{}); return e },
		func() error { _, e := (&fleet.Service{}).PatchTruck(ctx, p, "", id, fleet.TruckInput{}); return e },
		func() error { _, e := (&fleet.Service{}).ImportTrucks(ctx, p, "", fleet.ImportInput{}); return e },
		func() error {
			_, _, e := (&fleet.Service{}).RecordRenewal(ctx, p, "", id, fleet.RenewalInput{})
			return e
		},
		func() error { _, e := (&fleet.Service{}).CreateAssignment(ctx, p, "", fleet.AssignInput{}); return e },
		func() error { _, e := (&fleet.Service{}).RevokeAssignment(ctx, p, "", id); return e },
		func() error { _, e := (&maintenance.Service{}).Create(ctx, p, "", maintenance.Input{}); return e },
		func() error { _, e := (&maintenance.Service{}).Patch(ctx, p, "", id, maintenance.Input{}); return e },
		func() error {
			_, e := (&maintenance.Service{}).Remind(ctx, p, "", id, maintenance.RemindInput{})
			return e
		},
		func() error {
			_, e := (&maintenance.Service{}).PMCheck(ctx, p, "", id, maintenance.PMCheckInput{})
			return e
		},
		func() error { _, e := (&maintenance.Service{}).CheckPMInTx(ctx, nil, p, "", id, 0); return e },
		func() error { _, e := (&locations.Service{}).Probe(ctx, p); return e },
	}
	for i, fn := range checks {
		e := fn()
		var typed *httpx.Error
		if !errors.As(e, &typed) || typed.Code != httpx.CodeSupportReadOnly {
			t.Fatalf("adapter %d: %v", i, e)
		}
	}
	if _, e := (&drivers.Service{}).DocumentURL(ctx, p, id, "profile"); e == nil {
		t.Fatal("support document allowed")
	}
}
