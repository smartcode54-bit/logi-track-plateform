package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// AMRGoogle is the amr claim of a Google session.
const AMRGoogle = "google"

// GoogleNonceTTL is the life of a sign-in nonce, auth:google:nonce:{nonce} (Appendix B §B.6.2).
const GoogleNonceTTL = 10 * time.Minute

// maxIDTokenBytes bounds idToken; Google ID tokens are about 1-1.5 KB.
const maxIDTokenBytes = 8 << 10

// GoogleInput is the body of POST /v1/auth/google (R48, R83) plus the request facts the handler adds.
// The web sends {idToken, nonce, platform:'web'} through the BFF; the driver app sends {idToken,
// platform, installId, appVersion?} without a nonce (Appendix C §C.4.10). Its token may still carry a
// nonce claim the SDK made up (GoogleSignIn-iOS always sends one through AppAuth), which the app cannot
// read and which is not ours.
type GoogleInput struct {
	IDToken    string `json:"idToken"`
	Nonce      string `json:"nonce"`
	Platform   string `json:"platform"`
	InstallID  string `json:"installId"`
	AppVersion string `json:"appVersion"`

	IP        string `json:"-"`
	UserAgent string `json:"-"`
	RequestID string `json:"-"`
}

// NonceResult is the 200 body of GET /v1/auth/google/nonce.
type NonceResult struct {
	Nonce     string `json:"nonce"`
	ExpiresIn int    `json:"expiresIn"`
}

// googleNonce is the value of auth:google:nonce:{nonce}.
type googleNonce struct {
	IssuedAt int64 `json:"issuedAt"`
}

// nonceKind is the ticket kind of the nonce: Store.key("auth", "google:nonce", n) is
// auth:google:nonce:{n}.
const nonceKind = "google:nonce"

func validateGoogle(in *GoogleInput) []httpx.FieldViolation {
	var v []httpx.FieldViolation
	switch {
	case in.IDToken == "":
		v = append(v, httpx.FieldViolation{Field: "idToken", Reason: "required"})
	case len(in.IDToken) > maxIDTokenBytes:
		v = append(v, httpx.FieldViolation{Field: "idToken", Reason: "too_long"})
	}
	switch in.Platform {
	case PlatformWeb, PlatformAndroid, PlatformIOS:
	default:
		v = append(v, httpx.FieldViolation{Field: "platform", Reason: "one_of",
			Params: map[string]any{"values": []string{PlatformWeb, PlatformAndroid, PlatformIOS}}})
	}
	switch {
	case in.Nonce == "" && in.Platform == PlatformWeb:
		v = append(v, httpx.FieldViolation{Field: "nonce", Reason: "required"})
	case in.Nonce != "":
		if _, _, ok := secretHash(in.Nonce); !ok {
			v = append(v, httpx.FieldViolation{Field: "nonce", Reason: "invalid"}) // never one of ours
		}
	}
	if len(in.InstallID) > 200 || !utf8.ValidString(in.InstallID) {
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "too_long"})
	}
	if len(in.AppVersion) > 64 || !utf8.ValidString(in.AppVersion) {
		v = append(v, httpx.FieldViolation{Field: "appVersion", Reason: "too_long"})
	}
	return v
}

// GoogleNonce is GET /v1/auth/google/nonce: a single-use nonce for the GIS button (web, through the BFF),
// stored for GoogleNonceTTL. 404 while Google sign-in is off.
func (s *Service) GoogleNonce(ctx context.Context, ip string) (*NonceResult, error) {
	if s.google == nil {
		return nil, httpx.ErrNotFound()
	}
	if err := s.limit(ctx, "google_nonce_ip", ipSubject(ip), limitGoogleNonceIP); err != nil {
		return nil, err
	}
	n, err := newSecret()
	if err != nil {
		return nil, err
	}
	if err := s.store.putTicket(ctx, nonceKind, n, googleNonce{IssuedAt: s.clock().Unix()}, GoogleNonceTTL); err != nil {
		return nil, httpx.ErrUnavailable("nonce store unavailable").Wrap(err)
	}
	return &NonceResult{Nonce: n, ExpiresIn: int(GoogleNonceTTL.Seconds())}, nil
}

