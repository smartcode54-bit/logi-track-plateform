package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The account mirror (Appendix C §C.6.4). While the bridge mode is not off, every account change made in
// Go is written to the user's Firebase Auth account synchronously, inside the request's transaction and
// after its own rows, so a mirror failure rolls the change back and answers 503 bridge_unavailable: the
// two stores never diverge by a committed Go change. Installed APKs (3.x) keep signing in to Firebase
// until P7b, so a password set, a disable or a revocation made here must reach them.
//
//	Go change                                         Firebase Auth write
//	password set (change, reset, temporary)           password, validSince = now
//	disable / enable (SetStatusInTx)                  disableUser true + validSince = now / disableUser false
//	every session revoked with reason disabled,       disableUser true + validSince = now
//	e.g. a soft delete (RevokeInTx)
//	every session revoked by an admin (RevokeInTx)    validSince = now
//	claims changed: role, scope, driver link,         the legacy claim object of the user's default
//	platform role (RevokeInTx, claims_changed)        context, merged into customAttributes
//	user created (MirrorNewUserInTx)                  create (uid users.id, email, password, disabled), claims;
//	                                                  or adopt the orphan of a rolled-back creation
//
// A user without a Firebase uid (users.legacy_auth_uid NULL) has no Firebase account and nothing is
// written; neither is anything for a uid Firebase does not know (USER_NOT_FOUND: the account was never
// created, e.g. a Go-created staff user who never opened a legacy page). Plaintext passwords exist only
// in memory for the call and never reach a log, the outbox or a table.

// mirrorTimeout bounds one mirror write (a lookup and an update for a claims change).
const mirrorTimeout = 15 * time.Second

// accountChange is one write to Firebase Auth; the zero value writes nothing.
type accountChange struct {
	op       string  // metric label: password, status, revoke, claims, create
	password *string // set this password
	disabled *bool   // disable / enable
	revoke   bool    // validSince = now: refresh tokens issued before now stop working
	claims   bool    // recompute the legacy claim object and merge it into customAttributes
}

// mirror writes ch to the Firebase account of user uid; call it inside the transaction that made the
// change, after its writes (the claims are read from them).
func (s *Service) mirror(ctx context.Context, q *authdb.Queries, uid uuid.UUID, ch accountChange) error {
	if !s.fb.Mode.Mirror() {
		return nil
	}
	u, err := q.GetBridgeUser(ctx, uid)
	if err != nil {
		return err
	}
	if u.LegacyAuthUid == nil {
		return nil
	}
	fbuid := *u.LegacyAuthUid
	upd := firebase.Update{Password: ch.password, Disabled: ch.disabled}
	if ch.revoke {
		now := s.clock()
		upd.RevokeBefore = &now
	}
	mctx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()
	if ch.claims {
		a, err := loadAxes(ctx, q, uid)
		if err != nil {
			return err
		}
		g, err := legacyClaims(ctx, q, u, defaultContext(a))
		if err != nil {
			return err
		}
		acc, err := s.fb.Accounts.Lookup(mctx, fbuid)
		if errors.Is(err, firebase.ErrUserNotFound) {
			return nil
		}
		if err != nil {
			return s.mirrorFailed(ch.op, err)
		}
		attrs, err := mergeAttributes(acc.CustomAttributes, g.claims)
		if err != nil {
			return s.mirrorFailed(ch.op, err)
		}
		if attrs != acc.CustomAttributes {
			upd.CustomAttributes = &attrs
		}
	}
	err = s.fb.Accounts.Update(mctx, fbuid, upd)
	if errors.Is(err, firebase.ErrUserNotFound) {
		s.log.Info().Str("user_id", uid.String()).Str("op", ch.op).Msg("auth: no Firebase account to mirror to")
		return nil
	}
	if err != nil {
		return s.mirrorFailed(ch.op, err)
	}
	return nil
}

