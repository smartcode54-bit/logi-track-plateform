//go:build integration

// Firebase bridge (T08, Appendix C §C.6, test matrix §C.9.5) against PostgreSQL 18, Redis 7 and an
// in-process stand-in for Google (firebasetest): the securetoken keys, the OAuth2 token endpoint and the
// Identity Toolkit account table never leave the test process.
package auth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

const fbProject = "logitrack-bridge-test"

// bridgeHarness is a harness whose bridge runs in mode against an in-process Google.
func bridgeHarness(t *testing.T, mode auth.BridgeMode) (*harness, *firebasetest.Backend) {
	t.Helper()
	b := firebasetest.New(t, fbProject)
	h := newHarnessFull(t, nil, func(now func() time.Time) auth.Firebase {
		fb := auth.Firebase{Mode: mode, Verifier: b.Verifier(now)}
		if mode != auth.BridgeOff {
			fb.Signer, fb.Accounts = b.ServiceAccount(), b.Accounts()
		}
		return fb
	})
	return h, b
}

// imported marks user as loaded from Firebase Auth, as cmd/etl auth-import writes it: the uid and the
// firebase_legacy identity.
func (h *harness) imported(user, fbuid string) {
	h.t.Helper()
	h.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, user, fbuid)
	h.exec(`INSERT INTO auth_identities (user_id, provider, provider_subject) VALUES ($1, 'firebase_legacy', $2)`, user, fbuid)
}

// customerScope gives user a customer scope on a new customer whose legacy doc id is legacyID ("" none).
func (h *harness) customerScope(user, code, legacyID string) {
	h.t.Helper()
	bp := h.party(code)
	if legacyID != "" {
		h.exec(`UPDATE customers SET legacy_doc_id = $2 WHERE id = (SELECT customer_id FROM billing_parties WHERE id = $1)`, bp, legacyID)
	}
	h.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, user, bp)
}

func (h *harness) mintToken(bearer string) resp {
	h.t.Helper()
	return h.post("/v1/bridge/firebase-token", bearer, nil)
}

// minted checks a 200 of the bridge endpoint and returns the custom token's uid and developer claims,
// after verifying it as Firebase would (RS256 with the service account key, aud, iss, iat, exp).
func (h *harness) minted(b *firebasetest.Backend, r resp) (string, map[string]any) {
	h.t.Helper()
	if r.status != http.StatusOK {
		h.t.Fatalf("mint: %d %s", r.status, r.raw)
	}
	if got := keys(r.data()); !slices.Equal(got, []string{"customToken", "expiresIn"}) || r.data()["expiresIn"] != float64(3600) {
		h.t.Fatalf("body = %s", r.raw)
	}
	if r.header.Get("Cache-Control") != "no-store" {
		h.t.Fatalf("Cache-Control = %q", r.header.Get("Cache-Control"))
	}
	_, c := firebasetest.ParseCustomToken(h.t, b.ServiceAccount(), r.str("customToken"), h.clock.Now())
	claims, _ := c["claims"].(map[string]any)
	uid, _ := c["uid"].(string)
	return uid, claims
}

func sameClaims(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("claims = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("claims = %v, want %v", got, want)
		}
	}
}

