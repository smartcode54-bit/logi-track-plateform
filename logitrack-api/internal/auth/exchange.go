package auth

import (
	"context"
	"unicode/utf8"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// ExchangeInput is the body of POST /v1/auth/exchange (R42, Appendix C §C.4.6) plus the request facts the
// handler adds. idToken is the Firebase ID token of the user still signed in to Firebase from APK 3.x.
type ExchangeInput struct {
	IDToken    string `json:"idToken"`
	Platform   string `json:"platform"`
	InstallID  string `json:"installId"`
	AppVersion string `json:"appVersion"`

	IP        string `json:"-"`
	UserAgent string `json:"-"`
	RequestID string `json:"-"`
}

// validateExchange checks the body (spec mobile-auth-exchange R3): the driver app only, so web is refused, and an
// install id is required because the session belongs to that install (R83).
func validateExchange(in *ExchangeInput) []httpx.FieldViolation {
	var v []httpx.FieldViolation
	switch {
	case in.IDToken == "":
		v = append(v, httpx.FieldViolation{Field: "idToken", Reason: "required"})
	case len(in.IDToken) > maxIDTokenBytes:
		v = append(v, httpx.FieldViolation{Field: "idToken", Reason: "too_long"})
	}
	switch in.Platform {
	case PlatformAndroid, PlatformIOS:
	default:
		v = append(v, httpx.FieldViolation{Field: "platform", Reason: "one_of",
			Params: map[string]any{"values": []string{PlatformAndroid, PlatformIOS}}})
	}
	switch {
	case in.InstallID == "":
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "required"})
	case len(in.InstallID) > 200 || !utf8.ValidString(in.InstallID):
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "too_long"})
	}
	if len(in.AppVersion) > 64 || !utf8.ValidString(in.AppVersion) {
		v = append(v, httpx.FieldViolation{Field: "appVersion", Reason: "too_long"})
	}
	return v
}

// exchangeBeforeSession runs between the token check and the session transaction; nil outside tests. The
// integration tests set it (SetExchangeBeforeSession) to change the user in that window and prove the transaction
// re-checks the uid and the password change (R9).
var exchangeBeforeSession func()

// amrForProvider maps firebase.sign_in_provider to the amr of the Go session (Appendix C §C.4 amr row); "" for a
// provider APK 3.x never used, which the exchange refuses rather than guess (R6).
func amrForProvider(p string) string {
	switch p {
	case "password":
		return AMRPassword
	case "google.com":
		return AMRGoogle
	}
	return ""
}

// Exchange is POST /v1/auth/exchange (P7a, R42, Appendix C §C.4.6, spec mobile-auth-exchange R1-R10): the first
// launch of APK 4.x trades the Firebase ID token of the user still signed in from 3.x for a Go session, so the
// driver re-enters no password. It answers like a password login, or:
//
//   - 404 not_found while AUTH_FIREBASE_BRIDGE_MODE excludes mobile or FIREBASE_PROJECT_ID is unset, checked
//     before the body so a closed route says nothing about its shape;
//   - 422 invalid_argument for a bad body (web is refused: the browser never exchanges);
//   - 429 rate_limited (exchange_ip);
//   - 401 invalid_token: the token failed verification, predates the last password change, or came from a sign-in
//     provider other than password or google.com;
//   - 403 no_account / account_disabled; 403 password_change_required with a ticket (R79);
//   - 503 unavailable when the Firebase signing keys cannot be fetched.
//
// Go keeps no Firebase credential: neither the token nor the uid is stored with the session or logged.
func (s *Service) Exchange(ctx context.Context, in ExchangeInput) (*LoginResult, error) {
	if !s.fb.Mode.Mobile() || s.fb.Verifier == nil {
		return nil, httpx.ErrNotFound()
	}
	if v := validateExchange(&in); len(v) > 0 {
		return nil, httpx.ErrInvalidArgument(v...)
	}
	if err := s.limit(ctx, ratelimit.ExchangeIP, ipSubject(in.IP)); err != nil {
		return nil, err
	}
	id, err := s.VerifyFirebaseIDToken(ctx, in.IDToken, FirebaseAPK)
	if err != nil {
		return nil, err
	}
	amr := amrForProvider(id.SignInProvider)
	if amr == "" {
		s.log.Info().Str("reason", "provider_unsupported").Str("platform", in.Platform).Str("request_id", in.RequestID).
			Msg("auth: Firebase session exchange refused")
		return nil, errInvalidToken()
	}
	uid := id.Principal.UserID
	if id.MustChangePassword {
		return nil, s.passwordChangeTicket(ctx, uid, id.Principal.AuthVersion)
	}
	li := LoginInput{
		Platform: in.Platform, InstallID: in.InstallID, AppVersion: in.AppVersion,
		IP: in.IP, UserAgent: in.UserAgent, RequestID: in.RequestID,
	}
	g := grant{amr: amr, firebaseUID: id.UID, firebaseAuthTime: id.AuthTime.Unix()}
	if exchangeBeforeSession != nil {
		exchangeBeforeSession()
	}
	res, err := s.openSession(ctx, uid, g, li)
	if uniqueViolation(err, "sessions_user_install_live") {
		res, err = s.openSession(ctx, uid, g, li) // a concurrent sign-in of the same install won the insert
	}
	return res, err
}
