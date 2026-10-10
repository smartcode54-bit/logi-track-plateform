package authz

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// HeaderActOnTenant is the platform cross-tenant header (Appendix C §C.3.9): <tenant uuid> or *. The
// public listener refuses it with 400 header_not_allowed.
const HeaderActOnTenant = "X-Act-On-Tenant"

// Stable error codes of authorization (Appendix B §B.1.5).
const (
	CodePermissionDenied = "permission_denied"
	CodeTenantRequired   = "tenant_required"
)

type localKey int

const principalKey localKey = iota

// SetPrincipal stores the request principal (auth.RequireAuth).
func SetPrincipal(c fiber.Ctx, p *Principal) { c.Locals(principalKey, p) }

// PrincipalFrom returns the request principal, or nil before authentication.
func PrincipalFrom(c fiber.Ctx) *Principal {
	p, _ := c.Locals(principalKey).(*Principal)
	return p
}

// ErrPermissionDenied is 403 permission_denied; missing lists the capability keys of which the
// principal holds none (details.missingCapability, Appendix C §C.2.8).
func ErrPermissionDenied(msg string, missing ...Cap) *httpx.Error {
	e := httpx.NewError(http.StatusForbidden, CodePermissionDenied, msg)
	if len(missing) > 0 {
		keys := make([]string, len(missing))
		for i, k := range missing {
			keys[i] = string(k)
		}
		e = e.WithDetails(map[string]any{"missingCapability": keys})
	}
	return e
}

// ErrTenantRequired is 403 tenant_required: the route acts on tenant rows and the principal has no
// effective tenant.
func ErrTenantRequired() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodeTenantRequired, "this route needs an active tenant")
}

// principal returns the resolved principal of the request: 401 unauthenticated without one. A
// principal that internal/iam did not resolve holds no capabilities, so every guard fails closed.
func principal(c fiber.Ctx) (*Principal, error) {
	p := PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

// RequireCap passes when the principal holds at least one of caps (any-of; chain two guards for
// both-of). Global keys are only ever in the set of a steward, so a carrier tenant_admin is refused a
// global key even when an override grants it (R60). Otherwise 403 permission_denied with
// details.missingCapability = caps.
func RequireCap(caps ...Cap) fiber.Handler {
	if len(caps) == 0 {
		panic("authz: RequireCap needs at least one capability")
	}
	for _, k := range caps {
		if !Known(k) {
			panic("authz: RequireCap with a key outside the catalog: " + string(k))
		}
	}
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		for _, k := range caps {
			if p.Can(k) {
				return c.Next()
			}
		}
		return ErrPermissionDenied("missing capability", caps...)
	}
}

// RequireTenant passes when the request acts in a tenant (EffectiveTenant, or the read-only bypass of
// X-Act-On-Tenant: *); otherwise 403 tenant_required.
func RequireTenant() fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if p.EffectiveTenant() == nil && !p.ActOnAll {
			return ErrTenantRequired()
		}
		return c.Next()
	}
}

// RequirePlatform passes when the principal holds platform role r; otherwise 403 permission_denied.
func RequirePlatform(r PlatformRole) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if !p.HasPlatform(r) {
			return ErrPermissionDenied("needs platform role " + string(r))
		}
		return c.Next()
	}
}

// RequireSteward passes for a steward (own-fleet staff or platform_admin, R60): explicit for writes of
// platform-wide rows (PUBLIC holidays, platform-wide broadcasts); implied by every global key.
func RequireSteward() fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if !p.Steward {
			return ErrPermissionDenied("platform-wide data is maintained by the own fleet or a platform admin")
		}
		return c.Next()
	}
}
