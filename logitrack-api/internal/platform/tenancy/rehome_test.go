package tenancy

import (
	"slices"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The files Follow moves are found by owner kind: every kind must be a file_objects.owner_kind (storage.Owner*),
// and every table Rehome accepts either owns files under one kind or is known to own none.
func TestFileOwnerKinds(t *testing.T) {
	kinds := []string{storage.OwnerTrip, storage.OwnerTask, storage.OwnerStandby, storage.OwnerIncident, storage.OwnerChat,
		storage.OwnerDriver, storage.OwnerTruck, storage.OwnerCompany, storage.OwnerCustomer, storage.OwnerTenant,
		storage.OwnerMaintenance, storage.OwnerExpense, storage.OwnerLeave, storage.OwnerRelease, storage.OwnerUser,
		storage.OwnerStatement, storage.OwnerReport, storage.OwnerPenalty}
	noFiles := []string{"truck_assignments", "payroll_runs"}
	for table, kind := range fileOwnerKinds {
		if _, ok := rehomeTables[table]; !ok {
			t.Errorf("%s owns files but is not a table Rehome accepts", table)
		}
		if !slices.Contains(kinds, kind) {
			t.Errorf("%s: owner kind %q is not a storage owner kind", table, kind)
		}
	}
	for _, table := range RehomeTables() {
		if _, ok := fileOwnerKinds[table]; !ok && !slices.Contains(noFiles, table) {
			t.Errorf("%s: Rehome accepts it but Follow would not move its files", table)
		}
	}
}