// C.6.3 table: the custom token carries the legacy claims of the caller's active context, for the uid
// of its Firebase account; an own-fleet user created in Go gets users.id as its uid at the first mint.
func TestBridgeCustomTokenClaims(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeWeb)
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Partner Co")
	h.exec(`UPDATE tenants SET legacy_doc_id = 'subDocPartner1' WHERE id = $1`, carrier)

	admin := h.user("admin@logitrack.test")
	h.member(admin, own, "tenant_admin")
	h.imported(admin, "fbAdminUid0000000000000000001")

	mgr := h.user("manager@logitrack.test") // created in Go: no Firebase uid yet
	h.member(mgr, own, "manager")

	plat := h.user("platform@logitrack.test") // platform admin, no tenant
	h.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, plat)
	h.imported(plat, "fbPlatformUid000000000000001")

	partner := h.user("partner@logitrack.test")
	h.member(partner, carrier, "tenant_admin")
	h.imported(partner, "fbPartnerUid0000000000000001")

	cust := h.user("customer@logitrack.test")
	h.customerScope(cust, "CJSF", "custDocCJSF1")
	h.customerScope(cust, "LATE", "custDocLater")
	h.imported(cust, "fbCustomerUid000000000000001")

	cases := []struct {
		email, uid string
		claims     map[string]any
	}{
		{"admin@logitrack.test", "fbAdminUid0000000000000000001", map[string]any{"admin": true, "role": "admin"}},
		{"manager@logitrack.test", mgr, map[string]any{"admin": false, "role": "manager"}},
		{"platform@logitrack.test", "fbPlatformUid000000000000001", map[string]any{"admin": true, "role": "admin"}},
		{"partner@logitrack.test", "fbPartnerUid0000000000000001", map[string]any{"admin": false, "role": "partner", "partnerScopeId": "subDocPartner1"}},
		{"customer@logitrack.test", "fbCustomerUid000000000000001", map[string]any{"admin": false, "role": "customer", "customerScopeId": "custDocCJSF1"}},
	}
	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			s := h.mustLogin(tc.email, "web", "")
			uid, claims := h.minted(b, h.mintToken(s.access))
			if uid != tc.uid {
				t.Fatalf("uid = %q, want %q", uid, tc.uid)
			}
			sameClaims(t, claims, tc.claims)
			// GET /v1/me tells the web which Firebase uid the session belongs to.
			if me := h.get("/v1/me", s.access); me.str("legacyAuthUid") != tc.uid {
				t.Fatalf("legacyAuthUid = %v", me.data()["legacyAuthUid"])
			}
		})
	}
	if got := scalar[string](h, `SELECT legacy_auth_uid FROM users WHERE id = $1`, mgr); got != mgr {
		t.Fatalf("legacy_auth_uid of a Go-created user = %q, want its id", got)
	}
	if n := scalar[int](h, `SELECT count(*) FROM auth_identities WHERE user_id = $1`, mgr); n != 0 {
		t.Fatal("a Go-created user became 'imported'")
	}
	if len(b.Calls()) != 0 {
		t.Fatal("minting a custom token called Identity Toolkit")
	}
}

// AC: a dispatcher and a carrier user created in Go get 403 from the custom-token endpoint (and so do
// a customer user created in Go, a driver and a support-only platform user).
func TestBridgeCustomTokenRefused(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeWeb)
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier Go")
	h.exec(`UPDATE tenants SET legacy_doc_id = 'subDocCarrier' WHERE id = $1`, carrier)
	ttp := h.tenant("carrier", "TTP Dispatch")

	disp := h.user("dispatcher@logitrack.test")
	h.member(disp, ttp, "tenant_admin")
	h.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2)`, disp, h.party("TTP"))
	h.imported(disp, "fbDispatcher000000000000001") // even an imported dispatcher

	carrierAdmin := h.user("carrier.admin@logitrack.test") // created in Go
	h.member(carrierAdmin, carrier, "tenant_admin")
	carrierOps := h.user("carrier.ops@logitrack.test")
	h.member(carrierOps, carrier, "operator")
	h.imported(carrierOps, "fbCarrierOps000000000000001") // imported, but never a partner claim shape

	cust := h.user("customer.go@logitrack.test") // created in Go
	h.customerScope(cust, "CNEW", "custDocNew")

	drv := h.user("driver@logitrack.test")
	h.member(drv, own, "driver")
	h.driver(drv, own, "0812345678")
	h.imported(drv, "fbDriverUid0000000000000001")

	sup := h.user("support@logitrack.test")
	h.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'support')`, sup)

	for _, email := range []string{"dispatcher@logitrack.test", "carrier.admin@logitrack.test", "carrier.ops@logitrack.test",
		"customer.go@logitrack.test", "driver@logitrack.test", "support@logitrack.test"} {
		t.Run(email, func(t *testing.T) {
			s := h.mustLogin(email, "web", "")
			expectError(t, h.mintToken(s.access), http.StatusForbidden, auth.CodePermissionDenied)
		})
	}
	if n := scalar[int](h, `SELECT count(*) FROM users WHERE legacy_auth_uid = id::text`); n != 0 {
		t.Fatal("a refused principal was given a Firebase uid")
	}
	if len(b.Calls()) != 0 {
		t.Fatal("Identity Toolkit called")
	}
}

