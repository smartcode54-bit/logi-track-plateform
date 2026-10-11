package app

import (
	"context"

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
// The forms' rate limit (Appendix B §B.6.3): public_form_ip per client address (RATE_LIMIT_PUBLIC_FORMS, 5/h) in the
// route middleware, counting every post; then public_form_email per email (1/day, a code constant), spent by the forms
// service only for a post that passed validation, in the transaction that stores it (forms.EmailLimit over
// Limiter.Check: a 422 or a failed insert does not lock the visitor out for a day). The client address is the
// visitor's: the BFF forwards X-Forwarded-For from an address of TRUSTED_PROXY_CIDRS (as for login_ip). Both forms
// spend the same two buckets.
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
		o.FormLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.PublicFormIP, Limit: ip, By: ratelimit.ByIP})
		limiter, enabled := d.Limiter, d.RateLimitEnabled
		o.EmailLimit = func(ctx context.Context, email string) error {
			return limiter.Check(ctx, enabled, ratelimit.PublicFormEmail, ratelimit.Limit{}, forms.EmailSubject(email))
		}
	}
	return append(groups, forms.Groups(d.Forms, o)...)
}
