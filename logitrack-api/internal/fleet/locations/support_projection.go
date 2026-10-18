package locations

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
	panic("locations: unreviewed support route")
}

var _ = httpx.NewSchema[Location]("locations.location", func() map[string]httpx.Field {
	f := supportFields("truckId tenantId licensePlate speedKmh heading lastGpsAt updatedAt engineOn odometerKm fuelLevel fuelPercentage", "driverName", "", "positionDescription", "gpsVehicleId", "")
	f["lat"] = httpx.Field{Kind: httpx.Address}
	f["lng"] = httpx.Field{Kind: httpx.Address}
	// Cartrack supplies an unattributed raw name, which selected-tenant consent cannot authorize.
	f["driverName"] = httpx.Field{Kind: httpx.Person, Owner: func(any) string { return "" }}
	return f
}(), func(l Location) string { return l.TenantID.String() })