// AC: POST /v1/bridge/firebase-token is 404 on the public listener, and 404 on the internal one unless
// the mode includes web (before any credential is looked at). A Firebase ID token is never a bearer.
func TestBridgeEndpointGates(t *testing.T) {
	for _, mode := range auth.BridgeModes {
		t.Run(string(mode), func(t *testing.T) {
			h, b := bridgeHarness(t, mode)
			own := h.tenant("own_fleet", "Own")
			u := h.user("ops@logitrack.test")
			h.member(u, own, "operator")
			h.imported(u, "fbOpsUid00000000000000000001")
			s := h.mustLogin("ops@logitrack.test", "web", "")

			expectError(t, h.call(h.public, http.MethodPost, "/v1/bridge/firebase-token", s.access, nil), http.StatusNotFound, httpx.CodeNotFound)
			if mode.Web() {
				h.minted(b, h.mintToken(s.access))
				expectError(t, h.mintToken(""), http.StatusUnauthorized, httpx.CodeUnauthenticated)
			} else {
				expectError(t, h.mintToken(s.access), http.StatusNotFound, httpx.CodeNotFound)
				expectError(t, h.mintToken(""), http.StatusNotFound, httpx.CodeNotFound)
			}
			// A Firebase ID token alone as a bearer is invalid_token everywhere (C.6.2).
			fb := b.SignIDToken(b.IDTokenClaims("fbOpsUid00000000000000000001", h.clock.Now().Add(-time.Minute), h.clock.Now()))
			expectError(t, h.get("/v1/me", fb), http.StatusUnauthorized, auth.CodeInvalidToken)
			if mode.Web() {
				expectError(t, h.mintToken(fb), http.StatusUnauthorized, auth.CodeInvalidToken)
			}
			if me := h.get("/v1/me", s.access); !mode.Web() && me.data()["legacyAuthUid"] != nil {
				t.Fatalf("legacyAuthUid outside the web bridge: %s", me.raw)
			}
		})
	}
}

// AC: a Firebase ID token maps to the right user / driver principal, built from PostgreSQL; mode off
// rejects the APK direction (404) while the shim direction works in every mode (R45).
func TestFirebaseIDTokenPrincipal(t *testing.T) {
	for _, mode := range auth.BridgeModes {
		t.Run(string(mode), func(t *testing.T) {
			h, b := bridgeHarness(t, mode)
			ctx := context.Background()
			own := h.tenant("own_fleet", "Own")
			carrier := h.tenant("carrier", "Carrier")
			drvUser := h.user("driver@logitrack.test")
			h.member(drvUser, carrier, "driver")
			drv := h.driver(drvUser, carrier, "0812345678")
			h.imported(drvUser, "fbDriverUid0000000000000001")
			ops := h.user("ops@logitrack.test")
			h.member(ops, own, "operation_staff")
			h.imported(ops, "fbOpsUid00000000000000000001")

			now := h.clock.Now()
			tok := func(uid string, edit func(map[string]any)) string {
				c := b.IDTokenClaims(uid, now.Add(-10*time.Minute), now)
				if edit != nil {
					edit(c)
				}
				return b.SignIDToken(c)
			}
			// The token's custom claims say admin; the principal comes from PostgreSQL only.
			drvTok := tok("fbDriverUid0000000000000001", func(c map[string]any) { c["admin"] = true; c["role"] = "admin" })

			_, err := h.svc.VerifyFirebaseIDToken(ctx, drvTok, auth.FirebaseAPK)
			if !mode.Mobile() {
				if he, ok := errors.AsType[*httpx.Error](err); !ok || he.Status != http.StatusNotFound {
					t.Fatalf("APK token in mode %s: %v", mode, err)
				}
			} else if err != nil {
				t.Fatalf("APK token: %v", err)
			}

			id, err := h.svc.VerifyFirebaseIDToken(ctx, drvTok, auth.FirebaseShim)
			if err != nil {
				t.Fatal(err)
			}
			p := id.Principal
			if p.UserID.String() != drvUser || p.TenantID == nil || p.TenantID.String() != carrier || p.TenantRole != authz.Driver ||
				p.DriverID == nil || p.DriverID.String() != drv || p.AMR != auth.AMRFirebase || p.SessionID != uuid.Nil ||
				len(p.Platform) != 0 || id.UID != "fbDriverUid0000000000000001" || id.SignInProvider != "password" {
				t.Fatalf("driver principal = %+v (%+v)", p, id)
			}
			id, err = h.svc.VerifyFirebaseIDToken(ctx, tok("fbOpsUid00000000000000000001", nil), auth.FirebaseShim)
			if err != nil {
				t.Fatal(err)
			}
			if id.Principal.UserID.String() != ops || id.Principal.TenantRole != authz.OperationStaff || id.Principal.DriverID != nil {
				t.Fatalf("staff principal = %+v", id.Principal)
			}
		})
	}
}

