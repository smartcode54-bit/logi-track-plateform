package auth

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
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
// The order protects the one budget every driver shares (T32R): the key is checked (secret, state, scope) without
// spending anything, then the token; only an authenticated user is charged, first to its own bucket
// rl:shim_user:{key id}:{user id} (60/min), then to the key's rl:apikey:{id} at api_keys.rate_per_min, which is only
// the overall ceiling. A caller Go refuses (an unknown, invalid or expired token, no account, a disabled account)
// spends no budget, and one user calling in a loop exhausts its own bucket, not the other drivers'.
//
//   - 401 unauthenticated: no X-Api-Key, or no Authorization bearer;
//   - 401 invalid_token: a repeated, unknown, revoked or expired key (details.reason for a genuine key), a token that
//     fails verification or predates the user's last password change;
//   - 403 permission_denied (details.reason api_key_scope): a genuine key of another scope, or a cf_shim key on the
//     internal listener;
//   - 403 no_account / account_disabled / driver_profile_required, 404 (no verifier configured), 503 (signing keys
//     unreachable): the outcomes of VerifyFirebaseIDToken;
//   - 429 resource_exhausted (details.bucket shim_user or apikey, Retry-After): over the user's or the key's budget.
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
		key, err := s.lookupAPIKey(c, keys, true)
		if err != nil {
			return err
		}
		if err := key.admit(c, pol); err != nil {
			return err
		}
		id, err := s.VerifyFirebaseIDToken(c.Context(), raw, FirebaseShim)
		if err != nil {
			return err
		}
		p := id.Principal
		s.shimRequests.WithLabelValues(callable).Inc()
		if err := s.limitShimUser(c, key.row.ID, p.UserID); err != nil {
			return err
		}
		if err := s.limitAPIKey(c, key.row.ID, key.row.RatePerMin); err != nil {
			return err
		}
		kp := s.useAPIKey(key)
		p.APIKeyID, p.APIKeyScope = kp.APIKeyID, kp.APIKeyScope
		setPrincipal(c, p)
		if s.gate != nil {
			if err := s.gate.Authorize(c, p); err != nil {
				return err
			}
		}
		if err := authz.RefuseTenantHeader(c); err != nil {
			return err
		}
		return c.Next()
	}
}
