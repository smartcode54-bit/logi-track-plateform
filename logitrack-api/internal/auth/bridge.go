package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The Firebase bridge (T08, main spec §4.9, Appendix C §C.6) lets the strangler run with Firebase still
// in place: web pages that have not moved yet read Firestore under firestore.rules with a Firebase
// session minted from the Go session (POST /v1/bridge/firebase-token, mode web), installed APKs keep
// signing in to Firebase while Go mirrors every account change made here into Firebase Auth (modes web
// and mobile), and Firebase ID tokens prove a user to Go where the APK or a callable shim presents one
// (VerifyFirebaseIDToken). The Firebase protocol itself lives in internal/auth/firebase.

// BridgeMode is AUTH_FIREBASE_BRIDGE_MODE (R8): what the web and the APK may do with Firebase
// credentials. The callable shims (cf_shim keys, T32, R45) are outside it.
type BridgeMode string

// Bridge modes (Appendix C §C.6.1).
const (
	BridgeOff    BridgeMode = "off"    // after P8: no custom tokens, no APK tokens, no mirror
	BridgeMobile BridgeMode = "mobile" // P7a-P8: APK Firebase tokens at POST /v1/auth/exchange, mirror
	BridgeWeb    BridgeMode = "web"    // P0-P6 until TW7: custom tokens for the web, mirror
	BridgeBoth   BridgeMode = "both"   // only while P7a overlaps an unfinished TW7
)

// BridgeModes lists the valid values.
var BridgeModes = []BridgeMode{BridgeOff, BridgeMobile, BridgeWeb, BridgeBoth}

// ParseBridgeMode reads AUTH_FIREBASE_BRIDGE_MODE; "" is off.
func ParseBridgeMode(s string) (BridgeMode, bool) {
	if s == "" {
		return BridgeOff, true
	}
	m := BridgeMode(s)
	return m, slices.Contains(BridgeModes, m)
}

// Web reports whether custom tokens are minted for the web.
func (m BridgeMode) Web() bool { return m == BridgeWeb || m == BridgeBoth }

// Mobile reports whether the APK's own Firebase ID token is accepted (POST /v1/auth/exchange).
func (m BridgeMode) Mobile() bool { return m == BridgeMobile || m == BridgeBoth }

// Mirror reports whether account changes made in Go are written to Firebase Auth (every mode but off).
func (m BridgeMode) Mirror() bool { return m == BridgeMobile || m == BridgeWeb || m == BridgeBoth }

// FirebaseVerifier checks a Firebase ID token (firebase.Verifier): a rejected token is a
// *firebase.InvalidError, keys that cannot be fetched wrap firebase.ErrUnavailable.
type FirebaseVerifier interface {
	Verify(ctx context.Context, raw string) (*firebase.IDToken, error)
}

// FirebaseSigner mints Firebase custom tokens (firebase.ServiceAccount).
type FirebaseSigner interface {
	CustomToken(uid string, claims map[string]any, now time.Time) (string, error)
}

// FirebaseAccounts writes Firebase Auth accounts (firebase.Accounts).
type FirebaseAccounts interface {
	Lookup(ctx context.Context, uid string) (*firebase.Account, error)
	LookupEmail(ctx context.Context, email string) (*firebase.Account, error)
	Update(ctx context.Context, uid string, u firebase.Update) error
	Create(ctx context.Context, n firebase.NewAccount) error
	Delete(ctx context.Context, uid string) error
}

// Firebase are the bridge's collaborators. The zero value is mode off without a verifier.
type Firebase struct {
	Mode BridgeMode
	// Verifier checks Firebase ID tokens of FIREBASE_PROJECT_ID; nil when that is unset. Required when the
	// mode includes mobile; the shims use it in every mode.
	Verifier FirebaseVerifier
	// Signer mints custom tokens with GOOGLE_APPLICATION_CREDENTIALS; required when the mode includes web.
	Signer FirebaseSigner
	// Accounts mirrors account changes; required in every mode but off.
	Accounts FirebaseAccounts
}

