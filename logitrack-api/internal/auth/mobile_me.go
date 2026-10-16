package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// MobileMe is the body of GET /v1/mobile/me (Appendix B §B.2.21): GET /v1/me, whose driver also carries the
// driver's tenant and its Firestore drivers doc id. Driver shadows Me.Driver (encoding/json takes the shallower
// field), so the key "driver" appears once.
type MobileMe struct {
	*Me
	Driver *MobileDriver `json:"driver"`
}

// MobileDriver is the linked driver of a driver principal on GET /v1/mobile/me. legacyDocId is the Firestore drivers
// doc id, or the uuid of a driver created in Go (its write-back doc id, R25): the driverId custom claim the
// setDriverClaims shim merges (C.6.3, C.6.5).
type MobileDriver struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenantId"`
	LegacyDocID string    `json:"legacyDocId"`
}

// MobileMe is GET /v1/mobile/me for p: GET /v1/me plus the driver's tenant and Firestore doc id (driver null for a
// principal without a linked driver).
func (s *Service) MobileMe(ctx context.Context, p *authz.Principal) (*MobileMe, error) {
	if authz.IsSupport(p) || p.AccessMode == AccessModeSupport {
		return nil, errPermissionDenied("support has no mobile context")
	}
	me, err := s.Me(ctx, p)
	if err != nil {
		return nil, err
	}
	out := &MobileMe{Me: me}
	if p.DriverID == nil || p.TenantID == nil {
		return out, nil
	}
	err = s.system(ctx, func(q *authdb.Queries) error {
		ref, err := q.GetDriverLegacyRef(ctx, p.UserID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil // unlinked since the principal was built: no driver
		case err != nil:
			return err
		}
		out.Driver = &MobileDriver{ID: *p.DriverID, TenantID: *p.TenantID, LegacyDocID: ref}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HandleMobileMe serves GET /v1/mobile/me behind the guard app.MobileGroups gives it (the setDriverClaims shim target,
// auth.RequireShim, T32; APK 4.x's Go tokens join with T55). Never cached: it carries the caller's identity.
func (s *Service) HandleMobileMe(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	me, err := s.MobileMe(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, me)
}
