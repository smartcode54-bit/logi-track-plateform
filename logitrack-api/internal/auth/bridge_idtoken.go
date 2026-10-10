package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// AMRFirebase is the amr of a principal proven by a Firebase ID token (Appendix C §C.6.2). Such a
// principal has no Go session and never receives a Go access token.
const AMRFirebase = "firebase"

// FirebaseUse is where a Firebase ID token is presented (Appendix C §C.6.2).
type FirebaseUse int

const (
	// FirebaseAPK is the APK's own token: idToken of POST /v1/auth/exchange (T55, P7a). Accepted only
	// while the bridge mode includes mobile; otherwise 404.
	FirebaseAPK FirebaseUse = iota + 1
	// FirebaseShim is a token a Cloud Functions shim forwards with a cf_shim API key (T32, from P2):
	// accepted in every bridge mode (R45).
	FirebaseShim
)

// FirebaseIdentity is a verified Firebase ID token resolved to its LogiTrack user.
type FirebaseIdentity struct {
	// Principal is built from PostgreSQL (memberships, scopes, driver link), never from the token's custom
	// claims; AMR "firebase", no session id.
	Principal *authz.Principal
	UID       string // the Firebase uid (users.legacy_auth_uid)
	// SignInProvider is firebase.sign_in_provider ("password", "google.com", ...); the exchange maps it to
	// the amr of the Go session it creates.
	SignInProvider     string
	AuthTime           time.Time
	MustChangePassword bool
}

// firebaseUIDTTL is the life of auth:fbuid:{uid} (Appendix C §C.6.2).
const firebaseUIDTTL = 5 * time.Minute

func errNoFirebaseAccount() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, CodeNoAccount, "no LogiTrack account is linked to this Firebase user")
}

// VerifyFirebaseIDToken verifies a Firebase ID token of FIREBASE_PROJECT_ID (signature, iss, aud, exp,
// auth_time; firebase.Verifier) and resolves it to its user by users.legacy_auth_uid = sub. Outcomes:
//
//   - 404 not_found: use FirebaseAPK while the mode excludes mobile, or no verifier (FIREBASE_PROJECT_ID
//     unset);
//   - 401 invalid_token: the token failed verification, or it predates users.password_changed_at (a
//     Firebase session started before a password change or reset, though Firebase may still accept it;
//     compared in whole seconds, as Firebase's own auth_time and validSince are);
//   - 403 no_account: no user has the uid; 403 account_disabled: the user is not active (disabled,
//     reset_required); 403 driver_profile_required: a driver membership without its drivers row;
//   - 503 unavailable: the signing keys could not be fetched.
//
// A must_change_password user resolves (MustChangePassword is set); the exchange answers it with a
// password-change ticket (R79).
func (s *Service) VerifyFirebaseIDToken(ctx context.Context, raw string, use FirebaseUse) (*FirebaseIdentity, error) {
	switch use {
	case FirebaseAPK:
		if !s.fb.Mode.Mobile() {
			return nil, httpx.ErrNotFound()
		}
	case FirebaseShim:
	default:
		return nil, errors.New("auth: unknown Firebase token use")
	}
	if s.fb.Verifier == nil {
		return nil, httpx.ErrNotFound()
	}
	if raw == "" || len(raw) > maxIDTokenBytes {
		return nil, errInvalidToken()
	}
	tok, err := s.fb.Verifier.Verify(ctx, raw)
	if errors.Is(err, firebase.ErrUnavailable) {
		return nil, httpx.ErrUnavailable("Firebase token keys are unavailable; retry shortly").Wrap(err)
	}
	if err != nil {
		if ie, ok := errors.AsType[*firebase.InvalidError](err); ok {
			s.log.Debug().Str("reason", ie.Reason).Msg("auth: Firebase ID token rejected")
		}
		return nil, errInvalidToken()
	}
	var out *FirebaseIdentity
	err = s.system(ctx, func(q *authdb.Queries) error {
		u, err := s.firebaseUser(ctx, q, tok.UID)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return errAccountDisabled()
		}
		if u.PasswordChangedAt != nil && tok.AuthTime.Unix() < u.PasswordChangedAt.Unix() {
			return errInvalidToken()
		}
		a, err := loadAxes(ctx, q, u.ID)
		if err != nil {
			return err
		}
		p, err := firebasePrincipal(a, u)
		if err != nil {
			return err
		}
		out = &FirebaseIdentity{Principal: p, UID: tok.UID, SignInProvider: tok.SignInProvider, AuthTime: tok.AuthTime,
			MustChangePassword: u.MustChangePassword}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// firebaseUser resolves a Firebase uid. The id cached in auth:fbuid:{uid} only saves the uid lookup:
// the row (status, password_changed_at, claims) is read here on every call, and a cached id whose row no
// longer carries that uid is dropped and looked up again, so the cache is never trusted for anything
// but a hint and needs no invalidation hook.
func (s *Service) firebaseUser(ctx context.Context, q *authdb.Queries, fbuid string) (authdb.GetBridgeUserRow, error) {
	if id, ok := s.store.getFirebaseUID(ctx, fbuid); ok {
		u, err := q.GetBridgeUser(ctx, id)
		switch {
		case err == nil && u.LegacyAuthUid != nil && *u.LegacyAuthUid == fbuid && u.Status != "deleted":
			return u, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return u, err
		}
		s.store.dropFirebaseUID(ctx, fbuid)
	}
	id, err := q.GetUserIDByLegacyUID(ctx, fbuid)
	if errors.Is(err, pgx.ErrNoRows) {
		return authdb.GetBridgeUserRow{}, errNoFirebaseAccount()
	}
	if err != nil {
		return authdb.GetBridgeUserRow{}, err
	}
	u, err := q.GetBridgeUser(ctx, id)
	if err != nil {
		return u, err
	}
	if err := s.store.putFirebaseUID(ctx, fbuid, id, firebaseUIDTTL); err != nil {
		s.log.Warn().Err(err).Msg("auth: cache Firebase uid")
	}
	return u, nil
}

// firebasePrincipal builds the principal of a Firebase-authenticated user: the driver membership when
// the user has a linked driver (Firebase tokens come from the driver app and its shims: driver:self),
// otherwise the default tenant (own fleet first), plus the platform roles and scopes.
func firebasePrincipal(a axes, u authdb.GetBridgeUserRow) (*authz.Principal, error) {
	var m *authdb.ListMembershipsRow
	if a.driver != nil {
		for _, row := range a.usable() {
			if row.TenantID == a.driver.TenantID && authz.TenantRole(row.Role) == authz.Driver {
				m = &row
				break
			}
		}
	}
	if m == nil {
		m = a.pickTenant(nil)
	}
	c, err := a.claims(uuid.Nil, u.AuthVersion, AMRFirebase, m)
	if err != nil {
		return nil, err
	}
	p := &authz.Principal{UserID: u.ID, AuthVersion: u.AuthVersion, AMR: AMRFirebase, Dispatcher: c.Dispatcher}
	if m != nil {
		tid := m.TenantID
		p.TenantID, p.TenantRole = &tid, authz.TenantRole(m.Role)
	}
	if c.DriverID != "" {
		did := a.driver.ID
		p.DriverID = &did
	}
	for _, r := range c.Platform {
		p.Platform = append(p.Platform, authz.PlatformRole(r))
	}
	for _, v := range c.CustomerScopes {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, err
		}
		p.PartyIDs = append(p.PartyIDs, id)
	}
	return p, nil
}