// mirrorFailed counts a failed mirror write and turns it into 503 bridge_unavailable; the caller's
// transaction rolls back. The cause is logged; it never holds a password (internal/auth/firebase). A
// 401 / 403 from Identity Toolkit is a configuration fault, not an outage, and says so in the log: the
// service account lacks Firebase Authentication Admin on the project (Appendix C §C.6.4, §16.1).
func (s *Service) mirrorFailed(op string, err error) error {
	s.mirrorFailures.WithLabelValues(op).Inc()
	if ae, ok := errors.AsType[*firebase.APIError](err); ok && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
		s.log.Error().Str("op", op).Int("status", ae.Status).Str("code", ae.Code).
			Msg("auth: Identity Toolkit refused the bridge's service account; GOOGLE_APPLICATION_CREDENTIALS needs Firebase Authentication Admin on FIREBASE_PROJECT_ID")
	}
	return errBridgeUnavailable().Wrap(err)
}

// StatusChange disables or re-enables a user: POST /v1/users/{id}/disable|enable (T19).
type StatusChange struct {
	UserID    uuid.UUID
	Disabled  bool
	ActorID   *uuid.UUID // the admin
	RequestID string
}

// SetStatusInTx performs a disable or an enable inside the caller's WithSystem transaction (lock order:
// the users row first, C.4.4). Disable sets status disabled, revokes every session (reason disabled),
// bumps auth_version and queues user.sessions_revoked; enable makes a disabled user active. Either way
// the Firebase account follows before COMMIT (disableUser, plus validSince on disable), and a mirror
// failure is 503 bridge_unavailable. Repeating a change is harmless and still writes Firebase, which
// heals an account that drifted. An unknown or deleted user is 404. The caller appends user_disabled /
// user_enabled with security.Append in the same transaction and calls Apply after COMMIT.
func (s *Service) SetStatusInTx(ctx context.Context, tx pgx.Tx, c StatusChange) (*PostCommit, error) {
	q := authdb.New(tx)
	pc := newPostCommit()
	now := s.clock()
	u, err := q.LockUser(ctx, c.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound()
	}
	if err != nil {
		return nil, err
	}
	if u.Status == "deleted" {
		return nil, httpx.ErrNotFound()
	}
	if c.Disabled && u.Status != "disabled" {
		if err := q.SetUserDisabled(ctx, authdb.SetUserDisabledParams{At: now, ID: u.ID}); err != nil {
			return nil, err
		}
		if _, err := s.revokeTx(ctx, tx, Revocation{UserID: u.ID, Reason: RevokeDisabled, RevokedBy: c.ActorID,
			BumpVersion: true, RequestID: c.RequestID}, now, pc); err != nil {
			return nil, err
		}
	} else if !c.Disabled {
		if err := q.SetUserEnabled(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	disabled := c.Disabled
	if err := s.mirror(ctx, q, u.ID, accountChange{op: "status", disabled: &disabled, revoke: disabled}); err != nil {
		return nil, err
	}
	return pc, nil
}

// MirrorEmailInTx writes the user's current email to its Firebase account (PATCH /v1/users/{id}, T19)
// inside the caller's WithSystem transaction, after the users row was updated, so an installed APK and
// the legacy pages sign in with the new address. A user without a Firebase uid, or whose uid Firebase
// does not know, has nothing to update. Another Firebase account holding the address is 409
// already_exists (details.reason firebase_account_exists, never its uid); any other failure is 503
// bridge_unavailable. Either rolls the caller's transaction back. A no-op while the mode is off.
func (s *Service) MirrorEmailInTx(ctx context.Context, tx pgx.Tx, uid uuid.UUID) error {
	if !s.fb.Mode.Mirror() {
		return nil
	}
	u, err := authdb.New(tx).GetBridgeUser(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound()
	}
	if err != nil {
		return err
	}
	if u.LegacyAuthUid == nil || u.Email == nil {
		return nil
	}
	mctx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()
	err = s.fb.Accounts.Update(mctx, *u.LegacyAuthUid, firebase.Update{Email: u.Email})
	switch {
	case err == nil, errors.Is(err, firebase.ErrUserNotFound):
		return nil
	case errors.Is(err, firebase.ErrEmailExists):
		s.log.Warn().Str("user_id", uid.String()).Msg("auth: another Firebase account holds the new email of a user")
		return errFirebaseEmailTaken()
	default:
		return s.mirrorFailed("email", err)
	}
}

// TemporaryPassword is an admin-issued temporary password: POST /v1/users/{id}/password/temporary
// (T19, R29). Password and Hash come from NewTemporaryPassword.
type TemporaryPassword struct {
	UserID    uuid.UUID
	Password  string
	Hash      string
	ActorID   *uuid.UUID
	RequestID string
}

// NewTemporaryPassword generates a temporary password (password.Temporary) and its Argon2id hash,
// outside any transaction so no row is held during the memory-hard computation. The password is
// returned once to the admin and never logged or emailed (R29).
func (s *Service) NewTemporaryPassword(ctx context.Context) (plain, hash string, err error) {
	if plain, err = password.Temporary(); err != nil {
		return "", "", err
	}
	if hash, err = s.hasher.Hash(ctx, plain); err != nil {
		return "", "", kdfError(err)
	}
	return plain, hash, nil
}

// SetTemporaryPasswordInTx stores a temporary password inside the caller's WithSystem transaction: the
// hash, must_change_password (the next sign-in answers password_change_required, R79), the legacy hash
// cleared, auth_version bumped and every session revoked (reason password_reset, by the admin). The
// Firebase account gets the same password and validSince = now before COMMIT, so an installed APK signs
// in with it too. An unknown or deleted user is 404. The caller appends
// user_password_temporary_issued in the same transaction and calls Apply after COMMIT.
func (s *Service) SetTemporaryPasswordInTx(ctx context.Context, tx pgx.Tx, in TemporaryPassword) (*PostCommit, error) {
	if in.Password == "" || in.Hash == "" {
		return nil, errors.New("auth: a temporary password and its hash are required")
	}
	q := authdb.New(tx)
	pc := newPostCommit()
	now := s.clock()
	u, err := q.LockUser(ctx, in.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound()
	}
	if err != nil {
		return nil, err
	}
	if u.Status == "deleted" {
		return nil, httpx.ErrNotFound()
	}
	v, err := q.SetTemporaryPassword(ctx, authdb.SetTemporaryPasswordParams{PasswordHash: in.Hash, ChangedAt: now, ID: u.ID})
	if err != nil {
		return nil, err
	}
	pc.version(u.ID, v)
	if _, err := s.revokeTx(ctx, tx, Revocation{UserID: u.ID, Reason: RevokePasswordReset, RevokedBy: in.ActorID,
		RequestID: in.RequestID}, now, pc); err != nil {
		return nil, err
	}
	pw := in.Password
	if err := s.mirror(ctx, q, u.ID, accountChange{op: "password", password: &pw, revoke: true}); err != nil {
		return nil, err
	}
	return pc, nil
}

// MirrorNewUserInTx creates the Firebase account of a user just created in Go (POST /v1/users, T19),
// inside the caller's transaction after the users, memberships and driver-link rows: only for a driver
// or own-fleet staff, the accounts the installed APKs and the legacy pages sign in with. The Firebase uid
// is the existing users.legacy_auth_uid, else users.id (stored back). The account gets the email, the
// initial password when one was set ("" for an invite), the disabled flag and the legacy claims; an
// account that already exists under the uid is brought to that state instead. A no-op while the mode is
// off.
//
// Firebase is not transactional with PostgreSQL, so a creation can leave an account behind whose users
// row rolled back. A failure inside this call deletes the account it just created (best effort); a failure
// after it (a later write of the caller, a deferred constraint at COMMIT) cannot. The retry inserts a new
// users row whose new id meets EMAIL_EXISTS, and the account holding the email is adopted when it is such
// an orphan: its uid is a uuid that no users row (deleted ones included) and no drivers row holds. The
// user's legacy_auth_uid becomes that uid, and the account gets the new password (a random one for an
// invite, so a password typed for the failed attempt does not survive), the disabled flag, validSince =
// now and the claims. Any other holder (a legacy account auth-import has not loaded, the account of a
// soft-deleted user, a self-registered one) is never adopted, since whoever knows its password would sign
// in as the new user: that is 409 already_exists (details.reason firebase_account_exists), which a retry
// cannot change. A Firebase failure is 503 bridge_unavailable.
func (s *Service) MirrorNewUserInTx(ctx context.Context, tx pgx.Tx, uid uuid.UUID, initialPassword string) error {
	if !s.fb.Mode.Mirror() {
		return nil
	}
	q := authdb.New(tx)
	u, err := q.GetBridgeUser(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound()
	}
	if err != nil {
		return err
	}
	a, err := loadAxes(ctx, q, uid)
	if err != nil {
		return err
	}
	// A driver of any tenant, or any role in the own fleet (staff); carrier and customer users created in
	// Go never get a Firebase account (C.6.3).
	if m := a.pickTenant(nil); m == nil || (authz.TenantRole(m.Role) != authz.Driver && m.Kind != "own_fleet") {
		return nil
	}
	fbuid, err := firebaseUID(ctx, q, u)
	if err != nil {
		return err
	}
	disabled := u.Status == "disabled"
	mctx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()
	err = s.fb.Accounts.Create(mctx, firebase.NewAccount{
		UID: fbuid, Email: deref(u.Email), EmailVerified: u.EmailVerified, Password: initialPassword,
		DisplayName: deref(u.DisplayName), Disabled: disabled,
	})
	switch {
	case err == nil:
		if err := s.mirror(ctx, q, uid, accountChange{op: "create", claims: true}); err != nil {
			s.undoCreate(ctx, uid, fbuid)
			return err
		}
		return nil
	case errors.Is(err, firebase.ErrUIDExists):
		ch := accountChange{op: "create", disabled: &disabled, claims: true}
		if initialPassword != "" {
			ch.password = &initialPassword
		}
		return s.mirror(ctx, q, uid, ch)
	case errors.Is(err, firebase.ErrEmailExists):
		return s.adoptOrphan(ctx, q, u, initialPassword)
	default:
		return s.mirrorFailed("create", err)
	}
}

// undoCreate deletes the account MirrorNewUserInTx just created when a later write of the same call
// failed, so the rolled-back users row leaves nothing in Firebase. Best effort, under its own deadline
// (the request's may be what failed): an account that survives is adopted by the retry.
func (s *Service) undoCreate(ctx context.Context, uid uuid.UUID, fbuid string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mirrorTimeout)
	defer cancel()
	if err := s.fb.Accounts.Delete(dctx, fbuid); err != nil && !errors.Is(err, firebase.ErrUserNotFound) {
		s.log.Warn().Err(err).Str("user_id", uid.String()).
			Msg("auth: could not delete the Firebase account of a failed creation; a retry adopts it")
	}
}

// adoptOrphan answers EMAIL_EXISTS at a creation (see MirrorNewUserInTx): it adopts the account holding
// the email when that is the orphan of a rolled-back creation, and refuses any other holder with 409.
func (s *Service) adoptOrphan(ctx context.Context, q *authdb.Queries, u authdb.GetBridgeUserRow, initialPassword string) error {
	mctx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()
	acc, err := s.fb.Accounts.LookupEmail(mctx, deref(u.Email))
	if err != nil {
		// USER_NOT_FOUND included: the holder went away between the two calls, so a retry creates the account.
		return s.mirrorFailed("create", err)
	}
	if id, perr := uuid.Parse(acc.UID); perr == nil && id.String() == acc.UID {
		held, err := q.FirebaseUIDHeld(ctx, authdb.FirebaseUIDHeldParams{ID: id, Uid: acc.UID})
		if err != nil {
			return err
		}
		if !held {
			if err := q.AdoptLegacyAuthUID(ctx, authdb.AdoptLegacyAuthUIDParams{LegacyAuthUid: acc.UID, ID: u.ID}); err != nil {
				return err
			}
			pw := initialPassword
			if pw == "" {
				if pw, err = unusablePassword(); err != nil {
					return err
				}
			}
			disabled := u.Status == "disabled"
			s.log.Info().Str("user_id", u.ID.String()).Msg("auth: adopted the Firebase account of a rolled-back creation")
			return s.mirror(ctx, q, u.ID, accountChange{op: "create", password: &pw, disabled: &disabled, revoke: true, claims: true})
		}
	}
	s.log.Warn().Str("user_id", u.ID.String()).Msg("auth: another Firebase account holds the email of a new user")
	return errFirebaseEmailTaken()
}

// unusablePassword is the password an adopted account gets when the new user is invited without one: 256
// random bits nobody is told, replacing whatever the failed attempt set. The invitation sets the real one.
func unusablePassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
