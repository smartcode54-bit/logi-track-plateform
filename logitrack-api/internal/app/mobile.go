package app

import (
	"net/http"
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/linenotify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// MobilePrefix is the driver-app group (R42): public, and on the internal listener too.
const MobilePrefix = "/v1/mobile"

// The callable labels of the Cloud Functions shims in shim_requests_total{callable} (R26), one per shim target.
const (
	ShimSetDriverClaims    = "setDriverClaims"            // GET /v1/mobile/me (T32)
	ShimAddDeliveryStop    = "addDeliveryStop"            // POST /v1/mobile/tasks/{id}/stops (T32)
	ShimComputeTripBilling = "computeTripBillingSnapshot" // POST /v1/mobile/trips/{id}/billing/compute (T38)
	ShimMarkBroadcastRead  = "markBroadcastRead"          // POST /v1/mobile/broadcasts/{id}/read (T48)
	// ShimSendLineNotification is POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify (T49).
	ShimSendLineNotification = "sendCustomerLineNotification"
)

// MobileGroups is the one /v1/mobile group (ingress allows one group per prefix): GET /v1/mobile/events of T12
// (internal/sse MountMobile, SSE ticket) and, of T52, the anonymous version gate GET /v1/mobile/settings and POST
// /v1/mobile/heartbeat (a driver token, heartbeat_install 1/30 s per driver and install), the callable shim targets
// (a cf_shim API key with the caller's Firebase ID token, auth.RequireShim, R45): GET /v1/mobile/me (setDriverClaims;
// also a Go driver token since T55, auth.RequireShimOrAuth)
// and POST /v1/mobile/tasks/{id}/stops (addDeliveryStop, then mobile:submit_trip) of T32, POST
// /v1/mobile/trips/{id}/billing/compute (computeTripBillingSnapshot) of T38, the three POST
// /v1/mobile/{tasks|trips|standby}/{id}/line-notify (sendCustomerLineNotification) of T49, the driver's own chat
// /v1/mobile/chat* of T47 (a driver token, mobile:chat), and the driver's broadcasts of T48: GET
// /v1/mobile/broadcasts[/latest] (a driver token, mobile:broadcasts) and POST /v1/mobile/broadcasts/{id}/read
// (markBroadcastRead, then mobile:broadcasts). The driver-app API of T55 joins this group.
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
		r.Get("/me", d.Auth.RequireShimOrAuth(ShimSetDriverClaims), mobileMe(d.Auth, d.Mobile))
		operations.MobileRoutes(d.Operations, d.Auth.RequireShim(ShimAddDeliveryStop))(r)
		operations.DriverRoutes(d.Operations, d.Auth.RequireAuth(), idempotencyHandler(d))(r)
		fleet.DriverRoutes(d.Fleet, d.Auth.RequireAuth())(r)
		presign := []any{}
		if d.Limiter != nil {
			presign = append(presign, d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.PresignUser,
				By: func(c fiber.Ctx) string { return IdempotencyScope(c) }}))
		}
		r.Post("/uploads/presign", d.Auth.RequireAuth(), append(presign, mobilePresign(d.Storage))...)
		billing.MobileRoutes(d.Billing, d.Auth.RequireShim(ShimComputeTripBilling))(r)
		linenotify.MobileRoutes(d.LineNotify, d.Auth.RequireShim(ShimSendLineNotification))(r)
		d.Chats.MountMobile(r, d.Auth.RequireAuth())
		broadcasts.MobileRoutes(d.Broadcasts, d.Auth.RequireAuth(), d.Auth.RequireShim(ShimMarkBroadcastRead))(r)
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

// HeaderAppFlavor is the driver app's flavor header (§11.2): X-App-Flavor.
const HeaderAppFlavor = "X-App-Flavor"

// mobileMe serves GET /v1/mobile/me. A shim call (a cf_shim key, the setDriverClaims callable) is answered by
// auth.HandleMobileMe exactly as before (T32): its body never changes, so the shim's legacy response does not either.
// A Go driver token (APK 4.x, T55) gets the same body plus mobileSettings, the version gate as GET /v1/mobile/settings
// shows it for X-App-Flavor (dev or prod; any other value, gotest included, reads the deployment's gate flavor).
func mobileMe(a *auth.Service, m *mobileapp.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := authz.PrincipalFrom(c)
		if p == nil || p.APIKeyScope == authz.KeyCFShim || m == nil {
			return a.HandleMobileMe(c)
		}
		c.Set(fiber.HeaderCacheControl, "no-store")
		if len(c.Queries()) > 0 {
			return httpx.ErrBadRequest("this route takes no query parameter")
		}
		me, err := a.MobileMe(c.Context(), p)
		if err != nil {
			return err
		}
		st, err := m.Settings(c.Context())
		if err != nil {
			return err
		}
		flavor := c.Get(HeaderAppFlavor)
		if flavor != mobileapp.FlavorDev && flavor != mobileapp.FlavorProd {
			flavor = ""
		}
		return httpx.JSON(c, http.StatusOK, driverMobileMe{MobileMe: me, MobileSettings: st.Public(flavor)})
	}
}

// driverMobileMe is the GET /v1/mobile/me body of a Go driver token: MobileMe plus the public version gate.
type driverMobileMe struct {
	*auth.MobileMe
	MobileSettings mobileapp.PublicSettings `json:"mobileSettings"`
}

// idempotencyHandler is the Idempotency-Key middleware of the ✱ driver writes (T55). Without one (`api routes` lists
// the table from the zero APIDeps; a test world that does not build it) a ✱ route answers 503 rather than run
// unprotected.
func idempotencyHandler(d APIDeps) fiber.Handler {
	if d.Idempotency != nil {
		return d.Idempotency.Handler()
	}
	return func(fiber.Ctx) error { return httpx.ErrUnavailable("idempotency is not configured") }
}

// MobilePresignPurposes are the upload purposes the driver app may presign in the core loop (T55): the check-in photo
// and the customer-app screenshot (owner: the task) and the trip photos (owner: the trip). Widened additively with
// later slices (standby, incident, expense, leave).
var MobilePresignPurposes = []string{"checkin_photo", "checkin_app_screenshot", "trip_photo"}

// mobilePresign is POST /v1/mobile/uploads/presign: POST /v1/uploads/presign for a driver acting as itself, with an
// absolute upload URL (the local backend's LOCAL_MEDIA_PUBLIC_BASE_URL/media/{key}, or the S3 presign origin) that the
// phone calls directly, and only the purposes of MobilePresignPurposes (anything else 422 purpose invalid). A re-sign
// of a pending upload ({key}) is answered like the web's.
func mobilePresign(st *storage.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := authz.PrincipalFrom(c)
		if p == nil || p.DriverID == nil {
			return authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
		}
		cl, ok := StorageCaller(c)
		if !ok {
			return httpx.ErrUnauthenticated()
		}
		var in storage.PresignInput
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		if in.Key == "" && !slices.Contains(MobilePresignPurposes, in.Purpose) {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "purpose", Reason: "invalid",
				Params: map[string]any{"allowed": MobilePresignPurposes}})
		}
		out, err := st.Presign(c.Context(), cl, in, storage.PutOptions{APIPath: false})
		if err != nil {
			return err
		}
		return httpx.JSON(c, http.StatusOK, out)
	}
}