func expectHTTPError(t *testing.T, err error, status int, code string) {
	t.Helper()
	he, ok := errors.AsType[*httpx.Error](err)
	if !ok || he.Status != status || he.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

// C.6.2: refusals of a Firebase ID token.
func TestFirebaseIDTokenRefusals(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeMobile)
	ctx := context.Background()
	own := h.tenant("own_fleet", "Own")
	u := h.user("ops@logitrack.test")
	h.member(u, own, "operator")
	h.imported(u, "fbOpsUid00000000000000000001")
	now := h.clock.Now()
	tok := func(uid string, authTime time.Time, edit func(map[string]any)) string {
		c := b.IDTokenClaims(uid, authTime, now)
		if edit != nil {
			edit(c)
		}
		return b.SignIDToken(c)
	}
	verify := func(raw string) error {
		_, err := h.svc.VerifyFirebaseIDToken(ctx, raw, auth.FirebaseAPK)
		return err
	}
	good := tok("fbOpsUid00000000000000000001", now.Add(-time.Hour), nil)
	if err := verify(good); err != nil {
		t.Fatal(err)
	}

	expectHTTPError(t, verify(tok("fbUnknown", now.Add(-time.Hour), nil)), http.StatusForbidden, auth.CodeNoAccount)
	expectHTTPError(t, verify(tok("fbOpsUid00000000000000000001", now.Add(-time.Hour), func(c map[string]any) { c["aud"] = "another-project" })),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	expectHTTPError(t, verify(tok("fbOpsUid00000000000000000001", now.Add(-time.Hour), func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() })),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	expectHTTPError(t, verify(""), http.StatusUnauthorized, auth.CodeInvalidToken)

	// A Firebase session older than the last password change is refused (whole seconds).
	h.exec(`UPDATE users SET password_changed_at = $2 WHERE id = $1`, u, now.Add(-30*time.Minute))
	expectHTTPError(t, verify(good), http.StatusUnauthorized, auth.CodeInvalidToken)
	if err := verify(tok("fbOpsUid00000000000000000001", now.Add(-30*time.Minute).Truncate(time.Second), nil)); err != nil {
		t.Fatalf("a sign-in in the second of the change: %v", err)
	}
	fresh := tok("fbOpsUid00000000000000000001", now.Add(-time.Minute), nil)

	// A disabled user is refused at once, although Firebase would still accept its token for an hour.
	h.exec(`UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, u)
	expectHTTPError(t, verify(fresh), http.StatusForbidden, auth.CodeAccountDisabled)
	h.exec(`UPDATE users SET status = 'active', disabled_at = NULL WHERE id = $1`, u)

	// must_change_password resolves and says so (the exchange answers it with a ticket, R79).
	h.exec(`UPDATE users SET must_change_password = true WHERE id = $1`, u)
	if id, err := h.svc.VerifyFirebaseIDToken(ctx, fresh, auth.FirebaseAPK); err != nil || !id.MustChangePassword {
		t.Fatalf("must change: %+v %v", id, err)
	}

	// The cached uid -> user hint is checked against the row: a uid moved to another user resolves to it.
	other := h.user("other@logitrack.test")
	h.member(other, own, "user")
	h.exec(`UPDATE users SET legacy_auth_uid = NULL WHERE id = $1`, u)
	h.exec(`UPDATE users SET legacy_auth_uid = 'fbOpsUid00000000000000000001' WHERE id = $1`, other)
	id, err := h.svc.VerifyFirebaseIDToken(ctx, fresh, auth.FirebaseAPK)
	if err != nil || id.Principal.UserID.String() != other {
		t.Fatalf("stale hint followed: %+v %v", id, err)
	}

	// Keys that cannot be fetched: 503, never a judgement of the token.
	b.SetDown(true)
	unknownKid := firebasetest.SignWith(t, firebasetest.NewKey(t), "rotated-key", "RS256", b.IDTokenClaims("fbOpsUid00000000000000000001", now, now))
	expectHTTPError(t, verify(unknownKid), http.StatusServiceUnavailable, httpx.CodeUnavailable)
	b.SetDown(false)
}

// asSystem runs fn in a WithSystem transaction as internal/iam (T19) will, then applies the post-commit
// writes of a committed change.
func (h *harness) asSystem(fn func(tx pgx.Tx) (*auth.PostCommit, error)) error {
	h.t.Helper()
	ctx := context.Background()
	var pc *auth.PostCommit
	err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		var err error
		pc, err = fn(tx)
		return err
	})
	if err == nil {
		h.svc.Apply(ctx, pc)
	}
	return err
}

func (h *harness) mirrorFailures() float64 {
	h.t.Helper()
	reg := prometheus.NewRegistry()
	if err := h.svc.Register(reg); err != nil {
		h.t.Fatal(err)
	}
	return counterValue(h.t, reg, "auth_firebase_mirror_failures_total")
}

// AC: disabling a user in Go disables the Firebase user and revokes its refresh tokens; enabling it
// re-enables the account; a mirror failure commits nothing (503 bridge_unavailable).
func TestBridgeMirrorDisable(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeWeb)
	own := h.tenant("own_fleet", "Own")
	admin := h.user("admin@logitrack.test")
	h.member(admin, own, "tenant_admin")
	adminID := uuid.MustParse(admin)
	drvUser := h.user("driver@logitrack.test")
	h.member(drvUser, own, "driver")
	h.driver(drvUser, own, "0812345678")
	h.imported(drvUser, "fbDriverUid0000000000000001")
	b.Put(firebasetest.Account{UID: "fbDriverUid0000000000000001", Email: "driver@logitrack.test", Password: pw})
	uid := uuid.MustParse(drvUser)
	s := h.mustLogin("driver@logitrack.test", "android", "install-1")

	// A failing mirror: nothing changes in PostgreSQL, the session lives on.
	b.SetToolkitError(http.StatusInternalServerError, "INTERNAL_ERROR")
	err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: uid, Disabled: true, ActorID: &adminID, RequestID: "req-1"})
	})
	expectHTTPError(t, err, http.StatusServiceUnavailable, auth.CodeBridgeUnavailable)
	b.SetToolkitError(0, "")
	if st := scalar[string](h, `SELECT status FROM users WHERE id = $1`, drvUser); st != "active" {
		t.Fatalf("status after a failed mirror = %s", st)
	}
	if r := h.get("/v1/me", s.access); r.status != http.StatusOK {
		t.Fatalf("session ended by a rolled-back disable: %d", r.status)
	}
	if acc, _ := b.Get("fbDriverUid0000000000000001"); acc.Disabled || acc.ValidSince != 0 {
		t.Fatalf("Firebase changed: %+v", acc)
	}
	if h.mirrorFailures() != 1 {
		t.Fatalf("mirror failures = %v", h.mirrorFailures())
	}

	before := h.clock.Now().Unix()
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: uid, Disabled: true, ActorID: &adminID, RequestID: "req-2"})
	}); err != nil {
		t.Fatal(err)
	}
	acc, _ := b.Get("fbDriverUid0000000000000001")
	if !acc.Disabled || acc.ValidSince < before {
		t.Fatalf("Firebase account after disable = %+v (want disabled, validSince >= %d)", acc, before)
	}
	if st := scalar[string](h, `SELECT status FROM users WHERE id = $1`, drvUser); st != "disabled" {
		t.Fatalf("status = %s", st)
	}
	expectError(t, h.get("/v1/me", s.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	if n := scalar[int](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_reason = 'disabled' AND revoked_by = $2`, drvUser, admin); n != 1 {
		t.Fatalf("revoked sessions = %d", n)
	}

	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: uid, Disabled: false, ActorID: &adminID})
	}); err != nil {
		t.Fatal(err)
	}
	if acc, _ := b.Get("fbDriverUid0000000000000001"); acc.Disabled {
		t.Fatal("Firebase account still disabled")
	}
	if st := scalar[string](h, `SELECT status FROM users WHERE id = $1`, drvUser); st != "active" {
		t.Fatalf("status = %s", st)
	}
	h.mustLogin("driver@logitrack.test", "android", "install-1")

	// Unknown users are 404; a user without a Firebase account changes in PostgreSQL only.
	err = h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: uuid.New(), Disabled: true})
	})
	expectHTTPError(t, err, http.StatusNotFound, httpx.CodeNotFound)
	n := len(b.Calls())
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: adminID, Disabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	if len(b.Calls()) != n {
		t.Fatal("a user without a Firebase uid was mirrored")
	}
}

