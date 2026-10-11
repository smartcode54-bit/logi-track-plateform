package app

import (
	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/dashboard"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/forms"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// DashboardGroups are the P6 platform routes of T53 on the internal listener: GET /v1/vehicle-locations and GET
// /v1/admin/cartrack/probe (internal/fleet/locations), GET /v1/dashboard/summary and GET /v1/badges
// (internal/dashboard), and the forms: the anonymous POST /v1/waitlist and POST /v1/partner-interest and the staff GET
// /v1/waitlist, DELETE /v1/waitlist/{id} (internal/forms). None is public: the form routes are reached only through
// the BFF's unauthenticated POST /api/forms/* handlers (R44, R77), so the public listener answers 404 for them.
//
// The forms' rate limit (Appendix B §B.6.3): public_form_ip per client address (RATE_LIMIT_PUBLIC_FORMS, 5/h), then
// public_form_email per email of the body (1/day, a code constant). The client address is the visitor's: the BFF
// forwards X-Forwarded-For from an address of TRUSTED_PROXY_CIDRS (as for login_ip). Both forms spend the same two
// buckets.
func DashboardGroups(d APIDeps) []ingress.Group {
	auth := d.Auth.RequireAuth()
	groups := locations.Groups(d.Locations, auth)
	groups = append(groups, dashboard.Groups(d.Dashboard, auth)...)
	o := forms.Options{Auth: auth}
	if d.Limiter != nil {
		ip := d.PublicFormsLimit
		if !ip.Valid() {
			ip = ratelimit.PublicFormIP.Default
		}
		o.FormLimit = d.Limiter.Middleware(d.RateLimitEnabled,
			ratelimit.Rule{Bucket: ratelimit.PublicFormIP, Limit: ip, By: ratelimit.ByIP},
			ratelimit.Rule{Bucket: ratelimit.PublicFormEmail, By: func(c fiber.Ctx) string { return forms.EmailSubject(c.Body()) }})
	}
	return append(groups, forms.Groups(d.Forms, o)...)
}
