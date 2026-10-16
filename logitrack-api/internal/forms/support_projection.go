package forms

import (
	"context"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/forms/formsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

type WaitlistCounts struct {
	Total int64 `json:"total"`
}

var waitlistCountsSchema = httpx.NewSchema[WaitlistCounts]("forms.waitlist-counts", map[string]httpx.Field{"total": {Kind: httpx.Safe}})

func (s *Service) WaitlistCounts(ctx context.Context, p *authz.Principal) (WaitlistCounts, error) {
	var out WaitlistCounts
	if p == nil || (!p.Can(authz.WaitlistView) && !(p.IsPlatform() && authz.PlatformCaps(p).Has(authz.WaitlistView))) {
		return out, authz.ErrPermissionDenied("missing capability", authz.WaitlistView)
	}
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error { var err error; out.Total, err = formsdb.New(tx).CountWaitlist(ctx); return err })
	return out, err
}
func (h handlers) projection(c fiber.Ctx) error { return supportprojection.Middleware(h.s.pool)(c) }
func (h handlers) waitlistCounts(c fiber.Ctx) error {
	if _, err := httpx.StrictQuery(c); err != nil {
		return err
	}
	out, err := h.s.WaitlistCounts(c.Context(), authz.PrincipalFrom(c))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, 200, out)
}