// Mode off writes nothing to Firebase, whatever changes.
func TestBridgeMirrorOffWritesNothing(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeOff)
	own := h.tenant("own_fleet", "Own")
	u := h.user("ops@logitrack.test")
	h.member(u, own, "operator")
	h.imported(u, "fbOpsUid00000000000000000001")
	b.Put(firebasetest.Account{UID: "fbOpsUid00000000000000000001"})
	s := h.mustLogin("ops@logitrack.test", "web", "")
	if r := h.post("/v1/auth/password/change", s.access, map[string]any{"currentPassword": pw, "newPassword": "another long passphrase 2"}); r.status != http.StatusNoContent {
		t.Fatalf("change: %d %s", r.status, r.raw)
	}
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetStatusInTx(context.Background(), tx, auth.StatusChange{UserID: uuid.MustParse(u), Disabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	if len(b.Calls()) != 0 || b.TokenRequests() != 0 {
		t.Fatalf("mode off reached Google: %d calls", len(b.Calls()))
	}
}

// Password events reach Firebase with validSince; a failing mirror is 503 bridge_unavailable and the
// old password keeps working in both stores.
func TestBridgeMirrorPasswords(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeMobile)
	own := h.tenant("own_fleet", "Own")
	u := h.user("driver@logitrack.test")
	h.member(u, own, "driver")
	h.driver(u, own, "0812345678")
	h.imported(u, "fbDriverUid0000000000000001")
	b.Put(firebasetest.Account{UID: "fbDriverUid0000000000000001", Password: pw})
	s := h.mustLogin("driver@logitrack.test", "web", "")

	b.SetToolkitError(http.StatusServiceUnavailable, "UNAVAILABLE")
	r := h.post("/v1/auth/password/change", s.access, map[string]any{"currentPassword": pw, "newPassword": "another long passphrase 2"})
	expectError(t, r, http.StatusServiceUnavailable, auth.CodeBridgeUnavailable)
	b.SetToolkitError(0, "")
	if acc, _ := b.Get("fbDriverUid0000000000000001"); acc.Password != pw {
		t.Fatal("Firebase password changed by a failed request")
	}
	h.mustLogin("driver@logitrack.test", "web", "") // the old password still signs in

	before := h.clock.Now().Unix()
	if r := h.post("/v1/auth/password/change", s.access, map[string]any{"currentPassword": pw, "newPassword": "another long passphrase 2"}); r.status != http.StatusNoContent {
		t.Fatalf("change: %d %s", r.status, r.raw)
	}
	acc, _ := b.Get("fbDriverUid0000000000000001")
	if acc.Password != "another long passphrase 2" || acc.ValidSince < before {
		t.Fatalf("Firebase after change = %+v", acc)
	}

	// Temporary password (R29): the same password in Firebase, must change at the next Go sign-in.
	plain, hash, err := h.svc.NewTemporaryPassword(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetTemporaryPasswordInTx(context.Background(), tx, auth.TemporaryPassword{UserID: uuid.MustParse(u), Password: plain, Hash: hash, RequestID: "req-t"})
	}); err != nil {
		t.Fatal(err)
	}
	if acc, _ := b.Get("fbDriverUid0000000000000001"); acc.Password != plain {
		t.Fatal("temporary password not mirrored")
	}
	expectError(t, h.login("driver@logitrack.test", plain, "web", ""), http.StatusForbidden, auth.CodePasswordChangeRequired)
	if n := scalar[int](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, u); n != 0 {
		t.Fatalf("live sessions after a temporary password = %d", n)
	}

	// A legacy uid Firebase does not know (the account was never created) is not an error.
	h.exec(`UPDATE users SET legacy_auth_uid = 'fbNeverCreated' WHERE id = $1`, u)
	plain2, hash2, _ := h.svc.NewTemporaryPassword(context.Background())
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return h.svc.SetTemporaryPasswordInTx(context.Background(), tx, auth.TemporaryPassword{UserID: uuid.MustParse(u), Password: plain2, Hash: hash2})
	}); err != nil {
		t.Fatalf("missing Firebase account: %v", err)
	}
}

