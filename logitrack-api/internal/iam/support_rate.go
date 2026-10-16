package iam

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// GCRA uses the database clock and a single row per actor/tenant so every replica shares the budget.
func (a *Admin) supportRate(c fiber.Ctx, bucket string, count int, tenant bool) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	subject := p.UserID.String()
	if tenant {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil || id == uuid.Nil {
			return httpx.ErrNotFound()
		}
		if !consentMember(p, id) {
			return authz.ErrPermissionDenied("consent requires an eligible member")
		}
		subject += ":" + id.String()
	}
	err := db.WithSystem(c.Context(), a.pool, nil, func(tx pgx.Tx) error {
		var at time.Time
		err := tx.QueryRow(c.Context(), `WITH moment AS MATERIALIZED (SELECT clock_timestamp() AS at)
   INSERT INTO support_request_limits(bucket,subject,theoretical_at)
   SELECT $1,$2,at+interval '1 minute'/$3 FROM moment
   ON CONFLICT(bucket,subject) DO UPDATE SET theoretical_at=greatest(support_request_limits.theoretical_at,(SELECT at FROM moment))+interval '1 minute'/$3
   WHERE support_request_limits.theoretical_at <= (SELECT at FROM moment)+interval '1 minute'/$3*($3-1)
   RETURNING theoretical_at`, bucket, subject, float64(count)).Scan(&at)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(429, "resource_exhausted", "rate limit exceeded")
		}
		return err
	})
	var e *httpx.Error
	if errors.As(err, &e) && e.Status == 429 {
		c.Set(fiber.HeaderRetryAfter, "60")
	}
	return err
}