// GoogleSignIn is POST /v1/auth/google (main spec §4.2, Appendix C §C.4.10). It answers like a password
// login (tokens, tenants, defaultTenantId; amr "google"), or:
//
//   - 401 invalid_token: the ID token failed verification (signature, issuer, expiry, an aud outside
//     GOOGLE_OIDC_ALLOWED_CLIENT_IDS, email_verified not true) or its nonce did not match an unused one;
//   - 403 no_account: no active user is linked to the Google sub and none can be linked by the verified
//     email (no self-signup; an email Google is not authoritative for links nothing);
//   - 403 account_disabled before any link, ticket or token; 403 password_change_required with a ticket
//     (R79) for a must_change_password user;
//   - 503 unavailable when Google's keys cannot be fetched; 404 while Google sign-in is off.
//
// The nonce (consumeNonce): platform web must send one. When the body carries a nonce, or a token that
// is not the driver app's (azp == aud, or no azp: GIS) has a nonce claim, the token's nonce must equal
// the body's and be an unused one of ours, consumed (GETDEL) only then; so a GIS token signs in once,
// whichever platform it is presented with. A driver-app token (android or ios, azp != aud) sent without
// a body nonce may carry the SDK's own nonce, which is ignored.
func (s *Service) GoogleSignIn(ctx context.Context, in GoogleInput) (*LoginResult, error) {
	if s.google == nil {
		return nil, httpx.ErrNotFound()
	}
	if v := validateGoogle(&in); len(v) > 0 {
		return nil, httpx.ErrInvalidArgument(v...)
	}
	if err := s.limit(ctx, "google_ip", ipSubject(in.IP), limitGoogleIP); err != nil {
		return nil, err
	}
	id, err := s.google.Verify(ctx, in.IDToken)
	if err != nil {
		if errors.Is(err, google.ErrUnavailable) {
			// The error handler logs the cause (discovery or key fetch); the token was not judged.
			return nil, httpx.ErrUnavailable("Google sign-in is temporarily unavailable; retry shortly").Wrap(err)
		}
		if ie, ok := errors.AsType[*google.InvalidError](err); ok {
			s.log.Info().Str("reason", ie.Reason).Str("platform", in.Platform).Str("request_id", in.RequestID).
				Msg("auth: google ID token rejected")
			return nil, errInvalidToken()
		}
		return nil, err
	}
	if err := s.consumeNonce(ctx, id, in); err != nil {
		return nil, err
	}
	r, err := s.resolveGoogle(ctx, id, in)
	if uniqueViolation(err, "auth_identities_provider_provider_subject_key", "auth_identities_user_id_provider_key") {
		r, err = s.resolveGoogle(ctx, id, in) // a concurrent sign-in linked first; the retry finds the link
	}
	if err != nil {
		return nil, err
	}
	if r.MustChangePassword {
		return nil, s.passwordChangeTicket(ctx, r.ID, r.AuthVersion)
	}
	li := LoginInput{
		Platform: in.Platform, InstallID: in.InstallID, AppVersion: in.AppVersion,
		IP: in.IP, UserAgent: in.UserAgent, RequestID: in.RequestID,
	}
	g := grant{amr: AMRGoogle, googleSub: id.Subject}
	res, err := s.openSession(ctx, r.ID, g, li)
	if uniqueViolation(err, "sessions_user_install_live") {
		res, err = s.openSession(ctx, r.ID, g, li) // a concurrent sign-in of the same install won the insert
	}
	return res, err
}

// consumeNonce enforces the nonce rule of GoogleSignIn: required on the web (validateGoogle), single
// use, and bound to the token whenever the body carries a nonce or a GIS token carries one.
//
// The one exception is a driver-app token without a body nonce: on iOS, google_sign_in runs
// GoogleSignIn-iOS, whose AppAuth request always sends a random nonce when the app gives none, and Google
// copies it into the ID token; the app cannot read it and it was never issued here. google_sign_in 7
// accepts a nonce only in initialize(), once per process, so a single-use server nonce cannot serve the
// next sign-in either. Such a token is told from a GIS token by azp (Identity.NativeApp): the driver
// app's token has aud = the server client and azp = its Android or iOS client, a GIS token has
// azp == aud. A GIS token replayed through the driver-app path without its nonce therefore still fails.
func (s *Service) consumeNonce(ctx context.Context, id *google.Identity, in GoogleInput) error {
	if in.Nonce == "" {
		switch {
		case id.Nonce == "":
			return nil // driver app (validation already required a nonce on the web)
		case in.Platform != PlatformWeb && id.NativeApp():
			return nil // the SDK's own nonce (GoogleSignIn-iOS / AppAuth), not one of ours
		}
	}
	if subtle.ConstantTimeCompare([]byte(in.Nonce), []byte(id.Nonce)) != 1 {
		s.log.Info().Str("reason", "nonce_mismatch").Str("platform", in.Platform).Str("request_id", in.RequestID).
			Msg("auth: google ID token rejected")
		return errInvalidToken()
	}
	var v googleNonce
	found, err := s.store.takeTicket(ctx, nonceKind, in.Nonce, &v)
	if err != nil {
		return httpx.ErrUnavailable("nonce store unavailable").Wrap(err)
	}
	if !found {
		s.log.Info().Str("reason", "nonce_unknown_or_used").Str("platform", in.Platform).Str("request_id", in.RequestID).
			Msg("auth: google ID token rejected")
		return errInvalidToken()
	}
	return nil
}

