package drivers

import (
	"context"
	"errors"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

func (s *Service) supportDocument(c fiber.Ctx, p *authz.Principal, id uuid.UUID, kind string) error {
	if !p.SupportAccessEnabled {
		return httpx.NewError(403, httpx.CodeSupportAccessMasked, "support access is masked")
	}
	if kind != DocumentProfile && kind != DocumentIDCard && kind != DocumentLicense {
		return httpx.ErrNotFound()
	}
	var file *uuid.UUID
	all, tenants := p.TenantReach()
	if err := s.tx(c.Context(), p, func(_ pgx.Tx, q *driversdb.Queries) error {
		row, err := q.GetDriver(c.Context(), driversdb.GetDriverParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		switch kind {
		case DocumentProfile:
			file = row.Driver.ProfileFileID
		case DocumentIDCard:
			file = row.Driver.IDCardFileID
		default:
			file = row.Driver.LicenseFileID
		}
		return nil
	}); err != nil {
		return err
	}
	if file == nil {
		return httpx.ErrNotFound()
	}
	broker, ok := s.files.(interface {
		SupportFileSource(context.Context, *authz.Principal, httpx.ProjectionAudience, uuid.UUID, storage.FileRef) (httpx.StreamSource, error)
	})
	if !ok {
		return httpx.ErrSupportViewUnavailable()
	}
	purpose := map[string]string{DocumentProfile: "driver_profile", DocumentIDCard: "driver_id_card", DocumentLicense: "driver_license"}[kind]
	source, err := broker.SupportFileSource(c.Context(), p, httpx.ProjectionAudienceFrom(c), *file, storage.FileRef{ID: *file, OwnerID: id, Purpose: purpose})
	if err != nil {
		return err
	}
	return storage.StreamSupportSource(c, source)
}
