package iam

import (
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"slices"
	"testing"
)

func TestDerivedReadsAreNotEditableMatrixCells(t *testing.T) {
	for _, key := range []authz.Cap{authz.FleetViewMaintenance, authz.FleetViewSubcontractors, authz.UsersViewSessions} {
		if slices.Contains(matrixKeys(), key) {
			t.Fatal("derived key is editable", key)
		}
		for _, layer := range []matrixLayer{ownFleet, carrier, platform} {
			for _, role := range authz.TenantRoles {
				if got := notOverridable(role, key, layer); got != ReasonNotGrantable {
					t.Fatalf("%s %s: %s", role, key, got)
				}
			}
		}
	}
}
