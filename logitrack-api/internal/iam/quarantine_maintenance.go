package iam

import (
	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Quarantine rehome is a dedicated maintenance control, never a tenant-write override.
func quarantineMaintenance(p *authz.Principal) error {
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	if err := authz.RequireTenantWrite(p); err != nil {
		return err
	}
	if p.IsMachine() || !p.HasPlatform(authz.PlatformAdmin) || !authz.PlatformCaps(p).Has(authz.PlatformManageTenants) {
		return authz.ErrPermissionDenied("quarantine maintenance requires a platform tenant administrator")
	}
	return nil
}

func requireQuarantineMaintenance(c fiber.Ctx) error {
	if err := quarantineMaintenance(authz.PrincipalFrom(c)); err != nil {
		return err
	}
	return c.Next()
}
