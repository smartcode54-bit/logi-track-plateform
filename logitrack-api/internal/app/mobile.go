package app

import (
	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// MobilePrefix is the driver-app group (R42): public, and on the internal listener too.
const MobilePrefix = "/v1/mobile"

// MobileGroups is the one /v1/mobile group (ingress allows one group per prefix): GET /v1/mobile/events of T12
// (internal/sse MountMobile, SSE ticket) and, of T52, the anonymous version gate GET /v1/mobile/settings
// (public_ip 60/min per client address on the public listener; the web reads it through the BFF on the internal
// listener, where every user would share the BFF's address) and POST /v1/mobile/heartbeat (a driver token,
// heartbeat_install 1/30 s per driver and install). The driver-app API of T55 joins this group.
func MobileGroups(d APIDeps) []ingress.Group {
	o := mobileapp.MobileOptions{Auth: d.Auth.RequireAuth()}
	if d.Limiter != nil {
		o.SettingsLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.PublicIP,
			By: func(c fiber.Ctx) string {
				if httpx.ListenerFrom(c) != ingress.Public {
					return ""
				}
				return ratelimit.ByIP(c)
			}})
		o.HeartbeatLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.HeartbeatInstall,
			By: mobileapp.HeartbeatSubject})
	}
	return []ingress.Group{{Prefix: MobilePrefix, Public: true, Mount: func(r fiber.Router) {
		d.Events.MountMobile(r)
		d.Mobile.MountMobile(r, o)
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
