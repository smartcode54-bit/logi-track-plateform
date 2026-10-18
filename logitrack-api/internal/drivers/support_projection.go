package drivers

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
			p = httpx.TenantReadPolicy(p, schema, meta, false, query...)
			p.SchemaID = schema
			p.MetaSchemaID = meta
			return p
		}
	}
	panic("drivers: unreviewed support route")
}

func driverFields() map[string]httpx.Field {
	f := supportFields("id tenantId tenantKind userId status employmentType contractYears employmentStart employmentEnd hireDate probationPassed licenseType licenseExpiredDate createdAt updatedAt", "tenantNameTh tenantNameEn displayName firstName lastName fullNameTh mobile email truckLicenseId", "", "assignToProject", "legacyDocId legacyAuthUid", "")
	for _, key := range []string{"tenantNameTh", "tenantNameEn"} {
		f[key] = httpx.Field{Kind: httpx.Person, PublicWhen: func(v any) bool {
			var d Driver
			switch t := v.(type) {
			case Driver:
				d = t
			case DriverDetail:
				d = t.Driver
			}
			return d.organizationLabel
		}}
	}
	f["activeTruck"] = httpx.Field{Kind: httpx.Nested, Schema: activeTruckSchema}
	f["currentAssignment"] = httpx.Field{Kind: httpx.Nested, Schema: currentAssignmentSchema}
	return f
}

var activeTruckSchema = httpx.NewSchema[ActiveTruck]("drivers.active-truck", supportFields("truckId truckPlate taskId startedAt", "", "", "", "", ""))
var currentAssignmentSchema = httpx.NewSchema[CurrentAssignment]("drivers.current-assignment", supportFields("id truckId truckPlate truckModel assignedAt", "", "", "", "", ""))
var _ = httpx.NewSchema[Driver]("drivers.driver", driverFields(), func(d Driver) string { return d.TenantID.String() })
var _ = httpx.NewSchema[DriverMinimal]("drivers.minimal", supportFields("id", "displayName", "", "", "", ""))
var statusSchema = httpx.NewSchema[StatusEntry]("drivers.status-entry", func() map[string]httpx.Field {
	f := supportFields("status previousStatus changedAt changedBy", "changedByLabel", "", "reason", "", "")
	f["changedByLabel"] = httpx.Field{Kind: httpx.Person, Owner: func(any) string { return "" }}
	return f
}())
var customerIDSchema = httpx.NewSchema[CustomerDriverID]("drivers.customer-id", func() map[string]httpx.Field {
	f := supportFields("customerId", "customerName", "", "idTypeKey", "customerCode value", "")
	f["customerName"] = httpx.Field{Kind: httpx.Person, Owner: func(any) string { return "" }}
	f["customerCode"] = httpx.Field{Kind: httpx.Person, NullMasked: true, Owner: func(any) string { return "" }}
	f["idTypeKey"] = httpx.Field{Kind: httpx.FreeText, Owner: func(any) string { return "" }}
	return f
}())
var loginSchema = httpx.NewSchema[Login]("drivers.login", supportFields("userId status mustChangePassword", "email", "", "", "", ""), func(Login) string { return "" })
var _ = httpx.NewSchema[DriverDetail]("drivers.detail", func() map[string]httpx.Field {
	f := driverFields()
	for k, v := range supportFields("idCardExpiredDate birthDate", "idCard", "", "", "", "profileKey profileUrl idCardKey idCardUrl licenseKey licenseUrl") {
		f[k] = v
	}
	f["customerDriverIds"] = httpx.Field{Kind: httpx.Nested, Schema: customerIDSchema}
	f["statusHistory"] = httpx.Field{Kind: httpx.Nested, Schema: statusSchema}
	f["login"] = httpx.Field{Kind: httpx.Nested, Schema: loginSchema}
	return f
}(), func(d DriverDetail) string { return d.TenantID.String() })
var _ = httpx.NewSchema[ListMeta]("drivers.meta", map[string]httpx.Field{"statusCounts": {Kind: httpx.Safe, MapKeys: Statuses}})

func supportLabelVersions(c fiber.Ctx, value any) {
	if !httpx.ProjectionAudienceFrom(c).Support {
		return
	}
	versions := map[string]int64{}
	switch v := value.(type) {
	case []Driver:
		for _, d := range v {
			versions[d.TenantID.String()] = d.labelVersion
		}
	case *DriverDetail:
		versions[v.TenantID.String()] = v.labelVersion
	}
	p := authz.PrincipalFrom(c)
	if p != nil {
		p.SupportDataLabelVersions = versions
	}
}
