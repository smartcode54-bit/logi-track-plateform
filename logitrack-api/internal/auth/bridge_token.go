package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// FirebaseToken is the 200 body of POST /v1/bridge/firebase-token.
type FirebaseToken struct {
	CustomToken string `json:"customToken"`
	ExpiresIn   int    `json:"expiresIn"`
}

// FirebaseCustomToken is POST /v1/bridge/firebase-token (internal listener only; called by the BFF route
// POST /api/auth/firebase-token, R9, R40; Appendix C §C.6.3). It answers 404 unless the bridge mode
// includes web, and otherwise mints a Firebase custom token for the caller's Firebase uid
// (users.legacy_auth_uid) carrying the legacy claims of the caller's active context, so pages that still
// read Firestore keep working under firestore.rules until TW7. A principal outside the minting rule
// (dispatcher, driver, carrier or customer user created in Go, support-only) is 403 permission_denied.
// An own-fleet user or platform admin created in Go gets users.id as its Firebase uid at the first mint;
// Firebase creates the account at the first signInWithCustomToken.
func (s *Service) FirebaseCustomToken(ctx context.Context, p *authz.Principal) (*FirebaseToken, error) {
	if !s.fb.Mode.Web() {
		return nil, httpx.ErrNotFound()
	}
	var uid string
	var claims map[string]any
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		u, err := q.GetBridgeUser(ctx, p.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken()
		}
		if err != nil {
			return err
		}
		if u.Status != "active" && u.Status != "reset_required" {
			return errAccountDisabled() // its sessions are revoked already; defensive
		}
		g, err := legacyClaims(ctx, q, u, principalContext(p))
		if err != nil {
			return err
		}
		if !g.mint {
			return errPermissionDenied("no Firestore session for this principal (Appendix C §C.6.3)")
		}
		claims = g.claims
		uid, err = firebaseUID(ctx, tx, q, u)
		return err
	})
	if err != nil {
		return nil, err
	}
	tok, err := s.fb.Signer.CustomToken(uid, claims, s.now())
	if err != nil {
		return nil, err
	}
	s.customTokens.Inc()
	return &FirebaseToken{CustomToken: tok, ExpiresIn: int(firebase.CustomTokenTTL.Seconds())}, nil
}

// firebaseUID returns the user's Firebase uid, giving a user created in Go its users.id first (C.6.3,
// C.6.4). A concurrent request that assigned it first leaves no row to update; the uid it wrote is read
// back (read committed: the next statement sees it). A uid assigned here is followed by driver.updated for
// the user's driver (driverLoginChanged).
func firebaseUID(ctx context.Context, tx pgx.Tx, q *authdb.Queries, u authdb.GetBridgeUserRow) (string, error) {
	if u.LegacyAuthUid != nil {
		return *u.LegacyAuthUid, nil
	}
	uid, err := q.AssignLegacyAuthUID(ctx, u.ID)
	if err == nil {
		return uid, driverLoginChanged(ctx, tx, q, u.ID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uid, err
	}
	again, err := q.GetBridgeUser(ctx, u.ID)
	if err != nil {
		return "", err
	}
	if again.LegacyAuthUid == nil {
		return "", errors.New("auth: Firebase uid neither assigned nor present")
	}
	return *again.LegacyAuthUid, nil
}

// driverLoginChanged queues driver.updated (changed: login; topic tenant:{tid}:fleet, Appendix B §B.5.2) for the
// driver linked to user, if any, in the transaction that gave the user its Firebase uid: the projected drivers
// document carries that uid as authId, by which the installed 3.x app finds its driver (internal/etl
// encodeDrivers), so firestore.shadow projects the driver again.
func driverLoginChanged(ctx context.Context, tx pgx.Tx, q *authdb.Queries, user uuid.UUID) error {
	d, err := q.DriverOfLogin(ctx, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return insertOutbox(ctx, tx, outboxEvent{RoutingKey: routeDriverUpdated, AggregateType: "driver", AggregateID: d.ID.String(),
		TenantID: &d.TenantID, Payload: map[string]any{"entity": "driver", "id": d.ID.String(), "changed": []string{"login"}},
		Topics: []string{realtime.TenantTopic(d.TenantID.String(), realtime.FamilyFleet)}})
}

// routeDriverUpdated is the outbox key of a driver change (Appendix B §B.5.2; internal/drivers and internal/iam emit
// it for their writes).
const routeDriverUpdated = "driver.updated"

// mountBridge registers /v1/bridge (internal only). Like the other mount functions it never reads s
// while registering: the mode is checked per request.
func (s *Service) mountBridge(r fiber.Router) {
	r.Use(noStore, s.requireWebBridge)
	r.Post("/firebase-token", s.RequireAuth(), s.handleFirebaseToken)
}

// requireWebBridge answers 404 for every /v1/bridge path unless the mode includes web, before any
// credential is looked at: the route does not exist in the other modes.
func (s *Service) requireWebBridge(c fiber.Ctx) error {
	if !s.fb.Mode.Web() {
		return httpx.ErrNotFound()
	}
	return c.Next()
}

func (s *Service) handleFirebaseToken(c fiber.Ctx) error {
	res, err := s.FirebaseCustomToken(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}
