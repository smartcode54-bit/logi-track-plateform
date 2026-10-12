package auth

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// RequireShim authenticates the request of a Cloud Functions callable shim on its /v1/mobile shim target (Appendix C
// §C.6.5, §C.4.11; R45): exactly one X-Api-Key of scope cf_shim (a platform key) together with the end user's Firebase
// ID token as Authorization: Bearer, verified against FIREBASE_PROJECT_ID in every bridge mode (VerifyFirebaseIDToken,
// FirebaseShim). The principal is the end user's, built from PostgreSQL (driver:self for a driver; C.6.2), with the key
// as attribution (APIKeyID, APIKeyScope); the Authorizer (iam.RBAC) then completes it, so the route's capability
// checks apply to the user, never to the key. callable names the shim in shim_requests_total{callable}, counted for
// every authenticated request (R26 retirement evidence). There is no shared token and no acting-user header.
//
//   - 401 unauthenticated: no X-Api-Key, or no Authorization bearer;
//   - 401 invalid_token: a repeated, unknown, revoked or expired key (details.reason for a genuine key), a token that
//     fails verification or predates the user's last password change;
//   - 403 permission_denied (details.reason api_key_scope): a genuine key of another scope, or a cf_shim key on the
//     internal listener;
//   - 403 no_account / account_disabled / driver_profile_required, 404 (no verifier configured), 503 (signing keys
//     unreachable): the outcomes of VerifyFirebaseIDToken.
//
// The shim targets are GET /v1/mobile/me (setDriverClaims) and POST /v1/mobile/tasks/{id}/stops (addDeliveryStop) of
// T32 and POST /v1/mobile/trips/{id}/billing/compute (computeTripBillingSnapshot) of T38; markBroadcastRead (T48) and
// sendCustomerLineNotification (T49) join with their targets. While building the handler RequireShim reads s only to
// create the callable's series at zero, so a shim that is never called still reports 0 (R26); cmd/api `routes`
// passes a nil *Service.
func (s *Service) RequireShim(callable string) fiber.Handler {
	pol := authPolicy{shim: true}
	if s != nil && s.shimRequests != nil {
		s.shimRequests.WithLabelValues(callable)
	}
	return func(c fiber.Ctx) error {
		keys := c.Request().Header.PeekAll(HeaderAPIKey)
		raw, present := bearer(c)
		if len(keys) == 0 || !present || strings.TrimSpace(raw) == "" {
			return httpx.ErrUnauthenticated()
		}
		key, err := s.authenticateAPIKey(c, keys, true, pol)
		if err != nil {
			return err
		}
		id, err := s.VerifyFirebaseIDToken(c.Context(), raw, FirebaseShim)
		if err != nil {
			return err
		}
		p := id.Principal
		p.APIKeyID, p.APIKeyScope = key.APIKeyID, key.APIKeyScope
		s.shimRequests.WithLabelValues(callable).Inc()
		setPrincipal(c, p)
		if s.gate != nil {
			if err := s.gate.Authorize(c, p); err != nil {
				return err
			}
		}
		return c.Next()
	}
}
