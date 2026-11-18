package fleet

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// DriverTruck is a row of GET /v1/mobile/trucks: what the check-in truck picker shows (T55), nothing about cost,
// ownership or documents.
type DriverTruck struct {
	ID           uuid.UUID `json:"id"`
	LicensePlate string    `json:"licensePlate"`
	Province     string    `json:"province"`
	TypeRaw      string    `json:"typeRaw"`
	VehicleClass string    `json:"vehicleClass"`
	Model        string    `json:"model"`
	Status       string    `json:"status"`
}

// DriverTrucks is GET /v1/mobile/trucks for a driver acting as itself (driver:self): the active trucks of its own
// company or partner (RLS p_driver_read ON trucks, .vibe-rules.md "Vehicle identity"), searched by q, in the order and
// pages of GET /v1/trucks. A truck without a vehicle class is left out: it cannot price a trip, so the check-in
// refuses it (unknown_vehicle_class) and the picker never offers it.
func (s *Service) DriverTrucks(ctx context.Context, p *authz.Principal, q string, limit int, cursor string) ([]DriverTruck, string, error) {
	if p == nil || p.DriverID == nil || p.TenantID == nil {
		return nil, "", authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
	}
	tenant := p.TenantID.String()
	res, next, _, err := s.ListTrucks(ctx, p, ListTrucksInput{TenantID: tenant, Status: "active", Q: q, Limit: limit,
		Cursor: cursor})
	if err != nil {
		return nil, "", err
	}
	rows, ok := res.([]Truck)
	if !ok {
		return nil, "", fmt.Errorf("fleet: driver trucks: unexpected list shape %T", res)
	}
	out := make([]DriverTruck, 0, len(rows))
	for _, t := range rows {
		if t.VehicleClass == nil || *t.VehicleClass == "" {
			continue
		}
		out = append(out, DriverTruck{ID: t.ID, LicensePlate: t.LicensePlate, Province: t.Province, TypeRaw: t.Type,
			VehicleClass: *t.VehicleClass, Model: t.Model, Status: t.Status})
	}
	return out, next, nil
}

// DriverRoutes are the fleet routes of the APK 4.x below /v1/mobile (T55), mounted by app.MobileGroups:
//
//	GET /v1/mobile/trucks   mobile:checkin   the driver's company trucks for the check-in picker
//
// requireAuth takes a Go access token only. s may be nil while the route table is only listed.
func DriverRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r.Get("/trucks", requireAuth, authz.RequireCap(authz.MobileCheckin), h.driverTrucks)
	}
}

func (h handlers) driverTrucks(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	rows, next, err := h.s.DriverTrucks(c.Context(), p, c.Query("q"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, rows, next, nil)
}
