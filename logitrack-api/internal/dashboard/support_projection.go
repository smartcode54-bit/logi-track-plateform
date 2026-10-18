package dashboard

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Labels are never map keys. Every dynamic key below is a closed membership role.
var memberRoles = []string{"tenant_admin", "manager", "operation_staff", "operator", "user", "driver"}

var periodSchema = httpx.NewSchema[Period]("dashboard.period", map[string]httpx.Field{
	"from": {Kind: httpx.Safe}, "to": {Kind: httpx.Safe}, "days": {Kind: httpx.Safe},
})
var usersSchema = httpx.NewSchema[Users]("dashboard.users", map[string]httpx.Field{
	"total": {Kind: httpx.Safe}, "byRole": {Kind: httpx.Safe, MapKeys: memberRoles},
})
var tripsSchema = httpx.NewSchema[Trips]("dashboard.trips", map[string]httpx.Field{
	"delivered": {Kind: httpx.Safe}, "parcels": {Kind: httpx.Safe}, "withIncident": {Kind: httpx.Safe},
	"onTimePct": {Kind: httpx.Safe}, "delayPct": {Kind: httpx.Safe},
})
var daySchema = httpx.NewSchema[DayCount]("dashboard.day-count", map[string]httpx.Field{
	"date": {Kind: httpx.Safe}, "delivered": {Kind: httpx.Safe},
})
var activitySchema = httpx.NewSchema[Activity]("dashboard.activity", map[string]httpx.Field{
	"days": {Kind: httpx.Nested, Schema: daySchema}, "total": {Kind: httpx.Safe},
	"previousTotal": {Kind: httpx.Safe}, "trendPct": {Kind: httpx.Safe},
})
var incidentsSchema = httpx.NewSchema[Incidents]("dashboard.incidents", map[string]httpx.Field{
	"inPeriod": {Kind: httpx.Safe},
})
var driversSchema = httpx.NewSchema[Drivers]("dashboard.drivers", map[string]httpx.Field{
	"active": {Kind: httpx.Safe}, "onDuty": {Kind: httpx.Safe}, "inactive": {Kind: httpx.Safe}, "total": {Kind: httpx.Safe},
})
var expensesSchema = httpx.NewSchema[Expenses]("dashboard.expenses", map[string]httpx.Field{
	"pendingFuel": {Kind: httpx.Safe}, "pendingOther": {Kind: httpx.Safe},
})
var chatsSchema = httpx.NewSchema[Chats]("dashboard.chats", map[string]httpx.Field{
	"unassigned": {Kind: httpx.Safe}, "urgent": {Kind: httpx.Safe},
})
var complianceItemSchema = httpx.NewSchema[ComplianceItem]("dashboard.compliance-item", map[string]httpx.Field{
	"truckId": {Kind: httpx.Safe}, "licensePlate": {Kind: httpx.Safe}, "kind": {Kind: httpx.Safe},
	"dueDate": {Kind: httpx.Safe}, "daysRemaining": {Kind: httpx.Safe}, "kmRemaining": {Kind: httpx.Safe},
})
var complianceSchema = httpx.NewSchema[Compliance]("dashboard.compliance", map[string]httpx.Field{
	"taxDue": {Kind: httpx.Safe}, "insuranceDue": {Kind: httpx.Safe}, "serviceDateDue": {Kind: httpx.Safe},
	"serviceKmDue": {Kind: httpx.Safe}, "itemsTotal": {Kind: httpx.Safe},
	"items": {Kind: httpx.Nested, Schema: complianceItemSchema},
})
var recentSchema = httpx.NewSchema[RecentItem]("dashboard.recent-item", map[string]httpx.Field{
	"type": {Kind: httpx.Safe}, "id": {Kind: httpx.Safe}, "at": {Kind: httpx.Safe}, "plate": {Kind: httpx.Safe},
	"place": {Kind: httpx.Address}, "name": {Kind: httpx.Person}, "text": {Kind: httpx.FreeText},
})
var _ = httpx.NewSchema[Summary]("dashboard.summary", map[string]httpx.Field{
	"period": {Kind: httpx.Nested, Schema: periodSchema}, "users": {Kind: httpx.Nested, Schema: usersSchema},
	"trips": {Kind: httpx.Nested, Schema: tripsSchema}, "activity": {Kind: httpx.Nested, Schema: activitySchema},
	"incidents": {Kind: httpx.Nested, Schema: incidentsSchema}, "drivers": {Kind: httpx.Nested, Schema: driversSchema},
	"expenses": {Kind: httpx.Nested, Schema: expensesSchema}, "chats": {Kind: httpx.Nested, Schema: chatsSchema},
	"compliance": {Kind: httpx.Nested, Schema: complianceSchema}, "recent": {Kind: httpx.Nested, Schema: recentSchema},
})
var _ = httpx.NewSchema[Badges]("dashboard.badges", map[string]httpx.Field{
	"waitlist": {Kind: httpx.Safe}, "pendingExpenses": {Kind: httpx.Safe},
	"unassignedChats": {Kind: httpx.Safe}, "urgentChats": {Kind: httpx.Safe},
})

func dashboardPolicy(route, schema string, query ...string) httpx.EndpointPolicy {
	for _, policy := range authz.SupportPolicySeed() {
		if policy.Method == http.MethodGet && policy.Route == route {
			policy.SchemaID = schema
			policy.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
				if _, err := httpx.StrictQuery(c, query...); err != nil {
					return httpx.PolicyDeny, err
				}
				return httpx.PolicyTenantRead, nil
			}
			return policy
		}
	}
	panic("dashboard: unreviewed support route")
}
