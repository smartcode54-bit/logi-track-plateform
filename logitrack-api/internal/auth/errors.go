package auth

import (
	"net/http"
	"strconv"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Stable error codes of the auth surface (Appendix B §B.1.5, R48, R78). Clients branch on the code
// only: they refresh on token_expired, go to the login page on session_revoked / invalid_token, and
// show the change-password form on password_change_required.
const (
	CodeTokenExpired           = "token_expired"
	CodeSessionRevoked         = "session_revoked"
	CodeInvalidToken           = "invalid_token"
	CodeInvalidCredentials     = "invalid_credentials"
	CodeAccountDisabled        = "account_disabled"
	CodeNoAccount              = "no_account"
	CodeDriverProfileRequired  = "driver_profile_required"
	CodePasswordChangeRequired = "password_change_required"
	CodePermissionDenied       = "permission_denied"
	CodeLocked                 = "locked"
)

// details.reason of token_expired (R78).
const (
	ReasonExpired       = "expired"
	ReasonClaimsChanged = "claims_changed"
)

func errTokenExpired(reason string) *httpx.Error {
	return httpx.NewError(http.StatusUnauthorized, CodeTokenExpired, "access token expired; refresh it").
		WithDetails(map[string]any{"reason": reason})
}

func errSessionRevoked() *httpx.Error {
	return httpx.NewError(http.StatusUnauthorized, CodeSessionRevoked, "session revoked; sign in again")
}

func errInvalidToken() *httpx.Error {
	return httpx.NewError(http.StatusUnauthorized, CodeInvalidToken, "invalid token")
}

func errInvalidCredentials() *httpx.Error {
	return httpx.NewError(http.StatusUnauthorized, CodeInvalidCredentials, "wrong email or password")
}

func errAccountDisabled() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodeAccountDisabled, "account disabled")
}

// errNoAccount answers a Google sign-in that resolves to no usable user: no self-signup (C.4.10).
func errNoAccount() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodeNoAccount, "no LogiTrack account is linked to this Google account")
}

func errDriverProfileRequired() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodeDriverProfileRequired, "driver role without a linked driver profile")
}

func errPasswordChangeRequired(ticket string, ttl time.Duration) *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodePasswordChangeRequired, "the password must be changed before signing in").
		WithDetails(map[string]any{"passwordChangeTicket": ticket, "expiresIn": int(ttl.Seconds())})
}

func errPermissionDenied(msg string) *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodePermissionDenied, msg)
}

func errLocked(retryAfter time.Duration) *httpx.Error {
	return httpx.NewError(http.StatusLocked, CodeLocked, "too many failed sign-ins; try again later").
		WithDetails(map[string]any{"retryAfterSeconds": retrySeconds(retryAfter)})
}

func errRateLimited(retryAfter time.Duration) *httpx.Error {
	return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many requests").
		WithDetails(map[string]any{"retryAfterSeconds": retrySeconds(retryAfter)})
}

func retrySeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	return max(s, 1)
}

// retryAfterHeader is the Retry-After value of a 423 or 429 (B.1.5).
func retryAfterHeader(e *httpx.Error) (string, bool) {
	if e.Status != http.StatusTooManyRequests && e.Status != http.StatusLocked {
		return "", false
	}
	if v, ok := e.Details["retryAfterSeconds"].(int); ok {
		return strconv.Itoa(v), true
	}
	return "", false
}
