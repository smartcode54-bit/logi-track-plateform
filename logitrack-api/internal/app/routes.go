package app

import (
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/linenotify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/webcfg"
)

// FullAPI is the shared, I/O-free registration root used by serving and contract discovery.
func FullAPI(cfg *APIConfig, log zerolog.Logger, deps APIDeps, extra ...ingress.Group) (*API, error) {
	groups := append(deps.Auth.Groups(), JobGroups(deps)...)
	// GET /v1/roles also admits integration and script API keys (the catalog a script picks capabilities
	// from); the role matrix of T51 and every other route here admit people only (T32K).
	groups = append(groups, iam.RoleGroups(deps.Admin, deps.Auth.RequireAuth(),
		deps.Auth.RequireAuth(auth.AllowAPIKeys(authz.KeyIntegration, authz.KeyScript)))...)
	groups = append(groups, iam.AdminGroups(deps.Admin, deps.Auth.RequireAuth())...)
	groups = append(groups, iam.APIKeyGroups(deps.APIKeys, deps.Auth.RequireAuth())...)
	groups = append(groups, parties.Groups(deps.Parties, deps.Auth.RequireAuth())...)
	groups = append(groups, parties.SupportGroups(deps.Parties, deps.Auth.RequireAuth())...)
	groups = append(groups, hubs.Groups(deps.Hubs, deps.Auth.RequireAuth())...)
	groups = append(groups, fleet.Groups(deps.Fleet, deps.Auth.RequireAuth(),
		maintenance.TruckRoutes(deps.Maintenance, deps.Auth.RequireAuth()))...)
	groups = append(groups, drivers.Groups(deps.Drivers, deps.Auth.RequireAuth())...)
	groups = append(groups, expenses.Groups(deps.Expenses, deps.Auth.RequireAuth())...)
	groups = append(groups, maintenance.Groups(deps.Maintenance, deps.Auth.RequireAuth())...)
	groups = append(groups, billing.Groups(deps.Billing, deps.Auth.RequireAuth())...)
	groups = append(groups, billing.JobGroups(deps.Billing, deps.Auth.RequireAuth())...)
	groups = append(groups, operations.Groups(deps.Operations, deps.Auth.RequireAuth(), operations.Mounts{
		Tasks: []func(fiber.Router){linenotify.TaskRoutes(deps.LineNotify, deps.Auth.RequireAuth())},
		Trips: []func(fiber.Router){billing.TripRoutes(deps.Billing, deps.Auth.RequireAuth()),
			linenotify.TripRoutes(deps.LineNotify, deps.Auth.RequireAuth())},
		Standby: []func(fiber.Router){billing.StandbyRoutes(deps.Billing, deps.Auth.RequireAuth()),
			linenotify.StandbyRoutes(deps.LineNotify, deps.Auth.RequireAuth())},
	})...)
	groups = append(groups, hr.Groups(deps.HR, deps.Auth.RequireAuth())...)
	groups = append(groups, chats.Groups(deps.Chats, deps.Auth.RequireAuth())...)
	groups = append(groups, broadcasts.Groups(deps.Broadcasts, deps.Auth.RequireAuth())...)
	groups = append(groups, webcfg.Group(cfg.WebFlags))
	groups = append(groups, StorageGroups(deps)...)
	groups = append(groups, EventGroups(deps)...)
	groups = append(groups, MobileGroups(deps)...)
	groups = append(groups, MobileReleaseGroups(deps)...)
	groups = append(groups, DashboardGroups(deps)...)
	return NewAPI(cfg, log, append(groups, extra...)...)
}