func (f *Firebase) validate() error {
	if f.Mode == "" {
		f.Mode = BridgeOff
	}
	if !slices.Contains(BridgeModes, f.Mode) {
		return fmt.Errorf("auth: unknown Firebase bridge mode %q", f.Mode)
	}
	switch {
	case f.Mode.Web() && f.Signer == nil:
		return errors.New("auth: the web bridge needs a custom-token signer (GOOGLE_APPLICATION_CREDENTIALS)")
	case f.Mode.Mobile() && f.Verifier == nil:
		return errors.New("auth: the mobile bridge needs a Firebase ID-token verifier (FIREBASE_PROJECT_ID)")
	case f.Mode.Mirror() && f.Accounts == nil:
		return errors.New("auth: the bridge needs the Firebase account client (GOOGLE_APPLICATION_CREDENTIALS)")
	}
	return nil
}

// CodeBridgeUnavailable is the 503 of a Firebase account mirror that failed: the change was not
// committed (Appendix B §B.1.5).
const CodeBridgeUnavailable = "bridge_unavailable"

// CodeAlreadyExists is the 409 of a resource that exists (Appendix B §B.1.5); the bridge answers it when
// another Firebase account holds the email of a user being created (details.reason
// firebase_account_exists).
const CodeAlreadyExists = "already_exists"

// The message does not claim that Firebase is untouched: a failure can follow a write that succeeded (an
// account created before its claims failed). PostgreSQL is what was not committed.
func errBridgeUnavailable() *httpx.Error {
	return httpx.NewError(http.StatusServiceUnavailable, CodeBridgeUnavailable,
		"the Firebase account could not be updated; the change was not committed")
}

// errFirebaseEmailTaken: another Firebase account holds the email of a user being created (and is not an
// orphan the creation may adopt, C.6.4) or the new email of a user (MirrorEmailInTx, T19). A retry cannot succeed, so it is not bridge_unavailable; the
// holder's uid is never named.
func errFirebaseEmailTaken() *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeAlreadyExists,
		"a Firebase account already holds this email; the change was not committed").
		WithDetails(map[string]any{"field": "email", "reason": "firebase_account_exists"})
}

// --- legacy claims (Appendix C §C.6.3) ----------------------------------------------------------------

// legacyClaimKeys are the claims legacy code reads (firestore.rules isWebAdmin, isCustomer, the partner
// read of mobile_installations, the maintenance driverId gate; lib/permissions.ts getRole and isAdmin).
// The bridge owns exactly these keys of an account's custom attributes; any other attribute is kept.
var legacyClaimKeys = []string{"admin", "role", "driverId", "customerScopeId", "partnerScopeId"}

// legacyContext is the principal context a legacy claim object describes: the active context of a Go
// session (custom tokens), or the user's default context (the account mirror).
type legacyContext struct {
	tenantID      *uuid.UUID
	role          authz.TenantRole
	platformAdmin bool
	dispatcher    bool
	customerScope bool
}

// legacyGrant is the claim object of a context and whether a custom token may carry it.
type legacyGrant struct {
	claims map[string]any // nil: no legacy shape; the mirror clears the legacy keys
	mint   bool
}

var ownFleetStaffRoles = []authz.TenantRole{authz.Manager, authz.OperationStaff, authz.Operator, authz.User}

