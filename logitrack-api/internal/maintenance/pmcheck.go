package maintenance

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// PMAlertThresholdKm is the PM alert threshold (fn:core/maintenance.ts PM_ALERT_THRESHOLD_KM, kept in sync with
// web lib/maintenancePm.ts): a PM booking is due when at most this many kilometres are left to the next service.
const PMAlertThresholdKm = 2000

// IsDue is isMaintenanceDue (fn:core/maintenance.ts): the next service is at most PMAlertThresholdKm away, or past.
func IsDue(mileage, nextServiceMileage int64) bool {
	return nextServiceMileage-mileage <= PMAlertThresholdKm
}

// Outcomes of a PM check (the legacy messages of checkMaintenanceAlert, fn:triggers.ts:432-528, as codes).
const (
	OutcomeCreated         = "created"                 // "Created PM Booking task successfully"
	OutcomeNoNextService   = "no_next_service_mileage" // "No nextServiceMileage defined for this truck"
	OutcomeActive          = "active_maintenance"      // "Truck already has an active maintenance task"
	OutcomeAlreadyAlerted  = "already_alerted"         // "Already alert processed for this mileage"
	OutcomeOpenMaintenance = "open_maintenance"        // "Open PM or scheduled maintenance exists for this truck"
	OutcomeWithin          = "within_threshold"        // "Within safe threshold range. Mileage updated."
	OutcomeTruckNotFound   = "truck_not_found"         // the expense names a truck outside the caller's reach
)

// PMResult is the answer of a PM check: whether a PM booking was created (and which), and the outcome code.
type PMResult struct {
	Created       bool       `json:"created"`
	MaintenanceID *uuid.UUID `json:"maintenanceId"`
	Outcome       string     `json:"outcome"`
}

// PM booking fields (fn:triggers.ts:497-509). PMServiceType and PMNotes are exported for cmd/seed, which writes the
// bookings this check would write for the fuel logs it seeds approved (T43).
const (
	PMServiceType = "เช็คระยะตามรอบ"
	pmType        = "PM"
)

// PMNotes is the automatic note of a PM booking (fn:triggers.ts:490-493).
func PMNotes(mileage, next int64) string {
	left := next - mileage
	if left < 0 {
		return "ระบบอัตโนมัติ: เลยกำหนดเช็คระยะ (" + groupThousands(mileage) + " กม. / รอบ " + groupThousands(next) + " กม.)"
	}
	return "ระบบอัตโนมัติ: จากรายการเติมน้ำมัน (" + groupThousands(mileage) + " / " + groupThousands(next) + " กม. เหลือ " +
		groupThousands(left) + " กม. ≤ " + groupThousands(PMAlertThresholdKm) + " กม.)"
}

// CheckPMInTx is the PM check (checkMaintenanceAlert, fn:triggers.ts:432-528) in the caller's transaction: the expense
// approval runs it before COMMIT, so approving a fuel log over the threshold creates exactly one PM booking. The truck
// row is locked first, so concurrent checks of one truck (two approvals, a bulk approval) serialise and the second one
// finds the first one's booking. In order:
//
//  1. no next service mileage on the truck: nothing (no_next_service_mileage);
//  2. an active maintenance still open: nothing (active_maintenance); a terminal one is cleared with the PM alert mark;
//  3. the mileage equals the last alerted mileage: nothing (already_alerted);
//  4. due (at most PMAlertThresholdKm left, or past) and no open PM booking or scheduled visit: a pm_booking record
//     (PM, "เช็คระยะตามรอบ", today on the Bangkok calendar, the mileages, the automatic note) becomes the truck's active
//     maintenance, the truck's last alerted and current mileage are the mileage (created); an open one: nothing
//     (open_maintenance);
//  5. not due: the truck's current mileage is the mileage (within_threshold).
//
// A truck outside the caller's reach is truck_not_found and changes nothing.
func (s *Service) CheckPMInTx(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, truckID uuid.UUID, mileage int32) (PMResult, error) {
	if err := authz.RequireTenantWrite(p); err != nil {
		return PMResult{}, err
	}
	q := maintenancedb.New(tx)
	t, err := lockTruck(ctx, q, p, truckID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PMResult{Outcome: OutcomeTruckNotFound}, nil
	}
	if err != nil {
		return PMResult{}, err
	}
	previous := t.Status
	res, err := s.checkPM(ctx, q, t, p, mileage)
	if err != nil {
		return PMResult{}, err
	}
	if err := s.saveTruck(ctx, tx, q, p, t, previous, rid); err != nil {
		return PMResult{}, err
	}
	if res.Created {
		rec, err := q.LockRecord(ctx, *res.MaintenanceID)
		if err != nil {
			return PMResult{}, err
		}
		if err := emitRecord(ctx, tx, q, RouteCreated, rec, nil, rid); err != nil {
			return PMResult{}, err
		}
	}
	return res, nil
}

