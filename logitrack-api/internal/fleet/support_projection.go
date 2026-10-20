package fleet

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func supportFields(safe, person, money, text, private, files string) map[string]httpx.Field {
	f := map[string]httpx.Field{}
	for kind, names := range map[httpx.FieldKind]string{httpx.Safe: safe, httpx.Person: person, httpx.Money: money, httpx.FreeText: text, httpx.File: files} {
		for _, name := range strings.Fields(names) {
			f[name] = httpx.Field{Kind: kind}
		}
	}
	for _, name := range strings.Fields(private) {
		f[name] = httpx.Field{Kind: httpx.Person, NullMasked: true}
	}
	return f
}
func supportPolicy(route, schema, meta string, query ...string) httpx.EndpointPolicy {
	for _, p := range authz.SupportPolicySeed() {
		if p.Method == http.MethodGet && p.Route == route {
			p = httpx.TenantReadPolicy(p, schema, meta, route == TrucksPrefix+"/renewals", query...)
			p.SchemaID = schema
			p.MetaSchemaID = meta
			if route == AssignmentsPrefix {
				base := p.Predicate
				p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
					if c.Query("q") != "" {
						return httpx.PolicyDeny, httpx.ErrSupportViewUnavailable()
					}
					return base(c)
				}
			}
			return p
		}
	}
	panic("fleet: unreviewed support route")
}

func truckFields() map[string]httpx.Field {
	return supportFields("id tenantId ownershipType licensePlate province vin engineNumber status brand model year color type vehicleClass seats fuelType engineCapacity fuelCapacity maxLoadWeightKg registrationDate buyingDate taxExpiryDate taxRenewalStatus lastServiceDate nextServiceDate nextServiceMileage pmIntervalKm currentMileage lastAlertMileage activeMaintenanceId insuranceCompany insuranceType insuranceStartDate insuranceExpiryDate insuranceRenewalStatus paymentMethod createdBy updatedBy createdAt updatedAt", "", "taxExpenseThb insurancePremiumThb", "legacyStatus notes taxResponsible maintenanceResponsible insuranceNotes", "gpsVehicleId insurancePolicyId insurancePolicyNumber", "")
}

var _ = httpx.NewSchema[Truck]("fleet.truck", truckFields(), func(t Truck) string { return t.TenantID.String() })
var statusSchema = httpx.NewSchema[StatusEntry]("fleet.status-entry", func() map[string]httpx.Field {
	f := supportFields("status previousStatus changedAt changedBy", "changedByLabel", "", "reason", "", "")
	f["changedByLabel"] = httpx.Field{Kind: httpx.Person, Owner: func(any) string { return "" }}
	return f
}())
var assignmentSchema = httpx.NewSchema[Assignment]("fleet.assignment", supportFields("id driverId assignedAt", "driverName", "", "", "", ""), func(a Assignment) string { return a.driverOwner })
var fileSchema = httpx.NewSchema[TruckFile]("fleet.file", map[string]httpx.Field{"key": {Kind: httpx.File}, "url": {Kind: httpx.File, BrokerPath: true}})
var _ = httpx.NewSchema[TruckDetail]("fleet.truck-detail", func() map[string]httpx.Field {
	f := truckFields()
	f["statusHistory"] = httpx.Field{Kind: httpx.Nested, Schema: statusSchema}
	f["currentAssignments"] = httpx.Field{Kind: httpx.Nested, Schema: assignmentSchema}
	f["files"] = httpx.Field{Kind: httpx.Nested, Schema: fileSchema, MapKeys: []string{"imageFrontRight", "imageFrontLeft", "imageBackRight", "imageBackLeft", "documentTax", "documentRegister", "taxReceipt", "insuranceReceipt", "insuranceDocument", "legacyImage", "other"}}
	return f
}(), func(t TruckDetail) string { return t.TenantID.String() })
var _ = httpx.NewSchema[TruckListMeta]("fleet.truck-meta", map[string]httpx.Field{"statusCounts": {Kind: httpx.Safe, MapKeys: TruckStatuses}})

func renewalFields() map[string]httpx.Field {
	return supportFields("expiryDate renewalStatus state daysRemaining", "", "amountThb", "", "", "")
}

var taxSchema = httpx.NewSchema[TaxRenewal]("fleet.tax-renewal", func() map[string]httpx.Field {
	f := renewalFields()
	f["responsible"] = httpx.Field{Kind: httpx.FreeText}
	return f
}())
var insuranceSchema = httpx.NewSchema[InsuranceRenewal]("fleet.insurance-renewal", func() map[string]httpx.Field {
	f := renewalFields()
	f["company"] = httpx.Field{Kind: httpx.Safe}
	f["coverage"] = httpx.Field{Kind: httpx.Safe}
	f["policyNumber"] = httpx.Field{Kind: httpx.Person, NullMasked: true}
	return f
}())
var _ = httpx.NewSchema[Renewal]("fleet.renewal", func() map[string]httpx.Field {
	f := supportFields("truckId tenantId ownershipType licensePlate province status type vehicleClass paymentMethod nextDueDate", "", "", "", "", "")
	f["tax"] = httpx.Field{Kind: httpx.Nested, Schema: taxSchema}
	f["insurance"] = httpx.Field{Kind: httpx.Nested, Schema: insuranceSchema}
	return f
}(), func(r Renewal) string { return r.TenantID.String() })
var _ = httpx.NewSchema[TruckAssignment]("fleet.truck-assignment", func() map[string]httpx.Field {
	f := supportFields("id tenantId truckId truckPlate truckModel driverId status adminUserId createdAt revokedAt", "driverName adminLabel", "", "", "", "")
	f["driverName"] = httpx.Field{Kind: httpx.Person, Owner: func(v any) string { return v.(TruckAssignment).driverOwner }}
	f["adminLabel"] = httpx.Field{Kind: httpx.Person, Owner: func(any) string { return "" }}
	return f
}(), func(a TruckAssignment) string { return a.TenantID.String() })