// Claims changes merge the recomputed legacy claim object into the account's custom attributes (keeping
// unrelated attributes); an admin revocation of every session sets validSince; revoking one session
// leaves Firebase alone.
func TestBridgeMirrorClaimsAndRevoke(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeWeb)
	ctx := context.Background()
	own := h.tenant("own_fleet", "Own")
	u := h.user("ops@logitrack.test")
	h.member(u, own, "operator")
	h.imported(u, "fbOpsUid00000000000000000001")
	b.Put(firebasetest.Account{UID: "fbOpsUid00000000000000000001",
		CustomAttributes: `{"admin":false,"role":"driver","driverId":"staleDriverDoc","companyId":"keep-me"}`})
	uid := uuid.MustParse(u)
	s1 := h.mustLogin("ops@logitrack.test", "web", "")
	s2 := h.mustLogin("ops@logitrack.test", "android", "install-2")

	// The role change is written first, then RevokeInTx(claims_changed) mirrors it (as T19 will).
	change := func() error {
		return h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
			if _, err := tx.Exec(ctx, `UPDATE memberships SET role = 'manager' WHERE user_id = $1`, u); err != nil {
				return nil, err
			}
			pc, _, err := h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uid, Reason: auth.RevokeClaimsChanged, BumpVersion: true})
			return pc, err
		})
	}
	b.SetToolkitError(http.StatusInternalServerError, "INTERNAL_ERROR")
	expectHTTPError(t, change(), http.StatusServiceUnavailable, auth.CodeBridgeUnavailable)
	b.SetToolkitError(0, "")
	if role := scalar[string](h, `SELECT role FROM memberships WHERE user_id = $1`, u); role != "operator" {
		t.Fatalf("role after a failed mirror = %s", role)
	}
	if err := change(); err != nil {
		t.Fatal(err)
	}
	acc, _ := b.Get("fbOpsUid00000000000000000001")
	if acc.CustomAttributes != `{"admin":false,"companyId":"keep-me","role":"manager"}` {
		t.Fatalf("custom attributes = %s", acc.CustomAttributes)
	}
	if acc.ValidSince != 0 {
		t.Fatal("a claims change revoked Firebase sessions")
	}

	// One session revoked by an admin: Go only.
	n := len(b.Calls())
	sid := uuid.MustParse(s1.sid)
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		pc, _, err := h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uid, Reason: auth.RevokeAdmin, SessionIDs: []uuid.UUID{sid}})
		return pc, err
	}); err != nil {
		t.Fatal(err)
	}
	if len(b.Calls()) != n {
		t.Fatal("revoking one session wrote Firebase")
	}
	// Every session: validSince.
	before := h.clock.Now().Unix()
	if err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		pc, _, err := h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uid, Reason: auth.RevokeAdmin, BumpVersion: true})
		return pc, err
	}); err != nil {
		t.Fatal(err)
	}
	if acc, _ := b.Get("fbOpsUid00000000000000000001"); acc.ValidSince < before {
		t.Fatalf("validSince = %d, want >= %d", acc.ValidSince, before)
	}
	expectError(t, h.get("/v1/me", s2.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
}

// C.6.4 creation: a driver or own-fleet staff user created in Go gets a Firebase account under its id
// with the initial password and its legacy claims; a carrier user does not; a retry after a failed
// commit converges on the existing account.
func TestBridgeMirrorNewUser(t *testing.T) {
	h, b := bridgeHarness(t, auth.BridgeMobile)
	ctx := context.Background()
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier")

	create := func(email, tenant, role, initial string) (string, error) {
		var id string
		err := h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
			if err := tx.QueryRow(ctx, `INSERT INTO users (email, email_verified, display_name) VALUES ($1::text, true, 'New User') RETURNING id::text`,
				email).Scan(&id); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, id, tenant, role); err != nil {
				return nil, err
			}
			if role == "driver" {
				if _, err := tx.Exec(ctx, `INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
					VALUES ($1, 'self', $2, 'ก', 'ข', '0899999999')`, tenant, id); err != nil {
					return nil, err
				}
			}
			return nil, h.svc.MirrorNewUserInTx(ctx, tx, uuid.MustParse(id), initial)
		})
		return id, err
	}

	id, err := create("new.driver@logitrack.test", carrier, "driver", "initial passphrase 9")
	if err != nil {
		t.Fatal(err)
	}
	acc, ok := b.Get(id)
	drvRef := scalar[string](h, `SELECT id::text FROM drivers WHERE user_id = $1`, id)
	if !ok || acc.Email != "new.driver@logitrack.test" || acc.Password != "initial passphrase 9" || acc.Disabled ||
		acc.CustomAttributes != `{"admin":false,"driverId":"`+drvRef+`","role":"driver"}` {
		t.Fatalf("created account = %+v", acc)
	}
	if got := scalar[string](h, `SELECT legacy_auth_uid FROM users WHERE id = $1`, id); got != id {
		t.Fatalf("legacy_auth_uid = %q", got)
	}

	staff, err := create("new.staff@logitrack.test", own, "operator", "")
	if err != nil {
		t.Fatal(err)
	}
	if acc, ok := b.Get(staff); !ok || acc.Password != "" || acc.CustomAttributes != `{"admin":false,"role":"operator"}` {
		t.Fatalf("staff account = %+v", acc)
	}

	carrierUser, err := create("carrier.user@logitrack.test", carrier, "operator", "initial passphrase 9")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Get(carrierUser); ok {
		t.Fatal("a carrier user created in Go got a Firebase account")
	}
	if n := scalar[int](h, `SELECT count(*) FROM users WHERE id = $1 AND legacy_auth_uid IS NULL`, carrierUser); n != 1 {
		t.Fatal("a carrier user got a Firebase uid")
	}

	// Firebase down: nothing committed. A retry whose account already exists updates it.
	b.SetDown(true)
	if _, err := create("down@logitrack.test", own, "user", "initial passphrase 9"); err == nil {
		t.Fatal("created while Firebase is down")
	}
	b.SetDown(false)
	if n := scalar[int](h, `SELECT count(*) FROM users WHERE email = 'down@logitrack.test'`); n != 0 {
		t.Fatal("user committed without its Firebase account")
	}
	err = h.asSystem(func(tx pgx.Tx) (*auth.PostCommit, error) {
		return nil, h.svc.MirrorNewUserInTx(ctx, tx, uuid.MustParse(staff), "a later passphrase 1")
	})
	if err != nil {
		t.Fatal(err)
	}
	if acc, _ := b.Get(staff); acc.Password != "a later passphrase 1" {
		t.Fatalf("existing account not updated: %+v", acc)
	}
}
