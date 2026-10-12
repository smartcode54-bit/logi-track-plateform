package app

import (
	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// MobilePrefix is the driver-app group (R42): public, and on the internal listener too.
const MobilePrefix = "/v1/mobile"

// MobileGroups is the one /v1/mobile group (ingress allows one group per prefix): GET /v1/mobile/events of T12
// (internal/sse MountMobile, SSE ticket) and, of T52, the anonymous version gate GET /v1/mobile/settings and POST
// /v1/mobile/heartbeat (a driver token, heartbeat_install 1/30 s per driver and install), and the driver's own chat
// /v1/mobile/chat* of T47 (a driver token, mobile:chat). The driver-app API of T55 joins this group.
//
// The gate's public_ip limit (60/min per client address) applies on both listeners. On the internal listener the
// address is the browser's, as for login_ip: nginx sets X-Forwarded-For and the BFF forwards it from an address of
// TRUSTED_PROXY_CIDRS, so web users do not share one budget, and the BFF's anonymous GET of the gate is no way around
// the limit of the public host.
func MobileGroups(d APIDeps) []ingress.Group {
	o := mobileapp.MobileOptions{Auth: d.Auth.RequireAuth()}
	if d.Limiter != nil {
		o.SettingsLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.PublicIP, By: ratelimit.ByIP})
		o.HeartbeatLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.HeartbeatInstall,
			By: mobileapp.HeartbeatSubject})
	}
	return []ingress.Group{{Prefix: MobilePrefix, Public: true, Mount: func(r fiber.Router) {
		d.Events.MountMobile(r)
		d.Mobile.MountMobile(r, o)
		d.Chats.MountMobile(r, d.Auth.RequireAuth())
	}}}
}

// MobileReleaseGroups are the staff routes of T52 on the internal listener (R43): /v1/app-installations[/stats] and
// /v1/app-releases[/floor] for people (auth.RequireAuth), and the two publish routes /v1/app-releases[/presign], which
// also admit release_publisher API keys (cmd/release with RELEASE_API_KEY, R82).
func MobileReleaseGroups(d APIDeps) []ingress.Group {
	return d.Mobile.Groups(mobileapp.StaffOptions{
		Auth:          d.Auth.RequireAuth(),
		PublisherAuth: d.Auth.RequireAuth(auth.AllowAPIKeys(authz.KeyReleasePublisher)),
	})
}