func (s *Service) checkPM(ctx context.Context, q *maintenancedb.Queries, t *truckState, p *authz.Principal, mileage int32) (PMResult, error) {
	if t.NextServiceMileage == nil || *t.NextServiceMileage <= 0 {
		return PMResult{Outcome: OutcomeNoNextService}, nil
	}
	next := *t.NextServiceMileage
	if t.ActiveMaintenanceID != nil {
		st, err := q.RecordStatus(ctx, *t.ActiveMaintenanceID)
		switch {
		case err == nil && open(st):
			return PMResult{Outcome: OutcomeActive}, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return PMResult{}, err
		}
		t.setActive(nil)
		t.setLastAlert(nil)
	}
	last := int32(0) // Number(truckData.lastAlertMileage) || 0
	if t.LastAlertMileage != nil {
		last = *t.LastAlertMileage
	}
	if mileage == last {
		return PMResult{Outcome: OutcomeAlreadyAlerted}, nil
	}
	if !IsDue(int64(mileage), int64(next)) {
		t.setCurrentMileage(mileage)
		return PMResult{Outcome: OutcomeWithin}, nil
	}
	busy, err := q.OpenPMBookingExists(ctx, &t.ID)
	if err != nil {
		return PMResult{}, err
	}
	if busy {
		return PMResult{Outcome: OutcomeOpenMaintenance}, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return PMResult{}, err
	}
	user, _ := actor(p)
	plate, brand, notes := t.LicensePlate, t.Brand, PMNotes(int64(mileage), int64(next))
	m, nx := mileage, next
	if _, err := q.InsertRecord(ctx, maintenancedb.InsertRecordParams{ID: id, TenantID: t.TenantID,
		TruckID: &t.ID, PlateSnapshot: &plate, BrandSnapshot: &brand, MaintenanceType: pmType, ServiceType: PMServiceType,
		StartDate: s.today(), Status: StatusPMBooking, CurrentMileage: &m, NextServiceMileage: &nx, Notes: &notes,
		Actor: user}); err != nil {
		return PMResult{}, err
	}
	t.setActive(&id)
	t.setLastAlert(&m)
	t.setCurrentMileage(mileage)
	return PMResult{Created: true, MaintenanceID: &id, Outcome: OutcomeCreated}, nil
}

// PMCheckInput is the body of POST /v1/trucks/{id}/pm-check.
type PMCheckInput struct {
	Mileage *int64 `json:"mileage"`
}

// MaxMileage bounds an odometer reading of the PM check.
const MaxMileage = 10_000_000

// PMCheck is POST /v1/trucks/{id}/pm-check (accounting:audit_expense; checkMaintenanceAlert, which any caller could
// run): CheckPMInTx in its own transaction for a truck of the caller's reach (404 otherwise).
func (s *Service) PMCheck(ctx context.Context, p *authz.Principal, rid string, truckID uuid.UUID, in PMCheckInput) (*PMResult, error) {
	if err := authz.RequireTenantWrite(p); err != nil {
		return nil, err
	}
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	if in.Mileage == nil {
		return nil, httpx.ErrInvalidArgument(violation("mileage", "required"))
	}
	if *in.Mileage < 0 || *in.Mileage > MaxMileage {
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "mileage", Reason: "out_of_range",
			Params: map[string]any{"min": 0, "max": MaxMileage}})
	}
	var res PMResult
	err := s.tx(ctx, p, func(tx pgx.Tx, _ *maintenancedb.Queries) error {
		var err error
		if res, err = s.CheckPMInTx(ctx, tx, p, rid, truckID, int32(*in.Mileage)); err != nil {
			return err
		}
		if res.Outcome == OutcomeTruckNotFound {
			return httpx.ErrNotFound()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}