// resolveGoogle finds the user of a verified Google identity, linking it when needed, in one transaction
// under the user's row lock (C.4.10):
//
//  1. auth_identities(google, sub) -> that user;
//  2. else, when Google is authoritative for the token's email (a Gmail address or a Workspace account,
//     google.Identity.EmailAuthoritative), the user whose email equals it and who has no Google identity
//     yet: insert auth_identities and append google_identity_linked in the same transaction (C.4.13);
//  3. else 403 no_account.
//
// For any other address email_verified only says the address was verified when the Google account was
// created: whoever once held the mailbox (a former employee of a carrier on Microsoft 365, a recycled
// shared mailbox) could own that Google account, and a link would outlive every later password reset.
//
// A disabled user is 403 account_disabled and is never linked. Deleted and reset_required users (no
// usable credential, C.5.8: they recover through forgot-password or a temporary password) and a user
// already linked to another Google account are no_account.
func (s *Service) resolveGoogle(ctx context.Context, id *google.Identity, in GoogleInput) (*authdb.LockUserRow, error) {
	var out *authdb.LockUserRow
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		uid, err := q.GetGoogleIdentityUser(ctx, id.Subject)
		switch {
		case err == nil:
			u, err := q.LockUser(ctx, uid)
			if err != nil {
				return err
			}
			if err := signable(u); err != nil {
				return err
			}
			out = &u
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		if !id.EmailAuthoritative() {
			// Checked before the email lookup, so the answer says nothing about whether the address has a user.
			s.log.Info().Str("reason", "email_not_authoritative").Str("platform", in.Platform).
				Str("request_id", in.RequestID).Msg("auth: google sign-in not linked by email")
			return errNoAccount()
		}
		cand, err := q.GetUserForLogin(ctx, id.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoAccount()
		}
		if err != nil {
			return err
		}
		u, err := q.LockUser(ctx, cand.ID)
		if err != nil {
			return err
		}
		if u.Email == nil || !strings.EqualFold(*u.Email, id.Email) {
			return errNoAccount() // the email changed after the lookup
		}
		if err := signable(u); err != nil {
			return err
		}
		linked, err := q.GetUserGoogleSubject(ctx, u.ID)
		switch {
		case err == nil && linked == id.Subject:
			out = &u // linked by a concurrent sign-in after the first lookup
			return nil
		case err == nil:
			return errNoAccount() // the email belongs to a user linked to another Google account
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		now := s.clock()
		if err := q.InsertGoogleIdentity(ctx, authdb.InsertGoogleIdentityParams{
			UserID: u.ID, Subject: id.Subject, EmailAtLink: id.Email, At: now,
		}); err != nil {
			return err
		}
		email := id.Email
		if err := security.Append(ctx, tx, security.Event{
			EventType: "google_identity_linked", Severity: security.SeverityInfo,
			Summary: "Google account linked to the user by its verified email",
			Details: map[string]any{"provider": "google", "linkedBy": "verified_email", "platform": in.Platform,
				"clientId": id.ClientID},
			ActorUserID: &u.ID, ActorEmail: &email, TargetUserID: &u.ID, RequestID: in.RequestID, OccurredAt: now,
		}); err != nil {
			return err
		}
		out = &u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// signable rejects a resolved user that may not start a Google session: disabled -> account_disabled;
// anything but active -> no_account.
func signable(u authdb.LockUserRow) error {
	switch u.Status {
	case "active":
		return nil
	case "disabled":
		return errAccountDisabled()
	default:
		return errNoAccount()
	}
}