// legacyClaims maps a context onto the C.6.3 table. Custom tokens (mint) exist only where Firestore
// exposure exists today: own-fleet staff, platform_admin, and users imported from a legacy partner or
// customer claim. A dispatcher, a carrier user created in Go and a customer user created in Go get no
// claims and no token: any signed-in Firebase user reads tasks, trip_records and drivers under the
// legacy rules (C.3.1). A driver's claims are written by the mirror only; drivers have no web access.
func legacyClaims(ctx context.Context, q *authdb.Queries, u authdb.GetBridgeUserRow, lc legacyContext) (legacyGrant, error) {
	switch {
	case lc.platformAdmin:
		return legacyGrant{claims: map[string]any{"admin": true, "role": "admin"}, mint: true}, nil
	case lc.dispatcher:
		return legacyGrant{}, nil
	}
	if lc.tenantID != nil {
		t, err := q.GetTenantLegacy(ctx, *lc.tenantID)
		if err != nil {
			return legacyGrant{}, err
		}
		switch {
		case lc.role == authz.Driver:
			c := map[string]any{"admin": false, "role": "driver"}
			ref, err := q.GetDriverLegacyRef(ctx, u.ID)
			switch {
			case err == nil:
				c["driverId"] = ref
			case !errors.Is(err, pgx.ErrNoRows):
				return legacyGrant{}, err
			}
			return legacyGrant{claims: c}, nil
		case t.Kind == "own_fleet" && lc.role == authz.TenantAdmin:
			return legacyGrant{claims: map[string]any{"admin": true, "role": "admin"}, mint: true}, nil
		case t.Kind == "own_fleet" && slices.Contains(ownFleetStaffRoles, lc.role):
			return legacyGrant{claims: map[string]any{"admin": false, "role": string(lc.role)}, mint: true}, nil
		case t.Kind == "carrier" && lc.role == authz.TenantAdmin && u.Imported && t.LegacyDocID != nil:
			return legacyGrant{claims: map[string]any{"admin": false, "role": "partner", "partnerScopeId": *t.LegacyDocID}, mint: true}, nil
		}
		return legacyGrant{}, nil
	}
	if lc.customerScope && u.Imported {
		id, err := q.OldestCustomerScopeLegacyID(ctx, u.ID)
		switch {
		case err == nil:
			return legacyGrant{claims: map[string]any{"admin": false, "role": "customer", "customerScopeId": id}, mint: true}, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return legacyGrant{}, err
		}
	}
	return legacyGrant{}, nil
}

// principalContext is the active context of a Go session (its access token's claims).
func principalContext(p *authz.Principal) legacyContext {
	return legacyContext{
		tenantID: p.TenantID, role: p.TenantRole, platformAdmin: p.HasPlatform(authz.PlatformAdmin),
		dispatcher: p.Dispatcher, customerScope: p.TenantID == nil && len(p.PartyIDs) > 0,
	}
}

// defaultContext is the context a new session of the user would start in without a preference: the
// first usable membership (own fleet first), the platform role, the scopes.
func defaultContext(a axes) legacyContext {
	lc := legacyContext{platformAdmin: slices.Contains(a.platform, string(authz.PlatformAdmin))}
	if m := a.pickTenant(nil); m != nil {
		tid := m.TenantID
		lc.tenantID, lc.role = &tid, authz.TenantRole(m.Role)
	}
	for _, sc := range a.scopes {
		switch sc.Kind {
		case "dispatcher":
			lc.dispatcher = true
		case "customer":
			lc.customerScope = true
		}
	}
	return lc
}

// mergeAttributes replaces the legacy keys of an account's custom attributes with claims and keeps
// every other attribute, so a mirror write never drops an unrelated claim (fixes the overwrites of
// functions/src/triggers.ts:121-125, 236-240). Undecodable attributes are replaced.
func mergeAttributes(existing string, claims map[string]any) (string, error) {
	m := map[string]any{}
	if existing != "" {
		if err := json.Unmarshal([]byte(existing), &m); err != nil || m == nil {
			m = map[string]any{}
		}
	}
	for _, k := range legacyClaimKeys {
		delete(m, k)
	}
	maps.Copy(m, claims)
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if len(b) > firebase.MaxClaimsLength {
		return "", fmt.Errorf("auth: merged Firebase custom attributes exceed %d bytes", firebase.MaxClaimsLength)
	}
	return string(b), nil
}
