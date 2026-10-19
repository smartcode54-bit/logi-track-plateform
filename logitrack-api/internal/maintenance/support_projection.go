package maintenance

import (
	"net/http"
	"strings"

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
			p = httpx.TenantReadPolicy(p, schema, meta, false, query...)
			p.SchemaID = schema
			p.MetaSchemaID = meta
			return p
		}
	}
	panic("maintenance: unreviewed support route")
}

var filesSchema = httpx.NewSchema[RecordFiles]("maintenance.files", map[string]httpx.Field{"images": {Kind: httpx.File, BrokerPath: true}, "receipts": {Kind: httpx.File, BrokerPath: true}, "invoices": {Kind: httpx.File, BrokerPath: true}})
var _ = httpx.NewSchema[Record]("maintenance.record", func() map[string]httpx.Field {
	f := supportFields("id tenantId truckId licensePlate truckBrand truckModel truckOwnership truckStatus type startDate endDate status appointmentTime pickupAppointmentAt paymentMethod currentMileage nextServiceMileage driverSubmitted checkInAt checkOutAt createdBy updatedBy createdAt updatedAt", "", "costLaborThb costPartsThb totalCostThb invoiceAmountThb displayCostThb", "serviceType provider notes", "", "")
	f["providerLat"] = httpx.Field{Kind: httpx.Address}
	f["providerLng"] = httpx.Field{Kind: httpx.Address}
	f["files"] = httpx.Field{Kind: httpx.Nested, Schema: filesSchema}
	return f
}(), func(r Record) string { return r.TenantID.String() })
var _ = httpx.NewSchema[listMeta]("maintenance.meta", map[string]httpx.Field{"statusCounts": {Kind: httpx.Safe, MapKeys: Statuses}})
